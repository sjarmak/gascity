package temporalmaintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// This file is the gc-4zf.7 OBSERVE-mode bridge: a read-only instrument that
// measures whether the disabled signal-bridge orders
// (orders/temporal-maintenance-signal-*.toml.disabled) are moot or forgotten,
// without mutating Temporal or the bead store. It computes the five Module-8
// Phase-1 metrics plus the two expect-zero counts from the bead's pre-build
// evidence, and emits one JSONL record per run.
//
// HARD INVARIANT: nothing in this file signals, starts, terminates, or updates
// a workflow, and nothing mutates a bead. Precisely: no mutation of the bead
// store, the Temporal namespace, or city state. The gc CLI maintains
// process-local caches and locks of its own (observed: ~/.gc/cache/... lock
// files); those are gc internals, outside the invariant. The bridge holds only
// read interfaces (compile-time), and TestObserveBridge_ReadOnly proves at
// runtime that only read verbs are ever invoked (the allowlist-style gate the
// bead's acceptance requires).

// ObserveWindowDefault is how far back one observe run looks. Three days
// covers the soak history that exists at build time (armed cutover 07-16).
const ObserveWindowDefault = 72 * time.Hour

// ObserveRetentionDefault is the maintenance namespace's execution retention
// (WorkflowExecutionRetentionTtl, measured from workflow CLOSE time): closed
// executions older than this are purged from visibility, so the state diff
// can never bound its covered span earlier than now-retention.
const ObserveRetentionDefault = 24 * time.Hour

// waitingPhases are the phases in which a workflow is parked on a Signal — the
// only regime where the signal bridges could matter. Dispatch-only cycles never
// enter any of them (workflow.go completes straight from Selecting), which is
// the structural core of the moot hypothesis.
var waitingPhases = map[Phase]bool{
	PhaseFanout:         true,
	PhaseAwaitingEvents: true,
	PhaseAwaitingHuman:  true,
}

// realBeadIDRe matches a real store bead id exactly (vs the synthetic
// "temporal-shadow/..." refs a shadow or skipped-inflight selection records).
// Dotted segments are child beads (gc-4zf.3) and are just as real.
var realBeadIDRe = regexp.MustCompile(`^gc-[a-z0-9]+(\.[a-z0-9]+)*$`)

// ExecutionSummary is the read-only slice of a workflow execution the bridge
// consumes: identity, status, lifetime, and history size. It deliberately
// carries no payloads.
type ExecutionSummary struct {
	WorkflowID    string    `json:"workflow_id"`
	RunID         string    `json:"run_id"`
	Status        string    `json:"status"`
	StartTime     time.Time `json:"start_time"`
	CloseTime     time.Time `json:"close_time,omitempty"`
	HistoryLength int64     `json:"history_length"`
}

// ExecutionLister lists the maintenance workflow executions started since a
// cutoff. The real implementation wraps client.Client.ListWorkflow; tests use
// a fake.
type ExecutionLister interface {
	ListExecutions(ctx context.Context, since time.Time) ([]ExecutionSummary, error)
}

// ObserveStateReader is the observe-side state read. Unlike the shared
// WorkflowStateReader it is run-addressed: visibility can retain two runs
// under one Workflow ID (retry, re-schedule), and querying by Workflow ID
// alone would read the latest run twice and miss the other.
type ObserveStateReader interface {
	QueryStateForRun(ctx context.Context, workflowID, runID string) (MaintenanceCycleState, error)
}

// ObservedBead is the read-only slice of a store bead the bridge consumes.
// Metadata is kept raw (decoded JSON values) because the temporal.* contract
// has two plausible shapes — flat dotted keys ("temporal.cycle_key", the shape
// the store actually produces for other stamps) and a nested "temporal" object
// (the shape the disabled bridge's jq assumes) — and the instrument must
// detect either.
type ObservedBead struct {
	ID        string
	Status    string
	Labels    []string
	CreatedAt time.Time
	ClosedAt  time.Time
	Metadata  map[string]any
}

// BeadReader reads maintenance-cycle beads from the store. Both methods are
// list queries; the real implementation shells out to `gc bd ... list` and
// refuses any non-read verb (guardReadArgv).
type BeadReader interface {
	// ListClosedSince returns maintenance-cycle beads closed at/after cutoff.
	ListClosedSince(ctx context.Context, cutoff time.Time) ([]ObservedBead, error)
	// ListOpen returns the currently open maintenance-cycle beads.
	ListOpen(ctx context.Context) ([]ObservedBead, error)
}

// TemporalTagged reports whether a bead carries the temporal.* metadata
// contract the disabled signal orders key on, in either shape (flat dotted
// keys or a nested object). This is the expect-zero probe: per the pre-build
// evidence, the armed selection never stamps these, so no bead should match.
func TemporalTagged(md map[string]any) bool {
	if md == nil {
		return false
	}
	if md["temporal.repo"] != nil && md["temporal.cycle_key"] != nil {
		return true
	}
	if nested, ok := md["temporal"].(map[string]any); ok {
		return nested["repo"] != nil && nested["cycle_key"] != nil
	}
	return false
}

// MissedEventsMetric is spec metric 1: events the disabled signal orders WOULD
// have forwarded had they been enabled — closed maintenance-cycle beads
// carrying the temporal.* contract.
type MissedEventsMetric struct {
	ClosedMaintenanceBeads int      `json:"closed_maintenance_beads"` // candidates scanned in the window
	TaggedForwardable      int      `json:"tagged_forwardable"`       // of those, carrying the temporal.* contract
	TaggedBeadIDs          []string `json:"tagged_bead_ids,omitempty"`
}

// StateDiffMetric is spec metric 2: the diff between what the workflows'
// queryable state records and what the bead store actually holds.
//
// The comparison is bounded to the span workflow visibility actually covers:
// the maintenance namespace retains executions for 24h while the bead window
// defaults to 72h, so a store bead created before the oldest retained
// execution CANNOT be matched — its creating workflow no longer exists. Those
// beads are counted (BeadsOutsideExecutionRetention), never listed as diffs;
// treating a retention artifact as divergence would make the metric cry wolf
// every run.
type StateDiffMetric struct {
	ExecutionsQueried int `json:"executions_queried"`
	QueryFailures     int `json:"query_failures"`
	// Incomplete is set when any state query failed: an unqueryable workflow
	// may record any covered bead, so the store->workflow direction cannot be
	// attributed and its list is left empty rather than reporting false diffs.
	Incomplete bool `json:"incomplete,omitempty"`
	// CoveredFrom is the earliest moment the diff can see both sides of: the
	// oldest retained execution's start, floored at now-retention. Retention
	// is measured from CLOSE time, so a long-running execution visible past
	// the TTL must not stretch coverage over closed workflows already purged.
	CoveredFrom time.Time `json:"covered_from,omitempty"`
	// BeadsOutsideExecutionRetention counts store beads created before
	// CoveredFrom (or all of them when no execution is retained).
	BeadsOutsideExecutionRetention int `json:"beads_outside_execution_retention"`
	// WorkflowBeadsMissingFromStore: real bead ids recorded in workflow state
	// that the store scan (closed-in-window + open) does not contain.
	WorkflowBeadsMissingFromStore []string `json:"workflow_beads_missing_from_store"`
	// StoreBeadsUnknownToWorkflows: maintenance-cycle beads inside the covered
	// span that no queried workflow state records.
	StoreBeadsUnknownToWorkflows []string `json:"store_beads_unknown_to_workflows"`
	// SyntheticSelectionRefs counts branch selection refs that are synthetic
	// ("temporal-shadow/..."). Workflow state cannot distinguish a shadow run
	// from an armed skipped-inflight guard hit — a structural observability gap
	// this instrument surfaces rather than papering over.
	SyntheticSelectionRefs int `json:"synthetic_selection_refs"`
}

// HistoryGrowthMetric is spec metric 3: history event counts per execution.
// Flat numbers per cycle mean the workflow shape is constant; growth across a
// cycle's lifetime (long-poll loops, unbounded signal fans) would show here.
type HistoryGrowthMetric struct {
	Executions   int                `json:"executions"`
	MinEvents    int64              `json:"min_events"`
	MaxEvents    int64              `json:"max_events"`
	MeanEvents   float64            `json:"mean_events"`
	PerExecution []ExecutionSummary `json:"per_execution"`
}

// EventLatencyMetric is spec metric 4: bead event timestamp vs when a workflow
// would learn of it. In dispatch-only there is no waiting workflow, so there
// is nothing to deliver to — the metric reports that structural fact instead
// of inventing a number.
type EventLatencyMetric struct {
	Applicable bool `json:"applicable"`
	// MeasuredPairs is the number of (event, waiting consumer) pairs a latency
	// could be computed over. With the signal orders disabled it is 0 even when
	// a consumer exists, because nothing forwards.
	MeasuredPairs  int    `json:"measured_pairs"`
	StructuralFact string `json:"structural_fact,omitempty"`
}

// DuplicateEventsMetric is spec metric 5: forwardable events already recorded
// in the bridge's dedup state file — the events a naive re-enable would send
// twice.
type DuplicateEventsMetric struct {
	ForwardableEvents int `json:"forwardable_events"`
	AlreadySignalled  int `json:"already_signalled"`
	// StateFileError carries a dedup state file read/parse failure. When set,
	// AlreadySignalled is evidence-free — corrupt dedup state must be
	// distinguishable from an affirmative "never signalled" zero.
	StateFileError string `json:"state_file_error,omitempty"`
}

// ObserveRecord is one JSONL line: the five spec metrics plus the two
// expect-zero counts from the bead notes.
type ObserveRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	WindowHours float64   `json:"window_hours"`

	MissedEvents    MissedEventsMetric    `json:"missed_events"`
	StateDiff       StateDiffMetric       `json:"state_diff"`
	HistoryGrowth   HistoryGrowthMetric   `json:"history_growth"`
	EventLatency    EventLatencyMetric    `json:"event_latency"`
	DuplicateEvents DuplicateEventsMetric `json:"duplicate_events"`

	// The two expect-zero counts (bead gc-4zf.7 notes). Non-zero for either
	// falsifies the moot hypothesis and reopens the enable question.
	BeadsWithTemporalMetadata int `json:"beads_with_temporal_metadata"`
	WorkflowsInWaitingPhase   int `json:"workflows_in_waiting_phase"`

	// Incomplete marks a record whose zeros are not affirmative: a state query
	// failed or the dedup state file was unreadable. Expect-zero tripwires
	// must read such a record as unknown, never as clean.
	Incomplete bool `json:"incomplete"`
}

// ObserveBridge computes the observe-mode metrics. It holds ONLY read
// interfaces: it cannot signal, start, or mutate anything by construction.
type ObserveBridge struct {
	Executions ExecutionLister
	States     ObserveStateReader
	Beads      BeadReader
	// Window bounds the scan (default ObserveWindowDefault).
	Window time.Duration
	// Retention is the namespace's close-time execution retention TTL
	// (default ObserveRetentionDefault); it floors the diff's covered span.
	Retention time.Duration
	// AlreadySignalled is the dedup list from the disabled bridge's state file
	// (.gc/temporal-maintenance-signal-state.json), loaded by the caller; nil
	// means the file was absent (the orders never ran).
	AlreadySignalled []string
	// SignalledStateErr is a read/parse failure from loading that state file.
	// It is surfaced on the record and marks it incomplete — corrupt dedup
	// evidence must not read as a clean 0 duplicates.
	SignalledStateErr error
	// Now overrides the clock in tests; nil means time.Now.
	Now func() time.Time
}

// Observe runs one read-only measurement pass. Per-workflow query failures are
// counted, not fatal (a completed-and-evicted workflow or a briefly missing
// worker must not zero out the run); list failures are fatal to the pass
// because every metric depends on them.
func (b *ObserveBridge) Observe(ctx context.Context) (ObserveRecord, error) {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	window := b.Window
	if window <= 0 {
		window = ObserveWindowDefault
	}
	cutoff := now.Add(-window)

	execs, err := b.Executions.ListExecutions(ctx, cutoff)
	if err != nil {
		return ObserveRecord{}, fmt.Errorf("observe: list executions: %w", err)
	}
	closed, err := b.Beads.ListClosedSince(ctx, cutoff)
	if err != nil {
		return ObserveRecord{}, fmt.Errorf("observe: list closed beads: %w", err)
	}
	open, err := b.Beads.ListOpen(ctx)
	if err != nil {
		return ObserveRecord{}, fmt.Errorf("observe: list open beads: %w", err)
	}

	rec := ObserveRecord{Timestamp: now.UTC(), WindowHours: window.Hours()}
	rec.MissedEvents = missedEvents(closed)
	rec.HistoryGrowth = historyGrowth(execs)
	rec.BeadsWithTemporalMetadata = countTagged(closed) + countTagged(open)

	diff := StateDiffMetric{
		WorkflowBeadsMissingFromStore: []string{},
		StoreBeadsUnknownToWorkflows:  []string{},
	}
	// The diff can only see the span visibility retains (24h here vs the 72h
	// bead window). Two bounds apply: the oldest retained execution's start,
	// and now-retention — retention runs from CLOSE time, so a long-running
	// execution visible past the TTL must not stretch coverage over closed
	// workflows already purged. coveredFrom is the tighter (later) of the two.
	retention := b.Retention
	if retention <= 0 {
		retention = ObserveRetentionDefault
	}
	coveredFrom := time.Time{}
	for _, e := range execs {
		if coveredFrom.IsZero() || e.StartTime.Before(coveredFrom) {
			coveredFrom = e.StartTime
		}
	}
	if floor := now.Add(-retention); !coveredFrom.IsZero() && coveredFrom.Before(floor) {
		coveredFrom = floor
	}
	diff.CoveredFrom = coveredFrom

	storeIDs := map[string]bool{}   // every scanned bead, for the workflow->store direction
	coveredIDs := map[string]bool{} // beads inside the covered span, for store->workflow
	for _, bd := range append(append([]ObservedBead{}, closed...), open...) {
		storeIDs[bd.ID] = true
		if len(execs) > 0 && !bd.CreatedAt.Before(coveredFrom) {
			coveredIDs[bd.ID] = true
		} else {
			diff.BeadsOutsideExecutionRetention++
		}
	}

	// One run-addressed state-query pass feeds the state diff, the
	// waiting-phase count, and the waiting identities the latency metric
	// matches against.
	recorded := map[string]bool{} // real bead ids any workflow state records
	waitingIdentities := map[cycleIdentity]bool{}
	waiting := 0
	for _, e := range execs {
		st, err := b.States.QueryStateForRun(ctx, e.WorkflowID, e.RunID)
		if err != nil {
			// A failed query means this run's beads and phase are unknown —
			// the pass is attributionally incomplete, never affirmatively
			// clean (the expect-zero tripwires must read unknown, not 0).
			diff.QueryFailures++
			diff.Incomplete = true
			continue
		}
		diff.ExecutionsQueried++
		if waitingPhases[st.Phase] {
			waiting++
			waitingIdentities[cycleIdentity{repo: st.Repo, cycleKey: st.CycleKey}] = true
		}
		refs := append([]string{}, st.BeadIDs...)
		for _, br := range st.Branches {
			refs = append(refs, br.SelectionBead, br.WorkBead)
		}
		for _, ref := range refs {
			switch {
			case ref == "":
			case realBeadIDRe.MatchString(ref):
				recorded[ref] = true
				if !storeIDs[ref] {
					diff.WorkflowBeadsMissingFromStore = appendOnce(diff.WorkflowBeadsMissingFromStore, ref)
				}
			case strings.HasPrefix(ref, "temporal-shadow/"):
				diff.SyntheticSelectionRefs++
			}
		}
	}
	// The store->workflow direction is only sound when EVERY state read
	// succeeded: an unqueryable workflow may record any covered bead, so with
	// a failure present the direction reports nothing (Incomplete says why)
	// instead of listing false diffs.
	if diff.QueryFailures == 0 {
		for id := range coveredIDs {
			if !recorded[id] {
				diff.StoreBeadsUnknownToWorkflows = append(diff.StoreBeadsUnknownToWorkflows, id)
			}
		}
	}
	sort.Strings(diff.StoreBeadsUnknownToWorkflows)
	sort.Strings(diff.WorkflowBeadsMissingFromStore)
	rec.StateDiff = diff
	rec.WorkflowsInWaitingPhase = waiting

	rec.EventLatency = eventLatency(waiting, waitingIdentities, closed)
	rec.DuplicateEvents = duplicateEvents(rec.MissedEvents.TaggedBeadIDs, b.AlreadySignalled)
	if b.SignalledStateErr != nil {
		rec.DuplicateEvents.StateFileError = b.SignalledStateErr.Error()
	}
	rec.Incomplete = diff.Incomplete || b.SignalledStateErr != nil
	return rec, nil
}

// missedEvents computes metric 1 over the window's closed beads.
func missedEvents(closed []ObservedBead) MissedEventsMetric {
	m := MissedEventsMetric{ClosedMaintenanceBeads: len(closed)}
	for _, bd := range closed {
		if TemporalTagged(bd.Metadata) {
			m.TaggedForwardable++
			m.TaggedBeadIDs = append(m.TaggedBeadIDs, bd.ID)
		}
	}
	return m
}

// historyGrowth computes metric 3 from the listed executions.
func historyGrowth(execs []ExecutionSummary) HistoryGrowthMetric {
	m := HistoryGrowthMetric{Executions: len(execs), PerExecution: execs}
	if len(execs) == 0 {
		return m
	}
	var total int64
	m.MinEvents = execs[0].HistoryLength
	for _, e := range execs {
		total += e.HistoryLength
		if e.HistoryLength < m.MinEvents {
			m.MinEvents = e.HistoryLength
		}
		if e.HistoryLength > m.MaxEvents {
			m.MaxEvents = e.HistoryLength
		}
	}
	m.MeanEvents = float64(total) / float64(len(execs))
	return m
}

// cycleIdentity is the (repo, cycleKey) pair that names one maintenance cycle
// — the join key between a tagged bead and a waiting workflow's state.
type cycleIdentity struct {
	repo     string
	cycleKey string
}

// temporalTagIdentity extracts the (repo, cycle_key) a tagged bead names, in
// either metadata shape. ok is false when either value is missing, empty, or
// not a string — such a tag can never be matched to a workflow identity.
func temporalTagIdentity(md map[string]any) (id cycleIdentity, ok bool) {
	if md == nil {
		return cycleIdentity{}, false
	}
	if repo, rok := md["temporal.repo"].(string); rok {
		if key, kok := md["temporal.cycle_key"].(string); kok {
			return cycleIdentity{repo: repo, cycleKey: key}, repo != "" && key != ""
		}
	}
	if nested, nok := md["temporal"].(map[string]any); nok {
		repo, rok := nested["repo"].(string)
		key, kok := nested["cycle_key"].(string)
		if rok && kok {
			return cycleIdentity{repo: repo, cycleKey: key}, repo != "" && key != ""
		}
	}
	return cycleIdentity{}, false
}

// eventLatency computes metric 4. A delivery pair is structurally possible
// only when a tagged bead's (repo, cycle_key) names a waiting workflow's
// identity — unrelated aggregates (a tag on one cycle, a waiter on another)
// prove nothing deliverable; otherwise the metric reports why not.
func eventLatency(waiting int, waitingIdentities map[cycleIdentity]bool, closed []ObservedBead) EventLatencyMetric {
	if waiting == 0 {
		return EventLatencyMetric{
			Applicable: false,
			StructuralFact: "dispatch-only cycles complete at selection and never enter a " +
				"signal-waiting phase; there is no consumer for a bead event to reach, so " +
				"end-to-end delivery latency has no referent",
		}
	}
	tagged, matched := 0, 0
	for _, bd := range closed {
		if !TemporalTagged(bd.Metadata) {
			continue
		}
		tagged++
		if id, ok := temporalTagIdentity(bd.Metadata); ok && waitingIdentities[id] {
			matched++
		}
	}
	if tagged == 0 {
		return EventLatencyMetric{
			Applicable: false,
			StructuralFact: "a workflow is waiting but no bead carries the temporal.* " +
				"contract, so no event could be attributed to it",
		}
	}
	if matched == 0 {
		return EventLatencyMetric{
			Applicable: false,
			StructuralFact: "waiting consumers and tagged events coexist, but no tagged " +
				"bead's (repo, cycle_key) matches a waiting workflow's identity, so no " +
				"delivery pair is structurally possible",
		}
	}
	// A tagged event names a waiting consumer's cycle — but with the signal
	// orders disabled nothing forwards, so there are still zero measured
	// deliveries. A future enabled bridge would populate MeasuredPairs from
	// its forwarding log.
	return EventLatencyMetric{
		Applicable:    true,
		MeasuredPairs: 0,
		StructuralFact: "a tagged event names a waiting consumer's cycle; the disabled " +
			"orders forward nothing, so delivery latency is unbounded (never delivered)",
	}
}

// duplicateEvents computes metric 5 against the disabled bridge's dedup list.
func duplicateEvents(tagged, signalled []string) DuplicateEventsMetric {
	seen := map[string]bool{}
	for _, id := range signalled {
		seen[id] = true
	}
	m := DuplicateEventsMetric{ForwardableEvents: len(tagged)}
	for _, id := range tagged {
		if seen[id] {
			m.AlreadySignalled++
		}
	}
	return m
}

func countTagged(beads []ObservedBead) int {
	n := 0
	for _, bd := range beads {
		if TemporalTagged(bd.Metadata) {
			n++
		}
	}
	return n
}

func appendOnce(list []string, id string) []string {
	for _, existing := range list {
		if existing == id {
			return list
		}
	}
	return append(list, id)
}

// ---------------------------------------------------------------------------
// Real adapters (Temporal + bead store), read verbs only.
// ---------------------------------------------------------------------------

// TemporalExecutionLister adapts client.Client.ListWorkflow (a read) to
// ExecutionLister. It lists the namespace without a server-side query —
// the maintenance namespace is dedicated and small — and filters client-side,
// which sidesteps standard-visibility query-attribute quirks.
type TemporalExecutionLister struct {
	Client    client.Client
	Namespace string
}

// maxListPages bounds paging so a runaway namespace can never wedge an
// observe run; 20 pages x 100 = 2000 executions, far beyond the 12/day the
// 120m schedule produces.
const maxListPages = 20

// ListExecutions lists MaintenanceCycleWorkflow executions started at/after
// since. Visibility returns newest-first, so paging stops early once a page
// ends older than the cutoff.
func (l *TemporalExecutionLister) ListExecutions(ctx context.Context, since time.Time) ([]ExecutionSummary, error) {
	var infos []*workflowpb.WorkflowExecutionInfo
	var token []byte
	for page := 0; page < maxListPages; page++ {
		resp, err := l.Client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace:     l.Namespace,
			PageSize:      100,
			NextPageToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("ListWorkflow: %w", err)
		}
		infos = append(infos, resp.Executions...)
		token = resp.NextPageToken
		if len(token) == 0 || pageEndsBefore(resp.Executions, since) {
			break
		}
	}
	return summarizeExecutions(infos, "MaintenanceCycleWorkflow", since), nil
}

// pageEndsBefore reports whether the page's last (oldest, in newest-first
// order) execution started before the cutoff — nothing older is wanted.
func pageEndsBefore(page []*workflowpb.WorkflowExecutionInfo, cutoff time.Time) bool {
	if len(page) == 0 {
		return false
	}
	last := page[len(page)-1]
	return last.GetStartTime() != nil && last.GetStartTime().AsTime().Before(cutoff)
}

// summarizeExecutions filters raw visibility rows to the wanted workflow type
// and window and maps them to summaries. Pure, so it is table-testable without
// a server.
func summarizeExecutions(infos []*workflowpb.WorkflowExecutionInfo, wfType string, since time.Time) []ExecutionSummary {
	out := []ExecutionSummary{}
	for _, info := range infos {
		if info.GetType().GetName() != wfType {
			continue
		}
		start := time.Time{}
		if info.GetStartTime() != nil {
			start = info.GetStartTime().AsTime()
		}
		if start.Before(since) {
			continue
		}
		s := ExecutionSummary{
			WorkflowID:    info.GetExecution().GetWorkflowId(),
			RunID:         info.GetExecution().GetRunId(),
			Status:        executionStatusString(info.GetStatus()),
			StartTime:     start,
			HistoryLength: info.GetHistoryLength(),
		}
		if info.GetCloseTime() != nil {
			s.CloseTime = info.GetCloseTime().AsTime()
		}
		out = append(out, s)
	}
	return out
}

// executionStatusString maps the visibility enum to a stable lowercase token
// (the proto String() form is the SCREAMING_SNAKE constant name, useless in a
// metrics record).
func executionStatusString(s enumspb.WorkflowExecutionStatus) string {
	switch s {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return "running"
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return "completed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return "failed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return "canceled"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return "terminated"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return "continued-as-new"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return "timed-out"
	default:
		return "unknown"
	}
}

// ClientRunStateReader adapts client.Client to ObserveStateReader: the same
// `state` Query (a read) the reconciler's ClientStateReader runs, addressed
// to one specific run so two runs sharing a Workflow ID stay distinct.
type ClientRunStateReader struct {
	c client.Client
}

// NewClientRunStateReader wraps a Temporal client for the observe bridge.
func NewClientRunStateReader(c client.Client) *ClientRunStateReader {
	return &ClientRunStateReader{c: c}
}

// QueryStateForRun runs the workflow's `state` Query against one run.
func (r *ClientRunStateReader) QueryStateForRun(ctx context.Context, workflowID, runID string) (MaintenanceCycleState, error) {
	resp, err := r.c.QueryWorkflow(ctx, workflowID, runID, QueryState)
	if err != nil {
		return MaintenanceCycleState{}, err
	}
	var st MaintenanceCycleState
	if err := resp.Get(&st); err != nil {
		return MaintenanceCycleState{}, err
	}
	return st, nil
}

// beadReadVerbs is the ONLY set of bd subcommands the observe path may run.
var beadReadVerbs = map[string]bool{"list": true, "show": true}

// beadMutationVerbs is a deny-list backstop: none of these tokens may appear
// anywhere in an observe argv, even if the verb slot passes.
var beadMutationVerbs = map[string]bool{
	"create": true, "update": true, "close": true, "delete": true,
	"claim": true, "dep": true, "sling": true, "reopen": true,
}

// guardReadArgv enforces the read-only argv shape for gc bead calls:
// ["bd", "--rig", <rig>, <verb ∈ list|show>, ...] with no mutation token
// anywhere. It fails closed — the observe path refuses to exec rather than
// trusting its own construction.
//
// Scope: the guard proves the ARGUMENT SLICE only. Binary resolution via PATH
// and environment inheritance follow the codebase-wide ExecRunner convention
// (tests rely on PATH shims) — an accepted limitation; the order floor
// controls the execution environment the argv runs in.
func guardReadArgv(args []string) error {
	if len(args) < 4 || args[0] != "bd" || args[1] != "--rig" {
		return fmt.Errorf("observe: argv %v is not a gc bd read call", args)
	}
	if !beadReadVerbs[args[3]] {
		return fmt.Errorf("observe: bd verb %q is not a read verb", args[3])
	}
	for _, a := range args {
		if beadMutationVerbs[a] {
			return fmt.Errorf("observe: argv %v contains mutation token %q", args, a)
		}
	}
	return nil
}

// GCBeadReader is the real BeadReader: it shells out to `gc bd --rig <rig>
// list ... --json`, the same read path the disabled bridge script uses.
type GCBeadReader struct {
	Rig string
	Dir string // working directory for gc (the city root)
	// run overrides the exec transport in tests (recorded argv, canned output).
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewGCBeadReader returns a reader bound to the real gc binary.
func NewGCBeadReader(dir, rig string) *GCBeadReader {
	r := &GCBeadReader{Rig: rig, Dir: dir}
	r.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = r.Dir
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	return r
}

// ListClosedSince implements BeadReader over `gc bd list --status closed`.
func (r *GCBeadReader) ListClosedSince(ctx context.Context, cutoff time.Time) ([]ObservedBead, error) {
	return r.list(ctx, "--status", "closed",
		"--closed-after", cutoff.UTC().Format(time.RFC3339),
		"--label", "maintenance-cycle", "--json")
}

// ListOpen implements BeadReader over `gc bd list --status open`.
func (r *GCBeadReader) ListOpen(ctx context.Context) ([]ObservedBead, error) {
	return r.list(ctx, "--status", "open", "--label", "maintenance-cycle", "--json")
}

func (r *GCBeadReader) list(ctx context.Context, extra ...string) ([]ObservedBead, error) {
	args := append([]string{"bd", "--rig", r.Rig, "list"}, extra...)
	if err := guardReadArgv(args); err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "gc", args...)
	if err != nil {
		return nil, fmt.Errorf("GCBeadReader: %w", err)
	}
	return parseBeadRows(out)
}

// beadRow is the store's JSON row shape (the subset the bridge consumes).
type beadRow struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Labels    []string       `json:"labels"`
	CreatedAt string         `json:"created_at"`
	ClosedAt  string         `json:"closed_at"`
	Metadata  map[string]any `json:"metadata"`
}

// parseBeadRows decodes `gc bd list --json` output. An empty list may arrive
// as "[]", "null", or whitespace, all of which decode to no beads.
func parseBeadRows(out []byte) ([]ObservedBead, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var rows []beadRow
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, fmt.Errorf("parse bead list: %w", err)
	}
	beads := make([]ObservedBead, 0, len(rows))
	for _, row := range rows {
		beads = append(beads, ObservedBead{
			ID:        row.ID,
			Status:    row.Status,
			Labels:    row.Labels,
			CreatedAt: parseBeadTime(row.CreatedAt),
			ClosedAt:  parseBeadTime(row.ClosedAt),
			Metadata:  row.Metadata,
		})
	}
	return beads, nil
}

// parseBeadTime tolerates the store's absent/null timestamps as zero times —
// a missing closed_at on an open bead is normal, not an error.
func parseBeadTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// LoadSignalledState reads the disabled bridge's dedup state file
// (.gc/temporal-maintenance-signal-state.json). An absent file is normal (the
// orders never ran) and yields (nil, nil); an unreadable or malformed file is
// an error the caller must surface on the record — corrupt dedup evidence
// must be distinguishable from an affirmative "never signalled" zero.
func LoadSignalledState(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("signal state %s: %w", path, err)
	}
	var st struct {
		Signalled []string `json:"signalled"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("signal state %s: parse: %w", path, err)
	}
	return st.Signalled, nil
}

// ---------------------------------------------------------------------------
// [durable] config block.
// ---------------------------------------------------------------------------

// DurableModeObserve / DurableModeExecute are the recognized [durable] modes.
// Only observe is implemented; execute fails closed at load time.
const (
	DurableModeObserve = "observe"
	DurableModeExecute = "execute"
)

// DurableConfig is the parsed [durable] block. Enabled gates the FUTURE
// execute bridge only — observe runs are read-only and have nothing to gate,
// so they proceed regardless and record the flag.
type DurableConfig struct {
	Enabled bool
	Mode    string
}

// LoadDurableConfig parses the optional [durable] block from a TOML file
// (city.toml). A missing block yields the defaults (enabled=false,
// mode=observe); a missing file is an error (fail loud — a wrong path must not
// silently read as "defaults"). mode=execute fails closed: the execute path is
// not implemented and is gated behind the memory gate (bead gc-qaid).
//
// The parser is deliberately minimal — one section, two typed keys — because
// this module is standalone (no TOML dependency anywhere in go.mod/go.sum)
// and the block's grammar is fixed. Unknown keys inside [durable] are errors
// so a typo cannot silently no-op; every other section is skipped, with basic
// multiline-string awareness so a string body containing a "[section]"-shaped
// line cannot derail section tracking. Comments are stripped (quote-aware)
// BEFORE both multiline detection and header matching, so `[durable] # note`
// still names the section and a `"""` inside a comment opens nothing.
func LoadDurableConfig(path string) (DurableConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DurableConfig{}, fmt.Errorf("durable config: %w", err)
	}
	cfg := DurableConfig{Enabled: false, Mode: DurableModeObserve}

	inDurable := false
	inString := "" // active multiline-string delimiter ("""" or "'''"), if any
	for lineNo, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if inString != "" {
			if strings.Contains(trimmed, inString) {
				inString = ""
			}
			continue
		}
		// Comment stripping runs first: a `"""` inside a comment is dead text,
		// not a string opener, and a header's trailing comment must not stop
		// the header from matching below.
		trimmed = strings.TrimSpace(stripTOMLComment(trimmed))
		if trimmed == "" {
			continue
		}
		// A line OPENING an unterminated multiline string flips string state.
		for _, delim := range []string{`"""`, "'''"} {
			if strings.Count(trimmed, delim)%2 == 1 {
				inString = delim
			}
		}
		if inString != "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inDurable = trimmed == "[durable]"
			continue
		}
		if !inDurable {
			continue
		}
		key, value, err := parseTOMLKeyValue(trimmed)
		if err != nil {
			return DurableConfig{}, fmt.Errorf("durable config: %s line %d: %w", path, lineNo+1, err)
		}
		switch key {
		case "enabled":
			switch value {
			case "true":
				cfg.Enabled = true
			case "false":
				cfg.Enabled = false
			default:
				return DurableConfig{}, fmt.Errorf("durable config: enabled must be true or false, got %q", value)
			}
		case "mode":
			switch value {
			case DurableModeObserve:
				cfg.Mode = DurableModeObserve
			case DurableModeExecute:
				return DurableConfig{}, fmt.Errorf(
					"durable config: mode=execute is not implemented and is blocked by the "+
						"memory gate (bead gc-qaid): no Temporal execute-path expansion until it closes; "+
						"use mode=%q", DurableModeObserve)
			default:
				return DurableConfig{}, fmt.Errorf("durable config: unrecognized mode %q (want %q)", value, DurableModeObserve)
			}
		default:
			return DurableConfig{}, fmt.Errorf("durable config: unknown key %q in [durable]", key)
		}
	}
	return cfg, nil
}

// parseTOMLKeyValue splits one `key = value` line, stripping an optional
// trailing comment and surrounding quotes on the value.
func parseTOMLKeyValue(line string) (key, value string, err error) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", fmt.Errorf("expected key = value, got %q", line)
	}
	key = strings.TrimSpace(line[:eq])
	value = strings.TrimSpace(line[eq+1:])
	if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
		// Quoted value: take the body up to the matching close quote; anything
		// after (e.g. a trailing comment) is discarded.
		quote := value[0]
		end := strings.IndexByte(value[1:], quote)
		if end < 0 {
			return "", "", fmt.Errorf("unterminated quoted value in %q", line)
		}
		value = value[1 : 1+end]
	} else if i := strings.Index(value, "#"); i >= 0 {
		// Unquoted value: strip a trailing comment.
		value = strings.TrimSpace(value[:i])
	}
	if key == "" || value == "" {
		return "", "", fmt.Errorf("expected key = value, got %q", line)
	}
	return key, value, nil
}

// stripTOMLComment cuts an unquoted trailing `#` comment from one line,
// respecting `#` inside basic ("...", with backslash escapes) and literal
// ('...') strings so a value like "a#b" survives intact. A line that IS a
// comment strips to empty.
func stripTOMLComment(line string) string {
	var quote byte // active quote delimiter, 0 when outside a string
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote == '"' && c == '\\':
			i++ // escaped char inside a basic string
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return line[:i]
		}
	}
	return line
}
