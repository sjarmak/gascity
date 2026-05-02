# Project Brief — `<RIG-NAME>`

> Copy this file to `<rig-root>/.gc/project-brief.md` and customize.
> The project-lead reads it at the start of every tick. Keep it short
> (under 1 page) and concrete — vague briefs produce vague rollups.

## Project

**Name**: <project name>
**Repo**: <git URL or path>
**Current focus**: <one line — what the team is actually shipping right now>

## Persona

How should the project-lead sound when writing rollups for THIS project?

- **Voice**: <e.g. "terse and engineering-direct", "context-rich and explanatory", "blameless and outcome-focused">
- **Style notes**: <e.g. "always include the bead id inline", "never use jargon the founder won't recognize", "translate test failures into product impact">
- **What the human cares about most**: <e.g. "shipping the migration this week", "not breaking auth", "keeping CI green on main">

The project-lead writes the **Why** and **Smallest ask** sections of
rollups in this voice. Different projects can have different voices —
that's the point of per-project briefs.

## Escalate (severity:escalate)

Trigger an escalation when ANY of:

- <e.g. "any bead with `epic:migration` blocked for >2h">
- <e.g. "any retry count >3 on the same step (visible in bead metadata)">
- <e.g. "any blocked bead labeled `priority:high`">
- <e.g. "ambiguous spec — coder mailed asking a question you can't answer from the brief">
- <e.g. "infrastructure failure with no automated recovery path">

Each escalation must end in a single concrete ask the human can
answer in under a minute. If you can't phrase it that way, downgrade
to `severity:info`.

## Do Not Escalate (severity:info or skip)

Even if the rule above seems to apply, DON'T escalate when:

- <e.g. "the bead is on the `awaiting-vendor` epic — that gate is known and slow">
- <e.g. "the coder explicitly mailed 'will retry tomorrow' within last 24h">
- <e.g. "the failure is in flaky-test-known-list">
- <e.g. "the work is on the `cleanup` epic — never page about cleanup">

These suppression rules matter as much as the trigger rules. A
project-lead that pages on every blocked bead is noise; a
project-lead that pages on the right ones is leverage.

## Info (severity:info)

Write `severity:info` rollups for the weekly digest when:

- A milestone bead closed (`label:milestone status:closed`)
- A blocker was resolved by the team without escalation
- A new coder joined the rig
- Anything else the human would want in a weekly digest but doesn't
  need to act on now

Info rollups are never delivered — they accumulate for the digest.
