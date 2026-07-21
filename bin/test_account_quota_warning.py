"""Tests for routine capacity balancing in account-quota-warning."""
from __future__ import annotations

import importlib.machinery
import importlib.util
import sys
from pathlib import Path


_SCRIPT = Path(__file__).resolve().parent / "account-quota-warning"


def _load_module():
    loader = importlib.machinery.SourceFileLoader("quota_warning_under_test", str(_SCRIPT))
    spec = importlib.util.spec_from_loader("quota_warning_under_test", loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules["quota_warning_under_test"] = module
    loader.exec_module(module)
    return module


warning = _load_module()


def _cool_usage():
    return {
        "accounts": [{
            "name": "account1",
            "seven_day": {"utilization": 10.0, "resets_at": "window-1"},
        }]
    }


def test_main_balances_even_when_no_account_is_hot(monkeypatch):
    calls = []
    monkeypatch.setattr(
        warning,
        "load_json",
        lambda path: _cool_usage() if path == warning.USAGE_CACHE else {},
    )
    monkeypatch.setattr(warning, "pinned_or_always_agents_by_account", lambda: {})
    monkeypatch.setattr(warning, "AUTO_DRAIN", True)
    monkeypatch.setattr(warning, "auto_balance", lambda: calls.append(True) or [])

    assert warning.main() == 0
    assert calls == [True]


def test_routine_balance_is_silent_when_it_moves_agents(monkeypatch, capsys):
    monkeypatch.setattr(
        warning,
        "load_json",
        lambda path: _cool_usage() if path == warning.USAGE_CACHE else {},
    )
    monkeypatch.setattr(warning, "pinned_or_always_agents_by_account", lambda: {})
    monkeypatch.setattr(warning, "AUTO_DRAIN", True)
    monkeypatch.setattr(
        warning,
        "auto_balance",
        lambda: ["worker: claude-1 -> claude-2"],
    )
    monkeypatch.setattr(
        warning,
        "slack_post",
        lambda body: (_ for _ in ()).throw(AssertionError("routine move posted to Slack")),
    )
    monkeypatch.setattr(
        warning,
        "mail_mayor",
        lambda *args: (_ for _ in ()).throw(AssertionError("routine move mailed mayor")),
    )

    assert warning.main() == 0
    assert "capacity-balanced 1 agent(s)" in capsys.readouterr().out
