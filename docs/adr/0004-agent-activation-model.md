# ADR-0004: Agent Activation Model — Subscriptions and Standing Intent

**Date**: 2026-05-12
**Status**: retired (2026-07-07 audit)

> **2026-07-07 audit:** no Subscription/Goal/EventPattern types or
> `gc subscription`/`gc goal` ever built; every wake mechanism it targeted
> still runs as a cron order. Retired; re-propose narrowly (event
> subscriptions only) if a need recurs.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor

## Context

Gas City's agent activation model today is **reactive and fragmented**. Agents (mayor, PLs, polecats, workers) sit idle until something explicitly wakes them. The waking mechanisms are spread across five disconnected systems:

- `ScheduleWakeup` — mayor self-paces by scheduling next-wake, but no other agent uses it
- `gc order` (cron/event/cooldown) — scheduled scripts run **as scripts**, not as agent reasoning contexts; they can't ask the LLM to think, only execute bash
- `gc session nudge` — manual or programmatic prompt injection, requires a caller
- Mail injection at session start — only happens at fresh session boot, mid-session mail doesn't wake the agent
- Slack inbound → bound session wake — slack-pack-specific, doesn't generalize to other providers or to bead-state changes

Concrete pain points from v1.5 operation:

- **Mayor needs constant nudging.** Mail-redirect-to-mayor exists because mail to "human" is dead-letter; mayor only checks `gc mail count` on user-driven wakeup. Stephanie has to remember mayor doesn't auto-poll
- **Polecat pool sat idle while routed work waited.** `gc-bc2c` was routed to `/home/ds/gascity/polecat` for hours; the polecat sessions were active but in old worktrees, not subscribing to "new work routed to my pool"
- **PLs don't auto-react to blocker close.** `wake-mayor-on-blocker-close` is a gc order that nudges mayor — but a goal-directed system would have mayor STANDING SUBSCRIBED to "blocker beads closing for any work I dispatched"
- **Slack pings miss.** PL respawn left slack binding stale; Stephanie's ping hit a closed session because slack-pack's "wake bound session" semantics don't reflect session lifecycle changes (also tracked in `dr-s1mcig` / `dr-9y620w`)
- **Multi-step pipelines need mayor in the middle.** Per the formula-vs-skill discussion: triage → pick → pr-start → ship → open-PR currently needs a fresh Slack message from Stephanie at each transition because no agent has a STANDING goal of "drive this PR pipeline to completion"

## Decision

Promote agent activation to a first-class SDK primitive. Two new types:

```go
type Subscription struct {
    AgentRef     string                 // who wakes
    EventPattern EventPattern           // what triggers
    Action       SubscriptionAction     // what happens on trigger
    Owner        string                 // which agent/bead owns this subscription
    Lifetime     SubscriptionLifetime   // session-bound | persistent | bead-scoped
}

type Goal struct {
    AgentRef     string
    Intent       string                 // free-text: "keep pool capacity > 50%", "drive PR pipeline to completion"
    Triggers     []Subscription         // what causes the agent to re-evaluate the goal
    Constraints  []Constraint           // budget caps, cooldowns, halt conditions
    Status       GoalStatus             // active | suspended | satisfied | failed
}
```

`EventPattern` matches:

- Bead transitions: `{type: bead.updated, filter: {status: closed, metadata.originating_pl_agent: "$self"}}` — wake mayor when work it dispatched closes
- Conversation events (via ADR-0002): `{type: conversation.message, conversation_ref: "$bound"}` — wake PL when its bound channel gets a message
- Time: `{type: cron, schedule: "*/15 * * * *"}` — wake on schedule
- Pool state: `{type: pool.claim_ready, pool: "$self"}` — wake workers when work is routed to their pool
- Custom: `{type: custom, payload: {...}}` — extensibility hatch for packs

`SubscriptionAction` is one of:

- `nudge` — inject a prompt into the agent's session (today's session nudge semantics)
- `wake` — boot a fresh session if asleep; nudge if active
- `exec` — run a script (today's gc order semantics, reframed as a subscription)
- `goal_advance` — trigger goal re-evaluation in the agent's reasoning context

`gc subscription create` registers a subscription. `gc goal start` declares a standing intent.

## Alternatives Considered

### Alt 1: Status quo — five separate wake mechanisms glued via mayor-relay scripts

- **Pros**: shipped; works today
- **Cons**: every cross-cutting feature reimplements the wake plumbing; mayor is the relay for everything that doesn't fit `gc order`; agents have no introspectable model of "what wakes me"
- **Why not**: the recurring cost compounds. Each v1.5 feature added 1-2 new handlers. v2 needs unification

### Alt 2: Just expose `gc subscription` without `Goal` — keep activation reactive only

- **Pros**: lighter primitive; one new type
- **Cons**: doesn't address the multi-step pipeline gap (triage → ship → open-PR needs standing intent, not just event reaction)
- **Why not**: half-measure. Same shape would force every cross-step transition to re-fire via manual subscription

### Alt 3: Use an event bus library (NATS, Redis streams, Watermill)

- **Pros**: battle-tested; off-the-shelf
- **Cons**: heavy infra dependency; doesn't model the agent-reasoning loop (event → wake → THINK → act); abstracts at wrong level
- **Why not**: wrong layer. The hard part isn't moving events around — it's tying them to LLM reasoning sessions and goal-driven autonomy. Off-the-shelf event buses don't help with that

### Alt 4: Build it as a city-side pattern, not SDK

- **Pros**: lower SDK surface; can prototype faster
- **Cons**: every city would reimplement; oversight-rig pack already shows the limits of city-side activation glue (patrols + rollups are an early attempt)
- **Why not**: city-side leaves the foundational primitive un-typed and per-deployment. SDK is the right home

## Consequences

### Positive

- Mayor can declare standing goals (e.g. "monitor pool capacity", "drive dispatched work to completion") and re-evaluate them on subscribed events — no more user nudging for routine state checks
- Worker pools can subscribe to "work routed to my pool" — gc-bc2c-style "routed but unclaimed" stalls disappear
- PLs can subscribe to "bead I dispatched transitioned" — loop-close becomes one subscription instead of two handlers (event-driven + cooldown re-tick)
- Multi-step pipelines run via Goal — `mol-pr-from-issue` becomes "Goal: drive PR for issue N to merged" with subscriptions at each gate
- Five gc orders retire (mail-redirect-to-mayor, wake-mayor-on-blocker-close, pl-loop-close, pl-loop-close-timeout, pl-human-gate-surface-recheck) — replaced by subscriptions
- Cross-provider activation: a Conversation primitive subscription (slack OR github OR mail) wakes the right agent regardless of provider

### Negative

- Migration: every existing `gc order` is a candidate to refactor as a subscription; some stay as scripts (cron-based reapers), some become subscriptions (event-driven handlers)
- SDK surface grows substantially (Subscription + Goal + EventPattern + SubscriptionAction + GoalStatus + lifecycle hooks). Estimated 6-10 new exported types
- Runtime cost: subscription evaluation must be fast (subscriptions evaluated on every bead update, every conversation message) — needs an efficient index

### Risks

- **Subscription storm** — N subscriptions × M events per minute = N×M evaluations. Mitigation: bead update events carry a pre-built dispatch index (which subscriptions match which beads) maintained by the SDK; only matching subscriptions evaluate
- **Goal evaluation cost** — standing goals re-evaluate on every subscribed trigger. If mayor has 5 goals and 20 triggers fire per minute, that's 100 evaluations/min. Mitigation: subscriptions filter at the SDK layer; only goal-mutating events reach the agent's reasoning context. Bound mayor's `goal_advance` budget at N/min
- **Activation loops** — agent acts on goal → action triggers event → event re-fires subscription → goal re-advances → ... Mitigation: subscription lifecycle includes a cooldown; goal status tracks "last advanced at" to avoid tight loops
- **Multi-agent contention** — two PLs subscribed to the same channel; whose action fires? Mitigation: subscriptions carry priority + claim semantics (first-claim-wins per event id)
