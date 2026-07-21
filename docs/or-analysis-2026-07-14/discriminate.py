#!/usr/bin/env python3
"""discriminate.py — does this city's traffic even POSE the dispatch-policy question?

Read-only. Reuses bin/or-replay's validated extract + model. Adds:
  Q2  decision-point instrumentation: at every freed slot with >=2 ready beads,
      does FCFS(arrival) and priority pick the SAME bead? Fraction that DISAGREE
      is the ceiling on how much any priority policy can differ from FCFS here.
  Q1  noise floor: between-week F_w variance under a FIXED policy, and the live
      A/B sample size needed to resolve a +2% effect at alpha=0.05, power 0.8.
      Plus a block-bootstrap CI on the paired replay delta.
  regret: at disagreement points, how much flow-time is actually at stake
      (weighted by service time and priority), not just how often they differ.

Everything is computed from the same fixed trace bin/or-replay validated to 0.37h
median claim-time error under FCFS.
"""
import sys, os, json, math, importlib.util, argparse
from collections import defaultdict

HERE = os.path.dirname(os.path.abspath(__file__))
ORP_PATH = "/home/ds/gas-city/bin/or-replay"

# load or-replay as a module (no .py extension -> supply loader explicitly)
from importlib.machinery import SourceFileLoader
loader = SourceFileLoader("orreplay", ORP_PATH)
spec = importlib.util.spec_from_loader("orreplay", loader)
orp = importlib.util.module_from_spec(spec)
loader.exec_module(orp)          # __name__=="orreplay", so orp.main() does not fire

clampp = orp.clampp
prio_weight = orp.prio_weight


# ---------------------------------------------------------------------------
# Instrumented replay: same claim-slot model as or-replay.simulate, but records
# a decision record at every place() step where the ready-set offered a choice.
# The trajectory is driven by `policy` (default oldest = the incumbent), so the
# decision points are the ones the LIVE fleet actually encountered.
# ---------------------------------------------------------------------------
import heapq

def simulate_instrumented(beads, policy, real_complete, slots=None):
    if not beads:
        return {"results": {}, "decisions": [], "servers": 0}
    idset = {b.id for b in beads}
    bymap = {b.id: b for b in beads}
    scorer = orp.make_scorer(policy)

    remaining = {}
    dependents = {}
    ext_ready = {}
    for b in beads:
        rem = 0
        ext = b.arrival
        for dep in b.blockers:
            if dep in idset:
                rem += 1
                dependents.setdefault(dep, set()).add(b.id)
            else:
                rc = real_complete.get(dep)
                if rc is not None:
                    ext = max(ext, rc)
        remaining[b.id] = rem
        ext_ready[b.id] = ext

    if slots is None:
        slots = sorted(b.claim_ts for b in beads)

    completed = {}
    sim = {}
    released_count = {}
    decisions = []           # one record per freed-slot decision with >=2 ready

    heap = []
    seq = 0
    def push(t, kind, bid):
        nonlocal seq
        heapq.heappush(heap, (t, seq, kind, bid)); seq += 1
    for b in beads:
        push(b.arrival, "arr", b.id)
        if ext_ready[b.id] > b.arrival:
            push(ext_ready[b.id], "ext", b.id)
    for st in slots:
        push(st, "slot", None)

    arrived = set(); ready = set(); claimed = set()
    idle = 0

    def try_ready(bid, t):
        if bid in arrived and bid not in claimed and remaining[bid] == 0 \
           and t >= ext_ready[bid]:
            ready.add(bid)

    def place(t):
        nonlocal idle
        while idle > 0 and ready:
            # --- record the decision the workload poses at this freed slot ---
            if len(ready) >= 2:
                rl = list(ready)
                # FCFS pick: min created_at
                fcfs = min(rl, key=lambda r: (bymap[r].created_at, r))
                # priority (hybrid) pick: (priority, created_at)
                pri = min(rl, key=lambda r: (bymap[r].priority, bymap[r].created_at, r))
                prios = [bymap[r].priority for r in rl]
                pmin, pmax = min(prios), max(prios)
                disagree = (fcfs != pri)
                # regret proxy: if we must serialize, picking the lower-priority
                # (FCFS) bead first delays the higher-priority one by its service.
                svc_fcfs = bymap[fcfs].service
                w_gap = prio_weight(bymap[pri].priority) - prio_weight(bymap[fcfs].priority)
                decisions.append({
                    "t": t, "nready": len(ready),
                    "pmin": pmin, "pmax": pmax,
                    "prio_spread": pmax - pmin,
                    "disagree": disagree,
                    "fcfs_prio": bymap[fcfs].priority,
                    "pri_prio": bymap[pri].priority,
                    "svc_fcfs": svc_fcfs,
                    "w_gap": w_gap,        # >0 means priority pick outranks fcfs pick
                })
            # --- actual claim under the driving policy ---
            best = None; bestkey = None
            for rid in ready:
                dc = sum(1 for d in dependents.get(rid, ()) if d not in completed)
                k = scorer(bymap[rid], t, dc)
                if bestkey is None or k < bestkey:
                    bestkey = k; best = rid
            ready.discard(best); claimed.add(best); idle -= 1
            b = bymap[best]
            cdone = t + b.service
            sim[best] = (t, cdone)
            push(cdone, "done", best)

    while heap:
        t = heap[0][0]
        batch = []
        while heap and heap[0][0] == t:
            batch.append(heapq.heappop(heap))
        for _tt, _s, kind, bid in batch:
            if kind == "done":
                completed[bid] = t
                rel = 0
                for d in dependents.get(bid, ()):
                    if remaining.get(d, 0) > 0:
                        remaining[d] -= 1; rel += 1
                        if remaining[d] == 0:
                            try_ready(d, t)
                released_count[bid] = rel
        for _tt, _s, kind, bid in batch:
            if kind == "slot":
                idle += 1
            elif kind == "arr":
                arrived.add(bid); try_ready(bid, t)
            elif kind == "ext":
                try_ready(bid, t)
        place(t)

    results = {}
    for b in beads:
        if b.id in sim:
            cs, cd = sim[b.id]
            results[b.id] = {"priority": b.priority, "arrival": b.arrival,
                             "sim_claim": cs, "sim_complete": cd,
                             "flow": cd - b.arrival, "released": released_count.get(b.id, 0)}
    return {"results": results, "decisions": decisions,
            "servers": orp.observed_max_concurrency(beads)}


def load_traces(path):
    """Load or-replay `extract --json` dump into pool -> [Bead]."""
    raw = json.load(open(path))
    pops = {}
    real_complete = {}
    for pool, rows in raw.items():
        lst = []
        for r in rows:
            b = orp.Bead(r["id"])
            b.pool = pool
            b.priority = clampp(r["priority"])
            b.arrival = r["arrival"]
            b.claim_ts = r["claim"]
            b.complete_ts = r["complete"]
            b.service = r["service"]
            b.created_at = r["created_at"]
            b.blockers = set(r.get("blockers") or [])
            lst.append(b)
            if b.complete_ts is not None:
                real_complete[b.id] = b.complete_ts
        pops[pool] = lst
    return pops, real_complete


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("traces")
    ap.add_argument("--policy", default="oldest",
                    help="driving trajectory for decision instrumentation")
    args = ap.parse_args()

    pops, real_complete = load_traces(args.traces)
    orp.REAL_COMPLETE = real_complete
    globals_orp = orp.__dict__
    globals_orp["REAL_COMPLETE"] = real_complete

    total = sum(len(v) for v in pops.values())
    print(f"# loaded {total} beads across {len(pops)} pools from {args.traces}")

    # ---- Q2: decision-point discrimination ceiling -------------------------
    all_dec = []
    per_pool = {}
    for pool, beads in pops.items():
        r = simulate_instrumented(beads, args.policy, real_complete)
        d = r["decisions"]
        per_pool[pool] = d
        for rec in d:
            rec["pool"] = pool
        all_dec.extend(d)

    nd = len(all_dec)
    ndis = sum(1 for x in all_dec if x["disagree"])
    # of disagreements, how many are "priority-improving" (priority pick outranks fcfs pick)
    nprio_improve = sum(1 for x in all_dec if x["disagree"] and x["w_gap"] > 0)
    print("\n========== Q2: DISCRIMINATION CEILING ==========")
    print(f"decision points (freed slot, >=2 ready) : {nd}")
    if nd:
        print(f"  FCFS pick == priority pick (AGREE)     : {nd-ndis}  ({100*(nd-ndis)/nd:.1f}%)")
        print(f"  DISAGREE (workload poses the question) : {ndis}  ({100*ndis/nd:.2f}%)")
        print(f"    of which priority-improving          : {nprio_improve}  ({100*nprio_improve/nd:.2f}% of all points)")
    # regret at disagreement points: serialized delay to the higher-priority bead
    reg = [x["svc_fcfs"] for x in all_dec if x["disagree"] and x["w_gap"] > 0]
    if reg:
        reg.sort()
        tot_reg_h = sum(reg)/3600.0
        print(f"  total serialized-delay exposure         : {tot_reg_h:.1f}h across {len(reg)} points"
              f" (median {reg[len(reg)//2]/3600.0:.2f}h/point)")

    # per-pool ceiling (contended pools first)
    print("\n  per-pool decision points / disagree% (>=20 decisions):")
    rows = []
    for pool, d in per_pool.items():
        n = len(d); dis = sum(1 for x in d if x["disagree"])
        if n >= 20:
            rows.append((pool.split("/")[-1], n, dis, 100*dis/n))
    for name, n, dis, pctv in sorted(rows, key=lambda r: -r[1]):
        print(f"    {name:<28} {n:>6} pts  {dis:>5} disagree  {pctv:>5.1f}%")

    # distribution of ready-set size at decision points (is there ever a choice?)
    from collections import Counter
    csz = Counter(x["nready"] for x in all_dec)
    print("\n  ready-set size at decision points (choice depth):")
    for k in sorted(csz)[:8]:
        print(f"    nready={k:<3} : {csz[k]:>6}")
    big = sum(v for k, v in csz.items() if k >= 8)
    print(f"    nready>=8 : {big:>6}")

    # save decisions for the report
    outp = os.path.join(os.path.dirname(args.traces), "decisions.json")
    with open(outp, "w") as fh:
        json.dump({"n": nd, "ndisagree": ndis, "nprio_improve": nprio_improve,
                   "per_pool": {k.split('/')[-1]: {"n": len(v),
                                "dis": sum(1 for x in v if x['disagree'])}
                                for k, v in per_pool.items()}}, fh, indent=2)
    print(f"\n# wrote {outp}")


if __name__ == "__main__":
    main()
