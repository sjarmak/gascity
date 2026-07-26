package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/orders"
)

// TestParseOrderRunVarFlags locks the `gc order run --var key=value` parsing:
// well-formed pairs land in the map (value may itself contain '='), and
// malformed or empty-key entries are rejected.
func TestParseOrderRunVarFlags(t *testing.T) {
	var stderr bytes.Buffer

	vars, ok := parseOrderRunVarFlags([]string{"repo=octo/demo", "token=a=b=c", "empty="}, &stderr)
	if !ok {
		t.Fatalf("parseOrderRunVarFlags rejected valid flags: %s", stderr.String())
	}
	if vars["repo"] != "octo/demo" {
		t.Fatalf("vars[repo] = %q, want octo/demo", vars["repo"])
	}
	if vars["token"] != "a=b=c" {
		t.Fatalf("vars[token] = %q, want a=b=c (split on first '=')", vars["token"])
	}
	if v, exists := vars["empty"]; !exists || v != "" {
		t.Fatalf("vars[empty] = (%q, %v), want present empty string", v, exists)
	}

	if got, ok := parseOrderRunVarFlags(nil, &stderr); !ok || got != nil {
		t.Fatalf("parseOrderRunVarFlags(nil) = (%v, %v), want (nil, true)", got, ok)
	}

	if _, ok := parseOrderRunVarFlags([]string{"noequals"}, &stderr); ok {
		t.Fatal("parseOrderRunVarFlags accepted a flag without '='")
	}
	if _, ok := parseOrderRunVarFlags([]string{"=value"}, &stderr); ok {
		t.Fatal("parseOrderRunVarFlags accepted an empty key")
	}
}

// TestPrepareOrderWispRecipeThreadsVarsToFormula proves the args channel reaches
// a formula order: dispatch vars flow through prepareOrderWispRecipe into
// PrepareInvocation/ExpandVars and drive compile-time range expansion. The
// range var `n` is required, so the recipe only compiles when the var is
// supplied, and the resulting step count reflects the supplied value.
func TestPrepareOrderWispRecipeThreadsVarsToFormula(t *testing.T) {
	dir := t.TempDir()
	formulaBody := `
formula = "e1-var-range"
version = 1

[vars.n]
description = "Loop count"
required = true

[[steps]]
id = "loop"
title = "Loop"

[steps.loop]
range = "1..{n}"

[[steps.loop.body]]
id = "work"
title = "Work {i}"
`
	if err := os.WriteFile(filepath.Join(dir, "e1-var-range.toml"), []byte(strings.TrimSpace(formulaBody)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := beads.NewMemStore()
	a := orders.Order{Name: "range-order", Trigger: "manual", Formula: "e1-var-range", FormulaLayer: dir}

	// Without the var the required compile-time range var is unresolved and
	// compilation fails — proving the var is what makes the recipe compile.
	if _, _, err := prepareOrderWispRecipe(context.Background(), store, a, []string{dir}, nil); err == nil {
		t.Fatal("prepareOrderWispRecipe with nil vars: expected failure for missing required range var n")
	}

	recipe, effectiveVars, err := prepareOrderWispRecipe(context.Background(), store, a, []string{dir}, map[string]string{"n": "3"})
	if err != nil {
		t.Fatalf("prepareOrderWispRecipe with vars: %v", err)
	}
	if len(recipe.Steps) != 4 {
		t.Fatalf("len(recipe.Steps) = %d, want 4 (root + 3 loop iterations from n=3)", len(recipe.Steps))
	}
	// The resolved invocation vars must flow back to the caller so the wisp is
	// instantiated with them (see #4668) — not discarded after compile.
	if effectiveVars["n"] != "3" {
		t.Fatalf("effectiveVars[n] = %q, want 3 (resolved vars returned for instantiation)", effectiveVars["n"])
	}
}

// TestOrderRunSubstitutesCallerVarsInBeadText is the regression test for #4668:
// a formula order dispatched with a caller var must substitute that value into
// the instantiated bead text. The order-dispatch path discarded the resolved
// invocation vars after compile and instantiated with molecule.Options{}, so a
// {{var}} referencing a caller value rendered empty on the created bead. The
// var lives only in the step description (the title-only unresolved-var guard
// never catches it), reproducing the reporter's silent empty-text symptom.
func TestOrderRunSubstitutesCallerVarsInBeadText(t *testing.T) {
	dir := t.TempDir()
	// subject is a declared-but-defaultless var: instantiation renders {{subject}}
	// empty unless the caller value is threaded through to molecule.Instantiate.
	formulaBody := `
formula = "e1-var-text"
version = 1

[vars.subject]
description = "Work subject"

[[steps]]
id = "work"
title = "Work step"
description = "Handle {{subject}} now."
`
	if err := os.WriteFile(filepath.Join(dir, "e1-var-text.toml"), []byte(strings.TrimSpace(formulaBody)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	aa := []orders.Order{{Name: "textorder", Formula: "e1-var-text", Trigger: "manual", FormulaLayer: dir}}
	store := beads.NewMemStore()

	var stdout, stderr bytes.Buffer
	code := doOrderRunWithJSON(aa, "textorder", "", t.TempDir(), beads.OrdersStore{Store: store}, nil, false, map[string]string{"subject": "widgets"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doOrderRunWithJSON = %d, want 0; stderr: %s", code, stderr.String())
	}

	all, err := store.ListOpen()
	if err != nil {
		t.Fatalf("store.ListOpen(): %v", err)
	}
	var work *beads.Bead
	for i := range all {
		if all[i].Title == "Work step" {
			work = &all[i]
			break
		}
	}
	if work == nil {
		t.Fatalf("work step bead not created; beads=%#v", all)
	}
	if want := "Handle widgets now."; work.Description != want {
		t.Errorf("step description = %q, want %q (caller var must substitute into bead text)", work.Description, want)
	}
}

// TestOrderExecEnvWithErrorOverlaysVars proves the args channel reaches an exec
// order's process environment while leaving the static [order.env] reserved-key
// guard intact.
func TestOrderExecEnvWithErrorOverlaysVars(t *testing.T) {
	cityDir := t.TempDir()
	target := execStoreTarget{ScopeRoot: cityDir, ScopeKind: "city", Prefix: "ct"}
	a := orders.Order{Name: "hooked", Trigger: "manual", Exec: "true"}

	envSlice, err := orderExecEnvWithError(cityDir, nil, target, a, map[string]string{
		"repo": "octo/demo",
		"pr":   "42",
	})
	if err != nil {
		t.Fatalf("orderExecEnvWithError: %v", err)
	}

	got := map[string]string{}
	for _, entry := range envSlice {
		if key, value, ok := strings.Cut(entry, "="); ok {
			got[key] = value
		}
	}
	if got["repo"] != "octo/demo" {
		t.Fatalf("env[repo] = %q, want octo/demo; env=%v", got["repo"], envSlice)
	}
	if got["pr"] != "42" {
		t.Fatalf("env[pr] = %q, want 42; env=%v", got["pr"], envSlice)
	}

	// The static [order.env] reserved-key guard must still reject an order that
	// tries to override controller-owned env via [order.env].
	reserved := orders.Order{Name: "hooked", Trigger: "manual", Exec: "true", Env: map[string]string{"GC_CITY": "x"}}
	if _, err := orderExecEnvWithError(cityDir, nil, target, reserved, nil); err == nil {
		t.Fatal("orderExecEnvWithError: expected reserved [order.env] key GC_CITY to be rejected")
	}
}

// TestDispatchOneRefusesMissingRequiredParam proves a declared-required param
// absent from the dispatch vars is a hard error: dispatch records OrderFailed
// and never fires the order (no OrderFired, exec never runs).
func TestDispatchOneRefusesMissingRequiredParam(t *testing.T) {
	store := beads.NewMemStore()
	tracking, err := store.Create(beads.Bead{
		Title:  "order:needs-repo",
		Labels: []string{"order-run:needs-repo", labelOrderTracking},
	})
	if err != nil {
		t.Fatal(err)
	}

	var rec memRecorder
	ranExec := false
	execRun := func(context.Context, string, string, []string) ([]byte, error) {
		ranExec = true
		return nil, nil
	}
	ad := buildOrderDispatcherFromListExec([]orders.Order{{
		Name:     "needs-repo",
		Trigger:  "cooldown",
		Interval: "1h",
		Exec:     "echo hi",
		Params:   map[string]orders.OrderParam{"repo": {Required: true}},
	}}, store, nil, execRun, &rec)
	if ad == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	mad := ad.(*memoryOrderDispatcher)

	// nil vars → the required "repo" param is missing → dispatch must refuse.
	mad.addInflight()
	mad.dispatchOne(context.Background(), store, execStoreTarget{ScopeRoot: t.TempDir()}, mad.aa[0], t.TempDir(), tracking.ID, nil, nil)

	if !rec.hasType(events.OrderFailed) {
		t.Fatal("missing order.failed event for missing required param")
	}
	if rec.hasType(events.OrderFired) {
		t.Fatal("order.fired recorded; dispatch must refuse before firing")
	}
	if ranExec {
		t.Fatal("exec ran despite missing required param")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var failMsg string
	for _, e := range rec.events {
		if e.Type == events.OrderFailed {
			failMsg = e.Message
		}
	}
	if !strings.Contains(failMsg, "repo") || !strings.Contains(failMsg, "required") {
		t.Fatalf("order.failed message = %q, want it to name the missing required param repo", failMsg)
	}
}
