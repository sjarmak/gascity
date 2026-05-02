// Package oversight_test validates the Oversight pack.
//
// The pack ships formulas, orders, prompt templates, and a delivery
// script. These tests exercise the parse/validate paths the SDK uses
// at city startup so the pack stays ship-shape as the SDK evolves.
package oversight_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/orders"
)

// packDir returns the absolute path to the oversight pack root.
func packDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}

func TestPackTomlParses(t *testing.T) {
	var pack struct {
		Pack struct {
			Name   string `toml:"name"`
			Schema int    `toml:"schema"`
		} `toml:"pack"`
		NamedSession []struct {
			Template string `toml:"template"`
			Scope    string `toml:"scope"`
			Mode     string `toml:"mode"`
		} `toml:"named_session"`
	}
	data, err := os.ReadFile(filepath.Join(packDir(), "pack.toml"))
	if err != nil {
		t.Fatalf("read pack.toml: %v", err)
	}
	if err := toml.Unmarshal(data, &pack); err != nil {
		t.Fatalf("parse pack.toml: %v", err)
	}
	if pack.Pack.Name != "oversight" {
		t.Errorf("pack.name = %q, want %q", pack.Pack.Name, "oversight")
	}
	if pack.Pack.Schema != 2 {
		t.Errorf("pack.schema = %d, want 2", pack.Pack.Schema)
	}
	if len(pack.NamedSession) != 1 {
		t.Fatalf("named_session count = %d, want 1", len(pack.NamedSession))
	}
	ns := pack.NamedSession[0]
	if ns.Template != "chief-of-staff" || ns.Scope != "city" || ns.Mode != "always" {
		t.Errorf("named_session[0] = %+v, want chief-of-staff/city/always", ns)
	}
}

func TestEscalateBlockedFormulaParsesAndValidates(t *testing.T) {
	path := filepath.Join(packDir(), "formulas", "escalate-blocked.toml")
	parser := formula.NewParser(filepath.Dir(path))
	f, err := parser.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if f.Formula != "mol-escalate-blocked" {
		t.Errorf("Formula = %q, want mol-escalate-blocked", f.Formula)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if len(f.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(f.Steps))
	}
	if f.Steps[0].ID != "triage" {
		t.Errorf("Steps[0].ID = %q, want triage", f.Steps[0].ID)
	}
}

func TestOrdersScanCleanly(t *testing.T) {
	formulasDir := filepath.Join(packDir(), "formulas")
	got, err := orders.Scan(fsys.OSFS{}, []string{formulasDir}, nil)
	if err != nil {
		t.Fatalf("orders.Scan: %v", err)
	}

	// Index by name so order-of-discovery is irrelevant.
	byName := make(map[string]orders.Order, len(got))
	for _, o := range got {
		byName[o.Name] = o
	}

	want := map[string]struct {
		trigger string
		formula string
		exec    bool
	}{
		"escalate-blocked":     {trigger: "cooldown", formula: "mol-escalate-blocked"},
		"deliver-escalations":  {trigger: "cooldown", exec: true},
	}

	for name, w := range want {
		o, ok := byName[name]
		if !ok {
			t.Errorf("missing order %q", name)
			continue
		}
		if o.Trigger != w.trigger {
			t.Errorf("order %q trigger = %q, want %q", name, o.Trigger, w.trigger)
		}
		if w.exec && o.Exec == "" {
			t.Errorf("order %q expected exec, got formula=%q", name, o.Formula)
		}
		if !w.exec && o.Formula != w.formula {
			t.Errorf("order %q formula = %q, want %q", name, o.Formula, w.formula)
		}
	}
}

func TestPromptTemplatesExist(t *testing.T) {
	for _, role := range []string{"chief-of-staff", "project-mayor"} {
		path := filepath.Join(packDir(), "agents", role, "prompt.template.md")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("agent %q: prompt.template.md: %v", role, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("agent %q: prompt.template.md is empty", role)
		}
	}
}

func TestAgentTomlsParse(t *testing.T) {
	for _, role := range []string{"chief-of-staff", "project-mayor"} {
		path := filepath.Join(packDir(), "agents", role, "agent.toml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("agent %q: read agent.toml: %v", role, err)
			continue
		}
		var a struct {
			Scope       string `toml:"scope"`
			Nudge       string `toml:"nudge"`
			IdleTimeout string `toml:"idle_timeout"`
		}
		if err := toml.Unmarshal(data, &a); err != nil {
			t.Errorf("agent %q: parse agent.toml: %v", role, err)
			continue
		}
		if a.Scope != "city" {
			t.Errorf("agent %q: scope = %q, want city", role, a.Scope)
		}
		if a.Nudge == "" {
			t.Errorf("agent %q: nudge is required", role)
		}
	}
}

func TestDeliveryScriptIsExecutable(t *testing.T) {
	path := filepath.Join(packDir(), "assets", "scripts", "deliver-escalation.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat deliver-escalation.sh: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("deliver-escalation.sh is not executable (mode = %v)", info.Mode())
	}
}
