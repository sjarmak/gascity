# Edit suggestions: "The factory is an observatory"

Suggestions-only pass, 2026-07-11. Target: `/home/ds/projects/aoa/docs/the-factory-is-an-observatory.md`.
Cross-checked against `or-for-software-factories.md` (the paper) and `or-for-software-factories-literature.md` (the review). The draft was not modified.

---

## 1. STRUCTURE

The arc is scene → analogy → planning depth → anytime pragmatism → fairness → build-vs-buy → replay, and five of the six sections cash out their factory lesson at or near the exit:

- **Scene + analogy (intro):** lands. Closes on "it has published what it learned," which is the correct runway: it promises a record, and every section after it withdraws from that record.
- **Two monasteries:** cashes out explicitly. "The factory translation writes itself" through "let the weather be the weather" is the model for how every section should exit.
- **The sky does not wait for proofs:** cashes out twice, once for the epoch planner ("the merge queue, like the sky, keeps moving") and once for the dispatcher warning ("there was work to do and we provably missed it"). Strongest section.
- **Fairness:** the only section that ends without cashing out. The factory lesson lives mid-paragraph ("the defense is a coefficient, and starving Voyager is not something the schedule can do while claiming to be optimal"), and then the section exits on a catalog of correspondences: setup tax, minimum durations, arraying. The mappings are good; they are the wrong final beat. End on the directive, not the dictionary. See line edit 7.
- **Every dome:** cashes out ("the scheduler is a product, not a per-team script ... before the fifth reimplementation rather than after").
- **Replay (close):** pivots rather than summarizes. "Your nights will hold still while you measure them" turns the whole analogy into an assignment. Do not touch the final sentence.

**Weakest transition: Fairness → Every dome.** Fairness trails off into the correspondence catalog, and "The least flattering pattern in this literature is also the most familiar" re-enters through the literature rather than through anything Fairness established; the two sections touch only via the word "literature." Fixing the Fairness ending (line edit 7) also repairs the transition for free: a section that ends on "the DSN wrote the coefficient down" gives "the least flattering pattern" a real foil, a lesson adopted followed by a mistake repeated.

**Secondary structural nit:** the enumeration scaffold "The first thing the observatory record settles ..." / "The second lesson is about perfectionism" labels exactly two of five lessons and then abandons the count. Either number all of them (worse) or drop the labels (line edits 2 and 3).

## 2. CLAIM-SAFETY

Checked every number and attribution in the draft against the paper and the review.

**Clean, no drift:**
- ZTF: 30-minute blocks, ILP maximizing sky searched per unit time ("volumetric survey speed" in the review), TSP tour within blocks, in production since 2018, `2019PASP..131f8003B`. Matches both companions.
- Rubin/Naghib: MDP formulated then declined, weighted sum of handcrafted features, offline weight tuning against a simulator, re-decide per exposure, no replanning needed after weather, `2019AJ....157..151N`. Matches. (Review specifies "evolutionary optimization" for the tuning; the draft's vaguer "tuned offline" is safely under-specific, fine.)
- Scenario-tree uncertainty as a 2025 preprint only, `2025arXiv250403666R`. Matches both.
- MUSHROOMS: 500 s cap, 951 simulated alerts, most instances proved optimal, 100 s cap would have cost 64 truncated solves, `2022ApJ...935...87P`. All four numbers match the review verbatim.
- Handley: 7 of 360 instances where the heuristic reported infeasible and the exact method proved feasible, `2024AJ....167...33H`. Matches paper and review.
- LCO 2014 deployment with the 2015 bibcode; M4OPT as first multi-mission open-source framework, 2025; IPROS 2025 relay network. All match. (The review's §7 dates the rebuilds by publication year, "LCO in 2015, ZTF in 2019"; the draft uses deployment years, which is what the paper itself does. Consistent, no action.)
- "Thirty-five years" in the close matches the paper's framing from SPIKE 1990.

**Drift, fix before posting:**
- **D1. DSN fairness terms.** Draft: "terms balancing each mission's fraction of satisfied requests and the load across antennas." Review: "fairness terms balancing satisfied-time fractions across missions." Two drifts in one clause: requests vs time, and an antenna-load-balancing term that appears in neither companion. Align to satisfied time and drop the antenna-load claim unless verified directly in `2021arXiv211111628C`.
- **D2. "the papers report the resulting fairness numbers the way they report throughput."** Neither companion supports this; the review says the model was "validated against a real DSN week." Soften or verify against the source paper.
- **D3. WFST has no bibcode.** "WFST built one." is a citable observatory fact with no bracket, and the draft's own header promises every such fact carries one. WFST appears in the review's §7 also uncited. Find the bibcode or cut the sentence; IPROS alone carries the "kept rebuilding" point.
- **D4. "3% off the true optimum."** Reads as a citable figure but is invented; the paper's actual MUSHROOMS number is 2 to 11% gaps where proofs don't close. Sitting two paragraphs from four real numbers, an invented one is expensive. See line edit 5.
- **D5. "The datacenter-scheduling world already treats this as table stakes."** The companions support one paper (Decima) establishing the discipline, and the review credits it as that paper's load-bearing contribution, not the field's norm. See line edit 9.
- **D6. "a thirty-line priority index."** No companion sizes Rubin's scoring function in lines of code. Invented specificity in the final paragraph, the worst place for it. See line edit 11.

## 3. LINE EDITS

1. §intro (para 2): "I have come to think of that dome as the most honest picture available of what an automated software factory is trying to be, and the longer you hold the two side by side, the less metaphorical the comparison feels." → "That dome is the most honest picture available of what an automated software factory is trying to be, and the longer you hold the two side by side, the less metaphorical the comparison feels.", the claim is stronger owned outright; keep the first person only if the blog's register wants a narrator.
2. §Two monasteries: "The first thing the observatory record settles is a question every factory builder eventually loses sleep over" → "The observatory record settles a question every factory builder eventually loses sleep over", removes half of an enumeration scaffold the draft never completes.
3. §The sky does not wait: "The second lesson is about perfectionism. When two neutron stars collide and LIGO issues an alert, ..." → "When two neutron stars collide and LIGO issues an alert, ...", the label sentence is flow-narration; the collision is the concrete opening the section already owns.
4. §The sky does not wait: "the team runs it with a 500-second time limit on the solver" → "the team caps the solver at 500 seconds", tighter, and "caps" sets up "Hard time cap" in the next paragraph.
5. §The sky does not wait: "a plan that is 3% off the true optimum but exists now beats a perfect plan for a night that has already changed" → "a plan a few percent off the true optimum but in hand now beats a perfect plan for a night that has already changed", removes the invented figure (D4); alternatively cite the paper's real 2 to 11% gaps.
6. §Fairness: "explicit terms balancing each mission's fraction of satisfied requests and the load across antennas, and the papers report the resulting fairness numbers the way they report throughput" → "explicit terms balancing each mission's share of satisfied time, validated against a real week of DSN operations", fixes D1 and D2 in one move.
7. §Fairness (section ending): after "...several agents co-assigned to one epic under a single completion constraint." add → "The translation is one term in the dispatcher's objective: give fairness a coefficient before the week you need it.", the only section that exits without cashing out, and this ending also repairs the transition into §Every dome.
8. §Every dome: "WFST built one." → "WFST built one [bibcode]." or delete, keeps the header's every-fact-has-a-bibcode promise true (D3).
9. §Replay: "The datacenter-scheduling world already treats this as table stakes [2018arXiv181001963M]" → "Datacenter scheduling already ran this play [2018arXiv181001963M]: fix the arrival trace, or the noise swamps the policy signal", claims exactly what Decima supports (D5) and smuggles in the mechanism.
10. §Replay: "So the closing move belongs to the factory, and it is concrete." → "The closing move belongs to the factory, and it is concrete.", the leading "So" is connective filler before the strongest paragraph.
11. §Replay: "a thirty-line priority index of the kind Rubin points its billion-dollar survey with" → "a priority index of the kind Rubin points its billion-dollar survey with", drops unverifiable specificity from the final paragraph (D6); "of the kind Rubin points its survey with" already carries the smallness.
12. §intro (para 1): "and the scheduler's whole design assumes it" → "and the scheduler was designed for exactly this", slightly sharper agent; optional.
13. §Two monasteries: "replacing all of that machinery with a scoring function" → "replacing the machinery with a scoring function", "all of that" gestures where it could point; optional.

Everything else at section exits should stay as written: "let the weather be the weather", "there was work to do and we provably missed it", "before the fifth reimplementation rather than after", and the final sentence are the draft's best lines.

## 4. TITLE / HOOK

The current opening holds. "At 20:47 on Palomar Mountain, the marine layer starts climbing the ridge, and a computer in the dome of the 48-inch Samuel Oschin Telescope throws away its plan for the night." is concrete, kinetic, and pays off two sentences later with "Nobody is alarmed." Any alternative trades that specificity for cleverness. The title also pairs well against the paper's more argumentative "The factory's empty seams are solved scheduling problems": declarative image for the post, thesis for the paper. No alternatives offered.

## 5. PUBLISH-READINESS

1. **Citation integrity sweep.** Fix D1 through D6 above; the two that require going to a source rather than just rewording are the DSN fairness-reporting claim (D2, check `2021arXiv211111628C` directly) and the WFST bibcode (D3). The post's credibility rests on the header's promise that every fact resolves at ADS, so one uncited facility and one invented percentage are the highest-leverage fixes in the document.
2. **Citation rendering and the companion link.** Bracketed bibcodes are opaque to blog readers. Decide the published form: hyperlink each to `ui.adsabs.harvard.edu/abs/<bibcode>` or add a short references footer. Same decision for the header: the italic draft note referencing `or-for-software-factories.md` must become a real link to the published paper or be cut, because the body never otherwise points readers at it, and the close ("take your recorded traces") is exactly where a "the full argument, with the adoption path, is here" link belongs.
3. **The Fairness ending plus a final voice pass.** Apply line edit 7 (the one structural gap), remove the first/second enumeration scaffold (edits 2 and 3), then run one writing-voice pass over the final text. The composite-color declaration in the header covers the opening scene's invented details (20:47, the marine layer timing), but keep it in whatever form the header takes after item 2.
