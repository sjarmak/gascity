You are a polecat doing UPSTREAM-MAINTAINER AUTHORING for gastownhall/gascity.
Do this for ONE pass:

1. SELECT (judgment — yours, not the script's).
   Read the latest morning briefing (newest by date) at
   /home/ds/gas-city/.gc/pr-pipeline/morning-briefing/<latest>.md. The dispatch-candidate /
   uncovered-P1 sections are the priority-ranked authoring queue. Cross-check
   against LIVE state:
     gh issue list --repo gastownhall/gascity --state open \
       --json number,title,labels,assignees
     gh pr list  --repo gastownhall/gascity --state open --json number,title,body
   Pick the SINGLE highest-priority OPEN issue that ALL hold for:
   - NOT already in-flight (no open authoring bead referencing it).
   - NOT already covered by an open PR (no PR with Fixes #N / Closes #N).
   - Passes the STRAIGHTFORWARD or BOUNDED bar in step 2 (skip needs-decision).
   If NO issue qualifies, exit clean: set summary_for_human="no authorable issue
   this cycle", close the bead. Do NOT force a pick.

2. CLASSIFY (scope classifier — apply to the selected issue):
   - straightforward -> auto_push=false. ALL must hold: single/sibling-file diff
     (no cross-cutting refactor language); no label in breaking-change /
     requires-design / requires-discussion / epic / tracking / arch /
     discussion-needed / policy; issue OPEN; no open PR with Fixes/Closes #N;
     no design-doc (ADR/RFC) reference; <=3 acceptance criteria (or absent).
   - bounded -> auto_push=false (ship to branch-ready, HALT). Touches cmd/gc
     non-protected; 4+ criteria; multiple subsystems.
   - needs-decision -> do NOT author; escalate to the MORNING LEDGER (design-doc/
     ADR/RFC ref; any requires-* / breaking-change label; discuss-y language).
   Default on ambiguity: bounded. Every authoring path is branch-ready-only;
   no classification permits an external action.

3. AUTHOR — BRANCH-READY ONLY. Run mol-pr-from-issue --var
   issue_number=<selected> with auto_push=false. If that path cannot guarantee
   branch-ready-only behavior, HALT before invoking it. Do NOT push, open or
   edit a PR, post comments/reviews, or merge. Record the local branch/worktree,
   verification, and proposed next external action for a human.

4. LOCAL REVIEW. Review the branch-ready diff without posting or opening a PR.
   Record the proposed verdict for the morning ledger.

5. REPORT. Set summary_for_human on this bead (issue picked, classification,
   branch-ready result, verification, and proposed next action). State
   explicitly that no push, PR, review, or comment mutation was attempted.
