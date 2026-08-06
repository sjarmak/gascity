package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
)

type partialRestartCleanupStore struct {
	beads.Store
	updateErr error
}

func (s partialRestartCleanupStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if kvs["restart_requested"] == "" && kvs["continuation_reset_pending"] == "" {
		if err := s.SetMetadata(id, "restart_requested", ""); err != nil {
			return err
		}
		return s.updateErr
	}
	return s.Store.SetMetadataBatch(id, kvs)
}

func (s partialRestartCleanupStore) Update(id string, opts beads.UpdateOpts) error {
	if opts.Metadata["restart_requested"] == "" && opts.Metadata["continuation_reset_pending"] == "" {
		return s.updateErr
	}
	return s.Store.Update(id, opts)
}

// TestRepro_dr_4o68_PinnedNamedHandoffReturnsCheckpointOnly reproduces the
// city-infra-pl failure: a pinned named session cannot truthfully promise that
// the controller will restart it. Handoff must durably save one self-mail,
// clear abandoned restart state, and return without waiting.
func TestRepro_dr_4o68_PinnedNamedHandoffReturnsCheckpointOnly(t *testing.T) {
	env := newRestartRequestTestEnv()
	recorder := events.NewFake()
	env.rec = recorder
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true", MaxActiveSessions: restartRequestTestIntPtr(1)}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "always"}},
	}
	sessionName := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	env.desiredState[sessionName] = TemplateParams{
		Command:          "true",
		SessionName:      sessionName,
		TemplateName:     "worker",
		ResolvedProvider: &config.ResolvedProvider{SessionIDFlag: "--session-id"},
	}
	sessionBead := env.createSessionBead(sessionName)
	env.setSessionMetadata(&sessionBead, map[string]string{
		namedSessionMetadataKey:      "true",
		namedSessionIdentityMetadata: "worker",
		namedSessionModeMetadata:     "always",
		"state":                      "active",
		"pin_awake":                  "true",
		"restart_requested":          "true",
		"continuation_reset_pending": "true",
	})
	if err := env.sp.Start(context.Background(), sessionName, runtime.Config{Command: "true"}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_SESSION_ID", sessionBead.ID); err != nil {
		t.Fatalf("SetMeta(GC_SESSION_ID): %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_RESTART_REQUESTED", "1"); err != nil {
		t.Fatalf("SetMeta(GC_RESTART_REQUESTED): %v", err)
	}
	dops := newDrainOps(env.sp)

	persistCalls := 0
	var stdout, stderr bytes.Buffer
	outcome := doHandoffWithOutcome(env.store, env.store, env.rec, dops, func() error {
		persistCalls++
		return errors.New("must not request a pinned restart")
	}, sessionName, sessionName, []string{"HANDOFF: durable checkpoint"}, &stdout, &stderr)

	if outcome.code != 0 {
		t.Fatalf("code = %d, want 0 for a durable checkpoint; stderr: %s", outcome.code, stderr.String())
	}
	if outcome.disposition != handoffDispositionCheckpointOnly {
		t.Fatalf("disposition = %q, want %q", outcome.disposition, handoffDispositionCheckpointOnly)
	}
	if persistCalls != 0 {
		t.Fatalf("persistRestart calls = %d, want 0", persistCalls)
	}
	if !strings.Contains(stdout.String(), "checkpoint only") {
		t.Fatalf("stdout = %q, want truthful checkpoint-only result", stdout.String())
	}
	if messages := listOpenMessagesBothTiers(t, env.store); len(messages) != 1 || messages[0].Title != "HANDOFF: durable checkpoint" {
		t.Fatalf("messages = %+v, want exactly one durable handoff mail", messages)
	}
	if len(recorder.Events) != 1 || recorder.Events[0].Type != events.MailSent {
		t.Fatalf("events = %+v, want exactly one MailSent event", recorder.Events)
	}
	requested, err := dops.isRestartRequested(sessionName)
	if err != nil {
		t.Fatalf("isRestartRequested: %v", err)
	}
	if requested {
		t.Fatal("runtime restart request remains armed")
	}
	got, err := env.store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("reload session bead: %v", err)
	}
	if got.Metadata["restart_requested"] != "" || got.Metadata["continuation_reset_pending"] != "" {
		t.Fatalf("restart residue remains: restart_requested=%q continuation_reset_pending=%q",
			got.Metadata["restart_requested"], got.Metadata["continuation_reset_pending"])
	}

	env.reconcileWithPoolDesiredAndDrainOps([]beads.Bead{got}, map[string]int{"worker": 1}, dops)
	if !env.sp.IsRunning(sessionName) {
		t.Fatalf("pinned session %q was restarted after checkpoint-only handoff", sessionName)
	}
}

func TestRepro_dr_4o68_PinnedNamedHandoffCleanupFailurePreservesRestartState(t *testing.T) {
	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true", MaxActiveSessions: restartRequestTestIntPtr(1)}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "always"}},
	}
	sessionName := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	sessionBead := env.createSessionBead(sessionName)
	env.setSessionMetadata(&sessionBead, map[string]string{
		namedSessionMetadataKey:      "true",
		namedSessionIdentityMetadata: "worker",
		namedSessionModeMetadata:     "always",
		"pin_awake":                  "true",
		"restart_requested":          "true",
		"continuation_reset_pending": "true",
	})
	if err := env.sp.Start(context.Background(), sessionName, runtime.Config{Command: "true"}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_SESSION_ID", sessionBead.ID); err != nil {
		t.Fatalf("SetMeta(GC_SESSION_ID): %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_RESTART_REQUESTED", "1"); err != nil {
		t.Fatalf("SetMeta(GC_RESTART_REQUESTED): %v", err)
	}
	dops := newDrainOps(env.sp)
	cleanupErr := errors.New("durable cleanup unavailable")
	sessStore := partialRestartCleanupStore{Store: env.store, updateErr: cleanupErr}

	var stdout, stderr bytes.Buffer
	outcome := doHandoffWithOutcome(env.store, sessStore, env.rec, dops, nil,
		sessionName, sessionName, []string{"HANDOFF: durable checkpoint"}, &stdout, &stderr)

	if outcome.code != 1 {
		t.Fatalf("code = %d, want 1 when durable cleanup fails", outcome.code)
	}
	if !strings.Contains(stderr.String(), cleanupErr.Error()) {
		t.Fatalf("stderr = %q, want cleanup failure", stderr.String())
	}
	if messages := listOpenMessagesBothTiers(t, env.store); len(messages) != 1 {
		t.Fatalf("messages = %+v, want exactly one durable handoff mail", messages)
	}
	requested, err := dops.isRestartRequested(sessionName)
	if err != nil {
		t.Fatalf("isRestartRequested: %v", err)
	}
	if !requested {
		t.Fatal("runtime restart request was cleared before durable cleanup committed")
	}
	got, err := env.store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("reload session bead: %v", err)
	}
	if got.Metadata["restart_requested"] != "true" || got.Metadata["continuation_reset_pending"] != "true" {
		t.Fatalf("restart state partially cleared: restart_requested=%q continuation_reset_pending=%q",
			got.Metadata["restart_requested"], got.Metadata["continuation_reset_pending"])
	}
}
