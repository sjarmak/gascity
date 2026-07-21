package temporalmaintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/client"
)

// ClientStateReader adapts a Temporal client to WorkflowStateReader via the
// `state` Query.
type ClientStateReader struct {
	c client.Client
}

// NewClientStateReader wraps a Temporal client for the reconciler.
func NewClientStateReader(c client.Client) *ClientStateReader { return &ClientStateReader{c: c} }

// QueryState runs the workflow's `state` Query and decodes the result.
func (r *ClientStateReader) QueryState(ctx context.Context, workflowID string) (MaintenanceCycleState, error) {
	resp, err := r.c.QueryWorkflow(ctx, workflowID, "", QueryState)
	if err != nil {
		return MaintenanceCycleState{}, err
	}
	var st MaintenanceCycleState
	if err := resp.Get(&st); err != nil {
		return MaintenanceCycleState{}, err
	}
	return st, nil
}

// GHStatusFetcher reads a PR's CI/review status via `gh` REST and maps the
// provider enums to typed Verdicts. Read-only; the write side stays behind the
// at-most-once executor and the human gate.
type GHStatusFetcher struct {
	Dir string // working directory for gh (repo checkout or any dir with gh auth)
}

// NewGHStatusFetcher returns a fetcher running gh from dir.
func NewGHStatusFetcher(dir string) *GHStatusFetcher { return &GHStatusFetcher{Dir: dir} }

// statusCheck is one entry of the gh statusCheckRollup — a check run (Status +
// Conclusion) or a legacy commit status (State).
type statusCheck struct {
	Status     string `json:"status"`     // check runs: QUEUED|IN_PROGRESS|COMPLETED
	Conclusion string `json:"conclusion"` // SUCCESS|FAILURE|NEUTRAL|SKIPPED|CANCELLED|...
	State      string `json:"state"`      // statuses: SUCCESS|PENDING|FAILURE|ERROR
}

// ghPRView is the subset of `gh pr view --json` we consume.
type ghPRView struct {
	ReviewDecision    string        `json:"reviewDecision"`
	StatusCheckRollup []statusCheck `json:"statusCheckRollup"`
}

// Fetch reads pr's status. repo is "owner/name"; pr is the number.
func (f *GHStatusFetcher) Fetch(ctx context.Context, repo, pr string) (PRStatus, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", pr,
		"--repo", repo, "--json", "reviewDecision,statusCheckRollup")
	cmd.Dir = f.Dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// Output() (not CombinedOutput) keeps stderr — deprecation/rate-limit notes —
	// out of the JSON body we parse.
	out, err := cmd.Output()
	if err != nil {
		return PRStatus{}, fmt.Errorf("gh pr view %s/%s: %w: %s", repo, pr, err, strings.TrimSpace(stderr.String()))
	}
	var view ghPRView
	if err := json.Unmarshal(out, &view); err != nil {
		return PRStatus{}, fmt.Errorf("gh pr view %s/%s: parse: %w", repo, pr, err)
	}
	return PRStatus{
		CIVerdict:     ciVerdict(view),
		ReviewVerdict: reviewVerdict(view.ReviewDecision),
	}, nil
}

// reviewVerdict maps GitHub's reviewDecision enum to a Verdict.
func reviewVerdict(decision string) Verdict {
	switch decision {
	case "APPROVED":
		return VerdictPass
	case "CHANGES_REQUESTED":
		return VerdictFail
	default: // REVIEW_REQUIRED, "", ...
		return VerdictUnknown
	}
}

// checkOutcome classifies one rollup entry.
type checkOutcome int

const (
	outcomeSuccess checkOutcome = iota // affirmatively green
	outcomeFail                        // affirmatively red
	outcomeNotYet                      // pending, action-required, stale, or unknown
)

// classifyCheck maps one rollup entry (a check run with Status/Conclusion, or a
// legacy status context with State) to an outcome. Pass is an explicit allowlist:
// anything not affirmatively success is at best notYet, so an unrecognised or
// future GitHub enum value can never be read as green.
func classifyCheck(status, conclusion, state string) checkOutcome {
	// Legacy commit status context: State only, no Conclusion.
	if conclusion == "" && state != "" {
		switch state {
		case "SUCCESS":
			return outcomeSuccess
		case "FAILURE", "ERROR":
			return outcomeFail
		default: // PENDING, EXPECTED, ...
			return outcomeNotYet
		}
	}
	// Check run still running.
	if status != "" && status != "COMPLETED" {
		return outcomeNotYet
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return outcomeSuccess
	case "FAILURE", "CANCELLED", "TIMED_OUT", "STARTUP_FAILURE":
		return outcomeFail
	default: // ACTION_REQUIRED, STALE, "", or any unknown value — not green
		return outcomeNotYet
	}
}

// ciVerdict reduces the status-check rollup to a single Verdict. Pass requires
// EVERY entry to be affirmatively success-class; any failure -> fail; anything
// else (pending, action-required, stale, empty, unknown enum) -> unknown, so the
// cycle parks rather than advancing on not-actually-green CI. An empty rollup is
// unknown (no signal yet), never a false pass.
func ciVerdict(view ghPRView) Verdict {
	if len(view.StatusCheckRollup) == 0 {
		return VerdictUnknown
	}
	allSuccess := true
	for _, c := range view.StatusCheckRollup {
		switch classifyCheck(c.Status, c.Conclusion, c.State) {
		case outcomeFail:
			return VerdictFail
		case outcomeNotYet:
			allSuccess = false
		}
	}
	if allSuccess {
		return VerdictPass
	}
	return VerdictUnknown
}
