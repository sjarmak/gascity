package molecule

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func mustCreateContinuationLeaseRoot(t *testing.T, store beads.Store) beads.Bead {
	t.Helper()
	b, err := store.Create(beads.Bead{
		Title:  "workflow root",
		Type:   "task",
		Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create root bead: %v", err)
	}
	return b
}

func TestAcquireContinuationLease_FreshAcquire(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha")
	if err != nil {
		t.Fatalf("AcquireContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "session-alpha" {
		t.Fatalf("lease session = %q, want session-alpha", got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
	if got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey] != "cg-1" {
		t.Fatalf("lease group = %q, want cg-1", got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey])
	}
	if got.Metadata[beadmeta.ClaimGenerationMetadataKey] != "1" {
		t.Fatalf("generation = %q, want 1", got.Metadata[beadmeta.ClaimGenerationMetadataKey])
	}
}

// TestAcquireContinuationLease_ConcurrentSessions covers the "concurrent
// sessions" scenario: many sessions race to acquire the same fresh root's
// lease at once. Exactly one must win; every loser must observe a holder
// outcome that is NOT ContinuationLeaseAcquired.
func TestAcquireContinuationLease_ConcurrentSessions(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	const sessions = 12
	var wins int64
	var wg sync.WaitGroup
	wg.Add(sessions)
	for i := 0; i < sessions; i++ {
		go func(n int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+n))
			_, outcome, err := AcquireContinuationLease(store, root.ID, "cg-race", sessionID)
			if err != nil {
				t.Errorf("session %d: AcquireContinuationLease: %v", n, err)
				return
			}
			if outcome == ContinuationLeaseAcquired {
				atomic.AddInt64(&wins, 1)
			} else if outcome != ContinuationLeaseHeldByOther && outcome != ContinuationLeaseStale {
				t.Errorf("session %d: unexpected outcome %q", n, outcome)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins)
	}
	final, err := store.Get(root.ID)
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}
	if final.Metadata[beadmeta.ClaimGenerationMetadataKey] != "1" {
		t.Fatalf("generation = %q, want 1 (only one write should have landed)", final.Metadata[beadmeta.ClaimGenerationMetadataKey])
	}
}

// TestAcquireContinuationLease_LiveHolderCannotBeStolen is the race a bare
// generation fence would miss: a fresh session reads the SAME generation the
// current live holder is sitting on (nothing has moved) and tries to acquire
// anyway. The holder precondition must reject it even though the generation
// matches.
func TestAcquireContinuationLease_LiveHolderCannotBeStolen(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}

	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-beta")
	if err != nil {
		t.Fatalf("AcquireContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseHeldByOther {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseHeldByOther)
	}
	if got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "session-alpha" {
		t.Fatalf("lease was stolen: holder = %q, want session-alpha", got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
}

func TestAcquireContinuationLease_SameSessionRenews(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}
	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("renew outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if got.Metadata[beadmeta.ClaimGenerationMetadataKey] != "2" {
		t.Fatalf("generation after renew = %q, want 2", got.Metadata[beadmeta.ClaimGenerationMetadataKey])
	}
}

// TestAcquireContinuationLease_SameSessionCannotSwitchGroup pins the MEDIUM
// authority gap from the gc-ue0tsw exact-head review: AcquireContinuationLease
// must fence the continuation group the same way it fences the holder. A
// same-session renewal naming a DIFFERENT non-empty group must be rejected
// (ContinuationLeaseHeldByOther) rather than silently overwriting the root's
// live group — a session is only authorized to keep renewing the group it
// actually acquired.
func TestAcquireContinuationLease_SameSessionCannotSwitchGroup(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}

	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-2", "session-alpha")
	if err != nil {
		t.Fatalf("group-switch acquire: %v", err)
	}
	if outcome != ContinuationLeaseHeldByOther {
		t.Fatalf("group-switch outcome = %q, want %q", outcome, ContinuationLeaseHeldByOther)
	}
	if got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey] != "cg-1" {
		t.Fatalf("group was switched: group = %q, want cg-1 (first group must survive)", got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey])
	}
	if got.Metadata[beadmeta.ClaimGenerationMetadataKey] != "1" {
		t.Fatalf("generation after rejected group switch = %q, want 1 (no write should have landed)", got.Metadata[beadmeta.ClaimGenerationMetadataKey])
	}

	// Renewing with the ORIGINAL group must still succeed after the rejected
	// switch attempt — the fence must not have corrupted the lease state.
	got, outcome, err = AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha")
	if err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("renew with original group: outcome=%q err=%v", outcome, err)
	}
	if got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey] != "cg-1" {
		t.Fatalf("group after valid renew = %q, want cg-1", got.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey])
	}
}

// TestContinuationLease_RestartEpoch covers "restart epoch": a session
// releases (e.g. clean shutdown) and later a NEW session (or the same one
// after a restart) re-acquires. The generation must advance across the
// release/reacquire boundary, never reset.
func TestContinuationLease_RestartEpoch(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}
	if _, outcome, err := ReleaseContinuationLease(store, root.ID, "session-alpha"); err != nil || outcome != ContinuationLeaseReleased {
		t.Fatalf("release: outcome=%q err=%v", outcome, err)
	}

	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha-restarted")
	if err != nil {
		t.Fatalf("re-acquire after restart: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("re-acquire outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "session-alpha-restarted" {
		t.Fatalf("holder = %q, want session-alpha-restarted", got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
	if got.Metadata[beadmeta.ClaimGenerationMetadataKey] != "3" {
		t.Fatalf("generation = %q, want 3 (acquire=1, release=2, reacquire=3)", got.Metadata[beadmeta.ClaimGenerationMetadataKey])
	}
}

// TestAcquireContinuationLease_StaleGeneration covers "stale generation": a
// session that read the root before a release+reacquire cycle moved the
// generation forward must not be able to act as if its old view still holds.
// AcquireContinuationLease itself always re-reads, so we exercise staleness
// via ClaimExact directly against the lease keys, mirroring what a caller
// that cached an old generation would attempt.
func TestAcquireContinuationLease_StaleGeneration(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}
	staleGeneration := mustGetBead(t, store, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]

	// A concurrent renewal moves the generation forward.
	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("renew: outcome=%q err=%v", outcome, err)
	}

	// A caller still holding the stale generation tries to act directly via
	// ClaimExact against the lease keys (the same mechanism
	// AcquireContinuationLease uses internally) and must be fenced out.
	sessionAlpha := "session-alpha"
	_, outcome, err := ClaimExact(store, root.ID, ClaimExactPreconditions{
		MetadataEquals: map[string]*string{beadmeta.ContinuationLeaseSessionMetadataKey: &sessionAlpha},
	}, staleGeneration, beads.UpdateOpts{Metadata: map[string]string{beadmeta.ContinuationLeaseSessionMetadataKey: "session-alpha"}})
	if err != nil {
		t.Fatalf("ClaimExact: %v", err)
	}
	if outcome != ClaimExactStale {
		t.Fatalf("outcome = %q, want %q (stale generation must be fenced out)", outcome, ClaimExactStale)
	}
}

// TestAcquireContinuationLease_RootTerminal covers "root terminalization": a
// closed root can never (re)acquire a lease, fresh or renewal.
func TestAcquireContinuationLease_RootTerminal(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)
	if err := store.Update(root.ID, beads.UpdateOpts{Status: strp("closed")}); err != nil {
		t.Fatalf("close root: %v", err)
	}

	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha")
	if err != nil {
		t.Fatalf("AcquireContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseRootTerminal {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseRootTerminal)
	}
	if got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "" {
		t.Fatalf("lease session = %q, want empty (no write on a terminal root)", got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
}

func TestAcquireContinuationLease_RootTerminalizedBetweenReadAndCAS(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)
	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}
	// Terminalize the root without going through ReleaseContinuationLease —
	// modeling a workflow-finalize race landing between AcquireContinuationLease's
	// own read and its internal ClaimExact CAS is impractical to inject
	// deterministically here, so this asserts the equivalent state ClaimExact's
	// Status precondition guards against: a root that is closed by the time
	// the CAS's own fresh read runs must fail as terminal, not as a plain
	// precondition mismatch that a caller could misread as "held by other."
	if err := store.Update(root.ID, beads.UpdateOpts{Status: strp("closed")}); err != nil {
		t.Fatalf("close root: %v", err)
	}
	got, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha")
	if err != nil {
		t.Fatalf("AcquireContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseRootTerminal {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseRootTerminal)
	}
	_ = got
}

func TestAcquireContinuationLease_RequiresAllArguments(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	for _, tc := range []struct {
		name      string
		rootID    string
		group     string
		sessionID string
	}{
		{"empty root", "", "cg-1", "session-alpha"},
		{"empty group", root.ID, "", "session-alpha"},
		{"empty session", root.ID, "cg-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := AcquireContinuationLease(store, tc.rootID, tc.group, tc.sessionID); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestContinuationLeaseHolder(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	holder, err := ContinuationLeaseHolder(store, root.ID)
	if err != nil {
		t.Fatalf("ContinuationLeaseHolder (unheld): %v", err)
	}
	if holder != "" {
		t.Fatalf("holder = %q, want empty on an unheld root", holder)
	}

	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("acquire: outcome=%q err=%v", outcome, err)
	}
	holder, err = ContinuationLeaseHolder(store, root.ID)
	if err != nil {
		t.Fatalf("ContinuationLeaseHolder (held): %v", err)
	}
	if holder != "session-alpha" {
		t.Fatalf("holder = %q, want session-alpha", holder)
	}
}

func TestReleaseContinuationLease_NotHeld(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)

	got, outcome, err := ReleaseContinuationLease(store, root.ID, "")
	if err != nil {
		t.Fatalf("ReleaseContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseNotHeld {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseNotHeld)
	}
	_ = got
}

func TestReleaseContinuationLease_RequireHolderMismatch(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)
	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("acquire: outcome=%q err=%v", outcome, err)
	}

	got, outcome, err := ReleaseContinuationLease(store, root.ID, "session-beta")
	if err != nil {
		t.Fatalf("ReleaseContinuationLease: %v", err)
	}
	if outcome != ContinuationLeaseNotHeld {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseNotHeld)
	}
	if got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "session-alpha" {
		t.Fatalf("lease cleared by a non-holder release attempt: holder = %q", got.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
}

// TestReleaseContinuationLease_ReconcilerRecoversDeadSession covers "crash
// recovery": the reconciler releases a lease WITHOUT knowing/matching the
// holder (requireHolder=""), because it has already proven the holder dead
// through session-liveness checks this function does not re-derive. A live
// session can then rebind the same root.
func TestReleaseContinuationLease_ReconcilerRecoversDeadSession(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateContinuationLeaseRoot(t, store)
	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-dead"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("acquire: outcome=%q err=%v", outcome, err)
	}

	released, outcome, err := ReleaseContinuationLease(store, root.ID, "")
	if err != nil {
		t.Fatalf("reconciler release: %v", err)
	}
	if outcome != ContinuationLeaseReleased {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseReleased)
	}
	if released.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "" {
		t.Fatalf("lease session = %q, want cleared", released.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}

	rebound, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-live-successor")
	if err != nil {
		t.Fatalf("rebind after reconciler release: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("rebind outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if rebound.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] != "session-live-successor" {
		t.Fatalf("holder after rebind = %q, want session-live-successor", rebound.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	}
}

func mustGetBead(t *testing.T, store beads.Store, id string) beads.Bead {
	t.Helper()
	b, err := store.Get(id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return b
}

// mustCreateOpenSibling creates an open, unassigned sibling already stamped
// with rootID and the "cg-1" continuation group — the shape every real
// sibling AssignContinuationFenced is called on actually has, because
// ListContinuation only ever returns beads whose metadata already matches
// this exact root/group pair. Every test in this file exercises the same
// group; a test that needs a sibling to move to a different group mutates it
// after creation via store.Update rather than varying this fixture.
func mustCreateOpenSibling(t *testing.T, store beads.Store, rootID string) beads.Bead {
	t.Helper()
	b, err := store.Create(beads.Bead{
		Title:  "sibling",
		Type:   "task",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey:        rootID,
			beadmeta.ContinuationGroupMetadataKey: "cg-1",
		},
	})
	if err != nil {
		t.Fatalf("create sibling bead: %v", err)
	}
	return b
}

// mustOpenAtomicSQLiteStore opens a real SQLite-backed store rooted at a
// fresh temp dir. AssignContinuationFenced requires
// beads.StoreSupportsAtomicTx, which *beads.MemStore never satisfies (see
// TestAssignContinuationFenced_FailsClosedOnNonAtomicStore) — exercising the
// atomic-Tx path needs a store with genuine transactional isolation between
// its write connection and its read pool.
func mustOpenAtomicSQLiteStore(t *testing.T) *beads.SQLiteStore {
	t.Helper()
	opened, err := beads.OpenSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}
	store := opened.(*beads.SQLiteStore)
	t.Cleanup(func() { _ = store.CloseStore() })
	if !beads.StoreSupportsAtomicTx(store) {
		t.Fatal("SQLite store did not report atomic Tx support")
	}
	return store
}

func TestAssignContinuationFenced_HappyPath(t *testing.T) {
	store := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if got := mustGetBead(t, store, sibling.ID).Assignee; got != "session-alpha" {
		t.Fatalf("sibling assignee = %q, want session-alpha", got)
	}
	if got := mustGetBead(t, store, root.ID).Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]; got != "session-alpha" {
		t.Fatalf("root lease holder = %q, want session-alpha", got)
	}
}

// TestAssignContinuationFenced_NotAuthorized covers the case where the lease
// is already held by a different live session at call time: the sibling must
// never be touched at all.
func TestAssignContinuationFenced_NotAuthorized(t *testing.T) {
	store := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)
	if _, outcome, err := AcquireContinuationLease(store, root.ID, "cg-1", "session-alpha"); err != nil || outcome != ContinuationLeaseAcquired {
		t.Fatalf("initial acquire: outcome=%q err=%v", outcome, err)
	}

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-beta", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseHeldByOther {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseHeldByOther)
	}
	if got := mustGetBead(t, store, sibling.ID).Assignee; got != "" {
		t.Fatalf("sibling assignee = %q, want untouched (empty)", got)
	}
}

// TestAssignContinuationFenced_FailsClosedOnNonAtomicStore pins the
// gc-ue0tsw exact-head review's "fail closed on incapable stores"
// requirement: a store whose Tx cannot guarantee atomic, isolated commit
// (MemStore's sequential, non-transactional Tx — the same shape as
// production's BdStore) must be refused outright, never silently downgraded
// to the unsafe write-then-revert sequence this function used to perform.
// Neither the sibling nor the root's lease metadata may be touched.
func TestAssignContinuationFenced_FailsClosedOnNonAtomicStore(t *testing.T) {
	store := beads.NewMemStore()
	if beads.StoreSupportsAtomicTx(store) {
		t.Fatal("precondition: MemStore must not support atomic Tx")
	}
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err == nil {
		t.Fatal("AssignContinuationFenced: want an error against a non-atomic store, got nil")
	}
	if !errors.Is(err, beads.ErrAtomicTxUnsupported) {
		t.Fatalf("AssignContinuationFenced error = %v, want it to wrap beads.ErrAtomicTxUnsupported", err)
	}
	if outcome != "" {
		t.Fatalf("outcome = %q, want empty on a rejected call", outcome)
	}
	if got := mustGetBead(t, store, sibling.ID).Assignee; got != "" {
		t.Fatalf("sibling assignee = %q, want untouched (empty): a store lacking atomic Tx must never be written to", got)
	}
	if got := mustGetBead(t, store, root.ID).Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]; got != "" {
		t.Fatalf("root lease holder = %q, want untouched (empty)", got)
	}
}

// interposeDuringTxStore wraps a *beads.SQLiteStore and runs `during` after
// the wrapped Tx callback finishes writing but strictly before the
// transaction commits: SQLiteStore.Tx commits only after the callback passed
// to it returns, and this wrapper's own callback (which runs AssignContinuationFenced's
// writes via fn) returns to SQLiteStore.Tx only after `during` has already run
// and returned. Embedding the concrete *beads.SQLiteStore (not the
// beads.Store interface) promotes AtomicTx() straight through, so the
// wrapper still reports atomic Tx support.
type interposeDuringTxStore struct {
	*beads.SQLiteStore
	during func()
}

func (s *interposeDuringTxStore) Tx(commitMsg string, fn func(tx beads.Tx) error) error {
	return s.SQLiteStore.Tx(commitMsg, func(tx beads.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if s.during != nil {
			s.during()
		}
		return nil
	})
}

// TestAssignContinuationFenced_SiblingAssignmentNeverObservedBeforeCommit is
// the adversarial consumer-observation regression the gc-ue0tsw exact-head
// review required: a concurrent reader using a genuinely separate connection
// (SQLiteStore.Get reads through the read pool; the in-flight transaction
// holds the single write connection) must never see the sibling assignment,
// or the root's advanced lease generation, while AssignContinuationFenced's
// transaction is still open. Only after the call returns may either become
// visible. This proves the fix: the old design made the sibling write
// visible via an unconditional store.Update before the compensating revert,
// so a concurrent consumer could observe and act under authority that had
// already been (or was about to be) revoked. The new design has no such
// window because both the precondition check and both writes commit as one
// atomic unit.
func TestAssignContinuationFenced_SiblingAssignmentNeverObservedBeforeCommit(t *testing.T) {
	base := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, base)
	sibling := mustCreateOpenSibling(t, base, root.ID)
	originalGeneration := mustGetBead(t, base, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]

	store := &interposeDuringTxStore{SQLiteStore: base}
	observedDuringTx := false
	store.during = func() {
		observedDuringTx = true
		if got := mustGetBead(t, base, sibling.ID).Assignee; got != "" {
			t.Errorf("concurrent read mid-transaction: sibling assignee = %q, want untouched (empty) before commit", got)
		}
		if got := mustGetBead(t, base, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]; got != originalGeneration {
			t.Errorf("concurrent read mid-transaction: root claim generation = %q, want unchanged %q before commit", got, originalGeneration)
		}
	}

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseAcquired {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseAcquired)
	}
	if !observedDuringTx {
		t.Fatal("interposeDuringTxStore.during was never invoked: test did not actually exercise the mid-transaction window")
	}
	if got := mustGetBead(t, base, sibling.ID).Assignee; got != "session-alpha" {
		t.Fatalf("post-commit sibling assignee = %q, want session-alpha", got)
	}
}

func strPtr(s string) *string { return &s }

// TestAssignContinuationFenced_SiblingAssignedBetweenListAndAssign pins the
// first gc-ue0tsw round-3 HIGH finding: preassignHookContinuationGroup's
// eligibility check runs against a point-in-time ListContinuation snapshot,
// and another claimant can assign the sibling in the gap between that
// snapshot and this call. The transaction must re-read the sibling and
// refuse rather than steal it back from whoever holds it now.
func TestAssignContinuationFenced_SiblingAssignedBetweenListAndAssign(t *testing.T) {
	store := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)
	originalGeneration := mustGetBead(t, store, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]

	// Simulates a second claimant winning the sibling after this session's
	// ListContinuation snapshot was taken but before its AssignContinuationFenced call.
	if err := store.Update(sibling.ID, beads.UpdateOpts{Assignee: strPtr("someone-else")}); err != nil {
		t.Fatalf("Update sibling assignee: %v", err)
	}

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseSiblingUnavailable {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseSiblingUnavailable)
	}
	if got := mustGetBead(t, store, sibling.ID).Assignee; got != "someone-else" {
		t.Fatalf("sibling assignee = %q, want untouched (someone-else)", got)
	}
	root2 := mustGetBead(t, store, root.ID)
	if got := root2.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]; got != "" {
		t.Fatalf("root lease holder = %q, want untouched (empty): a losing precondition must not grant the lease", got)
	}
	if got := root2.Metadata[beadmeta.ClaimGenerationMetadataKey]; got != originalGeneration {
		t.Fatalf("root claim generation = %q, want unchanged %q", got, originalGeneration)
	}
}

// TestAssignContinuationFenced_SiblingClosedBetweenListAndAssign pins the
// same HIGH finding for the "sibling closed" adversarial case named in the
// review.
func TestAssignContinuationFenced_SiblingClosedBetweenListAndAssign(t *testing.T) {
	store := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)
	originalGeneration := mustGetBead(t, store, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]

	if err := store.Close(sibling.ID); err != nil {
		t.Fatalf("Close sibling: %v", err)
	}

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseSiblingUnavailable {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseSiblingUnavailable)
	}
	closed := mustGetBead(t, store, sibling.ID)
	if closed.Status != "closed" {
		t.Fatalf("sibling status = %q, want untouched (closed)", closed.Status)
	}
	if closed.Assignee != "" {
		t.Fatalf("sibling assignee = %q, want untouched (empty)", closed.Assignee)
	}
	root2 := mustGetBead(t, store, root.ID)
	if got := root2.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]; got != "" {
		t.Fatalf("root lease holder = %q, want untouched (empty): a losing precondition must not grant the lease", got)
	}
	if got := root2.Metadata[beadmeta.ClaimGenerationMetadataKey]; got != originalGeneration {
		t.Fatalf("root claim generation = %q, want unchanged %q", got, originalGeneration)
	}
}

// TestAssignContinuationFenced_SiblingMovedToDifferentGroupBetweenListAndAssign
// pins the "moved to a different continuation group" adversarial case named
// in the review: applySQLiteUpdateOpts merges Metadata key-by-key
// (internal/beads/sqlite_store.go), so this update only overwrites
// ContinuationGroupMetadataKey and leaves RootBeadIDMetadataKey intact,
// isolating the group-mismatch branch from the root-mismatch branch.
func TestAssignContinuationFenced_SiblingMovedToDifferentGroupBetweenListAndAssign(t *testing.T) {
	store := mustOpenAtomicSQLiteStore(t)
	root := mustCreateContinuationLeaseRoot(t, store)
	sibling := mustCreateOpenSibling(t, store, root.ID)
	originalGeneration := mustGetBead(t, store, root.ID).Metadata[beadmeta.ClaimGenerationMetadataKey]

	if err := store.Update(sibling.ID, beads.UpdateOpts{
		Metadata: map[string]string{beadmeta.ContinuationGroupMetadataKey: "cg-2"},
	}); err != nil {
		t.Fatalf("Update sibling group: %v", err)
	}
	moved := mustGetBead(t, store, sibling.ID)
	if got := moved.Metadata[beadmeta.RootBeadIDMetadataKey]; got != root.ID {
		t.Fatalf("precondition: sibling root metadata = %q, want unchanged %q after the merge update", got, root.ID)
	}

	outcome, err := AssignContinuationFenced(store, root.ID, "cg-1", "session-alpha", sibling.ID)
	if err != nil {
		t.Fatalf("AssignContinuationFenced: %v", err)
	}
	if outcome != ContinuationLeaseSiblingUnavailable {
		t.Fatalf("outcome = %q, want %q", outcome, ContinuationLeaseSiblingUnavailable)
	}
	after := mustGetBead(t, store, sibling.ID)
	if got := after.Metadata[beadmeta.ContinuationGroupMetadataKey]; got != "cg-2" {
		t.Fatalf("sibling group = %q, want untouched (cg-2)", got)
	}
	if after.Assignee != "" {
		t.Fatalf("sibling assignee = %q, want untouched (empty)", after.Assignee)
	}
	root2 := mustGetBead(t, store, root.ID)
	if got := root2.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]; got != "" {
		t.Fatalf("root lease holder = %q, want untouched (empty): a losing precondition must not grant the lease", got)
	}
	if got := root2.Metadata[beadmeta.ClaimGenerationMetadataKey]; got != originalGeneration {
		t.Fatalf("root claim generation = %q, want unchanged %q", got, originalGeneration)
	}
}
