#!/usr/bin/env python3
import datetime as dt
import importlib.util
import importlib.machinery
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("executive-status-sync")
LOADER = importlib.machinery.SourceFileLoader("executive_status_sync", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def status_block(
    *,
    project="AOA",
    owner="aoa-pl",
    updated="2026-07-17T16:00:00-04:00",
    health="on-track",
    current="Validating the new evaluation path.",
    next_step="Run the larger comparison.",
    risk="none",
):
    return f"""---
tags: [executive-status-input]
---
# {project}

<!-- executive-status:start -->
project: {project}
owner: {owner}
updated: {updated}
health: {health}
current: {current}
next: {next_step}
risk: {risk}
<!-- executive-status:end -->
"""


class ParseStatusTest(unittest.TestCase):
    def test_parses_complete_status_block(self):
        status = MODULE.parse_status(status_block(), pathlib.Path("aoa-pl.md"))

        self.assertEqual(status.project, "AOA")
        self.assertEqual(status.owner, "aoa-pl")
        self.assertEqual(status.health, "on-track")
        self.assertEqual(status.next_step, "Run the larger comparison.")

    def test_rejects_missing_required_field(self):
        text = status_block().replace("current: Validating the new evaluation path.\n", "")

        with self.assertRaisesRegex(ValueError, "missing required field: current"):
            MODULE.parse_status(text, pathlib.Path("aoa-pl.md"))

    def test_rejects_invalid_health(self):
        text = status_block().replace("health: on-track", "health: excellent")

        with self.assertRaisesRegex(ValueError, "invalid health"):
            MODULE.parse_status(text, pathlib.Path("aoa-pl.md"))

    def test_rejects_multiline_or_oversized_fields(self):
        text = status_block(current="x" * 241)

        with self.assertRaisesRegex(ValueError, "current exceeds 240 characters"):
            MODULE.parse_status(text, pathlib.Path("aoa-pl.md"))


class RenderBriefTest(unittest.TestCase):
    def setUp(self):
        self.now = dt.datetime.fromisoformat("2026-07-17T18:00:00-04:00")

    def test_renders_stable_obsidian_brief_and_slack_summary(self):
        statuses = [
            MODULE.parse_status(status_block(), pathlib.Path("aoa-pl.md")),
            MODULE.parse_status(
                status_block(
                    project="Codeprobe",
                    owner="codeprobe-pl",
                    updated="2026-07-17T15:00:00-04:00",
                    health="blocked",
                    current="Execution is paused while the worker pool is restored.",
                    next_step="Resume the queued validation work.",
                    risk="No project throughput until capacity returns.",
                ),
                pathlib.Path("codeprobe-pl.md"),
            ),
        ]

        markdown = MODULE.render_obsidian(statuses, self.now)
        slack = MODULE.render_slack(statuses, self.now)

        self.assertIn("# Gas City Executive Brief", markdown)
        self.assertIn("| AOA | 🟢 On track |", markdown)
        self.assertIn("| Codeprobe | 🔴 Blocked |", markdown)
        self.assertIn("## Risks to watch", markdown)
        self.assertNotIn("aoa-pl", markdown)
        self.assertTrue(slack.startswith("🟢 FYI — no decision needed\n"))
        self.assertIn("*TL;DR:* 2 projects reporting", slack)
        self.assertIn("🔴 *Codeprobe*", slack)
        self.assertIn("No project throughput until capacity returns.", slack)

    def test_marks_old_inputs_stale(self):
        status = MODULE.parse_status(
            status_block(updated="2026-07-14T12:00:00-04:00"),
            pathlib.Path("aoa-pl.md"),
        )

        markdown = MODULE.render_obsidian([status], self.now)
        slack = MODULE.render_slack([status], self.now)

        self.assertIn("⚪ Stale", markdown)
        self.assertIn("1 stale", slack)

    def test_output_is_deterministic_for_same_inputs(self):
        status = MODULE.parse_status(status_block(), pathlib.Path("aoa-pl.md"))

        first = MODULE.render_obsidian([status], self.now)
        later = MODULE.render_obsidian(
            [status], self.now + dt.timedelta(minutes=20)
        )

        self.assertEqual(first, later)

    def test_single_project_summary_uses_singular_noun(self):
        status = MODULE.parse_status(status_block(), pathlib.Path("aoa-pl.md"))

        slack = MODULE.render_slack([status], self.now)

        self.assertIn("*TL;DR:* 1 project reporting", slack)

    def test_load_statuses_reports_invalid_inputs_without_hiding_them(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "valid.md").write_text(status_block(), encoding="utf-8")
            (root / "invalid.md").write_text("not a status", encoding="utf-8")

            statuses, errors = MODULE.load_statuses(root)

        self.assertEqual(len(statuses), 1)
        self.assertEqual(len(errors), 1)
        self.assertIn("invalid.md", errors[0])

    def test_full_portfolio_slack_summary_fits_without_cutting_projects(self):
        statuses = [
            MODULE.parse_status(
                status_block(
                    project=f"Project {index:02d}",
                    owner=f"project-{index:02d}-pl",
                    health="at-risk" if index % 3 == 0 else "on-track",
                    current=f"Current outcome for project {index} " + "x" * 100,
                    risk="A material outcome is at risk " + "y" * 100,
                ),
                pathlib.Path(f"project-{index:02d}-pl.md"),
            )
            for index in range(24)
        ]

        slack = MODULE.render_slack(statuses, self.now)

        self.assertLessEqual(len(slack), MODULE.MAX_SLACK_LENGTH)
        self.assertIn("*Project 23*", slack)
        self.assertFalse(slack.endswith("…"))


if __name__ == "__main__":
    unittest.main()
