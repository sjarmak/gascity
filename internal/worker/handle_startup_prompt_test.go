package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

func newPromptTestHandle(t *testing.T, resolve StartupPromptResolver) (*SessionHandle, *runtime.Fake) {
	t.Helper()
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	manager := sessionpkg.NewManagerWithOptions(store, sp)
	handle, err := NewSessionHandle(SessionHandleConfig{
		Manager: manager,
		Session: SessionSpec{
			Profile:  ProfileClaudeTmuxCLI,
			Template: "probe",
			Title:    "Probe",
			Command:  "claude",
			WorkDir:  t.TempDir(),
			Provider: "claude",
		},
		ResolveStartupPrompt: resolve,
	})
	if err != nil {
		t.Fatalf("NewSessionHandle: %v", err)
	}
	return handle, sp
}

// The delivery half of dr-5fek: the startup prompt the resolver renders has to
// reach the runtime provider's Start config, because that is what the tmux
// adapter turns into the process command line.
//
// Unlike the first version of this test, the prompt is NOT injected by hand into
// SessionSpec.Hints -- it arrives through the production ResolveStartupPrompt
// seam, so the test fails if that seam is not wired.
func TestSessionHandleAttachDeliversResolvedStartupPrompt(t *testing.T) {
	const suffix = "'read the brief and start'"

	handle, sp := newPromptTestHandle(t, func(sessionpkg.Info, map[string]string) (StartupPrompt, error) {
		return StartupPrompt{
			PromptSuffix: suffix,
			Env:          map[string]string{"GC_STARTUP_PROMPT_DELIVERED": "1"},
		}, nil
	})

	if _, err := handle.Create(context.Background(), CreateModeDeferred); err != nil {
		t.Fatalf("Create(deferred): %v", err)
	}
	if err := handle.Attach(context.Background()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	start := firstCall(sp.Calls, "Start")
	if start == nil {
		t.Fatalf("runtime calls = %#v, want Start", sp.Calls)
	}
	if start.Config.PromptSuffix != suffix {
		t.Fatalf("Start config PromptSuffix = %q, want %q: the handle path dropped the startup prompt (dr-5fek)",
			start.Config.PromptSuffix, suffix)
	}
	if got := start.Config.Env["GC_STARTUP_PROMPT_DELIVERED"]; got != "1" {
		t.Fatalf("Start config Env[GC_STARTUP_PROMPT_DELIVERED] = %q, want \"1\"", got)
	}
}

// Rendering a startup prompt is NOT a read: it stages provider overlays and
// writes settings and skill snapshots. So it must happen only on paths that
// actually bring a runtime up. This is the regression guard for the review
// finding that sank the first attempt, where the prompt was resolved eagerly at
// handle construction and every read-only operation mutated the city on disk.
func TestSessionHandleReadOnlyOperationsDoNotResolveStartupPrompt(t *testing.T) {
	calls := 0
	handle, _ := newPromptTestHandle(t, func(sessionpkg.Info, map[string]string) (StartupPrompt, error) {
		calls++
		return StartupPrompt{}, nil
	})

	if _, err := handle.Create(context.Background(), CreateModeDeferred); err != nil {
		t.Fatalf("Create(deferred): %v", err)
	}
	if calls != 0 {
		t.Fatalf("startup prompt resolved %d time(s) during Create: handle construction must not render templates", calls)
	}

	if _, err := handle.State(context.Background()); err != nil {
		t.Fatalf("State: %v", err)
	}
	// Peek/Stop/Kill may legitimately refuse in this lifecycle state (a
	// start-pending session does not accept "suspend"). Their return values are
	// not the subject here -- reaching them at all is, because whether they
	// succeed or refuse they must never render a template.
	_, _ = handle.Peek(context.Background(), 10)
	_ = handle.Stop(context.Background())
	_ = handle.Kill(context.Background())

	if calls != 0 {
		t.Fatalf("startup prompt resolved %d time(s) across State/Peek/Stop/Kill, want 0: read-only operations must not stage overlays or write settings", calls)
	}
}

// A configured agent whose template will not resolve must fail the start rather
// than silently bring the session up unprimed -- that silent degrade IS the
// defect dr-5fek is about, so reproducing it in the fix would be self-defeating.
func TestSessionHandleStartPropagatesStartupPromptError(t *testing.T) {
	wantErr := errors.New("template resolution exploded")
	handle, sp := newPromptTestHandle(t, func(sessionpkg.Info, map[string]string) (StartupPrompt, error) {
		return StartupPrompt{}, wantErr
	})

	if _, err := handle.Create(context.Background(), CreateModeDeferred); err != nil {
		t.Fatalf("Create(deferred): %v", err)
	}
	err := handle.Attach(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Attach error = %v, want %v: a failed prompt resolution must not start the session unprimed", err, wantErr)
	}
	if start := firstCall(sp.Calls, "Start"); start != nil {
		t.Fatalf("runtime Start was called despite the prompt resolution failing: %#v", start)
	}
}

// FirstProviderSessionStart is exported so cmd/gc applies the startup-prompt
// delivery rule from this predicate rather than a second copy. Pin the contract
// at the boundary: a resume must not be mistaken for a first launch, or every
// attach would replay the prompt as a fresh first turn.
func TestFirstProviderSessionStartExportedContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    sessionpkg.State
		metadata map[string]string
		want     bool
	}{
		{"start pending, never started", sessionpkg.StateStartPending, nil, true},
		{"creating, never started", sessionpkg.StateCreating, nil, true},
		{"already active", sessionpkg.StateActive, nil, false},
		{"start pending but creation completed", sessionpkg.StateStartPending, map[string]string{"creation_complete_at": "2026-08-09T00:00:00Z"}, false},
		{"start pending but config hash recorded", sessionpkg.StateStartPending, map[string]string{"started_config_hash": "deadbeef"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstProviderSessionStart(tc.state, tc.metadata); got != tc.want {
				t.Fatalf("FirstProviderSessionStart(%q, %v) = %v, want %v", tc.state, tc.metadata, got, tc.want)
			}
		})
	}
}

// The guard above constructs a SessionHandle directly, which cannot see a
// regression reintroduced in the FACTORY wiring — and the factory's eager
// ResolveSessionRuntime hook is exactly where the original defect lived. This
// drives the production path (NewFactory -> SessionByID -> sessionFromRecord)
// and counts renders on both hooks: the runtime resolver may run eagerly, the
// startup-prompt resolver may not.
func TestFactorySessionByIDDoesNotResolveStartupPromptForReadOnlyUse(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()

	promptCalls, runtimeCalls := 0, 0
	factory, err := NewFactory(FactoryConfig{
		Store:    store,
		Provider: sp,
		ResolveSessionRuntime: func(sessionpkg.Info, string, map[string]string) (*ResolvedRuntime, error) {
			runtimeCalls++
			return nil, nil
		},
		ResolveStartupPrompt: func(sessionpkg.Info, map[string]string) (StartupPrompt, error) {
			promptCalls++
			return StartupPrompt{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	seed, err := factory.Session(SessionSpec{
		Profile:  ProfileClaudeTmuxCLI,
		Template: "probe",
		Title:    "Probe",
		Command:  "claude",
		WorkDir:  t.TempDir(),
		Provider: "claude",
	})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	info, err := seed.Create(context.Background(), CreateModeDeferred)
	if err != nil {
		t.Fatalf("Create(deferred): %v", err)
	}

	promptCalls = 0
	handle, err := factory.SessionByID(info.ID)
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("startup prompt resolved %d time(s) building a handle, want 0: template rendering stages overlays and writes settings", promptCalls)
	}

	_, _ = handle.State(context.Background())
	_, _ = handle.Peek(context.Background(), 10)
	_ = handle.Stop(context.Background())
	_ = handle.Kill(context.Background())

	if promptCalls != 0 {
		t.Fatalf("startup prompt resolved %d time(s) across SessionByID + State/Peek/Stop/Kill, want 0", promptCalls)
	}
	if runtimeCalls == 0 {
		t.Fatal("runtime resolver never ran: the test is not exercising the production factory resolution path")
	}
}
