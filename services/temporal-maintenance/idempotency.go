package temporalmaintenance

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ActionSelection is the Action string for the create-and-sling selection
// mutation. RealAdapter's runner treats it specially (create a tracking bead,
// then sling it); any other Action is executed as a single Argv command.
const ActionSelection = "gc sling (selection)"

// ProposedMutation is one external action the workflow WOULD take. In shadow
// mode the adapter records it instead of executing it; the armed RealAdapter
// executes it exactly once.
//
// Every field is history-safe: small typed values only — IDs, refs, flags,
// command tokens, and a *path* to a body payload. It never carries the payload
// itself (prompts, diffs, logs), so Workflow/Activity histories stay clean per
// the promotion plan's invariant 3.
type ProposedMutation struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Action         string            `json:"action"` // e.g. "gh pr merge", ActionSelection
	Target         string            `json:"target"` // PR number, issue number, bead id
	BeadRef        string            `json:"bead_ref,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
	// Argv is the concrete command for a single-command mutation (a gated
	// action). Tokens are subcommands/IDs/flags only — never a prompt body.
	Argv []string `json:"argv,omitempty"`
	// BodyFile points at a worker-local file holding a large payload (e.g. a
	// polecat prompt) that a command reads via --body-file. The path is
	// history-safe; the content never enters workflow state.
	BodyFile string `json:"body_file,omitempty"`
	// Result is the runner's result reference (e.g. the real created bead id, or
	// "skipped-inflight"), set on the mutation an armed adapter returns so callers
	// can record the real outcome instead of the synthetic proposal id. Empty for
	// the dry-run adapter (which executes nothing).
	Result     string    `json:"result,omitempty"`
	ProposedAt time.Time `json:"proposed_at"`
}

// SideEffectAdapter records or executes external mutations. The shadow pilot
// only ever binds the dry-run implementation; a promotion would bind a real one
// whose Propose actually calls gh/git/gc/slack.
//
// Propose is find-or-create keyed on IdempotencyKey: a second call with the same
// key returns the first recording and created=false, so Activity retries and
// duplicate deliveries never produce a duplicate proposed action.
//
// ctx is the Activity's context; a real adapter threads it into the subprocess
// (exec.CommandContext) so the Activity's timeout/cancellation bounds the actual
// command instead of leaking a detached process.
type SideEffectAdapter interface {
	Propose(ctx context.Context, m ProposedMutation) (recorded ProposedMutation, created bool, err error)
	// Recorded returns a stable-ordered snapshot of everything proposed so far.
	Recorded() []ProposedMutation
}

// DryRunAdapter is the shadow-mode adapter. It records proposals in memory,
// deduplicated by idempotency key, and never touches an external service.
// Safe for concurrent Activity execution.
type DryRunAdapter struct {
	mu    sync.Mutex
	byKey map[string]ProposedMutation
	order []string // insertion order of keys, for a deterministic snapshot
}

// NewDryRunAdapter returns an empty shadow adapter.
func NewDryRunAdapter() *DryRunAdapter {
	return &DryRunAdapter{byKey: map[string]ProposedMutation{}}
}

// Propose records m unless its idempotency key was already seen. The dry-run
// adapter performs no IO, so ctx is unused.
func (a *DryRunAdapter) Propose(_ context.Context, m ProposedMutation) (ProposedMutation, bool, error) {
	if m.IdempotencyKey == "" {
		return ProposedMutation{}, false, fmt.Errorf("proposed mutation %q has no idempotency key", m.Action)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, ok := a.byKey[m.IdempotencyKey]; ok {
		return existing, false, nil
	}
	a.byKey[m.IdempotencyKey] = m
	a.order = append(a.order, m.IdempotencyKey)
	return m, true, nil
}

// Recorded returns the proposals in insertion order.
func (a *DryRunAdapter) Recorded() []ProposedMutation {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ProposedMutation, 0, len(a.order))
	for _, k := range a.order {
		out = append(out, a.byKey[k])
	}
	return out
}

// idempotencyKey derives a stable key for a gated action. It is computed inside
// the Workflow (deterministically) and threaded through the Activity, so an
// Activity retry reuses the same key. Never include timestamps or random data.
func idempotencyKey(repo, cycleKey, branch, action, target string) string {
	return fmt.Sprintf("temporal-shadow/%s/%s/%s/%s/%s", repo, cycleKey, branch, action, target)
}
