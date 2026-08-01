package api

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type createTransportCapableProvider struct {
	*runtime.Fake
}

func (p *createTransportCapableProvider) SupportsTransport(transport string) bool {
	return transport == "acp"
}

func TestProviderSessionTransportUsesExplicitACPConfigOnCustomProvider(t *testing.T) {
	transport, err := providerSessionTransport(&config.ResolvedProvider{
		Name:        "custom-acp",
		SupportsACP: true,
		ACPCommand:  "/bin/echo",
	}, &createTransportCapableProvider{Fake: runtime.NewFake()})
	if err != nil {
		t.Fatalf("providerSessionTransport: %v", err)
	}
	if transport != "acp" {
		t.Fatalf("providerSessionTransport() = %q, want %q", transport, "acp")
	}
}

func TestProviderSessionTransportSupportsACPAloneStaysDefault(t *testing.T) {
	transport, err := providerSessionTransport(&config.ResolvedProvider{
		Name:        "custom-acp",
		SupportsACP: true,
	}, &createTransportCapableProvider{Fake: runtime.NewFake()})
	if err != nil {
		t.Fatalf("providerSessionTransport: %v", err)
	}
	if transport != "" {
		t.Fatalf("providerSessionTransport() = %q, want empty transport", transport)
	}
}

func TestValidateSessionTransportAcceptsTmuxTransport(t *testing.T) {
	transport, err := validateSessionTransport(&config.ResolvedProvider{
		Name: "custom",
	}, config.SessionTransportTmux, runtime.NewFake())
	if err != nil {
		t.Fatalf("validateSessionTransport: %v", err)
	}
	if transport != config.SessionTransportTmux {
		t.Fatalf("validateSessionTransport() = %q, want %q", transport, config.SessionTransportTmux)
	}
}

func TestValidateSessionTransportRejectsTmuxWhenSessionProviderIsACPOnly(t *testing.T) {
	_, err := validateSessionTransport(&config.ResolvedProvider{
		Name: "custom",
	}, config.SessionTransportTmux, &createTransportCapableProvider{Fake: runtime.NewFake()})
	if err == nil || !strings.Contains(err.Error(), "requires tmux transport") {
		t.Fatalf("validateSessionTransport() error = %v, want tmux routing error", err)
	}
}

func TestValidateSessionTransportRejectsUnknownTransport(t *testing.T) {
	_, err := validateSessionTransport(&config.ResolvedProvider{
		Name: "custom",
	}, "stdio", runtime.NewFake())
	if err == nil {
		t.Fatal("validateSessionTransport() error = nil, want unknown transport error")
	}
}

func TestResolveSessionTemplateForCreateUsesProviderACPDefault(t *testing.T) {
	fs := newSessionFakeState(t)
	supportsACP := true
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom-acp",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom-acp": {
				Command:     "/bin/echo",
				PathCheck:   "true",
				SupportsACP: &supportsACP,
				ACPCommand:  "/bin/echo",
				ACPArgs:     []string{"acp"},
			},
		},
	}

	srv := New(fs)
	_, _, transport, _, err := srv.resolveSessionTemplateForCreate("myrig/worker")
	if err != nil {
		t.Fatalf("resolveSessionTemplateForCreate: %v", err)
	}
	if transport != "acp" {
		t.Fatalf("transport = %q, want %q", transport, "acp")
	}
}

func TestResolveSessionTemplateUsesProviderACPDefaultForLegacyRuntimeTransport(t *testing.T) {
	fs := newSessionFakeState(t)
	supportsACP := true
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom-acp",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom-acp": {
				Command:     "/bin/echo",
				PathCheck:   "true",
				SupportsACP: &supportsACP,
				ACPCommand:  "/bin/echo",
				ACPArgs:     []string{"acp"},
			},
		},
	}

	srv := New(fs)
	_, _, transport, _, err := srv.resolveSessionTemplate("myrig/worker")
	if err != nil {
		t.Fatalf("resolveSessionTemplate: %v", err)
	}
	if transport != "acp" {
		t.Fatalf("transport = %q, want %q", transport, "acp")
	}
}

func TestConfiguredSessionTransportUsesProviderACPDefaultForAgentTemplates(t *testing.T) {
	supportsACP := true
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom-acp",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom-acp": {
				Command:     "/bin/echo",
				PathCheck:   "true",
				SupportsACP: &supportsACP,
				ACPCommand:  "/bin/echo",
				ACPArgs:     []string{"acp"},
			},
		},
	}

	transport := configuredSessionTransport(cfg, "myrig/worker", "")
	if transport != "acp" {
		t.Fatalf("configuredSessionTransport() = %q, want %q", transport, "acp")
	}
}

func TestConfiguredSessionTransportUsesT3RuntimeTransport(t *testing.T) {
	cfg := &config.City{
		Session: config.SessionConfig{Provider: "t3bridge"},
		Workspace: config.Workspace{
			Name:     "test-city",
			Provider: "custom",
		},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom": {
				Command:   "/bin/echo",
				PathCheck: "true",
			},
		},
	}

	if got := configuredSessionTransport(cfg, "myrig/worker", ""); got != "t3" {
		t.Fatalf("configuredSessionTransport() = %q, want %q", got, "t3")
	}
}

// TestConfiguredSessionTransportCanonicalizesPerAgentT3BridgeOverride pins the
// create-resolver fix: a per-agent `session = "t3bridge"` override must persist
// as the "t3" carrier, not the runtime-selection name "t3bridge" (which no
// classifier matches). The city-level session.provider path already resolved
// correctly; only the per-agent override leaked.
func TestConfiguredSessionTransportCanonicalizesPerAgentT3BridgeOverride(t *testing.T) {
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom",
			Session:  "t3bridge",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom": {
				Command:   "/bin/echo",
				PathCheck: "true",
			},
		},
	}

	if got := configuredSessionTransport(cfg, "myrig/worker", ""); got != "t3" {
		t.Fatalf("configuredSessionTransport(per-agent session=t3bridge) = %q, want t3", got)
	}
}

// TestConfiguredSessionTransportReturnsUnknownOnResolutionFailure pins finding 3:
// a provider-resolution failure must yield the unknown sentinel ("") so a later
// successful resolution can still correct the bead, rather than a confidently
// guessed carrier (tmux) that persists as authoritative.
func TestConfiguredSessionTransportReturnsUnknownOnResolutionFailure(t *testing.T) {
	// Agent path: the template matches an agent whose provider is not in the
	// catalog, so config.ResolveProvider errors.
	agentPath := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "ghost-provider",
		}},
		Providers: map[string]config.ProviderSpec{},
	}
	if got := configuredSessionTransport(agentPath, "myrig/worker", ""); got != "" {
		t.Fatalf("configuredSessionTransport(agent resolution failure) = %q, want \"\" (unknown sentinel, not a guessed carrier)", got)
	}

	// Provider path: no template match, and the provider name does not resolve.
	providerPath := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Providers: map[string]config.ProviderSpec{},
	}
	if got := configuredSessionTransport(providerPath, "", "ghost-provider"); got != "" {
		t.Fatalf("configuredSessionTransport(provider resolution failure) = %q, want \"\" (unknown sentinel)", got)
	}
}

func TestBuildSessionResumeDoesNotInferProviderACPDefaultForStoppedLegacyTemplateSession(t *testing.T) {
	fs := newSessionFakeState(t)
	supportsACP := true
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:     "worker",
			Dir:      "myrig",
			Provider: "custom-acp",
		}},
		Providers: map[string]config.ProviderSpec{
			"custom-acp": {
				Command:     "/bin/echo",
				PathCheck:   "true",
				SupportsACP: &supportsACP,
				ACPCommand:  "/bin/echo",
				ACPArgs:     []string{"acp"},
			},
		},
	}

	srv := New(fs)
	cmd, _, err := srv.buildSessionResume(session.Info{
		ID:       "gc-1",
		Template: "myrig/worker",
		Command:  "/bin/echo",
		WorkDir:  "/tmp/workdir",
	})
	if err != nil {
		t.Fatalf("buildSessionResume: %v", err)
	}
	if cmd != "/bin/echo" {
		t.Fatalf("resume command = %q, want %q", cmd, "/bin/echo")
	}
}

func TestResolvedSessionRuntimeCommandReplaysTemplateOverrides(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)
	resolved := &config.ResolvedProvider{
		Name:    "custom",
		Command: "/bin/echo",
		OptionsSchema: []config.ProviderOption{{
			Key:  "effort",
			Type: "select",
			Choices: []config.OptionChoice{{
				Value:    "high",
				FlagArgs: []string{"--effort", "high"},
			}},
		}},
	}

	command, err := srv.resolvedSessionRuntimeCommand(
		resolved,
		"",
		"/bin/echo",
		map[string]string{"template_overrides": `{"effort":"high","initial_message":"hello"}`},
	)
	if err != nil {
		t.Fatalf("resolvedSessionRuntimeCommand: %v", err)
	}
	if command != "/bin/echo --effort high" {
		t.Fatalf("command = %q, want %q", command, "/bin/echo --effort high")
	}
}

func TestShouldPreserveStoredRuntimeCommandForTransportRejectsExecutableOnlyMatch(t *testing.T) {
	if shouldPreserveStoredRuntimeCommandForTransport(
		"claude",
		"claude --settings /tmp/settings.json",
		"",
		nil,
	) {
		t.Fatal("shouldPreserveStoredRuntimeCommandForTransport() = true, want false")
	}
}
