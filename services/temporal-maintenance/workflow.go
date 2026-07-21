package temporalmaintenance

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Task queue and message names. The pilot injects Signals/Updates through a
// local harness; a promotion would drive them from the Gas City event bus and
// GitHub webhooks.
const (
	TaskQueue = "temporal-maintenance-shadow"

	SignalBeadClosed     = "bead.closed"
	SignalCICompleted    = "ci.completed"
	SignalReviewComplete = "review.completed"
	SignalEscalation     = "agent.escalation"

	QueryState = "state"

	UpdateHumanDecision = "humanDecision"
)

// branchOrder fixes iteration order for the two branches. Ranging a Go map is
// non-deterministic, which would break Workflow replay; the workflow always
// iterates this slice instead.
var branchOrder = []string{"review", "author"}

// MaintenanceCycleInput starts one cycle. RequireHumanGate models the design's
// "request human decision when an external mutation is gated" transition; when
// false, the cycle has no external mutation and finishes without a gate.
type MaintenanceCycleInput struct {
	Repo             string `json:"repo"`
	CycleKey         string `json:"cycle_key"`
	RequireHumanGate bool   `json:"require_human_gate"`
	// GatedAction/GatedTarget describe the single external mutation the approved
	// gate would authorize (e.g. action "gh pr merge", target "1712").
	GatedAction string `json:"gated_action,omitempty"`
	GatedTarget string `json:"gated_target,omitempty"`
	// DispatchOnly is the faithful replacement of the legacy maintenance-cycle:
	// dispatch the review+author selections (create+sling a polecat) and finish —
	// no fanout, no CI/review wait, no gate, NO merge. Legacy "NEVER auto-merge";
	// the polecat does the work and halts, a human merges via the normal pipeline.
	// The fanout/gate path stays available for a future richer, gated model.
	DispatchOnly bool `json:"dispatch_only,omitempty"`
}

// beadClosedSignal, ciSignal, reviewSignal, escalationSignal are the typed
// signal payloads.
type beadClosedSignal struct {
	Bead string `json:"bead"`
}
type branchEventSignal struct {
	Branch  string  `json:"branch"`
	Verdict Verdict `json:"verdict"`
}
type escalationSignal struct {
	Branch string `json:"branch"`
}

// HumanDecisionInput is the Update payload for the human gate.
type HumanDecisionInput struct {
	Decision Decision `json:"decision"`
	Approver string   `json:"approver"`
	Note     string   `json:"note,omitempty"`
}

// WorkflowID returns the stable ID for a cycle, per the design note.
func WorkflowID(repo, cycleKey string) string {
	return fmt.Sprintf("gascity-maintenance/%s/%s", repo, cycleKey)
}

// MaintenanceCycleWorkflow models one scheduled maintenance cycle as a durable
// Run. It is deterministic: all IO lives in Activities, timestamps come from
// workflow.Now, and it iterates branchOrder (never a map) for command order.
func MaintenanceCycleWorkflow(ctx workflow.Context, in MaintenanceCycleInput) (MaintenanceCycleState, error) {
	if in.Repo == "" {
		return MaintenanceCycleState{}, temporal.NewNonRetryableApplicationError(
			"repo is required", "InvalidInput", nil)
	}

	now := workflow.Now(ctx)
	// A recurring Schedule fires with a static input, but each cycle must have a
	// distinct key so its selection idempotency keys don't collide with the prior
	// cycle's (which would dedup every fire after the first into a no-op). Derive
	// a per-fire key from the deterministic workflow clock when none is supplied.
	cycleKey := in.CycleKey
	if cycleKey == "" {
		// Second granularity: distinct per 120m fire, and a manual/backfill start
		// only collides with another start in the same UTC second.
		cycleKey = now.UTC().Format("20060102T150405")
	}
	state := &MaintenanceCycleState{
		Repo:         in.Repo,
		CycleKey:     cycleKey,
		Phase:        PhaseScheduled,
		Branches:     map[string]BranchState{},
		BeadIDs:      []string{},
		ArtifactRefs: []string{},
		StartedAt:    now,
		UpdatedAt:    now,
	}
	for _, kind := range branchOrder {
		state.Branches[kind] = BranchState{Kind: kind, UpdatedAt: now}
	}

	touch := func() { state.UpdatedAt = workflow.Now(ctx) }

	// Query handler is registered before any await so dashboard reads work in
	// every phase.
	if err := workflow.SetQueryHandler(ctx, QueryState, func() (MaintenanceCycleState, error) {
		return *state, nil
	}); err != nil {
		return *state, err
	}

	// Human-decision Update: validated (approver present, decision recognized,
	// only while the gate is open) and acknowledged (returns the record).
	err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateHumanDecision,
		func(ctx workflow.Context, input HumanDecisionInput) (HumanDecisionRecord, error) {
			rec := HumanDecisionRecord{
				Decision:  input.Decision,
				Approver:  input.Approver,
				Note:      input.Note,
				DecidedAt: workflow.Now(ctx),
				AppliesTo: state.GatedAction,
			}
			state.Decision = &rec
			touch()
			return rec, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, input HumanDecisionInput) error {
				if !state.NeedsHuman {
					return temporal.NewApplicationError("no human gate is open", "GateClosed")
				}
				if input.Approver == "" {
					return temporal.NewApplicationError("approver is required", "MissingApprover")
				}
				if !ValidDecision(input.Decision) {
					return temporal.NewApplicationError(
						fmt.Sprintf("unrecognized decision %q", input.Decision), "InvalidDecision")
				}
				return nil
			},
		})
	if err != nil {
		return *state, err
	}

	ao := workflow.ActivityOptions{
		// Selection does a real create + gc-sling (worktree setup + nudge), which
		// runs well past 30s — legacy maintenance-cycle budgeted 5m. A tighter
		// timeout kills the sling mid-flight, stranding the at-most-once claim as
		// pending. Retries still bound transient (pre-claim) failures; a terminal
		// post-claim outcome is surfaced non-retryably by the Activity.
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	actx := workflow.WithActivityOptions(ctx, ao)
	var acts *Activities // nil method value: used only to resolve the Activity name

	// --- Selecting: dispatch a selection bead per branch. ---
	state.Phase = PhaseSelecting
	touch()
	for _, kind := range branchOrder {
		var res DispatchSelectionResult
		err := workflow.ExecuteActivity(actx, acts.DispatchSelection, DispatchSelectionInput{
			Repo: in.Repo, CycleKey: cycleKey, Branch: kind,
		}).Get(ctx, &res)
		if err != nil {
			return *state, err
		}
		b := state.Branches[kind]
		b.SelectionBead = res.SelectionBead
		b.UpdatedAt = workflow.Now(ctx)
		state.Branches[kind] = b
		// Don't record a bead the in-flight guard skipped — no work was dispatched
		// for this half this cycle.
		if !res.Skipped {
			state.addBead(res.SelectionBead)
		}
	}

	// --- Dispatch-only: legacy parity. The selections are slung; the cycle's
	// work now lives with the polecats (which select, do the work, and halt).
	// Nothing further to orchestrate, so finish. ---
	if in.DispatchOnly {
		state.Phase = PhaseDone
		state.TerminalOutcome = "dispatched"
		touch()
		return *state, nil
	}

	// --- Fanout + AwaitingEvents: wait for each branch's event. ---
	state.Phase = PhaseFanout
	touch()
	beadCh := workflow.GetSignalChannel(ctx, SignalBeadClosed)
	ciCh := workflow.GetSignalChannel(ctx, SignalCICompleted)
	reviewCh := workflow.GetSignalChannel(ctx, SignalReviewComplete)
	escCh := workflow.GetSignalChannel(ctx, SignalEscalation)

	applyBranchVerdict := func(branch string, v Verdict, review bool) {
		b, ok := state.Branches[branch]
		if !ok {
			return
		}
		if review {
			b.ReviewVerdict = v
		} else {
			b.CIVerdict = v
		}
		b.UpdatedAt = workflow.Now(ctx)
		state.Branches[branch] = b
		touch()
	}

	branchesComplete := func() bool {
		for _, kind := range branchOrder {
			if !state.Branches[kind].Complete() {
				return false
			}
		}
		return true
	}

	state.Phase = PhaseAwaitingEvents
	touch()
	for !branchesComplete() {
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(ciCh, func(c workflow.ReceiveChannel, _ bool) {
			var s branchEventSignal
			c.Receive(ctx, &s)
			applyBranchVerdict(s.Branch, s.Verdict, false)
		})
		sel.AddReceive(reviewCh, func(c workflow.ReceiveChannel, _ bool) {
			var s branchEventSignal
			c.Receive(ctx, &s)
			applyBranchVerdict(s.Branch, s.Verdict, true)
		})
		sel.AddReceive(beadCh, func(c workflow.ReceiveChannel, _ bool) {
			var s beadClosedSignal
			c.Receive(ctx, &s)
			state.addBead(s.Bead)
			touch()
		})
		sel.AddReceive(escCh, func(c workflow.ReceiveChannel, _ bool) {
			var s escalationSignal
			c.Receive(ctx, &s)
			if b, ok := state.Branches[s.Branch]; ok {
				b.Escalated = true
				b.UpdatedAt = workflow.Now(ctx)
				state.Branches[s.Branch] = b
				touch()
			}
		})
		sel.Select(ctx)
	}

	// --- Human gate: only when an external mutation is gated. ---
	if in.RequireHumanGate {
		state.GatedAction = idempotencyKey(in.Repo, cycleKey, "cycle", in.GatedAction, in.GatedTarget)
		state.Phase = PhaseAwaitingHuman
		for {
			state.NeedsHuman = true
			touch()
			if err := workflow.Await(ctx, func() bool { return state.Decision != nil }); err != nil {
				return *state, err
			}
			d := state.Decision
			if d.Decision == DecisionReprompt {
				state.Decision = nil // re-open the gate and wait again
				continue
			}
			break
		}
		state.NeedsHuman = false
		touch()

		switch state.Decision.Decision {
		case DecisionReject:
			state.Phase = PhaseDone
			state.TerminalOutcome = "rejected"
			touch()
			return *state, nil
		case DecisionSkip:
			state.Phase = PhaseDone
			state.TerminalOutcome = "skipped"
			touch()
			return *state, nil
		case DecisionAbort:
			state.Phase = PhaseAborted
			state.TerminalOutcome = "aborted"
			touch()
			return *state, nil
		case DecisionApprove:
			// fall through to Executing
		}

		// --- Executing: record the approved (dry-run) external mutation. ---
		state.Phase = PhaseExecuting
		touch()
		var recorded ProposedMutation
		err := workflow.ExecuteActivity(actx, acts.ProposeExternalAction, ProposeExternalActionInput{
			IdempotencyKey: state.GatedAction,
			Action:         in.GatedAction,
			Target:         in.GatedTarget,
			Params:         map[string]string{"approver": state.Decision.Approver},
		}).Get(ctx, &recorded)
		if err != nil {
			return *state, err
		}
		state.ArtifactRefs = append(state.ArtifactRefs, recorded.IdempotencyKey)
	}

	state.Phase = PhaseDone
	if state.TerminalOutcome == "" {
		state.TerminalOutcome = "completed"
	}
	touch()
	return *state, nil
}
