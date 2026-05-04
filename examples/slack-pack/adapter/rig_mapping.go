package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// rigMappingDiskRecord is the byte-for-byte mirror of
// cmd/gc.slackRigMappingRecord (cmd/gc/slack_rig_mapping.go). The
// schema lives at examples/slack-pack/schema/rig_mappings.schema.json.
type rigMappingDiskRecord struct {
	WorkspaceID string    `json:"workspace_id"`
	RigName     string    `json:"rig_name"`
	ChannelIDs  []string  `json:"channel_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// rigMappingRegistry is a read-mostly in-memory view of the
// rig_mappings.json file written by `gc slack map-rig`. Loaded once
// at adapter startup; restart adapter to pick up new bindings written
// via `gc slack map-rig`. Same caveat as channelMappingRegistry — a
// watcher would race against in-flight Slack interactions, and
// Slack's 3-second slash-command latency budget is too tight to
// retry.
type rigMappingRegistry struct {
	mu        sync.RWMutex
	byKey     map[string]rigMappingDiskRecord // "<workspace_id>:<rig_name>"
	byChannel map[string]string               // "<workspace_id>:<channel_id>" -> rigName
	diskPath  string
}

func rigMappingKey(workspaceID, rigName string) string {
	return workspaceID + ":" + rigName
}

func rigChannelKey(workspaceID, channelID string) string {
	return workspaceID + ":" + channelID
}

// newRigMappingRegistry opens (or creates) the registry at diskPath.
// A missing file yields an empty registry. Unknown fields, empty
// channel_ids, and missing workspace_id/rig_name are rejected at
// load time so a corrupt upstream write can't silently be served as
// policy.
func newRigMappingRegistry(diskPath string) (*rigMappingRegistry, error) {
	r := &rigMappingRegistry{
		byKey:     make(map[string]rigMappingDiskRecord),
		byChannel: make(map[string]string),
		diskPath:  diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load rig mapping registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// LookupRigForChannel returns the record covering (workspaceID,
// channelID), the source discriminator "rig", and ok=true on hit.
// Per-channel `map-channel` bindings (cby.3) take precedence — call
// the channel registry first.
func (r *rigMappingRegistry) LookupRigForChannel(workspaceID, channelID string) (rigMappingDiskRecord, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rigName, ok := r.byChannel[rigChannelKey(workspaceID, channelID)]
	if !ok {
		return rigMappingDiskRecord{}, "", false
	}
	rec, ok := r.byKey[rigMappingKey(workspaceID, rigName)]
	if !ok {
		return rigMappingDiskRecord{}, "", false
	}
	return rec, "rig", true
}

// All returns every loaded rig mapping, sorted by composite key for
// diff-stable ordering.
func (r *rigMappingRegistry) All() []rigMappingDiskRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]rigMappingDiskRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// Set is provided for tests only. Production reads only — operator
// writes go through `gc slack map-rig`.
func (r *rigMappingRegistry) Set(rec rigMappingDiskRecord) error {
	if rec.WorkspaceID == "" || rec.RigName == "" {
		return fmt.Errorf("rig mapping: workspace_id and rig_name required")
	}
	if len(rec.ChannelIDs) == 0 {
		return fmt.Errorf("rig mapping: at least one channel_id required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := rigMappingKey(rec.WorkspaceID, rec.RigName)
	if existing, ok := r.byKey[key]; ok {
		for _, ch := range existing.ChannelIDs {
			delete(r.byChannel, rigChannelKey(rec.WorkspaceID, ch))
		}
	}
	r.byKey[key] = rec
	for _, ch := range rec.ChannelIDs {
		r.byChannel[rigChannelKey(rec.WorkspaceID, ch)] = rec.RigName
	}
	return r.saveLocked()
}

func (r *rigMappingRegistry) load() error {
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var stored map[string]rigMappingDiskRecord
	if err := dec.Decode(&stored); err != nil {
		return fmt.Errorf("decode rig mapping store: %w", err)
	}
	for key, rec := range stored {
		if rec.WorkspaceID == "" || rec.RigName == "" {
			return fmt.Errorf("rig mapping store: record %q missing workspace_id or rig_name", key)
		}
		if len(rec.ChannelIDs) == 0 {
			return fmt.Errorf("rig mapping store: record %q has empty channel_ids", key)
		}
	}
	r.byKey = stored
	if r.byKey == nil {
		r.byKey = make(map[string]rigMappingDiskRecord)
	}

	// Rebuild byChannel deterministically. On overlap (only possible
	// via a hand-edited file), the first-by-sorted-key rig wins and
	// a WARN is emitted so operators see the conflict in adapter
	// logs.
	r.byChannel = make(map[string]string)
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec := r.byKey[k]
		for _, ch := range rec.ChannelIDs {
			ck := rigChannelKey(rec.WorkspaceID, ch)
			if existing, ok := r.byChannel[ck]; ok && existing != rec.RigName {
				log.Printf("WARN: rig mapping store: channel %q in workspace %q claimed by rig %q and rig %q (hand-edited?); rig %q wins for resolver",
					ch, rec.WorkspaceID, existing, rec.RigName, existing)
				continue
			}
			r.byChannel[ck] = rec.RigName
		}
	}
	return nil
}

func (r *rigMappingRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir rig mapping store dir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rig mapping store: %w", err)
	}
	return writeFile0600(r.diskPath, data)
}

// resolveChannelTarget returns the binding that should handle the
// slash command, along with a source discriminator. Per-channel
// map-channel bindings are overrides on top of the rig→channel-set
// default; channel mapping wins. The returned source is "channel"
// when cby.3 hits, "rig" when cby.4 hits, "" when neither.
//
// The returned record carries either a real channelMappingDiskRecord
// from cby.3 OR a synthetic channelMappingDiskRecord built from the
// rig record (TargetKind="rig", TargetID=<rigName>) so callers can
// route through a single switch statement. The synthetic record's
// CreatedAt/UpdatedAt mirror the rig record's so observability
// downstream stays accurate.
func resolveChannelTarget(chanReg *channelMappingRegistry, rigReg *rigMappingRegistry, workspaceID, channelID string) (channelMappingDiskRecord, string, bool) {
	if chanReg != nil {
		if rec, ok := chanReg.Get(workspaceID, channelID); ok {
			return rec, "channel", true
		}
	}
	if rigReg != nil {
		if rec, _, ok := rigReg.LookupRigForChannel(workspaceID, channelID); ok {
			return channelMappingDiskRecord{
				WorkspaceID: rec.WorkspaceID,
				ChannelID:   channelID,
				TargetKind:  channelMappingTargetKindRig,
				TargetID:    rec.RigName,
				CreatedAt:   rec.CreatedAt,
				UpdatedAt:   rec.UpdatedAt,
			}, "rig", true
		}
	}
	return channelMappingDiskRecord{}, "", false
}

// logCrossStoreOverlapWarnings inspects both registries and emits a
// WARN line for every (workspace, channel) where the cby.3 channel
// store binds the channel to a `rig` target AND the cby.4 rig store
// claims the same channel for a DIFFERENT rig. Channel mapping wins
// at resolution time; the WARN is purely observability so operators
// see the contradictory binding in adapter logs at startup.
func logCrossStoreOverlapWarnings(chanReg *channelMappingRegistry, rigReg *rigMappingRegistry) {
	if chanReg == nil || rigReg == nil {
		return
	}
	chanReg.mu.RLock()
	defer chanReg.mu.RUnlock()
	rigReg.mu.RLock()
	defer rigReg.mu.RUnlock()
	for _, m := range chanReg.byKey {
		if m.TargetKind != channelMappingTargetKindRig {
			continue
		}
		ck := rigChannelKey(m.WorkspaceID, m.ChannelID)
		if rigName, ok := rigReg.byChannel[ck]; ok && rigName != m.TargetID {
			log.Printf("WARN: channel %q in workspace %q is bound by both map-channel (rig=%q) and map-rig (rig=%q); map-channel wins",
				m.ChannelID, m.WorkspaceID, m.TargetID, rigName)
		}
	}
}
