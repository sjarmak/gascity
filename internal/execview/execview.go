// Package execview builds a read-only execution projection for a work bead:
// it correlates the bead with the active graph workflow(s) that reference it,
// the current runnable/assigned step and its assignee, the matching live
// session, and the target worktree's git state. The projection is a typed,
// rendering-independent value shared by the CLI and (later) the supervisor
// API. Build performs only targeted, read-only bead-store queries plus caller-
// injected session and worktree lookups; it never mutates beads, sessions,
// worktrees, or refs, and it emits warnings rather than guessing when the
// underlying metadata is ambiguous or inconsistent.
//
// Build searches every provided store rather than a single one: a graph
// workflow root and its source (work) bead can legitimately live in different
// stores (a rig-scoped run of a city-resident work bead), so a single-store
// scan would silently report zero workflows.
package execview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/sourceworkflow"
)

// issueVarMetadataKey is the metadata key a formula stamps on a graph workflow
// root to name the work bead it operates on (the formula variable "issue").
// It is the operator-facing correlation key: on live graph.v2 roots such as
// mol-focus-review it is present as a discrete metadata entry (e.g.
// "gc.var.issue" = "gc-im90"), distinct from the durable gc.source_bead_id
// index used by the source-workflow singleton machinery.
const issueVarMetadataKey = beadmeta.FormulaVarPrefix + "issue"

// baseBranchVarMetadataKey is the formula var recording a workflow's
// integration base branch (e.g. "origin/main"); baseRefMetadataKey is the
// work bead's own recorded base ref. Neither has a dedicated engine constant
// (both are formula/pack-supplied), so they are composed from the namespace.
const (
	baseBranchVarMetadataKey = beadmeta.FormulaVarPrefix + "base_branch"
	baseRefMetadataKey       = beadmeta.Namespace + "base_ref"
)

const (
	statusInProgress = "in_progress"
	statusClosed     = "closed"
)

// Projection is the read-only execution projection for a work bead. It is the
// single typed shape rendered as compact text and as JSON. It reflects live
// store state (a direct multi-store read), not the reconciler's cached API
// view, so it can be fresher than a plain `gc beads show`.
type Projection struct {
	WorkBead  WorkBeadView   `json:"work_bead"`
	Workflows []WorkflowView `json:"workflows,omitempty"`
	Session   *SessionView   `json:"session,omitempty"`
	Worktree  *WorktreeView  `json:"worktree,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// WorkBeadView is the requested work bead's own identity and status.
type WorkBeadView struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status"`
	Priority *int   `json:"priority,omitempty"`
	Assignee string `json:"assignee,omitempty"`
}

// WorkflowView is one active graph workflow correlated to the work bead.
type WorkflowView struct {
	RootID string `json:"root_id"`
	// Formula is the workflow's formula name (gc.formula_name), when recorded.
	Formula string `json:"formula,omitempty"`
	Status  string `json:"status"`
	// CurrentStep is the current in-progress (or otherwise assigned/runnable)
	// child step, or nil when no step is currently active.
	CurrentStep *StepView `json:"current_step,omitempty"`
	// RootSession is the session name recorded on the root (gc.session_name).
	// It can name a stale predecessor; compare against CurrentStep.Assignee.
	RootSession string `json:"root_session,omitempty"`
}

// StepView is a single workflow step bead.
type StepView struct {
	ID       string `json:"id"`
	StepRef  string `json:"step_ref,omitempty"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"`
	// Runnable reports that the step is deps-satisfied and ready to claim (its
	// status is open with no assignee yet); distinguishes a pool-routed step
	// awaiting a slot from an actively in-progress one.
	Runnable bool `json:"runnable,omitempty"`
}

// SessionView is the live runtime state of the session executing the work,
// resolved from the current step's assignee (or the root's recorded session).
type SessionView struct {
	Name string `json:"name"`
	// State is the live session state (e.g. "active"); empty when not live.
	State string `json:"state,omitempty"`
	// LastActive is the session's last observed activity; nil when not live.
	LastActive *time.Time `json:"last_active,omitempty"`
	// Live reports whether a live session by this name currently exists.
	Live bool `json:"live"`
}

// WorktreeView is the git state of the work bead's target worktree.
type WorktreeView struct {
	Path string `json:"path"`
	// Source is the metadata key the path was read from: the canonical
	// "gc.work_dir" or the legacy "work_dir".
	Source string `json:"source"`
	// Present reports whether the directory exists on disk.
	Present bool `json:"present"`
	// IsGit reports whether the directory is inside a git repository.
	IsGit  bool   `json:"is_git"`
	Branch string `json:"branch,omitempty"`
	// Dirty reports uncommitted changes (tracked or untracked).
	Dirty bool   `json:"dirty"`
	Head  string `json:"head,omitempty"`
	// CommitsAhead is the number of commits on Head not reachable from the
	// integration base; -1 when it could not be computed.
	CommitsAhead int `json:"commits_ahead"`
	// ReachableFromMain reports whether Head is an ancestor of the integration
	// base (i.e. the work is already merged). "Main" is the resolved base:
	// the bead's recorded base ref, else the repo's default branch.
	ReachableFromMain bool `json:"reachable_from_main"`
	// Err carries a non-fatal probe error, surfaced as a projection warning.
	Err string `json:"err,omitempty"`
}

// SessionLookup resolves a session name to its current live runtime state.
// Lookup returns ok=false when no live session by that name exists.
// Implementations must be read-only.
type SessionLookup interface {
	Lookup(name string) (SessionView, bool)
}

// WorktreeProbe inspects a target worktree directory and reports its git state
// relative to baseRef (the integration base; empty means "let the probe pick
// the repo default branch"). Implementations must not mutate the worktree.
type WorktreeProbe interface {
	Probe(dir, baseRef string) WorktreeView
}

// rootRef pairs a workflow root with the store it was read from, so step and
// ready queries for that workflow run against the store that holds it.
type rootRef struct {
	bead  beads.Bead
	store beads.Store
}

// Build assembles the execution projection for workBead. stores is searched
// for the correlated workflow(s) and their steps. sessions and worktrees are
// caller-injected read-only lookups; either may be nil, in which case the
// corresponding section is omitted with a warning. Build never mutates state.
func Build(stores []beads.Store, workBead beads.Bead, sessions SessionLookup, worktrees WorktreeProbe) Projection {
	p := Projection{
		WorkBead: WorkBeadView{
			ID:       workBead.ID,
			Title:    workBead.Title,
			Status:   workBead.Status,
			Priority: workBead.Priority,
			Assignee: workBead.Assignee,
		},
	}

	roots := p.findWorkflowRoots(stores, workBead.ID)
	if len(roots) > 1 {
		p.warn(fmt.Sprintf("%d active workflows reference this work bead; showing all", len(roots)))
	}

	var stepSession, rootSession string
	rootBeads := make([]beads.Bead, 0, len(roots))
	for _, r := range roots {
		root := r.bead
		rootBeads = append(rootBeads, root)
		wf := WorkflowView{
			RootID:      root.ID,
			Formula:     root.Metadata[beadmeta.FormulaNameMetadataKey],
			Status:      root.Status,
			RootSession: strings.TrimSpace(root.Metadata[beadmeta.SessionNameMetadataKey]),
		}
		steps := p.workflowSteps(r.store, root.ID)
		ready := p.readySteps(r.store, root.ID)
		cur, extraInProgress := currentStep(steps, root.ID, ready)
		wf.CurrentStep = cur
		if extraInProgress > 0 {
			p.warn(fmt.Sprintf("workflow %s has %d concurrently in-progress steps; showing lowest id", root.ID, extraInProgress+1))
		}
		if cur != nil && stepSession == "" && cur.Assignee != "" {
			stepSession = cur.Assignee
		}
		if rootSession == "" && wf.RootSession != "" {
			rootSession = wf.RootSession
		}
		p.Workflows = append(p.Workflows, wf)
	}

	p.Session = p.resolveSession(sessions, stepSession, rootSession)
	p.Worktree = p.resolveWorktree(worktrees, workBead, rootBeads)
	return p
}

// findWorkflowRoots returns the live workflow roots correlated to workBeadID
// across all stores, unioning the operator-facing gc.var.issue key with the
// durable gc.source_bead_id index so both formula-var and source-workflow
// launch paths are covered. Results are deduped by ID, exclude closed/non-root
// beads, and are sorted for stable output. Each root is paired with its store.
func (p *Projection) findWorkflowRoots(stores []beads.Store, workBeadID string) []rootRef {
	workBeadID = strings.TrimSpace(workBeadID)
	if workBeadID == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []rootRef
	add := func(b beads.Bead, store beads.Store) {
		if seen[b.ID] || b.Status == statusClosed || !sourceworkflow.IsWorkflowRoot(b) {
			return
		}
		seen[b.ID] = true
		out = append(out, rootRef{bead: b, store: store})
	}

	for _, store := range stores {
		if store == nil {
			continue
		}
		byVar, err := beads.HandlesFor(store).Live.List(beads.ListQuery{
			Metadata: map[string]string{issueVarMetadataKey: workBeadID},
		})
		if err != nil {
			p.warn(fmt.Sprintf("workflow lookup by %s failed: %v", issueVarMetadataKey, err))
		}
		for _, b := range byVar {
			add(b, store)
		}

		roots, err := sourceworkflow.ListLiveRoots(store, workBeadID, "", "")
		if err != nil {
			p.warn(fmt.Sprintf("workflow lookup by source bead id failed: %v", err))
		}
		for _, b := range roots {
			add(b, store)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].bead.ID < out[j].bead.ID })
	return out
}

// workflowSteps returns every bead in the workflow subtree (root plus
// descendants, including closed) for rootID.
func (p *Projection) workflowSteps(store beads.Store, rootID string) []beads.Bead {
	steps, err := sourceworkflow.ListWorkflowBeads(store, rootID)
	if err != nil {
		p.warn(fmt.Sprintf("listing steps for workflow %s failed: %v", rootID, err))
		return nil
	}
	return steps
}

// readySteps returns the IDs of steps in the workflow subtree that the store
// reports as runnable (dependencies satisfied, ready to claim). It filters the
// store's global ready set to this root's subtree via gc.root_bead_id.
func (p *Projection) readySteps(store beads.Store, rootID string) map[string]bool {
	ready, err := store.Ready()
	if err != nil {
		p.warn(fmt.Sprintf("ready-step lookup for workflow %s failed: %v", rootID, err))
		return nil
	}
	ids := make(map[string]bool)
	for _, b := range ready {
		if b.Metadata[beadmeta.RootBeadIDMetadataKey] == rootID {
			ids[b.ID] = true
		}
	}
	return ids
}

// currentStep selects the current step from a workflow subtree and reports how
// many additional concurrently in-progress steps were suppressed. The root
// bead and closed steps are excluded. Selection tiers, highest first: an
// in-progress (actively worked) step; a claimed-but-not-yet-running assigned
// step; a runnable (ready, unclaimed) step. Ties break on lowest ID. Returns
// (nil, 0) when no step is currently active.
func currentStep(steps []beads.Bead, rootID string, ready map[string]bool) (*StepView, int) {
	var inProgress, assigned, runnable []beads.Bead
	for _, s := range steps {
		if s.ID == rootID || s.Status == statusClosed {
			continue
		}
		switch {
		case s.Status == statusInProgress:
			inProgress = append(inProgress, s)
		case strings.TrimSpace(s.Assignee) != "":
			assigned = append(assigned, s)
		case ready[s.ID]:
			runnable = append(runnable, s)
		}
	}
	if len(inProgress) > 0 {
		sortByID(inProgress)
		sv := stepView(inProgress[0], false)
		return &sv, len(inProgress) - 1
	}
	if len(assigned) > 0 {
		sortByID(assigned)
		sv := stepView(assigned[0], false)
		return &sv, 0
	}
	if len(runnable) > 0 {
		sortByID(runnable)
		sv := stepView(runnable[0], true)
		return &sv, 0
	}
	return nil, 0
}

func sortByID(bs []beads.Bead) {
	sort.Slice(bs, func(i, j int) bool { return bs[i].ID < bs[j].ID })
}

func stepView(b beads.Bead, runnable bool) StepView {
	return StepView{
		ID:       b.ID,
		StepRef:  b.Metadata[beadmeta.StepRefMetadataKey],
		Status:   b.Status,
		Assignee: strings.TrimSpace(b.Assignee),
		Runnable: runnable,
	}
}

// resolveSession resolves the session executing the work. The current step's
// assignee is authoritative; the root's recorded session is a fallback that
// may name a stale predecessor. A live lookup failure or absence is surfaced
// as a warning rather than omitted silently.
func (p *Projection) resolveSession(sessions SessionLookup, stepSession, rootSession string) *SessionView {
	name := stepSession
	if name == "" {
		name = rootSession
	}
	if name == "" {
		return nil
	}
	if stepSession != "" && rootSession != "" && stepSession != rootSession {
		p.warn(fmt.Sprintf("root names session %q but the active step is assigned to %q (possible stale predecessor)", rootSession, stepSession))
	}
	if sessions == nil {
		p.warn("live session state unavailable")
		return &SessionView{Name: name}
	}
	sv, ok := sessions.Lookup(name)
	if !ok {
		p.warn(fmt.Sprintf("referenced session %q is not live", name))
		return &SessionView{Name: name, Live: false}
	}
	sv.Name = name
	sv.Live = true
	return &sv
}

// resolveWorktree resolves and probes the work bead's target worktree. The
// canonical gc.work_dir is preferred over the legacy work_dir; the work bead's
// own metadata wins over the workflow root's. Absent, non-git, or probe-error
// states become warnings.
func (p *Projection) resolveWorktree(worktrees WorktreeProbe, workBead beads.Bead, roots []beads.Bead) *WorktreeView {
	p.warnConflictingWorkDir(workBead)
	dir, source := worktreeDir(workBead, roots)
	if dir == "" {
		p.warn("no target worktree recorded (gc.work_dir / work_dir absent)")
		return nil
	}
	baseRef := integrationBaseRef(workBead, roots)
	if worktrees == nil {
		p.warn("worktree git state unavailable")
		return &WorktreeView{Path: dir, Source: source, CommitsAhead: -1}
	}
	wv := worktrees.Probe(dir, baseRef)
	wv.Path = dir
	wv.Source = source
	switch {
	case !wv.Present:
		p.warn(fmt.Sprintf("target worktree %s is absent", dir))
	case !wv.IsGit:
		p.warn(fmt.Sprintf("target worktree %s is not a git repository", dir))
	}
	if wv.Err != "" {
		p.warn(fmt.Sprintf("worktree probe error for %s: %s", dir, wv.Err))
	}
	return &wv
}

// warnConflictingWorkDir flags a work bead that records both the canonical and
// legacy work-dir keys with different values on itself.
func (p *Projection) warnConflictingWorkDir(workBead beads.Bead) {
	canonical := strings.TrimSpace(workBead.Metadata[beadmeta.WorkDirMetadataKey])
	legacy := strings.TrimSpace(workBead.Metadata[beadmeta.LegacyWorkDirMetadataKey])
	if canonical != "" && legacy != "" && canonical != legacy {
		p.warn(fmt.Sprintf("conflicting work-dir metadata: %s=%s vs %s=%s",
			beadmeta.WorkDirMetadataKey, canonical, beadmeta.LegacyWorkDirMetadataKey, legacy))
	}
}

// worktreeDir returns the target worktree path and the metadata key it came
// from, preferring the work bead's canonical key, then its legacy key, then
// the workflow root's keys.
func worktreeDir(workBead beads.Bead, roots []beads.Bead) (string, string) {
	if d := strings.TrimSpace(workBead.Metadata[beadmeta.WorkDirMetadataKey]); d != "" {
		return d, beadmeta.WorkDirMetadataKey
	}
	if d := strings.TrimSpace(workBead.Metadata[beadmeta.LegacyWorkDirMetadataKey]); d != "" {
		return d, beadmeta.LegacyWorkDirMetadataKey
	}
	for _, root := range roots {
		if d := strings.TrimSpace(root.Metadata[beadmeta.WorkDirMetadataKey]); d != "" {
			return d, beadmeta.WorkDirMetadataKey
		}
		if d := strings.TrimSpace(root.Metadata[beadmeta.LegacyWorkDirMetadataKey]); d != "" {
			return d, beadmeta.LegacyWorkDirMetadataKey
		}
	}
	return "", ""
}

// integrationBaseRef returns the base ref against which worktree ahead/merge
// state is measured, preferring the work bead's recorded base ref, then the
// workflow root's base_branch var. Empty means the probe should fall back to
// the repository's default branch.
func integrationBaseRef(workBead beads.Bead, roots []beads.Bead) string {
	if b := strings.TrimSpace(workBead.Metadata[baseRefMetadataKey]); b != "" {
		return b
	}
	for _, root := range roots {
		if b := strings.TrimSpace(root.Metadata[baseBranchVarMetadataKey]); b != "" {
			return b
		}
	}
	return ""
}

func (p *Projection) warn(msg string) {
	p.Warnings = append(p.Warnings, msg)
}
