package dispatch

import (
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/rollout/gate"
)

// spawnRecoveryFaultStore injects failures into the specific store writes
// markControllerSpawnError uses to record a spawn failure, so tests can
// exercise each recovery branch (metadata write, close, scope reconcile)
// independently and in combination.
type spawnRecoveryFaultStore struct {
	beads.Store
	targetID        string
	failSetMetadata error
	failUpdateClose error
}

func (s *spawnRecoveryFaultStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if id == s.targetID && s.failSetMetadata != nil {
		return s.failSetMetadata
	}
	return s.Store.SetMetadataBatch(id, kvs)
}

func (s *spawnRecoveryFaultStore) Update(id string, opts beads.UpdateOpts) error {
	if id == s.targetID && opts.Status != nil && *opts.Status == "closed" && s.failUpdateClose != nil {
		return s.failUpdateClose
	}
	return s.Store.Update(id, opts)
}

// TestMarkControllerSpawnErrorRecoveryFailureInjection is the table-driven
// coverage required for gc-dy1pr: every store write markControllerSpawnError
// attempts (metadata, close, scope-reconcile) can independently fail without
// aborting the other recovery stages, and every failure is surfaced on
// RecoveryErr rather than only traced. Retryable still reflects the ORIGINAL
// spawn error's classification, independent of whether recovery succeeded.
func TestMarkControllerSpawnErrorRecoveryFailureInjection(t *testing.T) {
	metadataErr := errors.New("metadata write boom")
	closeErr := errors.New("close boom")

	cases := []struct {
		name             string
		spawnErr         error
		failSetMetadata  error
		failUpdateClose  error
		withBrokenScope  bool
		wantRetryable    bool
		wantRecoveryNil  bool
		wantRecoverySubs []string
	}{
		{
			name:            "transient spawn error, all recovery writes succeed",
			spawnErr:        markTransientControllerBoundaryError(errors.New("dial tcp: i/o timeout")),
			wantRetryable:   true,
			wantRecoveryNil: true,
		},
		{
			name:             "transient spawn error, metadata write fails",
			spawnErr:         markTransientControllerBoundaryError(errors.New("dial tcp: i/o timeout")),
			failSetMetadata:  metadataErr,
			wantRetryable:    true,
			wantRecoverySubs: []string{"recording transient failure metadata", "metadata write boom"},
		},
		{
			name:            "hard spawn error, all recovery writes succeed",
			spawnErr:        errors.New("permanent spawn failure"),
			wantRetryable:   false,
			wantRecoveryNil: true,
		},
		{
			name:             "hard spawn error, metadata write fails alone",
			spawnErr:         errors.New("permanent spawn failure"),
			failSetMetadata:  metadataErr,
			wantRetryable:    false,
			wantRecoverySubs: []string{"recording hard failure metadata", "metadata write boom"},
		},
		{
			name:             "hard spawn error, close fails alone",
			spawnErr:         errors.New("permanent spawn failure"),
			failUpdateClose:  closeErr,
			wantRetryable:    false,
			wantRecoverySubs: []string{"closing failed bead", "close boom"},
		},
		{
			name:             "hard spawn error, scope reconcile fails alone",
			spawnErr:         errors.New("permanent spawn failure"),
			withBrokenScope:  true,
			wantRetryable:    false,
			wantRecoverySubs: []string{"reconciling enclosing scope", "gc.root_bead_id"},
		},
		{
			// Scope reconcile only acts on a bead the store reports as closed
			// (reconcileClosedScopeMemberWithOptions reloads and no-ops
			// otherwise), so when the close write itself fails the bead never
			// transitions to closed and the scope-reconcile stage legitimately
			// has nothing to do — it is attempted but reports no error.
			name:             "hard spawn error, metadata and close writes fail together",
			spawnErr:         errors.New("permanent spawn failure"),
			failSetMetadata:  metadataErr,
			failUpdateClose:  closeErr,
			withBrokenScope:  true,
			wantRetryable:    false,
			wantRecoverySubs: []string{"recording hard failure metadata", "closing failed bead"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := newStampedDrainStore(t, gate.Auto)
			bead := beads.Bead{Title: "spawn-target"}
			if tc.withBrokenScope {
				// A scope_ref with no root_bead_id makes
				// reconcileTerminalScopedMemberWithOptions fail deterministically,
				// without needing a store-level fault for this stage.
				bead.Metadata = map[string]string{beadmeta.ScopeRefMetadataKey: "body"}
			}
			created, err := base.Create(bead)
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			store := &spawnRecoveryFaultStore{
				Store:           base,
				targetID:        created.ID,
				failSetMetadata: tc.failSetMetadata,
				failUpdateClose: tc.failUpdateClose,
			}

			outcome := markControllerSpawnError(store, created.ID, tc.spawnErr, ProcessOptions{})

			if outcome.Retryable != tc.wantRetryable {
				t.Fatalf("Retryable = %v, want %v", outcome.Retryable, tc.wantRetryable)
			}
			if tc.wantRecoveryNil {
				if outcome.RecoveryErr != nil {
					t.Fatalf("RecoveryErr = %v, want nil", outcome.RecoveryErr)
				}
				return
			}
			if outcome.RecoveryErr == nil {
				t.Fatal("RecoveryErr = nil, want non-nil (a recovery write was injected to fail)")
			}
			got := outcome.RecoveryErr.Error()
			for _, sub := range tc.wantRecoverySubs {
				if !strings.Contains(got, sub) {
					t.Fatalf("RecoveryErr %q missing expected substring %q", got, sub)
				}
			}
			// The original spawn error must still be preserved on the bead
			// regardless of whether the recovery writes themselves succeeded.
			reloaded, err := base.Get(created.ID)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if tc.failSetMetadata == nil {
				if got := reloaded.Metadata[beadmeta.ControllerErrorMetadataKey]; got != tc.spawnErr.Error() {
					t.Fatalf("gc.controller_error = %q, want %q", got, tc.spawnErr.Error())
				}
			}
		})
	}
}

// TestControllerSpawnBoundaryPendingSurfacesRecoveryFailure pins criterion 4
// from gc-dy1pr: when the recovery write itself fails, the boundary helper
// must not report the caller's original error as ordinary pending
// convergence — it has to surface the incomplete-recovery error instead.
func TestControllerSpawnBoundaryPendingSurfacesRecoveryFailure(t *testing.T) {
	base := newStampedDrainStore(t, gate.Auto)
	created, err := base.Create(beads.Bead{Title: "boundary-target"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	metadataErr := errors.New("metadata write boom")
	store := &spawnRecoveryFaultStore{
		Store:           base,
		targetID:        created.ID,
		failSetMetadata: metadataErr,
	}

	transientErr := markTransientControllerBoundaryError(errors.New("dial tcp: i/o timeout"))
	pending, recoveryErr := controllerSpawnBoundaryPending(store, created.ID, transientErr, ProcessOptions{})
	if !pending {
		t.Fatal("pending = false, want true (original error is transient)")
	}
	if recoveryErr == nil {
		t.Fatal("recoveryErr = nil, want non-nil: the metadata write recording the transient classification failed")
	}

	resultErr := classifySpawnBoundary(store, created.ID, transientErr, ProcessOptions{}, "context")
	if errors.Is(resultErr, ErrControlPending) {
		t.Fatal("classifySpawnBoundary returned ErrControlPending despite a failed recovery write — incomplete recovery silently reported as convergence")
	}
	if !errors.Is(resultErr, transientErr) {
		t.Fatalf("classifySpawnBoundary result %v does not wrap the original error", resultErr)
	}
}
