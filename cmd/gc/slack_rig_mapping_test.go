package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSlackRigMappingsPathIsCityRooted pins the on-disk location to
// <cityPath>/.gc/slack/rig_mappings.json so the adapter and CLI agree.
func TestSlackRigMappingsPathIsCityRooted(t *testing.T) {
	cityRoot := newTestCity(t)
	got := slackRigMappingsPath(cityRoot)
	want := filepath.Join(cityRoot, ".gc", "slack", "rig_mappings.json")
	if got != want {
		t.Errorf("slackRigMappingsPath(%q) = %q, want %q", cityRoot, got, want)
	}
}

func TestSlackRigMappingRegistryTolerantLoadOnMissingFile(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatalf("newSlackRigMappingRegistry on missing file: %v", err)
	}
	if got := len(reg.AllSorted()); got != 0 {
		t.Errorf("fresh registry: AllSorted() len = %d, want 0", got)
	}
	if _, _, ok := reg.LookupRigForChannel("T1", "C1"); ok {
		t.Errorf("fresh registry LookupRigForChannel returned ok=true, want false")
	}
}

func TestSlackRigMappingRegistrySetAndLookup(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C2", "C1"},
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := reg.Set(rec); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, src, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatalf("LookupRigForChannel(T1,C1) ok=false, want true")
	}
	if src != "rig" {
		t.Errorf("source = %q, want %q", src, "rig")
	}
	if got.RigName != "alpha" {
		t.Errorf("RigName = %q, want alpha", got.RigName)
	}
	// Channels should be sorted+deduped after Set.
	if len(got.ChannelIDs) != 2 || got.ChannelIDs[0] != "C1" || got.ChannelIDs[1] != "C2" {
		t.Errorf("ChannelIDs = %v, want [C1 C2] (sorted)", got.ChannelIDs)
	}
}

func TestSlackRigMappingRegistryRequiredFields(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cases := []slackRigMappingRecord{
		{WorkspaceID: "", RigName: "alpha", ChannelIDs: []string{"C1"}, CreatedAt: now, UpdatedAt: now},
		{WorkspaceID: "T1", RigName: "", ChannelIDs: []string{"C1"}, CreatedAt: now, UpdatedAt: now},
		{WorkspaceID: "T1", RigName: "alpha", ChannelIDs: []string{}, CreatedAt: now, UpdatedAt: now},
		{WorkspaceID: "T1", RigName: "alpha", ChannelIDs: nil, CreatedAt: now, UpdatedAt: now},
		// All-empty channel after dedup → reject.
		{WorkspaceID: "T1", RigName: "alpha", ChannelIDs: []string{""}, CreatedAt: now, UpdatedAt: now},
	}
	for _, rec := range cases {
		if err := reg.Set(rec); err == nil {
			t.Errorf("Set(%+v): expected error, got nil", rec)
		}
	}
}

func TestSlackRigMappingRegistryRejectsInvalidRigName(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bad := []string{
		"alpha beta",  // whitespace
		"alpha\tbeta", // tab
		"alpha/beta",  // slash
		"alpha\\beta", // backslash
		"alpha\nbeta", // newline (control char)
		"alpha\x00",   // null
	}
	for _, name := range bad {
		err := reg.Set(slackRigMappingRecord{
			WorkspaceID: "T1", RigName: name,
			ChannelIDs: []string{"C1"},
			CreatedAt:  now, UpdatedAt: now,
		})
		if err == nil {
			t.Errorf("Set rig_name=%q: expected error, got nil", name)
		}
	}
}

func TestSlackRigMappingRegistryDedupesAndSortsChannels(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C2", "C1", "C2", "C3"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := reg.LookupRigForChannel("T1", "C1")
	if len(rec.ChannelIDs) != 3 {
		t.Fatalf("dedup failed: %v", rec.ChannelIDs)
	}
	for i, want := range []string{"C1", "C2", "C3"} {
		if rec.ChannelIDs[i] != want {
			t.Errorf("ChannelIDs[%d] = %q, want %q", i, rec.ChannelIDs[i], want)
		}
	}
}

func TestSlackRigMappingRegistryIdempotentReSet(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-time.Hour)
	t1 := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1", "C2"},
		CreatedAt:  t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C3", "C4"},
		CreatedAt:  t1, // caller passes t1 but registry must preserve t0
		UpdatedAt:  t1,
	}); err != nil {
		t.Fatal(err)
	}
	all := reg.AllSorted()
	if len(all) != 1 {
		t.Fatalf("re-set grew registry: %d records", len(all))
	}
	rec := all[0]
	if !rec.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want preserved t0=%v", rec.CreatedAt, t0)
	}
	if !rec.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want t1=%v", rec.UpdatedAt, t1)
	}
	if rec.ChannelIDs[0] != "C3" || rec.ChannelIDs[1] != "C4" {
		t.Errorf("ChannelIDs = %v, want [C3 C4]", rec.ChannelIDs)
	}
	// byChannel must reflect the replacement: C1/C2 dropped, C3/C4 added.
	if _, _, ok := reg.LookupRigForChannel("T1", "C1"); ok {
		t.Errorf("byChannel still has C1 after replacement")
	}
	if _, _, ok := reg.LookupRigForChannel("T1", "C3"); !ok {
		t.Errorf("byChannel missing C3 after replacement")
	}
}

func TestSlackRigMappingRegistryRejectsCrossRigOverlap(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	err = reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "beta",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for cross-rig overlap, got nil")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error should mention conflicting rig %q: %v", "alpha", err)
	}
}

func TestSlackRigMappingRegistryRemove(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1", "C2"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	existed, err := reg.Remove("T1", "alpha")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !existed {
		t.Errorf("first Remove existed=false, want true")
	}
	if _, _, ok := reg.LookupRigForChannel("T1", "C1"); ok {
		t.Errorf("byChannel still has C1 after Remove")
	}
	// Idempotent.
	existed, err = reg.Remove("T1", "alpha")
	if err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if existed {
		t.Errorf("second Remove existed=true, want false")
	}

	// Reload to confirm persistence.
	reg2, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reg2.LookupRigForChannel("T1", "C1"); ok {
		t.Errorf("after reload Get ok=true, want false (deletion not persisted)")
	}
}

func TestSlackRigMappingRegistryAllSorted(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, k := range []struct{ ws, rig string }{
		{"T2", "z"}, {"T1", "b"}, {"T1", "a"}, {"T2", "a"},
	} {
		if err := reg.Set(slackRigMappingRecord{
			WorkspaceID: k.ws, RigName: k.rig,
			ChannelIDs: []string{"C-" + k.ws + "-" + k.rig},
			CreatedAt:  now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got := reg.AllSorted()
	want := []string{"T1:a", "T1:b", "T2:a", "T2:z"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		k := got[i].WorkspaceID + ":" + got[i].RigName
		if k != w {
			t.Errorf("AllSorted()[%d] = %q, want %q", i, k, w)
		}
	}
}

func TestSlackRigMappingRegistryFilePermissions(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	reg, err := newSlackRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("rig_mappings.json mode = %o, want 0600", mode)
	}
	dirfi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirfi.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir mode = %o, want 0700", mode)
	}
}

func TestSlackRigMappingRegistryAtomicWriteCleansTmp(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	reg, err := newSlackRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := reg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("stray tmp file lingered: %s", e.Name())
		}
	}
}

func TestSlackRigMappingRegistryConcurrentSets(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	reg, err := newSlackRigMappingRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rig := "rig-" + string(rune('a'+i))
			if err := reg.Set(slackRigMappingRecord{
				WorkspaceID: "T1", RigName: rig,
				ChannelIDs: []string{"C-" + rig},
				CreatedAt:  now, UpdatedAt: now,
			}); err != nil {
				t.Errorf("concurrent Set: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(reg.AllSorted()); got != 10 {
		t.Errorf("concurrent Sets: All() len = %d, want 10", got)
	}
}

func TestSlackRigMappingRegistryRejectsCorruptFile(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Unknown field should be rejected via DisallowUnknownFields.
	if err := os.WriteFile(path, []byte(`{"T1:alpha":{"workspace_id":"T1","rig_name":"alpha","channel_ids":["C1"],"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z","bogus":42}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSlackRigMappingRegistry(path); err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestSlackRigMappingRegistryRejectsEmptyChannelIDsOnLoad(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := map[string]slackRigMappingRecord{
		"T1:alpha": {
			WorkspaceID: "T1", RigName: "alpha",
			ChannelIDs: []string{},
			CreatedAt:  time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}
	data, _ := json.MarshalIndent(corrupt, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSlackRigMappingRegistry(path); err == nil {
		t.Fatal("expected error for empty channel_ids on load")
	}
}

func TestSlackRigMappingRegistryLoadWarnsOnHandEditedOverlap(t *testing.T) {
	// A hand-edited file with overlapping channels across two records:
	// load succeeds (we can't refuse to start the CLI on an operator
	// edit), but the byChannel index keeps the first-by-sorted-key as
	// winner so subsequent lookups are deterministic.
	cityRoot := newTestCity(t)
	path := slackRigMappingsPath(cityRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	corrupt := map[string]slackRigMappingRecord{
		"T1:alpha": {
			WorkspaceID: "T1", RigName: "alpha",
			ChannelIDs: []string{"C1"},
			CreatedAt:  now, UpdatedAt: now,
		},
		"T1:beta": {
			WorkspaceID: "T1", RigName: "beta",
			ChannelIDs: []string{"C1"},
			CreatedAt:  now, UpdatedAt: now,
		},
	}
	data, _ := json.MarshalIndent(corrupt, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := newSlackRigMappingRegistry(path)
	if err != nil {
		t.Fatalf("load with overlap should succeed (with WARN): %v", err)
	}
	rec, _, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("byChannel did not survive overlap WARN")
	}
	// First-by-sorted-key wins. T1:alpha < T1:beta lexicographically.
	if rec.RigName != "alpha" {
		t.Errorf("overlap winner = %q, want alpha (first-by-sorted-key)", rec.RigName)
	}
}
