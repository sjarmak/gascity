#!/usr/bin/env python3
"""dr-fk4: hybrid-claim canary analysis. EB = hybrid since 2026-07-13 13:33 EDT; mem/gascity = oldest-first controls."""
import csv, statistics as st
from datetime import datetime, timedelta

S = "/tmp/claude-1000/-home-ds-gas-city/e9d3a44d-6ad7-434f-94e5-30a0e1f11508/scratchpad"
FLIP = datetime(2026, 7, 13, 13, 33, 0)
H48 = 48 * 3600
# issues.* datetimes are UTC; events.* datetimes are EDT (UTC-4). Verified:
# created events lag issues.created_at by exactly 4h. Normalize issues -> EDT.
TZOFF = timedelta(hours=4)

def parse(ts):
    if not ts:
        return None
    return datetime.strptime(ts.split(".")[0], "%Y-%m-%d %H:%M:%S")

def load_claims(path):
    rows = []
    with open(path) as f:
        for r in csv.DictReader(f):
            r["claim_time"] = parse(r["claim_time"])
            r["bead_created"] = parse(r["bead_created"]) - TZOFF  # UTC -> EDT
            r["priority"] = int(r["priority"])
            r["age"] = int(r["age_at_claim_s"]) + 14400  # UTC/EDT skew correction
            rows.append(r)
    return rows

def is_pool(actor):
    return "-worker-" in actor or actor.startswith("polecat-") or actor.startswith("--home--ds--")

def fmt_h(sec):
    return f"{sec/3600:.1f}h"

def summarize(name, rows):
    rows = [r for r in rows if is_pool(r["actor"]) and r["issue_type"] not in ("epic",)]
    print(f"\n=== {name}: {len(rows)} pool-worker claims ===")
    byp = {}
    for r in rows:
        byp.setdefault(r["priority"], []).append(r["age"])
    for p in sorted(byp):
        ages = byp[p]
        over48 = sum(1 for a in ages if a > H48)
        print(f"  P{p}: n={len(ages):4d}  median_wait={fmt_h(st.median(ages)):>8}  mean={fmt_h(st.mean(ages)):>9}  "
              f"claims_aged>48h={over48} ({100*over48/len(ages):.0f}%)")
    allages = [r["age"] for r in rows]
    over48 = sum(1 for a in allages if a > H48)
    print(f"  ALL: n={len(allages)}  median_wait={fmt_h(st.median(allages))}  >48h at claim: {over48} ({100*over48/len(allages):.1f}%)")
    return rows

def load_issues(path):
    issues = []
    with open(path) as f:
        for r in csv.DictReader(f):
            r["created_at"] = parse(r["created_at"])
            if r["created_at"]:
                r["created_at"] -= TZOFF  # UTC -> EDT
            r["closed_at"] = parse(r["closed_at"])
            if r["closed_at"]:
                r["closed_at"] -= TZOFF  # UTC -> EDT
            r["first_claim"] = parse(r["first_claim"])  # already EDT (events)
            r["priority"] = int(r["priority"])
            issues.append(r)
    return issues

def inversions(name, claims, issues):
    """For each pool claim of bead B at time T: count higher-priority beads that were open,
    unclaimed, created before T (candidate set approximation; routed_to history not reconstructable)."""
    claims = [r for r in claims if is_pool(r["actor"])]
    inv = 0
    tot = 0
    inv_fresh = 0  # inversion where the passed-over bead was <48h old at T (hybrid should have caught it)
    for c in claims:
        T = c["claim_time"]
        better = [i for i in issues
                  if i["priority"] < c["priority"]
                  and i["created_at"] and i["created_at"] < T
                  and (i["closed_at"] is None or i["closed_at"] > T)
                  and (i["first_claim"] is None or i["first_claim"] > T)]
        tot += 1
        if better:
            inv += 1
            if any((T - b["created_at"]).total_seconds() < H48 for b in better):
                inv_fresh += 1
    print(f"  {name}: {inv}/{tot} claims ({100*inv/tot:.0f}%) passed over >=1 open unclaimed higher-priority bead; "
          f"{inv_fresh} ({100*inv_fresh/tot:.0f}%) where a passed-over bead was <48h old")

print("#" * 70)
print("WAIT TIME BY PRIORITY (age of bead at claim), pool-worker claims only")
eb = summarize("EnterpriseBench POST-flip (hybrid)", load_claims(f"{S}/claims_EnterpriseBench.csv"))
ebpre = summarize("EnterpriseBench PRE-flip (oldest, 07-07..07-13)", load_claims(f"{S}/claims_EB_preflip.csv"))
mem = summarize("mem control (oldest)", load_claims(f"{S}/claims_mem.csv"))
gcz = summarize("gascity control (oldest)", load_claims(f"{S}/claims_gascity.csv"))

print("\n" + "#" * 70)
print("PRIORITY-INVERSION AT CLAIM (approx: whole-rig open unclaimed set, routed_to not reconstructable)")
inversions("EB post-flip", eb, load_issues(f"{S}/issues_EnterpriseBench.csv"))
inversions("mem control ", mem, load_issues(f"{S}/issues_mem.csv"))
inversions("gascity ctrl", gcz, load_issues(f"{S}/issues_gascity.csv"))

print("\n" + "#" * 70)
print("CLAIM CHURN (beads claimed >1 time in window, pool claims)")
for name, rows in [("EB post-flip", eb), ("EB pre-flip", ebpre), ("mem", mem), ("gascity", gcz)]:
    cnt = {}
    for r in rows:
        cnt[r["issue_id"]] = cnt.get(r["issue_id"], 0) + 1
    multi = {k: v for k, v in cnt.items() if v > 1}
    n = len(rows)
    print(f"  {name}: {len(multi)} beads multi-claimed / {len(cnt)} distinct beads "
          f"({sum(multi.values())-len(multi)} re-claims over {n} claims)")

print("\n" + "#" * 70)
print("STARVATION CHECK: oldest-bead claim behaviour")
for name, rows in [("EB post-flip (hybrid)", eb), ("EB pre-flip (oldest)", ebpre), ("mem control", mem)]:
    ages = sorted(r["age"] for r in rows)
    if ages:
        p90 = ages[int(0.9 * (len(ages)-1))]
        print(f"  {name}: max_age_at_claim={fmt_h(ages[-1])}  p90={fmt_h(p90)}")

# current open unclaimed queue ages in EB
issues_eb = load_issues(f"{S}/issues_EnterpriseBench.csv")
now = datetime(2026, 7, 19, 16, 45, 0)
open_unclaimed = [i for i in issues_eb if i["status"] == "open" and i["first_claim"] is None and i["created_at"]]
if open_unclaimed:
    ages = sorted((now - i["created_at"]).total_seconds() for i in open_unclaimed)
    over48 = sum(1 for a in ages if a > H48)
    byp = {}
    for i in open_unclaimed:
        byp.setdefault(i["priority"], []).append((now - i["created_at"]).total_seconds())
    print(f"\n  EB open never-claimed non-epic beads NOW: {len(open_unclaimed)}, {over48} aged >48h ({100*over48/len(ages):.0f}%)")
    for p in sorted(byp):
        a = sorted(byp[p])
        print(f"    P{p}: n={len(a):4d} median_age={fmt_h(st.median(a))} max={fmt_h(a[-1])}")
