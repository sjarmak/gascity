package api

import (
	"log/slog"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
)

func (s *Server) sessionManager(store beads.Store) *session.Manager {
	cfg := s.state.Config()
	if cfg == nil {
		return session.NewManagerWithOptions(store, s.state.SessionProvider(), session.WithCityPath(s.state.CityPath()))
	}
	return session.NewManagerWithOptions(
		store,
		s.state.SessionProvider(),
		session.WithCityPath(s.state.CityPath()),
		session.WithTransportPolicyResolver(func(template, provider string) (string, bool) {
			return configuredSessionTransportResolution(cfg, template, provider)
		}),
	)
}

func configuredSessionTransport(cfg *config.City, template, provider string) string {
	transport, _ := configuredSessionTransportResolution(cfg, template, provider)
	return transport
}

func configuredSessionTransportResolution(cfg *config.City, template, provider string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	if agentCfg, ok := resolveSessionTemplateAgent(cfg, template); ok {
		resolved, err := config.ResolveProvider(
			&agentCfg,
			&cfg.Workspace,
			cfg.Providers,
			func(name string) (string, error) { return name, nil },
		)
		if err != nil {
			// Provider resolution failed: return the unknown sentinel so a later
			// successful resolution can still correct the bead, rather than
			// durably stamping a guessed carrier. Log so the broken config is
			// attributable.
			slog.Warn("session transport unresolved: agent provider resolution failed",
				"template", template, "session", agentCfg.Session, "error", err)
			return "", false
		}
		return session.ResolveEffectiveTransport(
			effectiveSessionRuntimeName(agentCfg.Session, cfg.Session.Provider),
			config.ResolveSessionCreateTransport(agentCfg.Session, resolved),
		), false
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = strings.TrimSpace(template)
	}
	if provider == "" {
		return "", false
	}
	resolved, err := config.ResolveProvider(
		&config.Agent{Provider: provider},
		&cfg.Workspace,
		cfg.Providers,
		func(name string) (string, error) { return name, nil },
	)
	if err != nil {
		slog.Warn("session transport unresolved: provider resolution failed",
			"provider", provider, "error", err)
		return "", false
	}
	return session.ResolveEffectiveTransport(
		cfg.Session.Provider,
		resolved.ProviderSessionCreateTransport(),
	), false
}

func effectiveSessionRuntimeName(sessionOverride, cityRuntime string) string {
	if override := strings.TrimSpace(sessionOverride); override != "" {
		return override
	}
	return strings.TrimSpace(cityRuntime)
}
