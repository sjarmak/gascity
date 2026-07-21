import json
import os
import subprocess
from pathlib import Path

import pytest


SCRIPT = Path(__file__).with_name("terminal-worker-escalation")
SOURCE = "rig:gascity-dashboard:gascity-dashboard-1rey"

FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
p=Path(os.environ["FAKE_STATE"]); s=json.loads(p.read_text())
a=sys.argv[1:]
def save(): p.write_text(json.dumps(s))
if a[:2] == ["mail", "send"]:
    recipient=a[2]
    assert "--message" in a and "--body" not in a, a
    s.setdefault("mail",[]).append({"recipient":recipient,"args":a[3:]})
    fail=recipient in s.get("fail_mail",[]); save(); sys.exit(7 if fail else 0)
if a[:2] == ["rig", "list"]:
    print("  gascity-dashboard:\n    Prefix: gascity-dashboard"); sys.exit(0)
assert a[:2] == ["bd", "--rig"], a
rig=a[2]; action=a[3]; rest=a[4:]; rows=s["rigs"][rig]
if action == "show":
    if rest[0] in s.get("fail_show",[]): sys.exit(8)
    print(json.dumps([rows[rest[0]]])); sys.exit(0)
if action == "list":
    label=rest[rest.index("--label")+1]
    print(json.dumps([row for row in rows.values() if label in row.get("labels",[])])); sys.exit(0)
if action == "update":
    row=rows[rest[0]]; args=rest[1:]; i=0
    while i < len(args):
        flag=args[i]
        if flag == "--status": row["status"]=args[i+1]; i+=2
        elif flag == "--assignee": row["assignee"]=args[i+1] or None; i+=2
        elif flag == "--add-label":
            if args[i+1] not in row["labels"]: row["labels"].append(args[i+1])
            i+=2
        elif flag == "--set-metadata":
            key,value=args[i+1].split("=",1); row.setdefault("metadata",{})[key]=value; i+=2
        elif flag == "--unset-metadata": row.setdefault("metadata",{}).pop(args[i+1],None); i+=2
        else: raise AssertionError(args[i:])
    save(); print("updated"); sys.exit(0)
raise SystemExit(3)
'''


@pytest.fixture
def env(tmp_path):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    fake = bindir / "gc"
    fake.write_text(FAKE)
    fake.chmod(0o755)
    state = tmp_path / "state.json"
    state.write_text(json.dumps({
        "rigs": {"gascity-dashboard": {"gascity-dashboard-1rey": {
            "id": "gascity-dashboard-1rey", "status": "in_progress",
            "assignee": "gascity-dashboard-worker", "labels": ["dashboard"],
            "metadata": {"gc.routed_to": "/worker", "gc.session_name": "worker-1", "gc.work_dir": "/keep"},
        }}}, "mail": [],
    }))
    e = {
        **os.environ,
        "PATH": f"{bindir}:{os.environ['PATH']}",
        "FAKE_STATE": str(state),
        "TERMINAL_ESCALATION_DIR": str(tmp_path / "records"),
        "TERMINAL_ESCALATION_AUDIT_LOG": str(tmp_path / "audit.jsonl"),
        "TERMINAL_ESCALATION_LOCK": str(tmp_path / "lock"),
        "TERMINAL_ESCALATION_STORES": "gascity-dashboard",
        "TERMINAL_ESCALATION_NOTIFY_RETRY_SECONDS": "0",
    }
    return e, state, tmp_path


def run(env, *args):
    return subprocess.run([str(SCRIPT), *args], env=env, text=True, capture_output=True)


def raise_it(env):
    return run(
        env, "raise", "--source", SOURCE,
        "--worker", "gascity-dashboard-worker",
        "--owning-pl", "gascity-dashboard-pl",
        "--reason-class", "false-premise",
        "--evidence", "gc-512184 proves the API premise is false",
    )


def test_incident_becomes_blocked_disarmed_and_notifies_both(env):
    e, state, tmp = env
    result = raise_it(e)
    assert result.returncode == 0, result.stderr
    live = json.loads(state.read_text())
    source = live["rigs"]["gascity-dashboard"]["gascity-dashboard-1rey"]
    assert source["status"] == "blocked" and source["assignee"] is None
    assert {"terminal-escalated", "dispatch-blocked"} <= set(source["labels"])
    assert "help_request" in source["metadata"]
    assert "gc.routed_to" not in source["metadata"] and "gc.session_name" not in source["metadata"]
    assert source["metadata"]["gc.work_dir"] == "/keep"
    assert {mail["recipient"] for mail in live["mail"]} == {"gascity-dashboard-pl", "mayor"}
    assert all("/home/ds/gas-city/bin/terminal-worker-escalation" in mail["args"][mail["args"].index("--message") + 1] for mail in live["mail"])
    record = json.loads(next((tmp / "records").glob("*.json")).read_text())
    assert record["source_ref"] == SOURCE
    assert record["acknowledgement"]["state"] == "pending"
    assert record["disposition"]["state"] == "pending"
    assert run(e, "scan").returncode == 1


def test_one_coordinator_must_ack_and_dispose(env):
    e, state, _ = env
    assert raise_it(e).returncode == 0
    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "mayor", "--class", "upstream-gated", "--detail", "wait").returncode == 2
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "gascity-dashboard-pl").returncode == 0
    first_record = next(Path(e["TERMINAL_ESCALATION_DIR"]).glob("*.json")).read_text()
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "gascity-dashboard-pl").returncode == 0
    assert next(Path(e["TERMINAL_ESCALATION_DIR"]).glob("*.json")).read_text() == first_record
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "mayor").returncode == 2
    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "mayor", "--class", "upstream-gated", "--detail", "wait").returncode == 2
    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "gascity-dashboard-pl", "--class", "upstream-gated", "--detail", "blocked on rig:gascity:gc-r71n").returncode == 0
    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "gascity-dashboard-pl", "--class", "different", "--detail", "changed").returncode == 2
    assert run(e, "scan").returncode == 0
    source = json.loads(state.read_text())["rigs"]["gascity-dashboard"]["gascity-dashboard-1rey"]
    assert source["metadata"]["terminal_escalation_acknowledgement"] == "recorded"
    assert source["metadata"]["terminal_escalation_disposition"] == "recorded"


def test_patrol_repairs_redispatch_drift_and_retries_mail(env):
    e, state, _ = env
    live = json.loads(state.read_text()); live["fail_mail"] = ["gascity-dashboard-pl"]; state.write_text(json.dumps(live))
    assert raise_it(e).returncode == 1
    live = json.loads(state.read_text())
    source = live["rigs"]["gascity-dashboard"]["gascity-dashboard-1rey"]
    source["status"] = "in_progress"; source["assignee"] = "worker-2"; source["metadata"]["gc.routed_to"] = "/worker-2"
    live["fail_mail"] = []; state.write_text(json.dumps(live))
    result = run(e, "scan", "--apply")
    assert result.returncode == 1  # durable ack/disposition obligations remain
    repaired = json.loads(state.read_text())
    source = repaired["rigs"]["gascity-dashboard"]["gascity-dashboard-1rey"]
    assert source["status"] == "blocked" and source["assignee"] is None
    assert "gc.routed_to" not in source["metadata"]
    assert [m["recipient"] for m in repaired["mail"]].count("gascity-dashboard-pl") >= 2


def test_patrol_reconstructs_record_from_authoritative_bead(env):
    e, _, tmp = env
    assert raise_it(e).returncode == 0
    path = next((tmp / "records").glob("*.json"))
    path.unlink()
    result = run(e, "scan", "--apply")
    assert result.returncode == 1
    assert path.exists()
    assert json.loads(result.stdout)["records"] == 1


@pytest.mark.parametrize("transition", ["ack", "dispose"])
def test_patrol_recovers_crash_after_source_lifecycle_update(env, transition):
    e, state, tmp = env
    assert raise_it(e).returncode == 0
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "mayor").returncode == 0
    record_path = next((tmp / "records").glob("*.json"))
    stale = json.loads(record_path.read_text())
    if transition == "ack":
        stale["acknowledgement"] = {"state": "pending", "by": None, "at": None}
    else:
        assert run(e, "dispose", "--source", SOURCE, "--coordinator", "mayor", "--class", "upstream-gated", "--detail", "wait for API").returncode == 0
        stale = json.loads(record_path.read_text())
        stale["disposition"] = {"state": "pending", "by": None, "at": None, "class": None, "detail": None}
    record_path.write_text(json.dumps(stale))

    result = run(e, "scan", "--apply")
    recovered = json.loads(record_path.read_text())
    field = "disposition" if transition == "dispose" else "acknowledgement"
    assert recovered[field]["state"] == "recorded"
    expected = 0 if transition == "dispose" else 1
    assert result.returncode == expected


def test_retry_after_lifecycle_crash_cannot_replace_authoritative_values(env):
    e, _, tmp = env
    assert raise_it(e).returncode == 0
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "mayor").returncode == 0
    path = next((tmp / "records").glob("*.json"))
    stale = json.loads(path.read_text())
    stale["acknowledgement"] = {"state": "pending", "by": None, "at": None}
    path.write_text(json.dumps(stale))
    assert run(e, "ack", "--source", SOURCE, "--coordinator", "gascity-dashboard-pl").returncode == 2
    assert json.loads(path.read_text())["acknowledgement"]["by"] == "mayor"

    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "mayor", "--class", "upstream-gated", "--detail", "first").returncode == 0
    stale = json.loads(path.read_text())
    stale["disposition"] = {"state": "pending", "by": None, "at": None, "class": None, "detail": None}
    path.write_text(json.dumps(stale))
    assert run(e, "dispose", "--source", SOURCE, "--coordinator", "mayor", "--class", "different", "--detail", "changed").returncode == 2
    assert json.loads(path.read_text())["disposition"]["class"] == "upstream-gated"


def test_patrol_fails_when_store_discovery_fails(env):
    e, state, _ = env
    live = json.loads(state.read_text())
    del live["rigs"]["gascity-dashboard"]
    state.write_text(json.dumps(live))
    result = run(e, "scan", "--apply")
    assert result.returncode == 1
    assert "store discovery failed" in result.stdout


def test_patrol_fails_when_discovered_source_cannot_be_read(env):
    e, state, tmp = env
    assert raise_it(e).returncode == 0
    next((tmp / "records").glob("*.json")).unlink()
    live = json.loads(state.read_text())
    live["fail_show"] = ["gascity-dashboard-1rey"]
    state.write_text(json.dumps(live))
    result = run(e, "scan", "--apply")
    assert result.returncode == 1
    assert "source discovery read failed" in result.stdout
