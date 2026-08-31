# Reviewing Gas City changes

Code review is open to everyone. You do not need merge access, prior project
history, or an invitation to test a pull request and leave a useful review.
Review distributes judgment; it does not transfer merge authority.

This policy is informed by [Open Code Review](https://shaping.systems/blog/open-code-review/).

## What counts as a review

A review should name:

- the exact commit SHA reviewed;
- the behavior, files, or risks inspected;
- the checks actually run; and
- anything material that was not checked.

Useful review work includes reproducing the reported behavior, running the
change locally, checking that tests exercise the claim, probing failure paths,
and reading documentation as a new user. An approval without that evidence is
encouragement, not a review record.

## When a change is ready for final review

- Trivial typo and documentation-only changes need one independent approval.
- Other changes need two independent approvals from people other than the
  author.
- Security, architecture, wire-format, and durable-state changes also need a
  designated maintainer or an area owner named in
  [.github/CODEOWNERS](.github/CODEOWNERS) to resolve the high-impact decision.

When a second independent reviewer is unavailable, a change may proceed with
one independent approval only if the pull request records the open request for
a second reviewer, the missing approval, and the final maintainer's explicit
acceptance of that shortfall. The author cannot supply the independent approval
or accept the exception. This is a visible bootstrap path, not a silent waiver.

Maintainer-authored changes use the same gate. A maintainer may perform final
review and merge, but may not replace the independent reviews with their own
authority.

## Findings and scope

Authors must address every material finding or decline it with a reason. Mark
non-blocking ideas as suggestions so they do not become accidental merge gates.
Authors may decline feedback that expands the pull request beyond its stated
goal; that work belongs in a separate issue or change.

A verified defect in code we maintain should normally become an owned patch,
tests, and a pull request brought to the review gate. Filing an issue is
tracking, not a fix. An issue-only result is appropriate when a fix already has
an owner, belongs in another repository, needs a maintainer policy decision,
crosses a security or publication boundary, or cannot yet be reproduced. State
that reason and the single fact or decision that would unblock a patch.

If reviewers cannot resolve a design disagreement, ask the designated area
owner in [.github/CODEOWNERS](.github/CODEOWNERS), or a maintainer when no area
owner is listed, to decide the disputed point. That decision ends the review
deadlock; it does not make unrelated scope mandatory.

## Final authority

Community reviews make a change ready for maintainer review. A maintainer still
checks that the required reviews bind the current head, material findings are
resolved, and project gates pass before merging. Only maintainers merge.
