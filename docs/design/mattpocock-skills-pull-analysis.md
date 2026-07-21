# mattpocock/skills — pull/adapt analysis for Gas City

Source: `mattpocock/skills` (158k★, "Skills for Real Engineers. Straight from
my .claude directory"), read 2026-07-06. Full read of the meta layer + every
engineering/productivity SKILL + wayfinder.

## Top pull/adapt candidates (ranked)

1. **Skill-authoring discipline (`writing-great-skills` + `GLOSSARY.md` +
   `.agents/invocation.md`) — highest value, META.** Framework: *context load*
   (model-invoked, always in window) vs *cognitive load* (user-invoked, human is
   the index); *leading words*; *progressive disclosure*; *completion criterion*
   (checkable AND exhaustive); *premature completion*; *negation as a failure
   mode*; *no-op test*. We own 100+ skills/compasses with no shared authoring
   standard. Distill into a `rules-distill`-style house rule; keep the
   loads/leading-word/negation vocabulary verbatim (language-agnostic).

2. **Router skill (ask-matt style).** Maps not just skills but *flows between
   them* (idea→ship, on-ramps, standalones). Our compasses index *subsystems*;
   we have no top-level "which skill/flow do I reach for" router over 100+
   entries.

3. **`domain-modeling` + `CONTEXT.md` glossary.** Stateful grilling that writes a
   project glossary + inline ADRs. Our `grill-me` is stateless, no paper trail.
   Gas City jargon (bead, rig, polecat, formula, sling, mayor, order) is
   re-learned by every fresh agent; a `CONTEXT.md` glossary cuts token spend +
   naming drift. Point at `gas-city/CONTEXT.md`.

4. **`diagnosing-bugs`.** Force a *tight, red-capable* feedback loop (one command,
   already run, asserts the exact symptom) BEFORE any hypothesis — "no
   red-capable command, no Phase 2". 10-rung ladder for building a loop; tagged
   `[DEBUG-a4f2]` logs for single-grep cleanup; "raise the reproduction rate" for
   flakes. Composes with our `mechanic` (mechanic = read from source;
   diagnosing-bugs = the reproducing loop). Go-flavor the loop menu
   (`go test -run`, curl supervisor API :8372).

5. **Bead-brief durability spec (`triage/AGENT-BRIEF.md`).** "Durability over
   precision": no file paths, no line numbers, behavioral not procedural,
   complete testable acceptance criteria, explicit out-of-scope. Our beads rot
   because they name file:line. Fold into `gc sling`/formula bead-authoring
   guidance.

6. **`wayfinder` (in-progress).** Plan work too big for one session as a *map* of
   investigation tickets: *destination*, *fog of war*/not-yet-specified,
   *frontier* (open+unblocked+unclaimed), claim-by-assign, one ticket per
   session. Almost a formal spec of the mayor's own planning loop. Maps onto
   beads natively (`bd dep --blocks`, assignee=claim); "Decisions so far" index
   mirrors our decisions rig.

7. **`code-review` two-axis + `codebase-design`.** Splits **Standards** (repo
   conventions + portable Fowler smell baseline) from **Spec** (faithfully
   implements the originating issue), parallel sub-agents that never rerank
   across axes. Our `/review` is reuse/quality/efficiency+Codex; the explicit
   *Spec-conformance axis* + no-rerank rule is a distinct lens. Wire the Spec
   axis to read the originating bead/PRD. `codebase-design` deep-module
   vocabulary (seam/adapter/depth; deletion test; "one adapter = hypothetical
   seam, two = real") is worth borrowing.

## Already covered — skip

- `grilling`/`grill-me` → our `grill-me`
- `handoff` → our `gascity-handoff-write` (ours auto-prefills from memory)
- `research` → our `deep-research` (firecrawl/exa; Matt's is thinner)
- `implement` → our dispatch formulas (`mol-do-work`)
- `tdd` → our `tdd-workflow` — but steal two ideas: confirm seams with the user
  BEFORE writing tests, and the tautological-test anti-pattern (expected values
  must come from an independent source)
- `resolving-merge-conflicts` → our rebase practice (cheap standalone add: never
  `--abort`; resolve per-hunk intent)
- `setup-matt-pocock-skills` → assumes GitHub/Linear/local-markdown trackers;
  doesn't fit beads/dolt

## META lessons for how WE author skills (the real prize)

- **Invocation is one explicit axis.** Declare model-invoked (keeps `description`
  = permanent context load) or user-invoked (`disable-model-invocation: true`,
  zero context load, you are the index). Model-invoke only if an agent/skill must
  reach it autonomously. Many fork-local skills could drop their descriptions.
- **Router skill as the cure for cognitive load** (see #2).
- **Leading words (Leitwort).** Anchor a behavior in one pretrained token,
  repeated: *tight*, *red*, *seam*, *deep module*, *fog of war*, *tracer bullet*.
  Cheaper/sharper than a restated triad ("fast, deterministic, low-overhead" →
  *tight*); doubles as the invocation hook when the same word lives in
  prompts/docs/code.
- **Negation is a failure mode.** Steering by prohibition drags the banned
  behavior into context; prompt the positive target instead. FLAG: our
  `gas-city/CLAUDE.md` + house-rules are almost entirely "Don't…" lists. Hard
  guardrails stay; many could be recast as positive targets.
- **Completion criterion + premature completion.** Each step's done-condition
  checkable AND exhaustive ("every modified model accounted for", not "produce a
  change list"); split off post-completion steps only when a fuzzy bound + observed
  rushing coincide.
- **Progressive disclosure + no-op test.** SKILL.md = steps + primary reference;
  push the rest to sibling files behind context pointers. Prune sentence-by-
  sentence: delete any line the model already obeys by default.
- **Maintenance trigger.** Bake into CLAUDE.md: when you add/rename/change a
  skill, re-sync its docs page and re-read the router so it doesn't lie.

## Skeptical notes

- `improve-codebase-architecture` leans on a Tailwind+Mermaid CDN HTML report
  (fine for the dashboard, awkward for the Go CLI — keep the scan/deletion-test
  idea, drop the report).
- `wizard`/`loop-me`/`teach` are personal-workflow shaped; don't transfer.
- The whole repo assumes solo-human-in-the-loop; our mayor-dispatches-workers
  model means the HITL framing in `wayfinder`/`grilling` needs re-pointing at
  bead handoffs, not a live human.
