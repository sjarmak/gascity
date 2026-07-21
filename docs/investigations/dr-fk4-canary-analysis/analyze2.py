#!/usr/bin/env python3
"""dr-fk4 pass 2: sort-compliance vs hybrid/oldest keys, churn characterization, molecule step order."""
import csv, statistics as st
from datetime import datetime, timedelta
from collections import defaultdict

S = "/tmp/claude-1000/-home-ds-gas-city/e9d3a44d-6ad7-434f-94e5-30a0e1f11508/scratchpad"
H48 = 48 * 3600
TZOFF = timedelta(hours=4)

def parse(ts):
    if not ts:
        return None
    return datetime.strptime(ts.split(".")[0], "%Y-%m-%d %H:%M:%S")

def is_pool(actor):
    return "-worker-" in actor or actor.startswith("polecat-") or actor.startswith("--home--ds--")

def load(db, claims_file):
    claims = []
    with open(f"{S}/{claims_file}") as f:
        for r in csv.DictReader(f):
            r["claim_time"] = parse(r["claim_time"])
            r["bead_created"] = parse(r["bead_created"]) - TZOFF
            r["priority"] = int(r["priority"])
            r["age"] = int(r["age_at_claim_s"]) + 14400
            claims.append(r)
    issues = {}
    with open(f"{S}/issues_{db}.csv") as f:
        for r in csv.DictReader(f):
            r["created_at"] = parse(r["created_at"])
            if r["created_at"]: r["created_at"] -= TZOFF
            r["closed_at"] = parse(r["closed_at"])
            if r["closed_at"]: r["closed_at"] -= TZOFF
            r["first_claim"] = parse(r["first_claim"])
            r["priority"] = int(r["priority"])
            issues[r["id"]] = r
    return claims, issues

def sort_compliance(name, claims, issues, pool_prefix):
    """Candidate set proxy for the routed queue: beads eventually first-claimed by a pool actor.
    For each pool claim at T: candidates created<T, first_claim>=T. Was the claimed bead the
    argmin under (a) oldest key (created,id) and (b) hybrid key (48h-window priority, created, id)?"""
    pool_claims = [c for c in claims if is_pool(c["actor"])]
    # first pool claim per bead only (later claims are re-claims, muddier)
    firsts = {}
    for c in pool_claims:
        if c["issue_id"] not in firsts or c["claim_time"] < firsts[c["issue_id"]]["claim_time"]:
            firsts[c["issue_id"]] = c
    pool_bead_ids = set(firsts)
    n = old_ok = hyb_ok = 0
    for bid, c in sorted(firsts.items(), key=lambda kv: kv[1]["claim_time"]):
        T = c["claim_time"]
        cands = [issues[i] for i in pool_bead_ids
                 if i in issues and issues[i]["created_at"] and issues[i]["created_at"] < T
                 and firsts[i]["claim_time"] >= T]
        if len(cands) < 2:
            continue
        n += 1
        okey = lambda x: (x["created_at"], x["id"])
        hkey = lambda x: (x["priority"] if (T - x["created_at"]).total_seconds() < H48 else 999,
                          x["created_at"], x["id"])
        if min(cands, key=okey)["id"] == bid: old_ok += 1
        if min(cands, key=hkey)["id"] == bid: hyb_ok += 1
    print(f"  {name}: n={n} first-claims with >=2 queued candidates | claimed bead was queue-head "
          f"under OLDEST: {old_ok} ({100*old_ok/n:.0f}%) | under HYBRID: {hyb_ok} ({100*hyb_ok/n:.0f}%)")

print("SORT-COMPLIANCE (which order did the pool actually execute?)")
eb_claims, eb_issues = load("EnterpriseBench", "claims_EnterpriseBench.csv")
mem_claims, mem_issues = load("mem", "claims_mem.csv")
gc_claims, gc_issues = load("gascity", "claims_gascity.csv")
sort_compliance("EB post-flip (hybrid live)", eb_claims, eb_issues, "enterprisebench-worker")
sort_compliance("mem control (oldest)      ", mem_claims, mem_issues, "mem-worker")
sort_compliance("gascity control (oldest)  ", gc_claims, gc_issues, "polecat")

print("\nCHURN CHARACTERIZATION — top multi-claimed EB beads post-flip")
by_bead = defaultdict(list)
for c in eb_claims:
    if is_pool(c["actor"]):
        by_bead[c["issue_id"]].append(c)
multi = sorted(((len(v), k, v) for k, v in by_bead.items() if len(v) > 1), reverse=True)[:8]
for cnt, bid, cs in multi:
    actors = {c["actor"] for c in cs}
    span = (max(c["claim_time"] for c in cs) - min(c["claim_time"] for c in cs)).total_seconds() / 3600
    print(f"  {bid}: {cnt} claims by {len(actors)} sessions over {span:.1f}h  P{cs[0]['priority']}")

print("\nMOLECULE STEP ORDER — dotted-id sibling claims in EB post-flip (violations = child claimed before an earlier sibling)")
steps = defaultdict(list)
for c in eb_claims:
    bid = c["issue_id"]
    if "." in bid:
        parent, step = bid.rsplit(".", 1)
        if step.isdigit():
            steps[parent].append((int(step), c["claim_time"], bid))
viol = tot = 0
for parent, lst in steps.items():
    first = {}
    for stp, t, bid in lst:
        if stp not in first or t < first[stp]:
            first[stp] = t
    ordered = sorted(first.items())
    for (s1, t1), (s2, t2) in zip(ordered, ordered[1:]):
        tot += 1
        if t2 < t1:
            viol += 1
            print(f"  VIOLATION {parent}: step {s2} first-claimed {t2} before step {s1} at {t1}")
print(f"  {viol}/{tot} adjacent sibling-step pairs out of order across {len(steps)} parents with claimed steps")

print("\nEB QUEUE-DEPTH TREND: never-claimed open backlog is from analyze.py; claims of >48h-old beads by day (post-flip):")
byday = defaultdict(lambda: [0, 0])
for c in eb_claims:
    if is_pool(c["actor"]):
        d = c["claim_time"].date().isoformat()
        byday[d][0] += 1
        if c["age"] > H48:
            byday[d][1] += 1
for d in sorted(byday):
    t, o = byday[d]
    print(f"  {d}: {t:3d} claims, {o} of >48h-old beads")
