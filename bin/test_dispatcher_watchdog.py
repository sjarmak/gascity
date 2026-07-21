#!/usr/bin/env python3
"""Hermetic regression tests for dispatcher-watchdog conversion scoping."""

import json
import os
import subprocess
from pathlib import Path

import pytest


SCRIPT = Path(__file__).with_name("dispatcher-watchdog")
START = 2_000_000
LAST_ACTIVE = "1970-01-24T03:33:20Z"

FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path

path = Path(os.environ["FAKE_STATE"])
state = json.loads(path.read_text())
name = Path(sys.argv[0]).name
args = sys.argv[1:]

def save():
    path.write_text(json.dumps(state))

def ready(rows):
    return [row for row in rows
            if row.get("status") == "open"
            and not row.get("blocked_by")
            and row.get("issue_type") != "epic"]

if name == "bd":
    assert args[0] == "ready", args
    print(json.dumps(ready(state["stores"].get("city", []))))
    raise SystemExit(0)

if args[:2] == ["session", "list"]:
    print(json.dumps({"sessions": state["sessions"]}))
    raise SystemExit(0)
if args[:2] == ["bd", "--rig"]:
    rig = args[2]
    assert args[3] == "ready", args
    print(json.dumps(ready(state["stores"].get(rig, []))))
    raise SystemExit(0)
if args[:2] == ["session", "kill"]:
    state.setdefault("killed", []).append(args[2])
    save()
    raise SystemExit(0)
if args[:2] == ["mail", "send"]:
    state.setdefault("mail", []).append(args)
    save()
    raise SystemExit(0)
raise SystemExit(f"unexpected {name} args: {args}")
'''


def session(ident, target):
    rig = target.split("/", 1)[0] if "/" in target else None
    return {
        "id": ident,
        "name": target,
        "rig": rig,
        "state": "active",
        "last_active": LAST_ACTIVE,
    }


def bead(ident, target):
    return {
        "id": ident,
        "status": "open",
        "assignee": "",
        "metadata": {"gc.routed_to": target},
    }


@pytest.fixture
def watchdog(tmp_path):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    fake = bindir / "fake"
    fake.write_text(FAKE)
    fake.chmod(0o755)
    for name in ("gc", "bd"):
        (bindir / name).symlink_to(fake)

    city = tmp_path / "city"
    (city / ".gc").mkdir(parents=True)
    runtime = tmp_path / "runtime.json"
    runtime.write_text(json.dumps({"sessions": [], "stores": {}, "killed": []}))
    env = {
        **os.environ,
        "PATH": f"{bindir}:{os.environ['PATH']}",
        "FAKE_STATE": str(runtime),
        "DISPATCHER_WATCHDOG_CITY": str(city),
        "DISPATCHER_WATCHDOG_AUDIT_LOG": str(city / ".gc" / "dispatcher-watchdog.log"),
        "DISPATCHER_WATCHDOG_CONVERSION_STATE_FILE": str(
            city / ".gc" / "dispatcher-watchdog-conversion.json"
        ),
        "DISPATCHER_WATCHDOG_CONVERSION_STALL_SECS": "5400",
        "DISPATCHER_WATCHDOG_NOW": str(START),
        "DISPATCHER_WATCHDOG_IDLE_SECS": "999999",
        "DRY_RUN": "0",
    }
    return env, runtime, city


def run(env, now):
    current = {**env, "DISPATCHER_WATCHDOG_NOW": str(now)}
    return subprocess.run([str(SCRIPT)], env=current, text=True, capture_output=True)


def read(path):
    return json.loads(path.read_text())


def write(path, value):
    path.write_text(json.dumps(value))


def conversion_state(city):
    return read(city / ".gc" / "dispatcher-watchdog-conversion.json")


def test_stalled_rig_a_never_ages_or_kills_empty_rig_b(watchdog):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [
        session("alpha-old", "alpha/core.control-dispatcher"),
        session("beta-old", "beta/core.control-dispatcher"),
    ]
    state["stores"] = {
        "alpha": [
            bead("alpha-1", "alpha/core.control-dispatcher"),
            bead("wrong-target", "beta/core.control-dispatcher"),
        ],
        "beta": [],
    }
    write(runtime, state)

    assert run(env, START).returncode == 0
    first = conversion_state(city)
    assert set(first) == {"alpha/core.control-dispatcher"}
    assert first["alpha/core.control-dispatcher"]["set"] == "alpha-1"

    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == ["alpha-old"]
    assert "beta/core.control-dispatcher" not in conversion_state(city)


def test_rig_a_changes_do_not_reset_rig_b_clock(watchdog):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [
        session("alpha-old", "alpha/core.control-dispatcher"),
        session("beta-old", "beta/core.control-dispatcher"),
    ]
    state["stores"] = {
        "alpha": [bead("alpha-1", "alpha/core.control-dispatcher")],
        "beta": [bead("beta-1", "beta/core.control-dispatcher")],
    }
    write(runtime, state)
    assert run(env, START).returncode == 0

    state = read(runtime)
    state["stores"]["alpha"] = [bead("alpha-2", "alpha/core.control-dispatcher")]
    write(runtime, state)
    assert run(env, START + 100).returncode == 0
    clocks = conversion_state(city)
    assert clocks["alpha/core.control-dispatcher"]["since"] == START + 100
    assert clocks["beta/core.control-dispatcher"]["since"] == START

    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == ["beta-old"]


def test_empty_queue_clears_clock_before_same_set_reappears(watchdog):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [session("alpha-live", "alpha/core.control-dispatcher")]
    state["stores"] = {"alpha": [bead("alpha-1", "alpha/core.control-dispatcher")]}
    write(runtime, state)
    assert run(env, START).returncode == 0

    state = read(runtime)
    state["stores"]["alpha"] = []
    write(runtime, state)
    assert run(env, START + 100).returncode == 0
    assert "alpha/core.control-dispatcher" not in conversion_state(city)

    state = read(runtime)
    state["stores"]["alpha"] = [bead("alpha-1", "alpha/core.control-dispatcher")]
    write(runtime, state)
    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == []
    assert conversion_state(city)["alpha/core.control-dispatcher"]["since"] == START + 5400


def test_replacement_id_keeps_same_target_clock_and_still_flags(watchdog):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [session("alpha-old", "alpha/core.control-dispatcher")]
    # The historical non-dotted spelling must normalize to the session target.
    state["stores"] = {"alpha": [bead("alpha-1", "alpha/control-dispatcher")]}
    write(runtime, state)
    assert run(env, START).returncode == 0

    state = read(runtime)
    state["sessions"] = [session("alpha-new", "alpha/core.control-dispatcher")]
    write(runtime, state)
    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == ["alpha-new"]
    clock = conversion_state(city)["alpha/core.control-dispatcher"]
    assert clock == {"set": "alpha-1", "since": START, "session_id": "alpha-new"}


def test_city_target_is_scoped_to_city_store_and_legacy_id_state_is_not_reused(watchdog):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [session("city-new", "core.control-dispatcher")]
    state["stores"] = {
        "city": [bead("city-1", "control-dispatcher")],
        "alpha": [bead("alpha-1", "alpha/core.control-dispatcher")],
    }
    write(runtime, state)
    legacy = city / ".gc" / "dispatcher-watchdog-conversion.json"
    write(legacy, {"city-old": {"set": "city-1", "since": START - 5400}})

    assert run(env, START).returncode == 0
    assert read(runtime)["killed"] == []
    clock = conversion_state(city)["core.control-dispatcher"]
    assert clock == {"set": "city-1", "since": START, "session_id": "city-new"}


@pytest.mark.parametrize("malformation", ["target", "id"])
def test_malformed_store_row_cannot_age_a_partial_set(watchdog, malformation):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [session("alpha-live", "alpha/core.control-dispatcher")]
    state["stores"] = {"alpha": [bead("alpha-1", "alpha/core.control-dispatcher")]}
    write(runtime, state)
    assert run(env, START).returncode == 0
    first_clock = conversion_state(city)["alpha/core.control-dispatcher"]

    state = read(runtime)
    malformed = bead("alpha-malformed", "alpha/core.control-dispatcher")
    if malformation == "target":
        malformed["metadata"]["gc.routed_to"] = 42
    else:
        malformed.pop("id")
    state["stores"]["alpha"].append(malformed)
    write(runtime, state)
    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == []
    assert conversion_state(city)["alpha/core.control-dispatcher"] == first_clock


def test_unparseable_last_active_skips_idle_signal(watchdog):
    env, runtime, _ = watchdog
    state = read(runtime)
    active = session("alpha-live", "alpha/core.control-dispatcher")
    active["last_active"] = "not-a-timestamp"
    state["sessions"] = [active]
    state["stores"] = {"alpha": []}
    write(runtime, state)

    assert run(env, START).returncode == 0
    assert read(runtime)["killed"] == []


@pytest.mark.parametrize("excluded", ["blocked", "epic", "instantiating"])
def test_unprocessable_beads_never_age_conversion_clock(watchdog, excluded):
    env, runtime, city = watchdog
    state = read(runtime)
    state["sessions"] = [session("alpha-live", "alpha/core.control-dispatcher")]
    held = bead("alpha-held", "alpha/core.control-dispatcher")
    if excluded == "blocked":
        held["blocked_by"] = ["alpha-dependency"]
    elif excluded == "epic":
        held["issue_type"] = "epic"
    else:
        held["metadata"]["gc.instantiating"] = "workflow-123"
    state["stores"] = {"alpha": [held]}
    write(runtime, state)

    assert run(env, START).returncode == 0
    assert run(env, START + 5400).returncode == 0
    assert read(runtime)["killed"] == []
    path = city / ".gc" / "dispatcher-watchdog-conversion.json"
    assert not path.exists() or "alpha/core.control-dispatcher" not in read(path)
