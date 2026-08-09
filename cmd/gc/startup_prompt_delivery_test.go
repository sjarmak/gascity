package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	session "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

// resolveTestStartupPrompt drives the production start-time resolver the worker
// Factory is handed, so these tests exercise the same function gc session
// attach reaches rather than a test-only shortcut.
func resolveTestStartupPrompt(t *testing.T, cfg *config.City, info session.Info, metadata map[string]string) worker.StartupPrompt {
	t.Helper()
	resolver := workerStartupPromptResolverWithConfig(t.TempDir(), nil, nil, cfg)
	if resolver == nil {
		t.Fatal("workerStartupPromptResolverWithConfig() = nil")
	}
	prompt, err := resolver(info, metadata)
	if err != nil {
		t.Fatalf("startup prompt resolver: %v", err)
	}
	return prompt
}

func startupPromptTestCity() *config.City {
	return &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Provider: "stub",
		}},
		Providers: map[string]config.ProviderSpec{
			"stub": {},
		},
	}
}

// The core dr-5fek regression: a handle-path start of a session that has no
// provider session yet must carry the rendered startup prompt on its hints.
// Before the fix the resolver returned hints with an empty PromptSuffix, so
// `gc session attach` recreated the tmux runtime with no prompt and the agent
// came up live but unprimed.
func TestWorkerSessionRuntimeResolverDeliversStartupPromptOnFirstStart(t *testing.T) {
	cfg := startupPromptTestCity()
	resolved := resolveTestStartupPrompt(t, cfg, session.Info{
		Template:  "worker",
		AgentName: "worker",
		State:     session.StateStartPending,
	}, nil)
	if strings.TrimSpace(resolved.PromptSuffix) == "" {
		t.Fatal("Hints.PromptSuffix is empty: the handle path would start this session unprimed (dr-5fek)")
	}
	if !strings.Contains(resolved.PromptSuffix, "worker") {
		t.Fatalf("PromptSuffix = %q, want it to carry the rendered prompt for agent \"worker\"", resolved.PromptSuffix)
	}
	if got := resolved.Env[startupPromptDeliveredEnv]; got != "1" {
		t.Fatalf("Env[%s] = %q, want \"1\" on a delivering start", startupPromptDeliveredEnv, got)
	}
}

// A resume onto a provider session that already exists must NOT replay the
// prompt as a fresh first turn: nothing on the command line, re-prime through
// the nudge instead.
func TestWorkerSessionRuntimeResolverRePrimesInsteadOfReplayingOnResume(t *testing.T) {
	cfg := startupPromptTestCity()
	resolved := resolveTestStartupPrompt(t, cfg, session.Info{
		Template:   "worker",
		AgentName:  "worker",
		State:      session.StateActive,
		SessionKey: "provider-session-abc",
	}, map[string]string{"started_config_hash": "deadbeef"})
	if resolved.PromptSuffix != "" {
		t.Fatalf("PromptSuffix = %q, want empty: a resume must not replay the startup prompt as argv", resolved.PromptSuffix)
	}
	if resolved.PromptFlag != "" {
		t.Fatalf("PromptFlag = %q, want empty on resume", resolved.PromptFlag)
	}
	if strings.TrimSpace(resolved.Nudge) == "" {
		t.Fatal("Nudge is empty on resume: the session is never re-primed")
	}
	if got := resolved.Env[startupPromptDeliveredEnv]; got != "1" {
		t.Fatalf("Env[%s] = %q, want \"1\" so hooks can tell primed from never-primed", startupPromptDeliveredEnv, got)
	}
}

// A stored SessionKey does not by itself mean "resume": until the provider
// session has actually been started once, the prompt still has to be delivered.
func TestWorkerSessionRuntimeResolverDeliversWhenSessionKeyPresentButNeverStarted(t *testing.T) {
	cfg := startupPromptTestCity()
	resolved := resolveTestStartupPrompt(t, cfg, session.Info{
		Template:   "worker",
		AgentName:  "worker",
		State:      session.StateStartPending,
		SessionKey: "provider-session-abc",
	}, nil)
	if strings.TrimSpace(resolved.PromptSuffix) == "" {
		t.Fatal("PromptSuffix is empty: a never-started session must still be primed even though a SessionKey is stored")
	}
}

// The default fixture uses a bare provider, which exercises only the argv-suffix
// delivery branch. The bug being fixed was a whole delivery path going missing,
// so pin the other prompt modes at the resolver seam too: a "none"-mode provider
// must still be primed, just through the nudge instead of argv.
func TestWorkerSessionRuntimeResolverDeliversAcrossPromptModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		spec        config.ProviderSpec
		wantSuffix  bool
		wantFlag    string
		wantInNudge bool
	}{
		{name: "arg mode delivers via argv suffix", spec: config.ProviderSpec{}, wantSuffix: true},
		{name: "flag mode delivers via argv suffix plus flag", spec: config.ProviderSpec{PromptMode: "flag", PromptFlag: "--prompt"}, wantSuffix: true, wantFlag: "--prompt"},
		{name: "none mode delivers via nudge", spec: config.ProviderSpec{PromptMode: "none"}, wantInNudge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := startupPromptTestCity()
			cfg.Providers["stub"] = tc.spec

			resolved := resolveTestStartupPrompt(t, cfg, session.Info{
				Template:  "worker",
				AgentName: "worker",
				State:     session.StateStartPending,
			}, nil)

			if tc.wantSuffix && strings.TrimSpace(resolved.PromptSuffix) == "" {
				t.Fatal("PromptSuffix empty: this prompt mode delivers via argv and the session would start unprimed")
			}
			if !tc.wantSuffix && resolved.PromptSuffix != "" {
				t.Fatalf("PromptSuffix = %q, want empty for this prompt mode", resolved.PromptSuffix)
			}
			if got := resolved.PromptFlag; got != tc.wantFlag {
				t.Fatalf("PromptFlag = %q, want %q", got, tc.wantFlag)
			}
			if tc.wantInNudge && !strings.Contains(resolved.Nudge, "worker") {
				t.Fatalf("Nudge = %q, want it to carry the rendered prompt", resolved.Nudge)
			}
			if got := resolved.Env[startupPromptDeliveredEnv]; got != "1" {
				t.Fatalf("Env[%s] = %q, want \"1\": every prompt mode must mark the session primed", startupPromptDeliveredEnv, got)
			}
		})
	}
}

// The shared rule itself, pinned directly. Both start paths call this; a change
// here changes the reconciler and the handle path together, which is the point.
func TestApplyStartupPromptDeliveryThreeWayRule(t *testing.T) {
	delivery := promptDeliveryResult{PromptSuffix: "'do the work'", Delivered: true}

	t.Run("first start delivers", func(t *testing.T) {
		cfg := runtime.Config{PromptSuffix: "'do the work'"}
		got := applyStartupPromptDelivery(&cfg, "do the work", "", delivery, true, false, true)
		if !got {
			t.Fatal("delivered = false, want true on a first start")
		}
		if cfg.PromptSuffix != "'do the work'" {
			t.Fatalf("PromptSuffix = %q, want it left in place", cfg.PromptSuffix)
		}
	})

	t.Run("forced fresh delivers even with a resume key", func(t *testing.T) {
		cfg := runtime.Config{PromptSuffix: "'do the work'"}
		got := applyStartupPromptDelivery(&cfg, "do the work", "", delivery, false, true, true)
		if !got {
			t.Fatal("delivered = false, want true on a forced-fresh start")
		}
		if cfg.PromptSuffix == "" {
			t.Fatal("PromptSuffix cleared on a forced-fresh start")
		}
	})

	t.Run("resume re-primes and reports not delivered", func(t *testing.T) {
		cfg := runtime.Config{PromptSuffix: "'do the work'", PromptFlag: "-p"}
		got := applyStartupPromptDelivery(&cfg, "do the work", "", delivery, false, false, true)
		if got {
			t.Fatal("delivered = true on a resume, want false: nothing reached a first-turn mechanism")
		}
		if cfg.PromptSuffix != "" || cfg.PromptFlag != "" {
			t.Fatalf("PromptSuffix=%q PromptFlag=%q, want both cleared on resume", cfg.PromptSuffix, cfg.PromptFlag)
		}
		if strings.TrimSpace(cfg.Nudge) == "" {
			t.Fatal("Nudge empty on resume: the session is never re-primed")
		}
		if cfg.Env[startupPromptDeliveredEnv] != "1" {
			t.Fatalf("Env[%s] = %q, want \"1\"", startupPromptDeliveredEnv, cfg.Env[startupPromptDeliveredEnv])
		}
	})

	t.Run("no resume key delivers", func(t *testing.T) {
		cfg := runtime.Config{PromptSuffix: "'do the work'"}
		got := applyStartupPromptDelivery(&cfg, "do the work", "", delivery, false, false, false)
		if !got {
			t.Fatal("delivered = false, want true when there is no provider session to resume onto")
		}
	})

	t.Run("empty prompt on resume stamps no marker", func(t *testing.T) {
		cfg := runtime.Config{}
		applyStartupPromptDelivery(&cfg, "", "", promptDeliveryResult{}, false, false, true)
		if _, ok := cfg.Env[startupPromptDeliveredEnv]; ok {
			t.Fatal("marker stamped for an empty prompt: observers cannot tell primed from never-primed")
		}
	})
}

// resolveTestStartupPromptErr is the error-returning form, for the cases where
// a failed resolution is the assertion.
func resolveTestStartupPromptErr(t *testing.T, cfg *config.City, info session.Info, metadata map[string]string) (worker.StartupPrompt, error) {
	t.Helper()
	resolver := workerStartupPromptResolverWithConfig(t.TempDir(), nil, nil, cfg)
	if resolver == nil {
		t.Fatal("workerStartupPromptResolverWithConfig() = nil")
	}
	return resolver(info, metadata)
}

// A legacy or pooled record whose raw template no longer matches a configured
// agent directly can still be identified through its persisted agent_name.
// Looking only at info.Template misclassifies it as a provider-only session and
// starts it unprimed — the defect this bead is about, reached another way.
func TestResolveStartupPromptRecoversIdentityFromAgentName(t *testing.T) {
	cfg := startupPromptTestCity()
	resolved := resolveTestStartupPrompt(t, cfg, session.Info{
		Template:  "worker-stale-template",
		AgentName: "worker",
		State:     session.StateStartPending,
	}, nil)
	if strings.TrimSpace(resolved.PromptSuffix) == "" {
		t.Fatal("PromptSuffix empty: a session recoverable through agent_name was treated as provider-only and would start unprimed")
	}
}

// A session with no configured agent at all is legitimately promptless and must
// NOT be an error — legacy and provider-only sessions exist.
func TestResolveStartupPromptSkipsUnconfiguredAgentWithoutError(t *testing.T) {
	cfg := startupPromptTestCity()
	resolved, err := resolveTestStartupPromptErr(t, cfg, session.Info{
		Template: "nothing-like-this",
		State:    session.StateStartPending,
	}, nil)
	if err != nil {
		t.Fatalf("unconfigured agent returned an error: %v", err)
	}
	if resolved.PromptSuffix != "" || resolved.Nudge != "" {
		t.Fatalf("resolved = %#v, want empty for a session with no configured agent", resolved)
	}
}

// Malformed persisted overrides must not silently cost the user their initial
// message: the start fails instead.
func TestResolveStartupPromptFailsOnMalformedTemplateOverrides(t *testing.T) {
	cfg := startupPromptTestCity()
	_, err := resolveTestStartupPromptErr(t, cfg, session.Info{
		Template:  "worker",
		AgentName: "worker",
		State:     session.StateStartPending,
	}, map[string]string{"template_overrides": "{not json"})
	if err == nil {
		t.Fatal("malformed template_overrides resolved without error: the user's initial_message is dropped silently")
	}
}

// initial_message parity with the reconciler, at the resolver seam.
func TestResolveStartupPromptInitialMessage(t *testing.T) {
	const msg = "start with the migration"
	overrides := map[string]string{"template_overrides": `{"initial_message":"` + msg + `"}`}

	t.Run("arg mode appends to the argv payload on first start", func(t *testing.T) {
		resolved := resolveTestStartupPrompt(t, startupPromptTestCity(), session.Info{
			Template:  "worker",
			AgentName: "worker",
			State:     session.StateStartPending,
		}, overrides)
		if !strings.Contains(resolved.PromptSuffix, msg) {
			t.Fatalf("PromptSuffix = %q, want it to carry the initial message", resolved.PromptSuffix)
		}
	})

	t.Run("none mode appends to the nudge on first start", func(t *testing.T) {
		cfg := startupPromptTestCity()
		cfg.Providers["stub"] = config.ProviderSpec{PromptMode: "none"}
		resolved := resolveTestStartupPrompt(t, cfg, session.Info{
			Template:  "worker",
			AgentName: "worker",
			State:     session.StateStartPending,
		}, overrides)
		if !strings.Contains(resolved.Nudge, msg) {
			t.Fatalf("Nudge = %q, want it to carry the initial message", resolved.Nudge)
		}
	})

	t.Run("resume does not replay the initial message", func(t *testing.T) {
		md := map[string]string{"started_config_hash": "deadbeef"}
		for k, v := range overrides {
			md[k] = v
		}
		resolved := resolveTestStartupPrompt(t, startupPromptTestCity(), session.Info{
			Template:   "worker",
			AgentName:  "worker",
			State:      session.StateActive,
			SessionKey: "provider-session-abc",
		}, md)
		if strings.Contains(resolved.PromptSuffix, msg) || strings.Contains(resolved.Nudge, msg) {
			t.Fatalf("initial message replayed on resume: suffix=%q nudge=%q", resolved.PromptSuffix, resolved.Nudge)
		}
	})
}

// Delivering a prompt stages that agent's files and settings, so associating a
// session with the WRONG agent is worse than leaving it promptless. These are
// the cases where the general lookup precedence would guess.
func TestStartupPromptIdentityRefusesToGuess(t *testing.T) {
	twoAgents := func() *config.City {
		return &config.City{
			Workspace: config.Workspace{Name: "test-city"},
			Agents: []config.Agent{
				{Name: "alpha", Provider: "stub"},
				{Name: "beta", Provider: "stub"},
			},
			Providers: map[string]config.ProviderSpec{"stub": {}},
		}
	}

	t.Run("conflicting template and agent_name is an error", func(t *testing.T) {
		_, err := resolveTestStartupPromptErr(t, twoAgents(), session.Info{
			Template:  "alpha",
			AgentName: "beta",
			State:     session.StateStartPending,
		}, nil)
		if err == nil {
			t.Fatal("conflicting identities resolved without error: one agent's prompt would be delivered to another's session")
		}
	})

	t.Run("alias alone does not establish ownership", func(t *testing.T) {
		resolved, err := resolveTestStartupPromptErr(t, twoAgents(), session.Info{
			Template: "not-an-agent",
			Alias:    "alpha",
			State:    session.StateStartPending,
		}, nil)
		if err != nil {
			t.Fatalf("alias-only session errored: %v", err)
		}
		if resolved.PromptSuffix != "" || resolved.Nudge != "" {
			t.Fatalf("resolved = %#v, want promptless: an arbitrary alias must not promote a provider-only session into a configured agent", resolved)
		}
	})

	t.Run("agreeing identities resolve normally", func(t *testing.T) {
		resolved := resolveTestStartupPrompt(t, twoAgents(), session.Info{
			Template:  "alpha",
			AgentName: "alpha",
			State:     session.StateStartPending,
		}, nil)
		if strings.TrimSpace(resolved.PromptSuffix) == "" {
			t.Fatal("PromptSuffix empty for an unambiguous identity")
		}
	})

	t.Run("provider-backed session with a colliding template stays promptless", func(t *testing.T) {
		resolved, err := resolveTestStartupPromptErr(t, twoAgents(), session.Info{
			Template: "alpha",
			State:    session.StateStartPending,
		}, map[string]string{"real_world_app_session_kind": "provider"})
		if err != nil {
			t.Fatalf("provider-backed session errored: %v", err)
		}
		if resolved.PromptSuffix != "" || resolved.Nudge != "" {
			t.Fatalf("resolved = %#v, want promptless: a provider-backed session must not borrow agent %q's prompt and staged files just because its template name collides", resolved, "alpha")
		}
	})

	t.Run("manual-origin session with a colliding template stays promptless", func(t *testing.T) {
		resolved, err := resolveTestStartupPromptErr(t, twoAgents(), session.Info{
			Template: "alpha",
			State:    session.StateStartPending,
		}, map[string]string{"session_origin": "manual"})
		if err != nil {
			t.Fatalf("manual session errored: %v", err)
		}
		if resolved.PromptSuffix != "" {
			t.Fatalf("resolved = %#v, want promptless for a manually created session", resolved)
		}
	})

	t.Run("configured named session keeps its backing template", func(t *testing.T) {
		// A named session "beta" backed by template "alpha", with agent "beta"
		// also configured, is legitimate: the identity difference is structural,
		// not an ambiguity. It must resolve, not error.
		resolved := resolveTestStartupPrompt(t, twoAgents(), session.Info{
			Template:               "alpha",
			AgentName:              "beta",
			ConfiguredNamedSession: true,
			State:                  session.StateStartPending,
		}, nil)
		if strings.TrimSpace(resolved.PromptSuffix) == "" {
			t.Fatal("PromptSuffix empty: a configured named session backed by another template was refused")
		}
	})

	// A pool instance may legitimately carry an identity that collides with a
	// different configured agent — but only when its slot ACTUALLY derives that
	// identity. Namepool agent "alpha" expands slot 2 to "beta", and "beta" is
	// also a configured agent in its own right: the real collision.
	poolCity := func() *config.City {
		c := twoAgents()
		c.Agents[0].NamepoolNames = []string{"gamma", "beta"}
		return c
	}

	t.Run("pool instance whose slot derives agent_name keeps its backing template", func(t *testing.T) {
		resolved := resolveTestStartupPrompt(t, poolCity(), session.Info{
			Template:  "alpha",
			AgentName: "beta",
			PoolSlot:  "2",
			State:     session.StateStartPending,
		}, map[string]string{"pool_slot": "2"})
		if strings.TrimSpace(resolved.PromptSuffix) == "" {
			t.Fatal("PromptSuffix empty: a pool instance whose slot genuinely derives its agent_name was refused")
		}
	})

	t.Run("pool slot that does not derive agent_name is still an error", func(t *testing.T) {
		// Slot 1 of "alpha" derives "gamma", not "beta". Stale pool metadata is
		// an expected repository state, so a bare slot number must not be
		// accepted as proof that alpha owns this session.
		_, err := resolveTestStartupPromptErr(t, poolCity(), session.Info{
			Template:  "alpha",
			AgentName: "beta",
			PoolSlot:  "1",
			State:     session.StateStartPending,
		}, map[string]string{"pool_slot": "1"})
		if err == nil {
			t.Fatal("mismatched pool slot resolved without error: stale pool metadata bypassed the refuse-to-guess protection")
		}
	})

	t.Run("out-of-bounds pool slot cannot manufacture ownership", func(t *testing.T) {
		// Namepool ["gamma","beta"] has two slots. Slot 3 is out of bounds, but
		// poolInstanceIdentity still derives the fallback "alpha-3". If an agent
		// named "alpha-3" happens to be configured, an unbounded check would
		// exempt the collision on a slot the repository considers invalid.
		cfg := poolCity()
		cfg.Agents = append(cfg.Agents, config.Agent{Name: "alpha-3", Provider: "stub"})
		_, err := resolveTestStartupPromptErr(t, cfg, session.Info{
			Template:  "alpha",
			AgentName: "alpha-3",
			PoolSlot:  "3",
			State:     session.StateStartPending,
		}, map[string]string{"pool_slot": "3"})
		if err == nil {
			t.Fatal("out-of-bounds slot 3 was accepted as proof of ownership: an invalid slot must not exempt a collision")
		}
	})

	t.Run("non-namepool pool slot that cannot derive agent_name is an error", func(t *testing.T) {
		// Plain "alpha" slot 2 derives "alpha-2", never "beta".
		_, err := resolveTestStartupPromptErr(t, twoAgents(), session.Info{
			Template:  "alpha",
			AgentName: "beta",
			PoolSlot:  "2",
			State:     session.StateStartPending,
		}, map[string]string{"pool_slot": "2"})
		if err == nil {
			t.Fatal("slot 2 of \"alpha\" derives \"alpha-2\", not \"beta\"; this must not be exempted")
		}
	})
}
