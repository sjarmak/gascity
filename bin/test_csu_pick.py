"""Behavioral tests for the Claude account utilization picker.

Run with: python3 -m pytest bin/test_csu_pick.py
"""
from __future__ import annotations

import json
import os
import subprocess
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest


SCRIPT = Path(__file__).resolve().parent / "csu_pick.sh"


def _account(number: int, seven_day: float) -> dict:
    return {
        "name": f"account{number}",
        "fetched_at": datetime.now(timezone.utc).isoformat(),
        "token_expires_at": (datetime.now(timezone.utc) + timedelta(hours=8)).isoformat(),
        "five_hour": {"utilization": seven_day},
        "seven_day": {"utilization": seven_day},
    }


def _pick(tmp_path: Path, utilization: dict[int, float], exclude: str | None) -> str:
    cache = tmp_path / "usage_cache.json"
    cache.write_text(
        json.dumps({"accounts": [_account(number, heat) for number, heat in utilization.items()]})
    )
    env = {
        **os.environ,
        "CSU_PICK_CACHE": str(cache),
        "CSU_PICK_MAX_AGE_MIN": "60",
        "CSU_PICK_REFRESH_CMD": "false",
        "CSU_PICK_TOP_K": "1",
        "CSU_PICK_EXPIRY_PRESSURE_HOURS": "0",
    }
    if exclude is None:
        env.pop("CSU_PICK_EXCLUDE", None)
    else:
        env["CSU_PICK_EXCLUDE"] = exclude
    result = subprocess.run(
        [str(SCRIPT)],
        check=True,
        capture_output=True,
        text=True,
        env=env,
        timeout=10,
    )
    return result.stdout.strip()


@pytest.mark.parametrize("exclude", [None, ""])
def test_account2_is_excluded_by_default(tmp_path: Path, exclude: str | None) -> None:
    assert _pick(tmp_path, {1: 50, 2: 1, 3: 20, 4: 30, 5: 40}, exclude) == "3"


def test_explicit_account_exclusions_remain_available(tmp_path: Path) -> None:
    assert _pick(tmp_path, {1: 50, 2: 1, 3: 2, 4: 3, 5: 40}, "2,claude-3") == "4"


def test_exhausted_accounts_remain_ineligible(tmp_path: Path) -> None:
    assert _pick(tmp_path, {1: 50, 2: 96, 3: 2, 4: 3, 5: 40}, "") == "3"
