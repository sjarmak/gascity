package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gastownhall/gascity/internal/citylayout"
)

// slackRigMappingRecord is the persisted representation of a
// (workspace_id, rig_name) → set-of-channel-ids binding written by
// `gc slack map-rig` and read by the slack-pack adapter's
// /slack/interactions handler. The schema is the only contract between
// cmd/gc (writer) and examples/slack-pack/adapter (reader); both sides
// MUST match it byte-for-byte. The authoritative description lives at
// examples/slack-pack/schema/rig_mappings.schema.json.
//
// CreatedAt is set on first Set and preserved on every idempotent
// re-Set for the same composite key. UpdatedAt advances on every Set.
// ChannelIDs is sorted and deduplicated on every Set so on-disk JSON
// is diff-stable.
//
// SlingTarget and FixFormula are the dispatch-routing fields (cby.18.a):
// SlingTarget is the qualified agent name (`<rig>/<role>`) the adapter
// passes to `gc sling --target`, and FixFormula is the molecule name
// the adapter spawns (e.g. `mol-slack-fix-issue`). Both are stored as
// strings so role names never appear in Go code (ZFC). They are
// optional in the JSON Schema for legacy-record tolerance — load-time
// missing → empty string; the resolver surfaces a fix-it error at
// use time when SlingTarget is empty.
type slackRigMappingRecord struct {
	WorkspaceID string    `json:"workspace_id"`
	RigName     string    `json:"rig_name"`
	ChannelIDs  []string  `json:"channel_ids"`
	SlingTarget string    `json:"sling_target,omitempty"`
	FixFormula  string    `json:"fix_formula,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// slackRigMappingsPath returns the on-disk path for the rig mapping
// registry of the city rooted at cityPath.
func slackRigMappingsPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "slack", "rig_mappings.json")
}

// slackRigMappingRegistry persists rig→{channels} bindings written by
// `gc slack map-rig`. It maintains an inverted index byChannel so the
// adapter's resolver can look up "which rig owns this channel?" in
// O(1).
//
// Per-channel `map-channel` bindings (cby.3) take precedence over
// rig-scoped bindings — call the channel registry first, then fall
// back to this. The discriminator returned by LookupRigForChannel is
// always "rig" so callers can log which store resolved.
type slackRigMappingRegistry struct {
	mu        sync.RWMutex
	byKey     map[string]slackRigMappingRecord // "<workspace_id>:<rig_name>"
	byChannel map[string]string                // "<workspace_id>:<channel_id>" -> rigName
	diskPath  string
}

func slackRigMappingKey(workspaceID, rigName string) string {
	return workspaceID + ":" + rigName
}

func slackRigChannelKey(workspaceID, channelID string) string {
	return workspaceID + ":" + channelID
}

// newSlackRigMappingRegistry opens (or creates) the registry at
// diskPath. A missing file yields an empty registry (tolerant load).
// Records with empty fields, empty channel_ids, or invalid rig_name
// characters are rejected at load time so downstream readers cannot
// consume corrupt state silently. Cross-rig channel overlaps (only
// possible via a hand-edited file) are logged as WARN and the
// first-by-sorted-key rig wins in the inverted index.
func newSlackRigMappingRegistry(diskPath string) (*slackRigMappingRegistry, error) {
	r := &slackRigMappingRegistry{
		byKey:     make(map[string]slackRigMappingRecord),
		byChannel: make(map[string]string),
		diskPath:  diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load slack rig mapping registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// Get returns the record for (workspaceID, rigName), plus a bool
// indicating whether one is registered.
func (r *slackRigMappingRegistry) Get(workspaceID, rigName string) (slackRigMappingRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byKey[slackRigMappingKey(workspaceID, rigName)]
	return rec, ok
}

// LookupRigForChannel returns the rig that owns this channel via the
// inverted index. Per-channel map-channel bindings (cby.3) take
// precedence over rig-scoped bindings — call the channel registry
// first, then fall back to this. The source discriminator is always
// "rig" so callers can log which store resolved.
func (r *slackRigMappingRegistry) LookupRigForChannel(workspaceID, channelID string) (slackRigMappingRecord, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rigName, ok := r.byChannel[slackRigChannelKey(workspaceID, channelID)]
	if !ok {
		return slackRigMappingRecord{}, "", false
	}
	rec, ok := r.byKey[slackRigMappingKey(workspaceID, rigName)]
	if !ok {
		// Should be impossible — byChannel and byKey are maintained
		// together under the same lock.
		return slackRigMappingRecord{}, "", false
	}
	return rec, "rig", true
}

// Set persists rec, sorting and deduplicating ChannelIDs. Idempotent
// re-Set for the same (workspace_id, rig_name) preserves CreatedAt
// and replaces ChannelIDs (the inverted index is rebuilt to drop
// channels no longer in the set). Cross-rig channel overlaps within
// the registry are rejected: a channel can only be owned by one rig
// at a time.
func (r *slackRigMappingRegistry) Set(rec slackRigMappingRecord) error {
	if rec.WorkspaceID == "" {
		return fmt.Errorf("slack rig mapping: workspace_id is required")
	}
	if rec.RigName == "" {
		return fmt.Errorf("slack rig mapping: rig_name is required")
	}
	if err := validateRigName(rec.RigName); err != nil {
		return fmt.Errorf("slack rig mapping: %w", err)
	}
	if rec.SlingTarget != "" {
		if err := validateSlingTarget(rec.SlingTarget); err != nil {
			return fmt.Errorf("slack rig mapping: %w", err)
		}
	}
	channels := dedupSortedChannels(rec.ChannelIDs)
	if len(channels) == 0 {
		return fmt.Errorf("slack rig mapping: at least one non-empty channel_id is required")
	}
	rec.ChannelIDs = channels

	r.mu.Lock()
	defer r.mu.Unlock()
	key := slackRigMappingKey(rec.WorkspaceID, rec.RigName)

	// Cross-rig overlap check: any new channel already in byChannel
	// pointing at a DIFFERENT rig is a conflict.
	for _, ch := range channels {
		ck := slackRigChannelKey(rec.WorkspaceID, ch)
		if owner, ok := r.byChannel[ck]; ok && owner != rec.RigName {
			return fmt.Errorf("slack rig mapping: channel %q is already bound to rig %q in workspace %q",
				ch, owner, rec.WorkspaceID)
		}
	}

	if existing, ok := r.byKey[key]; ok {
		// Preserve CreatedAt across idempotent re-sets.
		rec.CreatedAt = existing.CreatedAt
		// Drop the previous channel set from the inverted index so
		// channels no longer in the new record are released.
		for _, ch := range existing.ChannelIDs {
			delete(r.byChannel, slackRigChannelKey(rec.WorkspaceID, ch))
		}
	}
	r.byKey[key] = rec
	for _, ch := range channels {
		r.byChannel[slackRigChannelKey(rec.WorkspaceID, ch)] = rec.RigName
	}
	return r.saveLocked()
}

// Remove deletes the rig mapping for (workspaceID, rigName) if
// present. Returns whether an entry existed; missing entries are not
// an error so callers can treat Remove as idempotent.
func (r *slackRigMappingRegistry) Remove(workspaceID, rigName string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := slackRigMappingKey(workspaceID, rigName)
	existing, existed := r.byKey[key]
	if !existed {
		return false, nil
	}
	delete(r.byKey, key)
	for _, ch := range existing.ChannelIDs {
		delete(r.byChannel, slackRigChannelKey(workspaceID, ch))
	}
	if err := r.saveLocked(); err != nil {
		return existed, err
	}
	return existed, nil
}

// AllSorted returns every registered rig mapping, sorted by composite
// key (<workspace_id>:<rig_name>). Deterministic order keeps `gc
// slack status` and JSON dumps diff-stable.
func (r *slackRigMappingRegistry) AllSorted() []slackRigMappingRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]slackRigMappingRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// dedupSortedChannels returns a new slice with empty entries dropped,
// duplicates removed, and entries sorted lexicographically. Returning
// a new slice (rather than mutating in place) keeps callers'
// immutable-input contract intact.
func dedupSortedChannels(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// validateSlingTarget enforces the `<rig>/<role>` shape required by
// `gc sling --target`. The discord-pack convention (e.g.
// `mission-control/polecat`) is the wire contract: a single forward
// slash, two non-empty segments, no whitespace or control characters,
// no backslashes, no extra slashes. Role names are operator-supplied
// (ZFC: never hardcoded in Go); this only checks structural shape.
func validateSlingTarget(target string) error {
	if target == "" {
		return fmt.Errorf("sling_target is required")
	}
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		return fmt.Errorf("sling_target %q must be of the form <rig>/<role>", target)
	}
	for _, seg := range parts {
		if seg == "" {
			return fmt.Errorf("sling_target %q has empty segment", target)
		}
		for _, r := range seg {
			if unicode.IsSpace(r) {
				return fmt.Errorf("sling_target %q must not contain whitespace", target)
			}
			if unicode.IsControl(r) {
				return fmt.Errorf("sling_target %q must not contain control characters", target)
			}
			if r == '\\' {
				return fmt.Errorf("sling_target %q must not contain backslashes", target)
			}
		}
	}
	return nil
}

// validateRigName rejects whitespace, slash, backslash, and control
// characters. Rig names are operator-supplied identifiers used as
// part of the composite key on disk and as the JSON object key after
// load — staying in a printable-non-separator subset prevents both
// path-traversal-like bugs and unparseable on-disk keys.
func validateRigName(name string) error {
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("rig_name %q must not contain whitespace", name)
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("rig_name %q must not contain control characters", name)
		}
		if r == '/' || r == '\\' {
			return fmt.Errorf("rig_name %q must not contain path separators", name)
		}
	}
	return nil
}

// maxRigMappingBytes caps the size of the JSON rig-mapping file
// we'll read off disk. Rig mappings are at most a few hundred records
// of a fixed shape, so 10 MiB is several orders of magnitude over a
// healthy install. A file beyond that is either corrupt or hostile
// and must not be loaded. Per-file constant (rather than a shared
// helper) keeps each registry's size policy explicit at the call
// site.
const maxRigMappingBytes = 10 << 20 // 10 MiB

func (r *slackRigMappingRegistry) load() error {
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
	data, err := io.ReadAll(io.LimitReader(f, maxRigMappingBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", r.diskPath, err)
	}
	if int64(len(data)) > maxRigMappingBytes {
		return fmt.Errorf("registry file %s exceeds %d bytes", r.diskPath, maxRigMappingBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var stored map[string]slackRigMappingRecord
	if err := dec.Decode(&stored); err != nil {
		return fmt.Errorf("decode slack rig mapping store: %w", err)
	}

	// Validate each record. Operator hand-edits with empty/invalid
	// fields are surfaced rather than silently dropped.
	for key, rec := range stored {
		if rec.WorkspaceID == "" || rec.RigName == "" {
			return fmt.Errorf("slack rig mapping store: record %q missing required workspace_id or rig_name", key)
		}
		if err := validateRigName(rec.RigName); err != nil {
			return fmt.Errorf("slack rig mapping store: record %q: %w", key, err)
		}
		if rec.SlingTarget != "" {
			if err := validateSlingTarget(rec.SlingTarget); err != nil {
				return fmt.Errorf("slack rig mapping store: record %q: %w", key, err)
			}
		}
		if len(rec.ChannelIDs) == 0 {
			return fmt.Errorf("slack rig mapping store: record %q has empty channel_ids", key)
		}
		// Reject duplicate channels within a single record.
		seen := make(map[string]struct{}, len(rec.ChannelIDs))
		for _, ch := range rec.ChannelIDs {
			if ch == "" {
				return fmt.Errorf("slack rig mapping store: record %q has empty channel_id entry", key)
			}
			if _, dup := seen[ch]; dup {
				return fmt.Errorf("slack rig mapping store: record %q has duplicate channel_id %q", key, ch)
			}
			seen[ch] = struct{}{}
		}
	}

	r.byKey = stored
	if r.byKey == nil {
		r.byKey = make(map[string]slackRigMappingRecord)
	}

	// Rebuild byChannel. Process keys in sorted order so first-by-
	// sorted-key wins on cross-record overlap (deterministic), and
	// log a WARN when overlap is detected.
	r.byChannel = make(map[string]string)
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec := r.byKey[k]
		for _, ch := range rec.ChannelIDs {
			ck := slackRigChannelKey(rec.WorkspaceID, ch)
			if existing, ok := r.byChannel[ck]; ok && existing != rec.RigName {
				log.Printf("WARN: slack rig mapping store: channel %q in workspace %q is claimed by both rig %q and rig %q (hand-edited?); rig %q wins for resolver",
					ch, rec.WorkspaceID, existing, rec.RigName, existing)
				continue
			}
			r.byChannel[ck] = rec.RigName
		}
	}
	return nil
}

func (r *slackRigMappingRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir slack rig mapping store dir %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod slack rig mapping store dir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode slack rig mapping store: %w", err)
	}
	f, err := os.CreateTemp(dir, "rig_mappings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create slack rig mapping store tmp: %w", err)
	}
	tmpName := f.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod slack rig mapping store tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("write slack rig mapping store tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close slack rig mapping store tmp: %w", err)
	}
	if err := os.Rename(tmpName, r.diskPath); err != nil {
		cleanup()
		return fmt.Errorf("rename slack rig mapping store: %w", err)
	}
	return nil
}
