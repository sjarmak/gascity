#!/usr/bin/env python3
"""bead-janitor-file-store-helper: close stale plumbing beads in the file-backed city store.

Sister to bin/bead-janitor (bash). That script targets bd-backed rig stores via
`bd list` + `bd close`. The file-backed city store at
/home/ds/gas-city/.gc/beads.json is invisible to bd CLI — accumulating
molecule/convoy/step-template beads indefinitely. This helper directly mutates
beads.json under the same flock the gc supervisor uses.

Scope of closures:
  - issue_type in {"molecule", "convoy"}                         — formula wrappers
  - metadata.gc.step_ref present                                  — formula step beads
  - title matches ^mol-... or ^sling-... (excludes formula-name beads themselves)
  - AND updated_at < cutoff (MIN_AGE_DAYS, default 7)
  - AND status in {"open", "in_progress"}

Excluded:
  - issue_type == "session"                                       — live runtime entries
  - metadata.gc.kind in {"workflow", "workflow-finalize"}         — formula templates
  - chore beads (issue_type == "chore")                           — nudge backing store has own lifecycle

Locking:
  - Acquires LOCK_EX on /home/ds/gas-city/.gc/beads.json.lock before read+write
  - Atomic write via tempfile + rename
  - Backup written to beads.json.bak-janitor-<timestamp> before each apply run

Env:
  JANITOR_BEADS_PATH    - file store path (default: /home/ds/gas-city/.gc/beads.json)
  JANITOR_MIN_AGE_DAYS  - threshold (default: 7)
  JANITOR_APPLY         - "1" to actually mutate; anything else = dry-run
"""

from __future__ import annotations

import datetime
import fcntl
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path

BEADS_PATH = Path(os.environ.get("JANITOR_BEADS_PATH", "/home/ds/gas-city/.gc/beads.json"))
LOCK_PATH = Path(str(BEADS_PATH) + ".lock")
MIN_AGE_DAYS = float(os.environ.get("JANITOR_MIN_AGE_DAYS", "7"))
APPLY = os.environ.get("JANITOR_APPLY", "") == "1"


def age_days(timestamp: str | None, now: datetime.datetime) -> float | None:
    if not timestamp:
        return None
    try:
        dt = datetime.datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
        return (now - dt).total_seconds() / 86400
    except ValueError:
        return None


def is_plumbing_candidate(b: dict, now: datetime.datetime) -> tuple[bool, str]:
    """Returns (is_candidate, reason)."""
    if b.get("status") not in ("open", "in_progress"):
        return False, ""
    typ = b.get("issue_type", "")
    md = b.get("metadata") or {}

    # Excluded — never close
    if typ == "session":
        return False, ""
    if typ == "chore":
        return False, ""
    if md.get("gc.kind") in ("workflow", "workflow-finalize"):
        return False, ""

    age = age_days(b.get("updated_at") or b.get("created_at"), now)
    if age is None or age < MIN_AGE_DAYS:
        return False, ""

    if typ in ("molecule", "convoy"):
        return True, f"{typ}_aged_{int(age)}d"
    if md.get("gc.step_ref") is not None:
        return True, f"step_ref_aged_{int(age)}d"

    title = b.get("title") or ""
    if title.startswith(("mol-", "sling-")):
        # but not the literal formula name itself (those are templates)
        return True, f"formula_title_aged_{int(age)}d"

    return False, ""


def main() -> int:
    if not BEADS_PATH.exists():
        print(f"bead-janitor-file-store-helper: {BEADS_PATH} not found", file=sys.stderr)
        return 1

    with open(LOCK_PATH, "w") as lock_f:
        fcntl.flock(lock_f.fileno(), fcntl.LOCK_EX)
        try:
            data = json.loads(BEADS_PATH.read_text())
        except (OSError, json.JSONDecodeError) as e:
            print(f"bead-janitor-file-store-helper: read failed: {e}", file=sys.stderr)
            return 1

        beads = data.get("beads", [])
        now = datetime.datetime.now(datetime.timezone.utc)
        candidates = []
        for i, b in enumerate(beads):
            is_cand, reason = is_plumbing_candidate(b, now)
            if is_cand:
                candidates.append((i, b, reason))

        if not candidates:
            print(f"bead-janitor-file-store-helper: 0 candidates (apply={APPLY}, min_age={MIN_AGE_DAYS}d)")
            return 0

        # By type for summary
        by_type: dict[str, int] = {}
        for _, b, _ in candidates:
            by_type[b.get("issue_type", "?")] = by_type.get(b.get("issue_type", "?"), 0) + 1

        print(f"bead-janitor-file-store-helper: {len(candidates)} candidates (apply={APPLY}):")
        for t, n in sorted(by_type.items(), key=lambda x: -x[1]):
            print(f"  {t:12} {n}")

        if not APPLY:
            print("DRY-RUN — re-run with JANITOR_APPLY=1 to close")
            return 0

        # Backup before write
        ts = datetime.datetime.now().strftime("%Y%m%dT%H%M%SZ")
        backup_path = BEADS_PATH.parent / f"{BEADS_PATH.name}.bak-janitor-{ts}"
        shutil.copy2(BEADS_PATH, backup_path)
        print(f"backup: {backup_path}")

        closed_at = now.isoformat()
        for i, _, reason in candidates:
            b = beads[i]
            b["status"] = "closed"
            b["closed_at"] = closed_at
            md = b.setdefault("metadata", {})
            # Metadata values must be strings (gc deserializer rejects bools)
            md["gc.janitor_closed"] = "true"
            md["gc.janitor_closed_at"] = closed_at
            md["gc.janitor_close_reason"] = reason
            # Add a note for auditability
            existing_notes = b.get("notes") or ""
            note_line = f"[bead-janitor 2026-05-20] closed as stale plumbing (reason={reason})"
            b["notes"] = (existing_notes + "\n" + note_line).strip()

        # Atomic write via tempfile + rename
        tmp_fd, tmp_path = tempfile.mkstemp(
            dir=str(BEADS_PATH.parent), prefix=BEADS_PATH.name + ".tmp-",
        )
        try:
            with os.fdopen(tmp_fd, "w") as f:
                json.dump(data, f, separators=(",", ":"))
            os.rename(tmp_path, BEADS_PATH)
        except Exception:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            raise

        print(f"closed {len(candidates)} bead(s) in file store")
        return 0


if __name__ == "__main__":
    sys.exit(main())
