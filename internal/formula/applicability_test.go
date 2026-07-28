package formula

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

func TestParseTOMLApplicabilityDirection(t *testing.T) {
	for _, dir := range []Direction{DirectionIncoming, DirectionOutgoing} {
		t.Run(string(dir), func(t *testing.T) {
			p := NewParser()
			f, err := p.ParseTOML([]byte(`
formula = "review"

[applicability]
direction = "` + string(dir) + `"

[[steps]]
id = "review"
title = "Review"
`))
			if err != nil {
				t.Fatalf("ParseTOML failed: %v", err)
			}
			if f.Applicability == nil {
				t.Fatal("Applicability is nil")
			}
			if got := f.Applicability.Direction; got != dir {
				t.Fatalf("Direction = %q, want %q", got, dir)
			}
			if got := DirectionOf(f); got != dir {
				t.Fatalf("DirectionOf = %q, want %q", got, dir)
			}
		})
	}
}

func TestParseTOMLApplicabilityAbsentIsUnconstrained(t *testing.T) {
	p := NewParser()
	f, err := p.ParseTOML([]byte(`
formula = "review"

[[steps]]
id = "review"
title = "Review"
`))
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}
	if f.Applicability != nil {
		t.Fatalf("Applicability = %+v, want nil when undeclared", f.Applicability)
	}
	if got := DirectionOf(f); got != "" {
		t.Fatalf("DirectionOf = %q, want empty for undeclared formula", got)
	}
}

func TestParseTOMLApplicabilityRejectsInvalidValue(t *testing.T) {
	p := NewParser()
	_, err := p.ParseTOML([]byte(`
formula = "review"

[applicability]
direction = "sideways"

[[steps]]
id = "review"
title = "Review"
`))
	if err == nil {
		t.Fatal("ParseTOML unexpectedly succeeded")
	}
	requireErrorContains(t, err, "formula.applicability_direction_invalid")
	requireErrorContains(t, err, `got "sideways"`)
}

func TestParseTOMLApplicabilityRejectsEmptyValue(t *testing.T) {
	p := NewParser()
	_, err := p.ParseTOML([]byte(`
formula = "review"

[applicability]
direction = ""

[[steps]]
id = "review"
title = "Review"
`))
	if err == nil {
		t.Fatal("ParseTOML unexpectedly succeeded on empty direction")
	}
	requireErrorContains(t, err, "formula.applicability_direction_invalid")
}

func TestParseTOMLApplicabilityRejectsUnknownKey(t *testing.T) {
	p := NewParser()
	_, err := p.ParseTOML([]byte(`
formula = "review"

[applicability]
scope = "incoming"

[[steps]]
id = "review"
title = "Review"
`))
	if err == nil {
		t.Fatal("ParseTOML unexpectedly succeeded")
	}
	requireErrorContains(t, err, "formula.applicability_unknown")
	requireErrorContains(t, err, `unknown formula applicability constraint "scope"`)
}

func TestParseJSONApplicabilityDirection(t *testing.T) {
	p := NewParser()
	f, err := p.Parse([]byte(`{
  "formula": "review",
  "applicability": {"direction": "outgoing"},
  "steps": [{"id": "review", "title": "Review"}]
}`))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if f.Applicability == nil || f.Applicability.Direction != DirectionOutgoing {
		t.Fatalf("Applicability = %+v, want direction outgoing", f.Applicability)
	}
}

func TestParseJSONApplicabilityRejectsInvalidValue(t *testing.T) {
	p := NewParser()
	_, err := p.Parse([]byte(`{
  "formula": "review",
  "applicability": {"direction": "sideways"},
  "steps": [{"id": "review", "title": "Review"}]
}`))
	if err == nil {
		t.Fatal("Parse unexpectedly succeeded")
	}
	requireErrorContains(t, err, "formula.applicability_direction_invalid")
}

func TestParseJSONApplicabilityRejectsUnknownKey(t *testing.T) {
	p := NewParser()
	_, err := p.Parse([]byte(`{
  "formula": "review",
  "applicability": {"scope": "incoming"},
  "steps": [{"id": "review", "title": "Review"}]
}`))
	if err == nil {
		t.Fatal("Parse unexpectedly succeeded")
	}
	requireErrorContains(t, err, "formula.applicability_unknown")
}

func TestParseDirection(t *testing.T) {
	tests := []struct {
		raw     string
		want    Direction
		wantErr bool
	}{
		{"incoming", DirectionIncoming, false},
		{"outgoing", DirectionOutgoing, false},
		{"", "", true},
		{"sideways", "", true},
		{"Incoming", "", true}, // case-sensitive: not a valid literal
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseDirection(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDirection(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDirection(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDirection(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDirectionsCompatible(t *testing.T) {
	tests := []struct {
		name       string
		formulaDir Direction
		goalDir    Direction
		want       bool
	}{
		{"exact match incoming", DirectionIncoming, DirectionIncoming, true},
		{"exact match outgoing", DirectionOutgoing, DirectionOutgoing, true},
		{"mismatch incoming vs outgoing", DirectionIncoming, DirectionOutgoing, false},
		{"mismatch outgoing vs incoming", DirectionOutgoing, DirectionIncoming, false},
		{"formula missing", "", DirectionIncoming, true},
		{"goal missing", DirectionOutgoing, "", true},
		{"both missing", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DirectionsCompatible(tt.formulaDir, tt.goalDir); got != tt.want {
				t.Fatalf("DirectionsCompatible(%q, %q) = %v, want %v", tt.formulaDir, tt.goalDir, got, tt.want)
			}
		})
	}
}

func TestGoalDirection(t *testing.T) {
	t.Run("absent key is unconstrained", func(t *testing.T) {
		dir, err := GoalDirection(map[string]string{"gc.kind": "run"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != "" {
			t.Fatalf("GoalDirection = %q, want empty", dir)
		}
	})
	t.Run("nil metadata is unconstrained", func(t *testing.T) {
		dir, err := GoalDirection(nil)
		if err != nil || dir != "" {
			t.Fatalf("GoalDirection(nil) = (%q, %v), want (\"\", nil)", dir, err)
		}
	})
	t.Run("empty value is unconstrained", func(t *testing.T) {
		dir, err := GoalDirection(map[string]string{beadmeta.ApplicabilityDirectionMetadataKey: ""})
		if err != nil || dir != "" {
			t.Fatalf("GoalDirection(empty) = (%q, %v), want (\"\", nil)", dir, err)
		}
	})
	t.Run("valid value parses", func(t *testing.T) {
		dir, err := GoalDirection(map[string]string{beadmeta.ApplicabilityDirectionMetadataKey: "incoming"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != DirectionIncoming {
			t.Fatalf("GoalDirection = %q, want incoming", dir)
		}
	})
	t.Run("invalid value fails closed", func(t *testing.T) {
		_, err := GoalDirection(map[string]string{beadmeta.ApplicabilityDirectionMetadataKey: "sideways"})
		if err == nil {
			t.Fatal("GoalDirection unexpectedly succeeded on invalid value")
		}
		requireErrorContains(t, err, "formula.applicability_direction_invalid")
	})
}

func TestValidateRejectsProgrammaticInvalidDirection(t *testing.T) {
	f := &Formula{
		Formula:       "review",
		Applicability: &Applicability{Direction: Direction("bogus")},
		Steps:         []*Step{{ID: "review", Title: "Review"}},
	}
	err := f.Validate()
	if err == nil {
		t.Fatal("Validate unexpectedly succeeded on invalid programmatic direction")
	}
	requireErrorContains(t, err, "formula.applicability_direction_invalid")
}

func TestValidateAllowsEmptyProgrammaticApplicability(t *testing.T) {
	// An Applicability with no direction places no constraint and must validate,
	// matching the matcher's treatment of the empty direction as unconstrained.
	f := &Formula{
		Formula:       "review",
		Applicability: &Applicability{},
		Steps:         []*Step{{ID: "review", Title: "Review"}},
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate rejected empty applicability: %v", err)
	}
}

func TestLoadFormulaDirection(t *testing.T) {
	dir := t.TempDir()
	withDir := `{
  "formula": "mol-pr-review",
  "type": "workflow",
  "applicability": {"direction": "incoming"},
  "steps": [{"id": "review", "title": "Review"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "mol-pr-review.formula.json"), []byte(withDir), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}
	noDir := `{
  "formula": "mol-generic",
  "type": "workflow",
  "steps": [{"id": "work", "title": "Work"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "mol-generic.formula.json"), []byte(noDir), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}

	got, err := LoadFormulaDirection("mol-pr-review", []string{dir})
	if err != nil {
		t.Fatalf("LoadFormulaDirection: %v", err)
	}
	if got != DirectionIncoming {
		t.Fatalf("LoadFormulaDirection(mol-pr-review) = %q, want incoming", got)
	}

	got, err = LoadFormulaDirection("mol-generic", []string{dir})
	if err != nil {
		t.Fatalf("LoadFormulaDirection: %v", err)
	}
	if got != "" {
		t.Fatalf("LoadFormulaDirection(mol-generic) = %q, want empty", got)
	}
}

func TestApplicabilityInheritedViaExtends(t *testing.T) {
	dir := t.TempDir()
	parent := `{
  "formula": "base-review",
  "type": "workflow",
  "applicability": {"direction": "incoming"},
  "steps": [{"id": "init", "title": "Init"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "base-review.formula.json"), []byte(parent), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	child := `{
  "formula": "child-review",
  "type": "workflow",
  "extends": ["base-review"],
  "steps": [{"id": "run", "title": "Run", "depends_on": ["init"]}]
}`
	childPath := filepath.Join(dir, "child-review.formula.json")
	if err := os.WriteFile(childPath, []byte(child), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	p := NewParser(dir)
	f, err := p.ParseFile(childPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	resolved, err := p.Resolve(f)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := DirectionOf(resolved); got != DirectionIncoming {
		t.Fatalf("inherited DirectionOf = %q, want incoming", got)
	}
}

func TestApplicabilityChildOverridesParent(t *testing.T) {
	dir := t.TempDir()
	parent := `{
  "formula": "base-review",
  "type": "workflow",
  "applicability": {"direction": "incoming"},
  "steps": [{"id": "init", "title": "Init"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "base-review.formula.json"), []byte(parent), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	child := `{
  "formula": "child-review",
  "type": "workflow",
  "extends": ["base-review"],
  "applicability": {"direction": "outgoing"},
  "steps": [{"id": "run", "title": "Run", "depends_on": ["init"]}]
}`
	childPath := filepath.Join(dir, "child-review.formula.json")
	if err := os.WriteFile(childPath, []byte(child), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	p := NewParser(dir)
	f, err := p.ParseFile(childPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	resolved, err := p.Resolve(f)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := DirectionOf(resolved); got != DirectionOutgoing {
		t.Fatalf("child-declared DirectionOf = %q, want outgoing (child wins)", got)
	}
}
