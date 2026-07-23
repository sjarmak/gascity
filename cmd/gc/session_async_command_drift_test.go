package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/session/sessiontest"
)

// TestAsyncStartCommandDriftGate pins the gate that decides whether an async
// start's freshly spawned runtime is already outdated. Drift is only real when
// the stored command CHANGED during the spawn window: tp.Command is resolved
// from live config at prepare time, so a stored command that has not moved
// since the start was enqueued describes an EARLIER launch, not a newer desire.
// Reading it as drift is what produced the #4144 start→kill loop.
func TestAsyncStartCommandDriftGate(t *testing.T) {
	gate := func(enqueued, current, prepared string) bool {
		return asyncStartPreparedCommandStaleInfo(
			preparedStart{candidate: startCandidate{
				info: session.Info{ID: "sess-1", Command: enqueued},
				tp:   TemplateParams{Command: prepared},
			}},
			session.Info{ID: "sess-1", Command: current},
		)
	}

	tests := []struct {
		name      string
		enqueued  string
		current   string
		prepared  string
		wantStale bool
		why       string
	}{
		{
			name:      "stored command behind config",
			enqueued:  "claude --resume",
			current:   "claude --resume",
			prepared:  "opencode",
			wantStale: false,
			why:       "the bead records the previous launch; config moved first, so the spawned opencode runtime IS the desired one",
		},
		{
			name:      "command changed during startup",
			enqueued:  "claude --resume",
			current:   "codex exec",
			prepared:  "opencode",
			wantStale: true,
			why:       "a concurrent writer advanced the desired command past what this start launched",
		},
		{
			name:      "command changed during startup to the launched command",
			enqueued:  "claude --resume",
			current:   "opencode",
			prepared:  "opencode",
			wantStale: false,
			why:       "the bead caught up to exactly what we launched",
		},
		{
			name:      "stored command empty",
			enqueued:  "",
			current:   "",
			prepared:  "opencode",
			wantStale: false,
			why:       "an unset stored command carries no signal either way",
		},
		{
			name:      "prepared command empty",
			enqueued:  "claude --resume",
			current:   "claude --resume",
			prepared:  "",
			wantStale: false,
			why:       "an unresolved template command carries no signal either way",
		},
		{
			name:      "stored command appeared during startup",
			enqueued:  "",
			current:   "codex exec",
			prepared:  "opencode",
			wantStale: true,
			why:       "the bead gained a desired command mid-flight that is not what we launched",
		},
		{
			name:      "whitespace-only difference",
			enqueued:  "claude --resume",
			current:   "  opencode  ",
			prepared:  "opencode",
			wantStale: false,
			why:       "the gate compares trimmed commands",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gate(tt.enqueued, tt.current, tt.prepared); got != tt.wantStale {
				t.Errorf("asyncStartPreparedCommandStaleInfo(enqueued=%q, current=%q, prepared=%q) = %v, want %v — %s",
					tt.enqueued, tt.current, tt.prepared, got, tt.wantStale, tt.why)
			}
		})
	}
}

// TestRefreshAsyncStartAcceptsStoredCommandBehindConfig is the seam-level
// regression guard for #4144. A session bead created under the old agent config
// still records the old command after the operator switches the agent's
// provider. Every tick then resolved the new command, spawned it, and refused
// its own result as stale — killing the runtime and repeating forever (180
// consecutive iterations in the filed report). The refresh must accept the
// start, leaving the runtime alive and the in-flight lease held.
func TestRefreshAsyncStartAcceptsStoredCommandBehindConfig(t *testing.T) {
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title:  "mutator",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":   "chip_design__mutator-1",
			"state":          "creating",
			"instance_token": "tok-1",
			"command":        "claude --dangerously-skip-permissions",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := startResult{
		prepared: preparedStart{
			candidate: startCandidate{
				info: sessiontest.SeedBead(t, bead),
				// The provider switch already landed in config, so prepare
				// resolved the NEW command for this spawn.
				tp: TemplateParams{TemplateName: "mutator", Command: "opencode"},
			},
		},
		outcome: "success",
	}

	_, ok, cleanupRuntime, releaseInFlight := refreshAsyncStartResult(result, store, ioDiscard{})
	if !ok {
		t.Fatal("refreshAsyncStartResult ok=false for a bead whose stored command merely predates the config change; want the start to commit")
	}
	if cleanupRuntime {
		t.Error("cleanupRuntime=true; killing the freshly spawned runtime here is the #4144 start→kill loop")
	}
	if releaseInFlight {
		t.Error("releaseInFlight=true; a committing start must keep its in-flight lease")
	}
}

// TestRefreshAsyncStartDiscardsCommandChangedDuringStartup is the complement:
// when the desired command genuinely moves past the launched one WHILE the
// spawn is in flight, the result is discarded and the outdated runtime stopped.
func TestRefreshAsyncStartDiscardsCommandChangedDuringStartup(t *testing.T) {
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title:  "mutator",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":   "chip_design__mutator-1",
			"state":          "creating",
			"instance_token": "tok-1",
			"command":        "claude --dangerously-skip-permissions",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued := sessiontest.SeedBead(t, bead)
	// A second config change lands after the spawn goroutine was enqueued.
	if err := store.SetMetadata(bead.ID, "command", "codex exec"); err != nil {
		t.Fatal(err)
	}
	result := startResult{
		prepared: preparedStart{
			candidate: startCandidate{
				info: enqueued,
				tp:   TemplateParams{TemplateName: "mutator", Command: "opencode"},
			},
		},
		outcome: "success",
	}

	_, ok, cleanupRuntime, releaseInFlight := refreshAsyncStartResult(result, store, ioDiscard{})
	if ok {
		t.Fatal("refreshAsyncStartResult ok=true after the desired command moved mid-flight; want the outdated start discarded")
	}
	if !cleanupRuntime {
		t.Error("cleanupRuntime=false; the runtime we spawned runs a command nobody wants anymore")
	}
	if !releaseInFlight {
		t.Error("releaseInFlight=false; the discarded start must release its lease so the next tick retries")
	}
}

// TestCommitStartResultRecordsLaunchedCommand pins the second half of the
// #4144 fix: a committing start writes the command it actually launched onto
// the session bead, in the same atomic batch as the state transition. Without
// it the bead keeps naming a command the session never ran — `gc session
// attach` and the legacy respawn paths read that field — and the drift gate
// re-reads the stale value on every later start.
func TestCommitStartResultRecordsLaunchedCommand(t *testing.T) {
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title:  "mutator",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "chip_design__mutator-1",
			"state":        "creating",
			"command":      "claude --dangerously-skip-permissions",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 11, 1, 51, 0, 0, time.UTC)
	result := startResult{
		prepared: preparedStart{
			candidate: startCandidate{
				info: sessiontest.SeedBead(t, bead),
				tp: TemplateParams{
					SessionName:  "chip_design__mutator-1",
					TemplateName: "mutator",
					Command:      "opencode",
				},
			},
			coreHash: "core",
			liveHash: "live",
		},
		outcome:  "success",
		started:  now,
		finished: now.Add(time.Second),
	}

	if !commitStartResult(result, sessionFrontDoor(store), &clock.Fake{Time: now}, events.Discard, 0, ioDiscard{}, ioDiscard{}) {
		t.Fatal("commitStartResult returned false for a successful start")
	}
	got, err := store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["command"] != "opencode" {
		t.Errorf("command = %q, want %q (the bead must record the command this start actually launched)", got.Metadata["command"], "opencode")
	}
	if got.Metadata["state"] != "active" {
		t.Errorf("state = %q, want active (the command write must ride the existing commit batch, not replace it)", got.Metadata["state"])
	}
}
