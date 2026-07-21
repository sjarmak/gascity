#!/usr/bin/env python3
"""Regression tests for resource-observability swap pressure signals."""

from __future__ import annotations

import importlib.util
import json
import stat
import tempfile
import unittest
from pathlib import Path
from importlib.machinery import SourceFileLoader


SCRIPT = Path(__file__).with_name("resource-observability-sample")
SPEC = importlib.util.spec_from_loader(
    "resource_observability_sample",
    SourceFileLoader("resource_observability_sample", str(SCRIPT)),
)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class SwapPressureTest(unittest.TestCase):
    def _sample(self, *, free_m: float, swap_in: int, swap_out: int) -> dict:
        return {
            "mem_available_g": 20.0,
            "swap_free_m": free_m,
            "swap_in_pages": swap_in,
            "swap_out_pages": swap_out,
            "mcp_servers": {},
            "hot_loop_procs": [],
            "supervisor_cgroup_g": 1.0,
        }

    def test_full_but_cold_swap_is_not_an_active_pressure_breach(self) -> None:
        prev = self._sample(free_m=1.0, swap_in=100_000, swap_out=50_000)
        cur = self._sample(free_m=1.0, swap_in=100_010, swap_out=50_000)
        keys = {key for key, _ in MODULE.evaluate(cur, prev)}
        self.assertNotIn("swap_churn", keys)
        self.assertNotIn("swap_free", keys)

    def test_full_swap_with_sustained_churn_is_a_pressure_breach(self) -> None:
        pages_for_threshold = int(
            MODULE.SWAP_CHURN_MIN_M * 1024 * 1024 / MODULE.PAGE_SIZE
        )
        prev = self._sample(free_m=1.0, swap_in=100_000, swap_out=50_000)
        cur = self._sample(
            free_m=1.0,
            swap_in=100_000 + pages_for_threshold,
            swap_out=50_000,
        )
        keys = {key for key, _ in MODULE.evaluate(cur, prev)}
        self.assertIn("swap_churn", keys)


class PressureSnapshotTest(unittest.TestCase):
    def test_metrics_sample_strips_all_argv_derived_fields(self) -> None:
        sample = {
            "top_rss": [{"pid": 1, "cmd": "server --token SENTINEL_SECRET"}],
            "hot_loop_procs": [{"pid": 1, "cpu": 300, "cmd": "SENTINEL_SECRET"}],
            "mcp_servers": {"SENTINEL_SECRET": {"procs": 1, "rss_g": 1.0}},
        }
        safe = MODULE._secret_safe_sample(sample)
        self.assertNotIn("SENTINEL_SECRET", json.dumps(safe))
        self.assertEqual(safe["mcp_servers"], {"other-mcp": {"procs": 1, "rss_g": 1.0}})

    def test_snapshot_preserves_bounded_process_attribution_without_arguments(self) -> None:
        process_rows = [
            {
                "pid": 123,
                "ppid": 45,
                "cpu": 17.5,
                "rss_kb": 8192,
                "elapsed_s": 90,
                "comm": "SENTINEL_SECRET",
                "cgroup": "/user.slice/test.scope",
                "exe": "/usr/bin/bd",
                "cwd": "/tmp/SENTINEL_SECRET",
            }
        ]

        with tempfile.TemporaryDirectory() as tmp:
            path = MODULE.record_pressure_snapshot(
                {
                    "ts": "2026-07-21T12:00:00+0000",
                    "mem_available_g": 2.0,
                    "top_rss": [{"pid": 123, "cmd": "server --token SENTINEL_SECRET"}],
                    "hot_loop_procs": [{"pid": 123, "cmd": "server SENTINEL_SECRET"}],
                    "mcp_servers": {"--mcp-token=SENTINEL_SECRET": {"procs": 1, "rss_g": 1.0}},
                },
                [
                    ("mem_available", "unsafe detail SENTINEL_SECRET"),
                    ("mcp_fanout_SENTINEL_SECRET", "unsafe MCP detail"),
                ],
                out_dir=Path(tmp),
                process_rows=process_rows,
                memory_psi="some avg10=1.00",
                supervisor={"MemoryCurrent": "1234", "MemoryPeak": "5678"},
            )

            payload = json.loads(path.read_text())
            self.assertEqual(
                payload["processes"],
                [{
                    "pid": 123,
                    "ppid": 45,
                    "cpu": 17.5,
                    "rss_kb": 8192,
                    "elapsed_s": 90,
                    "process": "bd",
                    "cgroup": "/user.slice/test.scope",
                }],
            )
            self.assertEqual(payload["breaches"], ["mem_available", "mcp_fanout_other-mcp"])
            self.assertEqual(payload["sample"]["mcp_servers"], {"other-mcp": {"procs": 1, "rss_g": 1.0}})
            self.assertNotIn("args", payload["processes"][0])
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertNotIn("SENTINEL_SECRET", path.read_text())

    def test_unknown_mcp_command_is_bucketed_under_fixed_label(self) -> None:
        self.assertEqual(
            MODULE._mcp_server_of("worker --mcp-server-token=SENTINEL_SECRET"),
            "other-mcp",
        )


if __name__ == "__main__":
    unittest.main()
