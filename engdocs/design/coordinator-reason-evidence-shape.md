# Coordinator disposition reason evidence-shape check

Status: Proposed

`internal/dispatch/retry.go`'s `typedDeliverableCloseFor` decodes the typed
`gc.coordinator_outcome.producer_disposition` close envelope (written by the
external `gc-outcome-close` tool) and validates it strictly in six ways, but
the `Reason` field only had to be non-empty. `classifyRetryAttempt` then
promotes an outcome-less bead to `pass` on the strength of that envelope
alone, so a one-word placeholder reason (recorded on gc-5a56b as the literal
string `test-probe-ignore`) manufactures a pass with no other signal
downstream noticing. The artifact on gc-5a56b was fine; only the attestation
was junk, which is the dangerous shape — nothing fails, and the record reads
as delivered.

The writer of the envelope lives outside this repository
(`internal/beadmeta/keys.go` says so at the key's doc comment), so this fix
hardens the reader instead: a check at the consumer catches every writer, not
just the one city tool that produced this instance.

## The reason field cannot be validated by meaning

This repository bans keyword/regex meaning-detection and hardcoded-phrasing
lists in orchestration code — a judgment call in Go is a violation. Scanning
the reason text for words like "commit" or "test" would be exactly that
violation, and it would also be trivially defeated by whatever the next
placeholder string happens to be.

The check implemented here is instead a pure SHAPE test:
`coordinatorReasonHasEvidenceShape` (`internal/dispatch/retry.go`) tokenizes
the reason and asks whether any token has the mechanical shape of one of
three structural artifacts, never what the token *means*:

- a git object name: 7-40 hex characters, requiring at least one `a-f`/`A-F`
  letter so an ordinary decimal number (a duration, a count) is not mistaken
  for an abbreviated SHA;
- a ref-shaped path: alphanumeric/`.`/`_`/`-` segments joined by at least one
  `/`, matching a branch name or worktree-relative path;
- a Go test identifier: the `TestXxx` shape `go test` itself requires to
  select a test.

None of the three predicates enumerate acceptable words or phrases. A reason
either contains a token with one of these shapes or it doesn't.

## Warn-only by default, following the ADR-0009 precedent

This repository already solved "a close gate that must not break every
caller on day one": `cmd/gc/work_record_gate.go` / `internal/workrecord`
gates the ADR-0009 work-record close behind `GC_WORK_RECORD_ENFORCE`,
warn-only by default. This change copies that shape exactly:
`GC_COORDINATOR_REASON_ENFORCE` (`coordinatorReasonEnforceEnvVar` in
`internal/dispatch/retry.go`) gates whether a weak reason blocks promotion
(enforce) or only emits a `retry-eval ... result=surfaced` trace line while
still promoting to `pass` (the default). Flipping the default to reject is a
separate decision this change does not make.

When enforcement is on and the reason fails the shape check,
`classifyRetryAttempt` does not invent a new outcome string — it simply does
not promote, so the bead falls through to the existing outcome-less path
(`transient` / `missing_outcome`), the same path an envelope-less subject
already takes today.

## Scope boundary

- `typedDeliverableCloseFor`'s six existing checks are unchanged; the new
  check is a separate, later gate applied only when all six already pass.
- The envelope schema is unchanged; no new required field was added to the
  writer's contract.
- No role names appear in the new code.
