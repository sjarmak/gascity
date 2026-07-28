package sling

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/runtime"
)

// applicabilityFixture builds sling deps whose formula search path holds one
// direction-declaring formula (mol-pr-review = outgoing) and one undeclared
// formula (mol-generic), plus a city-scoped agent.
func applicabilityFixture(t *testing.T) (SlingDeps, config.Agent) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write formula %s: %v", name, err)
		}
	}
	write("mol-pr-review", "formula = \"mol-pr-review\"\nversion = 1\n\n[applicability]\ndirection = \"outgoing\"\n\n[[steps]]\nid = \"work\"\ntitle = \"Work\"\n")
	write("mol-generic", "formula = \"mol-generic\"\nversion = 1\n\n[[steps]]\nid = \"work\"\ntitle = \"Work\"\n")

	cfg := &config.City{
		Workspace:     config.Workspace{Name: "test"},
		FormulaLayers: config.FormulaLayers{City: []string{dir}},
	}
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	return deps, a
}

// createGoal inserts a task bead carrying an optional goal-side direction
// declaration and returns its ID.
func createGoal(t *testing.T, store beads.Store, dir string) string {
	t.Helper()
	meta := map[string]string{}
	if dir != "" {
		meta[beadmeta.ApplicabilityDirectionMetadataKey] = dir
	}
	b, err := store.Create(beads.Bead{Title: "goal", Type: "task", Status: "open", Metadata: meta})
	if err != nil {
		t.Fatalf("create goal bead: %v", err)
	}
	return b.ID
}

func TestEnforceFormulaGoalDirection(t *testing.T) {
	deps, a := applicabilityFixture(t)

	tests := []struct {
		name        string
		goalDir     string
		formula     string
		explicit    bool
		wantErr     bool
		wantErrText string
	}{
		{
			name:        "explicit mismatch fails closed as incompatible",
			goalDir:     "incoming",
			formula:     "mol-pr-review", // declares outgoing
			explicit:    true,
			wantErr:     true,
			wantErrText: "formula.applicability_incompatible",
		},
		{
			name:        "default mismatch fails closed as no-compatible-formula",
			goalDir:     "incoming",
			formula:     "mol-pr-review",
			explicit:    false,
			wantErr:     true,
			wantErrText: "formula.no_compatible_formula",
		},
		{
			name:     "matching direction binds",
			goalDir:  "outgoing",
			formula:  "mol-pr-review",
			explicit: true,
			wantErr:  false,
		},
		{
			name:     "undeclared goal keeps legacy binding",
			goalDir:  "",
			formula:  "mol-pr-review",
			explicit: true,
			wantErr:  false,
		},
		{
			name:     "undeclared formula binds any goal",
			goalDir:  "incoming",
			formula:  "mol-generic",
			explicit: false,
			wantErr:  false,
		},
		{
			name:        "malformed goal direction fails closed",
			goalDir:     "sideways",
			formula:     "mol-generic",
			explicit:    false,
			wantErr:     true,
			wantErrText: "formula.applicability_direction_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadID := createGoal(t, deps.Store, tt.goalDir)
			err := enforceFormulaGoalDirection(deps, a, deps.Store, beadID, tt.formula, tt.explicit)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("enforceFormulaGoalDirection = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("enforceFormulaGoalDirection = nil, want error %q", tt.wantErrText)
			}
			if !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantErrText)
			}
		})
	}
}

func TestEnforceFormulaGoalDirectionTypedErrors(t *testing.T) {
	deps, a := applicabilityFixture(t)

	t.Run("explicit yields DirectionIncompatibleError", func(t *testing.T) {
		beadID := createGoal(t, deps.Store, "incoming")
		err := enforceFormulaGoalDirection(deps, a, deps.Store, beadID, "mol-pr-review", true)
		var typed *formula.DirectionIncompatibleError
		if !errors.As(err, &typed) {
			t.Fatalf("error = %T %[1]v, want *formula.DirectionIncompatibleError", err)
		}
		if typed.FormulaDirection != formula.DirectionOutgoing || typed.GoalDirection != formula.DirectionIncoming {
			t.Fatalf("directions = (%q,%q), want (outgoing,incoming)", typed.FormulaDirection, typed.GoalDirection)
		}
	})

	t.Run("default yields NoCompatibleFormulaError listing the excluded formula", func(t *testing.T) {
		beadID := createGoal(t, deps.Store, "incoming")
		err := enforceFormulaGoalDirection(deps, a, deps.Store, beadID, "mol-pr-review", false)
		var typed *formula.NoCompatibleFormulaError
		if !errors.As(err, &typed) {
			t.Fatalf("error = %T %[1]v, want *formula.NoCompatibleFormulaError", err)
		}
		if len(typed.Excluded) != 1 || typed.Excluded[0] != "mol-pr-review" {
			t.Fatalf("Excluded = %v, want [mol-pr-review]", typed.Excluded)
		}
	})
}

// TestDoSlingExplicitFormulaDirectionMismatchFailsClosed proves the explicit
// (--on) sling path rejects an incompatible formula before mutating the store.
func TestDoSlingExplicitFormulaDirectionMismatchFailsClosed(t *testing.T) {
	deps, a := applicabilityFixture(t)
	beadID := createGoal(t, deps.Store, "incoming")

	opts := SlingOpts{Target: a, BeadOrFormula: beadID, OnFormula: "mol-pr-review", Force: true}
	_, err := DoSling(opts, deps, deps.Store)

	var typed *formula.DirectionIncompatibleError
	if !errors.As(err, &typed) {
		t.Fatalf("DoSling error = %T %[1]v, want *formula.DirectionIncompatibleError", err)
	}
	requireNoBinding(t, deps.Store, beadID)
}

// TestDoSlingDefaultFormulaDirectionMismatchFailsClosed proves the default
// (dispatch fallback) sling path excludes the single incompatible candidate and
// fails with no-compatible-formula before mutating the store.
func TestDoSlingDefaultFormulaDirectionMismatchFailsClosed(t *testing.T) {
	deps, a := applicabilityFixture(t)
	a.DefaultSlingFormula = stringPtr("mol-pr-review")
	beadID := createGoal(t, deps.Store, "incoming")

	opts := SlingOpts{Target: a, BeadOrFormula: beadID, Force: true}
	_, err := DoSling(opts, deps, deps.Store)

	var typed *formula.NoCompatibleFormulaError
	if !errors.As(err, &typed) {
		t.Fatalf("DoSling error = %T %[1]v, want *formula.NoCompatibleFormulaError", err)
	}
	requireNoBinding(t, deps.Store, beadID)
}

// TestDoSlingUndeclaredGoalKeepsLegacyDefaultBinding proves an undeclared goal
// binds the agent's default formula exactly as before the direction axis
// existed: the gate is transparent, and the sling proceeds to materialize a
// wisp.
func TestDoSlingUndeclaredGoalKeepsLegacyDefaultBinding(t *testing.T) {
	deps, a := applicabilityFixture(t)
	a.DefaultSlingFormula = stringPtr("mol-pr-review")
	beadID := createGoal(t, deps.Store, "") // no direction declared

	opts := SlingOpts{Target: a, BeadOrFormula: beadID, Force: true}
	result, err := DoSling(opts, deps, deps.Store)
	if err != nil {
		t.Fatalf("DoSling on undeclared goal: %v", err)
	}
	if result.WispRootID == "" {
		t.Fatal("WispRootID empty; expected legacy binding to materialize a wisp")
	}
}

// TestDoSlingBatchChildFormulaDirectionMismatchFailsClosed proves the batch
// (container-expansion) path gates each child on the direction axis: a child
// declaring an incompatible direction fails closed and is not bound, rather than
// silently inheriting the agent's default formula.
func TestDoSlingBatchChildFormulaDirectionMismatchFailsClosed(t *testing.T) {
	deps, a := applicabilityFixture(t)
	a.DefaultSlingFormula = stringPtr("mol-pr-review") // outgoing
	store := deps.Store

	convoy, err := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	epic, err := store.Create(beads.Bead{Title: "epic", Type: "epic"})
	if err != nil {
		t.Fatalf("create epic: %v", err)
	}
	child, err := store.Create(beads.Bead{
		Title:    "child",
		Type:     "task",
		Status:   "open",
		ParentID: epic.ID,
		Metadata: map[string]string{beadmeta.ApplicabilityDirectionMetadataKey: "incoming"},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := store.DepAdd(convoy.ID, child.ID, "tracks"); err != nil {
		t.Fatalf("track child: %v", err)
	}

	result, _ := DoSlingBatch(SlingOpts{Target: a, BeadOrFormula: convoy.ID}, deps, store)

	if result.Routed != 0 {
		t.Fatalf("Routed = %d, want 0 (incompatible child must not bind)", result.Routed)
	}
	if len(result.Children) != 1 || !result.Children[0].Failed {
		t.Fatalf("children = %#v, want a single failed child", result.Children)
	}
	if !strings.Contains(result.Children[0].FailReason, "formula.no_compatible_formula") {
		t.Fatalf("FailReason = %q, want no_compatible_formula", result.Children[0].FailReason)
	}
	requireNoBinding(t, store, child.ID)
}

func requireNoBinding(t *testing.T, store beads.Store, beadID string) {
	t.Helper()
	got, err := store.Get(beadID)
	if err != nil {
		t.Fatalf("Get(%s): %v", beadID, err)
	}
	if got.Metadata["molecule_id"] != "" {
		t.Fatalf("molecule_id = %q, want unset after fail-closed rejection", got.Metadata["molecule_id"])
	}
}

// createGoalClaimed inserts a task bead already claimed by an actor
// (status=in_progress + assignee set), mirroring a goal a prior actor grabbed
// via `bd update --claim`. This is precisely the state preflight's --reassign
// reopen clears, so it lets the direction gate's "no store mutation on reject"
// guarantee be asserted against the assignee and status, not just the binding.
func createGoalClaimed(t *testing.T, store beads.Store, dir, assignee string) beads.Bead {
	t.Helper()
	meta := map[string]string{}
	if dir != "" {
		meta[beadmeta.ApplicabilityDirectionMetadataKey] = dir
	}
	b, err := store.Create(beads.Bead{Title: "goal", Type: "task", Metadata: meta})
	if err != nil {
		t.Fatalf("create claimed goal bead: %v", err)
	}
	// MemStore.Create forces status=open, so apply the claimed state
	// (in_progress + assignee) via a follow-up Update, mirroring
	// orderClaimedPoolHandoffSetup.
	inProgress := "in_progress"
	if err := store.Update(b.ID, beads.UpdateOpts{Status: &inProgress, Assignee: &assignee}); err != nil {
		t.Fatalf("update goal to claimed state: %v", err)
	}
	got, err := store.Get(b.ID)
	if err != nil {
		t.Fatalf("re-read claimed goal: %v", err)
	}
	return got
}

func requireGoalUnmutated(t *testing.T, store beads.Store, beadID, wantAssignee, wantStatus string) {
	t.Helper()
	got, err := store.Get(beadID)
	if err != nil {
		t.Fatalf("Get(%s): %v", beadID, err)
	}
	if got.Assignee != wantAssignee {
		t.Fatalf("assignee = %q, want %q: a fail-closed direction reject must not reassign the goal", got.Assignee, wantAssignee)
	}
	if got.Status != wantStatus {
		t.Fatalf("status = %q, want %q: a fail-closed direction reject must not reopen the goal", got.Status, wantStatus)
	}
	requireNoBinding(t, store, beadID)
}

// TestDoSlingReassignExplicitDirectionMismatchLeavesGoalUnmutated proves the
// direction gate runs before preflight's --reassign reopen: an incompatible
// explicit sling with Reassign fails closed WITHOUT clearing the goal's
// assignee or reopening its status. Regression for the pre-gate mutation where
// preflight reassigned the goal before the direction check rejected it.
func TestDoSlingReassignExplicitDirectionMismatchLeavesGoalUnmutated(t *testing.T) {
	deps, a := applicabilityFixture(t)
	goal := createGoalClaimed(t, deps.Store, "incoming", "human") // mol-pr-review is outgoing

	opts := SlingOpts{Target: a, BeadOrFormula: goal.ID, OnFormula: "mol-pr-review", Reassign: true, Force: true}
	_, err := DoSling(opts, deps, deps.Store)

	var typed *formula.DirectionIncompatibleError
	if !errors.As(err, &typed) {
		t.Fatalf("DoSling error = %T %[1]v, want *formula.DirectionIncompatibleError", err)
	}
	requireGoalUnmutated(t, deps.Store, goal.ID, "human", "in_progress")
}

// TestDoSlingReassignDefaultDirectionMismatchLeavesGoalUnmutated is the default
// (dispatch fallback) analog: an incompatible agent-default sling with
// Reassign fails no-compatible-formula and likewise leaves the claimed goal's
// assignee and status intact.
func TestDoSlingReassignDefaultDirectionMismatchLeavesGoalUnmutated(t *testing.T) {
	deps, a := applicabilityFixture(t)
	a.DefaultSlingFormula = stringPtr("mol-pr-review") // outgoing
	goal := createGoalClaimed(t, deps.Store, "incoming", "human")

	opts := SlingOpts{Target: a, BeadOrFormula: goal.ID, Reassign: true, Force: true}
	_, err := DoSling(opts, deps, deps.Store)

	var typed *formula.NoCompatibleFormulaError
	if !errors.As(err, &typed) {
		t.Fatalf("DoSling error = %T %[1]v, want *formula.NoCompatibleFormulaError", err)
	}
	requireGoalUnmutated(t, deps.Store, goal.ID, "human", "in_progress")
}

// TestDoSlingBatchDirectionMismatchDoesNotBurnExistingMolecule proves the batch
// path gates direction BEFORE the pre-attach molecule burn: a direction-
// incompatible child that already carries an open molecule is rejected WITHOUT
// that molecule being auto-closed. Regression for the burn-before-gate
// mutation, where checkBatchNoMoleculeChildren closed the child's molecule
// before the per-child direction gate rejected the child.
func TestDoSlingBatchDirectionMismatchDoesNotBurnExistingMolecule(t *testing.T) {
	deps, a := applicabilityFixture(t)
	a.DefaultSlingFormula = stringPtr("mol-pr-review") // outgoing
	store := deps.Store

	convoy, err := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	epic, err := store.Create(beads.Bead{Title: "epic", Type: "epic"})
	if err != nil {
		t.Fatalf("create epic: %v", err)
	}
	child, err := store.Create(beads.Bead{
		Title:    "child",
		Type:     "task",
		Status:   "open",
		ParentID: epic.ID,
		Metadata: map[string]string{beadmeta.ApplicabilityDirectionMetadataKey: "incoming"},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// An existing open molecule attached to the (unassigned) child — exactly
	// the state checkBatchNoMoleculeChildren would auto-burn.
	mol, err := store.Create(beads.Bead{Title: "existing molecule", Type: "molecule", Status: "open", ParentID: child.ID})
	if err != nil {
		t.Fatalf("create molecule: %v", err)
	}
	if err := store.SetMetadata(child.ID, beadmeta.MoleculeIDMetadataKey, mol.ID); err != nil {
		t.Fatalf("set molecule_id: %v", err)
	}
	if err := store.DepAdd(convoy.ID, child.ID, "tracks"); err != nil {
		t.Fatalf("track child: %v", err)
	}

	// Force bypasses the idempotency skip so the incompatible child reaches the
	// attachment burn and the gate; mol-pr-review is not a graph formula, so the
	// burn helper is still CheckBatchNoMoleculeChildren.
	result, _ := DoSlingBatch(SlingOpts{Target: a, BeadOrFormula: convoy.ID, Force: true}, deps, store)

	if len(result.Children) != 1 || !result.Children[0].Failed {
		t.Fatalf("children = %#v, want a single failed child", result.Children)
	}
	if !strings.Contains(result.Children[0].FailReason, "formula.no_compatible_formula") {
		t.Fatalf("FailReason = %q, want no_compatible_formula", result.Children[0].FailReason)
	}
	if len(result.AutoBurned) != 0 {
		t.Fatalf("AutoBurned = %#v, want empty: a rejected child's molecule must not be burned", result.AutoBurned)
	}
	gotMol, err := store.Get(mol.ID)
	if err != nil {
		t.Fatalf("get molecule: %v", err)
	}
	if gotMol.Status == "closed" {
		t.Fatal("existing molecule was closed; a fail-closed direction reject must not burn it")
	}
	gotChild, err := store.Get(child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if gotChild.Metadata[beadmeta.MoleculeIDMetadataKey] != mol.ID {
		t.Fatalf("child molecule_id = %q, want %q: the attachment must be preserved on a fail-closed reject",
			gotChild.Metadata[beadmeta.MoleculeIDMetadataKey], mol.ID)
	}
}
