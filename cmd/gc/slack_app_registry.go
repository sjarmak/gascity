package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
)

// slackAppRecord is the persisted representation of a Slack app imported
// into a gc city. The schema is the only contract between cmd/gc (writer)
// and examples/slack-pack/adapter (reader); both sides MUST match it
// byte-for-byte. The authoritative description lives at
// examples/slack-pack/schema/apps.schema.json.
//
// BotUserID and SigningSecret are populated post-OAuth (gc-cby.9), not
// at import time; both are optional. ManifestRaw preserves the raw
// manifest bytes verbatim so future readers can re-parse fields the
// current struct ignores (forward-compat).
type slackAppRecord struct {
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

func slackAppsRegistryPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "slack", "apps.json")
}

// slackAppRegistry mirrors identityRegistry in
// examples/slack-pack/adapter/main.go (sync.RWMutex + atomic
// temp+rename writes, 0o700/0o600 perms, tolerant load on missing
// file). The duplication is intentional: cmd/gc cannot import from
// examples/.
type slackAppRegistry struct {
	mu       sync.RWMutex
	byKey    map[string]slackAppRecord
	diskPath string
}

func slackAppKey(workspaceID, appID string) string {
	return workspaceID + ":" + appID
}

func newSlackAppRegistry(diskPath string) (*slackAppRegistry, error) {
	r := &slackAppRegistry{
		byKey:    make(map[string]slackAppRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load slack app registry from %s: %w", diskPath, err)
	}
	return r, nil
}

func (r *slackAppRegistry) Get(workspaceID, appID string) (slackAppRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byKey[slackAppKey(workspaceID, appID)]
	return rec, ok
}

// Set is idempotent: re-setting an existing (workspace_id, app_id)
// overwrites the record in place; the registry size does not grow.
func (r *slackAppRegistry) Set(rec slackAppRecord) error {
	if rec.WorkspaceID == "" || rec.AppID == "" {
		return fmt.Errorf("slack app registry: workspace_id and app_id are both required (got workspace_id=%q app_id=%q)", rec.WorkspaceID, rec.AppID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[slackAppKey(rec.WorkspaceID, rec.AppID)] = rec
	return r.saveLocked()
}

func (r *slackAppRegistry) All() []slackAppRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]slackAppRecord, 0, len(r.byKey))
	for _, rec := range r.byKey {
		out = append(out, rec)
	}
	return out
}

func (r *slackAppRegistry) load() error {
	if r.diskPath == "" {
		return nil
	}
	data, err := os.ReadFile(r.diskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored map[string]slackAppRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode slack app store: %w", err)
	}
	if stored != nil {
		r.byKey = stored
	}
	return nil
}

func (r *slackAppRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	// 0o700/0o600: records carry workspace ids and (post-OAuth)
	// signing secrets — not world-readable. Chmod after MkdirAll so
	// the contract holds even when the directory already exists with
	// looser permissions (MkdirAll is a no-op on existing dirs).
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir slack app store dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod slack app store dir: %w", err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode slack app store: %w", err)
	}
	// os.CreateTemp picks a unique name in dir, so two concurrent CLI
	// invocations writing the same registry don't clobber each other's
	// temp file before the rename.
	f, err := os.CreateTemp(dir, "apps-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create slack app store tmp: %w", err)
	}
	tmpName := f.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod slack app store tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("write slack app store tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close slack app store tmp: %w", err)
	}
	if err := os.Rename(tmpName, r.diskPath); err != nil {
		cleanup()
		return fmt.Errorf("rename slack app store: %w", err)
	}
	return nil
}
