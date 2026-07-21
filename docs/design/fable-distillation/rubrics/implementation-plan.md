# Judge Rubric — Implementation Plan from Issue

**Task type:** root-cause analysis plus executable fix plan, produced from a GitHub issue against a real read-only checkout of the repository.

**Authored:** 2026-07-06 by Claude Fable 5, calibrated against the 2026-07-07 golden run (task-02, issue #3972, gastownhall/gascity at pinned SHA). This rubric is generic to the task type: it scores any issue-to-plan output on any repo. Facts from issue #3972 appear only inside the quoted anchor examples.

---

## What the task demands

The candidate was given an issue and a checkout, and asked for: (1) a root-cause hypothesis with verified file:line evidence, (2) blast radius, (3) at least two fix candidates with a reasoned pick, (4) step-by-step implementation with per-step verification, (5) a test strategy shipping in the same commit(s), (6) maintainer-rejection risks with pre-emption — and an explicit instruction to say so with evidence if the issue's premise is wrong.

The single quality that separates a 5 from a 3 across every dimension: **the plan is derived from the checkout, not from the issue text.** A 3-level plan is a fluent elaboration of the issue's own framing; a 5-level plan has visibly collided with the code and been reshaped by what it found there.

## Judge setup

Judges score with the candidate text AND the same pinned checkout the candidate had. Several checks below are marked **[repo]** — they cannot be done from the text alone. If the checkout is unavailable, score those dimensions provisionally and cap overall judge confidence at **medium** (see Scoring procedure).

---

## Dimensions

Weights in parentheses. Score each 1–5 (integers; use the 5/3/1 descriptors as anchors, 4 and 2 for in-between).

### D1. Evidence-grounding (20%)

Are the file:line claims real, specific, and load-bearing — verified against the checkout rather than asserted?

- **5** — Citations are dense, exact, and falsifiable: line numbers, symbol names, and often quoted literals ("the string at `:176` reads …", "constant X = 10s at `file:line`"). Claims about _absence_ are stated as searched-for absences ("zero references to package Y in this file", "symbol Z has exactly one caller"), which only someone who ran the search would write. Spot-checking citations against the checkout confirms them, including line ranges landing on the named symbol. The plan states up front that references were verified against the pinned checkout, and the citations bear that out.
- **3** — Citations name real files and mostly-real symbols but line numbers are approximate or missing; a spot-check finds the symbol exists but not at the cited line, or the cited code does something adjacent to what is claimed. Absence claims ("nothing handles X") are asserted without evidence of a search. The evidence supports the narrative but would not survive a hostile reviewer.
- **1** — Citations are plausible-sounding fabrications: files that don't exist, symbols invented from the issue's vocabulary, or line numbers attached to prose that no code matches. Or the plan cites nothing concrete at all — root cause is argued from the issue text and general architecture intuition.

**Judge check [repo]:** sample at least 5 file:line citations spanning the root-cause and blast-radius sections. For each: does the file exist, does the named symbol sit at (or within a few lines of) the cited location, and does the code actually do what the sentence claims? One fabricated citation caps D1 at 2; one materially wrong _behavioral_ claim about real code caps D1 at 3.

### D2. Premise-checking (15%)

Did the candidate test whether the issue is valid — per claim, not wholesale — before planning?

- **5** — The plan opens with an explicit premise check that treats the issue as a set of separable claims and adjudicates each one against the code: confirms some, refutes or narrows others with evidence, and detects issue vocabulary that doesn't exist in this repo (translating it to the repo's actual concept instead of planning against a phantom). Where part of the issue is already fixed upstream, the plan says so and excludes it from scope.
- **3** — The plan confirms the issue is "real" in aggregate and proceeds. No per-claim adjudication; issue terminology is adopted verbatim without checking it maps to anything in the checkout. If a sub-claim happens to be wrong, the plan silently plans around it or silently includes it.
- **1** — The premise is assumed. The plan restates the issue's causal story as the root cause. If the issue is partially or wholly invalid, the plan builds a fix for the invalid part.

**Judge check:** text-alone — is there a distinct premise-check section (or equivalent) with verdicts per claim? **[repo]** — grep for the issue's key terms in the checkout: if a term the issue uses does not exist in the code and the candidate planned against it anyway, that is a 1-2 signature. Conversely, if the candidate claims a term/feature doesn't exist, verify that too.

### D3. Coverage / blast radius (20%)

Does the plan map everything the fix touches — callers, config paths, goroutine/concurrency boundaries, cross-subsystem contracts, output-format consumers — or only the function being edited?

- **5** — Blast radius is a distinct analysis, organized by boundary type, and includes _second-order_ interactions a naive fix would break: e.g. two code paths that each apply a transformation, so routing one through the other double-applies it; a registry that rejects unregistered types downstream; a lock inside which new work must not run; consumers of an output format whose shape must stay stable; the explicit statement of which surfaces are _not_ touched (config, wire formats) and why. Layer boundaries (which package may import which) are identified where the fix crosses them.
- **3** — Blast radius lists the direct callers of the changed function and the obvious adjacent module, but misses cross-subsystem effects, concurrency boundaries, or downstream consumers. Reads like the output of one grep for the function name.
- **1** — Blast radius is the edited function plus a generic gesture ("update related tests", "check callers"). No named callers, no boundaries.

**Judge check [repo]:** pick the primary function/seam the fix modifies and independently enumerate its callers and downstream consumers (grep/callers). Compare against the plan's list — a caller or consumer the plan misses that the fix would plausibly break is the key negative signal. Text-alone: does the section name concrete boundary _types_ (goroutines, locks, registries, serialized formats, import layering) or just files?

### D4. Decomposition & fix selection (15%)

Are there genuinely distinct fix candidates, weighed on engineering grounds, and is the chosen work split into reviewable, independently-testable units?

- **5** — At least two candidates that are _architecturally_ different (not the same fix at two sizes), each with concrete costs stated in the repo's own terms; the rejection rationale is specific enough that it could only apply to this codebase ("pins one goroutine per member inside the server", "duplicates logic subsystem X already owns"). Out-of-scope-but-real work is captured as an explicitly deferred candidate with a reason, not dropped. The chosen work is split into commits/phases along ask or risk boundaries, each carrying its own tests, ordered so early commits stand alone.
- **3** — Two candidates exist but one is a strawman (obviously worse, listed to satisfy the requirement) or the comparison is generic ("more robust vs simpler"). Decomposition is a linear step list with no commit/phase boundaries or with boundaries that don't correspond to independently shippable units.
- **1** — One fix, presented as the fix. Steps are a narrative ("then update the handler to be more resilient") rather than discrete actions.

**Judge check:** text-alone — could the rejected candidate's rejection paragraph be pasted into a plan for a different repo without edits? If yes, the weighing is generic. Does the rejected option get concrete mechanics (which existing function it would call, what it costs), or only adjectives?

### D5. Verification planning (15%)

Does each step carry a check that could actually _fail_ if the step is wrong — distinct from restating the step?

- **5** — Per-step verification commands are concrete and runnable (specific test invocations with run-filters, build of the touched package), and the asserted condition is an _observable consequence_, not the step's own description: byte-identical output on the unchanged path, absence of a marker string in the changed path, an item surviving in a specific state under specific conditions. Existing conformance/regression suites that would catch the change are named. A final full-gate step exists, with failures classified against a clean baseline of the unmodified base commit.
- **3** — Verification exists but is coarse ("run the tests", one package-wide `go test` repeated after every step) or circular ("verify the event is emitted" as the check for "emit the event", with no mechanism that would detect a wrong implementation). No baseline discipline.
- **1** — Verification is absent, or is "manually confirm it works", or every check is the implementation step re-worded with "verify that" in front.

**Judge check:** text-alone — for each step, ask "if the engineer implemented this step subtly wrong, would the stated check catch it?" Count the steps where the answer is no. **[repo]** — spot-check that named test files/suites/run-filters correspond to real tests or plausible new ones in existing test files.

### D6. Executability (10%)

Could a mid-level engineer run this plan without making judgment calls the plan should have made?

- **5** — Every step names the file, the symbol, the pattern to follow (pointing at an existing in-repo precedent by file:line), and the decision already made: what the new signature is, where the seam lives, what must _not_ be re-implemented and why, which side of a boundary code goes on. Ambiguity is resolved in the plan ("emit after the transaction commits, never inside the lock"), not delegated ("handle locking appropriately").
- **3** — Steps name files and intent but leave real decisions open: "add an event type" without where it registers, "queue the message" without which existing enqueue path to use, "avoid double-wrapping" without saying how. An engineer could execute it, but two engineers would produce materially different diffs.
- **1** — Steps are goals, not actions ("make delivery reliable", "improve error handling"). The plan is a restated issue with section headers.

**Judge check:** text-alone — pick the two most complex steps and ask what an engineer would still have to decide. Signature phrases of a 3-or-below: "as appropriate", "consider", "e.g." on load-bearing decisions, "similar to how other code does it" without a pointer.

### D7. Calibration & risk anticipation (5%)

Does the plan's confidence track its evidence, and does it anticipate the specific objections a maintainer of _this_ repo would raise?

- **5** — Verified claims are stated flatly; inferred claims are marked as such; scope is defended (what is deferred, and why deferring is safe). The rejection-risks section names repo-specific objections (latency on the hot path, output-shape stability, layering violations, noise in a shared feed, behavior change a fix _intentionally_ introduces) and points at the exact plan step that pre-empts each. Where the fix will make a previously-hidden failure class _visible_ (and thus look like a regression), the plan says so proactively.
- **3** — Risks section exists but is generic (breakage, performance, "needs review"), unlinked to plan steps. Confidence is uniform whether the claim was verified or guessed.
- **1** — No risks, or boilerplate. Hedging on verified facts and certainty on unverified ones.

**Judge check:** text-alone — for each listed risk, is there a named plan step or test that answers it? A risk with no pre-emption, or a pre-emption with no risk, is the 3-level tell.

---

## Failure signatures

The characteristic ways weaker models fail this task type. Each entry: the signature, then how the judge detects it.

1. **Plausible-but-unverified file:line citations.** The text is dense with `path/file.go:123` references that were pattern-completed, not read. _Detect [repo]:_ sample citations as in D1. Tells that predict fabrication even before opening the repo: suspiciously round line numbers, symbols named with the issue's vocabulary rather than the repo's, every citation being a single line (real investigation produces ranges and quoted literals), and no absence-claims anywhere (fabricators only assert presence).

2. **Blast radius limited to the edited function.** The "blast radius" section is the diff's file list. _Detect [repo]:_ enumerate callers/consumers of the primary changed seam yourself and diff against the plan. _Detect (text-alone):_ no goroutine, lock, serialization, layering, or output-consumer language anywhere in the section; no statement of what is deliberately untouched.

3. **Verification steps that restate the implementation step.** "Step: emit an event on failure. Verify: check that an event is emitted on failure." _Detect (text-alone):_ apply the D5 could-this-fail test per step; three or more circular checks caps D5 at 2.

4. **Planning a fix for an invalid premise.** The issue is partly wrong, already fixed, or uses concepts that don't exist in this repo, and the plan builds against the phantom. _Detect [repo]:_ grep the issue's key nouns and the plan's claimed-broken behavior in the checkout; check git log/blame around the cited area for an existing fix. This is the single highest-severity failure — it caps D2 at 1 and the overall verdict at "reject" regardless of other scores, because the prompt explicitly instructed the candidate to check.

5. **Alternatives listed but not genuinely weighed.** Candidate B exists only to be rejected; its rejection paragraph contains no repo-specific mechanics. _Detect (text-alone):_ the portability test from D4 — a rejection that transplants cleanly to any other repo was never weighed. Also: candidate B is candidate A with a smaller scope, not a different mechanism.

6. **Issue-echo root cause.** The root-cause section paraphrases the issue's own causal story with citations decorating it, adding nothing the issue author didn't already know. _Detect (text-alone + repo):_ does the plan's causal chain contain at least one link the issue does not mention (a constant's actual value, an existing-but-unwired mechanism, a second failure path)? If every fact in the root cause appears in the issue text, D1 and D2 both cap at 3.

7. **Scope creep / unrequested machinery.** New config keys, feature flags, abstractions, or "while we're here" refactors the issue never asked for. _Detect (text-alone):_ map every implementation step back to a numbered ask or a named risk; orphan steps are creep. Note the inverse is 5-level behavior: explicitly stating "no new config surface" is a positive signal.

8. **Tests that ship later, or assert mechanism instead of symptom.** The test plan is "add tests in a follow-up," or every test asserts internal bookkeeping while nothing asserts the user-visible symptom from the issue is gone. _Detect (text-alone):_ is there one named test the plan itself identifies as _the regression test for the reported symptom_, and does the plan bind tests to the same commit as the source change?

---

## Anchor examples (5-level behavior, from the 2026-07-07 golden run)

Quoted from the golden output on issue #3972; these anchor what a 5 looks like, not facts a candidate must reproduce.

1. > "**`pending_thread` does not exist upstream.** Zero hits across the checkout; it is downstream-shim terminology. Ask (d) is reframed below as reconciliation from the durable extmsg transcript cursor…"

   **Why:** premise-checking at the 5 level — the candidate grepped the issue's own vocabulary, found it doesn't exist in this repo, said so with a searched-absence claim, and translated the ask into the repo's real concept instead of planning against a phantom (D2).

2. > "**Every failure in this function is `log.Printf` only** (`:146`, `:176` \"notify %s failed\", `:198`). Nothing is enqueued, retried, or dead-lettered."

   **Why:** evidence-grounding at the 5 level — three exact line citations, one with the quoted literal string at that line, plus an absence claim ("nothing is enqueued") that only a completed search produces. Every clause is falsifiable against the checkout (D1).

3. > "the queued drain path applies its own framing (`formatNudgeInjectOutput` / `formatNudgeRuntimeMessage`, used at `cmd_nudge.go:500-505`), so routing the reminder through the queue risks double-framing. Must be handled."

   **Why:** blast radius at the 5 level — a second-order interaction between the chosen fix and a subsystem the fix does not edit, discovered before implementation and converted into an explicit plan step. A 3-level plan discovers this in review, or ships the double-wrap bug (D3).

4. > "**B (rejected): widen the readiness window in place** … Rejected: pins one goroutine per member for minutes inside the API server; still fire-and-forget (a crash/restart between accept and paste loses the event with no trace, which is the reported symptom); duplicates delivery logic the queue+poller already own; does nothing for ask (c). It patches the window; A removes the class."

   **Why:** genuine weighing at the 5 level — the rejected candidate gets concrete mechanics and four repo-specific costs, one of which ties back to the issue's reported symptom. None of it transplants to another repo (D4).

5. > "assert `EnqueueSessionNudge` is called with the resolved session ID, source `extmsg`, and an **unwrapped** body (no `<system-reminder>` substring). This is the regression test for the reported symptom: the notify no longer evaporates when the target is cold."

   **Why:** verification at the 5 level — the assertion is an observable consequence (a specific substring must be absent) rather than a restatement of the step, and the plan explicitly binds one named test to the issue's user-visible symptom (D5).

---

## Scoring procedure

1. **Read the candidate end-to-end once** before scoring anything. Note where it collides with the checkout versus where it elaborates the issue.
2. **Run the [repo] checks** (D1 citation sample, D2 vocabulary greps, D3 independent caller enumeration, D5 test-name spot-check). Record what you ran and what you found — these become citable evidence.
3. **Score each dimension 1–5.** For every score, cite either (a) a direct quote from the candidate's own text, or (b) a repo finding ("cited `foo.go:88`, symbol is actually at `:410` and does Y"). A score with no citation is invalid.
4. **Apply caps:** failure signature 4 (invalid premise, fix planned anyway) → D2 = 1 and overall verdict = reject. Fabricated citation → D1 ≤ 2. Three or more circular verification steps → D5 ≤ 2.
5. **Compute the weighted overall:** D1×0.20 + D2×0.15 + D3×0.20 + D4×0.15 + D5×0.15 + D6×0.10 + D7×0.05.
6. **Verdict bands:**
   - **≥ 4.5 — golden-equivalent.** Executable as written; a maintainer would review the resulting PR on its merits.
   - **3.5–4.4 — acceptable.** Sound plan, gaps a senior engineer would patch in an hour; usable with supervision.
   - **2.5–3.4 — needs rework.** Real investigation happened but the plan cannot be executed without re-deriving major pieces.
   - **< 2.5 — reject.** Issue-echo, fabricated evidence, or unexecutable narrative.
7. **Judge confidence — required field,** one of high / medium / low, with a one-line reason. Cap at **medium** if the checkout was unavailable or if fewer than 5 citations could be sampled; cap at **low** if scoring was text-alone on a candidate whose plausibility depends primarily on its citations.

**Report format per candidate:**

```
D1 evidence-grounding:   <1-5> — <citation/evidence>
D2 premise-checking:     <1-5> — <citation/evidence>
D3 coverage:             <1-5> — <citation/evidence>
D4 decomposition:        <1-5> — <citation/evidence>
D5 verification:         <1-5> — <citation/evidence>
D6 executability:        <1-5> — <citation/evidence>
D7 calibration:          <1-5> — <citation/evidence>
Caps applied:            <none | which>
Weighted overall:        <x.xx> → <verdict band>
Judge confidence:        <high|medium|low> — <reason>
Repo checks performed:   <commands/lookups run and outcomes>
```
