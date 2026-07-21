package temporalmaintenance

import "time"

// Phase enumerates the durable states of one maintenance cycle. The design note
// (docs/design/temporal-maintenance-run-pilot.md) fixes this transition order:
//
//	scheduled -> selecting -> fanout -> awaiting-events
//	  -> awaiting-human-decision (only when an external mutation is gated)
//	  -> executing -> done
//
// A cycle may also terminate at Aborted from a human abort decision.
type Phase string

const (
	PhaseScheduled      Phase = "scheduled"
	PhaseSelecting      Phase = "selecting"
	PhaseFanout         Phase = "fanout"
	PhaseAwaitingEvents Phase = "awaiting-events"
	PhaseAwaitingHuman  Phase = "awaiting-human-decision"
	PhaseExecuting      Phase = "executing"
	PhaseDone           Phase = "done"
	PhaseAborted        Phase = "aborted"
)

// Verdict is a small typed outcome carried on the workflow state. Business
// payloads (diffs, transcripts, review bodies) never enter workflow state; only
// the verdict, the bead/artifact IDs, and timestamps do.
type Verdict string

const (
	VerdictUnknown Verdict = ""
	VerdictPass    Verdict = "pass"
	VerdictFail    Verdict = "fail"
	VerdictSkip    Verdict = "skip"
)

// Decision is the set of human gate outcomes. Updates that do not carry one of
// these are rejected by the Update validator before mutating state.
type Decision string

const (
	DecisionApprove  Decision = "approve"
	DecisionReject   Decision = "reject"
	DecisionSkip     Decision = "skip"
	DecisionAbort    Decision = "abort"
	DecisionReprompt Decision = "reprompt"
)

// ValidDecision reports whether d is a recognized human gate decision.
func ValidDecision(d Decision) bool {
	switch d {
	case DecisionApprove, DecisionReject, DecisionSkip, DecisionAbort, DecisionReprompt:
		return true
	default:
		return false
	}
}

// BranchState is the durable state of one of the two independent branches (the
// review branch over the highest-priority PR, the author branch over the
// highest-priority suitable issue).
type BranchState struct {
	Kind          string    `json:"kind"` // "review" | "author"
	SelectionBead string    `json:"selection_bead,omitempty"`
	WorkBead      string    `json:"work_bead,omitempty"`
	CIVerdict     Verdict   `json:"ci_verdict,omitempty"`
	ReviewVerdict Verdict   `json:"review_verdict,omitempty"`
	Escalated     bool      `json:"escalated,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Complete reports whether the branch has received the events it waits on. The
// author branch waits on CI; the review branch waits on a review verdict.
func (b BranchState) Complete() bool {
	switch b.Kind {
	case "author":
		return b.CIVerdict != VerdictUnknown
	case "review":
		return b.ReviewVerdict != VerdictUnknown
	default:
		return false
	}
}

// HumanDecisionRecord is the acknowledged result of a human gate Update. It is
// stored in workflow state (and returned to the Update caller) so the decision
// survives worker restart and is queryable.
type HumanDecisionRecord struct {
	Decision  Decision  `json:"decision"`
	Approver  string    `json:"approver"`
	Note      string    `json:"note,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
	AppliesTo string    `json:"applies_to,omitempty"` // idempotency key of the gated action
}

// MaintenanceCycleState is the complete queryable state of a cycle. It holds
// only small typed values: IDs, phases, verdicts, timestamps, and artifact
// references — never prompt/transcript/diff/log/secret payloads.
type MaintenanceCycleState struct {
	Repo            string                 `json:"repo"`
	CycleKey        string                 `json:"cycle_key"`
	Phase           Phase                  `json:"phase"`
	Branches        map[string]BranchState `json:"branches"`
	BeadIDs         []string               `json:"bead_ids"`
	NeedsHuman      bool                   `json:"needs_human"`
	GatedAction     string                 `json:"gated_action,omitempty"` // idempotency key awaiting a decision
	Decision        *HumanDecisionRecord   `json:"decision,omitempty"`
	ArtifactRefs    []string               `json:"artifact_refs"`
	TerminalOutcome string                 `json:"terminal_outcome,omitempty"`
	StartedAt       time.Time              `json:"started_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// addBead records a bead ID once (find-or-append), preserving determinism.
func (s *MaintenanceCycleState) addBead(id string) {
	if id == "" {
		return
	}
	for _, existing := range s.BeadIDs {
		if existing == id {
			return
		}
	}
	s.BeadIDs = append(s.BeadIDs, id)
}
