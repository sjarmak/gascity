#!/usr/bin/env python3
"""dr-fk4 pass 3: sorted-tier-only analysis.
Tier ground truth from claimed-event old_value (pre-claim assignee + gc.routed_to).
EB routed tier sorts hybrid (canary); mem/gascity routed tiers sort oldest (compiled default)."""
import csv, json, re, statistics as st
from datetime import datetime, timedelta
from collections import Counter

csv.field_size_limit(10**8)
S = "/tmp/claude-1000/-home-ds-gas-city/e9d3a44d-6ad7-434f-94e5-30a0e1f11508/scratchpad"
H48 = 48 * 3600
TZOFF = timedelta(hours=4)

def parse_ev(ts):  # events are EDT
    return datetime.strptime(ts.split(".")[0], "%Y-%m-%d %H:%M:%S")

def parse_iso_utc(ts):  # issue JSON created_at like 2026-07-13T17:33:00Z or with offset
    ts = ts.replace("Z", "+00:00")
    dt = datetime.fromisoformat(ts)
    if dt.utcoffset() is not None:
        dt = (dt - dt.utcoffset()).replace(tzinfo=None)
    return dt - TZOFF  # -> EDT

def is_pool(actor):
    return "-worker-" in actor or actor.startswith("polecat-") or actor.startswith("--home--ds--")

def load_tiered(db, pool_route_marker):
    rows = []
    with open(f"{S}/claims_{db}_raw.csv") as f:
        for r in csv.DictReader(f):
            ov = r["old_value"]
            try:
                j = json.loads(ov)
                pre_assignee = j.get("assignee") or ""
                pre_routed = (j.get("metadata") or {}).get("gc.routed_to", "") or ""
                prio = int(j.get("priority", 2))
                created = parse_iso_utc(j["created_at"]) if "created_at" in j else None
            except Exception:
                m = re.search(r'"assignee"\s*:\s*"([^"]*)"', ov); pre_assignee = m.group(1) if m else ""
                m = re.search(r'"gc\.routed_to"\s*:\s*"([^"]*)"', ov); pre_routed = m.group(1) if m else ""
                m = re.search(r'"priority"\s*:\s*(\d+)', ov); prio = int(m.group(1)) if m else 2
                m = re.search(r'"created_at"\s*:\s*"([^"]+)"', ov)
                created = parse_iso_utc(m.group(1)) if m else None
            actor = r["actor"]
            if not is_pool(actor):
                continue
            if pre_assignee == actor:
                tier = "resume"
            elif pre_assignee:
                tier = "preassigned"
            elif pool_route_marker in pre_routed:
                tier = "SORTED"
            elif pre_routed:
                tier = "routed-else"
            else:
                tier = "legacy"
            rows.append({"id": r["issue_id"], "actor": actor, "T": parse_ev(r["claim_time"]),
                         "prio": prio, "created": created, "tier": tier})
    return rows

def analyze(name, rows, live_sort):
    tc = Counter(x["tier"] for x in rows)
    print(f"\n=== {name} — tier mix: {dict(tc)}")
    srt = [x for x in rows if x["tier"] == "SORTED" and x["created"]]
    # first sorted-tier claim per bead
    firsts = {}
    for x in srt:
        if x["id"] not in firsts or x["T"] < firsts[x["id"]]["T"]:
            firsts[x["id"]] = x
    F = sorted(firsts.values(), key=lambda x: x["T"])
    print(f"  sorted-tier claims: {len(srt)} total, {len(F)} distinct beads (live sort = {live_sort})")
    byp = {}
    for x in F:
        byp.setdefault(x["prio"], []).append((x["T"] - x["created"]).total_seconds())
    for p in sorted(byp):
        a = byp[p]
        o = sum(1 for v in a if v > H48)
        print(f"    P{p}: n={len(a):4d} median_wait={st.median(a)/3600:6.1f}h mean={st.mean(a)/3600:6.1f}h >48h_at_claim={o} ({100*o/len(a):.0f}%)")
    alla = [(x["T"] - x["created"]).total_seconds() for x in F]
    o = sum(1 for v in alla if v > H48)
    print(f"    ALL n={len(alla)} median={st.median(alla)/3600:.1f}h >48h at claim: {o} ({100*o/len(alla):.1f}%)")

    # pairwise concordance: pairs (A claimed at Ta, B claimed at Tb>Ta) where B was already
    # created before Ta (both plausibly queued at Ta). Which key explains A-before-B?
    conc_h = conc_o = tot = 0
    hkey_viol_examples = []
    for i, A in enumerate(F):
        for B in F[i+1:]:
            if B["created"] >= A["T"]:
                continue
            tot += 1
            ageA = (A["T"] - A["created"]).total_seconds()
            ageB = (A["T"] - B["created"]).total_seconds()
            ka = (A["prio"] if ageA < H48 else 999, A["created"])
            kb = (B["prio"] if ageB < H48 else 999, B["created"])
            if ka <= kb:
                conc_h += 1
            elif len(hkey_viol_examples) < 3:
                hkey_viol_examples.append((A["id"], A["prio"], f"{ageA/3600:.0f}h", B["id"], B["prio"], f"{ageB/3600:.0f}h"))
            if A["created"] <= B["created"]:
                conc_o += 1
    if tot:
        print(f"    pairwise concordance over {tot} co-queued pairs: HYBRID-key {100*conc_h/tot:.0f}%  OLDEST-key {100*conc_o/tot:.0f}%")
    return F

eb = analyze("EB post-flip", load_tiered("EnterpriseBench", "enterprisebench-worker"), "hybrid")
mm = analyze("mem control", load_tiered("mem", "mem-worker"), "oldest")
gz = analyze("gascity control", load_tiered("gascity", "polecat"), "oldest")
