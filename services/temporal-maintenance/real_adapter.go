package temporalmaintenance

import (
	"context"
	"errors"
	"fmt"
)

// ErrRealAdapterUnarmed is returned by RealAdapter.Propose when no store/runner
// is bound. The zero-value RealAdapter (NewRealAdapter) is intentionally
// unarmed: binding it by mistake fails closed rather than silently no-opping or
// half-executing a mutation. NewArmedRealAdapter binds the real execution path.
var ErrRealAdapterUnarmed = errors.New(
	"RealAdapter: unarmed — no persisted store/runner bound; " +
		"use NewArmedRealAdapter, or bind DryRunAdapter for shadow operation",
)

// TerminalExecError reports a key whose one execution already failed (or is
// stuck pending after a crash). RealAdapter returns it instead of re-running, so
// a caller can surface the stuck mutation to the human/reconciler rather than
// risk a double side effect.
type TerminalExecError struct {
	Key    string
	Status ExecStatus
	Msg    string
}

func (e *TerminalExecError) Error() string {
	return fmt.Sprintf("RealAdapter: key %q is terminal (%s): %s", e.Key, e.Status, e.Msg)
}

// RealAdapter is the promotion-path SideEffectAdapter. It executes each proposed
// mutation exactly once, keyed by IdempotencyKey against a persisted KeyStore,
// so an Activity retry or a worker restart mid-execution never double-fires.
//
// It stays UNREGISTERED through P2: the worker still binds DryRunAdapter. An
// armed RealAdapter is exercised only by tests and the staging harness. The
// human-approval gate lives in the Workflow (ProposeExternalAction runs only
// after an approve Update), so RealAdapter itself performs no gate check — it
// executes whatever it is handed, once.
type RealAdapter struct {
	store     *KeyStore
	runner    CommandRunner
	escalator Escalator // optional; nil disables notification (quarantine is the durable backstop)
}

// RealAdapterOption customizes an armed RealAdapter at construction.
type RealAdapterOption func(*RealAdapter)

// WithEscalator binds the transport that surfaces a poisoned-pending drop to a
// human (mayor / #gascity-maintenance). Without it the adapter still quarantines
// the poison durably; it just does not notify.
func WithEscalator(e Escalator) RealAdapterOption {
	return func(a *RealAdapter) { a.escalator = e }
}

// compile-time proof RealAdapter implements the adapter contract.
var _ SideEffectAdapter = (*RealAdapter)(nil)

// NewRealAdapter returns the unarmed adapter (fails closed on Propose).
func NewRealAdapter() *RealAdapter { return &RealAdapter{} }

// NewArmedRealAdapter binds a persisted store and a command runner. Both are
// required; a nil either way leaves the adapter unarmed. Options bind the
// optional escalator.
func NewArmedRealAdapter(store *KeyStore, runner CommandRunner, opts ...RealAdapterOption) *RealAdapter {
	a := &RealAdapter{store: store, runner: runner}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *RealAdapter) armed() bool { return a.store != nil && a.runner != nil }

// Propose executes m exactly once. On the first call for a key it claims the key,
// runs the command (bounded by ctx), and records the outcome; a duplicate call
// (retry, restart) returns the recorded mutation with created=false and never
// re-executes.
func (a *RealAdapter) Propose(ctx context.Context, m ProposedMutation) (ProposedMutation, bool, error) {
	if m.IdempotencyKey == "" {
		return ProposedMutation{}, false, fmt.Errorf("proposed mutation %q has no idempotency key", m.Action)
	}
	if !a.armed() {
		return ProposedMutation{}, false, ErrRealAdapterUnarmed
	}

	rec, claimedNow, err := a.store.Claim(m)
	if err != nil {
		return ProposedMutation{}, false, err
	}
	if !claimedNow {
		// Someone already owns this key. Never re-run the side effect.
		switch rec.Status {
		case ExecDone:
			done := rec.Mutation
			done.Result = rec.ResultRef // surface the real outcome on a duplicate too
			return done, false, nil
		case ExecPending:
			// A pending record on a non-claiming Propose means the claimer is
			// gone: Temporal serializes attempts of one activity and cancels the
			// prior attempt's ctx (killing its subprocess) at StartToClose, so a
			// redelivery only reaches here after the original attempt ended. The
			// claim is poison — unresolvable, and re-running would risk a double
			// side effect — so quarantine it and escalate rather than fail silently.
			return a.handlePoisonedPending(ctx, rec)
		default: // failed — a genuine terminal outcome, surface it (no escalation).
			return rec.Mutation, false, &TerminalExecError{Key: m.IdempotencyKey, Status: rec.Status, Msg: rec.Err}
		}
	}

	// resultRef may be non-empty even on error (e.g. runSelection created a bead
	// then failed to sling) — record it so the reconciler reads a structured ref
	// instead of parsing the error text.
	resultRef, runErr := a.runner.Run(ctx, m)
	if runErr != nil {
		if ferr := a.store.Fail(m.IdempotencyKey, resultRef, runErr.Error()); ferr != nil {
			return ProposedMutation{}, false, fmt.Errorf("run failed (%v) and recording failure failed: %w", runErr, ferr)
		}
		return ProposedMutation{}, false, runErr
	}
	if cerr := a.store.Complete(m.IdempotencyKey, resultRef); cerr != nil {
		return ProposedMutation{}, false, cerr
	}
	m.Result = resultRef // the real created bead id (or "skipped-inflight")
	return m, true, nil
}

// handlePoisonedPending quarantines a claim left pending by a crashed worker and
// escalates the dropped cycle, then returns a TerminalExecError so the workflow
// still fails closed (the mutation is never re-run — at-most-once beats a
// duplicate polecat against real upstream work). Quarantine only transitions the
// record pending -> failed; it never executes the command. Escalation fires
// exactly once (only on the first redelivery that performs the transition); a
// delivery failure is folded into the returned error but never lost, because the
// quarantine record on disk is the durable backstop.
func (a *RealAdapter) handlePoisonedPending(ctx context.Context, rec ExecRecord) (ProposedMutation, bool, error) {
	reason := fmt.Sprintf(
		"quarantined: claim left pending by a crashed worker (claimed %s), unresolvable — never retried to preserve at-most-once",
		rec.ClaimedAt.UTC().Format("2006-01-02T15:04:05Z"),
	)
	quarantined, transitioned, qerr := a.store.Quarantine(rec.Key, reason)
	if qerr != nil {
		// Could not quarantine — still refuse to re-run; surface the original poison.
		return rec.Mutation, false, &TerminalExecError{Key: rec.Key, Status: rec.Status, Msg: rec.Err}
	}

	termMsg := reason
	if transitioned && a.escalator != nil {
		esc := Escalation{
			Key:       quarantined.Key,
			Action:    quarantined.Mutation.Action,
			Target:    quarantined.Mutation.Target,
			BeadRef:   quarantined.Mutation.BeadRef,
			Reason:    reason,
			ClaimedAt: quarantined.ClaimedAt,
		}
		if eerr := a.escalator.Escalate(ctx, esc); eerr != nil {
			// Notification failed; the quarantine record persists as evidence. Note
			// it in the terminal error so the workflow history shows the gap.
			termMsg = fmt.Sprintf("%s (escalation delivery failed: %v)", reason, eerr)
		}
	}
	return quarantined.Mutation, false, &TerminalExecError{Key: quarantined.Key, Status: ExecFailed, Msg: termMsg}
}

// Recorded returns every mutation the store has seen, in stable order — the same
// contract DryRunAdapter.Recorded offers, so tests can assert against either.
func (a *RealAdapter) Recorded() []ProposedMutation {
	if a.store == nil {
		return nil
	}
	recs, err := a.store.All()
	if err != nil {
		return nil
	}
	out := make([]ProposedMutation, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Mutation)
	}
	return out
}
