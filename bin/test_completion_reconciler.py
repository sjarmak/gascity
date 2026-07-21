"""Behavioral tests for the completion reconciler."""

from __future__ import annotations

import importlib.util
from importlib.machinery import SourceFileLoader
import json
import os
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "bin" / "completion-reconciler"
MODULE_SPEC = importlib.util.spec_from_loader(
    "completion_reconciler",
    SourceFileLoader("completion_reconciler", str(SCRIPT)),
)
assert MODULE_SPEC is not None and MODULE_SPEC.loader is not None
reconciler = importlib.util.module_from_spec(MODULE_SPEC)
sys.modules[MODULE_SPEC.name] = reconciler
MODULE_SPEC.loader.exec_module(reconciler)

UTC = timezone.utc
NOW = datetime(2026, 7, 18, 14, 0, tzinfo=UTC)


def issue(**overrides: object) -> dict[str, object]:
    value: dict[str, object] = {
        "id": "dr-test",
        "title": "stale P0",
        "status": "in_progress",
        "priority": 0,
        "assignee": "city-infra-pl",
        "updated_at": "2026-07-18T12:00:00Z",
        "labels": ["city-infra"],
        "metadata": {},
    }
    value.update(overrides)
    return value


def config(**overrides: object):
    value = reconciler.Config(
        thresholds={0: 3600, 1: 4 * 3600, 2: 12 * 3600, 3: 24 * 3600, 4: 48 * 3600},
        checkpoint_grace=1800,
        max_recoveries=2,
        human_assignees=frozenset({"sjarmak"}),
    )
    return reconciler.dataclasses.replace(value, **overrides)


@pytest.mark.parametrize(
    ("priority", "seconds"),
    [(0, 3600), (1, 14400), (2, 43200), (3, 86400), (4, 172800), (99, 172800)],
)
def test_priority_thresholds_are_explicit(priority: int, seconds: int) -> None:
    assert reconciler.threshold_for(priority, config()) == seconds


def test_stale_work_requests_checkpoint_before_recovery() -> None:
    decision = reconciler.decide(issue(), NOW, config())
    assert decision.action == reconciler.Action.REQUEST_CHECKPOINT
    assert decision.stale_seconds == 7200


def test_recent_work_is_left_alone() -> None:
    recent = issue(updated_at="2026-07-18T13:30:00Z")
    assert reconciler.decide(recent, NOW, config()).action == reconciler.Action.NONE


def test_checkpoint_grace_prevents_early_recovery() -> None:
    waiting = issue(
        metadata={
            reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:45:00Z",
            reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
        }
    )
    assert reconciler.decide(waiting, NOW, config()).action == reconciler.Action.WAIT


def test_explicit_progress_clears_pending_recovery() -> None:
    progressed = issue(
        metadata={
            reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:00:00Z",
            reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
            reconciler.PROGRESS_AT: "2026-07-18T13:30:00Z",
            reconciler.PROGRESS_EVIDENCE: "integration suite running",
        }
    )
    assert reconciler.decide(progressed, NOW, config()).action == reconciler.Action.RESUMED


def test_progress_timestamp_without_evidence_does_not_renew_lease() -> None:
    heartbeat_only = issue(
        metadata={
            reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:00:00Z",
            reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
            reconciler.PROGRESS_AT: "2026-07-18T13:30:00Z",
        }
    )
    assert reconciler.decide(heartbeat_only, NOW, config()).action == reconciler.Action.REQUEUE


def test_expired_checkpoint_requeues_and_repeated_failure_blocks() -> None:
    pending = {
        reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:00:00Z",
        reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
    }
    assert reconciler.decide(issue(metadata=pending), NOW, config()).action == reconciler.Action.REQUEUE

    exhausted = {**pending, reconciler.RECOVERY_ATTEMPTS: "2"}
    assert reconciler.decide(issue(metadata=exhausted), NOW, config()).action == reconciler.Action.BLOCK


@pytest.mark.parametrize(
    "candidate",
    [
        issue(assignee=""),
        issue(assignee="sjarmak"),
        issue(labels=["health-soak"]),
        issue(labels=["human-wip"]),
        issue(metadata={"long_running": "true"}),
        issue(metadata={"gc.hold_reason": "operator hold"}),
        issue(metadata={reconciler.COMPLETION_EXEMPT: "true"}),
    ],
)
def test_intentional_or_human_work_is_exempt(candidate: dict[str, object]) -> None:
    assert reconciler.decide(candidate, NOW, config()).action == reconciler.Action.EXEMPT


def _write_executable(path: Path, body: str) -> None:
    path.write_text(body)
    path.chmod(0o755)


def _mock_cli(tmp_path: Path, initial: dict[str, object]) -> tuple[dict[str, str], Path]:
    mock_bin = tmp_path / "bin"
    mock_bin.mkdir()
    state = tmp_path / "state.json"
    state.write_text(json.dumps(initial))
    calls = tmp_path / "calls.jsonl"

    _write_executable(
        mock_bin / "bd",
        """#!/usr/bin/env python3
import json, os, sys
from datetime import datetime, timezone
state_path = os.environ["MOCK_STATE"]
calls_path = os.environ["MOCK_CALLS"]
args = sys.argv[1:]
with open(calls_path, "a") as fh:
    fh.write(json.dumps(["bd", *args]) + "\\n")
with open(state_path) as fh:
    state = json.load(fh)
if args[0] == "list":
    print(json.dumps([state] if state.get("status") == "in_progress" else []))
elif args[0] == "show":
    print(json.dumps([state]))
elif args[0] == "update":
    if os.environ.get("MOCK_UPDATE_FAIL") == "1":
        raise SystemExit(1)
    idx = 2
    while idx < len(args):
        if args[idx] == "--status":
            state["status"] = args[idx + 1]; idx += 2
        elif args[idx] == "--assignee":
            state["assignee"] = args[idx + 1]; idx += 2
        elif args[idx] == "--set-metadata":
            key, value = args[idx + 1].split("=", 1)
            state.setdefault("metadata", {})[key] = value; idx += 2
        elif args[idx] == "--unset-metadata":
            state.setdefault("metadata", {}).pop(args[idx + 1], None); idx += 2
        else:
            idx += 1
    state["updated_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    with open(state_path, "w") as fh:
        json.dump(state, fh)
else:
    raise SystemExit(2)
""",
    )
    _write_executable(
        mock_bin / "gc",
        """#!/usr/bin/env python3
import json, os, sys
with open(os.environ["MOCK_CALLS"], "a") as fh:
    fh.write(json.dumps(["gc", *sys.argv[1:]]) + "\\n")
if sys.argv[1:3] == ["rig", "list"]:
    if os.environ.get("MOCK_RIG_LIST_FAIL") == "1":
        print("rig list unavailable", file=sys.stderr)
        raise SystemExit(1)
    print('{"rigs":[]}')
elif sys.argv[1:3] == ["session", "nudge"]:
    pass
else:
    raise SystemExit(2)
""",
    )
    env = {
        **os.environ,
        "PATH": f"{mock_bin}:{os.environ['PATH']}",
        "MOCK_STATE": str(state),
        "MOCK_CALLS": str(calls),
        "COMPLETION_RECONCILER_AUDIT_LOG": str(tmp_path / "audit.jsonl"),
        "COMPLETION_RECONCILER_LOCK": str(tmp_path / "reconciler.lock"),
    }
    return env, calls


def _run(tmp_path: Path, initial: dict[str, object], *args: str, **extra_env: str):
    env, calls = _mock_cli(tmp_path, initial)
    env.update(extra_env)
    result = subprocess.run(
        [
            str(SCRIPT),
            "--scope",
            "city",
            "--now",
            "2026-07-18T14:00:00Z",
            "--checkpoint-grace",
            "30m",
            *args,
        ],
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )
    state = json.loads(Path(env["MOCK_STATE"]).read_text())
    call_rows = [json.loads(line) for line in calls.read_text().splitlines()]
    return result, state, call_rows, Path(env["COMPLETION_RECONCILER_AUDIT_LOG"])


def test_e2e_checkpoint_then_requeue_preserves_work_metadata(tmp_path: Path) -> None:
    initial = issue(metadata={"gc.work_dir": "/tmp/work", "gc.routed_to": "city-infra-pl"})
    first, requested, calls, audit = _run(tmp_path, initial, "--apply", "--nudge-mayor")

    assert first.returncode == 0, first.stderr
    assert reconciler.CHECKPOINT_REQUESTED_AT in requested["metadata"]
    assert requested["metadata"]["gc.work_dir"] == "/tmp/work"
    assert any(row[:3] == ["gc", "session", "nudge"] for row in calls)
    assert '"event":"checkpoint_requested"' in audit.read_text()

    requested["metadata"][reconciler.CHECKPOINT_REQUESTED_AT] = "2026-07-18T13:00:00Z"
    requested["metadata"][reconciler.OBSERVED_UPDATED_AT] = "2026-07-18T12:00:00Z"
    second_dir = tmp_path / "second"
    second_dir.mkdir()
    second, recovered, _, audit2 = _run(second_dir, requested, "--apply", "--nudge-mayor")

    assert second.returncode == 0, second.stderr
    assert recovered["status"] == "open"
    assert recovered["assignee"] == ""
    assert recovered["metadata"]["gc.work_dir"] == "/tmp/work"
    assert recovered["metadata"]["gc.routed_to"] == "city-infra-pl"
    assert recovered["metadata"][reconciler.RECOVERY_ATTEMPTS] == "1"
    assert '"event":"requeued"' in audit2.read_text()


def test_dry_run_does_not_mutate_or_nudge(tmp_path: Path) -> None:
    initial = issue()
    result, state, calls, _ = _run(tmp_path, initial)
    assert result.returncode == 0
    assert state == initial
    assert not any(row[:3] == ["gc", "session", "nudge"] for row in calls)


def test_failed_mutation_is_loud_and_does_not_claim_success(tmp_path: Path) -> None:
    result, state, _, audit = _run(tmp_path, issue(), "--apply", MOCK_UPDATE_FAIL="1")
    assert result.returncode == 1
    assert reconciler.CHECKPOINT_REQUESTED_AT not in state["metadata"]
    assert '"event":"mutation_failed"' in audit.read_text()


def _run_main(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    initial: dict[str, object],
    *args: str,
    scope: str = "city",
    **extra_env: str,
) -> tuple[int, dict[str, object], list[list[str]], Path]:
    env, calls = _mock_cli(tmp_path, initial)
    env.update(extra_env)
    for key, value in env.items():
        monkeypatch.setenv(key, value)
    rc = reconciler.main(
        [
            "--scope",
            scope,
            "--now",
            "2026-07-18T14:00:00Z",
            "--checkpoint-grace",
            "30m",
            *args,
        ]
    )
    state = json.loads(Path(env["MOCK_STATE"]).read_text())
    call_rows = [json.loads(line) for line in calls.read_text().splitlines()]
    return rc, state, call_rows, Path(env["COMPLETION_RECONCILER_AUDIT_LOG"])


def test_main_applies_checkpoint_and_nudges_owner_and_mayor(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    rc, state, calls, audit = _run_main(
        monkeypatch,
        tmp_path,
        issue(),
        "--apply",
        "--nudge-mayor",
    )
    assert rc == 0
    assert reconciler.CHECKPOINT_REQUESTED_AT in state["metadata"]
    targets = [row[3] for row in calls if row[:3] == ["gc", "session", "nudge"]]
    assert targets == ["city-infra-pl", "mayor"]
    assert '"event":"checkpoint_requested"' in audit.read_text()


def test_notification_timeout_is_recorded_without_crashing_reconciliation(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    runner = reconciler.Reconciler(
        config(),
        NOW,
        apply=True,
        nudge_mayor=True,
        audit_log=tmp_path / "audit.jsonl",
    )

    def time_out(_argv: object, timeout: int = 30) -> object:
        raise subprocess.TimeoutExpired(["gc", "session", "nudge"], timeout)

    monkeypatch.setattr(reconciler, "run_command", time_out)
    runner.mayor_messages.append("checkpoint requested: city/dr-test")

    runner.finish()

    assert '"event":"mayor_nudge_failed"' in runner.audit_log.read_text()


def test_main_clears_recovery_when_explicit_progress_arrives(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    progressed = issue(
        metadata={
            reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:00:00Z",
            reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
            reconciler.PROGRESS_AT: "2026-07-18T13:30:00Z",
            reconciler.PROGRESS_EVIDENCE: "tests running",
        }
    )
    rc, state, _, audit = _run_main(monkeypatch, tmp_path, progressed, "--apply")
    assert rc == 0
    assert reconciler.CHECKPOINT_REQUESTED_AT not in state["metadata"]
    assert state["metadata"][reconciler.LAST_PROGRESS_ACK] == "2026-07-18T13:30:00Z"
    assert '"event":"progress_resumed"' in audit.read_text()


def test_main_blocks_after_recovery_budget_is_exhausted(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    exhausted = issue(
        metadata={
            reconciler.CHECKPOINT_REQUESTED_AT: "2026-07-18T13:00:00Z",
            reconciler.OBSERVED_UPDATED_AT: "2026-07-18T12:00:00Z",
            reconciler.RECOVERY_ATTEMPTS: "2",
            "gc.work_dir": "/tmp/preserved",
        }
    )
    rc, state, _, audit = _run_main(monkeypatch, tmp_path, exhausted, "--apply")
    assert rc == 0
    assert state["status"] == "blocked"
    assert state["assignee"] == ""
    assert state["metadata"]["gc.work_dir"] == "/tmp/preserved"
    assert "exhausted" in state["metadata"]["help_request"]
    assert '"event":"blocked"' in audit.read_text()


def test_invalid_issue_is_loud_but_not_mutated(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    invalid = issue(updated_at="not-a-time")
    rc, state, _, audit = _run_main(monkeypatch, tmp_path, invalid, "--apply")
    assert rc == 1
    assert state == invalid
    assert '"event":"invalid_issue"' in audit.read_text()


def test_main_reports_unknown_scope_and_invalid_now(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    env, _ = _mock_cli(tmp_path, issue())
    for key, value in env.items():
        monkeypatch.setenv(key, value)
    assert reconciler.main(["--scope", "missing"]) == 2
    assert reconciler.main(["--scope", "city", "--now", "bad"]) == 2


def test_scope_all_scans_city_despite_rig_enum_timeout(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    audit = tmp_path / "audit.jsonl"
    runner = reconciler.Reconciler(config(), NOW, apply=False, nudge_mayor=False, audit_log=audit)

    def fake_load(argv: object, timeout: int = 30) -> object:
        command = list(argv)  # type: ignore[call-overload]
        if command[:3] == ["gc", "rig", "list"]:
            raise subprocess.TimeoutExpired(command, timeout)
        if command[:2] == ["bd", "list"]:
            return [issue()]
        raise AssertionError(f"unexpected command {command}")

    monkeypatch.setattr(reconciler, "load_json_command", fake_load)
    runner.run_scope("all")

    assert runner.result.scanned == 1
    assert runner.result.errors == 1
    text = audit.read_text()
    assert '"event":"rig_enum_failed"' in text
    assert '"event":"scan_failed"' not in text


def test_partial_rig_failure_skips_only_that_rig(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    audit = tmp_path / "audit.jsonl"
    runner = reconciler.Reconciler(config(), NOW, apply=False, nudge_mayor=False, audit_log=audit)

    def fake_load(argv: object, timeout: int = 30) -> object:
        command = list(argv)  # type: ignore[call-overload]
        if command[:3] == ["gc", "rig", "list"]:
            return {"rigs": [{"name": "alpha"}, {"name": "beta"}, {"name": "ds-research"}]}
        if command[:2] == ["bd", "list"]:
            return [issue(id="city-1")]
        if command[:5] == ["gc", "bd", "--rig", "alpha", "list"]:
            raise RuntimeError("alpha dolt endpoint unreachable")
        if command[:5] == ["gc", "bd", "--rig", "beta", "list"]:
            return [issue(id="beta-1")]
        raise AssertionError(f"unexpected command {command}")

    monkeypatch.setattr(reconciler, "load_json_command", fake_load)
    runner.run_scope("all")

    assert runner.result.scanned == 2  # city + beta; alpha alone is skipped
    assert runner.result.errors == 1
    text = audit.read_text()
    assert '"event":"scan_failed"' in text
    assert '"store":"alpha"' in text
    assert '"store":"beta"' not in text
    assert '"event":"rig_enum_failed"' not in text


def test_main_scope_all_recovers_city_despite_rig_enum_failure(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    rc, state, _, audit = _run_main(
        monkeypatch, tmp_path, issue(), "--apply", scope="all", MOCK_RIG_LIST_FAIL="1"
    )
    assert rc == 1  # enumeration failure stays loud, but the run completes
    assert reconciler.CHECKPOINT_REQUESTED_AT in state["metadata"]
    text = audit.read_text()
    assert '"event":"checkpoint_requested"' in text
    assert '"event":"rig_enum_failed"' in text


def test_main_scope_all_clean_run_emits_no_enum_failure(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    rc, state, _, audit = _run_main(monkeypatch, tmp_path, issue(), "--apply", scope="all")
    assert rc == 0
    assert reconciler.CHECKPOINT_REQUESTED_AT in state["metadata"]
    assert '"event":"rig_enum_failed"' not in audit.read_text()


@pytest.mark.parametrize(
    ("value", "expected"),
    [("15s", 15), ("30m", 1800), ("4h", 14400), ("2d", 172800)],
)
def test_duration_parser(value: str, expected: int) -> None:
    assert reconciler.parse_duration(value) == expected


def test_duration_parser_rejects_ambiguous_values() -> None:
    with pytest.raises(ValueError):
        reconciler.parse_duration("30")


def test_store_commands_distinguish_city_and_rig() -> None:
    assert reconciler.Store("city", None).command("list") == ["bd", "list"]
    assert reconciler.Store("mem", "mem").command("list") == [
        "gc",
        "bd",
        "--rig",
        "mem",
        "list",
    ]
