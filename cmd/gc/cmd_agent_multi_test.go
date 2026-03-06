package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestResolveMultiInstance(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "researcher", Multi: true},
			{Name: "worker"},
		},
	}

	// Create an instance.
	reg.start("researcher", "spike-1") //nolint:errcheck

	// Explicit template/instance.
	tmpl, name, err := resolveMultiInstance(cfg, reg, "researcher/spike-1")
	if err != nil {
		t.Fatalf("explicit resolve: %v", err)
	}
	if tmpl != "researcher" || name != "spike-1" {
		t.Errorf("got template=%q name=%q, want researcher/spike-1", tmpl, name)
	}

	// Bare instance name.
	tmpl, name, err = resolveMultiInstance(cfg, reg, "spike-1")
	if err != nil {
		t.Fatalf("bare resolve: %v", err)
	}
	if tmpl != "researcher" || name != "spike-1" {
		t.Errorf("got template=%q name=%q, want researcher/spike-1", tmpl, name)
	}

	// Non-existent instance.
	_, _, err = resolveMultiInstance(cfg, reg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}

	// Non-multi template.
	_, _, err = resolveMultiInstance(cfg, reg, "worker/spike-1")
	if err == nil {
		t.Fatal("expected error for non-multi template")
	}
}

func TestResolveMultiInstanceAmbiguous(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "researcher", Multi: true},
			{Name: "analyzer", Multi: true},
		},
	}

	// Create same-named instance on both templates.
	reg.start("researcher", "shared-name") //nolint:errcheck
	reg.start("analyzer", "shared-name")   //nolint:errcheck

	_, _, err := resolveMultiInstance(cfg, reg, "shared-name")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestFindAgentByQualifiedMulti(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "researcher", Multi: true},
			{Name: "worker"},
		},
	}

	// Template itself should be found.
	a, ok := findAgentByQualified(cfg, "researcher")
	if !ok {
		t.Fatal("expected to find researcher template")
	}
	if !a.IsMulti() {
		t.Error("expected template to be multi")
	}

	// Instance pattern: template/instance.
	a, ok = findAgentByQualified(cfg, "researcher/spike-1")
	if !ok {
		t.Fatal("expected to find researcher/spike-1")
	}
	if a.Name != "spike-1" {
		t.Errorf("expected instance name spike-1, got %q", a.Name)
	}
	if a.IsMulti() {
		t.Error("instance should not be multi")
	}
	if a.PoolName != "researcher" {
		t.Errorf("instance PoolName should be template QN, got %q", a.PoolName)
	}
}

func TestCmdAgentStartNonMulti(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// This will fail because we can't resolve the city, but we can at least
	// verify the function exists and runs.
	code := cmdAgentStart("nonexistent", "", &stdout, &stderr)
	if code == 0 {
		t.Error("expected non-zero exit for missing city")
	}
}
