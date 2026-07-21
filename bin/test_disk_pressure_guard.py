#!/usr/bin/env python3
from __future__ import annotations

import importlib.machinery
import importlib.util
import json
from pathlib import Path
import sys


SCRIPT = Path(__file__).with_name("disk-pressure-guard")
LOADER = importlib.machinery.SourceFileLoader("disk_pressure_guard", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def test_metrics_disabled_reads_only_metrics_block(tmp_path: Path) -> None:
    config = tmp_path / "config.yaml"
    config.write_text("other:\n  disabled: true\nmetrics:\n  disabled: false\n")
    assert MODULE.metrics_disabled(config) is False
    config.write_text("metrics:\n  disabled: true\n")
    assert MODULE.metrics_disabled(config) is True


def test_trim_telemetry_queue_recreates_empty_private_directory(tmp_path: Path) -> None:
    queue = tmp_path / "eventsData"
    queue.mkdir()
    (queue / "one.evtq").write_bytes(b"telemetry")

    freed = MODULE.trim_telemetry_queue(str(queue))

    assert freed > 0
    assert queue.is_dir()
    assert list(queue.iterdir()) == []
    assert queue.stat().st_mode & 0o777 == 0o700
    assert not list(tmp_path.glob("eventsData.reclaim-*"))


def test_pressure_boundaries_include_inodes() -> None:
    p = MODULE.pressure_level
    assert p(300, 84.9) == "ok"
    assert p(299.9, 0) == "warning"
    assert p(200, 85) == "warning"
    assert p(199.9, 0) == "action"
    assert p(500, 90) == "action"
    assert p(99.9, 0) == "critical"
    assert p(500, 95) == "critical"


def run_main(monkeypatch, capsys, tmp_path: Path, *, disabled: bool, apply: bool):
    monkeypatch.setattr(MODULE, "AUDIT_LOG", str(tmp_path / "audit.jsonl"))
    monkeypatch.setattr(MODULE, "CACHES", [])
    monkeypatch.setattr(MODULE, "BD_EVENTS", str(tmp_path / "eventsData"))
    monkeypatch.setattr(MODULE, "free_gb", lambda: 500.0)
    monkeypatch.setattr(MODULE, "inode_used_pct", lambda: 10.0)
    monkeypatch.setattr(MODULE, "dir_gb", lambda path: 2.0 if path == MODULE.BD_EVENTS else 0.0)
    monkeypatch.setattr(MODULE, "measured_dir_gb", lambda path: (2.0, True) if path == MODULE.BD_EVENTS else (0.0, True))
    monkeypatch.setattr(MODULE, "detached_telemetry_gb", lambda: (0.0, True))
    monkeypatch.setattr(MODULE, "trim_detached_telemetry", lambda: 0.0)
    monkeypatch.setattr(MODULE, "metrics_disabled", lambda: disabled)
    called = []
    monkeypatch.setattr(MODULE, "trim_telemetry_queue", lambda: called.append(True) or 2.0)
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), "--json"] + (["--apply"] if apply else []))
    assert MODULE.main() == 0
    return json.loads(capsys.readouterr().out), called


def test_metrics_enabled_queue_is_report_only(monkeypatch, capsys, tmp_path: Path) -> None:
    result, called = run_main(monkeypatch, capsys, tmp_path, disabled=False, apply=True)
    assert called == []
    assert result["trimmed"][0]["report_only_gb"] == 2.0
    assert result["summary"]["freed_gb"] == 0.0


def test_disabled_queue_dry_run_never_mutates(monkeypatch, capsys, tmp_path: Path) -> None:
    result, called = run_main(monkeypatch, capsys, tmp_path, disabled=True, apply=False)
    assert called == []
    assert result["trimmed"][0]["would_free_gb"] == 2.0


def test_disabled_queue_apply_uses_atomic_trimmer(monkeypatch, capsys, tmp_path: Path) -> None:
    result, called = run_main(monkeypatch, capsys, tmp_path, disabled=True, apply=True)
    assert called == [True]
    assert result["trimmed"][0]["freed_gb"] == 2.0


def test_detached_queue_is_retried_and_symlink_refused(tmp_path: Path) -> None:
    queue = tmp_path / "eventsData"
    queue.mkdir()
    residual = tmp_path / "eventsData.reclaim-123"
    residual.mkdir()
    (residual / "old.evtq").write_bytes(b"old")
    outside = tmp_path / "outside"
    outside.mkdir()
    symlink = tmp_path / "eventsData.reclaim-link"
    symlink.symlink_to(outside, target_is_directory=True)

    assert MODULE.trim_detached_telemetry(str(queue)) > 0
    assert not residual.exists()
    assert symlink.is_symlink()
    assert outside.is_dir()
