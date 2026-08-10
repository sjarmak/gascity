package main

import (
	"context"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// getCountingStore counts store.Get calls, delegating everything else. It guards
// the WI-5 tick-budget invariant: bd ops cost ~2s under Dolt, and the
// write-returns-Info cutover (ApplyPatchInfo) must add ZERO Gets — a patch write
// plus a LOCAL fold, never a re-Get. A future "convenient re-Get" on the
// reconciler fast path bumps this count and fails CI.
type getCountingStore struct {
	beads.Store
	mu               sync.Mutex
	gets             int
	lists            int
	liveSessionLists int
}

func (s *getCountingStore) Get(id string) (beads.Bead, error) {
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	return s.Store.Get(id)
}

func (s *getCountingStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.mu.Lock()
	s.lists++
	if query.Live && (query.Type == sessionpkg.BeadType || query.Label == sessionpkg.LabelSession) {
		s.liveSessionLists++
	}
	s.mu.Unlock()
	return s.Store.List(query)
}

func (s *getCountingStore) reset() {
	s.mu.Lock()
	s.gets = 0
	s.lists = 0
	s.liveSessionLists = 0
	s.mu.Unlock()
}

func (s *getCountingStore) liveSessionListCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveSessionLists
}

func (s *getCountingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *getCountingStore) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

func TestDurableDrainAckSnapshotBatchesAuthoritativeReads(t *testing.T) {
	base := beads.NewMemStoreFrom(2, []beads.Bead{
		{ID: "s-1", Type: "session", Status: "open", Labels: []string{sessionpkg.LabelSession}, Metadata: beads.StringMap{"state": "active"}},
		{ID: "s-2", Type: "session", Status: "open", Labels: []string{sessionpkg.LabelSession}, Metadata: beads.StringMap{"state": "active"}},
	}, nil)
	counting := &getCountingStore{Store: base}
	front := sessionpkg.NewStore(beads.SessionStore{Store: counting})
	snapshot := newDurableDrainAckSnapshot(front)

	for _, id := range []string{"s-1", "s-2"} {
		if _, _, _, err := snapshot.refresh(sessionpkg.Info{ID: id}); err != nil {
			t.Fatalf("refresh %s: %v", id, err)
		}
	}
	if got := counting.count(); got != 0 {
		t.Fatalf("authoritative drain-ack snapshot issued %d Gets, want 0", got)
	}
	if got := counting.listCount(); got != 2 {
		t.Fatalf("authoritative drain-ack snapshot issued %d Lists, want 2 union legs regardless of session count", got)
	}
}

func TestReconcileSessionBeadsOrdinaryDeadOrphanSkipsDurableAckSnapshot(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{Agents: []config.Agent{{Name: "worker", StartCommand: "true"}}}
	session := env.createSessionBead("worker", "worker")
	counting := &getCountingStore{Store: env.store}
	counting.reset()
	reconcileSessionBeads(
		context.Background(), []beads.Bead{session}, map[string]TemplateParams{},
		map[string]bool{}, env.cfg, env.sp, counting, nil, nil, nil, env.dt,
		map[string]int{}, false, nil, "", nil, env.clk, env.rec, 0, 0,
		&env.stdout, &env.stderr,
	)
	if got := counting.liveSessionListCount(); got != 0 {
		t.Fatalf("ordinary dead orphan issued %d authoritative all-session lists, want 0", got)
	}
}

// TestReconcileSessionBeadsFastPathGetBudget pins the number of store.Get calls a
// single healthy reconcile tick issues, so the ApplyPatchInfo write-returns-Info
// cutover (WI-5 W1) provably adds none. The forward pass builds its infoByID
// snapshot from the input slice (InfoFromPersistedBead, no Get) and folds every
// mutation locally via ApplyPatch/ApplyPatchInfo/MarkClosed (SetMetadataBatch +
// local fold, no Get); the only tick-body store.Get is the rare NDI witness close
// (finalizeDrainAckStoppedSession), which a healthy running session never hits.
// The expected count is therefore fixed and small — a ratchet against a
// regressive re-Get sneaking onto the hot path.
func TestReconcileSessionBeadsFastPathGetBudget(t *testing.T) {
	env, session, sessionName := newProgressStallTestEnv(t)
	// Recent activity so the healthy running session is neither progress-stalled
	// nor idle-killed — a clean steady-state fast-path tick.
	env.sp.SetActivity(sessionName, env.clk.Now())

	counting := &getCountingStore{Store: env.store}
	cfgNames := configuredSessionNames(env.cfg, "", counting)
	poolDesired := map[string]int{"worker": 1}

	// Count only the reconcile tick itself, not the harness setup above.
	counting.reset()
	reconcileSessionBeads(
		context.Background(),
		[]beads.Bead{session},
		env.desiredState,
		cfgNames,
		env.cfg,
		env.sp,
		counting,
		nil,
		nil,
		nil,
		env.dt,
		poolDesired,
		false,
		nil,
		"",
		nil,
		env.clk,
		env.rec,
		0,
		0,
		&env.stdout,
		&env.stderr,
	)

	// The healthy fast path issues zero store Gets: the snapshot and every
	// intra-tick refresh are local folds. If a future change reintroduces a re-Get
	// on this path, this fails — deliberately, per the WI-5 tick budget.
	const wantGets = 0
	if got := counting.count(); got != wantGets {
		t.Fatalf("healthy reconcile tick issued %d store.Get calls, want %d — a re-Get crept onto the reconciler fast path (WI-5 tick budget: write + local fold, never a re-Get). stdout=%q stderr=%q", got, wantGets, env.stdout.String(), env.stderr.String())
	}
}
