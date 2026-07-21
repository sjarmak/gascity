#!/usr/bin/env python3
"""Tests for mem_brain_arm_select: arm selection + key-stamping + loud safety net.

Round-trips the EXACT contract example blobs (docs/mem-0ut-dispatch-contract.md)
so a field-name or literal drift fails here, mirroring the read-side
test_arm_analysis_live.py.

    cd /home/ds/gas-city/bin && python3 -m pytest mem_brain_arm_select_test.py -q
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import mem_brain_arm_select as m


def _brain(name="alpha", sid="78b48fa8-ec60-4c55-b033-7bb48f06f8c9",
           built="2026-06-26T04:20:10.683Z", files=("a.ts", "b.ts")):
    return m.Brain(name=name, parent_sid=sid, fork_ts=built, scope_files=frozenset(files))


def _cands(n, prefix="bead", blast=("a.ts",)):
    return [m.Candidate(bead_id=f"{prefix}-{i:03d}", blast_radius=frozenset(blast))
            for i in range(n)]


# --- exact contract literals -------------------------------------------------

def test_keys_match_contract_exactly():
    assert m.KEY_ARM == "gc.brain_arm"
    assert m.KEY_FORK_TS == "gc.brain_fork_ts"
    assert m.KEY_PARENT_SID == "gc.brain_parent_sid"
    assert m.ARM_WARM == "warm" and m.ARM_COLD == "cold"


def test_warm_blob_matches_contract_example():
    # The contract's "Warm dispatch" example, byte-for-byte on the read keys.
    brain = _brain()
    blob = m.build_warm_metadata(brain)
    assert blob[m.KEY_ARM] == "warm"
    assert blob[m.KEY_PARENT_SID] == "78b48fa8-ec60-4c55-b033-7bb48f06f8c9"
    assert blob[m.KEY_FORK_TS] == "2026-06-26T04:20:10.683Z"


def test_cold_blob_has_arm_only_no_fork_keys():
    blob = m.build_cold_metadata(m.SEL_NO_MATCH)
    assert blob[m.KEY_ARM] == "cold"
    assert m.KEY_FORK_TS not in blob
    assert m.KEY_PARENT_SID not in blob


# --- ISO parse mirrors read-side --------------------------------------------

def test_fork_ts_parses_z_and_offset():
    assert m.parse_iso_ts("2026-06-26T04:20:10.683Z").tzinfo is not None
    assert m.parse_iso_ts("2026-06-26T04:20:10.683+00:00").tzinfo is not None


def test_corrupt_built_at_raises_warmkeyerror():
    with pytest.raises(m.WarmKeyError):
        m.build_warm_metadata(_brain(built="not-a-timestamp"))


def test_missing_sid_or_built_at_raises():
    with pytest.raises(m.WarmKeyError):
        m.build_warm_metadata(_brain(sid=""))
    with pytest.raises(m.WarmKeyError):
        m.build_warm_metadata(_brain(built=""))


# --- set-intersection eligibility -------------------------------------------

def test_no_intersection_forces_cold_no_match():
    brain = _brain(files=("x.ts", "y.ts"))
    cands = _cands(20, blast=("unrelated.ts",))
    # min_per_arm=0 isolates the eligibility rule from the balance floor (an
    # all-cold set is legitimately underpowered and raises at the default floor).
    out = m.select_arms(cands, [brain], min_per_arm=0)
    assert all(a.arm == "cold" and a.selection == m.SEL_NO_MATCH for a in out)
    assert all(m.KEY_FORK_TS not in a.metadata for a in out)


def test_best_brain_maximizes_overlap():
    a = _brain(name="a", files=("f1.ts",))
    b = _brain(name="b", files=("f1.ts", "f2.ts", "f3.ts"))
    cand = m.Candidate("z-1", frozenset(("f1.ts", "f2.ts", "f3.ts")))
    best, overlap = m.best_brain_for(cand, [a, b])
    assert best.name == "b" and overlap == 3


# --- deterministic alternation + balance ------------------------------------

def test_alternation_is_deterministic_and_balanced():
    brain = _brain(files=("a.ts",))
    cands = _cands(24, blast=("a.ts",))  # all warm-eligible
    out1 = m.select_arms(cands, [brain])
    out2 = m.select_arms(cands, [brain])
    assert [a.arm for a in out1] == [a.arm for a in out2]  # deterministic
    warm = [a for a in out1 if a.arm == "warm"]
    cold = [a for a in out1 if a.arm == "cold"]
    assert len(warm) >= m.MIN_PER_ARM and len(cold) >= m.MIN_PER_ARM
    # cold-from-eligible carries the balance provenance, not no_match.
    assert all(a.selection == m.SEL_BALANCE for a in cold)


def test_warm_assignments_carry_all_three_keys():
    brain = _brain(files=("a.ts",))
    out = m.select_arms(_cands(24, blast=("a.ts",)), [brain])
    for a in out:
        if a.arm == "warm":
            assert set((m.KEY_ARM, m.KEY_PARENT_SID, m.KEY_FORK_TS)) <= set(a.metadata)
            assert a.brain_name == "alpha"


def test_underpowered_raises_balanceerror():
    brain = _brain(files=("a.ts",))
    with pytest.raises(m.BalanceError):
        m.select_arms(_cands(6, blast=("a.ts",)), [brain])  # only 3 warm / 3 cold


def test_output_order_matches_input():
    brain = _brain(files=("a.ts",))
    cands = _cands(24, blast=("a.ts",))
    out = m.select_arms(cands, [brain])
    assert [a.bead_id for a in out] == [c.bead_id for c in cands]


# --- load_brain from a real-shaped manifest ---------------------------------

def test_load_brain_uses_filehashes_keys(tmp_path: Path):
    manifest = {
        "v": 1, "name": "demo", "repo": "/r", "scope": ["src/"],
        "sessionId": "abc-123", "model": "sonnet", "commit": "",
        "builtAt": "2026-06-26T04:20:10.683Z",
        "fileHashes": {"src/a.ts": "h1", "src/b.ts": "h2"}, "buildTokens": None,
    }
    p = tmp_path / "demo.json"
    p.write_text(json.dumps(manifest))
    brain = m.load_brain(p)
    assert brain.parent_sid == "abc-123"
    assert brain.fork_ts == "2026-06-26T04:20:10.683Z"
    assert brain.scope_files == frozenset({"src/a.ts", "src/b.ts"})


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-q"]))
