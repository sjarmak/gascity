"""Regression tests for blocked-routed-reaper notification deduplication."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "bin" / "blocked-routed-reaper"


def _write_executable(path: Path, body: str) -> None:
    path.write_text(body)
    path.chmod(0o755)


def _run_reaper(tmp_path: Path, beads: list[str]) -> subprocess.CompletedProcess[str]:
    mock_bin = tmp_path / "bin"
    mock_bin.mkdir(exist_ok=True)
    beads_file = tmp_path / "beads"
    beads_file.write_text("id\n" + "\n".join(beads) + "\n")

    _write_executable(
        mock_bin / "dolt",
        """#!/usr/bin/env bash
case "$*" in
  *"SHOW DATABASES"*) printf 'Database\\ngc\\n' ;;
  *) cat "$MOCK_BEADS_FILE" ;;
esac
""",
    )
    _write_executable(mock_bin / "bd", "#!/usr/bin/env bash\nexit 0\n")
    _write_executable(
        mock_bin / "gc",
        """#!/usr/bin/env bash
if [[ "$1 $2" == "session nudge" ]]; then
  printf '%s\\n' "$*" >> "$MOCK_NUDGE_LOG"
fi
exit 0
""",
    )

    env = {
        **os.environ,
        "PATH": f"{mock_bin}:{os.environ['PATH']}",
        "GC_STORE_ROOT": str(tmp_path / "city"),
        "BLOCKED_ROUTED_AUDIT_LOG": str(tmp_path / "audit.jsonl"),
        "BLOCKED_ROUTED_NOTIFY_STATE": str(tmp_path / "notified"),
        "MOCK_BEADS_FILE": str(beads_file),
        "MOCK_NUDGE_LOG": str(tmp_path / "nudges"),
    }
    return subprocess.run(
        [str(SCRIPT), "--apply", "--nudge-mayor"],
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )


def test_repeated_offender_set_notifies_only_for_new_beads(tmp_path: Path) -> None:
    first = _run_reaper(tmp_path, ["gc-a", "gc-b"])
    assert first.returncode == 0, first.stderr
    assert not (tmp_path / "nudges").exists()
    assert (tmp_path / "notified").read_text().splitlines() == ["gc|gc-a", "gc|gc-b"]

    repeated = _run_reaper(tmp_path, ["gc-a", "gc-b"])
    assert repeated.returncode == 0, repeated.stderr
    assert not (tmp_path / "nudges").exists()

    changed = _run_reaper(tmp_path, ["gc-a", "gc-b", "gc-c"])
    assert changed.returncode == 0, changed.stderr
    nudges = (tmp_path / "nudges").read_text().splitlines()
    assert len(nudges) == 1
    assert "1 newly seen" in nudges[0]
    assert (tmp_path / "notified").read_text().splitlines() == [
        "gc|gc-a",
        "gc|gc-b",
        "gc|gc-c",
    ]
