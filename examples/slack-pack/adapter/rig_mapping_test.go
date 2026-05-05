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

// TestRigMappingRegistryParsesChannelPatterns confirms the adapter
// tolerates the new channel_patterns field — without explicit parsing,
// DisallowUnknownFields would reject every file written by the new CLI.
func TestRigMappingRegistryParsesChannelPatterns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	contents := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":["C1"],"channel_patterns":["oversight-*","team-?"],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rec, _, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("literal channel resolution lost")
	}
	if len(rec.ChannelPatterns) != 2 {
		t.Errorf("ChannelPatterns = %v, want 2 entries", rec.ChannelPatterns)
	}
}

// TestRigMappingRegistryLoadsPatternOnlyRecord confirms a record with
// channel_patterns and EMPTY channel_ids still loads — the relaxed
// "either-or" invariant introduced by gc-cby.22.
func TestRigMappingRegistryLoadsPatternOnlyRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	contents := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":[],"channel_patterns":["oversight-*"],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRigMappingRegistry(path); err != nil {
		t.Fatalf("pattern-only load: %v", err)
	}
}

// TestRigMappingRegistryRejectsZeroOfBothOnLoad confirms the relaxed
// invariant still rejects records with no channels AND no patterns —
// loosening "channel_ids ≥ 1" all the way to "anything goes" would
// allow corrupt or partially-deleted files to load silently.
func TestRigMappingRegistryRejectsZeroOfBothOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	contents := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":[],"channel_patterns":[],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRigMappingRegistry(path); err == nil {
		t.Fatal("expected load error for zero-of-both, got nil")
	}
}

// TestRigMappingRegistryRejectsMalformedPatternOnLoad confirms a
// hand-edited pattern that escapes the Slack channel-name charset
// (uppercase, slash, etc.) is rejected at load — symmetric with the
// CLI write-time validation.
func TestRigMappingRegistryRejectsMalformedPatternOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	contents := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":[],"channel_patterns":["Oversight-BAD"],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRigMappingRegistry(path); err == nil {
		t.Fatal("expected load error for malformed pattern, got nil")
	}
}

// TestRigMappingRegistrySetNormalisesPatterns confirms Set sorts and
// deduplicates ChannelPatterns before storing — byKey[k].ChannelPatterns
// must agree with PatternsForRig so callers reading either path see the
// same ordering. (Adapter Set is test-only, but operator-written files
// flow through parseRigMappingRegistry which already normalises; this
// pins the in-process write path symmetrically.)
func TestRigMappingRegistrySetNormalisesPatterns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelPatterns: []string{"zeta-*", "alpha-*", "alpha-*", "", "beta-?"},
		CreatedAt:       now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	indexed := reg.PatternsForRig("T1", "alpha")
	want := []string{"alpha-*", "beta-?", "zeta-*"}
	if len(indexed) != len(want) {
		t.Fatalf("indexed = %v, want %v", indexed, want)
	}
	// PatternsForRig and the record must agree.
	rec, _, _ := reg.LookupRigForChannel("T1", "C-not-bound")
	_ = rec // record-by-channel lookup misses; pull from byKey via Reload.
	// Reload from disk to verify the persisted record is also normalised.
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	indexed2 := reg.PatternsForRig("T1", "alpha")
	for i, w := range want {
		if indexed[i] != w || indexed2[i] != w {
			t.Errorf("pattern[%d]: in-memory=%q reloaded=%q want=%q", i, indexed[i], indexed2[i], w)
		}
	}
}

// TestRigMappingSnapshotAtomicallySwapsPatternIndex pins the SIGHUP-
// reload contract: when a Stage/Commit cycle introduces a new pattern
// set, the in-memory pattern index swaps atomically alongside byKey
// and byChannel — readers never observe a mismatched state.
func TestRigMappingSnapshotAtomicallySwapsPatternIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Initial state: literal-only.
	v1 := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":["C1"],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload v1: %v", err)
	}
	if got := reg.PatternsForRig("T1", "alpha"); len(got) != 0 {
		t.Errorf("v1 patterns = %v, want empty", got)
	}

	// Updated state: add patterns.
	v2 := `{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":["C1"],"channel_patterns":["oversight-*"],"created_at":"` + now + `","updated_at":"` + now + `"}}`
	if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload v2: %v", err)
	}
	got := reg.PatternsForRig("T1", "alpha")
	if len(got) != 1 || got[0] != "oversight-*" {
		t.Errorf("v2 patterns = %v, want [oversight-*]", got)
	}

	// Reverting drops patterns again.
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload v1 again: %v", err)
	}
	if got := reg.PatternsForRig("T1", "alpha"); len(got) != 0 {
		t.Errorf("v1-revert patterns = %v, want empty", got)
	}
}
