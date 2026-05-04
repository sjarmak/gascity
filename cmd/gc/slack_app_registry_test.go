package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newTestCity creates a minimal city directory (with city.toml marker)
// rooted at t.TempDir() and returns its absolute path. Mirrors the
// minimum shape the rest of cmd/gc city tests rely on.
func newTestCity(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cityRoot := filepath.Join(dir, "testcity")
	if err := os.MkdirAll(cityRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cityRoot, "city.toml"),
		[]byte("[workspace]\nname = \"test\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	return cityRoot
}

func TestSlackAppsRegistryPathIsCityRooted(t *testing.T) {
	cityRoot := newTestCity(t)
	got := slackAppsRegistryPath(cityRoot)
	want := filepath.Join(cityRoot, ".gc", "slack", "apps.json")
	if got != want {
		t.Errorf("slackAppsRegistryPath(%q) = %q, want %q", cityRoot, got, want)
	}
}

func TestSlackAppRegistryTolerantLoadOnMissingFile(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)

	reg, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatalf("newSlackAppRegistry on missing file: unexpected error: %v", err)
	}
	if got := len(reg.All()); got != 0 {
		t.Errorf("fresh registry: All() len = %d, want 0", got)
	}
	if _, ok := reg.Get("T123", "A456"); ok {
		t.Errorf("fresh registry: Get returned ok=true, want false")
	}
}

func TestSlackAppRegistrySetAndGet(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)
	reg, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}

	rec := slackAppRecord{
		WorkspaceID: "T123",
		AppID:       "A456",
		DisplayName: "gc-oversight",
		Scopes:      []string{"commands", "chat:write"},
		// SlashCommands left nil intentionally: omitempty + JSON
		// reload produces nil, not []string{}, and this test
		// exercises round-trip semantics on the next read.
		ManifestPath: "/tmp/app.json",
		ManifestRaw:  json.RawMessage(`{"display_information":{"name":"gc-oversight"}}`),
		ImportedAt:   time.Now().UTC(),
	}
	if err := reg.Set(rec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok := reg.Get("T123", "A456")
	if !ok {
		t.Fatalf("Get(T123,A456) ok=false, want true")
	}
	if got.DisplayName != "gc-oversight" {
		t.Errorf("Get DisplayName = %q, want gc-oversight", got.DisplayName)
	}
	if got.WorkspaceID != "T123" || got.AppID != "A456" {
		t.Errorf("Get composite key mismatch: workspace=%q app=%q", got.WorkspaceID, got.AppID)
	}
}

func TestSlackAppRegistryRejectsEmptyKey(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	cases := []slackAppRecord{
		{WorkspaceID: "", AppID: "A456"},
		{WorkspaceID: "T123", AppID: ""},
		{WorkspaceID: "", AppID: ""},
	}
	for _, rec := range cases {
		if err := reg.Set(rec); err == nil {
			t.Errorf("Set(%+v): expected error for empty key, got nil", rec)
		}
	}
}

func TestSlackAppRegistryPersistsAndReloads(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)
	reg1, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := slackAppRecord{
		WorkspaceID: "T123", AppID: "A456",
		DisplayName: "gc-oversight",
		Scopes:      []string{"commands"},
		ImportedAt:  time.Now().UTC(),
	}
	if err := reg1.Set(rec); err != nil {
		t.Fatal(err)
	}

	// Open a fresh registry pointing at the same file — must see the record.
	reg2, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reg2.Get("T123", "A456")
	if !ok {
		t.Fatalf("reload Get ok=false, want true")
	}
	if got.DisplayName != "gc-oversight" {
		t.Errorf("reload DisplayName = %q, want gc-oversight", got.DisplayName)
	}
}

func TestSlackAppRegistryAtomicWriteCleansTmp(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)
	reg, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := slackAppRecord{
		WorkspaceID: "T1", AppID: "A1",
		ImportedAt: time.Now().UTC(),
	}
	if err := reg.Set(rec); err != nil {
		t.Fatal(err)
	}

	// apps.json must exist and be valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read apps.json: %v", err)
	}
	var roundtrip map[string]slackAppRecord
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("apps.json is not valid JSON: %v\ncontents=%s", err, data)
	}

	// No stray *.tmp files in the registry dir after a successful write.
	// (Catches both the conventional "<path>.tmp" suffix and any
	// os.CreateTemp-style randomized name.)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || strings.Contains(e.Name(), ".tmp") {
			t.Errorf("stray tmp file lingered after successful write: %s", e.Name())
		}
	}
}

func TestSlackAppRegistryFilePermissions(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)
	reg, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Set(slackAppRecord{
		WorkspaceID: "T1", AppID: "A1", ImportedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("apps.json mode = %o, want 0600", mode)
	}
	dirfi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirfi.Mode().Perm(); mode != 0o700 {
		t.Errorf("apps.json parent dir mode = %o, want 0700", mode)
	}
}

func TestSlackAppRegistryIdempotentOverwrite(t *testing.T) {
	cityRoot := newTestCity(t)
	reg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-time.Hour)
	t1 := time.Now().UTC()

	if err := reg.Set(slackAppRecord{
		WorkspaceID: "T1", AppID: "A1", DisplayName: "v1", ImportedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Set(slackAppRecord{
		WorkspaceID: "T1", AppID: "A1", DisplayName: "v2", ImportedAt: t1,
	}); err != nil {
		t.Fatal(err)
	}

	if got := len(reg.All()); got != 1 {
		t.Errorf("idempotent re-set: All() len = %d, want 1", got)
	}
	got, _ := reg.Get("T1", "A1")
	if got.DisplayName != "v2" {
		t.Errorf("re-set DisplayName = %q, want v2 (overwrite)", got.DisplayName)
	}
	if !got.ImportedAt.Equal(t1) {
		t.Errorf("re-set ImportedAt = %v, want %v (advanced)", got.ImportedAt, t1)
	}
}

func TestSlackAppRegistryManifestRawRoundTrip(t *testing.T) {
	cityRoot := newTestCity(t)
	path := slackAppsRegistryPath(cityRoot)
	reg, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	original := json.RawMessage(`{"display_information":{"name":"x"},"oauth_config":{"scopes":{"bot":["commands"]}}}`)
	if err := reg.Set(slackAppRecord{
		WorkspaceID: "T1", AppID: "A1",
		ManifestRaw: original,
		ImportedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	reg2, err := newSlackAppRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reg2.Get("T1", "A1")
	if !ok {
		t.Fatal("reload Get not ok")
	}

	// Compare semantically (whitespace differences are tolerated by re-decoding).
	var a, b any
	if err := json.Unmarshal(original, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.ManifestRaw, &b); err != nil {
		t.Fatalf("persisted manifest_raw not valid JSON: %v\nraw=%s", err, got.ManifestRaw)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("manifest_raw round-trip mismatch:\noriginal=%s\nreloaded=%s", original, got.ManifestRaw)
	}
}
