"""Regression tests for the Claude OAuth expiry recovery order.

The executable has no ``.py`` suffix, so load it explicitly and redirect all
account homes and subprocesses to test doubles.  No test touches live tokens.
"""
from __future__ import annotations

import importlib.machinery
import importlib.util
import json
import subprocess
import sys
import time
from pathlib import Path
from types import SimpleNamespace

import pytest


BIN_DIR = Path(__file__).resolve().parent
CITY_DIR = BIN_DIR.parent
SCRIPT = BIN_DIR / "account-keepalive"


def _load_module():
    loader = importlib.machinery.SourceFileLoader(
        "account_keepalive_under_test", str(SCRIPT)
    )
    spec = importlib.util.spec_from_loader("account_keepalive_under_test", loader)
    module = importlib.util.module_from_spec(spec)
    sys.modules["account_keepalive_under_test"] = module
    loader.exec_module(module)
    return module


keepalive = _load_module()


def _write_expiry(home: Path, expiry_epoch: float) -> None:
    credentials = home / ".claude" / ".credentials.json"
    credentials.parent.mkdir(parents=True, exist_ok=True)
    credentials.write_text(
        json.dumps({"claudeAiOauth": {"expiresAt": int(expiry_epoch * 1000)}})
    )


@pytest.fixture
def isolated_accounts(tmp_path, monkeypatch):
    homes = tmp_path / "homes"
    homes.mkdir()
    monkeypatch.setattr(keepalive, "HOMES", homes)
    monkeypatch.setattr(keepalive, "CLAUDE_ACCOUNT", tmp_path / "claude-account")
    monkeypatch.setattr(
        keepalive,
        "STATE_PATH",
        tmp_path / "keepalive-state.json",
        raising=False,
    )
    return homes


def test_valid_token_near_expiry_is_quiet(isolated_accounts, monkeypatch, capsys):
    """A still-valid token need not rotate and must never trigger /ds-cred noise."""
    _write_expiry(isolated_accounts / "account1", time.time() + 30 * 60)
    launches = []
    mail = []
    monkeypatch.setattr(
        keepalive.subprocess,
        "run",
        lambda *args, **kwargs: launches.append((args, kwargs)),
    )
    monkeypatch.setattr(
        keepalive,
        "mail_mayor",
        lambda subject, body: not mail.append((subject, body)),
    )

    keepalive.main()

    assert launches == []
    assert mail == []
    assert "valid" in capsys.readouterr().out


def test_expired_token_is_recovered_before_alerting(
    isolated_accounts, monkeypatch
):
    account = isolated_accounts / "account2"
    _write_expiry(account, time.time() - 60)
    launches = []
    mail = []

    def recover(*args, **kwargs):
        launches.append((args, kwargs))
        _write_expiry(account, time.time() + 8 * 3600)

    monkeypatch.setattr(keepalive.subprocess, "run", recover)
    monkeypatch.setattr(
        keepalive,
        "mail_mayor",
        lambda subject, body: not mail.append((subject, body)),
    )

    keepalive.main()

    assert len(launches) == 1
    assert mail == []


def test_expired_token_that_cannot_recover_alerts_once(
    isolated_accounts, monkeypatch
):
    _write_expiry(isolated_accounts / "account3", time.time() - 60)
    launches = []
    mail = []
    monkeypatch.setattr(
        keepalive.subprocess,
        "run",
        lambda *args, **kwargs: launches.append((args, kwargs)),
    )
    monkeypatch.setattr(
        keepalive,
        "mail_mayor",
        lambda subject, body: not mail.append((subject, body)),
    )

    keepalive.main()

    assert len(launches) == 1
    assert len(mail) == 1
    assert "EXPIRED" in mail[0][0]
    assert "/ds-cred" in mail[0][1]


def test_failed_recovery_alert_is_deduplicated_by_expiry(
    isolated_accounts, monkeypatch
):
    _write_expiry(isolated_accounts / "account4", time.time() - 60)
    mail = []
    monkeypatch.setattr(keepalive.subprocess, "run", lambda *args, **kwargs: None)
    monkeypatch.setattr(
        keepalive,
        "mail_mayor",
        lambda subject, body: not mail.append((subject, body)),
    )

    keepalive.main()
    keepalive.main()

    assert len(mail) == 1


def test_failed_alert_delivery_is_retried(isolated_accounts, monkeypatch):
    _write_expiry(isolated_accounts / "account5", time.time() - 60)
    mail = []
    monkeypatch.setattr(keepalive.subprocess, "run", lambda *args, **kwargs: None)

    def fail_mail(subject, body):
        mail.append((subject, body))
        return False

    monkeypatch.setattr(
        keepalive,
        "mail_mayor",
        fail_mail,
    )

    keepalive.main()
    keepalive.main()

    assert len(mail) == 2


def test_mail_mayor_reports_delivery_result(monkeypatch):
    monkeypatch.setattr(
        keepalive.subprocess,
        "run",
        lambda *args, **kwargs: SimpleNamespace(returncode=0),
    )
    assert keepalive.mail_mayor("subject", "body") is True

    def time_out(*args, **kwargs):
        raise subprocess.TimeoutExpired("gc", 30)

    monkeypatch.setattr(keepalive.subprocess, "run", time_out)
    assert keepalive.mail_mayor("subject", "body") is False


def test_one_fifteen_minute_order_owns_expiry_recovery():
    """Do not reintroduce a separate pre-expiry pager with conflicting policy."""
    order = (CITY_DIR / "orders" / "account-keepalive.toml").read_text()

    assert 'schedule = "*/15 * * * *"' in order
    assert not (CITY_DIR / "orders" / "account-1h-warning.toml").exists()
    assert not (BIN_DIR / "account-1h-warning").exists()
