#!/usr/bin/env python3
"""q1_power.py — noise floor and live-A/B power for the dispatch-policy question.

Read-only. Reuses bin/or-replay's model. Answers Q1:
  * between-week variance of F_w under a FIXED policy (the noise a live A/B fights)
  * the deterministic paired replay delta and a block-bootstrap CI on it
  * required sample size (weeks / beads) for a live A/B to resolve +2% at
    alpha=0.05, power 0.8 -- citywide and on the contended pools where a canary
    would actually run.
"""
import sys, os, json, math, importlib.util, random
from importlib.machinery import SourceFileLoader
from collections import defaultdict

ORP_PATH = "/home/ds/gas-city/bin/or-replay"
loader = SourceFileLoader("orreplay", ORP_PATH)
spec = importlib.util.spec_from_loader("orreplay", loader)
orp = importlib.util.module_from_spec(spec)
loader.exec_module(orp)

clampp = orp.clampp
iso_week = orp.iso_week


def load_traces(path):
    raw = json.load(open(path))
    pops = {}; real_complete = {}
    for pool, rows in raw.items():
        lst = []
        for r in rows:
            b = orp.Bead(r["id"]); b.pool = pool; b.priority = clampp(r["priority"])
            b.arrival = r["arrival"]; b.claim_ts = r["claim"]; b.complete_ts = r["complete"]
            b.service = r["service"]; b.created_at = r["created_at"]
            b.blockers = set(r.get("blockers") or [])
            lst.append(b)
            if b.complete_ts is not None:
                real_complete[b.id] = b.complete_ts
        pops[pool] = lst
    return pops, real_complete


def fw_for(pops_subset, policy):
    sims = [orp.simulate(beads, policy) for beads in pops_subset.values()]
    return orp.metrics_for(sims)["F_w"]


def mean_std(xs):
    n = len(xs)
    if n == 0: return (0.0, 0.0)
    m = sum(xs)/n
    if n == 1: return (m, 0.0)
    v = sum((x-m)**2 for x in xs)/(n-1)
    return (m, math.sqrt(v))


def weekly_fw(pops, policy, pool_filter=None):
    """F_w per iso-week (by arrival week), optionally restricted to one pool substr."""
    weeks = defaultdict(dict)   # week -> pool -> [beads]
    for pool, v in pops.items():
        if pool_filter and pool_filter not in pool:
            continue
        for b in v:
            weeks[iso_week(b.arrival)].setdefault(pool, []).append(b)
    out = {}
    for wk, wpops in weeks.items():
        if sum(len(x) for x in wpops.values()) < 5:   # skip tiny weeks
            continue
        out[wk] = (fw_for(wpops, policy), sum(len(x) for x in wpops.values()))
    return out


def required_weeks(sigma, mean, effect_frac=0.02, alpha=0.05, power=0.8):
    """Two-arm test, n weeks per arm, detect delta=effect_frac*mean given between-week sigma."""
    z_a = 1.959963985   # two-sided 0.05
    z_b = 0.841621234   # power 0.8
    delta = effect_frac * mean
    if delta <= 0: return float("inf")
    n = 2 * (z_a + z_b)**2 * (sigma**2) / (delta**2)
    return n


def main():
    path = sys.argv[1]
    pops, real_complete = load_traces(path)
    orp.REAL_COMPLETE = real_complete
    orp.__dict__["REAL_COMPLETE"] = real_complete

    print("========== Q1: NOISE FLOOR & LIVE-A/B POWER ==========\n")

    # ---- deterministic paired replay delta (whole corpus) ------------------
    fw_old = fw_for(pops, "oldest")
    fw_hyb = fw_for(pops, "hybrid")
    fw_pri = fw_for(pops, "priority")
    print(f"whole-corpus replay F_w:  oldest={fw_old/3600:.3f}h  hybrid={fw_hyb/3600:.3f}h  "
          f"priority={fw_pri/3600:.3f}h")
    print(f"  hybrid gain vs oldest: {100*(fw_old-fw_hyb)/fw_old:+.2f}%   "
          f"(absolute {(fw_old-fw_hyb)/3600:+.3f}h)\n")

    # ---- between-week F_w under FIXED policy (the live noise floor) ---------
    for scope, filt in [("CITYWIDE", None),
                        ("enterprisebench", "enterprisebench"),
                        ("gascity-packs-polecat", "packs")]:
        wf_old = weekly_fw(pops, "oldest", filt)
        wf_hyb = weekly_fw(pops, "hybrid", filt)
        common = sorted(set(wf_old) & set(wf_hyb))
        old_series = [wf_old[w][0]/3600 for w in common]
        hyb_series = [wf_hyb[w][0]/3600 for w in common]
        beads_series = [wf_old[w][1] for w in common]
        m_old, s_old = mean_std(old_series)
        # paired weekly delta
        deltas = [o - h for o, h in zip(old_series, hyb_series)]
        m_d, s_d = mean_std(deltas)
        cv = (s_old/m_old*100) if m_old else 0
        print(f"--- {scope} ---")
        print(f"  weeks used: {len(common)}   beads/week: "
              f"min {min(beads_series) if beads_series else 0} "
              f"med {sorted(beads_series)[len(beads_series)//2] if beads_series else 0} "
              f"max {max(beads_series) if beads_series else 0}")
        print(f"  FCFS weekly F_w: mean {m_old:.2f}h  std {s_old:.2f}h  CV {cv:.0f}%")
        print(f"  paired weekly delta (oldest-hybrid): mean {m_d:+.3f}h  std {s_d:.3f}h")
        # required weeks for a LIVE A/B (between-week noise), effect = 2% of mean
        n_wk = required_weeks(s_old, m_old, 0.02)
        beads_per_week_med = sorted(beads_series)[len(beads_series)//2] if beads_series else 0
        print(f"  live A/B to resolve +2% at a=0.05,power=0.8:")
        print(f"     required weeks/arm: {n_wk:.0f}   (~{n_wk*beads_per_week_med:.0f} beads/arm "
              f"at {beads_per_week_med} beads/wk)")
        # block bootstrap CI on the paired replay delta (resample weeks)
        if len(deltas) >= 3:
            rng = random.Random(12345)
            boots = []
            for _ in range(5000):
                sample = [deltas[rng.randrange(len(deltas))] for _ in deltas]
                boots.append(sum(sample)/len(sample))
            boots.sort()
            lo = boots[int(0.025*len(boots))]; hi = boots[int(0.975*len(boots))]
            frac_pos = sum(1 for x in boots if x > 0)/len(boots)
            print(f"  block-bootstrap 95% CI on weekly delta: [{lo:+.3f}h, {hi:+.3f}h]   "
                  f"P(delta>0)={frac_pos:.2f}")
        print()


if __name__ == "__main__":
    main()
