// Package oversightrig_test validates the rig-scoped oversight pack.
//
// The pack ships rig-scoped project-lead and city-scoped chief-of-staff
// agents, plus a deterministic outbound delivery loop. These tests
// exercise the parse/validate paths the SDK uses at city startup.
package oversightrig_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/orders"
)

// packDir returns the absolute path to the oversight-rig pack root.
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
	if pack.Pack.Name != "oversight-rig" {
		t.Errorf("pack.name = %q, want oversight-rig", pack.Pack.Name)
	}
	if pack.Pack.Schema != 2 {
		t.Errorf("pack.schema = %d, want 2", pack.Pack.Schema)
	}

	want := map[string]struct {
		scope string
		mode  string
	}{
		"chief-of-staff": {scope: "city", mode: "on_demand"},
		"project-lead":   {scope: "rig", mode: "always"},
	}
	if len(pack.NamedSession) != len(want) {
		t.Fatalf("named_session count = %d, want %d", len(pack.NamedSession), len(want))
	}
	for _, ns := range pack.NamedSession {
		w, ok := want[ns.Template]
		if !ok {
			t.Errorf("unexpected named_session %q", ns.Template)
			continue
		}
		if ns.Scope != w.scope {
			t.Errorf("%s scope = %q, want %q", ns.Template, ns.Scope, w.scope)
		}
		if ns.Mode != w.mode {
			t.Errorf("%s mode = %q, want %q", ns.Template, ns.Mode, w.mode)
		}
	}
}

func TestOrdersScanCleanly(t *testing.T) {
	formulasDir := filepath.Join(packDir(), "formulas")
	got, err := orders.Scan(fsys.OSFS{}, []string{formulasDir}, nil)
	if err != nil {
		t.Fatalf("orders.Scan: %v", err)
	}

	byName := make(map[string]orders.Order, len(got))
	for _, o := range got {
		byName[o.Name] = o
	}

	want := map[string]struct {
		trigger string
	}{
		"escalate-rollups":     {trigger: "condition"},
		"patrol-project-leads": {trigger: "cooldown"},
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
		if o.Exec == "" {
			t.Errorf("order %q expected exec command, got formula=%q", name, o.Formula)
		}
	}
}

func TestEscalateRollupsHasConditionCheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(packDir(), "orders", "escalate-rollups.toml"))
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	var f struct {
		Order struct {
			Trigger string `toml:"trigger"`
			Check   string `toml:"check"`
			Exec    string `toml:"exec"`
		} `toml:"order"`
	}
	if err := toml.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Order.Trigger != "condition" {
		t.Errorf("trigger = %q, want condition", f.Order.Trigger)
	}
	if f.Order.Check == "" {
		t.Error("condition trigger requires a check command")
	}
	if f.Order.Exec == "" {
		t.Error("order requires an exec command")
	}
}

func TestPromptTemplatesExist(t *testing.T) {
	for _, role := range []string{"chief-of-staff", "project-lead"} {
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

func TestProjectBriefTemplateExists(t *testing.T) {
	path := filepath.Join(packDir(), "agents", "project-lead", "project-brief.template.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("project-brief.template.md: %v", err)
	}
	if info.Size() == 0 {
		t.Error("project-brief.template.md is empty")
	}
}

func TestAgentTomlsParseWithRigScope(t *testing.T) {
	cases := map[string]string{
		"chief-of-staff": "city",
		"project-lead":   "rig",
	}
	for role, wantScope := range cases {
		path := filepath.Join(packDir(), "agents", role, "agent.toml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("agent %q: read agent.toml: %v", role, err)
			continue
		}
		var a struct {
			Scope   string `toml:"scope"`
			Nudge   string `toml:"nudge"`
			WorkDir string `toml:"work_dir"`
		}
		if err := toml.Unmarshal(data, &a); err != nil {
			t.Errorf("agent %q: parse: %v", role, err)
			continue
		}
		if a.Scope != wantScope {
			t.Errorf("agent %q: scope = %q, want %q", role, a.Scope, wantScope)
		}
		if a.Nudge == "" {
			t.Errorf("agent %q: nudge is required", role)
		}
		if role == "project-lead" && a.WorkDir == "" {
			t.Error("project-lead: work_dir is required (templates expect it)")
		}
	}
}

func TestScriptsExecutable(t *testing.T) {
	for _, name := range []string{
		"deliver-rollup.sh",
		"has-undelivered-escalates.sh",
		"nudge-project-leads.sh",
	} {
		path := filepath.Join(packDir(), "assets", "scripts", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("script %q: %v", name, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("script %q: not executable (mode = %v)", name, info.Mode())
		}
	}
}
