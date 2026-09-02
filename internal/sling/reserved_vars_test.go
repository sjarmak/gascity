package sling

import (
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestValidateReservedVarNames(t *testing.T) {
	tests := []struct {
		name    string
		vars    []string
		wantErr bool
		wantKey string
	}{
		{name: "no vars", vars: nil, wantErr: false},
		{name: "unrelated var passes", vars: []string{"base_branch=main"}, wantErr: false},
		{name: "no_land rejected", vars: []string{"no_land=true"}, wantErr: true, wantKey: "no_land"},
		{name: "finalize_mode rejected", vars: []string{"finalize_mode=verify-only"}, wantErr: true, wantKey: "finalize_mode"},
		{name: "reserved key among others rejected", vars: []string{"base_branch=main", "finalize_mode=verify-only"}, wantErr: true, wantKey: "finalize_mode"},
		{name: "malformed entry without = is ignored", vars: []string{"no_land"}, wantErr: false},
		{name: "prefix match is not a collision", vars: []string{"no_land_reason=flaky"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReservedVarNames(tt.vars)
			if tt.wantErr != (err != nil) {
				t.Fatalf("validateReservedVarNames(%v) error = %v, wantErr %v", tt.vars, err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			var reservedErr *ReservedVarNameError
			if !errors.As(err, &reservedErr) {
				t.Fatalf("error = %v, want *ReservedVarNameError", err)
			}
			if reservedErr.Key != tt.wantKey {
				t.Errorf("Key = %q, want %q", reservedErr.Key, tt.wantKey)
			}
			if !strings.Contains(err.Error(), "--set-metadata") {
				t.Errorf("error message %q does not name --set-metadata as the alternative", err.Error())
			}
		})
	}
}

// TestDoSlingRejectsReservedVarNames pins the gc-8umql fix at the DoSling
// entry point rather than only the helper: no_land/finalize_mode passed via
// --var are silently invisible to a guard that reads bead metadata directly
// (stampFormulaVars only ever writes gc.var.<name> on the molecule root, not
// the bare key on the target bead), so DoSling must fail loudly instead of
// routing the bead as if the guard were set.
func TestDoSlingRejectsReservedVarNames(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{Name: "test"}}
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	b, err := deps.Store.Create(beads.Bead{Title: "adopt PR lineage", Type: "task", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	for _, dryRun := range []bool{false, true} {
		_, err := DoSling(SlingOpts{
			Target:        a,
			BeadOrFormula: b.ID,
			OnFormula:     "code-review",
			Vars:          []string{"no_land=true", "finalize_mode=verify-only"},
			DryRun:        dryRun,
		}, deps, deps.Store)
		if err == nil {
			t.Fatalf("DoSling(dryRun=%v) with reserved vars: expected error, got nil", dryRun)
		}
		var reservedErr *ReservedVarNameError
		if !errors.As(err, &reservedErr) {
			t.Fatalf("DoSling(dryRun=%v) error = %v, want *ReservedVarNameError", dryRun, err)
		}

		got, getErr := deps.Store.Get(b.ID)
		if getErr != nil {
			t.Fatalf("re-fetching target bead: %v", getErr)
		}
		if got.Metadata["gc.no_land"] != "" || got.Metadata["gc.finalize_mode"] != "" {
			t.Fatalf("target bead metadata mutated despite rejected sling: %#v", got.Metadata)
		}
		if got.Status != "open" {
			t.Fatalf("target bead status = %q, want unchanged \"open\"", got.Status)
		}
	}
}
