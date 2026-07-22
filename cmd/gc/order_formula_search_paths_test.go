package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/orders"
)

// writeOrderTestFormula writes a minimal single-step formula named name into
// dir and returns dir, so callers can build layer stacks inline.
func writeOrderTestFormula(t *testing.T, dir, name, title string) string {
	t.Helper()
	body := `
formula = "` + name + `"
version = 1

[[steps]]
id = "work"
title = "` + title + `"
`
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestOrderFormulaSearchPathsSpansTheScopeStack is the regression for #4378:
// order dispatch resolved an order's formula against the single layer the
// order file was discovered in, so a city order naming a formula shipped by a
// city-imported pack failed with `formula %q not found in search paths` even
// though `gc formula list`/`gc formula show` resolved it on the same build.
//
// The order's own layer must stay highest priority (a co-located formula still
// shadows a pack-shipped one of the same name), but the rest of the scope's
// layer stack has to be searched too.
func TestOrderFormulaSearchPathsSpansTheScopeStack(t *testing.T) {
	packLayer := writeOrderTestFormula(t, t.TempDir(), "github-pr-review", "Review")
	localLayer := t.TempDir()

	cfg := &config.City{
		FormulaLayers: config.FormulaLayers{
			City: []string{packLayer, localLayer},
		},
	}
	// A user-authored city order: discovered in <city>/orders, so its
	// FormulaLayer is the city-local formula dir — not the pack's.
	a := orders.Order{Name: "review-pr", Trigger: "manual", Formula: "github-pr-review", FormulaLayer: localLayer}

	// Negative control: the pre-fix single-layer resolution cannot see the
	// pack layer, which is exactly the reported failure.
	if _, err := prepareOrderWispRecipe(context.Background(), beads.NewMemStore(), a, []string{a.FormulaLayer}, nil); err == nil {
		t.Fatal("single-layer resolution unexpectedly found the formula; the negative control no longer reproduces #4378")
	}

	paths := orderFormulaSearchPaths(cfg, a)
	if len(paths) == 0 || paths[len(paths)-1] != localLayer {
		t.Fatalf("orderFormulaSearchPaths = %v, want the order's own layer %q last (highest priority)", paths, localLayer)
	}
	if _, err := prepareOrderWispRecipe(context.Background(), beads.NewMemStore(), a, paths, nil); err != nil {
		t.Fatalf("prepareOrderWispRecipe over the city layer stack: %v", err)
	}
}

// TestOrderFormulaSearchPathsUsesRigLayersForRigOrders proves a rig-scoped
// order resolves against its rig's layer stack, not the city's.
func TestOrderFormulaSearchPathsUsesRigLayersForRigOrders(t *testing.T) {
	cityLayer := t.TempDir()
	rigPackLayer := writeOrderTestFormula(t, t.TempDir(), "rig-only", "Rig work")
	rigLocalLayer := t.TempDir()

	cfg := &config.City{
		FormulaLayers: config.FormulaLayers{
			City: []string{cityLayer},
			Rigs: map[string][]string{
				"demo": {cityLayer, rigPackLayer, rigLocalLayer},
			},
		},
	}
	a := orders.Order{Name: "rig-order", Trigger: "manual", Formula: "rig-only", Rig: "demo", FormulaLayer: rigLocalLayer}

	paths := orderFormulaSearchPaths(cfg, a)
	if _, err := prepareOrderWispRecipe(context.Background(), beads.NewMemStore(), a, paths, nil); err != nil {
		t.Fatalf("prepareOrderWispRecipe over the rig layer stack: %v", err)
	}

	// A city order of the same name must not reach the rig-only formula.
	cityOrder := orders.Order{Name: "rig-order", Trigger: "manual", Formula: "rig-only", FormulaLayer: cityLayer}
	if _, err := prepareOrderWispRecipe(context.Background(), beads.NewMemStore(), cityOrder, orderFormulaSearchPaths(cfg, cityOrder), nil); err == nil {
		t.Fatal("city-scoped order resolved a rig-only formula; scope isolation is broken")
	}
}

// TestOrderFormulaSearchPathsKeepsOrderLayerHighestPriority locks the
// shadowing rule: when the same formula name exists in a lower layer and in
// the order's own layer, the order's own layer wins.
func TestOrderFormulaSearchPathsKeepsOrderLayerHighestPriority(t *testing.T) {
	packLayer := writeOrderTestFormula(t, t.TempDir(), "shared", "From pack")
	localLayer := writeOrderTestFormula(t, t.TempDir(), "shared", "From city local")

	cfg := &config.City{
		FormulaLayers: config.FormulaLayers{City: []string{packLayer, localLayer}},
	}
	a := orders.Order{Name: "shadowed", Trigger: "manual", Formula: "shared", FormulaLayer: localLayer}

	recipe, err := prepareOrderWispRecipe(context.Background(), beads.NewMemStore(), a, orderFormulaSearchPaths(cfg, a), nil)
	if err != nil {
		t.Fatalf("prepareOrderWispRecipe: %v", err)
	}
	var titles []string
	for _, s := range recipe.Steps {
		titles = append(titles, s.Title)
	}
	if !strings.Contains(strings.Join(titles, "|"), "From city local") {
		t.Fatalf("recipe step titles = %v, want the city-local formula to shadow the pack one", titles)
	}
}

// TestOrderFormulaSearchPathsDegradesToTheOrderLayer covers the callers that
// have no city config (nil cfg) and orders discovered outside any configured
// layer: resolution must still fall back to the order's own layer rather than
// returning an empty stack, which would silently resolve against the process
// default search paths.
func TestOrderFormulaSearchPathsDegradesToTheOrderLayer(t *testing.T) {
	layer := writeOrderTestFormula(t, t.TempDir(), "standalone", "Work")
	a := orders.Order{Name: "standalone-order", Trigger: "manual", Formula: "standalone", FormulaLayer: layer}

	if got := orderFormulaSearchPaths(nil, a); len(got) != 1 || got[0] != layer {
		t.Fatalf("orderFormulaSearchPaths(nil cfg) = %v, want [%q]", got, layer)
	}

	cfg := &config.City{FormulaLayers: config.FormulaLayers{City: []string{t.TempDir()}}}
	got := orderFormulaSearchPaths(cfg, a)
	if len(got) != 2 || got[1] != layer {
		t.Fatalf("orderFormulaSearchPaths = %v, want the order layer %q appended last", got, layer)
	}

	// An order with no layer at all must not inject an empty search path.
	noLayer := orders.Order{Name: "no-layer", Trigger: "manual", Formula: "standalone"}
	for _, p := range orderFormulaSearchPaths(cfg, noLayer) {
		if strings.TrimSpace(p) == "" {
			t.Fatalf("orderFormulaSearchPaths emitted an empty search path: %v", orderFormulaSearchPaths(cfg, noLayer))
		}
	}
}
