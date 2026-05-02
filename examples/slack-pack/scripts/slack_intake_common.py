"""Shared helpers for the slack pack scripts.

Mirrors the role of ``discord_intake_common`` in the upstream discord
pack but kept intentionally small for the v0 scaffold. Only the
helpers actually consumed by ``slack_chat_bind`` and
``slack_chat_reply_current`` live here.
"""

from __future__ import annotations

import json
import os
import pathlib
import sys
import urllib.error
import urllib.request
from typing import Any


CSRF_HEADER = "X-GC-Request"
DEFAULT_GC_API = "http://127.0.0.1:8372"
DEFAULT_ADAPTER_PUBLISH = "http://127.0.0.1:8766/publish"
DEFAULT_ADAPTER_ENV = pathlib.Path.home() / ".config" / "gc-slack-adapter" / "env"


def _maybe_load_adapter_env() -> None:
    """Load SLACK_* keys from the adapter's env file if not in os.environ.

    The adapter's run.sh reads ~/.config/gc-slack-adapter/env. Pack
    commands are invoked from inside agent sessions that don't inherit
    that file, so opportunistically read it here.
    """
    env_path = pathlib.Path(os.environ.get("GC_SLACK_ADAPTER_ENV", str(DEFAULT_ADAPTER_ENV)))
    if not env_path.exists():
        return
    needed = {"SLACK_WORKSPACE_ID", "SLACK_BOT_TOKEN", "SLACK_SIGNING_SECRET",
              "GC_API_BASE_URL"}
    if not needed - os.environ.keys():
        return
    try:
        for raw in env_path.read_text(encoding="utf-8").splitlines():
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            value = value.strip()
            if value and value[0] in ("'", '"') and value[-1] == value[0]:
                value = value[1:-1]
            if key and key not in os.environ:
                os.environ[key] = value
    except OSError:
        return


_maybe_load_adapter_env()


class GCAPIError(RuntimeError):
    """Raised when a gc API call fails."""


class AdapterError(RuntimeError):
    """Raised when the local Slack adapter rejects a publish."""


# --- environment / config -------------------------------------------------

def gc_api_base() -> str:
    return os.environ.get("GC_API_BASE_URL", DEFAULT_GC_API).rstrip("/")


def gc_city_name() -> str:
    name = os.environ.get("GC_CITY_NAME", "").strip()
    if not name:
        raise GCAPIError("GC_CITY_NAME is not set")
    return name


def adapter_publish_url() -> str:
    return os.environ.get("SLACK_ADAPTER_PUBLISH_URL", DEFAULT_ADAPTER_PUBLISH)


def pack_state_dir() -> pathlib.Path:
    """Per-pack state directory inside the active city.

    Falls back to the GC_CITY_PATH-rooted .gc/services/slack/data/ tree.
    """
    base = os.environ.get("GC_CITY_PATH", "").strip()
    if not base:
        raise GCAPIError("GC_CITY_PATH is not set; cannot resolve pack state")
    return pathlib.Path(base) / ".gc" / "services" / "slack" / "data"


def load_pack_config() -> dict[str, Any]:
    path = pack_state_dir() / "config.json"
    if not path.exists():
        return {"version": 1, "bindings": {}}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise GCAPIError(f"corrupt pack state at {path}: {exc}") from exc


def save_pack_config(cfg: dict[str, Any]) -> None:
    path = pack_state_dir() / "config.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(cfg, indent=2, sort_keys=True), encoding="utf-8")
    tmp.replace(path)


# --- HTTP helpers ---------------------------------------------------------

def _request(method: str, url: str, body: dict[str, Any] | None = None,
             *, csrf: bool = True, timeout: float = 30.0) -> dict[str, Any]:
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if csrf:
        headers[CSRF_HEADER] = "1"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise GCAPIError(f"{method} {url} -> {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise GCAPIError(f"{method} {url} failed: {exc}") from exc
    if not raw:
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise GCAPIError(f"{method} {url}: response is not JSON: {raw!r}") from exc


def gc_post(path: str, body: dict[str, Any]) -> dict[str, Any]:
    url = f"{gc_api_base()}/v0/city/{gc_city_name()}{path}"
    return _request("POST", url, body)


def gc_get(path: str) -> dict[str, Any]:
    url = f"{gc_api_base()}/v0/city/{gc_city_name()}{path}"
    return _request("GET", url, csrf=False)


# --- adapter publish ------------------------------------------------------

def publish_via_adapter(
    *,
    session_id: str,
    scope_id: str,
    provider: str,
    account_id: str,
    conversation_id: str,
    kind: str,
    text: str,
    reply_to_message_id: str = "",
    idempotency_key: str = "",
) -> dict[str, Any]:
    body = {
        "session_id": session_id,
        "conversation": {
            "scope_id": scope_id,
            "provider": provider,
            "account_id": account_id,
            "conversation_id": conversation_id,
            "kind": kind,
        },
        "text": text,
    }
    if reply_to_message_id:
        body["reply_to_message_id"] = reply_to_message_id
    if idempotency_key:
        body["idempotency_key"] = idempotency_key
    try:
        return _request("POST", adapter_publish_url(), body, csrf=False)
    except GCAPIError as exc:
        raise AdapterError(str(exc)) from exc


# --- session resolution ---------------------------------------------------

def current_session_id() -> str:
    """Best-effort session-id lookup from the calling environment.

    Pack commands are typically invoked from inside a session's tmux
    pane, where gc sets GC_SESSION_ID. Fall back to GC_SESSION_NAME +
    a gc API resolve if needed.
    """
    sid = os.environ.get("GC_SESSION_ID", "").strip()
    if sid:
        return sid
    name = os.environ.get("GC_SESSION_NAME", "").strip()
    if not name:
        raise GCAPIError(
            "neither GC_SESSION_ID nor GC_SESSION_NAME set; pass --session explicitly")
    # Resolve via list (good enough for v0).
    res = gc_get("/sessions")
    for entry in res.get("items", []):
        if entry.get("alias") == name or entry.get("session_name") == name:
            sid = entry.get("id", "")
            if sid:
                return sid
    raise GCAPIError(f"could not resolve session id for name {name!r}")


# --- inbound-event lookup -------------------------------------------------

def find_latest_inbound_for_session(session_id: str) -> dict[str, Any] | None:
    """Find the most recent extmsg.inbound event targeting session_id.

    Queries the gc events stream (HTTP, not SSE — single shot snapshot).
    Returns the parsed event dict, or None if no match found.
    """
    url = f"{gc_api_base()}/v0/city/{gc_city_name()}/events?type=extmsg.inbound&limit=50"
    raw = _request("GET", url, csrf=False).get("items", [])
    matches = [e for e in raw if (e.get("payload") or {}).get("target_session") == session_id]
    if not matches:
        return None
    return matches[-1]  # events are in chronological order


def look_up_binding(session_id: str) -> dict[str, Any] | None:
    """Resolve a session's most recent active extmsg binding."""
    res = gc_get(f"/extmsg/bindings?session_id={session_id}")
    items = res.get("items", [])
    for entry in reversed(items):
        if entry.get("Status") == "active":
            return entry.get("Conversation") or {}
    return None
