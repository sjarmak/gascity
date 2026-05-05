package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// appRecord is the byte-for-byte mirror of cmd/gc.slackAppRecord
// (cmd/gc/slack_app_registry.go). The on-disk JSON file at
// <cityPath>/.gc/slack/apps.json — written by `gc slack import-app` and
// populated post-OAuth by gc-cby.9 — is the only contract between gc
// and this adapter. Field tags MUST match the writer's tags exactly;
// the canonical schema lives at examples/slack-pack/schema/apps.schema.json.
//
// SigningSecret is optional at import time and populated post-OAuth.
// An empty SigningSecret is NOT an error during load — it just means
// OAuth hasn't completed yet for that app and the record is currently
// unusable for request verification.
//
// ManifestRaw preserves the raw manifest bytes verbatim so future readers
// can re-parse fields the current struct ignores (forward-compat).
type appRecord struct {
	WorkspaceID   string          `json:"workspace_id"`
	AppID         string          `json:"app_id"`
	BotUserID     string          `json:"bot_user_id,omitempty"`
	DisplayName   string          `json:"display_name,omitempty"`
	Scopes        []string        `json:"scopes,omitempty"`
	SlashCommands []string        `json:"slash_commands,omitempty"`
	SigningSecret string          `json:"signing_secret,omitempty"`
	ManifestPath  string          `json:"manifest_path,omitempty"`
	ManifestRaw   json.RawMessage `json:"manifest_raw,omitempty"`
	ImportedAt    time.Time       `json:"imported_at"`
}

// appsRegistry is a read-mostly in-memory view of apps.json for the
// adapter side. The adapter loads the file once at startup and re-reads
// it on SIGHUP via Stage/Commit (gc-cby.23). RWMutex is provided
// because gc-cby.9 (OAuth flow) will eventually drive in-process
// updates from the same binary.
//
// Schema duplication with cmd/gc/slack_app_registry.go is intentional:
// examples/ may not import cmd/gc, and cmd/gc may not import examples/.
// The on-disk JSON is the contract.
type appsRegistry struct {
	mu       sync.RWMutex
	byKey    map[string]appRecord
	diskPath string
}

// appsSnapshot is a parsed-but-not-yet-committed view of apps.json. A
// nil snapshot is the "file is absent" sentinel (see Stage); Commit
// treats it as a no-op so an operator-side `rm` does NOT silently wipe
// in-memory state. To clear, write an empty `{}` JSON document.
type appsSnapshot struct {
	byKey map[string]appRecord
}

func appsRegistryKey(workspaceID, appID string) string {
	return workspaceID + ":" + appID
}

// newAppsRegistry opens the registry at diskPath. A missing file yields
// an empty registry (tolerant load) so adapter restarts on a fresh
// city — where no apps have been imported yet — succeed instead of
// fatal. Same contract as identityRegistry / channelMappingRegistry.
func newAppsRegistry(diskPath string) (*appsRegistry, error) {
	r := &appsRegistry{
		byKey:    make(map[string]appRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load apps registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// GetByTeamID returns every record for workspaceID. A workspace may host
// multiple gc-imported apps, each with its own signing secret — the
// caller (lookupSigningSecrets) trial-verifies the inbound HMAC against
// each in turn. Empty result means no app for this workspace.
func (r *appsRegistry) GetByTeamID(workspaceID string) []appRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []appRecord
	for _, rec := range r.byKey {
		if rec.WorkspaceID == workspaceID {
			out = append(out, rec)
		}
	}
	return out
}

// parseAppsRegistry reads diskPath and returns a ready-to-commit
// snapshot. A missing file returns (nil, nil) — the "no change" sentinel
// for SIGHUP reload semantics. 10 MiB cap matches channelMappingRegistry.
// DisallowUnknownFields is intentionally NOT enabled — the cmd/gc writer
// may grow the schema (e.g. forward-compat manifest_raw additions) before
// the adapter is updated. Field-by-field strict matching would silently
// break operators on partial upgrades; the on-disk schema is the only
// contract.
func parseAppsRegistry(diskPath string) (*appsSnapshot, error) {
	if diskPath == "" {
		return nil, nil
	}
	f, err := os.Open(diskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open apps registry %s: %w", diskPath, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRegistryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read apps registry %s: %w", diskPath, err)
	}
	if int64(len(data)) > maxRegistryBytes {
		return nil, fmt.Errorf("apps registry file %s exceeds %d bytes", diskPath, maxRegistryBytes)
	}
	var stored map[string]appRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode apps registry: %w", err)
	}
	if stored == nil {
		stored = make(map[string]appRecord)
	}
	return &appsSnapshot{byKey: stored}, nil
}

// load is the constructor-time helper — called pre-publish, no lock needed.
func (r *appsRegistry) load() error {
	snap, err := parseAppsRegistry(r.diskPath)
	if err != nil {
		return err
	}
	if snap != nil {
		r.byKey = snap.byKey
	}
	return nil
}

// Stage parses the on-disk file into a snapshot ready for atomic Commit.
// nil snapshot + nil error = file absent, preserve live state.
func (r *appsRegistry) Stage() (*appsSnapshot, error) {
	return parseAppsRegistry(r.diskPath)
}

// Commit atomically swaps the in-memory snapshot under the write lock.
// nil snapshot is a no-op so missing-file Stages preserve live state.
func (r *appsRegistry) Commit(snap *appsSnapshot) {
	if snap == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey = snap.byKey
}

// Reload combines Stage and Commit; per-registry test convenience.
// Production reload uses reloadAllRegistries for all-or-nothing semantics.
func (r *appsRegistry) Reload() error {
	snap, err := r.Stage()
	if err != nil {
		return err
	}
	r.Commit(snap)
	return nil
}

// Len returns the number of records currently loaded. Used by the
// startup log to surface "registry loaded empty" cases to operators.
func (r *appsRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKey)
}

// Set writes a record to the registry. Production callers today are
// limited to test setup: operator-driven writes go through
// `gc slack import-app` (cmd/gc side), and the adapter only reads.
// gc-cby.9 (OAuth flow) will promote this to a real production
// caller when it lands; the locking, atomic-write, and validation
// already match production requirements.
func (r *appsRegistry) Set(rec appRecord) error {
	if rec.WorkspaceID == "" || rec.AppID == "" {
		return fmt.Errorf("apps registry: workspace_id and app_id are both required (got workspace_id=%q app_id=%q)", rec.WorkspaceID, rec.AppID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[appsRegistryKey(rec.WorkspaceID, rec.AppID)] = rec
	return r.saveLocked()
}

func (r *appsRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir apps registry dir: %w", err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode apps registry: %w", err)
	}
	return writeFile0600(r.diskPath, data)
}

// lookupSigningSecrets resolves the candidate signing secrets used to
// verify an inbound Slack request, given the team_id parsed from the
// (still-unsigned) body. The adapter trial-verifies each candidate and
// short-circuits on the first match.
//
// Resolution order:
//
//  1. Apps registry: every record matching teamID with a non-empty
//     signing_secret. A workspace can host multiple gc-imported apps;
//     trial-verify picks the right one mechanically.
//  2. Env fallback: cfg.slackSigningKey, when set. Single-app dev /
//     legacy installs that haven't run `gc slack import-app` get the
//     same behavior they always had.
//
// Empty signing_secret records (post-import-pre-OAuth) are silently
// skipped — their existence is not a verify-failure signal, just
// "OAuth hasn't run for this app yet". When all matching records are
// empty, we fall through to env fallback so a half-onboarded multi-
// app workspace doesn't become un-verifiable.
//
// teamID == "" (couldn't parse from body) skips the registry lookup
// (the composite key would be meaningless) and tries env fallback. A
// single-app install still verifies; a multi-app install rejects with
// 401 once trial-verify exhausts candidates.
//
// No candidates returned -> handler returns 401. This is the correct
// fail-closed behavior; structured logging at the call site surfaces
// the case to operators without leaking secret material.
func lookupSigningSecrets(reg *appsRegistry, envFallback, teamID string) []string {
	if reg != nil && teamID != "" {
		var out []string
		for _, rec := range reg.GetByTeamID(teamID) {
			if rec.SigningSecret == "" {
				continue
			}
			out = append(out, rec.SigningSecret)
		}
		if len(out) > 0 {
			return out
		}
	}
	if envFallback != "" {
		return []string{envFallback}
	}
	return nil
}
