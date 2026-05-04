package orders

import (
	"strings"
	"testing"
)

// boolPtr / strPtr are tiny helpers for Override pointer fields.
func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func TestApplyOverridesAppliesCityLevelMatch(t *testing.T) {
	aa := []Order{{Name: "patrol"}, {Name: "sweeper"}}
	overrides := []Override{
		{Name: "patrol", Enabled: boolPtr(false)},
	}
	if err := ApplyOverrides(aa, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if aa[0].Enabled == nil || *aa[0].Enabled {
		t.Errorf("aa[0].Enabled = %v, want disabled", aa[0].Enabled)
	}
}

func TestApplyOverridesAppliesPerRigMatch(t *testing.T) {
	aa := []Order{
		{Name: "patrol", Rig: "foo"},
		{Name: "patrol", Rig: "bar"},
	}
	overrides := []Override{
		{Name: "patrol", Rig: "foo", Interval: strPtr("5m")},
	}
	if err := ApplyOverrides(aa, overrides); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if aa[0].Interval != "5m" {
		t.Errorf("aa[0] (rig=foo) interval = %q, want %q", aa[0].Interval, "5m")
	}
	if aa[1].Interval == "5m" {
		t.Errorf("aa[1] (rig=bar) interval = %q, want unchanged", aa[1].Interval)
	}
}

func TestApplyOverridesEmptyNameIsError(t *testing.T) {
	aa := []Order{{Name: "patrol"}}
	overrides := []Override{{Name: "", Enabled: boolPtr(false)}}
	err := ApplyOverrides(aa, overrides)
	if err == nil {
		t.Fatal("expected error for empty override name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error = %v, want 'name is required'", err)
	}
}

func TestApplyOverridesUnknownNameIsError(t *testing.T) {
	aa := []Order{{Name: "patrol"}}
	overrides := []Override{{Name: "ghost"}}
	err := ApplyOverrides(aa, overrides)
	if err == nil {
		t.Fatal("expected error for unknown override name, got nil")
	}
	if !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("error = %v, want quoted name 'ghost'", err)
	}
	// No per-rig hint when the name doesn't exist anywhere.
	if strings.Contains(err.Error(), "per-rig instance") {
		t.Errorf("error = %v, should not include per-rig hint when name doesn't exist", err)
	}
}

func TestApplyOverridesUnknownRigOnExistingNameIsError(t *testing.T) {
	aa := []Order{{Name: "patrol", Rig: "foo"}}
	overrides := []Override{{Name: "patrol", Rig: "ghost"}}
	err := ApplyOverrides(aa, overrides)
	if err == nil {
		t.Fatal("expected error for unknown rig, got nil")
	}
	if !strings.Contains(err.Error(), `"patrol"`) || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("error = %v, want both name and rig quoted", err)
	}
}

func TestApplyOverridesRiglessOnPerRigOnlyHasEnrichedError(t *testing.T) {
	// THE BUG: rigless override targets a name that exists only as per-rig
	// instances. Today this silently no-ops; with the fix it returns an
	// error that mentions the rig-scoping requirement and lists matching
	// rigs so the user can copy-paste.
	aa := []Order{
		{Name: "patrol-project-leads", Rig: "alpha"},
		{Name: "patrol-project-leads", Rig: "beta"},
		{Name: "patrol-project-leads", Rig: "gamma"},
	}
	overrides := []Override{{Name: "patrol-project-leads", Enabled: boolPtr(false)}}
	err := ApplyOverrides(aa, overrides)
	if err == nil {
		t.Fatal("expected error for rigless override on per-rig-only name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"patrol-project-leads"`) {
		t.Errorf("error = %v, want quoted order name", err)
	}
	if !strings.Contains(msg, "city scope") {
		t.Errorf("error = %v, want 'city scope' clarification", err)
	}
	if !strings.Contains(msg, "per-rig") {
		t.Errorf("error = %v, want 'per-rig' hint", err)
	}
	if !strings.Contains(msg, `rig =`) {
		t.Errorf("error = %v, want 'rig = ...' usage hint", err)
	}
	// Should mention at least one of the actual rig names so the user can
	// copy-paste the fix.
	mentioned := 0
	for _, rig := range []string{"alpha", "beta", "gamma"} {
		if strings.Contains(msg, rig) {
			mentioned++
		}
	}
	if mentioned == 0 {
		t.Errorf("error = %v, want at least one matching rig name (alpha/beta/gamma) listed", err)
	}
}

func TestApplyOverridesPreservesOrderOnSecondCallWithEmptyOverrides(t *testing.T) {
	aa := []Order{{Name: "patrol"}}
	if err := ApplyOverrides(aa, nil); err != nil {
		t.Fatalf("nil overrides: %v", err)
	}
	if err := ApplyOverrides(aa, []Override{}); err != nil {
		t.Fatalf("empty overrides: %v", err)
	}
}
