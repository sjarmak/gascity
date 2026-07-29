package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/session/sessiontest"
)

// TestMaybeEnqueueStepKickoffNudge pins the #4382 fix: a reconciler-spawned step
// session that is born unprimed (no startup prompt delivered at spawn) and carries
// a routed trigger bead must get a first-prompt kickoff nudge enqueued after its
// start commits, so the session's already-running `gc nudge poll` sidecar can
// deliver it. Sessions that were primed at spawn, or that carry no trigger bead
// (idle pool capacity), must be left alone.
//
// The enqueued nudge is fenced to the exact spawned session: Agent and SessionID
// are both the session bead ID, which equals the poller target's GC_SESSION_ID, so
// no other session can claim it.
func TestMaybeEnqueueStepKickoffNudge(t *testing.T) {
	t.Setenv("GC_BEADS", "file")

	const sessionID = "gws-step1"
	cases := []struct {
		name            string
		promptDelivered bool
		triggerBeadID   string
		id              string
		wantPending     int
	}{
		{name: "born-unprimed routed step enqueues kickoff", promptDelivered: false, triggerBeadID: "gp-decompose-step", id: sessionID, wantPending: 1},
		{name: "primed step is not re-nudged", promptDelivered: true, triggerBeadID: "gp-decompose-step", id: sessionID, wantPending: 0},
		{name: "unprimed non-step (no trigger bead) is left alone", promptDelivered: false, triggerBeadID: "", id: sessionID, wantPending: 0},
		{name: "missing session id is skipped", promptDelivered: false, triggerBeadID: "gp-decompose-step", id: "", wantPending: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var stderr bytes.Buffer
			prepared := preparedStart{
				candidate: startCandidate{info: sessionpkg.Info{
					ID:                  tc.id,
					TriggerBeadID:       tc.triggerBeadID,
					SessionNameMetadata: "rig/gc.task-decomposer-1",
				}},
				promptDelivered: tc.promptDelivered,
			}

			maybeEnqueueStepKickoffNudge(prepared, dir, beads.NewMemStore(), time.Now(), &stderr)

			var pending []queuedNudge
			if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
				pending = append(pending, state.Pending...)
				return nil
			}); err != nil {
				t.Fatalf("withNudgeQueueState: %v", err)
			}

			if len(pending) != tc.wantPending {
				t.Fatalf("pending nudges = %d, want %d; stderr: %s", len(pending), tc.wantPending, stderr.String())
			}
			if tc.wantPending == 0 {
				return
			}

			got := pending[0]
			if got.SessionID != tc.id {
				t.Errorf("nudge SessionID = %q, want %q (must fence to the spawned session's GC_SESSION_ID)", got.SessionID, tc.id)
			}
			if got.Agent != tc.id {
				t.Errorf("nudge Agent = %q, want %q (session ID is a poller queue key)", got.Agent, tc.id)
			}
			if got.Message == "" {
				t.Error("nudge Message is empty, want a first-prompt kickoff message")
			}
		})
	}
}

// TestExecutePlannedStartsEnqueuesStepKickoffNudge proves the #4382 wiring at BOTH
// commit sites: driving the real reconciler start+commit path for a born-unprimed
// routed step session (empty startup prompt + gc.trigger_bead_id) lands a kickoff
// nudge in the queue, fenced to the spawned session's bead ID. Without the fix the
// queue stays empty and the session sits idle forever. The sync subtest exercises
// the commit loop in executePlannedStartsTraced; the async subtest exercises the
// commit goroutine in enqueuePreparedStartWaveForCity.
func TestExecutePlannedStartsEnqueuesStepKickoffNudge(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("GC_BEADS", "file")
			dir := t.TempDir()
			sp := runtime.NewFake()
			store := beads.NewMemStore()
			clk := &clock.Fake{Time: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
			cfg := &config.City{Agents: []config.Agent{{Name: "worker"}}}

			// Empty Prompt ⇒ nothing is delivered at spawn (promptDelivered == false),
			// the born-unprimed condition #4382 describes.
			tp := TemplateParams{
				Command:      "claude --dangerously-skip-permissions",
				SessionName:  "worker",
				TemplateName: "worker",
				Prompt:       "",
				ResolvedProvider: &config.ResolvedProvider{
					Name:          "claude",
					Command:       "claude",
					PromptMode:    "arg",
					ResumeFlag:    "--resume",
					ResumeStyle:   "flag",
					SessionIDFlag: "--session-id",
				},
			}
			session, err := store.Create(beads.Bead{
				Title:  "worker",
				Type:   sessionBeadType,
				Labels: []string{sessionBeadLabel},
				Metadata: map[string]string{
					"session_name":       "worker",
					"template":           "worker",
					"state":              "asleep",
					"gc.trigger_bead_id": "gp-decompose-step",
					"generation":         "1",
					"instance_token":     "test-token",
				},
			})
			if err != nil {
				t.Fatalf("Create(session): %v", err)
			}

			opts := []startExecutionOption{
				withStartStabilityWaiter(immediateStartStabilityWaiter),
				withSessionStaleKeyDetectionWaiter(immediateSessionStaleKeyDetectionWaiter),
			}
			tracker := &asyncStartTracker{}
			if async {
				opts = append(opts, withAsyncStartExecution(), withAsyncStartTracker(tracker))
			}

			woken := executePlannedStartsTraced(
				context.Background(),
				[]startCandidate{{info: sessiontest.SeedBead(t, session), tp: tp, order: 0}},
				cfg,
				map[string]TemplateParams{"worker": tp},
				sp,
				store,
				"",
				dir,
				clk,
				events.Discard,
				5*time.Second,
				ioDiscard{},
				ioDiscard{},
				nil,
				opts...,
			)
			if woken != 1 {
				t.Fatalf("woken = %d, want 1", woken)
			}
			// The async commit + enqueue run in a goroutine after the call returns;
			// wait for it to finish before reading the queue.
			if async && !tracker.wait(5*time.Second) {
				t.Fatal("async start goroutines did not finish within timeout")
			}

			var pending []queuedNudge
			if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
				pending = append(pending, state.Pending...)
				return nil
			}); err != nil {
				t.Fatalf("withNudgeQueueState: %v", err)
			}
			if len(pending) != 1 {
				t.Fatalf("pending nudges = %d, want 1 (kickoff for born-unprimed routed step session)", len(pending))
			}
			if pending[0].SessionID != session.ID {
				t.Errorf("nudge SessionID = %q, want %q (spawned session bead ID)", pending[0].SessionID, session.ID)
			}
		})
	}
}
