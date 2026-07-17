package beads

import (
	"sort"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// TestDisarmRouteOnNonRunnableTransition pins gc-nuhl's core invariant: a
// transition to a non-runnable status clears the executable routes and
// leaves everything else (including gc.run_target) untouched.
func TestDisarmRouteOnNonRunnableTransition(t *testing.T) {
	tests := []struct {
		name       string
		opts       UpdateOpts
		wantRouted bool // true if the gate should have fired
	}{
		{
			name:       "blocked clears routes",
			opts:       UpdateOpts{Status: strptr("blocked")},
			wantRouted: true,
		},
		{
			name:       "deferred clears routes",
			opts:       UpdateOpts{Status: strptr("deferred")},
			wantRouted: true,
		},
		{
			name:       "open is untouched",
			opts:       UpdateOpts{Status: strptr("open")},
			wantRouted: false,
		},
		{
			name:       "in_progress is untouched",
			opts:       UpdateOpts{Status: strptr("in_progress")},
			wantRouted: false,
		},
		{
			name:       "closed is untouched",
			opts:       UpdateOpts{Status: strptr("closed")},
			wantRouted: false,
		},
		{
			name:       "nil status is untouched",
			opts:       UpdateOpts{},
			wantRouted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := disarmRouteOnNonRunnableTransition(tt.opts)
			routed, hasRouted := got.Metadata[beadmeta.RoutedToMetadataKey]
			execRouted, hasExecRouted := got.Metadata[beadmeta.ExecutionRoutedToMetadataKey]
			if tt.wantRouted {
				if !hasRouted || routed != "" {
					t.Errorf("gc.routed_to = %q (present=%v), want \"\" (present=true)", routed, hasRouted)
				}
				if !hasExecRouted || execRouted != "" {
					t.Errorf("gc.execution_routed_to = %q (present=%v), want \"\" (present=true)", execRouted, hasExecRouted)
				}
			} else if hasRouted || hasExecRouted {
				t.Errorf("gate fired unexpectedly: metadata=%v", got.Metadata)
			}
		})
	}
}

// TestDisarmRouteOnNonRunnableTransitionPreservesRunTargetAndOtherKeys
// asserts gc.run_target and unrelated metadata survive the transition
// untouched -- run_target is non-executable recovery intent, not a live
// claim, per gc-nuhl's design text.
func TestDisarmRouteOnNonRunnableTransitionPreservesRunTargetAndOtherKeys(t *testing.T) {
	opts := UpdateOpts{
		Status: strptr("blocked"),
		Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey: "/home/ds/gascity/polecat",
			"help_request":                "missing worktree",
		},
	}
	got := disarmRouteOnNonRunnableTransition(opts)
	if got.Metadata[beadmeta.RunTargetMetadataKey] != "/home/ds/gascity/polecat" {
		t.Errorf("gc.run_target = %q, want preserved", got.Metadata[beadmeta.RunTargetMetadataKey])
	}
	if got.Metadata["help_request"] != "missing worktree" {
		t.Errorf("help_request = %q, want preserved", got.Metadata["help_request"])
	}
	if got.Metadata[beadmeta.RoutedToMetadataKey] != "" {
		t.Errorf("gc.routed_to = %q, want cleared", got.Metadata[beadmeta.RoutedToMetadataKey])
	}
}

// TestDisarmRouteOnNonRunnableTransitionOverridesExplicitRoute confirms the
// gate is unconditional: even if a caller explicitly (mistakenly) sets
// gc.routed_to to a non-empty value in the SAME call that transitions to
// blocked/deferred, the transition wins -- there is no legitimate reason to
// route a bead that is simultaneously being marked non-runnable.
func TestDisarmRouteOnNonRunnableTransitionOverridesExplicitRoute(t *testing.T) {
	opts := UpdateOpts{
		Status: strptr("blocked"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat",
		},
	}
	got := disarmRouteOnNonRunnableTransition(opts)
	if got.Metadata[beadmeta.RoutedToMetadataKey] != "" {
		t.Errorf("gc.routed_to = %q, want forced to empty", got.Metadata[beadmeta.RoutedToMetadataKey])
	}
}

// TestDisarmRouteOnNonRunnableTransitionDoesNotMutateCallerMap is the
// immutability regression: the caller's own Metadata map must be unaffected
// by the gate, since map values are reference types in Go and a naive
// in-place write would leak back to the caller.
func TestDisarmRouteOnNonRunnableTransitionDoesNotMutateCallerMap(t *testing.T) {
	original := map[string]string{
		beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat",
	}
	opts := UpdateOpts{Status: strptr("blocked"), Metadata: original}
	_ = disarmRouteOnNonRunnableTransition(opts)
	if original[beadmeta.RoutedToMetadataKey] != "/home/ds/gascity/polecat" {
		t.Errorf("caller's original map was mutated: %v", original)
	}
}

// TestBdUpdateArgsDisarmsRouteOnNonRunnableTransition verifies BdStore's
// specific wiring point: bdUpdateArgs is shared by both Update and
// UpdateIfMatch (bdstore.go, bdstore_conditional.go), so gating it there
// covers both write paths for the bd-backed store without a live bd binary.
func TestBdUpdateArgsDisarmsRouteOnNonRunnableTransition(t *testing.T) {
	args := bdUpdateArgs("gc-x", UpdateOpts{Status: strptr("blocked")})

	// --set-metadata flags are emitted in sorted key order (bdUpdateArgs
	// sorts keys before appending), so assert on adjacent pairs rather than
	// a brittle full-slice comparison.
	want := map[string]string{
		beadmeta.RoutedToMetadataKey:          "",
		beadmeta.ExecutionRoutedToMetadataKey: "",
	}
	found := map[string]bool{}
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--set-metadata" {
			continue
		}
		kv := args[i+1]
		for key, wantVal := range want {
			if kv == key+"="+wantVal {
				found[key] = true
			}
		}
	}
	var missing []string
	for key := range want {
		if !found[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("bdUpdateArgs did not emit --set-metadata clears for %v; args=%v", missing, args)
	}
	hasStatusBlocked := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--status" && args[i+1] == "blocked" {
			hasStatusBlocked = true
		}
	}
	if !hasStatusBlocked {
		t.Errorf("bdUpdateArgs dropped --status blocked; args=%v", args)
	}
}

// TestApplyUpdateOptsToBeadDisarmsRouteOnNonRunnableTransition verifies the
// gate at applyUpdateOptsToBead directly and in isolation from any backing
// store. This matters because a black-box test through CachingStore.Update
// cannot isolate this specific call site: CachingStore.Get re-fetches a
// dirty entry from backing on next access, which would mask this function's
// own contribution and make such a test pass whether or not this particular
// gate call fires (the backing store's own gate, exercised earlier in the
// same call, already produces the correct end state independently). Calling
// the function directly, on a bead whose cached Metadata was never touched
// by a real write, is the only way to pin its own behavior.
func TestApplyUpdateOptsToBeadDisarmsRouteOnNonRunnableTransition(t *testing.T) {
	bead := Bead{
		ID:     "gc-1",
		Status: "in_progress",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat",
		},
	}
	got := applyUpdateOptsToBead(bead, UpdateOpts{Status: strptr("blocked")})
	if got.Status != "blocked" {
		t.Errorf("Status = %q, want blocked", got.Status)
	}
	if v := got.Metadata[beadmeta.RoutedToMetadataKey]; v != "" {
		t.Errorf("gc.routed_to = %q, want cleared", v)
	}
}
