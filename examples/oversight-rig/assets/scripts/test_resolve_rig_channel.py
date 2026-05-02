"""Tests for resolve_rig_channel — pure resolver logic, no live HTTP.

The module is meant to be runnable as both a CLI and a library; the
tests target the library surface (``resolve``, ``find_project_lead_session``,
``pick_active_binding``) and the CLI exit-code mapping.
"""

from __future__ import annotations

import io
import pathlib
import sys
from typing import Any

import pytest

SCRIPTS_DIR = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))


@pytest.fixture(autouse=True)
def _isolate_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GC_API_BASE_URL", "http://127.0.0.1:8372")
    monkeypatch.setenv("GC_CITY_NAME", "test-city")


def _import():
    sys.modules.pop("resolve_rig_channel", None)
    import resolve_rig_channel  # type: ignore
    return resolve_rig_channel


def _session(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "id": "gc-77139",
        "alias": "geo/oversight-rig.project-lead",
        "session_name": "geo/oversight-rig.project-lead",
        "template": "geo/oversight-rig.project-lead",
        "rig": "geo",
        "state": "active",
    }
    base.update(overrides)
    return base


def _binding(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "ID": "gc-83357",
        "SessionID": "gc-77139",
        "Status": "active",
        "Conversation": {
            "scope_id": "test-city",
            "provider": "slack",
            "account_id": "T0TESTWS",
            "conversation_id": "C0123ROOM",
            "kind": "room",
        },
    }
    base.update(overrides)
    return base


def test_resolve_success_returns_session_and_conversation():
    mod = _import()
    out = mod.resolve(
        "geo",
        fetch_sessions=lambda: [_session()],
        fetch_bindings=lambda _sid: [_binding()],
    )
    assert out == {
        "session_id": "gc-77139",
        "conversation": {
            "scope_id": "test-city",
            "provider": "slack",
            "account_id": "T0TESTWS",
            "conversation_id": "C0123ROOM",
            "kind": "room",
        },
    }


def test_find_project_lead_prefers_rig_match_over_alias_fallback():
    mod = _import()
    sessions = [
        # Pure alias-based fallback match — score 1.
        _session(id="gc-old", rig="", alias="geo/oversight-rig.project-lead",
                 session_name="", template="", state="active"),
        # Strong rig-based match — score 0.
        _session(id="gc-new", rig="geo", alias="geo/oversight-rig.project-lead",
                 state="active"),
    ]
    out = mod.find_project_lead_session(sessions, "geo")
    assert out is not None and out["id"] == "gc-new"


def test_find_project_lead_prefers_active_over_closed():
    mod = _import()
    sessions = [
        _session(id="gc-closed", state="closed"),
        _session(id="gc-active", state="active"),
    ]
    out = mod.find_project_lead_session(sessions, "geo")
    assert out is not None and out["id"] == "gc-active"


def test_find_project_lead_returns_none_for_unknown_rig():
    mod = _import()
    sessions = [_session(id="gc-77139", rig="geo")]
    assert mod.find_project_lead_session(sessions, "codescalebench") is None


def test_find_project_lead_skips_unrelated_sessions():
    mod = _import()
    sessions = [
        # mayor session under "oversight-rig" rig — not a project-lead.
        {
            "id": "gc-mayor",
            "rig": "oversight-rig",
            "alias": "oversight-rig.mayor",
            "session_name": "oversight-rig.mayor",
            "template": "oversight-rig.mayor",
            "state": "active",
        },
    ]
    assert mod.find_project_lead_session(sessions, "oversight-rig") is None


def test_pick_active_binding_returns_most_recent_active():
    mod = _import()
    bindings = [
        _binding(ID="b1", Status="active", Conversation={
            "scope_id": "x", "provider": "slack", "account_id": "a",
            "conversation_id": "C-OLD", "kind": "room",
        }),
        _binding(ID="b2", Status="inactive"),
        _binding(ID="b3", Status="active", Conversation={
            "scope_id": "x", "provider": "slack", "account_id": "a",
            "conversation_id": "C-NEW", "kind": "room",
        }),
    ]
    out = mod.pick_active_binding(bindings)
    assert out is not None and out["Conversation"]["conversation_id"] == "C-NEW"


def test_pick_active_binding_returns_none_when_all_inactive():
    mod = _import()
    bindings = [_binding(Status="inactive"), _binding(Status="closed")]
    assert mod.pick_active_binding(bindings) is None


def test_resolve_raises_when_no_session():
    mod = _import()
    with pytest.raises(mod.ResolveError, match="no project-lead session"):
        mod.resolve(
            "missing-rig",
            fetch_sessions=lambda: [_session()],
            fetch_bindings=lambda _sid: [],
        )


def test_resolve_raises_when_no_active_binding():
    mod = _import()
    with pytest.raises(mod.ResolveError, match="no active binding"):
        mod.resolve(
            "geo",
            fetch_sessions=lambda: [_session()],
            fetch_bindings=lambda _sid: [_binding(Status="inactive")],
        )


def test_main_exit_code_maps_no_session_to_2(monkeypatch: pytest.MonkeyPatch,
                                             capsys: pytest.CaptureFixture[str]):
    mod = _import()
    monkeypatch.setattr(mod, "resolve", lambda _rig: (_ for _ in ()).throw(
        mod.ResolveError("no project-lead session for rig 'unknown'")))
    rc = mod.main(["unknown"])
    assert rc == mod.EXIT_NO_SESSION


def test_main_exit_code_maps_no_binding_to_3(monkeypatch: pytest.MonkeyPatch):
    mod = _import()
    monkeypatch.setattr(mod, "resolve", lambda _rig: (_ for _ in ()).throw(
        mod.ResolveError("project-lead session gc-1 for rig 'geo' has no active binding")))
    rc = mod.main(["geo"])
    assert rc == mod.EXIT_NO_BINDING


def test_main_emits_json_on_success(monkeypatch: pytest.MonkeyPatch,
                                    capsys: pytest.CaptureFixture[str]):
    mod = _import()
    monkeypatch.setattr(mod, "resolve", lambda rig: {
        "session_id": "gc-77139",
        "conversation": {
            "scope_id": "test-city",
            "provider": "slack",
            "account_id": "T0TESTWS",
            "conversation_id": "C0123ROOM",
            "kind": "room",
        },
    })
    rc = mod.main(["geo"])
    assert rc == mod.EXIT_OK
    captured = capsys.readouterr()
    import json
    parsed = json.loads(captured.out)
    assert parsed["session_id"] == "gc-77139"
    assert parsed["conversation"]["conversation_id"] == "C0123ROOM"
