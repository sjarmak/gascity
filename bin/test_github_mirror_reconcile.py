"""Regression tests for dr-3f34: github-mirror-reconcile must never treat a
closed bead with a still-open GitHub mirror issue as a human-filed issue.

Root cause: reconcile_rig() built its known-bead map from `bd list --limit 0
--json`, which excludes closed beads by default. Every closed bead whose
mirror issue was still open on GitHub therefore looked "unmatched" and was
routed into ingest(), which ran `bd github pull` (overwriting the closed bead
with GitHub's stale open state) and then `bd update --status deferred`.

Two independent fixes are covered here:
  1. reconcile_rig()'s known-bead map must include closed beads (`bd list
     --all --limit 0 --json`), so a closed bead with a matching external_ref
     is found and never falls into the "unmatched -> ingest" branch.
  2. ingest() itself must refuse to ingest/defer any issue whose ref already
     resolves to an existing local bead, whatever its status — defense in
     depth, independent of whether the caller's map is complete.

Also covers two follow-up review findings on that same commit (e3dbe4e):
  3. find_bead_by_ref() must fail closed (raise RuntimeError, not let
     json.JSONDecodeError escape) when `bd list --all --limit 0 --json`
     exits 0 but emits non-JSON stdout, so ingest()'s existing
     `except RuntimeError` guards actually catch it instead of crashing the
     whole reconcile run.
  4. reconcile_rig() must surface (not silently ignore) an issue that is
     still open on GitHub after its matching closed bead has aged past the
     mirror push's re-close window (MIRROR_CLOSED_DAYS) — otherwise that
     drift is permanent and never alerts.
"""
from __future__ import annotations

import importlib.machinery
import importlib.util
import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace

BIN = Path(__file__).resolve().parent
sys.path.insert(0, str(BIN))


def load_reconcile(module_name: str):
    loader = importlib.machinery.SourceFileLoader(module_name, str(BIN / "github-mirror-reconcile"))
    spec = importlib.util.spec_from_loader(module_name, loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    loader.exec_module(module)
    return module


def result(*, stdout="", stderr="", returncode=0):
    return SimpleNamespace(stdout=stdout, stderr=stderr, returncode=returncode)


ISSUE_URL = "https://github.com/sjarmak/codeprobe-beads/issues/5"

CLOSED_BEAD = {
    "id": "codeprobe-f7rl.99",
    "status": "closed",
    "external_ref": ISSUE_URL,
    "labels": [],
    "created_at": "2026-06-01T00:00:00Z",
    "updated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
}

OPEN_MIRROR_ISSUE = {
    "html_url": ISSUE_URL,
    "number": 5,
    "state": "open",
    "title": "Stale reopened mirror issue",
    "updated_at": "2026-07-20T00:00:00Z",
    "labels": [],
    "body": "",
}


def test_reconcile_rig_known_map_includes_closed_beads_and_skips_ingest(monkeypatch):
    """Part 1: the known-bead map must be built from ALL beads (incl. closed),
    so a closed bead with a still-open mirror issue is matched and never
    routed into ingest()."""
    reconcile = load_reconcile("github_mirror_reconcile_known_map_test")
    monkeypatch.setenv("RECONCILE_REPO_ALLOWLIST", "sjarmak/")
    commands = []

    def fake_run(command, cwd="/", env=None, **_kwargs):
        commands.append(command)
        if command[:2] == ["bd", "config"]:
            return result(stdout="sjarmak/codeprobe-beads\n")
        if command[0] == "gh":
            return result(stdout=json.dumps(OPEN_MIRROR_ISSUE) + "\n")
        if command[:2] == ["bd", "list"]:
            assert "--all" in command, (
                "reconcile_rig's known-bead map must query --all so closed "
                "beads are included (dr-3f34)"
            )
            return result(stdout=json.dumps([CLOSED_BEAD]))
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []
    now = datetime.now(timezone.utc)

    reconcile.reconcile_rig("/fixture/codeprobe", {}, now, findings, set())

    assert not any(c[:2] == ["bd", "github"] for c in commands), (
        "closed bead with an open mirror issue must never be pulled as intake"
    )
    assert not any("INGESTED" in line for line in findings)
    assert not any("INTAKE" in line and "FAILED" in line for line in findings)


def test_ingest_skips_issue_matching_existing_bead_of_any_status(monkeypatch):
    """Part 2: ingest() must independently refuse to pull/defer an issue
    whose ref already resolves to a local bead, regardless of status, and
    must log why instead of silently doing nothing."""
    reconcile = load_reconcile("github_mirror_reconcile_ingest_guard_test")
    commands = []

    def fake_run(command, cwd="/", env=None, **_kwargs):
        commands.append(command)
        if command[:2] == ["bd", "list"]:
            assert "--all" in command
            return result(stdout=json.dumps([CLOSED_BEAD]))
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []

    reconcile.ingest(
        "/fixture/codeprobe",
        "sjarmak/codeprobe-beads",
        ISSUE_URL,
        OPEN_MIRROR_ISSUE,
        {},
        findings,
    )

    assert not any(c[:2] == ["bd", "github"] for c in commands), "must not pull"
    assert not any(
        c[:2] == ["bd", "update"] and "deferred" in c for c in commands
    ), "must not defer an existing bead"
    assert any(
        "INTAKE-SKIPPED" in line and CLOSED_BEAD["id"] in line and "closed" in line
        for line in findings
    ), f"expected an INTAKE-SKIPPED finding naming the existing bead, got: {findings}"


def test_ingest_still_proceeds_for_a_genuinely_new_issue(monkeypatch):
    """Sanity check: the guard must not block real new-issue intake."""
    reconcile = load_reconcile("github_mirror_reconcile_ingest_allows_new_test")
    commands = []
    new_bead = {"id": "codeprobe-new1", "external_ref": ISSUE_URL}

    def fake_run(command, cwd="/", env=None, **_kwargs):
        commands.append(command)
        if command[:2] == ["bd", "list"]:
            assert "--all" in command
            # Before the pull: nothing local yet. After the pull: the new bead exists.
            has_pulled = any(c[:3] == ["bd", "github", "pull"] for c in commands)
            return result(stdout=json.dumps([new_bead] if has_pulled else []))
        if command[:3] == ["bd", "github", "pull"]:
            return result(returncode=0)
        if command[:2] == ["bd", "update"]:
            return result(returncode=0)
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []

    reconcile.ingest(
        "/fixture/codeprobe",
        "sjarmak/codeprobe-beads",
        ISSUE_URL,
        OPEN_MIRROR_ISSUE,
        {},
        findings,
    )

    assert any(c[:3] == ["bd", "github", "pull"] for c in commands)
    assert any(
        c[:2] == ["bd", "update"] and "deferred" in c for c in commands
    )
    assert any("INGESTED" in line for line in findings)


def test_find_bead_by_ref_fails_closed_on_non_json_stdout(monkeypatch):
    """Review finding 1 (e3dbe4e): `bd list --all --limit 0 --json` can exit 0
    with non-JSON stdout (e.g. a warning line ahead of the array). That must
    raise RuntimeError, not let json.JSONDecodeError escape — ingest()'s
    `except RuntimeError` guards only catch RuntimeError."""
    reconcile = load_reconcile("github_mirror_reconcile_find_bead_bad_json_test")

    def fake_run(command, cwd="/", env=None, **_kwargs):
        if command[:2] == ["bd", "list"]:
            return result(stdout="WARN: slow query\n[]")
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)

    try:
        reconcile.find_bead_by_ref("/fixture/codeprobe", ISSUE_URL)
    except RuntimeError:
        pass
    except json.JSONDecodeError:
        raise AssertionError(
            "find_bead_by_ref must wrap JSONDecodeError as RuntimeError so "
            "callers' `except RuntimeError` guards fail closed"
        )
    else:
        raise AssertionError("expected RuntimeError for non-JSON bd stdout")


def test_ingest_fails_closed_on_non_json_bd_stdout_instead_of_crashing(monkeypatch):
    """Review finding 1: ingest()'s existing fail-closed guard must actually
    trigger (INTAKE-SKIPPED, no pull, no crash) when bd's JSON output is
    malformed, not just when bd exits nonzero."""
    reconcile = load_reconcile("github_mirror_reconcile_ingest_bad_json_test")
    commands = []

    def fake_run(command, cwd="/", env=None, **_kwargs):
        commands.append(command)
        if command[:2] == ["bd", "list"]:
            return result(stdout="not json at all")
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []

    reconcile.ingest(
        "/fixture/codeprobe",
        "sjarmak/codeprobe-beads",
        ISSUE_URL,
        OPEN_MIRROR_ISSUE,
        {},
        findings,
    )

    assert not any(c[:3] == ["bd", "github", "pull"] for c in commands), "must not pull"
    assert any("INTAKE-SKIPPED" in line for line in findings), (
        f"expected a fail-closed INTAKE-SKIPPED finding, got: {findings}"
    )


def test_reconcile_rig_does_not_crash_when_ingest_hits_bad_json(monkeypatch):
    """Review finding 1: reconcile_rig()'s issue loop calls ingest() with no
    try/except of its own (only the map-build above it is wrapped). A
    genuinely new issue whose find_bead_by_ref call hits non-JSON bd stdout
    must not crash the whole rig — it must fail closed via ingest()'s guard."""
    reconcile = load_reconcile("github_mirror_reconcile_rig_bad_json_test")
    monkeypatch.setenv("RECONCILE_REPO_ALLOWLIST", "sjarmak/")
    new_issue = {**OPEN_MIRROR_ISSUE, "html_url": "https://github.com/sjarmak/codeprobe-beads/issues/9",
                 "number": 9}
    calls = {"n": 0}

    def fake_run(command, cwd="/", env=None, **_kwargs):
        if command[:2] == ["bd", "config"]:
            return result(stdout="sjarmak/codeprobe-beads\n")
        if command[0] == "gh":
            return result(stdout=json.dumps(new_issue) + "\n")
        if command[:2] == ["bd", "list"]:
            calls["n"] += 1
            if calls["n"] == 1:
                # reconcile_rig's own known-bead map build: valid, empty.
                return result(stdout="[]")
            # ingest()'s find_bead_by_ref call: malformed.
            return result(stdout="<<not json>>")
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []
    now = datetime.now(timezone.utc)

    reconcile.reconcile_rig("/fixture/codeprobe", {}, now, findings, set())

    assert any("INTAKE-SKIPPED" in line for line in findings), (
        f"expected the rig to fail closed on ingest instead of crashing, got: {findings}"
    )


def test_reconcile_rig_flags_stale_close_past_push_window(monkeypatch):
    """Review finding 2: an issue still open on GitHub whose matching bead
    closed longer than MIRROR_CLOSED_DAYS ago will never be re-pushed closed
    by bin/github-mirror, and previously fell through both branches of the
    issues loop with no finding at all — permanent silent drift."""
    reconcile = load_reconcile("github_mirror_reconcile_stale_close_test")
    monkeypatch.setenv("RECONCILE_REPO_ALLOWLIST", "sjarmak/")
    monkeypatch.setenv("MIRROR_CLOSED_DAYS", "14")
    now = datetime.now(timezone.utc)
    stale_bead = {
        **CLOSED_BEAD,
        "updated_at": (now - timedelta(days=20)).isoformat().replace("+00:00", "Z"),
    }

    def fake_run(command, cwd="/", env=None, **_kwargs):
        if command[:2] == ["bd", "config"]:
            return result(stdout="sjarmak/codeprobe-beads\n")
        if command[0] == "gh":
            return result(stdout=json.dumps(OPEN_MIRROR_ISSUE) + "\n")
        if command[:2] == ["bd", "list"]:
            return result(stdout=json.dumps([stale_bead]))
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []

    reconcile.reconcile_rig("/fixture/codeprobe", {}, now, findings, set())

    assert any(
        "MIRROR-STALE-CLOSE" in line and stale_bead["id"] in line for line in findings
    ), f"expected a MIRROR-STALE-CLOSE finding, got: {findings}"


def test_reconcile_rig_does_not_flag_stale_close_within_push_window(monkeypatch):
    """Sanity check: a bead closed recently (within MIRROR_CLOSED_DAYS) is
    still expected to be re-pushed by bin/github-mirror, so it must not be
    flagged as drift yet."""
    reconcile = load_reconcile("github_mirror_reconcile_fresh_close_test")
    monkeypatch.setenv("RECONCILE_REPO_ALLOWLIST", "sjarmak/")
    monkeypatch.setenv("MIRROR_CLOSED_DAYS", "14")
    now = datetime.now(timezone.utc)
    fresh_bead = {
        **CLOSED_BEAD,
        "updated_at": (now - timedelta(days=2)).isoformat().replace("+00:00", "Z"),
    }

    def fake_run(command, cwd="/", env=None, **_kwargs):
        if command[:2] == ["bd", "config"]:
            return result(stdout="sjarmak/codeprobe-beads\n")
        if command[0] == "gh":
            return result(stdout=json.dumps(OPEN_MIRROR_ISSUE) + "\n")
        if command[:2] == ["bd", "list"]:
            return result(stdout=json.dumps([fresh_bead]))
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(reconcile, "run", fake_run)
    findings = []

    reconcile.reconcile_rig("/fixture/codeprobe", {}, now, findings, set())

    assert not any("MIRROR-STALE-CLOSE" in line for line in findings), (
        f"must not flag drift while still inside the push window, got: {findings}"
    )
