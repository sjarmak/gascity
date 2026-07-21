#!/usr/bin/env python3
"""ZFC-clean arm selection + key-stamping for the mem warm-vs-cold brain A/B.

Dispatch-side of the mem-0ut experiment. Reads the EXACT keys pinned by
`docs/mem-0ut-dispatch-contract.md` (mem branch mem-0ut.3-contract):

    gc.brain_arm        "warm" | "cold"            -- every A/B bead
    gc.brain_fork_ts    ISO-8601 == brain builtAt  -- WARM beads only
    gc.brain_parent_sid brain session id           -- WARM beads only
    gc.brain_selection  "match" | "no_match"        -- provenance (this side)

This module is MECHANISM only (per house-rules ZFC):
  * set intersection: brain scope-manifest (manifest fileHashes keys) ∩
    bead blast-radius (concrete repo-relative paths);
  * deterministic alternation for arm balance (sorted by bead id, index
    parity) -- never random;
  * structural validation of the warm keys (presence + ISO parse via the
    SAME `fromisoformat` the read-side `_parse_iso_ts` uses).

It does NOT do semantic classification, heuristic scoring, keyword matching,
or any planning/quality judgment. The single numeric constant, MIN_PER_ARM,
is an experiment sample-size floor (>=10/arm, contract + mem-0ut charter),
not a quality threshold -- and it FAILS LOUD rather than silently shrinking
an arm.

Safety net (dispatch mirror of the read-side `fork_warnings`): a warm
selection whose brain is missing `sessionId`/`builtAt`, or whose `builtAt`
will not parse, RAISES `WarmKeyError`. The wrapper must never route a warm
bead without all three keys -- a mislabeled warm bead silently inverts the
headline metric downstream.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Mapping, Sequence

# Exact literals the read-side asserts (armcompare.py: ARMS = ("warm","cold")).
ARM_WARM = "warm"
ARM_COLD = "cold"

# Metadata keys -- EXACT, grep+test-verified against the contract. Do not rename.
KEY_ARM = "gc.brain_arm"
KEY_FORK_TS = "gc.brain_fork_ts"
KEY_PARENT_SID = "gc.brain_parent_sid"
KEY_SELECTION = "gc.brain_selection"

# Selection provenance values (this dispatch side only; not read by the trim).
SEL_MATCH = "match"          # warm-eligible: scope ∩ blast-radius non-empty
SEL_NO_MATCH = "no_match"    # forced cold: empty intersection with every brain
SEL_BALANCE = "balance"      # warm-eligible but assigned cold to keep the arms balanced

# Experiment sample-size floor (>=10/arm). NOT a semantic threshold.
MIN_PER_ARM = 10


class ArmSelectionError(Exception):
    """Base class for loud dispatch-side refusals."""


class WarmKeyError(ArmSelectionError):
    """A warm bead cannot be stamped with all three required keys."""


class BalanceError(ArmSelectionError):
    """The candidate set cannot fill both arms to MIN_PER_ARM."""


@dataclass(frozen=True)
class Brain:
    """A forkable brain, from its `<repo>/.claude/brains/<name>.json` manifest."""

    name: str
    parent_sid: str          # manifest.sessionId  -> gc.brain_parent_sid
    fork_ts: str             # manifest.builtAt     -> gc.brain_fork_ts
    scope_files: frozenset[str]  # manifest.fileHashes keys (concrete paths)


@dataclass(frozen=True)
class Candidate:
    """An exploration-heavy A/B candidate bead and its blast-radius."""

    bead_id: str
    blast_radius: frozenset[str]  # concrete repo-relative paths the work touches


@dataclass(frozen=True)
class Assignment:
    """One bead's resolved arm, provenance, the brain it matched (warm), and
    the exact metadata blob to stamp."""

    bead_id: str
    arm: str
    selection: str
    brain_name: str | None
    metadata: Mapping[str, str]


def parse_iso_ts(value: str) -> datetime:
    """The read-side boundary parser (armcompare `_parse_iso_ts`): tolerate a
    trailing `Z` by mapping it to `+00:00`, then `datetime.fromisoformat`.

    A present-but-unparseable boundary RAISES -- a corrupt fork boundary must
    not pass to dispatch (mirrors `load_brain_built_at`)."""
    if not isinstance(value, str) or not value:
        raise ValueError(f"empty/non-string ISO timestamp: {value!r}")
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def load_brain(manifest_path: Path) -> Brain:
    """Build a Brain from a brains-CLI manifest. The in-scope file set is the
    `fileHashes` keys -- EXACTLY what the read-side `load_scope_files` uses for
    the distractor rate, so the dispatch intersection and the measurement agree
    on what 'in scope' means."""
    raw = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(raw, Mapping):
        raise ValueError(f"{manifest_path}: manifest must be a JSON object")
    name = raw.get("name")
    sid = raw.get("sessionId")
    built_at = raw.get("builtAt")
    hashes = raw.get("fileHashes")
    if not isinstance(name, str) or not name:
        raise ValueError(f"{manifest_path}: missing 'name'")
    if not isinstance(hashes, Mapping):
        raise ValueError(f"{manifest_path}: missing 'fileHashes' map")
    # sid/built_at may be absent in a malformed manifest; that is a WARM key
    # failure surfaced at stamp time (build_warm_metadata), not here, so a
    # cold-only run over a degenerate manifest still works.
    return Brain(
        name=name,
        parent_sid=sid if isinstance(sid, str) else "",
        fork_ts=built_at if isinstance(built_at, str) else "",
        scope_files=frozenset(str(f) for f in hashes),
    )


def best_brain_for(candidate: Candidate, brains: Sequence[Brain]) -> tuple[Brain | None, int]:
    """The brain maximizing |scope ∩ blast-radius|, with its overlap size.

    Pure set intersection. Deterministic tie-break: larger overlap wins; ties
    break on brain name ascending. Empty overlap with every brain -> (None, 0)."""
    best: Brain | None = None
    best_overlap = 0
    for brain in sorted(brains, key=lambda b: b.name):
        overlap = len(candidate.blast_radius & brain.scope_files)
        if overlap > best_overlap:
            best, best_overlap = brain, overlap
    return best, best_overlap


def build_warm_metadata(brain: Brain) -> dict[str, str]:
    """The warm key blob -- with the loud safety net. RAISES `WarmKeyError`
    unless all three keys are present and `fork_ts` parses as ISO-8601."""
    if not brain.parent_sid:
        raise WarmKeyError(f"brain {brain.name!r}: missing sessionId (gc.brain_parent_sid)")
    if not brain.fork_ts:
        raise WarmKeyError(f"brain {brain.name!r}: missing builtAt (gc.brain_fork_ts)")
    try:
        parse_iso_ts(brain.fork_ts)
    except ValueError as exc:
        raise WarmKeyError(f"brain {brain.name!r}: builtAt not ISO-8601: {exc}") from exc
    return {
        KEY_ARM: ARM_WARM,
        KEY_PARENT_SID: brain.parent_sid,
        KEY_FORK_TS: brain.fork_ts,
        KEY_SELECTION: SEL_MATCH,
    }


def build_cold_metadata(selection: str) -> dict[str, str]:
    """The cold key blob: arm only, NO fork keys (cold is never trimmed)."""
    return {KEY_ARM: ARM_COLD, KEY_SELECTION: selection}


def select_arms(
    candidates: Sequence[Candidate],
    brains: Sequence[Brain],
    *,
    min_per_arm: int = MIN_PER_ARM,
) -> list[Assignment]:
    """Resolve every candidate to an arm + its exact stamp blob.

    1. Eligibility (set intersection): a bead is warm-eligible iff some brain's
       scope-manifest overlaps its blast-radius; otherwise forced cold +
       no_match.
    2. Deterministic alternation: warm-eligible beads, sorted by bead id, split
       by index parity (even -> warm, odd -> cold/balance) so the cold arm is
       populated from genuine warm-eligible work, never random.
    3. Floor: each arm must reach `min_per_arm`, else `BalanceError` (refuse to
       launch an underpowered experiment).

    Output order matches the input order."""
    # Stage 1: eligibility via pure intersection.
    eligible: list[tuple[Candidate, Brain]] = []
    forced_cold: list[Candidate] = []
    for cand in candidates:
        brain, overlap = best_brain_for(cand, brains)
        if brain is not None and overlap > 0:
            eligible.append((cand, brain))
        else:
            forced_cold.append(cand)

    # Stage 2: deterministic alternation over warm-eligible (sorted by bead id).
    eligible.sort(key=lambda cb: cb[0].bead_id)
    arm_by_bead: dict[str, Assignment] = {}
    for idx, (cand, brain) in enumerate(eligible):
        if idx % 2 == 0:
            arm_by_bead[cand.bead_id] = Assignment(
                bead_id=cand.bead_id,
                arm=ARM_WARM,
                selection=SEL_MATCH,
                brain_name=brain.name,
                metadata=build_warm_metadata(brain),
            )
        else:
            arm_by_bead[cand.bead_id] = Assignment(
                bead_id=cand.bead_id,
                arm=ARM_COLD,
                selection=SEL_BALANCE,
                brain_name=None,
                metadata=build_cold_metadata(SEL_BALANCE),
            )
    for cand in forced_cold:
        arm_by_bead[cand.bead_id] = Assignment(
            bead_id=cand.bead_id,
            arm=ARM_COLD,
            selection=SEL_NO_MATCH,
            brain_name=None,
            metadata=build_cold_metadata(SEL_NO_MATCH),
        )

    assignments = [arm_by_bead[c.bead_id] for c in candidates]

    # Stage 3: loud floor check.
    warm_n = sum(1 for a in assignments if a.arm == ARM_WARM)
    cold_n = sum(1 for a in assignments if a.arm == ARM_COLD)
    if warm_n < min_per_arm or cold_n < min_per_arm:
        raise BalanceError(
            f"underpowered: warm={warm_n}, cold={cold_n}, need >={min_per_arm}/arm "
            f"(warm-eligible={len(eligible)}, no_match={len(forced_cold)}). "
            "Add more exploration-heavy candidates whose blast-radius overlaps a brain scope."
        )
    return assignments
