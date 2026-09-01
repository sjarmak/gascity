package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// This file expresses gc-6rae5's "feeder" surface: filterReadyByRoute (Tier 3
// pool-demand/control-dispatcher routing) and its callers up through
// tryControlReadyFromCacheOrFallback must fence a descendant whose graph.v2
// workflow root is held, even though the descendant carries no hold label of
// its own -- the same fencing gc-6rae5 already added to `gc hook`
// (hook_root_hold.go), extended to the `gc ready`/feeder data source.

func TestFilterReadyByRouteExcludesHeldGraphRootDescendant(t *testing.T) {
	older := time.Unix(100, 0)
	newer := time.Unix(200, 0)
	ready := []beads.Bead{
		{ID: "ga-under-held-root", CreatedAt: older, Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey:  "core/control-dispatcher",
			beadmeta.RootBeadIDMetadataKey: "ga-held-root",
		}},
		{ID: "ga-under-unheld-root", CreatedAt: older, Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey:  "core/control-dispatcher",
			beadmeta.RootBeadIDMetadataKey: "ga-unheld-root",
		}},
		{ID: "ga-is-own-root", CreatedAt: newer, Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey:  "core/control-dispatcher",
			beadmeta.RootBeadIDMetadataKey: "ga-is-own-root",
		}},
	}
	rootHeld := func(rootID string) bool { return rootID == "ga-held-root" }

	got := filterReadyByRoute(ready, beadmeta.RunTargetMetadataKey, "core/control-dispatcher", rootHeld)
	want := []string{"ga-under-unheld-root", "ga-is-own-root"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("filterReadyByRoute ids = %v, want %v (a descendant of a held graph.v2 root must be excluded; a candidate that is its own root must not be)", beadIDs(got), want)
	}
}

func TestFilterReadyByRouteNilRootHeldIsNoop(t *testing.T) {
	ready := []beads.Bead{
		{ID: "ga-under-root", Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey:  "core/control-dispatcher",
			beadmeta.RootBeadIDMetadataKey: "ga-some-root",
		}},
	}
	got := filterReadyByRoute(ready, beadmeta.RunTargetMetadataKey, "core/control-dispatcher", nil)
	want := []string{"ga-under-root"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("filterReadyByRoute ids = %v, want %v (nil rootHeld must be a no-op, matching pre-gc-6rae5 behavior)", beadIDs(got), want)
	}
}

func TestEvaluateControlReadyExcludesHeldGraphRootDescendant(t *testing.T) {
	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: query not recognized: %q", query)
	}
	envList := []string{
		"GC_SESSION_NAME=gascity--control-dispatcher",
		"GC_ALIAS=gascity/control-dispatcher",
	}
	ready := []beads.Bead{
		{ID: "ga-routed", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "gascity/control-dispatcher"}},
		{ID: "ga-routed-held-root", Metadata: map[string]string{
			beadmeta.RunTargetMetadataKey:  "gascity/control-dispatcher",
			beadmeta.RootBeadIDMetadataKey: "ga-held-root",
		}},
	}
	rootHeld := func(rootID string) bool { return rootID == "ga-held-root" }

	got := evaluateControlReady(ready, parsed, envList, rootHeld)
	want := []string{"ga-routed"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("evaluateControlReady ids = %v, want %v (a routed descendant of a held graph.v2 root must be excluded)", beadIDs(got), want)
	}
}

// TestTryControlReadyFromCacheOrFallbackFencesHeldGraphRootFromCache is
// gc-6rae5 acceptance criterion 1 end-to-end on the feeder's cache path: a
// routed_to-matching bead whose gc.root_bead_id names a currently-held
// graph.v2 root must not reach the control dispatcher's queue when the
// answer is served from CachedReady(), even though the descendant itself
// carries no hold label.
func TestTryControlReadyFromCacheOrFallbackFencesHeldGraphRootFromCache(t *testing.T) {
	cityDir, store := setUpControlReadyFileStoreCity(t)
	noBDOnPathForTest(t)

	target := "gascity/control-dispatcher"
	heldRoot, err := store.Create(beads.Bead{Labels: []string{beadmeta.HoldMayorLabel}})
	if err != nil {
		t.Fatalf("create held root: %v", err)
	}
	underHeldRoot, err := store.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey:   target,
		beadmeta.RootBeadIDMetadataKey: heldRoot.ID,
	}})
	if err != nil {
		t.Fatalf("create descendant of held root: %v", err)
	}
	unheldRoot, err := store.Create(beads.Bead{})
	if err != nil {
		t.Fatalf("create unheld root: %v", err)
	}
	underUnheldRoot, err := store.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey:   target,
		beadmeta.RootBeadIDMetadataKey: unheldRoot.ID,
	}})
	if err != nil {
		t.Fatalf("create descendant of unheld root: %v", err)
	}

	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"}
	query := workflowServeControlReadyQuery(agentCfg)

	queue, handled, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback: %v", err)
	}
	if !handled {
		t.Fatalf("tryControlReadyFromCacheOrFallback: handled = false, want true for a control-ready query")
	}

	var gotIDs []string
	for _, b := range queue {
		gotIDs = append(gotIDs, b.ID)
	}
	wantIDs := []string{underUnheldRoot.ID}
	if !stringSlicesEqual(gotIDs, wantIDs) {
		t.Fatalf("queue ids = %#v, want %#v (descendant %s of held root %s must not be auto-routed to the pool)", gotIDs, wantIDs, underHeldRoot.ID, heldRoot.ID)
	}
}

// TestTryControlReadyFromCacheOrFallbackServesGraphRootDescendantOnceUnheld is
// gc-6rae5 acceptance criterion 2 on the feeder path: removing the root's
// hold label makes its descendant routable again.
func TestTryControlReadyFromCacheOrFallbackServesGraphRootDescendantOnceUnheld(t *testing.T) {
	cityDir, store := setUpControlReadyFileStoreCity(t)
	noBDOnPathForTest(t)

	target := "gascity/control-dispatcher"
	root, err := store.Create(beads.Bead{Labels: []string{beadmeta.HoldMayorLabel}})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	descendant, err := store.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey:   target,
		beadmeta.RootBeadIDMetadataKey: root.ID,
	}})
	if err != nil {
		t.Fatalf("create descendant: %v", err)
	}

	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"}
	query := workflowServeControlReadyQuery(agentCfg)

	queue, _, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback (held): %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("queue while root held = %v, want empty", queue)
	}

	if err := store.Update(root.ID, beads.UpdateOpts{RemoveLabels: []string{beadmeta.HoldMayorLabel}}); err != nil {
		t.Fatalf("clear root hold: %v", err)
	}
	// Cache TTL: force a fresh scan instead of the entry primed while held.
	controlReadyCacheRegistry.mu.Lock()
	delete(controlReadyCacheRegistry.byDir, cityDir)
	controlReadyCacheRegistry.mu.Unlock()

	queue, _, err = tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback (unheld): %v", err)
	}
	var gotIDs []string
	for _, b := range queue {
		gotIDs = append(gotIDs, b.ID)
	}
	wantIDs := []string{descendant.ID}
	if !stringSlicesEqual(gotIDs, wantIDs) {
		t.Fatalf("queue ids after clearing hold = %#v, want %#v", gotIDs, wantIDs)
	}
}

// TestTryControlReadyFromCacheOrFallbackFencesHeldGraphRootOnFallbackPath is
// gc-6rae5 acceptance criterion 1 end-to-end on the feeder's fallback path: a
// routed_to-matching bead whose gc.root_bead_id names a currently-held
// graph.v2 root must not reach the control dispatcher's queue when the
// answer is taken from the single batched `bd ready --json` fallback,
// matching the cache-path behavior above.
func TestTryControlReadyFromCacheOrFallbackFencesHeldGraphRootOnFallbackPath(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[workspace]\nname = \"test-city\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}

	tmp := t.TempDir()
	bdPath := filepath.Join(tmp, "bd")
	target := "gascity/control-dispatcher"
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  list)
    exit 7
    ;;
  show)
    case "$3" in
      root-held) printf '[{"id":"root-held","labels":["hold:mayor"]}]' ;;
      root-open) printf '[{"id":"root-open"}]' ;;
      *) exit 1 ;;
    esac
    ;;
  *)
    printf '[{"id":"ga-fallback-under-held-root","metadata":{"gc.routed_to":"%s","gc.root_bead_id":"root-held"}},{"id":"ga-fallback-under-open-root","metadata":{"gc.routed_to":"%s","gc.root_bead_id":"root-open"}}]'
    ;;
esac
`, target, target)
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_BEADS", "bd")

	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"}
	query := workflowServeControlReadyQuery(agentCfg)

	queue, handled, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback: %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want true")
	}
	var gotIDs []string
	for _, b := range queue {
		gotIDs = append(gotIDs, b.ID)
	}
	wantIDs := []string{"ga-fallback-under-open-root"}
	if !stringSlicesEqual(gotIDs, wantIDs) {
		t.Fatalf("queue ids = %#v, want %#v (descendant of held root root-held must not be auto-routed to the pool)", gotIDs, wantIDs)
	}
}
