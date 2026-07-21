#!/usr/bin/env python3
"""One-pass offline compactor for closed order-tracking beads in .gc/beads.json.

The supervisor's retention watchdog (cmd/gc/order_dispatch.go,
sweepClosedOrderTrackingRetentionBounded) deletes these beads ONE AT A TIME,
and each FileStore.Delete reloads + rewrites the whole file. At 225MB / 320K
beads that starves the order dispatch loop entirely (2026-06-12 incident).
This applies the SAME policy in a single read-filter-write pass:

  eligible: status == "closed" AND "order-tracking" in labels
  bucket:   label "order-run:<name>" prefix, else title "order:<name>",
            else one shared legacy bucket
  retain:   the 10 most recent per bucket (reference time = updated_at,
            falling back to created_at) regardless of age
  delete:   the rest, only if reference time < now - 7d

The SECOND pass (COMPACT_CLOSED_ANY_DAYS) is the one that actually holds the
file down. Closed NON-order-tracking beads — gc's own session records, titled
by working directory — are covered by NO reaper in the file store
(bead-prune-reaper is Dolt-only; bead-janitor is .disabled), and they are 62%
of the closed population. Set it to the same 7d TTL as the primary pass; a 30d
window is effectively a no-op (2026-07-14: 30d deleted 828 of 55,272 beads =
1.5%, while 7d deleted 26,621 = 48%), because the store re-bloats to stall size
in about a day but a 30d window releases nothing for weeks.

Locking mirrors bin/bead-janitor-file-store-helper.py: LOCK_EX on
beads.json.lock, backup, atomic tempfile+rename. Safe against the live
supervisor because FileStore writes reload from disk under the same lock.
Dependency edges whose endpoints were compacted away are pruned in the same
pass, and a non-closed bead in the delete set aborts the write.

Env: COMPACT_APPLY=1 to mutate (default dry-run), COMPACT_TTL_DAYS (7),
COMPACT_RETAIN_LAST (10), COMPACT_CLOSED_ANY_DAYS (0 = off).
"""
import datetime
import fcntl
import json
import os
import shutil
import sys
import tempfile
from collections import defaultdict
from pathlib import Path

BEADS = Path("/home/ds/gas-city/.gc/beads.json")
LOCK = Path(str(BEADS) + ".lock")
APPLY = os.environ.get("COMPACT_APPLY", "") == "1"
TTL_DAYS = float(os.environ.get("COMPACT_TTL_DAYS", "7"))
RETAIN = int(os.environ.get("COMPACT_RETAIN_LAST", "10"))
LEGACY = "\x00legacy-unscoped-order-tracking"


def ref_time(b):
    for k in ("updated_at", "created_at"):
        v = b.get(k)
        if v:
            try:
                return datetime.datetime.fromisoformat(v.replace("Z", "+00:00"))
            except ValueError:
                pass
    return datetime.datetime.min.replace(tzinfo=datetime.timezone.utc)


def bucket(b):
    for label in b.get("labels") or []:
        if label.startswith("order-run:") and label != "order-run:":
            return label[len("order-run:"):]
    title = b.get("title") or ""
    if title.startswith("order:") and title != "order:":
        return title[len("order:"):]
    return LEGACY


def main():
    now = datetime.datetime.now(datetime.timezone.utc)
    cutoff = now - datetime.timedelta(days=TTL_DAYS)

    lock_fd = os.open(LOCK, os.O_CREAT | os.O_RDWR, 0o644)
    fcntl.flock(lock_fd, fcntl.LOCK_EX)
    try:
        raw = json.loads(BEADS.read_text())
        key = "issues" if "issues" in raw else "beads"
        items = raw[key]

        # Optional generic pass: apply the city's standing 30d prune policy
        # (bin/bead-prune-reaper / `bd prune --older-than 30d`) to closed
        # NON-order-tracking beads in the file store, which no reaper covers.
        generic_days = float(os.environ.get("COMPACT_CLOSED_ANY_DAYS", "0"))
        generic_cutoff = now - datetime.timedelta(days=generic_days) if generic_days > 0 else None

        buckets = defaultdict(list)
        keep = []
        generic_deleted = 0
        for b in items:
            if b.get("status") == "closed" and "order-tracking" in (b.get("labels") or []):
                buckets[bucket(b)].append(b)
            elif generic_cutoff and b.get("status") == "closed" and ref_time(b) < generic_cutoff:
                generic_deleted += 1
            else:
                keep.append(b)
        if generic_cutoff:
            print(f"generic pass: closed >{generic_days:g}d non-order-tracking deleted={generic_deleted}")

        deleted = 0
        for name, entries in buckets.items():
            entries.sort(key=lambda b: (ref_time(b), b.get("id", "")), reverse=True)
            for i, b in enumerate(entries):
                if i < RETAIN or ref_time(b) >= cutoff:
                    keep.append(b)
                else:
                    deleted += 1

        # Invariant: compaction only ever removes CLOSED beads. An open/blocked
        # bead reaching the delete set would silently drop live work, so abort
        # rather than write. Cheap (one pass over keep) and worth it: there is
        # no git in the city root to recover a bad write from.
        kept_open = sum(1 for b in keep if b.get("status") != "closed")
        open_total = sum(1 for b in items if b.get("status") != "closed")
        if kept_open != open_total:
            raise SystemExit(
                f"ABORT: compaction would drop {open_total - kept_open} non-closed bead(s); refusing to write"
            )

        # Prune dependency edges whose endpoints no longer exist. The rewrite
        # replaces raw["beads"] but historically left raw["deps"] alone, so each
        # compaction stranded edges: as of 2026-07-14, 19 of 60 edges were inert
        # and open bead gc-1927 was left blocked on gc-1926, a bead a prior
        # compaction had deleted. Dropping an edge is safe here precisely because
        # the endpoint it pointed at was a CLOSED bead (the invariant above), so
        # the dependency was already satisfied.
        surviving = {b.get("id") for b in keep}
        deps = raw.get("deps") or []
        kept_deps = [
            e for e in deps
            if e.get("issue_id") in surviving and e.get("depends_on_id") in surviving
        ]
        dropped_deps = len(deps) - len(kept_deps)

        total_deleted = deleted + generic_deleted
        print(f"total={len(items)} order-tracking-closed={sum(len(v) for v in buckets.values())} "
              f"buckets={len(buckets)} delete={deleted} keep={len(keep)} apply={APPLY}")
        print(f"deps: total={len(deps)} keep={len(kept_deps)} dropped-dangling={dropped_deps}")
        if not APPLY or total_deleted == 0:
            return

        ts = now.strftime("%Y%m%dT%H%M%SZ")
        backup = Path(f"{BEADS}.bak-compact-{ts}")
        shutil.copy2(BEADS, backup)
        raw[key] = keep
        raw["deps"] = kept_deps
        with tempfile.NamedTemporaryFile("w", dir=BEADS.parent, delete=False) as tmp:
            json.dump(raw, tmp, separators=(",", ":"))
            tmp.flush()
            os.fsync(tmp.fileno())
            tmppath = tmp.name
        os.replace(tmppath, BEADS)
        os.chmod(BEADS, 0o644)
        print(f"compacted: backup={backup.name} new_size={BEADS.stat().st_size}")
    finally:
        fcntl.flock(lock_fd, fcntl.LOCK_UN)
        os.close(lock_fd)


if __name__ == "__main__":
    sys.exit(main())
