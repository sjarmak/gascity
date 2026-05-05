package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
)

// slackRoomLaunchMappingRecord is the persisted representation of a
// (workspace_id, channel_id) → pool_template binding written by
// `gc slack enable-room-launch` and read by the slack-pack adapter when
// dispatching `@@<handle>` posts. The schema is the only contract
// between cmd/gc (writer) and examples/slack-pack/adapter (reader);
// both sides MUST match it byte-for-byte.
//
// PoolTemplate is opaque (operator-supplied) and intentionally does NOT
// reference any specific role name — Gas City's ZERO-hardcoded-roles
// rule means the launcher's identity is whatever the operator wired in
// `gc slack enable-room-launch <ch> --launcher <pool>`. The adapter
// passes the string verbatim to gc's session-create endpoint as the
// `name` field.
//
// CreatedAt is set on first Set and preserved on every idempotent
// re-Set for the same composite key. UpdatedAt advances on every Set.
type slackRoomLaunchMappingRecord struct {
	WorkspaceID  string    `json:"workspace_id"`
	ChannelID    string    `json:"channel_id"`
	PoolTemplate string    `json:"pool_template"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// slackRoomLaunchMappingsPath returns the on-disk path for the room
// launch mapping registry of the city rooted at cityPath.
func slackRoomLaunchMappingsPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "slack", "room_launch_mappings.json")
}

// slackRoomLaunchMappingRegistry persists channel→pool_template
// bindings written by `gc slack enable-room-launch`. The slack-pack
// adapter reads this once at startup; restart the adapter to pick up
// new bindings (same caveat as channel/rig mappings).
type slackRoomLaunchMappingRegistry struct {
	mu       sync.RWMutex
	byKey    map[string]slackRoomLaunchMappingRecord // "<workspace_id>:<channel_id>"
	diskPath string
}

func slackRoomLaunchMappingKey(workspaceID, channelID string) string {
	return workspaceID + ":" + channelID
}

// newSlackRoomLaunchMappingRegistry opens (or creates) the registry at
// diskPath. A missing file yields an empty registry (tolerant load).
// A corrupt file is surfaced as an error so operators can repair
// rather than silently overwrite.
func newSlackRoomLaunchMappingRegistry(diskPath string) (*slackRoomLaunchMappingRegistry, error) {
	r := &slackRoomLaunchMappingRegistry{
		byKey:    make(map[string]slackRoomLaunchMappingRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load slack room-launch mapping registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// Get returns the record for (workspaceID, channelID), plus a bool
// indicating whether one is registered.
func (r *slackRoomLaunchMappingRegistry) Get(workspaceID, channelID string) (slackRoomLaunchMappingRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byKey[slackRoomLaunchMappingKey(workspaceID, channelID)]
	return rec, ok
}

// Set persists rec. Validates required fields. Idempotent re-Set for
// the same (workspace_id, channel_id) preserves CreatedAt and replaces
// PoolTemplate.
func (r *slackRoomLaunchMappingRegistry) Set(rec slackRoomLaunchMappingRecord) error {
	if rec.WorkspaceID == "" {
		return fmt.Errorf("slack room-launch mapping: workspace_id is required")
	}
	if rec.ChannelID == "" {
		return fmt.Errorf("slack room-launch mapping: channel_id is required")
	}
	if rec.PoolTemplate == "" {
		return fmt.Errorf("slack room-launch mapping: pool_template is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	key := slackRoomLaunchMappingKey(rec.WorkspaceID, rec.ChannelID)

	now := time.Now().UTC()
	if existing, ok := r.byKey[key]; ok {
		// Preserve CreatedAt across idempotent re-bind.
		rec.CreatedAt = existing.CreatedAt
	} else if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() || !rec.UpdatedAt.After(rec.CreatedAt) {
		rec.UpdatedAt = now
	}
	r.byKey[key] = rec
	return r.saveLocked()
}

// Remove deletes the binding for (workspaceID, channelID) if present.
// Returns whether an entry existed; missing entries are not an error
// so callers can treat Remove as idempotent.
func (r *slackRoomLaunchMappingRegistry) Remove(workspaceID, channelID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := slackRoomLaunchMappingKey(workspaceID, channelID)
	if _, ok := r.byKey[key]; !ok {
		return false, nil
	}
	delete(r.byKey, key)
	if err := r.saveLocked(); err != nil {
		return true, err
	}
	return true, nil
}

// AllSorted returns every registered binding, sorted by composite key
// for diff-stable JSON dumps and `gc slack status` output.
func (r *slackRoomLaunchMappingRegistry) AllSorted() []slackRoomLaunchMappingRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]slackRoomLaunchMappingRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// maxRoomLaunchMappingBytes caps the size of the JSON file we read.
// Bindings are at most a few hundred records of a fixed shape; 10 MiB
// is several orders of magnitude over a healthy install.
const maxRoomLaunchMappingBytes = 10 << 20 // 10 MiB

func (r *slackRoomLaunchMappingRegistry) load() error {
	if r.diskPath == "" {
		return nil
	}
	f, err := os.Open(r.diskPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxRoomLaunchMappingBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", r.diskPath, err)
	}
	if int64(len(data)) > maxRoomLaunchMappingBytes {
		return fmt.Errorf("registry file %s exceeds %d bytes", r.diskPath, maxRoomLaunchMappingBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var stored map[string]slackRoomLaunchMappingRecord
	if err := dec.Decode(&stored); err != nil {
		return fmt.Errorf("decode slack room-launch mapping store: %w", err)
	}
	for key, rec := range stored {
		if rec.WorkspaceID == "" || rec.ChannelID == "" {
			return fmt.Errorf("slack room-launch mapping store: record %q missing workspace_id or channel_id", key)
		}
		if rec.PoolTemplate == "" {
			return fmt.Errorf("slack room-launch mapping store: record %q missing pool_template", key)
		}
	}
	r.byKey = stored
	if r.byKey == nil {
		r.byKey = make(map[string]slackRoomLaunchMappingRecord)
	}
	return nil
}

func (r *slackRoomLaunchMappingRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir slack room-launch mapping store dir %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod slack room-launch mapping store dir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode slack room-launch mapping store: %w", err)
	}
	f, err := os.CreateTemp(dir, "room_launch_mappings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create slack room-launch mapping store tmp: %w", err)
	}
	tmpName := f.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod slack room-launch mapping store tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("write slack room-launch mapping store tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close slack room-launch mapping store tmp: %w", err)
	}
	if err := os.Rename(tmpName, r.diskPath); err != nil {
		cleanup()
		return fmt.Errorf("rename slack room-launch mapping store: %w", err)
	}
	return nil
}
