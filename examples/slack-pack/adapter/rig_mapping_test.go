package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRigMappingRegistryRoundTripWithSlingTargetAndFixFormula pins the
// new fields (cby.18.a) — round-trip on disk preserves the values
// written by `gc slack map-rig --sling-target ... --fix-formula ...`.
func TestRigMappingRegistryRoundTripWithSlingTargetAndFixFormula(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "alpha/polecat",
		FixFormula:  "mol-slack-fix-issue",
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := reg.Set(rec); err != nil {
		t.Fatalf("Set: %v", err)
	}
	reg2, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, _, ok := reg2.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("LookupRigForChannel ok=false after reload")
	}
	if got.SlingTarget != "alpha/polecat" {
		t.Errorf("SlingTarget = %q, want alpha/polecat", got.SlingTarget)
	}
	if got.FixFormula != "mol-slack-fix-issue" {
		t.Errorf("FixFormula = %q, want mol-slack-fix-issue", got.FixFormula)
	}
}

// TestRigMappingRegistryLoadsLegacyRecord covers the tolerance
// contract: a legacy rig_mappings.json with no sling_target /
// fix_formula keys must still load.
func TestRigMappingRegistryLoadsLegacyRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	legacy := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":["C1"],"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatalf("legacy record load: %v", err)
	}
	rec, _, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("legacy record missing")
	}
	if rec.SlingTarget != "" || rec.FixFormula != "" {
		t.Errorf("expected empty sling_target/fix_formula on legacy record, got %q / %q",
			rec.SlingTarget, rec.FixFormula)
	}
}

// TestResolveSlingTargetReturnsErrorWhenSlingTargetEmpty exercises the
// resolution-time error contract: legacy records (or partially-
// configured rigs) MUST surface a fix-it message rather than route to
// an empty target.
func TestResolveSlingTargetReturnsErrorWhenSlingTargetEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		// no SlingTarget — simulate legacy record
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err = reg.ResolveSlingTarget("T1", "alpha")
	if err == nil {
		t.Fatal("expected error when sling_target is empty, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"sling target", "gc slack map-rig", "--sling-target"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing substring %q", msg, want)
		}
	}
}

// TestResolveSlingTargetSucceedsForConfiguredRig pins the success path:
// when sling_target is present, ResolveSlingTarget returns it (and the
// optional fix_formula default).
func TestResolveSlingTargetSucceedsForConfiguredRig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "alpha/polecat",
		FixFormula:  "mol-slack-fix-issue",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	target, fixFormula, err := reg.ResolveSlingTarget("T1", "alpha")
	if err != nil {
		t.Fatalf("ResolveSlingTarget: %v", err)
	}
	if target != "alpha/polecat" {
		t.Errorf("target = %q, want alpha/polecat", target)
	}
	if fixFormula != "mol-slack-fix-issue" {
		t.Errorf("fixFormula = %q, want mol-slack-fix-issue", fixFormula)
	}
}

// TestResolveSlingTargetReturnsErrorForUnknownRig pins the
// not-found path so the dispatch handler can surface a clear "no rig
// mapping" error rather than a zero-value silent success.
func TestResolveSlingTargetReturnsErrorForUnknownRig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.ResolveSlingTarget("T1", "ghost"); err == nil {
		t.Fatal("expected error for unknown rig, got nil")
	}
}
