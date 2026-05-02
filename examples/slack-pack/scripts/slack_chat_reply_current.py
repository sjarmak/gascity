#!/usr/bin/env python3
"""Reply to the latest Slack inbound event seen by the current session.

The lookup order:

1. If --conversation-id is supplied, reply to that conversation.
2. Otherwise, scan recent extmsg.inbound events for one targeting the
   current session, and reply to the same conversation.
3. If neither yields a target, fall back to the session's saved
   extmsg binding.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import sys
from typing import Any

import slack_intake_common as common


def _load_body(args: argparse.Namespace) -> str:
    if args.body and args.body_file:
        raise SystemExit("pass --body OR --body-file, not both")
    if args.body:
        return args.body
    if args.body_file:
        return pathlib.Path(args.body_file).read_text(encoding="utf-8")
    raise SystemExit("either --body or --body-file is required")


def _resolve_conversation(
    args: argparse.Namespace, session_id: str
) -> dict[str, str]:
    """Pick which Slack conversation to publish into."""
    explicit = (args.conversation_id or "").strip()
    if explicit:
        workspace = os.environ.get("SLACK_WORKSPACE_ID", "").strip()
        if not workspace:
            raise SystemExit("SLACK_WORKSPACE_ID must be set when using --conversation-id")
        return {
            "scope_id": common.gc_city_name(),
            "provider": "slack",
            "account_id": workspace,
            "conversation_id": explicit,
            "kind": args.kind,
        }
    event = common.find_latest_inbound_for_session(session_id)
    if event is not None:
        payload = event.get("payload") or {}
        cid = (payload.get("conversation_id") or "").strip()
        if cid:
            return {
                "scope_id": common.gc_city_name(),
                "provider": "slack",
                "account_id": os.environ.get("SLACK_WORKSPACE_ID", ""),
                "conversation_id": cid,
                "kind": args.kind,
            }
    binding = common.look_up_binding(session_id)
    if binding:
        return {
            "scope_id": binding.get("scope_id", common.gc_city_name()),
            "provider": binding.get("provider", "slack"),
            "account_id": binding.get("account_id", os.environ.get("SLACK_WORKSPACE_ID", "")),
            "conversation_id": binding.get("conversation_id", ""),
            "kind": binding.get("kind", args.kind),
        }
    raise SystemExit(
        "no inbound event and no binding found for this session; "
        "pass --conversation-id explicitly")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Reply to the latest Slack inbound event seen by the current session",
    )
    parser.add_argument("--session", default="", help="Override session id")
    parser.add_argument("--conversation-id", default="",
                        help="Override Slack channel/DM id")
    parser.add_argument("--kind", default="dm",
                        help="Conversation kind (dm/room/thread). Default: dm")
    parser.add_argument("--reply-to", default="",
                        help="Slack message ts to reply to (threaded reply)")
    parser.add_argument("--idempotency-key", default="",
                        help="Caller-supplied idempotency key")
    parser.add_argument("--body", default="")
    parser.add_argument("--body-file", default="")
    parser.add_argument(
        "--via",
        choices=("gc", "adapter"),
        default="gc",
        help=("Publish through gc /extmsg/outbound (default) so peer fanout "
              "+ transcript recording fire, or directly to the local adapter "
              "(adapter-only diagnostics; peers in a bind-room won't see "
              "the message)."),
    )
    args = parser.parse_args(argv)

    body = _load_body(args)

    session_id = (args.session or "").strip()
    if not session_id:
        try:
            session_id = common.current_session_id()
        except common.GCAPIError as exc:
            raise SystemExit(str(exc)) from exc

    conv = _resolve_conversation(args, session_id)
    if not conv.get("conversation_id"):
        raise SystemExit("could not resolve a target conversation_id")
    if not conv.get("account_id"):
        raise SystemExit("missing slack account_id (SLACK_WORKSPACE_ID env)")

    publish_kwargs = dict(
        session_id=session_id,
        scope_id=conv["scope_id"],
        provider=conv["provider"],
        account_id=conv["account_id"],
        conversation_id=conv["conversation_id"],
        kind=conv["kind"],
        text=body,
        reply_to_message_id=args.reply_to,
        idempotency_key=args.idempotency_key,
    )
    try:
        if args.via == "adapter":
            result = common.publish_via_adapter(**publish_kwargs)
        else:
            result = common.publish_via_gc_outbound(**publish_kwargs)
    except (common.AdapterError, common.GCAPIError) as exc:
        raise SystemExit(str(exc)) from exc

    print(json.dumps({
        "conversation_id": conv["conversation_id"],
        "session_id": session_id,
        "via": args.via,
        "result": result,
    }, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
