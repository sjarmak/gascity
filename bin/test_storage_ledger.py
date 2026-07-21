from __future__ import annotations

import importlib.machinery
import importlib.util
from pathlib import Path


SCRIPT = Path(__file__).with_name("storage-ledger")
LOADER = importlib.machinery.SourceFileLoader("storage_ledger", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def test_parse_worktree_paths_preserves_spaces() -> None:
    text = "worktree /tmp/main\nHEAD abc\n\nworktree /tmp/with space\nHEAD def\n"
    assert MODULE.parse_worktree_paths(text) == [Path("/tmp/main"), Path("/tmp/with space")]


def test_discover_generated_prunes_nested_content(tmp_path: Path) -> None:
    (tmp_path / "src" / "node_modules" / "pkg" / "dist").mkdir(parents=True)
    (tmp_path / "src" / "build" / "nested" / "target").mkdir(parents=True)
    (tmp_path / ".git" / "node_modules").mkdir(parents=True)
    found = MODULE.discover_generated([tmp_path])
    assert set(found) == {tmp_path / "src" / "node_modules", tmp_path / "src" / "build"}


def test_discover_generated_does_not_double_scan_nested_root(tmp_path: Path) -> None:
    nested = tmp_path / "worktrees" / "child"
    (nested / "node_modules").mkdir(parents=True)
    assert MODULE.discover_generated([tmp_path, nested]) == [nested / "node_modules"]


def test_load_previous_uses_last_record(tmp_path: Path) -> None:
    log = tmp_path / "ledger.jsonl"
    log.write_text('{"value": 1}\n{"value": 2}\n')
    assert MODULE.load_previous(log) == {"value": 2}
