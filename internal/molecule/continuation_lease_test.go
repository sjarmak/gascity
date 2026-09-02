package molecule

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func mustCreateContinuationLeaseRoot(t *testing.T, store *beads.MemStore) beads.Bead {
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
