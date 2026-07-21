"""Keep active authoring instructions aligned with the formula-v2 contract."""
from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent


def _active_instruction_paths() -> list[Path]:
    paths = [
        ROOT / "bin/maintenance-cycle",
        ROOT / "services/temporal-maintenance/prompts/author.md",
        ROOT / "docs/design/autonomous-pr-ship.md",
        ROOT / "docs/design/autonomous-pr-ship-staging.md",
    ]
    paths.extend(sorted((ROOT / "agents").glob("*-pl/prompt.template.md")))
    return [path for path in paths if "mol-pr-from-issue" in path.read_text()]


def test_active_instructions_never_pass_reserved_issue_var():
    offenders = []
    for path in _active_instruction_paths():
        for number, line in enumerate(path.read_text().splitlines(), start=1):
            if "mol-pr-from-issue" in line and "--var issue=" in line:
                offenders.append(f"{path.relative_to(ROOT)}:{number}")
    assert offenders == []


def test_active_contract_tables_name_issue_number():
    reserved_contract = re.compile(
        r"mol-pr-from-issue.*?`issue`\s*\(required\)",
    )
    offenders = []
    for path in _active_instruction_paths():
        for number, line in enumerate(path.read_text().splitlines(), start=1):
            if reserved_contract.search(line):
                offenders.append(f"{path.relative_to(ROOT)}:{number}")
    assert offenders == []
