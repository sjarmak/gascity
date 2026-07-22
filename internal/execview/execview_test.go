package execview

import (
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// fakeStore is a minimal beads.Store: only Get, List, and Ready are exercised
// by Build (directly and via sourceworkflow). All other methods are
// unreachable in these tests and would panic on the embedded nil interface,
// which is the intended tripwire if Build starts touching more of the store.
type fakeStore struct {
	beads.Store
	byID  map[string]beads.Bead
	ready []beads.Bead
}

func newFakeStore(bs ...beads.Bead) *fakeStore {
	m := make(map[string]beads.Bead, len(bs))
	for _, b := range bs {
		m[b.ID] = b
	}
	return &fakeStore{byID: m}
}

func (f *fakeStore) Get(id string) (beads.Bead, error) {
	b, ok := f.byID[id]
	if !ok {
		return beads.Bead{}, beads.ErrNotFound
	}
	return b, nil
}

func (f *fakeStore) List(q beads.ListQuery) ([]beads.Bead, error) {
	var out []beads.Bead
	for _, b := range f.byID {
		if !q.IncludesClosed() && b.Status == statusClosed {
			continue
		}
		match := true
		for k, v := range q.Metadata {
			if b.Metadata[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStore) Ready(_ ...beads.ReadyQuery) ([]beads.Bead, error) {
	return f.ready, nil
}

func stores(s ...beads.Store) []beads.Store { return s }

type fakeSessions map[string]SessionView

func (f fakeSessions) Lookup(name string) (SessionView, bool) {
	sv, ok := f[name]
	return sv, ok
}

type fakeProbe map[string]WorktreeView

func (f fakeProbe) Probe(dir, _ string) WorktreeView { return f[dir] }

// recordingProbe captures the baseRef Build passes so the base-ref resolution
// can be asserted end to end.
type recordingProbe struct{ gotBaseRef string }

func (r *recordingProbe) Probe(_, baseRef string) WorktreeView {
	r.gotBaseRef = baseRef
	return WorktreeView{Present: true, IsGit: true}
}

func meta(kv ...string) beads.StringMap {
	m := beads.StringMap{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func graphRoot(id string, m beads.StringMap) beads.Bead {
	m[beadmeta.FormulaContractMetadataKey] = beadmeta.FormulaContractGraphV2
	return beads.Bead{ID: id, Status: statusInProgress, Metadata: m}
}

func step(id, rootID, ref, status, assignee string) beads.Bead {
	return beads.Bead{
		ID:       id,
		Status:   status,
		Assignee: assignee,
		Metadata: meta(beadmeta.RootBeadIDMetadataKey, rootID, beadmeta.StepRefMetadataKey, ref),
	}
}

func hasWarning(p Projection, substr string) bool {
	for _, w := range p.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestBuild_HappyPath_CorrelatesWorkflowStepSessionWorktree(t *testing.T) {
	wt := "/wt/gc-work"
	work := beads.Bead{
		ID:       "gc-work",
		Title:    "do the thing",
		Status:   "open",
		Metadata: meta(beadmeta.WorkDirMetadataKey, wt),
	}
	root := graphRoot("gc-root", meta(
		issueVarMetadataKey, "gc-work",
		beadmeta.FormulaNameMetadataKey, "mol-focus-review",
		beadmeta.SessionNameMetadataKey, "sess-old",
	))
	active := step("gc-step", "gc-root", "mol-focus-review.focus", statusInProgress, "sess-new")
	done := step("gc-done", "gc-root", "mol-focus-review.load", statusClosed, "sess-old")

	store := newFakeStore(work, root, active, done)
	last := time.Unix(1_700_000_000, 0).UTC()
	sessions := fakeSessions{"sess-new": {State: "active", LastActive: &last}}
	probes := fakeProbe{wt: {Present: true, IsGit: true, Branch: "work/gc-work", Head: "abc123", CommitsAhead: 2}}

	p := Build(stores(store), work, sessions, probes)

	if len(p.Workflows) != 1 {
		t.Fatalf("Workflows = %d, want 1: %+v", len(p.Workflows), p.Workflows)
	}
	wf := p.Workflows[0]
	if wf.RootID != "gc-root" || wf.Formula != "mol-focus-review" {
		t.Errorf("workflow = %+v, want root gc-root / mol-focus-review", wf)
	}
	if wf.CurrentStep == nil || wf.CurrentStep.ID != "gc-step" {
		t.Fatalf("CurrentStep = %+v, want gc-step (in_progress)", wf.CurrentStep)
	}
	if wf.CurrentStep.StepRef != "mol-focus-review.focus" || wf.CurrentStep.Assignee != "sess-new" {
		t.Errorf("CurrentStep = %+v, want focus/sess-new", wf.CurrentStep)
	}
	if wf.CurrentStep.Runnable {
		t.Errorf("in-progress step should not be marked Runnable")
	}
	if p.Session == nil || p.Session.Name != "sess-new" || !p.Session.Live || p.Session.State != "active" {
		t.Fatalf("Session = %+v, want live sess-new/active", p.Session)
	}
	if !hasWarning(p, "stale predecessor") {
		t.Errorf("want stale-predecessor warning (root sess-old vs step sess-new); warnings=%v", p.Warnings)
	}
	if p.Worktree == nil || p.Worktree.Path != wt || p.Worktree.Source != beadmeta.WorkDirMetadataKey {
		t.Fatalf("Worktree = %+v, want path %s source gc.work_dir", p.Worktree, wt)
	}
	if p.Worktree.Branch != "work/gc-work" || p.Worktree.CommitsAhead != 2 {
		t.Errorf("Worktree git facts = %+v", p.Worktree)
	}
}

func TestBuild_NoWorkflow(t *testing.T) {
	work := beads.Bead{ID: "gc-solo", Status: "open"}
	p := Build(stores(newFakeStore(work)), work, fakeSessions{}, fakeProbe{})
	if len(p.Workflows) != 0 {
		t.Errorf("Workflows = %d, want 0", len(p.Workflows))
	}
	if p.Session != nil {
		t.Errorf("Session = %+v, want nil (no step, no root session)", p.Session)
	}
	if !hasWarning(p, "no target worktree") {
		t.Errorf("want no-worktree warning; warnings=%v", p.Warnings)
	}
}

func TestBuild_SourceBeadIDPathAlsoCorrelates(t *testing.T) {
	// A root correlated only via gc.source_bead_id (no gc.var.issue) must still
	// be found through the sourceworkflow union path.
	work := beads.Bead{ID: "gc-work", Status: "open"}
	root := graphRoot("gc-root", meta(beadmeta.SourceBeadIDMetadataKey, "gc-work"))
	p := Build(stores(newFakeStore(work, root)), work, fakeSessions{}, fakeProbe{})
	if len(p.Workflows) != 1 || p.Workflows[0].RootID != "gc-root" {
		t.Fatalf("Workflows = %+v, want gc-root via source_bead_id", p.Workflows)
	}
}

func TestBuild_CrossStore_FindsRootInDifferentStore(t *testing.T) {
	// Work bead lives in store A; its workflow root (and steps) live in store B.
	// A single-store scan would report zero workflows — the multi-store search
	// must find them.
	work := beads.Bead{ID: "gc-work", Status: "open"}
	storeA := newFakeStore(work)
	root := graphRoot("gc-xroot", meta(issueVarMetadataKey, "gc-work"))
	active := step("gc-step", "gc-xroot", "x.focus", statusInProgress, "sess-1")
	storeB := newFakeStore(root, active)

	p := Build(stores(storeA, storeB), work, fakeSessions{}, fakeProbe{})
	if len(p.Workflows) != 1 || p.Workflows[0].RootID != "gc-xroot" {
		t.Fatalf("Workflows = %+v, want gc-xroot found in store B", p.Workflows)
	}
	if p.Workflows[0].CurrentStep == nil || p.Workflows[0].CurrentStep.ID != "gc-step" {
		t.Fatalf("CurrentStep = %+v, want gc-step (steps read from root's store)", p.Workflows[0].CurrentStep)
	}
}

func TestBuild_RunnableStepPickedWhenNoneInProgress(t *testing.T) {
	// The common pool-workflow "between steps" state: the next step is open,
	// unclaimed (no assignee), but deps-satisfied/ready. It must surface as the
	// current step, marked Runnable.
	work := beads.Bead{ID: "gc-work", Status: "open"}
	root := graphRoot("gc-root", meta(issueVarMetadataKey, "gc-work"))
	nextStep := step("gc-next", "gc-root", "x.run-tests", "open", "")
	store := newFakeStore(work, root, nextStep)
	store.ready = []beads.Bead{nextStep} // store reports it ready

	p := Build(stores(store), work, fakeSessions{}, fakeProbe{})
	cur := p.Workflows[0].CurrentStep
	if cur == nil || cur.ID != "gc-next" || !cur.Runnable {
		t.Fatalf("CurrentStep = %+v, want runnable gc-next", cur)
	}
}

func TestBuild_OpenUnreadyStepIsNotCurrent(t *testing.T) {
	// An open, unassigned, NOT-ready step (deps unsatisfied) must not be picked.
	work := beads.Bead{ID: "gc-work", Status: "open"}
	root := graphRoot("gc-root", meta(issueVarMetadataKey, "gc-work"))
	blocked := step("gc-blk", "gc-root", "x.finalize", "open", "")
	store := newFakeStore(work, root, blocked) // store.ready stays empty

	p := Build(stores(store), work, fakeSessions{}, fakeProbe{})
	if p.Workflows[0].CurrentStep != nil {
		t.Fatalf("CurrentStep = %+v, want nil (step is open but not ready)", p.Workflows[0].CurrentStep)
	}
}

func TestBuild_MultipleWorkflowsWarns(t *testing.T) {
	work := beads.Bead{ID: "gc-work", Status: "open"}
	r1 := graphRoot("gc-r1", meta(issueVarMetadataKey, "gc-work"))
	r2 := graphRoot("gc-r2", meta(issueVarMetadataKey, "gc-work"))
	p := Build(stores(newFakeStore(work, r1, r2)), work, fakeSessions{}, fakeProbe{})
	if len(p.Workflows) != 2 {
		t.Fatalf("Workflows = %d, want 2", len(p.Workflows))
	}
	if !hasWarning(p, "2 active workflows") {
		t.Errorf("want multiple-workflow warning; warnings=%v", p.Warnings)
	}
}

func TestBuild_WorktreeAbsentWarns(t *testing.T) {
	wt := "/gone"
	work := beads.Bead{ID: "gc-work", Status: "open", Metadata: meta(beadmeta.WorkDirMetadataKey, wt)}
	// fakeProbe returns the zero WorktreeView (Present=false) for an unknown dir.
	p := Build(stores(newFakeStore(work)), work, fakeSessions{}, fakeProbe{})
	if p.Worktree == nil || p.Worktree.Present {
		t.Fatalf("Worktree = %+v, want present=false", p.Worktree)
	}
	if !hasWarning(p, "is absent") {
		t.Errorf("want absent-worktree warning; warnings=%v", p.Warnings)
	}
}

func TestBuild_ReferencedSessionNotLiveWarns(t *testing.T) {
	work := beads.Bead{ID: "gc-work", Status: "open"}
	root := graphRoot("gc-root", meta(issueVarMetadataKey, "gc-work"))
	active := step("gc-step", "gc-root", "x.step", statusInProgress, "sess-dead")
	p := Build(stores(newFakeStore(work, root, active)), work, fakeSessions{}, fakeProbe{})
	if p.Session == nil || p.Session.Live {
		t.Fatalf("Session = %+v, want non-live", p.Session)
	}
	if !hasWarning(p, "is not live") {
		t.Errorf("want not-live warning; warnings=%v", p.Warnings)
	}
}

func TestBuild_BaseRefFlowsToProbe(t *testing.T) {
	// The root's base_branch var must flow through to the worktree probe as the
	// integration base, not a hardcoded origin/main.
	wt := "/wt"
	work := beads.Bead{ID: "gc-work", Status: "open", Metadata: meta(beadmeta.WorkDirMetadataKey, wt)}
	root := graphRoot("gc-root", meta(issueVarMetadataKey, "gc-work", baseBranchVarMetadataKey, "origin/master"))
	rp := &recordingProbe{}
	Build(stores(newFakeStore(work, root)), work, fakeSessions{}, rp)
	if rp.gotBaseRef != "origin/master" {
		t.Errorf("probe base ref = %q, want origin/master (from root base_branch var)", rp.gotBaseRef)
	}

	// The work bead's own base ref wins over the root's.
	work.Metadata[baseRefMetadataKey] = "integration/x"
	rp2 := &recordingProbe{}
	Build(stores(newFakeStore(work, root)), work, fakeSessions{}, rp2)
	if rp2.gotBaseRef != "integration/x" {
		t.Errorf("probe base ref = %q, want integration/x (work bead base_ref wins)", rp2.gotBaseRef)
	}
}

func TestCurrentStep_ExcludesRootAndClosed_PrefersInProgress(t *testing.T) {
	steps := []beads.Bead{
		{ID: "gc-root", Status: statusInProgress}, // the root itself, must be excluded
		step("gc-a", "gc-root", "a", statusClosed, "s1"),
		step("gc-b", "gc-root", "b", "open", "s2"),           // assigned-but-open
		step("gc-c", "gc-root", "c", statusInProgress, "s3"), // the winner
	}
	cur, extra := currentStep(steps, "gc-root", nil)
	if cur == nil || cur.ID != "gc-c" {
		t.Fatalf("currentStep = %+v, want gc-c", cur)
	}
	if extra != 0 {
		t.Errorf("extra = %d, want 0", extra)
	}
}

func TestCurrentStep_MultipleInProgressReportsExtra(t *testing.T) {
	steps := []beads.Bead{
		step("gc-b", "gc-root", "b", statusInProgress, "s2"),
		step("gc-a", "gc-root", "a", statusInProgress, "s1"),
	}
	cur, extra := currentStep(steps, "gc-root", nil)
	if cur == nil || cur.ID != "gc-a" { // lowest id wins
		t.Fatalf("currentStep = %+v, want gc-a", cur)
	}
	if extra != 1 {
		t.Errorf("extra = %d, want 1", extra)
	}
}

func TestWorktreeDir_PrefersCanonicalThenLegacyThenRoot(t *testing.T) {
	work := beads.Bead{Metadata: meta(beadmeta.LegacyWorkDirMetadataKey, "/legacy")}
	root := beads.Bead{Metadata: meta(beadmeta.WorkDirMetadataKey, "/rootwt")}
	dir, src := worktreeDir(work, []beads.Bead{root})
	if dir != "/legacy" || src != beadmeta.LegacyWorkDirMetadataKey {
		t.Errorf("worktreeDir = %s/%s, want /legacy (work bead legacy beats root canonical)", dir, src)
	}

	work2 := beads.Bead{Metadata: meta(beadmeta.WorkDirMetadataKey, "/canon")}
	dir2, src2 := worktreeDir(work2, []beads.Bead{root})
	if dir2 != "/canon" || src2 != beadmeta.WorkDirMetadataKey {
		t.Errorf("worktreeDir = %s/%s, want /canon", dir2, src2)
	}

	dir3, src3 := worktreeDir(beads.Bead{}, []beads.Bead{root})
	if dir3 != "/rootwt" || src3 != beadmeta.WorkDirMetadataKey {
		t.Errorf("worktreeDir = %s/%s, want /rootwt fallback", dir3, src3)
	}
}
