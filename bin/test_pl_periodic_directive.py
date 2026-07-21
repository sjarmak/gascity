#!/usr/bin/env python3
import os
import pathlib
import subprocess
import unittest


SCRIPT = pathlib.Path(__file__).with_name("pl-periodic-directive")


class PeriodicDirectiveTest(unittest.TestCase):
    def test_debug_map_discovers_current_work_dir_key(self):
        environment = dict(os.environ)
        environment["PL_DIRECTIVE_DEBUG_MAP"] = "1"

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
