package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/formulatest"
)

// v2FormulaFixture is the exact body-scope example from
// docs/reference/specs/formula-spec-v2.md section 3.5: a formula declaring
// [requires] formula_compiler = ">=2.0.0", a scope body step, and a
// needs-gated member step ("body" needs "implement").
const v2FormulaFixture = `
formula = "%s"

[requires]
formula_compiler = ">=2.0.0"

[[steps]]
id = "body"
title = "Worktree scope"
needs = ["implement"]
metadata = { "gc.kind" = "scope", "gc.scope_name" = "worktree", "gc.scope_role" = "body" }

[[steps]]
id = "implement"
title = "Implement the change"
metadata = { "gc.scope_ref" = "body", "gc.scope_role" = "member", "gc.on_fail" = "abort_scope" }

[[steps]]
id = "cleanup"
title = "Tear down the worktree"
needs = ["body"]
metadata = { "gc.kind" = "cleanup", "gc.scope_ref" = "body", "gc.scope_role" = "teardown" }
`

// TestFormulaShowResolvesAndCompilesPackDefinedV2Formula is the regression
// test for gc-dq88p: `gc formula show` must resolve AND live-compile a v2
// formula (scope + needs graph, [requires] formula_compiler = ">=2.0.0")
// defined in an imported pack, at both city and rig scope, honoring the same
// visibility rules as TestPackV2ImportedFormulasAndOrdersVisibleToCityAndRig.
//
// Premise check (gascity-pl, 2026-09-19, against origin/main): the layer
// plumbing (config.FormulaLayers) and ResolveFormulas symlinking are already
// covered. What was uncovered is the `gc formula show` path itself —
// resolveFormulaScope + formula.CompileWithoutRuntimeVarValidation — over a
// pack-shipped v2 formula. This test exercises exactly those two functions,
// the same ones newFormulaShowCmd's RunE calls, rather than shelling out to
// the gc binary.
func TestFormulaShowResolvesAndCompilesPackDefinedV2Formula(t *testing.T) {
	formulatest.EnableV2ForTest(t)

	cityDir := t.TempDir()
	rigDir := filepath.Join(cityDir, "frontend")
	opsPackDir := filepath.Join(cityDir, "packs", "ops")
	sidecarPackDir := filepath.Join(cityDir, "packs", "sidecar")

	for _, dir := range []string{
		filepath.Join(cityDir, ".gc"),
		rigDir,
		filepath.Join(opsPackDir, "formulas"),
		filepath.Join(sidecarPackDir, "formulas"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(t, filepath.Join(cityDir, "pack.toml"), `
[pack]
name = "testcity"
schema = 2

[imports.ops]
source = "./packs/ops"
`)
	writeFile(t, filepath.Join(cityDir, "city.toml"), `
[workspace]

[[rigs]]
name = "frontend"

[rigs.imports.sidecar]
source = "./packs/sidecar"
`)
	writeFile(t, filepath.Join(cityDir, ".gc", "site.toml"), `
workspace_name = "testcity"

[[rig]]
name = "frontend"
path = "./frontend"
`)
	writeFile(t, filepath.Join(opsPackDir, "pack.toml"), `
[pack]
name = "ops"
schema = 2
`)
	writeFile(t, filepath.Join(opsPackDir, "formulas", "city-scoped.toml"),
		formatFormula(v2FormulaFixture, "city-scoped"))
	writeFile(t, filepath.Join(sidecarPackDir, "pack.toml"), `
[pack]
name = "sidecar"
schema = 2
`)
	writeFile(t, filepath.Join(sidecarPackDir, "formulas", "rig-scoped.toml"),
		formatFormula(v2FormulaFixture, "rig-scoped"))

	cfg, err := loadCityConfig(cityDir)
	if err != nil {
		t.Fatalf("loadCityConfig: %v", err)
	}

	opsFormulaDir := filepath.Join(opsPackDir, "formulas")
	sidecarFormulaDir := filepath.Join(sidecarPackDir, "formulas")
	assertContainsString(t, cfg.FormulaLayers.City, opsFormulaDir)
	assertNotContainsString(t, cfg.FormulaLayers.City, sidecarFormulaDir)

	prevRigFlag := rigFlag
	t.Cleanup(func() { rigFlag = prevRigFlag })

	assertV2Compile := func(t *testing.T, recipe *formula.Recipe, wantName string) {
		t.Helper()
		if recipe.Name != wantName {
			t.Fatalf("recipe.Name = %q, want %q", recipe.Name, wantName)
		}
		bodyID := wantName + ".body"
		implementID := wantName + ".implement"
		cleanupID := wantName + ".cleanup"

		var bodyStep, implementStep *formula.RecipeStep
		for i := range recipe.Steps {
			switch recipe.Steps[i].ID {
			case bodyID:
				bodyStep = &recipe.Steps[i]
			case implementID:
				implementStep = &recipe.Steps[i]
			}
		}
		if bodyStep == nil {
			t.Fatalf("compiled recipe missing scope body step %q; steps: %#v", bodyID, recipe.Steps)
		}
		if bodyStep.Metadata["gc.kind"] != "scope" || bodyStep.Metadata["gc.scope_role"] != "body" {
			t.Fatalf("body step %q metadata = %#v, want gc.kind=scope gc.scope_role=body", bodyID, bodyStep.Metadata)
		}
		if implementStep == nil {
			t.Fatalf("compiled recipe missing scope member step %q; steps: %#v", implementID, recipe.Steps)
		}
		if implementStep.Metadata["gc.scope_ref"] != "body" {
			t.Fatalf("implement step %q metadata = %#v, want gc.scope_ref=body", implementID, implementStep.Metadata)
		}

		foundNeedsEdge := false
		for _, dep := range recipe.Deps {
			if dep.StepID == bodyID && dep.DependsOnID == implementID {
				foundNeedsEdge = true
				break
			}
		}
		if !foundNeedsEdge {
			t.Fatalf("compiled recipe missing needs edge %s -> %s; deps: %#v", bodyID, implementID, recipe.Deps)
		}

		foundCleanup := false
		for _, s := range recipe.Steps {
			if s.ID == cleanupID {
				foundCleanup = true
				break
			}
		}
		if !foundCleanup {
			t.Fatalf("compiled recipe missing cleanup step %q; steps: %#v", cleanupID, recipe.Steps)
		}
	}

	// City scope: the city-pack formula resolves and compiles; the
	// rig-imported pack's formula must not be visible here.
	t.Run("city scope resolves and compiles the city-pack formula", func(t *testing.T) {
		rigFlag = ""
		cityFlag = cityDir
		t.Cleanup(func() { cityFlag = "" })

		scope, err := resolveFormulaScope(cfg, cityDir, io.Discard)
		if err != nil {
			t.Fatalf("resolveFormulaScope: %v", err)
		}
		assertContainsString(t, scope.searchPaths, opsFormulaDir)
		assertNotContainsString(t, scope.searchPaths, sidecarFormulaDir)

		recipe, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "city-scoped", scope.searchPaths, nil)
		if err != nil {
			t.Fatalf("CompileWithoutRuntimeVarValidation(city-scoped): %v", err)
		}
		assertV2Compile(t, recipe, "city-scoped")

		if _, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "rig-scoped", scope.searchPaths, nil); err == nil {
			t.Fatal("CompileWithoutRuntimeVarValidation(rig-scoped) at city scope: want error, got nil (rig-imported pack formula must not be visible at city scope)")
		}
	})

	// Rig scope: both the city-pack formula and the rig-imported pack's own
	// formula resolve and compile.
	t.Run("rig scope resolves and compiles both layers' formulas", func(t *testing.T) {
		rigFlag = "frontend"
		t.Cleanup(func() { rigFlag = "" })

		scope, err := resolveFormulaScope(cfg, cityDir, io.Discard)
		if err != nil {
			t.Fatalf("resolveFormulaScope: %v", err)
		}
		assertContainsString(t, scope.searchPaths, opsFormulaDir)
		assertContainsString(t, scope.searchPaths, sidecarFormulaDir)

		cityRecipe, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "city-scoped", scope.searchPaths, nil)
		if err != nil {
			t.Fatalf("CompileWithoutRuntimeVarValidation(city-scoped) at rig scope: %v", err)
		}
		assertV2Compile(t, cityRecipe, "city-scoped")

		rigRecipe, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "rig-scoped", scope.searchPaths, nil)
		if err != nil {
			t.Fatalf("CompileWithoutRuntimeVarValidation(rig-scoped): %v", err)
		}
		assertV2Compile(t, rigRecipe, "rig-scoped")
	})

	// A formula name that exists in no layer still fails with the existing
	// not-found error, at either scope.
	t.Run("unknown formula name fails at both scopes", func(t *testing.T) {
		rigFlag = ""
		scope, err := resolveFormulaScope(cfg, cityDir, io.Discard)
		if err != nil {
			t.Fatalf("resolveFormulaScope: %v", err)
		}
		if _, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "does-not-exist", scope.searchPaths, nil); err == nil {
			t.Fatal("CompileWithoutRuntimeVarValidation(does-not-exist) at city scope: want error, got nil")
		}

		rigFlag = "frontend"
		t.Cleanup(func() { rigFlag = "" })
		scope, err = resolveFormulaScope(cfg, cityDir, io.Discard)
		if err != nil {
			t.Fatalf("resolveFormulaScope: %v", err)
		}
		if _, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "does-not-exist", scope.searchPaths, nil); err == nil {
			t.Fatal("CompileWithoutRuntimeVarValidation(does-not-exist) at rig scope: want error, got nil")
		}
	})
}

func formatFormula(template, name string) string {
	return fmt.Sprintf(template, name)
}
