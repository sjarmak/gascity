#!/usr/bin/env python3
"""Scope tests for claude-account startup-modal pre-acceptance."""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("claude-account")


class ClaudeAccountModalScopeTest(unittest.TestCase):
    def test_external_includes_are_preaccepted_only_for_pool_workers(self) -> None:
        named = self._launch("city-infra-pl")
        pool = self._launch("/home/ds/gascity/polecat")

        self.assertTrue(named["hasTrustDialogAccepted"])
        self.assertNotIn("hasClaudeMdExternalIncludesApproved", named)
        self.assertNotIn("hasClaudeMdExternalIncludesWarningShown", named)

        self.assertTrue(pool["hasTrustDialogAccepted"])
        self.assertTrue(pool["hasClaudeMdExternalIncludesApproved"])
        self.assertTrue(pool["hasClaudeMdExternalIncludesWarningShown"])

    def _launch(self, template: str) -> dict[str, object]:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            account_home = home / ".claude-homes" / "account1"
            config = account_home / ".claude"
            config.mkdir(parents=True)
            (config / ".credentials.json").write_text("{}")
            (account_home / ".claude.json").write_text(
                json.dumps({"hasCompletedOnboarding": True, "projects": {}})
            )
            fake_bin = root / "bin"
            fake_bin.mkdir()
            fake_claude = fake_bin / "claude"
            fake_claude.write_text("#!/usr/bin/env bash\nexit 0\n")
            fake_claude.chmod(0o755)
            work_dir = root / "work"
            work_dir.mkdir()

            env = os.environ | {
                "HOME": str(home),
                "PATH": f"{fake_bin}:{os.environ['PATH']}",
                "GC_TEMPLATE": template,
                "GC_WORK_DIR": str(work_dir),
            }
            subprocess.run([str(SCRIPT), "1"], check=True, env=env)
            state = json.loads((account_home / ".claude.json").read_text())
            return state["projects"][str(work_dir)]


if __name__ == "__main__":
    unittest.main()
