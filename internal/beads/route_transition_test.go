package beads_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// TestUpdateDisarmsRouteOnNonRunnableTransition is gc-nuhl's AC1/AC2: a
// routed open/in_progress bead transitioning to blocked or deferred must
// atomically lose gc.routed_to (and gc.execution_routed_to) in the same
// write, while gc.run_target survives untouched.
func TestUpdateDisarmsRouteOnNonRunnableTransition(t *testing.T) {
	for _, status := range []string{"blocked", "deferred"} {
		t.Run(status, func(t *testing.T) {
			s := beads.NewMemStore()
			b, err := s.Create(beads.Bead{
				Title:  "routed work",
				Status: "in_progress",
				Metadata: map[string]string{
					beadmeta.RoutedToMetadataKey:          "/home/ds/gascity/polecat",
					beadmeta.ExecutionRoutedToMetadataKey: "/home/ds/gascity/polecat-3",
					beadmeta.RunTargetMetadataKey:         "/home/ds/gascity/polecat",
				},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if err := s.Update(b.ID, beads.UpdateOpts{Status: strPtr(status)}); err != nil {
				t.Fatalf("Update to %s: %v", status, err)
			}

			got, err := s.Get(b.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Status != status {
				t.Errorf("Status = %q, want %q", got.Status, status)
			}
			if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
				t.Errorf("gc.routed_to = %q, want cleared", v)
			}
			if v := got.Metadata[beadmeta.ExecutionRoutedToMetadataKey]; v != "" {
				t.Errorf("gc.execution_routed_to = %q, want cleared", v)
			}
			if v := got.Metadata[beadmeta.RunTargetMetadataKey]; v != "/home/ds/gascity/polecat" {
				t.Errorf("gc.run_target = %q, want preserved", v)
			}
		})
	}
}

// TestUpdateIfMatchDisarmsRouteOnNonRunnableTransition is the CAS-path
// regression: UpdateIfMatch must apply the same gc-nuhl invariant as Update,
// since a worker fencing a status change against a concurrent claim (exactly
// where a blocked transition is most likely to race a dispatcher) must not
// bypass the disarm.
func TestUpdateIfMatchDisarmsRouteOnNonRunnableTransition(t *testing.T) {
	s := beads.NewMemStore()
	b, err := s.Create(beads.Bead{
		Title:  "routed work",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	writer, ok := beads.ConditionalWriterFor(s)
	if !ok {
		t.Fatal("MemStore does not implement ConditionalWriter")
	}
	if err := writer.UpdateIfMatch(b.ID, b.Revision, beads.UpdateOpts{Status: strPtr("blocked")}); err != nil {
		t.Fatalf("UpdateIfMatch: %v", err)
	}

	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
		t.Errorf("gc.routed_to = %q, want cleared via UpdateIfMatch", v)
	}
}

// TestReopenDoesNotSilentlyRestoreRoute is gc-nuhl's AC3: transitioning a
// disarmed bead back to an active status must not, by itself, resurrect
// gc.routed_to. The gate only ever clears on the way to non-runnable; there
// is no symmetric "restore on reopen" in this Update call. (A SEPARATE,
// pre-existing mechanism -- cmd/gc/route_recovery.go's carried-route
// restamper -- may re-derive gc.routed_to from the preserved gc.run_target on
// its own patrol cadence once status is back to "open"; that is the
// explicit, status-gated recovery path the design text sanctions, and it is
// out of scope for this Update-level test.)
func TestReopenDoesNotSilentlyRestoreRoute(t *testing.T) {
	s := beads.NewMemStore()
	b, err := s.Create(beads.Bead{
		Title:  "routed work",
		Status: "in_progress",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:  "/home/ds/gascity/polecat",
			beadmeta.RunTargetMetadataKey: "/home/ds/gascity/polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Update(b.ID, beads.UpdateOpts{Status: strPtr("blocked")}); err != nil {
		t.Fatalf("Update to blocked: %v", err)
	}
	if err := s.Update(b.ID, beads.UpdateOpts{Status: strPtr("open")}); err != nil {
		t.Fatalf("Update to open: %v", err)
	}

	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
		t.Errorf("gc.routed_to = %q after reopen, want still cleared (no silent restore)", v)
	}
}

// TestGCMt22ShapedHelpBlockPath is gc-nuhl's AC5: it reproduces the metadata
// shape and call sequence of the live gc-mt22/gc-4rrz incidents at the layer
// this fix reaches. In both incidents, a worker recorded a help_request and
// then set status=blocked via TWO separate `bd update` calls, and
// gc.routed_to survived until it was cleared out-of-band, well after the
// transition.
//
// SCOPE NOTE: the real incidents ran through the external `bd` binary
// directly (a worker's raw shell command), which bypasses this package
// entirely -- no Go-level fix can intercept that process boundary. This test
// validates the front door for every Update/UpdateIfMatch write this
// repository's own Go code controls (internal callers, the HTTP API, and CAS
// writers all fan out to one of those two entry points). The raw-external-CLI
// gap is not closed by any mechanism in this repository today -- a full-repo
// search found no automated sweep for it under any name.
func TestGCMt22ShapedHelpBlockPath(t *testing.T) {
	s := beads.NewMemStore()
	b, err := s.Create(beads.Bead{
		Title:  "external-import trust design call",
		Status: "in_progress",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:  "/home/ds/gascity/polecat",
			beadmeta.RunTargetMetadataKey: "/home/ds/gascity/polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Call 1: record the help request (metadata-only, no status change) --
	// mirrors `bd update <id> --set-metadata help_request=...`. This alone
	// must NOT clear the route: only a status transition does.
	if err := s.Update(b.ID, beads.UpdateOpts{
		Metadata: map[string]string{"help_request": "NEEDS A DESIGN CALL"},
	}); err != nil {
		t.Fatalf("Update help_request: %v", err)
	}
	mid, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("Get after help_request: %v", err)
	}
	if v := mid.Metadata[beadmeta.RoutedToMetadataKey]; v != "/home/ds/gascity/polecat" {
		t.Errorf("gc.routed_to = %q after help_request-only write, want unchanged (route clears only on status transition)", v)
	}

	// Call 2: the status transition -- mirrors `bd update <id> --status blocked`.
	if err := s.Update(b.ID, beads.UpdateOpts{Status: strPtr("blocked")}); err != nil {
		t.Fatalf("Update to blocked: %v", err)
	}

	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("Get after blocked: %v", err)
	}
	if got.Status != "blocked" {
		t.Errorf("Status = %q, want blocked", got.Status)
	}
	if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
		t.Errorf("gc.routed_to = %q after status=blocked, want cleared", v)
	}
	if v := got.Metadata["help_request"]; v != "NEEDS A DESIGN CALL" {
		t.Errorf("help_request = %q, want preserved", v)
	}
	if v := got.Metadata[beadmeta.RunTargetMetadataKey]; v != "/home/ds/gascity/polecat" {
		t.Errorf("gc.run_target = %q, want preserved as non-executable recovery intent", v)
	}
}

// TestCachingStoreUpdateDisarmsRouteWhenRefreshFails covers the fourth gated
// call site (applyUpdateOptsToBead, caching_store_writes.go) at the one
// branch that actually exercises it end-to-end: CachingStore.Update's
// Get-after-Update fallback, taken when the backing store's write succeeds
// but the immediate re-fetch fails. Without the gate here, the cache would
// keep echoing the caller's original (un-cleared) opts.Metadata for the
// route even though the backing store already disarmed it -- a real
// dispatcher reading the cache could still see a blocked bead as routed.
// Reuses the getFailsAfterUpdateStore double from caching_store_test.go
// (same beads_test package), which lets the real backing Update apply (and
// gate) before forcing exactly one subsequent Get to fail.
func TestCachingStoreUpdateDisarmsRouteWhenRefreshFails(t *testing.T) {
	mem := beads.NewMemStore()
	original, err := mem.Create(beads.Bead{
		Title:  "routed work",
		Status: "in_progress",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing := &getFailsAfterUpdateStore{Store: mem}
	cs := beads.NewCachingStoreForTest(backing, nil)
	if err := cs.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	if err := cs.Update(original.ID, beads.UpdateOpts{Status: strPtr("blocked")}); err != nil {
		t.Fatalf("Update to blocked: %v", err)
	}

	got, err := cs.Get(original.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
	if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
		t.Errorf("gc.routed_to = %q in cached fallback view, want cleared", v)
	}
}

// TestBdStoreUpdateAllDisarmsRouteOnNonRunnableTransition covers the fourth
// real write choke point found during review: BdStore.UpdateAll builds its
// own bd argv independently of bdUpdateArgs (used by controller batch paths
// such as internal/dispatch scope-skip), so it needs its own call to the
// gate rather than inheriting bdUpdateArgs's.
func TestBdStoreUpdateAllDisarmsRouteOnNonRunnableTransition(t *testing.T) {
	var calls [][]string
	runner := func(_, name string, args ...string) ([]byte, error) {
		if name != "bd" {
			return nil, fmt.Errorf("unexpected command name %q", name)
		}
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}
	s := beads.NewBdStore("/city", runner)

	updated, err := s.UpdateAll([]string{"bd-1", "bd-2"}, beads.UpdateOpts{
		Status: strPtr("blocked"),
	})
	if err != nil {
		t.Fatalf("UpdateAll: %v", err)
	}
	if updated != 2 {
		t.Fatalf("updated = %d, want 2", updated)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	// UpdateAll sorts --set-metadata pairs by key (sort.Strings(keys) in
	// bdstore.go), and "gc.execution_routed_to" < "gc.routed_to"
	// lexicographically, so the order below is deterministic.
	want := []string{
		"update", "--json", "bd-1", "bd-2",
		"--status", "blocked",
		"--set-metadata", beadmeta.ExecutionRoutedToMetadataKey + "=",
		"--set-metadata", beadmeta.RoutedToMetadataKey + "=",
	}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("UpdateAll args = %v, want %v", calls[0], want)
	}
}
