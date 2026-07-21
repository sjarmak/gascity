#!/usr/bin/env python3
import os
import pathlib
import re
import subprocess
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("pl-periodic-directive")
AOA_AGENT = SCRIPT.parent.parent / "agents" / "aoa-pl" / "agent.toml"


class PeriodicDirectiveTest(unittest.TestCase):
    def test_debug_map_discovers_current_work_dir_key(self) -> None:
        # Hermetic since 2026-07-21 (found red during the gc-f5zg selftest
        # wiring): the debug map matches PL work_dirs against `gc rig list`
        # filtered to UNSUSPENDED rigs, so calling the real gc made this test
        # depend on live city state — it went red the moment the 07-21 cleanup
        # pause suspended the aoa rig. Stub gc with a fixture rig list instead;
        # the aoa path still comes from the real agent.toml, so the
        # dir-matching logic under test is exercised against real config.
        match = re.search(r'^work_dir = "(.*)"$', AOA_AGENT.read_text(), re.M)
        self.assertIsNotNone(match, "aoa-pl agent.toml lost its work_dir")
        aoa_dir = match.group(1)

        with tempfile.TemporaryDirectory() as tmp:
            stub = pathlib.Path(tmp) / "gc"
            stub.write_text(
                "#!/usr/bin/env bash\n"
                'if [[ "${1:-} ${2:-}" == "rig list" ]]; then\n'
                "  cat <<'EOF'\n"
                '{"rigs":[{"name":"aoa","path":"' + aoa_dir + '","hq":false,"suspended":false}]}\n'
                "EOF\n"
                "fi\n"
                "exit 0\n"
            )
            stub.chmod(0o755)

            environment = dict(os.environ)
            environment["PL_DIRECTIVE_DEBUG_MAP"] = "1"
            environment["PATH"] = f"{tmp}:{environment['PATH']}"

            result = subprocess.run(
                [str(SCRIPT), "EXECUTIVE_STATUS"],
                capture_output=True,
                text=True,
                env=environment,
                timeout=30,
                check=False,
            )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("aoa-pl", result.stdout)
        self.assertIn("-> aoa", result.stdout)


if __name__ == "__main__":
    unittest.main()
