---
name: gc-events-payloads
description: >
  Gas City typed-event runbook. Load when adding a new event type or event
  payload, changing an existing payload struct, debugging
  TestEveryKnownEventTypeHasRegisteredPayload or spec-ci/Dashboard-SPA CI
  failures caused by event schema drift, upgrading a NoPayload event to a
  typed payload, filtering events with `gc events --type/--payload-match`,
  or touching internal/events/, internal/api/event_payloads.go, or the
  worker.operation telemetry (1a) fields. Not for event-bus consumption
  patterns, order gates, or general codegen — see the sibling table inside.
---

# gc-events-payloads — adding and evolving typed events

Tier 1 (single-session, no subagents; survives `DISABLE_INTERACTIVITY=1`).

Gas City's event bus is an append-only JSONL log (`.gc/events.jsonl`) plus an
HTTP/SSE projection. The load-bearing invariant (AGENTS.md, "Typed events"):
**every constant in `events.KnownEventTypes` must have a payload registered
via `events.RegisterPayload`**, enforced in CI by
`TestEveryKnownEventTypeHasRegisteredPayload`
(`internal/api/event_payloads_coverage_test.go:16`). This skill is the
end-to-end runbook for satisfying that invariant when you add or change an
event, and for the regen chain and CI gates downstream of it.

## When NOT to use this skill

| You are doing                                                                    | Use instead                                                                                    |
| -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Debugging controller/reconciler behavior via the event log                       | `gc-debugging` (sibling, departure library) and `engdocs/contributors/reconciler-debugging.md` |
| Orders, gate conditions, molecules, sling semantics                              | `gc-meow-work-model` (sibling)                                                                 |
| The full codegen chain beyond events (config schema, dashboard dist, Huma rules) | `gc-generated-artifacts` (sibling) and `engdocs/contributors/huma-usage.md`                    |
| Running the right test tiers / make targets in general                           | `gc-build-verify` (sibling) and `TESTING.md`                                                   |

Sibling skills are part of the same departure library and may land
separately; the repo docs cited above are always authoritative.

## Vocabulary (defined once)

| Term                   | Meaning                                                                                                                                                                                                    |
| ---------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Event**              | One JSONL record: `Seq, Type, Ts, Actor, Subject, Message, Payload` (`internal/events/events.go:100`).                                                                                                     |
| **Envelope**           | The non-payload fields of an Event. Some events are fully described by their envelope.                                                                                                                     |
| **Payload**            | Per-event-type structured data. Stored as raw `[]byte` on the bus; typed Go struct everywhere else.                                                                                                        |
| **`events.Payload`**   | Sealed marker interface (`IsEventPayload()`) so `map[string]any` can never be a payload (`internal/events/payload.go:23`).                                                                                 |
| **Registry**           | The `event type → sample payload` map populated by `events.RegisterPayload` at package init (`internal/events/payload.go:51`).                                                                             |
| **`events.NoPayload`** | The registered shape for envelope-only events (`internal/events/payload.go:32`).                                                                                                                           |
| **SSE projection**     | The `/v0/events/stream` and `/v0/city/{cityName}/events/stream` endpoints, which decode stored bytes back into typed variants via `events.DecodePayload`.                                                  |
| **Typed wire**         | The OpenAPI discriminated union (`TypedEventStreamEnvelope`) built at spec-generation time from the registry — one `oneOf` variant per known event type (`internal/api/event_envelope_schemas.go:85-106`). |
| **Custom variant**     | The catch-all schema variant for event types NOT in `KnownEventTypes`; its payload is untyped (`internal/api/event_envelope_schemas.go:151`).                                                              |

## How the pieces connect

```
internal/events/events.go        constants + KnownEventTypes
internal/api/event_payloads.go   API-layer payload structs + init() registrations
internal/extmsg/events.go        domain-package registrations (same pattern)
internal/events/payload.go       registry: RegisterPayload / LookupPayload / DecodePayload
internal/api/event_envelope_schemas.go
                                 registry → OpenAPI oneOf (panics on missing registration)
cmd/genspec                      → internal/api/openapi.json,
                                   docs/reference/schema/openapi.{json,txt},
                                   docs/reference/schema/events.{json,txt}
go generate ./internal/api/genclient
                                 → internal/api/genclient/client_gen.go
[out-of-band] @hey-api/openapi-ts
                                 → internal/api/dashboardspa/web/shared/src/generated/
                                   gc-supervisor-client/*.gen.ts (no in-repo regen target;
                                   see gc-generated-artifacts Trap 4)
make dashboard-build             → internal/api/dashboardspa/dist/ (committed, embedded)
```

(Paths per the 2026-06-28 dashboard/schema migration, commit `677ce243f`;
sibling `gc-generated-artifacts` owns the full layout and its Layout note
translates pre-migration paths.)

Registration must run before the api package's tests: put it in an `init()`
of a package that `internal/api` imports (directly or transitively).
`internal/api/event_payloads.go` is the default home; `internal/extmsg/events.go:77-83`
is the pattern for a domain package that owns its own payload types.

Recording is **best-effort**: `Recorder.Record` never returns an error, and
emit helpers log marshal failures to stderr and drop the event (see
`extmsgEmitEvent`, `internal/api/handler_extmsg.go:17-42`). Do not build
correctness on an event having been recorded.

## Runbook: add a new typed event end-to-end

Work through every box. Steps 1-4 are one commit with their tests
(tests ship with the change, not a follow-up).

- [ ] **1. Constant + KnownEventTypes.** Add the constant to the `const`
      block AND the `KnownEventTypes` slice in `internal/events/events.go`.
      Skipping the slice is the silent failure mode — see the trap table.
- [ ] **2. Payload struct.** Define it with json tags, `doc:` tags on every
      field (they become OpenAPI descriptions), and the marker method:

  ```go
  // FooHappenedPayload is emitted on foo.happened.
  type FooHappenedPayload struct {
      FooID  string `json:"foo_id" doc:"Canonical foo bead ID."`
      Reason string `json:"reason,omitempty" doc:"Short human-readable reason."`
  }

  // IsEventPayload marks FooHappenedPayload as an events.Payload variant.
  func (FooHappenedPayload) IsEventPayload() {}
  ```

  If the envelope's `Actor`/`Subject`/`Message` alone carry the semantics,
  register `events.NoPayload{}` instead and skip the struct.

- [ ] **3. Register.** In the owning package's `init()`:
      `events.RegisterPayload(events.FooHappened, FooHappenedPayload{})`.
      Registering the same type twice with a _different_ struct panics at init
      (`internal/events/payload.go:54-59`); same struct is a no-op.
- [ ] **4. Emit.** Marshal the typed struct into `Event.Payload`
      (`json.RawMessage`). For payloads emitted from several `cmd/gc` sites,
      add a `...JSON` helper next to the struct — the existing pattern is
      `api.SessionLifecyclePayloadJSON` (`internal/api/event_payloads.go:222`).
- [ ] **5. Tests.** Round-trip through `events.DecodePayload` (pattern:
      `internal/api/event_payloads_test.go`), plus emission-site tests if you
      touched `cmd/gc` (pattern: `cmd/gc/session_lifecycle_payload_test.go`).
      Run the gate:

  ```bash
  go test ./internal/api/ -run 'TestEveryKnownEventTypeHasRegisteredPayload' -count=1
  ```

- [ ] **6. Regenerate spec + client.** The pre-commit hook
      (`.githooks/pre-commit`) does this automatically for any staged `*.go`
      file: `go run ./cmd/genspec`, `go generate ./internal/api/genclient`,
      `go run ./cmd/genschema`, staging the outputs. Manual equivalent when the
      hook is not active: run those three commands and commit
      `internal/api/openapi.json`, `docs/reference/schema/openapi.{json,txt}`,
      `docs/reference/schema/events.{json,txt}`,
      `internal/api/genclient/client_gen.go`, and any
      `docs/reference/{config,cli}.md` churn. CI gate: `make spec-ci`
      (regenerates, fails on diff).
- [ ] **7. Regenerate the dashboard.** Any change to
      `internal/api/openapi.json` can change the TS union the dashboard
      consumes. Two pieces, different mechanics (details owned by sibling
      `gc-generated-artifacts`):

  ```bash
  make dashboard-check   # npm ci + workspace build + typecheck + go test dashboardspa/... dashboardbff/...
  ```

  Commit `internal/api/dashboardspa/dist/` (CI gate: `make dashboard-ci`).
  The TS supervisor client
  (`internal/api/dashboardspa/web/shared/src/generated/gc-supervisor-client/`)
  has **no in-repo regen target** — regenerate it deliberately with
  `@hey-api/openapi-ts` when your payload shape is one the dashboard
  consumes, and say so in the commit (gc-generated-artifacts Trap 4; no
  gate catches drift there). The pre-commit hook rebuilds dist/ only if
  `npm` is on PATH; without Node it prints a warning and **CI's
  dashboard-ci gate fails instead** — do not push assuming the hook
  covered you.

- [ ] **8. Docs.** `docs/reference/events.md` documents the `gc events` CLI
      output contract; update it only if that contract changed, then
      `make check-docs`.

## Trap table

| Mistake                                                                    | What happens                                                                                                                              | Caught by                                                                                                                           |
| -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Constant added, `KnownEventTypes` not updated                              | Event ships on the wire as the untyped **Custom** variant. `DecodePayload` returns `(nil, false, nil)` and raw bytes pass through opaque. | **Nothing fails.** Silent schema hole — only review catches it.                                                                     |
| In `KnownEventTypes`, no `RegisterPayload`                                 | Unit test fails; spec generation panics (`event_envelope_schemas.go:91`).                                                                 | `TestEveryKnownEventTypeHasRegisteredPayload`, `make spec-ci`                                                                       |
| Re-register with a different struct                                        | `panic` at package init.                                                                                                                  | Any test in the importing package                                                                                                   |
| Registration in a package `internal/api` doesn't import                    | Coverage test can't see it; fails as "unregistered".                                                                                      | Same test                                                                                                                           |
| Spec regenerated, embedded dashboard `dist/` not rebuilt                   | `dashboard-ci` (rebuild + `git diff` on `internal/api/dashboardspa/dist`) fails; drags CI/preflight and CI/required red with it.          | `make dashboard-ci`                                                                                                                 |
| Spec regenerated, dashboard TS supervisor client not                       | **Nothing fails** unless SPA code references the changed shape — the TS client has no in-repo regen target or drift gate.                 | Only review; see `gc-generated-artifacts` Trap 4                                                                                    |
| Payload field renamed without decode compat                                | Old JSONL lines in existing cities fail to decode at the SSE projection.                                                                  | Nothing automatic — see `BeadEventPayload.UnmarshalJSON` (`internal/api/event_payloads.go:132`) for the legacy-shape compat pattern |
| Documenting a field "always present" that an emission site can leave empty | Downstream consumers join on garbage.                                                                                                     | Only emission-site tests — see worked example, commit `301536739`                                                                   |

## Worked example: SessionLifecyclePayload (May 2026, three commits on main)

The most recent NoPayload→typed upgrade, and a compressed tour of every step
and two of the traps above.

1. **`5a6c5f33f`** `feat(events): typed SessionLifecyclePayload for
session.stopped/crashed` — replaced the `events.NoPayload` registrations
   for `SessionStopped`/`SessionCrashed` with a typed struct
   (`SessionID` always present, optional `Template`, `Reason`), added the
   `api.SessionLifecyclePayloadJSON` helper, updated **eight emission
   sites** across `cmd_session.go`, `cmd_handoff.go`, `controller.go`,
   `session_lifecycle_parallel.go`, `session_reconciler.go`, and
   regenerated `internal/api/openapi.json`, `docs/schema/openapi.{json,txt}`,
   `internal/api/genclient/client_gen.go` in the same commit, with
   round-trip + registry tests and per-site emission tests. 13 files.
2. **`58e0b8dbb`** `fix(dashboard): regenerate TS schema` — step 7 was
   missed: `cmd/gc/dashboard/web/src/generated/` wasn't regenerated, and
   three CI rollups (CI/preflight, CI/required, Dashboard SPA) went red
   from that one root cause. Fix was mechanical `npm run gen` output.
3. **`301536739`** `fix(events): fall back to session_name when
SessionLifecyclePayload sessionID is empty` — the doc contract said
   `SessionID` is "Always present", but three emission sites could emit
   `session_id:""` (targets built without a store, or whose session bead
   was already retired). Fixed with a `lifecycleCorrelationID()` fallback
   to the always-populated session name, plus tests per site.

(The dashboard/schema paths in those three commits are pre-migration:
`docs/schema/...` and `cmd/gc/dashboard/web/...` moved on 2026-06-28,
commit `677ce243f`. The runbook above uses the current paths; translate
when reading the old diffs.)

Lessons, in order: land struct + registration + emission + regen + tests as
one commit; the dashboard regen is the step everyone forgets; and audit
every emission site before writing "always present" in a `doc:` tag.

## Filtering events (consumer quick reference)

```bash
gc events --type=bead.closed --since 1h          # exact type match — NO wildcards
gc events --type=bead.updated --payload-match bead.status=in_progress
gc events --watch --timeout 30s --type=convoy.closed   # exits on first match
gc events --follow                                # continuous stream
```

- `--type` is an **exact string match** (`internal/events/reader.go:30`);
  `--type=bead.*` matches nothing.
- `--payload-match key=value` supports **dotted paths** for nested payloads
  (`bead.issue_type=task`) since commit `f2cc8e54f` (#1615). A flat key
  matches only top-level and does **not** traverse into nested objects;
  repeating the same key means OR. Most payloads nest their fields
  (`{"bead":{...}}`), so the dotted form is usually the one you want.

## The worker.operation telemetry (1a) fields — open work

Snapshot dated **2026-07-06** (state at PR #1272 merge; re-verify before
relying on it). `WorkerOperationEventPayload`
(`internal/api/event_payloads.go:265`) carries per-invocation cost/latency
fields (`Model`, `PromptVersion`, `PromptSHA`, `BeadID`, token counts,
`LatencyMs`, `CostUSDEstimate`). Contract and status:

- Every 1a field is **best-effort and `omitempty`**. Absence means "not
  measured", never "zero cost" / "no model". Aggregating consumers MUST
  presence-check (e.g. `Model != ""`) at their input boundary.
- As of the snapshot only `AgentName` is wired; the rest are documented
  TODOs (sessionlog token extraction, promptmeta propagation, pricing
  seam #1255). The per-field "Wired:" comments in the struct are pinned by
  `TestWorkerOperationPayload1aWiringStatusPin`
  (`internal/api/event_payloads_1a_wiring_test.go:30`) — if you wire a
  field, update the comment or that test fails.
- `internal/worker/operation_events.go` holds a **mirror struct**
  (`operationEventPayload`) with the same JSON shape; keep the two in sync.
  `TestWorkerOperationEventPayloadMatchesWorkerJSONShape`
  (`internal/api/event_payloads_1a_test.go:105`) enforces the shape match.

## Change control

**Provisional** (maintainer answer pending, 2026-07-06): an event payload
shape is a cross-subsystem contract — CLI, API clients, dashboard, and
external consumers all decode it. Adding or changing one should be routed
to maintainer review rather than auto-merged, per the provisional
human-review rule derived from reverts `b8120d697` and `19e34ab71`. This
skill does not authorize skipping that review.

## Provenance and maintenance

Authored 2026-07-06 from the working tree at commit `58e0b8dbb`
(branch `_pr1945_check`) and the fable-distillation discovery report; the
regen-chain paths (steps 6-7, chain diagram, trap table) were re-verified
against `origin/main` at `f828bbe4b` after the 2026-06-28 dashboard/schema
migration made the original paths stale. The
"Change control" paragraph and sibling-skill names are provisional pending
maintainer answers. Re-verify volatile claims (against `origin/main`, not
a possibly-stale worktree):

| Claim                                   | Re-verify with                                                                                                                 |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Enforcement test name/location          | `grep -rn "func TestEveryKnownEventTypeHasRegisteredPayload" internal/api/`                                                    |
| Constant + KnownEventTypes layout       | `grep -n "KnownEventTypes" internal/events/events.go`                                                                          |
| Registration sites outside internal/api | `grep -rn "events.RegisterPayload" --include="*.go" internal/ cmd/ \| grep -v internal/api/event_payloads.go \| grep -v _test` |
| Spec/dashboard regen artifact list      | `grep -n "spec-ci:\|dashboard-ci:\|dashboard-check:" -A 8 Makefile`                                                            |
| Pre-commit regen behavior               | `git show HEAD:.githooks/pre-commit`                                                                                           |
| 1a wiring status                        | `go test ./internal/api/ -run 'TestWorkerOperationPayload1aWiringStatusPin' -count=1`                                          |
| Dotted-path filter semantics            | `go test ./cmd/gc/ -run 'TestMatchPayload' -count=1`                                                                           |
| The whole invariant still holds         | `go test ./internal/api/ -run 'TestEveryKnownEventTypeHasRegisteredPayload' -count=1`                                          |
