#!/usr/bin/env python3
"""Resolve the publishing target for a rig's rollup delivery.

Given a ``rig`` label (e.g. ``geo``), find the project-lead session that
owns the rig's bound conversation and emit the publish parameters needed
by ``POST /v0/city/{city}/extmsg/outbound``.

Resolution order:

1. ``GET /v0/city/{city}/sessions`` — find the session whose
   ``rig`` matches and whose ``alias`` (or ``session_name``) ends in
   ``oversight-rig.project-lead``.
2. ``GET /v0/city/{city}/extmsg/bindings?session_id=<id>`` — pick the
   most recent active binding (the bind-room participant binding).
3. Print JSON ``{session_id, conversation: {...}}`` to stdout.

Exit codes:

* ``0`` — resolution succeeded; JSON written to stdout.
* ``1`` — usage / required env missing.
* ``2`` — no project-lead session exists for ``rig``.
* ``3`` — project-lead exists but has no active extmsg binding.

The bash caller treats exit codes 2 and 3 as "fall back to the legacy
city-wide ``GC_OVERSIGHT_*`` env vars", and any other non-zero as a
hard failure.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any

DEFAULT_GC_API = "http://127.0.0.1:8372"
PROJECT_LEAD_SUFFIX = "oversight-rig.project-lead"

EXIT_OK = 0
EXIT_USAGE = 1
EXIT_NO_SESSION = 2
EXIT_NO_BINDING = 3


class ResolveError(RuntimeError):
    """Raised when the resolver hits an unrecoverable error."""


def _api_base() -> str:
    return os.environ.get("GC_API_BASE_URL", DEFAULT_GC_API).rstrip("/")


def _city_name() -> str:
    name = os.environ.get("GC_CITY_NAME", "").strip()
    if not name:
        raise ResolveError("GC_CITY_NAME is not set")
    return name


def _http_get(url: str, *, timeout: float = 10.0) -> dict[str, Any]:
    """GET ``url`` and return the decoded JSON body."""
    req = urllib.request.Request(url, headers={"Accept": "application/json"}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise ResolveError(f"GET {url} -> {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise ResolveError(f"GET {url} failed: {exc}") from exc
    if not raw:
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ResolveError(f"GET {url}: response is not JSON: {raw!r}") from exc


def find_project_lead_session(sessions: list[dict[str, Any]], rig: str) -> dict[str, Any] | None:
    """Pick the project-lead session for ``rig`` from a sessions list.

    Two matching strategies are accepted because both forms appear in
    the wild and either alone could break under naming drift:

    * ``rig == <rig>`` AND template/alias/session_name ending in
      ``oversight-rig.project-lead``.
    * fallback: alias or session_name equal to
      ``<rig>/oversight-rig.project-lead`` exactly.

    Active sessions are preferred over closed ones; among ties the
    first match wins (sessions list is ordered by the supervisor).
    """
    rig = rig.strip()
    if not rig:
        return None

    candidates: list[tuple[int, dict[str, Any]]] = []
    exact_alias = f"{rig}/{PROJECT_LEAD_SUFFIX}"
    for entry in sessions:
        if not isinstance(entry, dict):
            continue
        entry_rig = (entry.get("rig") or "").strip()
        alias = (entry.get("alias") or "").strip()
        session_name = (entry.get("session_name") or "").strip()
        template = (entry.get("template") or "").strip()
        ends_with_pl = (
            alias.endswith(PROJECT_LEAD_SUFFIX)
            or session_name.endswith(PROJECT_LEAD_SUFFIX)
            or template.endswith(PROJECT_LEAD_SUFFIX)
        )
        if entry_rig == rig and ends_with_pl:
            score = 0  # strongest match
        elif alias == exact_alias or session_name == exact_alias:
            score = 1  # exact alias fallback
        else:
            continue
        # Prefer non-closed states.
        state = (entry.get("state") or "").strip().lower()
        if state == "closed":
            score += 10
        candidates.append((score, entry))

    if not candidates:
        return None
    candidates.sort(key=lambda item: item[0])
    return candidates[0][1]


def pick_active_binding(bindings: list[dict[str, Any]]) -> dict[str, Any] | None:
    """Return the most recent active binding from a bindings list.

    Mirrors the discord/slack pack convention: scan from newest to
    oldest, return the first one whose ``Status`` is ``active``.
    """
    for entry in reversed(bindings):
        if not isinstance(entry, dict):
            continue
        if (entry.get("Status") or "").strip().lower() == "active":
            return entry
    return None


def resolve(
    rig: str,
    *,
    fetch_sessions: Any = None,
    fetch_bindings: Any = None,
) -> dict[str, Any]:
    """Resolve ``rig`` to a publish target.

    ``fetch_sessions`` and ``fetch_bindings`` are injected for tests;
    in production they default to live HTTP calls.
    """
    rig = rig.strip()
    if not rig:
        raise ResolveError("rig is empty")

    if fetch_sessions is None:
        def fetch_sessions() -> list[dict[str, Any]]:
            base = _api_base()
            city = _city_name()
            return list(_http_get(f"{base}/v0/city/{city}/sessions").get("items", []))

    if fetch_bindings is None:
        def fetch_bindings(session_id: str) -> list[dict[str, Any]]:
            base = _api_base()
            city = _city_name()
            return list(_http_get(
                f"{base}/v0/city/{city}/extmsg/bindings?session_id={session_id}"
            ).get("items", []))

    sessions = fetch_sessions()
    session = find_project_lead_session(sessions, rig)
    if session is None:
        raise ResolveError(f"no project-lead session for rig {rig!r}")

    session_id = (session.get("id") or "").strip()
    if not session_id:
        raise ResolveError(f"project-lead session for rig {rig!r} has no id")

    bindings = fetch_bindings(session_id)
    binding = pick_active_binding(bindings)
    if binding is None:
        raise ResolveError(
            f"project-lead session {session_id} for rig {rig!r} has no active binding")

    conversation = binding.get("Conversation") or {}
    return {
        "session_id": session_id,
        "conversation": {
            "scope_id": conversation.get("scope_id", ""),
            "provider": conversation.get("provider", ""),
            "account_id": conversation.get("account_id", ""),
            "conversation_id": conversation.get("conversation_id", ""),
            "kind": conversation.get("kind", ""),
        },
    }


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("rig", help="rig label (e.g. 'geo')")
    args = parser.parse_args(argv)

    try:
        result = resolve(args.rig)
    except ResolveError as exc:
        msg = str(exc)
        sys.stderr.write(msg + "\n")
        if msg.startswith("no project-lead session"):
            return EXIT_NO_SESSION
        if "no active binding" in msg:
            return EXIT_NO_BINDING
        return EXIT_USAGE
    json.dump(result, sys.stdout)
    sys.stdout.write("\n")
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
