#!/usr/bin/env python3
"""Bind a Slack room/channel to one or more named gc sessions.

Creates a launcher-mode conversation group rooted at the Slack channel
and adds each named session as a participant. With ``--enable-peer-fanout``
or any of the related fanout flags, the group is created with a fanout
policy preserved on the group record.

Why a group instead of a binding per session: gc bindings are 1:1 by
conversation (a second ``Bind`` call returns ``ErrBindingConflict``).
Memberships are 1:N and are what drives peer-fanout system reminders
(``extmsgNotifyMembers``). The simplest way to create N memberships
through the public API today is via group participants.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from typing import Any

import slack_intake_common as common


def _slack_workspace_id() -> str:
    val = os.environ.get("SLACK_WORKSPACE_ID", "").strip()
    if not val:
        raise SystemExit("SLACK_WORKSPACE_ID must be set in the slack adapter env")
    return val


def _parse_handle_overrides(values: list[str]) -> dict[str, str]:
    """Parse repeated ``--handle handle=session`` flags.

    Returns a map of session_name -> handle. Raises SystemExit on
    malformed input. Handles are normalized to lowercase on the gc
    side; we don't pre-normalize here because the API does it for us.
    """
    overrides: dict[str, str] = {}
    for raw in values:
        if "=" not in raw:
            raise SystemExit(f"--handle expects HANDLE=SESSION, got: {raw!r}")
        handle, _, session = raw.partition("=")
        handle = handle.strip()
        session = session.strip()
        if not handle or not session:
            raise SystemExit(f"--handle expects HANDLE=SESSION, got: {raw!r}")
        if session in overrides:
            raise SystemExit(f"--handle session {session!r} specified twice")
        overrides[session] = handle
    return overrides


def _default_handle_for_session(session_name: str) -> str:
    """Derive a participant handle from a session alias or id.

    For aliases like ``geo/oversight-rig.project-lead``, take the last
    dot-separated segment of the path tail (``project-lead``) and
    prefix with the directory (``geo-project-lead``). For unstructured
    ids like ``gc-83347`` we fall back to the raw id; the caller is
    expected to override via ``--handle`` in that case.
    """
    if "/" in session_name:
        head, tail = session_name.split("/", 1)
        last = tail.rsplit(".", 1)[-1]
        return f"{head}-{last}"
    if "." in session_name:
        return session_name.rsplit(".", 1)[-1]
    return session_name


def build_conversation_ref(
    *, conversation_id: str, kind: str, workspace_id: str, scope_id: str
) -> dict[str, str]:
    return {
        "scope_id": scope_id,
        "provider": "slack",
        "account_id": workspace_id,
        "conversation_id": conversation_id,
        "kind": kind,
    }


def build_fanout_policy(args: argparse.Namespace) -> dict[str, Any] | None:
    """Translate CLI flags into a FanoutPolicy dict, or None if no flag set."""
    any_set = (
        args.enable_peer_fanout
        or args.allow_untargeted_publication
        or args.max_peer_triggered_publishes
        or args.max_total_peer_deliveries
    )
    if not any_set:
        return None
    return {
        "enabled": bool(args.enable_peer_fanout),
        "allow_untargeted_publication": bool(args.allow_untargeted_publication),
        "max_peer_triggered_publishes": int(args.max_peer_triggered_publishes or 0),
        "max_total_peer_deliveries": int(args.max_total_peer_deliveries or 0),
    }


def build_participants(
    sessions: list[str],
    overrides: dict[str, str],
    default_handle: str,
) -> list[tuple[str, str]]:
    """Return [(handle, session_name), ...] in the order sessions were given.

    Raises SystemExit on duplicate handles or empty input.
    """
    if not sessions:
        raise SystemExit("at least one session is required")
    out: list[tuple[str, str]] = []
    seen: set[str] = set()
    for session in sessions:
        handle = overrides.get(session) or _default_handle_for_session(session)
        if not handle:
            raise SystemExit(f"could not derive handle for session {session!r}")
        if handle in seen:
            raise SystemExit(
                f"duplicate handle {handle!r}; pass --handle to disambiguate")
        seen.add(handle)
        out.append((handle, session))
    if default_handle and default_handle not in seen:
        raise SystemExit(
            f"--default-handle {default_handle!r} does not match any participant handle "
            f"({sorted(seen)})")
    return out


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Bind a Slack room/channel to one or more named gc sessions",
    )
    parser.add_argument("conversation_id", help="Slack channel id (e.g. C0123ROOM01)")
    parser.add_argument("session_names", nargs="+", help="gc session name or id")
    parser.add_argument("--kind", default="room", choices=("room",),
                        help="Conversation kind. Default: room")
    parser.add_argument("--mode", default="launcher", choices=("launcher",),
                        help="Group mode. Default: launcher")
    parser.add_argument("--default-handle", default="",
                        help="Default participant handle for untargeted messages "
                             "(must match one of the participant handles)")
    parser.add_argument("--handle", action="append", default=[],
                        metavar="HANDLE=SESSION",
                        help="Override the handle assigned to a session (repeatable)")
    parser.add_argument("--enable-peer-fanout", action="store_true",
                        help="Set FanoutPolicy.enabled = true on the group")
    parser.add_argument("--allow-untargeted-publication", action="store_true",
                        help="Set FanoutPolicy.allow_untargeted_publication = true")
    parser.add_argument("--max-peer-triggered-publishes", type=int, default=0,
                        help="Cap peer-triggered publishes per inbound (0 = unlimited)")
    parser.add_argument("--max-total-peer-deliveries", type=int, default=0,
                        help="Cap total peer deliveries per inbound (0 = unlimited)")
    parser.add_argument("--binding-owner", default="",
                        metavar="SESSION",
                        help="Also bind this session to the conversation as the publisher "
                             "for /extmsg/outbound. Required to make outbound publishes "
                             "work; without it, peer-fanout still fires but publishes need "
                             "a separate /extmsg/bind call. Should refer semantically to one "
                             "of the participants. Pass the gc-id (e.g. gc-77139) when the "
                             "binding will be looked up by gc-id (e.g. resolve_rig_channel.py); "
                             "pass the participant alias when the rest of the system reads "
                             "the binding by alias. The script does NOT cross-check the owner "
                             "against the participant list — this is intentional so gc-ids "
                             "can be used alongside alias-based participants.")
    args = parser.parse_args(argv)

    workspace_id = _slack_workspace_id()
    city = common.gc_city_name()
    overrides = _parse_handle_overrides(args.handle)
    participants = build_participants(args.session_names, overrides, args.default_handle)
    default_handle = args.default_handle or participants[0][0]
    binding_owner = args.binding_owner.strip()
    conv = build_conversation_ref(
        conversation_id=args.conversation_id,
        kind=args.kind,
        workspace_id=workspace_id,
        scope_id=city,
    )
    fanout_policy = build_fanout_policy(args)

    group_body: dict[str, Any] = {
        "root_conversation": conv,
        "mode": args.mode,
        "default_handle": default_handle,
    }
    if fanout_policy is not None:
        group_body["fanout_policy"] = fanout_policy

    try:
        group = common.gc_post("/extmsg/groups", group_body)
    except common.GCAPIError as exc:
        raise SystemExit(f"ensure group: {exc}") from exc
    group_id = group.get("ID", "")
    if not group_id:
        raise SystemExit(f"ensure group: response missing ID: {group!r}")

    participant_records: list[dict[str, Any]] = []
    for handle, session in participants:
        try:
            res = common.gc_post(
                "/extmsg/participants",
                {"group_id": group_id, "handle": handle, "session_id": session, "public": True},
            )
        except common.GCAPIError as exc:
            raise SystemExit(f"upsert participant {handle}={session}: {exc}") from exc
        participant_records.append(res)

    binding_record: dict[str, Any] | None = None
    if binding_owner:
        try:
            binding_record = common.gc_post(
                "/extmsg/bind",
                {"session_id": binding_owner, "conversation": conv},
            )
        except common.GCAPIError as exc:
            raise SystemExit(f"bind {binding_owner}: {exc}") from exc

    cfg = common.load_pack_config()
    cfg.setdefault("bindings", {})
    binding_key = f"{args.kind}:{args.conversation_id}"
    cfg["bindings"][binding_key] = {
        "kind": args.kind,
        "conversation": conv,
        "group_id": group_id,
        "default_handle": default_handle,
        "fanout_policy": fanout_policy,
        "participants": [
            {"handle": h, "session_name": s} for h, s in participants
        ],
        "binding_owner": binding_owner or None,
        "binding_record": (binding_record or {}).get("ID", "") or None,
    }
    common.save_pack_config(cfg)

    print(json.dumps({
        "binding_key": binding_key,
        "group_id": group_id,
        "default_handle": default_handle,
        "fanout_policy": fanout_policy,
        "participants": participant_records,
        "binding_owner": binding_owner or None,
        "binding_record": binding_record,
    }, indent=2, default=str))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
