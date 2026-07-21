package temporalmaintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Activities carries the worker-side dependencies for the pilot. The adapter is
// injected at worker registration time; the Workflow never sees it directly
// (workflow code stays deterministic and side-effect free).
type Activities struct {
	Adapter SideEffectAdapter
	// Selection, when set, makes DispatchSelection build a REAL create+sling
	// (the armed cutover). When nil the selection stays a recorded shadow, so the
	// dry-run worker's behaviour is unchanged.
	Selection *SelectionConfig
}

// SelectionConfig is the worker-side configuration the armed selection needs —
// the polecat target and the per-branch prompt template. Prompts live on disk
// (never in workflow history); their paths flow to the runner as BodyFile.
type SelectionConfig struct {
	Polecat  string // gc-sling target, e.g. a polecat pool path
	Rig      string // beads rig, e.g. "gascity"
	Priority string // bead priority, e.g. "1"
	// PromptFile maps branch ("review"|"author") to the absolute path of that
	// half's polecat prompt template (migrated from bin/maintenance-cycle).
	PromptFile map[string]string
	// SlackChannel / PLAgent drive the loop-close metadata legacy stamped so the
	// polecat's result surfaces to the maintenance channel. Optional.
	SlackChannel string
	PLAgent      string
}

// selectionParams builds the create+sling Params + BodyFile for a branch. With
// no SelectionConfig it returns the shadow params (recorded, not executed).
func (a *Activities) selectionParams(branch, title string) (map[string]string, string) {
	if a.Selection == nil {
		return map[string]string{"branch": branch, "label": "temporal-shadow"}, ""
	}
	rig := a.Selection.Rig
	if rig == "" {
		rig = "gascity"
	}
	p := map[string]string{
		"branch":     branch,
		"polecat":    a.Selection.Polecat,
		"title":      title,
		"priority":   a.Selection.Priority,
		"labels":     fmt.Sprintf("maintenance-cycle,maintenance-cycle:%s,rig:%s", branch, rig),
		"half_label": "maintenance-cycle:" + branch,
	}
	if a.Selection.SlackChannel != "" {
		p["slack_channel"] = a.Selection.SlackChannel
	}
	if a.Selection.PLAgent != "" {
		p["pl_agent"] = a.Selection.PLAgent
	}
	return p, a.Selection.PromptFile[branch]
}

// DispatchSelectionInput asks the selection branch to nominate its subject. In
// shadow mode this does not actually sling a polecat; it records the proposed
// selection dispatch and returns a deterministic shadow bead ID so tests are
// hermetic and replays are stable.
type DispatchSelectionInput struct {
	Repo     string `json:"repo"`
	CycleKey string `json:"cycle_key"`
	Branch   string `json:"branch"` // "review" | "author"
}

// DispatchSelectionResult reports the selection bead the branch uses: the real
// gc-* id when armed, or the synthetic shadow id in dry-run. Skipped is true when
// the in-flight guard fired (a same-half bead was already open), so no bead was
// created this cycle.
type DispatchSelectionResult struct {
	SelectionBead string `json:"selection_bead"`
	Created       bool   `json:"created"`
	Skipped       bool   `json:"skipped"`
}

// DispatchSelection records a proposed selection dispatch (a gc mutation, so it
// is dry-run) and returns the internal shadow bead ID. The bead carries the
// temporal-shadow marking via its ID prefix so it is cleanly identifiable for
// later removal.
//
// GATING NOTE (design question flagged in P2 review, pending Stephanie's
// confirmation): selection dispatch runs in the Selecting phase, BEFORE the
// human gate — mirroring the legacy bin/maintenance-cycle create_and_sling,
// which slings work to our own polecats autonomously every cycle. Only the
// terminal publish-class mutation (gh pr merge, handled by ProposeExternalAction
// after an approve Update) is human-gated. The promotion plan's invariant 3
// lists gc as an "external mutation"; whether autonomous selection-dispatch must
// also sit behind the gate, or stays autonomous to preserve the 120m cadence, is
// a decision for Stephanie before Phase 4 arms a real adapter here.
//
// P2 SCOPE NOTE: this Activity still emits a SHADOW selection (branch+label
// only) — it does not populate the polecat/title/BodyFile that the armed
// ExecRunner.runSelection requires, so an armed adapter fails closed on
// selection (verified by TestExecRunner_SelectionRequiresParams). Wiring real
// selection config (polecat target + prompt template) into this Activity is
// Phase 4 work, where the worker receives its live config at deploy.
func (a *Activities) DispatchSelection(ctx context.Context, in DispatchSelectionInput) (DispatchSelectionResult, error) {
	if in.Repo == "" || in.CycleKey == "" || in.Branch == "" {
		return DispatchSelectionResult{}, fmt.Errorf("DispatchSelection: repo, cycle_key and branch are required")
	}
	bead := fmt.Sprintf("temporal-shadow/%s/%s/%s/selection", in.Repo, in.CycleKey, in.Branch)
	// The idempotency key is stable per (repo, cycle, branch) whether shadow or
	// armed, so an Activity retry never double-creates the tracking bead.
	key := idempotencyKey(in.Repo, in.CycleKey, in.Branch, "sling-selection", bead)
	title := fmt.Sprintf("maintenance-cycle %s — %s/%s", in.Branch, in.Repo, in.CycleKey)
	params, bodyFile := a.selectionParams(in.Branch, title)
	recorded, created, err := a.Adapter.Propose(ctx, ProposedMutation{
		IdempotencyKey: key,
		Action:         ActionSelection,
		Target:         bead,
		BeadRef:        bead,
		Params:         params,
		BodyFile:       bodyFile,
		ProposedAt:     activity.GetInfo(ctx).ScheduledTime,
	})
	if err != nil {
		return DispatchSelectionResult{}, nonRetryableIfTerminal(err)
	}
	// The armed adapter returns the real result: a gc-* bead id when it created
	// one, or "skipped-inflight" when the guard fired. Dry-run leaves it empty, so
	// the synthetic id is used for shadow observability.
	res := DispatchSelectionResult{SelectionBead: bead, Created: created}
	switch recorded.Result {
	case "":
		// shadow — keep the synthetic bead id
	case "skipped-inflight":
		res.Skipped = true
	default:
		res.SelectionBead = recorded.Result // real gc-* id
	}
	return res, nil
}

// ProposeExternalActionInput is a gated external mutation the workflow reached
// after its human gate approved it. The IdempotencyKey is computed in the
// Workflow and passed in, so a retry of this Activity reuses it.
type ProposeExternalActionInput struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Action         string            `json:"action"` // "gh pr merge", "gh pr review", "git push", ...
	Target         string            `json:"target"`
	BeadRef        string            `json:"bead_ref,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
}

// ProposeExternalAction records the external mutation instead of executing it.
// Returns whether this call created the record (false = the key was already
// recorded, i.e. a retry or duplicate delivery).
func (a *Activities) ProposeExternalAction(ctx context.Context, in ProposeExternalActionInput) (ProposedMutation, error) {
	if in.IdempotencyKey == "" {
		return ProposedMutation{}, fmt.Errorf("ProposeExternalAction: idempotency_key is required")
	}
	if in.Action == "" {
		return ProposedMutation{}, fmt.Errorf("ProposeExternalAction: action is required")
	}
	recorded, _, err := a.Adapter.Propose(ctx, ProposedMutation{
		IdempotencyKey: in.IdempotencyKey,
		Action:         in.Action,
		Target:         in.Target,
		BeadRef:        in.BeadRef,
		Params:         in.Params,
		Argv:           gatedArgv(in.Action, in.Target),
		ProposedAt:     activity.GetInfo(ctx).ScheduledTime,
	})
	return recorded, nonRetryableIfTerminal(err)
}

// nonRetryableIfTerminal converts a TerminalExecError (a failed, or stuck-pending-
// after-timeout, at-most-once claim) into a non-retryable Activity error: retrying
// would only re-hit the claimed key, so fail fast instead of burning attempts.
// Other errors (and nil) pass through unchanged so transient failures still retry.
func nonRetryableIfTerminal(err error) error {
	if err == nil {
		return nil
	}
	var te *TerminalExecError
	if errors.As(err, &te) {
		return temporal.NewNonRetryableApplicationError(te.Error(), "TerminalExecError", te)
	}
	return err
}

// gatedArgv turns a gated action ("gh pr merge") and its target ("1712") into the
// concrete command an armed runner executes (["gh","pr","merge","1712"]). The
// tokens are history-safe: subcommands, IDs, and flags only. Returns nil when
// the action is empty (the DryRunAdapter ignores Argv anyway).
func gatedArgv(action, target string) []string {
	fields := strings.Fields(action)
	if len(fields) == 0 {
		return nil
	}
	if target != "" {
		fields = append(fields, target)
	}
	return fields
}
