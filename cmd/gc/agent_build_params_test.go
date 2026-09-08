package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestResolveTmuxAliasForAgent_NilAgentReturnsError(t *testing.T) {
	bp := &agentBuildParams{cityPath: "/city", cityName: "test-city"}

	alias, err := bp.resolveTmuxAliasForAgent(nil)
	if err == nil {
		t.Fatalf("resolveTmuxAliasForAgent(nil agent) = (%q, nil), want non-nil error", alias)
	}
	if alias != "" {
		t.Fatalf("resolveTmuxAliasForAgent(nil agent) alias = %q, want empty", alias)
	}
}

func TestResolveTmuxAliasForAgent_NilBuildParamsReturnsError(t *testing.T) {
	var bp *agentBuildParams
	cfgAgent := &config.Agent{Name: "graph-worker", TmuxAlias: "graph-worker-pool-7"}

	alias, err := bp.resolveTmuxAliasForAgent(cfgAgent)
	if err == nil {
		t.Fatalf("resolveTmuxAliasForAgent(nil build params) = (%q, nil), want non-nil error", alias)
	}
	if alias != "" {
		t.Fatalf("resolveTmuxAliasForAgent(nil build params) alias = %q, want empty", alias)
	}
	if !strings.Contains(err.Error(), cfgAgent.QualifiedName()) {
		t.Fatalf("resolveTmuxAliasForAgent(nil build params) error = %v, want to identify agent %q", err, cfgAgent.QualifiedName())
	}
}

func TestResolveTmuxAliasForAgent_UnconfiguredAliasReturnsEmptyNoError(t *testing.T) {
	bp := &agentBuildParams{cityPath: "/city", cityName: "test-city"}
	cfgAgent := &config.Agent{Name: "graph-worker"}

	alias, err := bp.resolveTmuxAliasForAgent(cfgAgent)
	if err != nil {
		t.Fatalf("resolveTmuxAliasForAgent(no tmux_alias configured) error = %v, want nil", err)
	}
	if alias != "" {
		t.Fatalf("resolveTmuxAliasForAgent(no tmux_alias configured) alias = %q, want empty", alias)
	}
}

func TestResolveTmuxAliasForAgent_ResolvesConfiguredAlias(t *testing.T) {
	bp := &agentBuildParams{cityPath: "/city", cityName: "test-city"}
	cfgAgent := &config.Agent{Name: "graph-worker", TmuxAlias: "gascity-worker-pool-7"}

	alias, err := bp.resolveTmuxAliasForAgent(cfgAgent)
	if err != nil {
		t.Fatalf("resolveTmuxAliasForAgent(configured alias) error = %v, want nil", err)
	}
	if alias != "gascity-worker-pool-7" {
		t.Fatalf("resolveTmuxAliasForAgent(configured alias) = %q, want %q", alias, "gascity-worker-pool-7")
	}
}
