---
name: gc-runtime-providers
description: >
  Gas City runtime-provider seam runbook. Load when working under
  internal/runtime/ (tmux, subprocess, exec, acp, k8s, fake, or the
  auto/hybrid routing layers), when adding or modifying a runtime.Provider
  implementation, when wiring provider selection in cmd/gc/providers.go,
  when a session hangs on a TUI dialog or a nudge is "delivered" but never
  submitted, when ProviderCapabilities or sleep/wake gating behaves
  unexpectedly, or when running the runtimetest conformance suite.
---

# gc-runtime-providers

Skill tier: Tier 1 (single-session knowledge skill; no subagents, no
worktrees; safe under `DISABLE_INTERACTIVITY=1`).

The **runtime provider seam** is `runtime.Provider` in
`internal/runtime/runtime.go`: the one interface through which Gas City
starts, stops, observes, and talks to agent sessions, regardless of
whether the session lives in a tmux pane, a child process, a Kubernetes
pod, or a test fake. This skill teaches the contract, the roster of
implementations, how selection and routing work, how capabilities gate
reconciler decisions, how to add a provider without breaking the
conformance contract, and the tmux TUI-driving hazards that produced the
subsystem's costliest fixes.

**Jargon, defined once:**

| Term              | Meaning                                                                                                           |
| ----------------- | ----------------------------------------------------------------------------------------------------------------- |
| session           | A named, running agent process managed by a provider (tmux session, subprocess, pod).                             |
| provider          | An implementation of `runtime.Provider`; owns session lifecycle and I/O for one transport.                        |
| nudge             | Structured content sent into a running session to wake or redirect the agent (`Provider.Nudge`).                  |
| routing provider  | A composite provider (`auto`, `hybrid`) that delegates each call to one of two backends per session name.         |
| capability        | A `ProviderCapabilities` flag declaring what a provider can _reliably observe_ (attachment, activity).            |
| conformance suite | `internal/runtime/runtimetest/conformance.go`: one shared test suite every provider implementation must pass.     |
| wake reason       | A reconciler-side justification for keeping or waking a session (attached, recent activity, pending interaction). |
| TUI driving       | Sending keystrokes into an agent's terminal UI (Enter, Down, Escape) to submit input or dismiss dialogs.          |

## When NOT to use this skill

| Your task                                                                                      | Use instead                                                                                    |
| ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Reconciler tick anatomy, poke-channel debounce, desired-state computation, drain/orphan sweeps | sibling skill `gc-reconciler-lifecycle`                                                        |
| Which test tier to write, fakes-vs-mocks doctrine, testscript setup                            | sibling skill `gc-test-authoring`, and `TESTING.md`                                            |
| Make targets, build tags, local-vs-CI gate deltas                                              | sibling skill `gc-build-verify`                                                                |
| Agent config fields, TOML compose/patch/override                                               | sibling skill `gc-config-system`                                                               |
| Session naming, lifecycle projection, session beads (the layer _above_ the provider seam)      | `engdocs/architecture/session.md` (the doc owns those facts; this skill does not restate them) |

## 1. The contract (what every provider must honor)

`runtime.Provider` (`internal/runtime/runtime.go`) is transport only.
Per the ZFC rule in `AGENTS.md`, no provider method makes a judgment
call; providers move bytes and report observable state. "Is this agent
stuck?" is never answered here.

Contract rules, all enforced or documented in `runtime.go` and checked
by the conformance suite:

- [ ] **Concurrency-safe.** Start/Stop/Interrupt/IsRunning/ProcessAlive/
      ListRunning may be called from multiple goroutines for distinct
      names. Duplicate `Start` for the same name must consistently
      return an error (`ErrSessionExists` semantics).
- [ ] **`Stop` is idempotent.** Returns nil when the session does not
      exist, and nil on a second Stop.
- [ ] **Best-effort methods never error on a missing session.**
      `Interrupt`, `Nudge`, `SendKeys`, `CopyTo` return nil for unknown
      names. `Attach` and `Send`-class failures are fatal; wrap internal
      not-found conditions in `ErrSessionNotFound` so callers can use
      `errors.Is`. `runtime.IsSessionGone(err)` is the one place that
      also recognizes legacy string phrasings; use it, do not re-derive.
- [ ] **`ProcessAlive(name, nil)` returns true.** An empty process-name
      list means "no check possible", contract-mandated true.
- [ ] **Startup errors are typed.** `ErrSessionInitializing` means back
      off and retry (e.g. pod running, tmux not up yet);
      `ErrSessionDiedDuringStartup` means the process exited before
      startup completed.
- [ ] **`Start` respects its context.** Providers check `ctx.Err()`
      between startup steps.
- [ ] **Capabilities are honest.** Report only what the provider can
      _reliably_ detect (section 4). The zero value of
      `ProviderCapabilities` is the safe default.
- [ ] **Metadata is provider-owned state keyed by session name.**
      `GetMeta` on an unset key returns `("", nil)`, not an error.

Multi-backend helpers live in `internal/runtime/provider_core.go`:
`MergeBackendListResults` (partial-failure `ListRunning` with
`PartialListError`) and `MergeBackendStopErrors` (any success wins;
all-gone stays idempotent-nil). Routing providers use these instead of
hand-rolling aggregation.

## 2. Provider roster (verified 2026-07-06)

| Name            | Package                                    | Selected by                          | Capabilities (attach / activity) | Sleep capability   |
| --------------- | ------------------------------------------ | ------------------------------------ | -------------------------------- | ------------------ |
| tmux (default)  | `internal/runtime/tmux/`                   | any unrecognized name, incl. empty   | yes / yes                        | full               |
| subprocess      | `internal/runtime/subprocess/`             | `"subprocess"`                       | no / no                          | timed_only         |
| exec            | `internal/runtime/exec/`                   | `"exec:<script>"`                    | no / no                          | timed_only         |
| acp             | `internal/runtime/acp/`                    | `"acp"`                              | no / no                          | timed_only         |
| k8s             | `internal/runtime/k8s/`                    | `"k8s"`                              | no / yes                         | timed_only         |
| fake            | `internal/runtime/fake.go`                 | `"fake"`                             | yes / yes (configurable)         | configurable       |
| fail fake       | `internal/runtime/fake.go` (`NewFailFake`) | `"fail"`                             | n/a (all ops error)              | n/a                |
| auto (router)   | `internal/runtime/auto/`                   | wrapper, not user-selectable by name | AND of both backends             | per routed backend |
| hybrid (router) | `internal/runtime/hybrid/`                 | `"hybrid"`                           | AND of both backends             | per routed backend |

Notes:

- The **exec** provider delegates every operation to a user-supplied
  script (`internal/runtime/exec/exec.go`), Git-credential-helper style:
  operation name as first argument; exit 0 = success, 1 = error (stderr
  carries the message), 2 = unknown operation, treated as success for
  forward compatibility. This is the extension point for transports the
  SDK does not ship.
- **auto** routes per session name: sessions registered via
  `Provider.RouteACP(name)` _before_ `Start` go to the ACP backend,
  everything else to the default backend. `Unroute` on Stop prevents
  route-map leaks.
- **hybrid** routes on a caller-supplied `isRemote(name)` predicate
  (local vs remote backend).
- **Main-only additions (2026-07-06, `origin/main` at `f828bbe4b`).** The
  registry there also registers `"t3bridge"` (`internal/runtime/t3bridge/`,
  its own turn transport), `"herdr"` (`internal/runtime/herdr/`, opt-in
  multiplexer backend; design doc `internal/runtime/herdr-provider-design.md`),
  the `"ssh:<...>"` prefix (`internal/runtime/ssh/`), and pack-declared
  runtimes resolved to exec proxies (RUNTIME-SEL-011). The table above is
  the stable builtin core; re-run the §re-verify greps against
  `cmd/gc/runtime_registry.go` for the live roster before adding a name.

## 3. Selection and routing (who decides which provider runs)

Resolution order for the provider name (verified in
`cmd/gc/city_runtime.go` and `cmd/gc/providers.go`):

1. `GC_SESSION` environment variable, when non-empty. This is how test
   tiers force fakes (`GC_SESSION=fake` is the testscript default; see
   `TESTING.md`).
2. `[session] provider = "..."` in `city.toml`
   (`config.SessionConfig.Provider`, `internal/config/config.go`).
3. Default: tmux, with the socket name defaulting to the city name when
   `[session] socket` is unset (`tmuxConfigFromSession`,
   `cmd/gc/providers.go`).

The factory is `newSessionProviderForCityByName` in `cmd/gc/providers.go`,
reached through the `buildSessionProviderByName` seam variable
(`providers.go:77`); it maps the selection name to a `runtime.WorkerSpec`
and resolves it via `resolveWorkerSpec` against the builtin+pack runtime
registry (`cmd/gc/runtime_registry.go`). Two wrinkles worth knowing before
you touch it:

- **Per-agent ACP transport.** An agent may set `session = "acp"`
  (`config` agent field, enum `acp|tmux`). When the _city_ provider is
  not `"acp"` but some agent wants it, `newSessionProviderFromContext`
  wraps the city provider and an ACP provider in an `auto.Provider` and
  registers the ACP sessions on it. So the provider you get back in
  `cmd/gc` code is often a router, not a concrete backend: never type-
  assert to a concrete provider in caller code, assert to the _optional
  interface_ you need (section 5).
- **Hot reload swaps providers.** The controller detects a provider-name
  change on config reload and constructs the new provider while sessions
  from the old one may still be running (`cmd/gc/city_runtime.go`,
  provider-change branch). A construction failure keeps the old provider
  and logs a warning. Any state your provider holds must therefore be
  reconstructible from the live system (process table, tmux server,
  cluster), never from in-memory maps alone. This is the no-status-files
  doctrine from `AGENTS.md` applied to the seam.

## 4. Capabilities gate wake and sleep decisions

`ProviderCapabilities` (`internal/runtime/probe.go`) has exactly two
flags: `CanReportAttachment` (IsAttached is meaningful) and
`CanReportActivity` (GetLastActivity is meaningful). The reconciler uses
them to _skip wake reasons it cannot trust_:

- `resolveSleepCapability` (`cmd/gc/session_sleep.go`) maps capabilities
  to a sleep tier: both flags → `full`, activity only → `timed_only`,
  neither → `disabled`. A provider may override per routed session via
  the optional `SleepCapabilityProvider`.
- `namedSessionActiveUseReason` (`cmd/gc/session_reconciler.go`) treats
  a session on a provider that cannot report activity as
  `activity_unknown` and **defers config-drift restarts** rather than
  killing a possibly-working headless agent.

Consequences when writing a provider:

- **Under-claiming is safe, over-claiming is not.** Returning the zero
  value means "never auto-sleep my sessions and never trust my idle
  signals", which degrades to conservative behavior. Claiming
  `CanReportActivity` while returning junk from `GetLastActivity` makes
  the reconciler idle-kill live sessions.
- Routing providers AND their backends' capabilities (see
  `auto.Provider.Capabilities`, `hybrid.Provider.Capabilities`), so a
  mixed fleet degrades to the weaker backend. If per-session precision
  matters, implement `SleepCapabilityProvider` on the router.

## 5. Extending the seam: optional interfaces, not interface growth

The core `Provider` interface is deliberately closed. Every capability
that not all providers share is a small optional interface in
`internal/runtime/` that callers reach with a type assertion:

| Optional interface              | Purpose                                                               | Known implementers (2026-07-06)        |
| ------------------------------- | --------------------------------------------------------------------- | -------------------------------------- |
| `InteractionProvider`           | structured pending/respond interactions                               | acp, routers                           |
| `IdleWaitProvider`              | wait for a safe input boundary, hard timeout bound                    | tmux                                   |
| `DialogProvider`                | dismiss known startup dialogs on a running session                    | tmux                                   |
| `TransportCapabilityProvider`   | fail fast when a transport cannot be routed                           | auto                                   |
| `ImmediateNudgeProvider`        | inject input skipping the wait-idle heuristic                         | tmux                                   |
| `InterruptedTurnResetProvider`  | drop a just-interrupted turn (Gemini Ctrl-C semantics)                | routers, backends per CLI              |
| `InterruptBoundaryWaitProvider` | wait for Codex's durable `<turn_aborted>` marker before the next turn | routers, backends per CLI              |
| `SleepCapabilityProvider`       | per-session sleep tier                                                | tmux, subprocess, exec, acp, k8s, fake |
| `DeadRuntimeSessionChecker`     | positive proof an artifact is dead, for destructive cleanup           | routers, backends                      |

Follow the same pattern for new cross-provider needs: define the
optional interface next to `Provider`, implement it where the transport
genuinely supports it, and make every caller handle the assertion
failing (skip, or return `ErrInteractionUnsupported`-style sentinels).
Adding a method to `Provider` itself forces all nine implementations
plus every test double to change and is almost never the right move.

Note how provider-specific CLI quirks are encoded: the interface doc
comment names the concrete behavior (Gemini's combined-turn bug, Codex's
`<turn_aborted>` marker) so the mechanism stays mechanical and the
reasoning stays out of Go, per ZFC.

## 6. Adding a new provider: checklist

- [ ] **Check the exec provider first.** If your transport can be driven
      by a script, `session.provider = "exec:/path/to/script"` needs no
      Go at all. Write a new Go provider only when you need streaming,
      capabilities, or optional interfaces the script protocol cannot
      express.
- [ ] Implement `runtime.Provider` in a new subpackage
      `internal/runtime/<name>/`, honoring every rule in section 1.
- [ ] Return honest `Capabilities()` (zero value until proven).
- [ ] Wire the conformance suite in a `conformance_test.go` calling
      `runtimetest.RunProviderTests` with a factory. Slow transports may
      split into `RunLifecycleTests` + one shared session passed to
      `RunSessionTests` (that is exactly what
      `test/integration/session_k8s_test.go` does).
- [ ] Gate it correctly: in-memory doubles run untagged (fake, and exec
      against a mock script); anything touching real infrastructure gets
      `//go:build integration` (tmux, subprocess, acp).
- [ ] Register the selection name in `buildRuntimeRegistry`
      (`cmd/gc/runtime_registry.go`) and update the selection-contract doc
      comment on `newSessionProviderForCityByName` in `cmd/gc/providers.go`,
      plus the `SessionConfig.Provider` doc string in
      `internal/config/config.go`.
- [ ] Unit tests next to the code, hand-written fakes only (repo test
      doctrine; details owned by `TESTING.md` and sibling skill
      `gc-test-authoring`).
- [ ] **Route the PR to a human.** A new provider protocol is a
      cross-subsystem contract. _(Provisional, 2026-07-06: derived from
      the maintainer's revert history, e.g. commit `b8120d697` reverted
      solely because an automated pipeline landed a new core-platform
      contract without maintainer review. Pending the maintainer's own
      wording of the routing rule; see sibling skill
      `gc-change-workflow`.)_ Do not route around change-control.

## 7. Running the conformance suites (copy-paste)

All test invocations go through make so `TEST_ENV` scrubbing applies
(shell exports are invisible to `make test`; see sibling skill
`gc-build-verify`). Direct `go test` equivalents, verified against the
Makefile and test files on 2026-07-06:

```bash
# Fast, no infrastructure needed:
go test ./internal/runtime/ -run TestFakeConformance -count=1
go test ./internal/runtime/exec/ -run TestExecConformance -count=1   # mock script

# Real infrastructure (integration tag):
go test -tags integration ./internal/runtime/subprocess/ -run TestSubprocessConformance -count=1
go test -tags integration ./internal/runtime/acp/ -run TestACPConformance -count=1        # builds testdata/fakeacp
go test -tags integration ./internal/runtime/tmux/ -run TestTmuxConformance -count=1      # real tmux

# Kubernetes (needs a reachable cluster):
make test-k8s   # = go test -tags integration ./test/integration/ -run TestK8sSessionConformance -v -count=1

# Everything integration-tagged (30m budget):
make test-integration
```

Tmux safety (constitution-level, `AGENTS.md`): never run bare
`tmux kill-server`; never touch the default tmux server. Tests isolate
via the `gctest-<8hex>` prefix and `test/tmuxtest` Guard
(`tmuxtest.NewGuard(t)`), which registers cleanup for its own sessions
only. If you must clean up manually, target the known socket explicitly:
`tmux -L <socket> kill-session -t <name>`.

## 8. tmux TUI-driving hazards (the subsystem's fix history, distilled)

The tmux provider drives real terminal UIs (Claude, Codex, Gemini,
OpenCode). Keystrokes are fire-and-forget at the tmux layer: tmux
accepting `send-keys` proves nothing about the _agent_ receiving or
acting on it. Every hard bug in this package is some form of that gap.

Standing hazards, each encoded in `internal/runtime/tmux/tmux.go`:

| Hazard                                             | Idiom that guards it                                                                                                                                                           |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Concurrent nudges interleave pasted text           | per-session nudge lock with timeout (`acquireNudgeLock`)                                                                                                                       |
| Submit Enter races the still-completing paste      | fixed 500ms paste debounce before Enter (tested, required)                                                                                                                     |
| Escape is a semantic key in some TUIs' busy states | `shouldSendEscapeBeforeEnter` sends Escape only where it is an insert-mode escape                                                                                              |
| Detached panes drop the submit key                 | `WakePaneIfDetached` (SIGWINCH) before _and_ after Enter                                                                                                                       |
| Injecting text mid-tool-call corrupts the turn     | adapter `Nudge` waits via `WaitForIdle` (bounded) before sending; `NudgeNow` exists for callers that must skip it                                                              |
| Startup dialogs block automation                   | shared recognizers + dismissers in `internal/runtime/dialog.go` (`AcceptStartupDialogs`: resume selector, update dialog, workspace trust, bypass permissions, API-key confirm) |

### Worked example: two real fixes, one week apart (July 2026)

**(a) Blind Enter: commit `d82074594`, "confirm nudge submit landed
instead of sending Enter blind" (#3910).** Symptom: on busy or detached
Claude sessions the submit Enter was lost; the nudge sat _drafted in the
input box, never submitted_, while gc reported delivery success, and the
fleet stalled until something re-kicked the session. Root cause: the
Enter send only retried on tmux-layer errors; tmux accepting the
keystroke was treated as success. The fix, live on main as
`submitEnterAndConfirm` (`internal/runtime/tmux/tmux.go`), confirms the
_agent_ went busy after Enter (Claude's spinner / "esc to interrupt",
`paneBusy`) and re-sends Enter only while the pane is still idle, so an
already-submitted turn can never receive a second Enter
(double-submission is structurally impossible, not just unlikely).
Budgets: up to 3 sends, 4 confirm polls of 150ms each, 200ms re-send
backoff. Two idioms to copy:

1. **Scope reliability upgrades to where the signal is reliable.**
   `submitVerifyEligible` gates confirmation to the Claude provider
   family, whose busy indicator is dependable; other providers keep the
   historical best-effort single delivery so the fix cannot regress them.
2. **Inject side effects.** `submitEnterAndConfirm(sendEnter, wake,
busy, sleep)` takes all effects as functions, so the retry/confirm
   decision logic is unit-tested without a live tmux server
   (`nudge_submit_confirm_test.go`), with a separate integration test
   proving it on real tmux.

**(b) Surprise modal: commit `1ce90331a`, "dismiss mid-session Codex
model-switch modal so sessions don't hang" (#3916).** Symptom: Codex
raises an "Approaching rate limits, switch to a cheaper model?" modal
_mid-session_; the pane becomes input-blocked (neither idle prompt nor
busy indicator), `WaitForIdle` can never confirm idle, and the session
hangs (observed live: a session stuck ~35 minutes after its work was
already committed and pushed, freed by a human keying the modal). Root
cause: dialog dismissal ran only at startup. The trap in the obvious
fix: the existing startup recognizer `ContainsRateLimitDialog` matches
the broad substring "rate limit", safe against a fresh startup pane but
guaranteed to false-match ordinary agent output mid-session and fire
stray Down/Enter into live work. The landed fix adds a _strict_
recognizer, `ContainsModelSwitchModal` (`internal/runtime/dialog.go`),
which requires both "Keep current model" and "Switch to " in the pane,
and `DismissModelSwitchModalIfPresent` (`internal/runtime/tmux/tmux.go`)
selects "Keep current model" (Down, 150ms delay so Enter cannot race the
selection move, Enter). It is invoked exactly where the hang manifests:
in the adapter's `Nudge`, only after `WaitForIdle` times out
(`internal/runtime/tmux/adapter.go`), so a genuinely busy pane is never
touched. The transferable rule: **recognizer strictness must match the
context it runs in.** Startup panes may use permissive matchers;
anything scanned mid-session over arbitrary output needs high-confidence
multi-marker matches, and dismissal must be a no-op by default.

ZFC boundary check for this code: recognizing a known blocking modal and
pressing the key that preserves current behavior (keep model, no spend
change) is mechanical unblocking of a transport, not a judgment call.
Choosing _whether to switch models_ would be policy and belongs to
config/prompts, never here.

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign, from
direct reads of the repo at `internal/runtime/`, `cmd/gc/providers.go`,
`cmd/gc/session_sleep.go`, `cmd/gc/session_reconciler.go`, `Makefile`,
`TESTING.md`, and commits `d82074594` / `1ce90331a` / `b8120d697` on
origin/main; discovery evidence in the workspace discovery report
(machine-local, non-load-bearing). The change-control claim in section 6
is marked provisional pending maintainer answers.

Re-verification one-liners (run from the repo root; if any fails, the
matching section has drifted):

```bash
# §1 contract: interface + sentinels still in place
grep -n "type Provider interface" internal/runtime/runtime.go && grep -n "ErrSessionNotFound\|ErrSessionInitializing" internal/runtime/runtime.go | head -4
# §2/§3 roster + selection strings (registry on main; older trees use a switch in providers.go)
grep -n 'r.Register\|r.RegisterPrefix' cmd/gc/runtime_registry.go 2>/dev/null || grep -n "case \"" cmd/gc/providers.go
grep -n "buildSessionProviderByName\|newSessionProviderForCityByName\|resolveWorkerSpec" cmd/gc/providers.go
# §3 GC_SESSION override still wins
grep -n "GC_SESSION" cmd/gc/city_runtime.go | head -2
# §4 capability gating call sites
grep -n "resolveSleepCapability" cmd/gc/session_sleep.go cmd/gc/session_reconciler.go | head -3
# §5 optional interfaces
grep -n "InteractionProvider\|IdleWaitProvider\|DialogProvider\|SleepCapabilityProvider" internal/runtime/runtime.go internal/runtime/probe.go | head -6
# §7 conformance entry points
grep -rn "func TestFakeConformance\|func TestExecConformance\|func TestTmuxConformance\|func TestSubprocessConformance\|func TestACPConformance\|func TestK8sSessionConformance" internal/runtime/ test/integration/
# §8 worked-example code still on main
grep -n "func submitEnterAndConfirm\|func ContainsModelSwitchModal\|DismissModelSwitchModalIfPresent" internal/runtime/tmux/tmux.go internal/runtime/dialog.go internal/runtime/tmux/adapter.go
```
