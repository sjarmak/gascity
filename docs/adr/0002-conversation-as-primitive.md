# ADR-0002: Conversation as a First-Class Primitive

**Date**: 2026-05-12
**Status**: proposed — superseded-in-practice, needs revision (2026-07-07 audit)

> **2026-07-07 audit:** the provider-neutral goal was met under a different
> shape (`internal/extmsg/` AdapterRegistry + ConversationRef), not the
> `conversation.Conversation` primitive decided here. Revise to ratify what
> shipped. Root of the 0006/0008 dependency chain.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor

## Context

External conversation contexts (Slack threads, GitHub PR comment chains, email threads, future Signal/Discord/IRC) live as **per-provider ad-hoc metadata** on beads today: `metadata.originating_slack.{channel_id, thread_ts, user_id, asked_at}`. Each provider's pack reimplements its own publish/reply surface (`gc slack publish-to-channel`, `gc mail send`, hypothetical `gc discord post`).

Every cross-cutting feature that needs to reply to a conversation reimplements provider-specific routing:

- **`pl-loop-close`** reads `originating_slack.*` and posts via `gc slack publish-to-channel`
- **`pl-human-gate-surface`** does the same shape — different lifecycle, same plumbing
- **`mail-redirect-to-mayor`** maps mail → slack manually
- **`slack-binding-reaper`** handles binding drift specific to slack-pack's binding model
- Two live regressions traced to slack-pack ad-hoc state management (`dr-s1mcig`): `reply-current` resolves stale inbounds across PL respawn; identity registration doesn't re-fire on session start

Each new conversational feature (`dr-1lf4h3` Phase 2, `dr-xbs45x`, `dr-45le2r`) reimplements the same shape. The "polecat replies to Stephanie in the thread her ask came from" pattern recurs at every loop-close and gate-surface point — but there's no provider-neutral abstraction for "a conversation a bead participates in."

## Decision

Promote Conversation to a first-class SDK primitive. A `conversation.Conversation` declares:

- `provider`: slack, mail, github_pr, discord, etc.
- `account_id` + `conversation_id`: provider-scoped routing keys
- `thread_ref`: nullable provider-specific thread identifier (slack thread_ts, github comment id, etc.)
- `participants[]`: list of `{actor_uri, role}` (e.g. `{slack:U..., human}` + `{gc:gascity-maintenance-pl, agent}`)
- `voice`: which agent identity should sender be when this scope posts (replaces `gc slack identity --as`)

Beads carry a `conversation_ref: <conversation-bead-id>` instead of provider-specific `originating_slack.*`. Conversations are themselves bead-backed (`issue_type: conversation`).

The SDK provides a provider-neutral interface:

```go
type ConversationAdapter interface {
    Publish(ctx, conv, body) error
    React(ctx, conv, emoji) error
    Reply(ctx, conv, parent_ref, body) error   // parent_ref scopes within conversation
    Resolve(ctx, conv) (ConversationState, error)   // adapter-specific state introspection
}
```

Each provider-pack (slack-pack, mail-pack, github-pack) implements the adapter. Cross-cutting handlers (`pl-loop-close`, `pl-human-gate-surface`) call the abstraction; they never name a provider directly.

## Alternatives Considered

### Alt 1: Status quo — per-provider metadata + per-handler routing scaffolding

- **Pros**: shipped and working in v1.5
- **Cons**: every new conversational feature reimplements the same shape; slack-pack regressions like `dr-s1mcig` will recur in every future pack; mayor-side relays (mail-redirect-to-mayor) accumulate
- **Why not**: the recurring cost compounds. Three v2 pillars (#4 human-gate as step type, #7 scoped knowledge, plus the v1.5 handlers) all depend on this abstraction existing

### Alt 2: Conversation as a metadata shape (typed, but not a primitive)

- **Pros**: lighter than a full primitive; just standardize the metadata key (`metadata.conversation = {...}`)
- **Cons**: doesn't get the SDK to a provider-neutral publish API; handlers still branch on `provider`; no participant-tracking surface for v2 features like cross-channel scoped knowledge
- **Why not**: half-measure. Would defer the real abstraction; pillar #7 (scoped knowledge per conversation) needs the participant model

### Alt 3: Use an existing protocol abstraction (Matrix, ActivityPub, MCP-like)

- **Pros**: reuse battle-tested abstractions
- **Cons**: those protocols are heavy; their semantic models include features we don't need (federation, signed events, etc.); adapter cost per provider is higher
- **Why not**: wrong scope; we want a small SDK-internal abstraction, not a wire protocol

## Consequences

### Positive

- One implementation of "post to thread" used by all handlers; bug fixes in slack-pack (e.g. `dr-s1mcig`) don't need to be re-fixed in mail-pack, github-pack, etc.
- Adding a new provider becomes implementing one adapter interface
- Cross-channel scoped memory (ADR-0007) becomes natural — knowledge scoped to a Conversation
- Human-gate as step type (ADR-0005) becomes one step body that takes a `conversation_ref` regardless of provider
- Identity-on-respawn fixed at the adapter level (ADR-0002 contract) instead of per-PL-brief instructions
- Mayor-pattern-miner can analyze conversation health across providers uniformly

### Negative

- Migration: every handler using `metadata.originating_slack.*` must be ported to `metadata.conversation_ref`. ~5 handlers in v1.5
- Migration of historical beads: leave `originating_slack` in place for legacy, add `conversation_ref` going forward
- New SDK surface (Conversation primitive + adapter interface) adds 4-6 new exported types

### Risks

- **Adapter interface too narrow** — providers have quirky surfaces (slack threads vs github review threads vs email reply chains) that don't fit a single Reply() signature. Mitigation: design the interface around the 80% case; expose provider-specific extensions via typed escape hatch on Conversation (`provider_metadata: any`)
- **Conversation bead bloat** — every Slack ask creates a new Conversation bead. High-traffic channels could create thousands. Mitigation: conversation beads are ephemeral (TTL compaction after N days of no participation); only the originating thread_ts and a stable hash are durable
- **Identity-vs-conversation coupling** — the slack-pack identity-on-respawn bug (`dr-s1mcig` Bug B) is partially a session-lifecycle issue, not just an adapter one. Mitigation: ADR-0002 specifies adapters must accept session-id at publish-time and resolve identity from the conversation's voice field; identity registration moves from "once per session" to "once per voice"
