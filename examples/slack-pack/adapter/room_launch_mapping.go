package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// roomLaunchMappingDiskRecord is the byte-for-byte mirror of
// cmd/gc.slackRoomLaunchMappingRecord (cmd/gc/slack_room_launch_mapping.go).
// PoolTemplate is the operator-supplied agent template the adapter
// passes to gc's session-create endpoint when an `@@<handle>` post
// arrives in this channel — Gas City's ZERO-hardcoded-roles rule means
// neither side parses or validates it beyond non-emptiness.
type roomLaunchMappingDiskRecord struct {
	WorkspaceID  string    `json:"workspace_id"`
	ChannelID    string    `json:"channel_id"`
	PoolTemplate string    `json:"pool_template"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// roomLaunchMappingRegistry is the read-mostly adapter-side view of the
// room_launch_mappings.json file written by `gc slack
// enable-room-launch`. Loaded once at adapter startup; restart the
// adapter to pick up new bindings (same caveat as
// channelMappingRegistry / rigMappingRegistry).
type roomLaunchMappingRegistry struct {
	mu       sync.RWMutex
	byKey    map[string]roomLaunchMappingDiskRecord // "<workspace_id>:<channel_id>"
	diskPath string
}

func roomLaunchMappingKey(workspaceID, channelID string) string {
	return workspaceID + ":" + channelID
}

// newRoomLaunchMappingRegistry opens (or creates) the registry at
// diskPath. A missing file yields an empty registry. Records with
// missing required fields are rejected at load time so a corrupt
// upstream write can't silently be served as policy.
func newRoomLaunchMappingRegistry(diskPath string) (*roomLaunchMappingRegistry, error) {
	r := &roomLaunchMappingRegistry{
		byKey:    make(map[string]roomLaunchMappingDiskRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load room launch mapping registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// LookupPoolTemplate returns the pool_template bound to (workspaceID,
// channelID), plus a bool indicating whether the channel is enabled
// for launcher mode. A miss means the channel is NOT enabled — the
// adapter should reply with an actionable ephemeral instructing the
// operator to run `gc slack enable-room-launch <channel> --launcher
// <pool>`.
func (r *roomLaunchMappingRegistry) LookupPoolTemplate(workspaceID, channelID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byKey[roomLaunchMappingKey(workspaceID, channelID)]
	if !ok {
		return "", false
	}
	return rec.PoolTemplate, true
}

// Len returns the number of records currently loaded. Used by startup
// log lines.
func (r *roomLaunchMappingRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKey)
}

// All returns every loaded binding, sorted by composite key for
// diff-stable ordering.
func (r *roomLaunchMappingRegistry) All() []roomLaunchMappingDiskRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]roomLaunchMappingDiskRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// Set is provided for tests only. Production reads only — operator
// writes go through `gc slack enable-room-launch`.
func (r *roomLaunchMappingRegistry) Set(rec roomLaunchMappingDiskRecord) error {
	if rec.WorkspaceID == "" || rec.ChannelID == "" || rec.PoolTemplate == "" {
		return fmt.Errorf("room launch mapping: workspace_id, channel_id, pool_template are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[roomLaunchMappingKey(rec.WorkspaceID, rec.ChannelID)] = rec
	return r.saveLocked()
}

// maxRoomLaunchRegistryBytes caps the size of the JSON file we read.
// Bindings are at most a few hundred records of a fixed shape; 10 MiB
// is several orders of magnitude over a healthy install.
const maxRoomLaunchRegistryBytes = 10 << 20 // 10 MiB

func (r *roomLaunchMappingRegistry) load() error {
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
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRoomLaunchRegistryBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", r.diskPath, err)
	}
	if int64(len(data)) > maxRoomLaunchRegistryBytes {
		return fmt.Errorf("registry file %s exceeds %d bytes", r.diskPath, maxRoomLaunchRegistryBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var stored map[string]roomLaunchMappingDiskRecord
	if err := dec.Decode(&stored); err != nil {
		return fmt.Errorf("decode room launch mapping store: %w", err)
	}
	for key, rec := range stored {
		if rec.WorkspaceID == "" || rec.ChannelID == "" {
			return fmt.Errorf("room launch mapping store: record %q missing workspace_id or channel_id", key)
		}
		if rec.PoolTemplate == "" {
			return fmt.Errorf("room launch mapping store: record %q missing pool_template", key)
		}
	}
	r.byKey = stored
	if r.byKey == nil {
		r.byKey = make(map[string]roomLaunchMappingDiskRecord)
	}
	return nil
}

func (r *roomLaunchMappingRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode room launch mapping store: %w", err)
	}
	return writeFile0600(r.diskPath, data)
}
