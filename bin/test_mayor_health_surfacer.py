#!/usr/bin/env python3
"""Regression tests for mayor-health-surfacer finding classification."""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("mayor-health-surfacer")


class MayorHealthSurfacerTest(unittest.TestCase):
    def test_expected_health_soak_is_not_reported_as_stalled(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            bindir = Path(tmp)
            self._write_executable(
                bindir / "gc",
                """
                #!/usr/bin/env bash
                if [[ "$1 $2" == "rig list" ]]; then exit 0; fi
                if [[ "$1 $2 $3" == "session list --json" ]]; then
                  cat <<'JSON'
                {"sessions":[
                  {"id":"old-create","name":"old-create","template":"worker","state":"creating","created_at":"2020-01-01T00:00:00Z"},
                  {"id":"new-create","name":"new-create","template":"worker","state":"creating","created_at":"2999-01-01T00:00:00Z"},
                  {"id":"sleeping","name":"sleeping","template":"worker","state":"asleep","created_at":"2020-01-01T00:00:00Z"},
                  {"id":"degraded","name":"degraded","template":"worker","state":"degraded","created_at":"2020-01-01T00:00:00Z"},
                  {"id":"blocked","name":"blocked","template":"worker","state":"blocked","created_at":"2020-01-01T00:00:00Z"}
                ]}
                JSON
                  exit 0
                fi
                if [[ "$1 $2" == "session list" ]]; then
                  cat <<'TABLE'
                ID TEMPLATE STATE REASON
                old-create worker creating create
                new-create worker creating create
                sleeping worker asleep drained
                degraded worker degraded error
                blocked worker blocked error
                TABLE
                  exit 0
                fi
                exit 1
                """,
            )
            self._write_executable(
                bindir / "bd",
                """
                #!/usr/bin/env bash
                cat <<'JSON'
                [
                  {"id":"gc-soak","title":"expected soak","status":"in_progress","assignee":"worker","updated_at":"2020-01-01T00:00:00Z","labels":["health-soak"]},
                  {"id":"gc-stall","title":"real stall","status":"in_progress","assignee":"worker","updated_at":"2020-01-01T00:00:00Z","labels":[]}
                ]
                JSON
                """,
            )
            self._write_executable(
                bindir / "gc-capacity",
                """
                #!/usr/bin/env bash
                printf '%s\n' '{"accounts":[]}'
                """,
            )

            env = os.environ | {
                "PATH": f"{bindir}:{os.environ['PATH']}",
                "HUMAN_ASSIGNEES": "[]",
            }
            result = subprocess.run(
                [str(SCRIPT), "--json", "--stalled-hours", "1"],
                check=True,
                capture_output=True,
                text=True,
                env=env,
            )
            findings = [json.loads(line) for line in result.stdout.splitlines()]
            stalled_ids = {
                finding["id"]
                for finding in findings
                if finding["category"] == "WORK_STALLED"
            }
            self.assertEqual(stalled_ids, {"gc-stall"})

            session_ids = {
                finding["id"]
                for finding in findings
                if finding["category"] == "SESSIONS"
            }
            self.assertEqual(session_ids, {"old-create", "degraded", "blocked"})

    def _write_executable(self, path: Path, body: str) -> None:
        path.write_text(textwrap.dedent(body).lstrip())
        path.chmod(0o755)


if __name__ == "__main__":
    unittest.main()
