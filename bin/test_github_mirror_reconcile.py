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
"""
from __future__ import annotations

import importlib.machinery
import importlib.util
import json
import sys
from datetime import datetime, timezone
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
