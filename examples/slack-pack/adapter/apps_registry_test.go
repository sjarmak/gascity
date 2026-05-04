package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestAppsRegistryLoadMissingFile — tolerant load when apps.json doesn't exist.
// Mirrors identityRegistry semantics so adapter restarts on a fresh city
// (no apps imported yet) succeed instead of fatal.
func TestAppsRegistryLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry on missing file: %v", err)
	}
	if reg == nil {
		t.Fatal("newAppsRegistry returned nil")
	}
	if got := reg.GetByTeamID("T1"); len(got) != 0 {
		t.Errorf("GetByTeamID on empty registry = %v, want empty", got)
	}
}

func writeAppsRegistryFile(t *testing.T, dir string, recs map[string]appRecord) string {
	t.Helper()
	path := filepath.Join(dir, "apps.json")
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		t.Fatalf("marshal apps registry seed: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write apps registry seed: %v", err)
	}
	return path
}

func TestAppsRegistryLoadAndGetByTeamID(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T1:A1": {WorkspaceID: "T1", AppID: "A1", SigningSecret: "secret-a1"},
		"T1:A2": {WorkspaceID: "T1", AppID: "A2", SigningSecret: "secret-a2"},
		"T2:A3": {WorkspaceID: "T2", AppID: "A3", SigningSecret: "secret-a3"},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	got := reg.GetByTeamID("T1")
	if len(got) != 2 {
		t.Fatalf("GetByTeamID(T1) returned %d records, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, rec := range got {
		seen[rec.AppID] = true
	}
	if !seen["A1"] || !seen["A2"] {
		t.Errorf("GetByTeamID(T1) missing A1/A2: %v", got)
	}

	t2 := reg.GetByTeamID("T2")
	if len(t2) != 1 || t2[0].AppID != "A3" {
		t.Errorf("GetByTeamID(T2) = %v, want single A3", t2)
	}

	if got := reg.GetByTeamID("T_UNKNOWN"); len(got) != 0 {
		t.Errorf("GetByTeamID(unknown) = %v, want empty", got)
	}
}

// TestLookupSigningSecretsByTeam — registry has 3 apps for T1, one with empty
// signing_secret (post-import but pre-OAuth). Lookup must return the 2 with
// non-empty secrets and skip the empty one — empty signing_secret is "OAuth
// hasn't run yet", not an error.
func TestLookupSigningSecretsByTeam(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T1:A1": {WorkspaceID: "T1", AppID: "A1", SigningSecret: "secret-a1"},
		"T1:A2": {WorkspaceID: "T1", AppID: "A2", SigningSecret: ""},
		"T1:A3": {WorkspaceID: "T1", AppID: "A3", SigningSecret: "secret-a3"},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	got := lookupSigningSecrets(reg, "env-fallback", "T1")
	// Registry has matches with non-empty secrets — env fallback NOT used.
	if len(got) != 2 {
		t.Fatalf("lookupSigningSecrets returned %d, want 2: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s] = true
	}
	if !seen["secret-a1"] || !seen["secret-a3"] {
		t.Errorf("lookupSigningSecrets missing secret-a1/secret-a3: %v", got)
	}
	if seen["env-fallback"] {
		t.Errorf("lookupSigningSecrets included env fallback when registry had matches: %v", got)
	}
}

func TestLookupSigningSecretsFallsBackToEnvWhenRegistryNil(t *testing.T) {
	got := lookupSigningSecrets(nil, "env-secret", "T1")
	if len(got) != 1 || got[0] != "env-secret" {
		t.Errorf("lookupSigningSecrets(nil) = %v, want [env-secret]", got)
	}
}

func TestLookupSigningSecretsFallsBackToEnvOnTeamMiss(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T2:A3": {WorkspaceID: "T2", AppID: "A3", SigningSecret: "secret-a3"},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	got := lookupSigningSecrets(reg, "env-secret", "T1")
	if len(got) != 1 || got[0] != "env-secret" {
		t.Errorf("lookupSigningSecrets(team-miss) = %v, want [env-secret]", got)
	}
}

// TestLookupSigningSecretsFallsBackToEnvWhenAllRegistryRecordsEmpty — a
// matching team has only post-import-pre-OAuth records (empty
// signing_secret). Treat as "no usable registry secret for this team" and
// fall through to env, instead of yielding an empty candidate list.
func TestLookupSigningSecretsFallsBackToEnvWhenAllRegistryRecordsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T1:A1": {WorkspaceID: "T1", AppID: "A1", SigningSecret: ""},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	got := lookupSigningSecrets(reg, "env-secret", "T1")
	if len(got) != 1 || got[0] != "env-secret" {
		t.Errorf("lookupSigningSecrets(all-empty) = %v, want [env-secret]", got)
	}
}

func TestLookupSigningSecretsNoneAvailable(t *testing.T) {
	got := lookupSigningSecrets(nil, "", "T1")
	if len(got) != 0 {
		t.Errorf("lookupSigningSecrets(no env, no reg) = %v, want empty", got)
	}
}

// TestLookupSigningSecretsEmptyTeamIDFallsBackToEnv — no team_id parsed
// from body (e.g. legitimately-truncated event). Skip registry lookup
// (key would be empty) and use env. Single-app dev installs still work;
// multi-app installs reject with 401 if env is also empty.
func TestLookupSigningSecretsEmptyTeamIDFallsBackToEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T1:A1": {WorkspaceID: "T1", AppID: "A1", SigningSecret: "secret-a1"},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	got := lookupSigningSecrets(reg, "env-secret", "")
	if len(got) != 1 || got[0] != "env-secret" {
		t.Errorf("lookupSigningSecrets(empty team) = %v, want [env-secret]", got)
	}
}

// TestAppsRegistryConcurrentReads exercises RLock semantics. A pile of
// concurrent GetByTeamID calls must neither deadlock nor return torn
// state. Run with -race for full value.
func TestAppsRegistryConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	path := writeAppsRegistryFile(t, dir, map[string]appRecord{
		"T1:A1": {WorkspaceID: "T1", AppID: "A1", SigningSecret: "secret-a1"},
		"T1:A2": {WorkspaceID: "T1", AppID: "A2", SigningSecret: "secret-a2"},
	})
	reg, err := newAppsRegistry(path)
	if err != nil {
		t.Fatalf("newAppsRegistry: %v", err)
	}
	const readers = 32
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got := reg.GetByTeamID("T1")
				if len(got) != 2 {
					t.Errorf("concurrent GetByTeamID got %d records, want 2", len(got))
					return
				}
			}
		}()
	}
	wg.Wait()
}
