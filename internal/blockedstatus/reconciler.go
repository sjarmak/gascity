// Package blockedstatus plans and applies the stored-status projection of
// canonical dependency readiness. It never derives blockedness itself.
package blockedstatus

import (
	"errors"
	"fmt"
	"sort"
)

// MetadataProjectionKey and its companion constants identify a lifecycle
// projection owned by this reconciler.
const (
	MetadataProjectionKey = "gc.blocked_status_projection"
	MetadataPreimageKey   = "gc.blocked_status_preimage"
	ProjectionVersion     = "v1"
)

// UnsafeReason classifies corpus state that must stop an acting pass.
type UnsafeReason string

// ReasonInvalidPreimage and its companion values identify fail-closed planner
// refusals.
const (
	ReasonInvalidPreimage    UnsafeReason = "invalid_preimage"
	ReasonMissingProjection  UnsafeReason = "missing_is_blocked_projection"
	ReasonProjectionDrift    UnsafeReason = "projection_metadata_drift"
	ReasonUnclassifiedLegacy UnsafeReason = "unclassified_legacy_blocked_status"
)

// ErrIncompleteSnapshot and ErrUnsafeCorpus are acting-pass refusal classes.
var (
	ErrIncompleteSnapshot = errors.New("blocked-status reconciliation: incomplete snapshot")
	ErrUnsafeCorpus       = errors.New("blocked-status reconciliation: unsafe corpus")
)

// UnsafeRow identifies a row and the reason it stopped the whole pass.
type UnsafeRow struct {
	ID     string       `json:"id"`
	Reason UnsafeReason `json:"reason"`
}

// Action is one lifecycle projection guarded by its complete observed tuple.
type Action struct {
	ID                string
	ExpectedRevision  int64
	ExpectedStatus    string
	ExpectedIsBlocked bool
	Status            string
	MetadataSet       map[string]string
	MetadataUnset     []string
}

// Observation is the raw upstream lifecycle/projection tuple. In particular,
// Status must not pass through Gas City's ordinary bead mapper, which
// intentionally normalizes upstream blocked rows to open for legacy callers.
type Observation struct {
	ID             string
	Status         string
	Revision       int64
	IsBlocked      *bool
	Metadata       map[string]string
	LegacyPreimage string
}

// ReconciliationPlan contains deterministic actions or corpus-wide refusals.
type ReconciliationPlan struct {
	Actions []Action
	Unsafe  []UnsafeRow
}

// Snapshot is the complete raw candidate corpus. Readers set Complete only
// after proving their query was not truncated; a bounded reader that reaches
// its limit must leave it false.
type Snapshot struct {
	Observations []Observation
	Complete     bool
}

// Plan builds a deterministic, fail-closed reconciliation plan. A status set
// by this package carries its lifecycle pre-image so a later unblock restores
// open or in_progress exactly. Existing blocked rows without that marker are
// legacy state requiring explicit migration classification.
func Plan(rows []Observation) ReconciliationPlan {
	plan := ReconciliationPlan{}
	for _, row := range rows {
		if row.Status == "closed" {
			continue
		}
		if row.IsBlocked == nil {
			plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonMissingProjection})
			continue
		}

		projection := row.Metadata[MetadataProjectionKey]
		preimage := row.Metadata[MetadataPreimageKey]
		switch row.Status {
		case "blocked":
			switch {
			case projection == "":
				switch row.LegacyPreimage {
				case "open", "in_progress":
					if *row.IsBlocked {
						plan.Actions = append(plan.Actions, migrateLegacyAction(row))
					} else {
						plan.Actions = append(plan.Actions, restoreAction(row, row.LegacyPreimage))
					}
				case "":
					plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonUnclassifiedLegacy})
				default:
					plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonInvalidPreimage})
				}
			case projection != ProjectionVersion:
				plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonProjectionDrift})
			case preimage != "open" && preimage != "in_progress":
				plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonInvalidPreimage})
			case !*row.IsBlocked:
				plan.Actions = append(plan.Actions, restoreAction(row, preimage))
			}
		case "open", "in_progress":
			if projection != "" || preimage != "" {
				plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonProjectionDrift})
				continue
			}
			if *row.IsBlocked {
				plan.Actions = append(plan.Actions, blockAction(row))
			}
		default:
			// Deferred and other lifecycle states are orthogonal to the stored
			// blocked projection. They remain governed by their own lifecycle,
			// but ownership markers on them mean a prior transition did not
			// finish cleanly and must stop the whole pass.
			if projection != "" || preimage != "" {
				plan.Unsafe = append(plan.Unsafe, UnsafeRow{ID: row.ID, Reason: ReasonProjectionDrift})
			}
		}
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].ID < plan.Actions[j].ID })
	sort.Slice(plan.Unsafe, func(i, j int) bool { return plan.Unsafe[i].ID < plan.Unsafe[j].ID })
	return plan
}

func migrateLegacyAction(row Observation) Action {
	return Action{
		ID:                row.ID,
		ExpectedRevision:  row.Revision,
		ExpectedStatus:    row.Status,
		ExpectedIsBlocked: true,
		Status:            "blocked",
		MetadataSet: map[string]string{
			MetadataProjectionKey: ProjectionVersion,
			MetadataPreimageKey:   row.LegacyPreimage,
		},
	}
}

func blockAction(row Observation) Action {
	return Action{
		ID:                row.ID,
		ExpectedRevision:  row.Revision,
		ExpectedStatus:    row.Status,
		ExpectedIsBlocked: true,
		Status:            "blocked",
		MetadataSet: map[string]string{
			MetadataProjectionKey: ProjectionVersion,
			MetadataPreimageKey:   row.Status,
		},
	}
}

func restoreAction(row Observation, status string) Action {
	return Action{
		ID:                row.ID,
		ExpectedRevision:  row.Revision,
		ExpectedStatus:    row.Status,
		ExpectedIsBlocked: false,
		Status:            status,
		MetadataUnset:     []string{MetadataPreimageKey, MetadataProjectionKey},
	}
}

// Patch is the lifecycle and marker mutation applied in one guarded write.
type Patch struct {
	Status        string
	MetadataSet   map[string]string
	MetadataUnset []string
}

// GuardedWriter applies a patch only while the complete observation still
// matches canonical storage state.
type GuardedWriter interface {
	UpdateIfBlockedStateMatches(
		id string,
		expectedRevision int64,
		expectedStatus string,
		expectedIsBlocked bool,
		patch Patch,
	) error
}

// Result reports the bounded pass without hiding partial application.
type Result struct {
	Scanned int
	Planned int
	Applied int
	Unsafe  []UnsafeRow
}

// Options controls whether Run plans only or applies guarded actions.
type Options struct {
	DryRun bool
}

// Run checks the complete corpus before writing any row. Each write then
// composes the row revision with the same in-transaction canonical is_blocked
// observation; a lost observation stops the pass for a later full re-read.
func Run(snapshot Snapshot, writer GuardedWriter, options Options) (Result, error) {
	result := Result{Scanned: len(snapshot.Observations)}
	if !snapshot.Complete {
		return result, ErrIncompleteSnapshot
	}
	plan := Plan(snapshot.Observations)
	result.Planned = len(plan.Actions)
	result.Unsafe = plan.Unsafe
	if len(plan.Unsafe) > 0 {
		return result, fmt.Errorf("%w: %d row(s) require classification", ErrUnsafeCorpus, len(plan.Unsafe))
	}
	if options.DryRun {
		return result, nil
	}
	if len(plan.Actions) > 0 && writer == nil {
		return result, errors.New("blocked-status reconciliation: nil guarded writer")
	}
	for _, action := range plan.Actions {
		if err := writer.UpdateIfBlockedStateMatches(
			action.ID,
			action.ExpectedRevision,
			action.ExpectedStatus,
			action.ExpectedIsBlocked,
			Patch{Status: action.Status, MetadataSet: action.MetadataSet, MetadataUnset: action.MetadataUnset},
		); err != nil {
			return result, fmt.Errorf("reconciling blocked status for %q: %w", action.ID, err)
		}
		result.Applied++
	}
	return result, nil
}
