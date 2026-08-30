package beads

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func TestApplyGuardedMetadataClearAppliesWhenGuardMatches(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	bead, err := store.Create(Bead{
		Title: "worktree provenance",
		Metadata: StringMap{
			"gc.worktree_attempt_id": "attempt-1",
			"gc.work_dir":            "/roots/dr-1",
			"gc.work_branch":         "work/dr-1",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	outcome, err := ApplyGuardedMetadataClear(store, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir", "gc.work_branch", "gc.worktree_attempt_id"},
		map[string]string{"gc.worktree_lifecycle": "removed"})
	if err != nil {
		t.Fatalf("ApplyGuardedMetadataClear: %v", err)
	}
	if outcome != GuardedClearApplied {
		t.Fatalf("outcome = %q, want %q", outcome, GuardedClearApplied)
	}

	got, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, k := range []string{"gc.work_dir", "gc.work_branch", "gc.worktree_attempt_id"} {
		if _, present := got.Metadata[k]; present {
			t.Fatalf("metadata[%q] still present after guarded clear: %v", k, got.Metadata)
		}
	}
	if got.Metadata["gc.worktree_lifecycle"] != "removed" {
		t.Fatalf("gc.worktree_lifecycle = %q, want %q", got.Metadata["gc.worktree_lifecycle"], "removed")
	}
}

func TestApplyGuardedMetadataClearSkipsWhenGuardMismatches(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	bead, err := store.Create(Bead{
		Title: "worktree provenance",
		Metadata: StringMap{
			"gc.worktree_attempt_id": "attempt-2",
			"gc.work_dir":            "/roots/dr-1",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A stale caller believes the attempt is still "attempt-1"; a fresh
	// `gc worktree ensure` has already moved it to "attempt-2". This is the
	// dr-mf5u shape dr-y6ndy A1/A3 exist to prevent: the stale caller must
	// not clear the live workspace's pointers.
	outcome, err := ApplyGuardedMetadataClear(store, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir", "gc.worktree_attempt_id"},
		map[string]string{"gc.worktree_lifecycle": "removed"})
	if err != nil {
		t.Fatalf("ApplyGuardedMetadataClear: %v (want nil — a guard mismatch is SKIPPED, not an error)", err)
	}
	if outcome != GuardedClearSkipped {
		t.Fatalf("outcome = %q, want %q", outcome, GuardedClearSkipped)
	}

	got, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata["gc.work_dir"] != "/roots/dr-1" {
		t.Fatalf("gc.work_dir mutated on a skipped clear: %v", got.Metadata)
	}
	if got.Metadata["gc.worktree_attempt_id"] != "attempt-2" {
		t.Fatalf("gc.worktree_attempt_id mutated on a skipped clear: %v", got.Metadata)
	}
	if _, present := got.Metadata["gc.worktree_lifecycle"]; present {
		t.Fatalf("terminal metadata written on a skipped clear: %v", got.Metadata)
	}
}

func TestApplyGuardedMetadataClearRequiresCapability(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	bead, err := backing.Create(Bead{
		Title:    "worktree provenance",
		Metadata: StringMap{"gc.worktree_attempt_id": "attempt-1", "gc.work_dir": "/roots/dr-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store := storeWithoutMetadataCAS{Store: backing}

	_, err = ApplyGuardedMetadataClear(store, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir"}, map[string]string{"gc.worktree_lifecycle": "removed"})
	if !errors.Is(err, ErrConditionalWriteUnsupported) {
		t.Fatalf("ApplyGuardedMetadataClear error = %v, want ErrConditionalWriteUnsupported", err)
	}

	got, err := backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata["gc.work_dir"] != "/roots/dr-1" {
		t.Fatalf("metadata changed through an unconditional fallback: %v", got.Metadata)
	}
}

// TestApplyGuardedMetadataClearSkipUsesLiveListNotGet is the dr-y6ndy A2
// regression test: classifying a guard mismatch must read the live List
// path, never the (possibly cache-served) Get path. A store that panics from
// Get proves the classifier never calls it; List is instrumented separately
// to prove it DOES get called, with Live:true and the target ID, so this
// cannot pass by skipping verification entirely.
func TestApplyGuardedMetadataClearSkipUsesLiveListNotGet(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	bead, err := backing.Create(Bead{
		Title:    "worktree provenance",
		Metadata: StringMap{"gc.worktree_attempt_id": "attempt-2", "gc.work_dir": "/roots/dr-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy := &getForbiddenListSpyStore{Store: backing}

	outcome, err := ApplyGuardedMetadataClear(spy, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir", "gc.worktree_attempt_id"},
		map[string]string{"gc.worktree_lifecycle": "removed"})
	if err != nil {
		t.Fatalf("ApplyGuardedMetadataClear: %v", err)
	}
	if outcome != GuardedClearSkipped {
		t.Fatalf("outcome = %q, want %q", outcome, GuardedClearSkipped)
	}
	if spy.getCalls != 0 {
		t.Fatalf("Get calls = %d, want 0 (classification must use List, never Get)", spy.getCalls)
	}
	if spy.liveListCalls == 0 {
		t.Fatal("live List calls = 0, want at least 1 (classification must verify via a live List read)")
	}
}

func TestApplyGuardedMetadataClearContentionAdmitsExactlyOneWinner(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	bead, err := store.Create(Bead{
		Title:    "worktree provenance",
		Metadata: StringMap{"gc.worktree_attempt_id": "attempt-1", "gc.work_dir": "/roots/dr-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const racers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied []string
		errs    []error
	)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func(racer int) {
			defer wg.Done()
			<-start
			outcome, err := ApplyGuardedMetadataClear(store, bead.ID, "gc.worktree_attempt_id", "attempt-1",
				[]string{"gc.work_dir", "gc.worktree_attempt_id"},
				map[string]string{"gc.worktree_lifecycle": "removed"})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if outcome == GuardedClearApplied {
				applied = append(applied, strconv.Itoa(racer))
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		t.Fatalf("racer returned an error (a lost race must be Skipped, not an error): %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("racers that applied the clear = %d %v, want exactly 1", len(applied), applied)
	}
}

// TestApplyGuardedMetadataClearNeverClearsFreshWorkspace is the direct A3
// proof: a stale finalize racing a fresh `gc worktree ensure` must never
// observe a torn state — either the fresh workspace's provenance survives
// completely intact, or the stale clear lands first and the fresh write
// layers cleanly on top afterward. What must never happen is the fresh
// workspace ending up with SOME but not all of its pointer keys present.
func TestApplyGuardedMetadataClearNeverClearsFreshWorkspace(t *testing.T) {
	t.Parallel()

	for iter := 0; iter < 200; iter++ {
		store := NewMemStore()
		bead, err := store.Create(Bead{
			Title: "worktree provenance",
			Metadata: StringMap{
				"gc.worktree_attempt_id": "attempt-1",
				"gc.work_dir":            "/roots/dr-1",
				"gc.work_branch":         "work/dr-1",
			},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})

		// The stale finalizer: believes the attempt is still "attempt-1".
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = ApplyGuardedMetadataClear(store, bead.ID, "gc.worktree_attempt_id", "attempt-1",
				[]string{"gc.work_dir", "gc.work_branch", "gc.worktree_attempt_id"},
				map[string]string{"gc.worktree_lifecycle": "removed"})
		}()

		// A fresh `gc worktree ensure` re-provisioning the SAME bead.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = store.Update(bead.ID, UpdateOpts{Metadata: map[string]string{
				"gc.worktree_attempt_id": "attempt-2",
				"gc.work_dir":            "/roots/dr-2",
				"gc.work_branch":         "work/dr-2",
			}})
		}()

		close(start)
		wg.Wait()

		got, err := store.Get(bead.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		attempt := got.Metadata["gc.worktree_attempt_id"]
		workDir, hasWorkDir := got.Metadata["gc.work_dir"]
		workBranch, hasWorkBranch := got.Metadata["gc.work_branch"]

		switch attempt {
		case "attempt-2":
			// The fresh write must be COMPLETE: either the stale clear never
			// touched the bead, or it ran first and the fresh write landed
			// cleanly afterward. A torn mix (fresh attempt id with a missing
			// or stale pointer key) is exactly the dr-mf5u race.
			if !hasWorkDir || workDir != "/roots/dr-2" {
				t.Fatalf("iter %d: torn state — attempt=attempt-2 but gc.work_dir=%q present=%v, want /roots/dr-2",
					iter, workDir, hasWorkDir)
			}
			if !hasWorkBranch || workBranch != "work/dr-2" {
				t.Fatalf("iter %d: torn state — attempt=attempt-2 but gc.work_branch=%q present=%v, want work/dr-2",
					iter, workBranch, hasWorkBranch)
			}
		case "":
			// The stale clear landed and nothing re-provisioned it yet in
			// this observation — only valid if the clear ran before ensure.
			if hasWorkDir || hasWorkBranch {
				t.Fatalf("iter %d: torn state — attempt cleared but a pointer key survived (work_dir present=%v, work_branch present=%v)",
					iter, hasWorkDir, hasWorkBranch)
			}
		default:
			t.Fatalf("iter %d: unexpected gc.worktree_attempt_id = %q", iter, attempt)
		}
	}
}

// TestApplyGuardedMetadataClearFailsClosedWhenLiveGuardStillMatches is a
// regression test for a review finding: an earlier implementation performed
// the live List read but ignored its result, treating ANY not-applied writer
// verdict as Skipped. If the live read shows the guard key still equal to
// guardExpected, that contradicts the writer's own "not applied" contract —
// this must fail closed with an error, never be classified as an ordinary
// Skipped result.
func TestApplyGuardedMetadataClearFailsClosedWhenLiveGuardStillMatches(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	bead, err := store.Create(Bead{
		Title:    "worktree provenance",
		Metadata: StringMap{"gc.worktree_attempt_id": "attempt-1", "gc.work_dir": "/roots/dr-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	forced := &forcedWriterVerdictStore{Store: store}

	outcome, err := ApplyGuardedMetadataClear(forced, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir"}, map[string]string{"gc.worktree_lifecycle": "removed"})
	if err == nil {
		t.Fatalf("ApplyGuardedMetadataClear outcome=%q, err=nil; want an error — the live List shows gc.worktree_attempt_id still equals attempt-1 after a not-applied writer result", outcome)
	}
}

// TestApplyGuardedMetadataClearFailsClosedWhenLiveListReturnsNoBead is a
// regression test for the same review finding: a not-applied writer verdict
// whose live List classification comes back empty (the bead cannot be found
// at all) must fail closed rather than being reported as Skipped — Skipped
// promises "the bead exists, its guard just moved on", which an absent bead
// does not satisfy.
func TestApplyGuardedMetadataClearFailsClosedWhenLiveListReturnsNoBead(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	forced := &forcedWriterVerdictStore{Store: store}

	_, err := ApplyGuardedMetadataClear(forced, "does-not-exist", "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir"}, map[string]string{"gc.worktree_lifecycle": "removed"})
	if err == nil {
		t.Fatal("ApplyGuardedMetadataClear err=nil; want an error — live List returned zero beads for the target id")
	}
}

// TestApplyGuardedMetadataClearFailsClosedOnDuplicateLiveListResult covers
// the same finding's "duplicate results" case: a backend whose live List
// returns more than one bead for a single-ID query is broken in a way that
// makes the guard-value comparison meaningless, so it must fail closed too.
func TestApplyGuardedMetadataClearFailsClosedOnDuplicateLiveListResult(t *testing.T) {
	t.Parallel()

	store := NewMemStore()
	bead, err := store.Create(Bead{
		Title:    "worktree provenance",
		Metadata: StringMap{"gc.worktree_attempt_id": "attempt-2", "gc.work_dir": "/roots/dr-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dup := &duplicateLiveListStore{Store: store, dup: bead}

	_, err = ApplyGuardedMetadataClear(dup, bead.ID, "gc.worktree_attempt_id", "attempt-1",
		[]string{"gc.work_dir"}, map[string]string{"gc.worktree_lifecycle": "removed"})
	if err == nil {
		t.Fatal("ApplyGuardedMetadataClear err=nil; want an error — live List returned 2 beads for a single-ID query")
	}
}

// forcedWriterVerdictStore always reports a not-applied write, regardless of
// the store's actual current metadata, to simulate a writer whose false
// result must be cross-checked against (rather than trusted in place of) a
// live List read.
type forcedWriterVerdictStore struct {
	Store
}

func (s *forcedWriterVerdictStore) ClearMetadataIfKeyMatches(_, _, _ string, _ []string, _ map[string]string) (bool, error) {
	return false, nil
}

// duplicateLiveListStore simulates a backend whose live List for a single ID
// returns more than one row.
type duplicateLiveListStore struct {
	Store
	dup Bead
}

func (s *duplicateLiveListStore) ClearMetadataIfKeyMatches(_, _, _ string, _ []string, _ map[string]string) (bool, error) {
	return false, nil
}

func (s *duplicateLiveListStore) List(query ListQuery) ([]Bead, error) {
	if query.Live && len(query.IDs) == 1 {
		return []Bead{s.dup, s.dup}, nil
	}
	return s.Store.List(query)
}

type getForbiddenListSpyStore struct {
	Store
	getCalls      int
	liveListCalls int
}

func (s *getForbiddenListSpyStore) Get(_ string) (Bead, error) {
	s.getCalls++
	panic("guarded-clear classification must not call Get")
}

func (s *getForbiddenListSpyStore) List(query ListQuery) ([]Bead, error) {
	if query.Live && len(query.IDs) == 1 {
		s.liveListCalls++
	}
	return s.Store.List(query)
}

func (s *getForbiddenListSpyStore) ClearMetadataIfKeyMatches(id, guardKey, guardExpected string, clearKeys []string, terminal map[string]string) (bool, error) {
	writer, ok := MetadataGuardedClearerFor(s.Store)
	if !ok {
		return false, ErrConditionalWriteUnsupported
	}
	return writer.ClearMetadataIfKeyMatches(id, guardKey, guardExpected, clearKeys, terminal)
}
