package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// channelMappingDiskRecord is the byte-for-byte mirror of
// cmd/gc.slackChannelMappingRecord (cmd/gc/slack_channel_mapping.go).
// Keep the JSON tags in lockstep with the Go writer; the on-disk file
// at <cityPath>/.gc/slack/channel_mappings.json is the only contract
// between gc and this adapter.
type channelMappingDiskRecord struct {
	WorkspaceID string    `json:"workspace_id"`
	ChannelID   string    `json:"channel_id"`
	TargetKind  string    `json:"target_kind"` // "rig" or "session"
	TargetID    string    `json:"target_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	channelMappingTargetKindRig     = "rig"
	channelMappingTargetKindSession = "session"
)

// channelMappingRegistry is a read-mostly in-memory view of the
// channel_mappings.json file written by `gc slack map-channel`. The
// adapter loads it once at startup; it does NOT watch the file for
// changes — operators must restart the adapter to pick up new
// bindings. This is intentional: a watcher introduces races against
// in-flight Slack interactions, and slash-command latency budget
// (Slack's 3s) is too tight to retry.
type channelMappingRegistry struct {
	mu       sync.RWMutex
	byKey    map[string]channelMappingDiskRecord
	diskPath string
}

func channelMappingKey(workspaceID, channelID string) string {
	return workspaceID + ":" + channelID
}

// newChannelMappingRegistry opens the registry at diskPath. A missing
// file yields an empty registry (tolerant load). A file with a record
// carrying an unknown target_kind is rejected at startup so a corrupt
// upstream write cannot silently be served as policy.
func newChannelMappingRegistry(diskPath string) (*channelMappingRegistry, error) {
	r := &channelMappingRegistry{
		byKey:    make(map[string]channelMappingDiskRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load channel mapping registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// Get returns the record for (workspaceID, channelID), plus a bool
// indicating whether one is registered.
func (r *channelMappingRegistry) Get(workspaceID, channelID string) (channelMappingDiskRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byKey[channelMappingKey(workspaceID, channelID)]
	return rec, ok
}

// Len returns the number of records currently loaded. Read-locked so
// callers (e.g. startup logs) don't race with concurrent Set in tests.
func (r *channelMappingRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKey)
}

// Set is provided for tests only. Production reads only — operator
// writes go through `gc slack map-channel`.
func (r *channelMappingRegistry) Set(rec channelMappingDiskRecord) error {
	if rec.WorkspaceID == "" || rec.ChannelID == "" {
		return fmt.Errorf("channel mapping: workspace_id and channel_id required")
	}
	if rec.TargetKind != channelMappingTargetKindRig &&
		rec.TargetKind != channelMappingTargetKindSession {
		return fmt.Errorf("channel mapping: target_kind %q must be %q or %q",
			rec.TargetKind, channelMappingTargetKindRig, channelMappingTargetKindSession)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[channelMappingKey(rec.WorkspaceID, rec.ChannelID)] = rec
	return r.saveLocked()
}

// maxRegistryBytes caps the size of the JSON registry file we'll
// load off disk. Channel mappings are a few hundred records of a
// fixed shape; 10 MiB is several orders of magnitude over a healthy
// install. A file beyond that is either corrupt or hostile and must
// not be loaded.
const maxRegistryBytes = 10 << 20 // 10 MiB

func (r *channelMappingRegistry) load() error {
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
	data, err := io.ReadAll(io.LimitReader(f, maxRegistryBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", r.diskPath, err)
	}
	if int64(len(data)) > maxRegistryBytes {
		return fmt.Errorf("registry file %s exceeds %d bytes", r.diskPath, maxRegistryBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var stored map[string]channelMappingDiskRecord
	if err := dec.Decode(&stored); err != nil {
		return fmt.Errorf("decode channel mapping store: %w", err)
	}
	for key, rec := range stored {
		if rec.TargetKind != channelMappingTargetKindRig &&
			rec.TargetKind != channelMappingTargetKindSession {
			return fmt.Errorf("channel mapping store: record %q has invalid target_kind %q (must be %q or %q)",
				key, rec.TargetKind, channelMappingTargetKindRig, channelMappingTargetKindSession)
		}
	}
	if stored != nil {
		r.byKey = stored
	}
	return nil
}

func (r *channelMappingRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	dir := filepath.Dir(r.diskPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir channel mapping store dir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(r.byKey, "", "  ")
	if err != nil {
		return fmt.Errorf("encode channel mapping store: %w", err)
	}
	return writeFile0600(r.diskPath, data)
}

// writeFile0600 atomically writes data to path with 0o600 perms.
// Uses os.CreateTemp so two concurrent writers in the same directory
// (in tests) don't clobber each other's temp file before the rename.
// Helper exposed so tests can seed a corrupt file at the same perms
// the production writer would use.
func writeFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp in %q: %w", dir, err)
	}
	tmpName := f.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod %q: %w", tmpName, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("write %q: %w", tmpName, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %q -> %q: %w", tmpName, path, err)
	}
	return nil
}

// slackInteractionResponse is the ephemeral envelope Slack expects on
// a slash-command HTTP response.
type slackInteractionResponse struct {
	ResponseType string `json:"response_type"`
	Text         string `json:"text"`
}

// writeEphemeral writes status with an ephemeral JSON body. Errors
// from Encode are logged but not surfaced — the slash-command response
// is best-effort and Slack treats any 2xx with empty body as success.
func writeEphemeral(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(slackInteractionResponse{
		ResponseType: "ephemeral",
		Text:         text,
	}); err != nil {
		log.Printf("slack interactions: encode response: %v", err)
	}
}

// handleSlackInteractions serves POST /slack/interactions — the
// public webhook for Slack slash-command and (eventually) block-action
// payloads. HMAC-verified with cfg.slackSigningKey. Slash commands
// resolve through resolveChannelTarget — per-channel `map-channel`
// bindings (cby.3) are overrides on top of the rig→{channels} default
// (cby.4); channel mapping wins. Block-action payloads return a clear
// "not yet supported" response (tracked as gc-cby.17).
//
// Slack's 3-second response deadline means dispatch to gc happens in a
// goroutine; the HTTP response is always immediate. Errors from the
// goroutine are logged.
func handleSlackInteractions(cfg config, mapReg *channelMappingRegistry, rigReg *rigMappingRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if !verifySlackSignature(cfg.slackSigningKey, r.Header.Get("X-Slack-Request-Timestamp"), body, r.Header.Get("X-Slack-Signature")) {
			log.Printf("slack interactions: signature verify FAILED")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		form, err := url.ParseQuery(string(body))
		if err != nil {
			log.Printf("slack interactions: parse form: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(form) == 0 {
			http.Error(w, "empty form body", http.StatusBadRequest)
			return
		}

		// Detect block-action / view-submission payloads. Slack sends
		// these as a single `payload=<JSON>` field; slash commands set
		// `command` instead. Tracked as gc-cby.17 — return a clear
		// "not yet supported" message so users aren't left waiting.
		if form.Get("payload") != "" && form.Get("command") == "" {
			writeEphemeral(w, http.StatusOK,
				"Interactivity payloads (block actions, view submissions) are not yet supported by this slack-pack version. Tracked as gc-cby.17.")
			return
		}

		teamID := form.Get("team_id")
		channelID := form.Get("channel_id")
		command := form.Get("command")
		text := form.Get("text")
		userID := form.Get("user_id")

		if teamID == "" {
			log.Printf("slack interactions: missing team_id in slash-command form")
			http.Error(w, "team_id mismatch", http.StatusUnauthorized)
			return
		}
		if cfg.accountID != "" && teamID != cfg.accountID {
			log.Printf("slack interactions: team_id %q does not match configured workspace %q", teamID, cfg.accountID)
			http.Error(w, "team_id mismatch", http.StatusUnauthorized)
			return
		}
		if cfg.accountID == "" {
			log.Printf("slack interactions: SLACK_WORKSPACE_ID is empty; accepting team_id=%q without verification (single-tenant deployment)", teamID)
		}

		if command == "" || channelID == "" {
			http.Error(w, "missing required slash-command fields", http.StatusBadRequest)
			return
		}

		rec, source, ok := resolveChannelTarget(mapReg, rigReg, teamID, channelID)
		if !ok {
			writeEphemeral(w, http.StatusOK, fmt.Sprintf(
				"No binding for this channel. Run `gc slack map-channel %s --workspace-id %s --rig <name>` (or `--session <id>`), or bind a rig set with `gc slack map-rig <name> --workspace-id %s --channel %s`.",
				channelID, teamID, teamID, channelID))
			return
		}
		log.Printf("interaction: workspace=%q channel=%q source=%s target=%s/%s",
			teamID, channelID, source, rec.TargetKind, rec.TargetID)

		switch rec.TargetKind {
		case channelMappingTargetKindSession:
			writeEphemeral(w, http.StatusOK, fmt.Sprintf(
				"Routing %s to session %s…", command, rec.TargetID))
			go dispatchSlashCommandToSession(cfg, rec.TargetID, command, text, channelID, teamID, userID)
		case channelMappingTargetKindRig:
			writeEphemeral(w, http.StatusOK, fmt.Sprintf(
				"Channel %s is bound to rig %s (source=%s). Rig-target dispatch is implemented in a follow-up bead (gc-cby.18); the binding is recorded but not yet routed.",
				channelID, rec.TargetID, source))
		default:
			// load() rejects unknown target_kind, so reaching this branch
			// means the registry was mutated mid-flight by another
			// process. Fail closed.
			log.Printf("slack interactions: unexpected target_kind %q for %q/%q", rec.TargetKind, teamID, channelID)
			writeEphemeral(w, http.StatusOK,
				"Channel binding is in an unexpected state; please re-run `gc slack map-channel`.")
		}
	}
}

// dispatchSlashCommandToSession POSTs the slash command text as a
// system reminder to gc's session-message endpoint, mirroring the
// shape used by dispatchToAliasedSession. Best-effort: errors are
// logged; the user's HTTP response was already sent.
func dispatchSlashCommandToSession(cfg config, sessionID, command, text, channelID, teamID, userID string) {
	body := fmt.Sprintf(
		"<system-reminder>\n"+
			"Slack slash-command %s arrived from channel %s (workspace %s) by user %s.\n"+
			"\n"+
			"Command text:\n"+
			"%s\n"+
			"\n"+
			"To reply in that channel, write your reply to a tmpfile and run:\n"+
			"  gc slack publish-to-channel \\\n"+
			"    --conversation-id %s \\\n"+
			"    --body-file <tmpfile>\n"+
			"</system-reminder>",
		command, channelID, teamID, userID, text, channelID,
	)
	payload, err := json.Marshal(gcSessionMessageRequest{Message: body})
	if err != nil {
		log.Printf("slack interactions: marshal session-message body: %v", err)
		return
	}
	target := fmt.Sprintf("%s/v0/city/%s/session/%s/messages",
		cfg.gcAPIBase, cfg.cityName, sessionID)
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		log.Printf("slack interactions: build session-message request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "gc-slack-adapter-interactions")

	// Use a bounded client timeout so a hung gc API doesn't pin the
	// goroutine indefinitely. Slack's 3s deadline already fired on the
	// caller; this timeout is for the dispatch leg only.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("slack interactions: POST %s: %v", target, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("slack interactions: %s -> %s: %s", target, resp.Status, string(respBody))
		return
	}
	log.Printf("slack interactions: dispatched command=%q to session=%s OK", command, sessionID)
}
