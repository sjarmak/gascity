package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
//
// Log/CLI policy (gc-cby.13): SigningSecret, ManifestRaw, ManifestPath,
// and BotUserID MUST NOT be passed through fmt.Fprintf, log.*, or any
// human-facing output sink. DisplayName is operator-supplied via the
// uploaded manifest and MUST be passed through sanitizeForLog before
// printing. Use safeLogFields() to obtain a struct that exposes only
// the allowlisted fields with DisplayName already sanitized.
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

// slackAppLogView is the safe-for-print projection of slackAppRecord.
// It intentionally omits SigningSecret, ManifestRaw, ManifestPath, and
// BotUserID (gc-cby.13 deny-list); DisplayName is pre-sanitized.
// Adding a new sensitive field to slackAppRecord must NOT add a field
// here without explicit policy review.
type slackAppLogView struct {
	WorkspaceID       string
	AppID             string
	DisplayName       string
	ScopeCount        int
	SlashCommandCount int
	ImportedAt        time.Time
}

// safeLogFields returns the allowlisted, sanitized projection of r for
// human-facing output. See slackAppRecord's "Log/CLI policy" comment
// for the deny-list rationale.
func (r slackAppRecord) safeLogFields() slackAppLogView {
	return slackAppLogView{
		WorkspaceID:       r.WorkspaceID,
		AppID:             r.AppID,
		DisplayName:       sanitizeForLog(r.DisplayName),
		ScopeCount:        len(r.Scopes),
		SlashCommandCount: len(r.SlashCommands),
		ImportedAt:        r.ImportedAt,
	}
}

// slackAppJSONView is the safe-for-emission projection of slackAppRecord
// used by `gc slack status --json` (gc-cby.13). It excludes
// SigningSecret (HMAC verification credential, populated post-OAuth in
// gc-cby.9), ManifestRaw (large, redundant with the on-disk file), and
// ManifestPath/BotUserID (operator-internal). The on-disk apps.json
// continues to serialize the full slackAppRecord through json.Marshal
// — that path is the persistence contract and intentionally retains
// every field. This view is for the operator-facing `--json` CLI
// projection only.
type slackAppJSONView struct {
	WorkspaceID   string    `json:"workspace_id"`
	AppID         string    `json:"app_id"`
	DisplayName   string    `json:"display_name,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	SlashCommands []string  `json:"slash_commands,omitempty"`
	ImportedAt    time.Time `json:"imported_at"`
}

// safeJSONFields returns the allowlisted projection of r for the
// `--json` CLI emission. DisplayName is NOT sanitized here — JSON
// encoding escapes control bytes (, \n, etc.) so the wire form
// is safe regardless of payload, and downstream consumers may want
// the original Unicode for non-CLI rendering.
func (r slackAppRecord) safeJSONFields() slackAppJSONView {
	return slackAppJSONView{
		WorkspaceID:   r.WorkspaceID,
		AppID:         r.AppID,
		DisplayName:   r.DisplayName,
		Scopes:        r.Scopes,
		SlashCommands: r.SlashCommands,
		ImportedAt:    r.ImportedAt,
	}
}

// ansiCSIPattern matches complete ANSI CSI sequences per ECMA-48:
// ESC [ , optional parameter bytes 0x30-0x3f, optional intermediate
// bytes 0x20-0x2f, terminator byte 0x40-0x7e. CSI is the dominant
// terminal-corrupting escape family.
//
// Non-CSI escape forms (OSC `\x1b]...\x07`, DCS `\x1bP...\x1b\`,
// SS2/SS3, single-char escapes) are NOT matched by this regex —
// the bare ESC at the head of those sequences is instead stripped
// by the control-byte loop in sanitizeForLog. That defangs the
// terminal-control effect (no ESC means the kernel of the sequence
// never executes) but leaves the inert payload bytes (e.g. the
// "0;title" of an OSC) as plain text in the output. Acceptable for
// the gc-cby.13 threat model (terminal corruption + log-aggregator
// tripping); a tighter "strip every escape family entirely" pass
// would need additional patterns and is not required for any known
// attack class against display_name.
var ansiCSIPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[\x40-\x7e]`)

// sanitizeForLog returns s with bytes that corrupt terminals or log
// aggregators removed: ANSI CSI sequences, control bytes below 0x20
// (except horizontal tab), DEL (0x7f), and any invalid UTF-8 byte.
// Newline (0x0a) and carriage return (0x0d) are STRIPPED — log
// aggregators and `%s`-format printers split on those bytes, so
// preserving them in operator-supplied strings would let an attacker
// forge log lines or overwrite the line in a TTY.
//
// Legitimate Unicode (CJK, emoji, accented Latin) passes through
// unchanged. Used at every CLI/log boundary that prints
// operator-supplied strings such as slackAppRecord.DisplayName
// (gc-cby.13).
func sanitizeForLog(s string) string {
	if s == "" {
		return s
	}
	withoutCSI := ansiCSIPattern.ReplaceAllString(s, "")
	// Builder size: upper bound — the loop only removes bytes.
	var b strings.Builder
	b.Grow(len(withoutCSI))
	for i := 0; i < len(withoutCSI); {
		r, size := utf8.DecodeRuneInString(withoutCSI[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		i += size
		if r < 0x20 && r != '\t' {
			continue
		}
		if r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
