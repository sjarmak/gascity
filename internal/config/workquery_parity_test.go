package config

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/shellquote"
)

// updateGolden regenerates the workquery golden fixtures when set.
var updateGolden = flag.Bool("update", false, "update workquery golden files")

// This file freezes the behavior of the seven private Effective*Query
// resolvers as they existed before S04b's table-driven refactor. The
// oldEffective* functions are verbatim copies of the pre-refactor private
// method bodies (override check + poolDemandTarget + build-script dance).
// TestEffectiveQueryParity asserts that every exported Effective*Query and
// Effective*QueryForBeads accessor produces byte-identical output versus its
// frozen oracle for a matrix of agent shapes and both flag values. When the
// oracle copies are eventually retired, TestWorkQueryGolden below remains as
// the permanent byte-identity pin.

func oldEffectiveWorkQuery(a *Agent, includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	legacyTarget := legacyWorkflowControlQualifiedName(target)
	if legacyTarget == "" {
		script := standardAssignedWorkQueryScript(includeEphemeralReady) +
			poolDemandOriginGateScript() +
			poolDemandFirstRowFunctionScript(includeEphemeralReady) +
			`probe_pool_demand "$1"; ` +
			`printf "[]"`
		return shellquote.Join([]string{"sh", "-c", script, "--", target})
	}
	script := legacyControlAssignedWorkQueryScript(includeEphemeralReady) +
		poolDemandOriginGateScript() +
		poolDemandFirstRowFunctionScript(includeEphemeralReady) +
		`probe_pool_demand "$1"; ` +
		`probe_pool_demand "$2"; ` +
		`printf "[]"`
	return shellquote.Join([]string{"sh", "-c", script, "--", target, legacyTarget})
}

func oldEffectiveAssignedInProgressQuery(a *Agent, includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	if legacyWorkflowControlQualifiedName(target) != "" {
		return shellquote.Join([]string{"sh", "-c", legacyControlAssignedInProgressWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
	}
	return shellquote.Join([]string{"sh", "-c", standardAssignedInProgressWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
}

func oldEffectiveAssignedReadyQuery(a *Agent, includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	if legacyWorkflowControlQualifiedName(target) != "" {
		return shellquote.Join([]string{"sh", "-c", legacyControlAssignedReadyWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
	}
	return shellquote.Join([]string{"sh", "-c", standardAssignedReadyWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
}

func oldEffectiveRoutedPoolQuery(a *Agent, includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	legacyTarget := legacyWorkflowControlQualifiedName(target)
	if legacyTarget == "" {
		return routedPoolWorkQueryCommand(includeEphemeralReady, target)
	}
	return routedPoolWorkQueryCommand(includeEphemeralReady, target, legacyTarget)
}

func oldEffectivePoolDemandQuery(a *Agent, includeEphemeralReady bool) string {
	if a.ScaleCheck != "" {
		return a.ScaleCheck
	}
	target := a.poolDemandTarget()
	return poolDemandCountShell(target, includeEphemeralReady)
}

func oldEffectiveOnDeath(a *Agent, includeEphemeralInProgress bool) string {
	if a.OnDeath != "" {
		return a.OnDeath
	}
	route := a.QualifiedName()
	if a.PoolName != "" {
		route = a.PoolName
	}
	_ = includeEphemeralInProgress
	ephemeralRead := bdQueryEphemeralStatusQuietShell("in_progress") + ` | ` +
		`jq -r --arg assignee ` + shellquote.Quote(a.QualifiedName()) + ` '.[] | select((.assignee // "") == $assignee) | [.id, ` + jqMeta(beadmeta.RunTargetMetadataKey) + `, ` + jqMeta(beadmeta.RoutedToMetadataKey) + `] | @tsv' 2>/dev/null; `
	return `{ ` +
		`bd list --assignee=` + a.QualifiedName() +
		` --status=in_progress --json 2>/dev/null | ` +
		`jq -r '.[] | [.id, ` + jqMeta(beadmeta.RunTargetMetadataKey) + `, ` + jqMeta(beadmeta.RoutedToMetadataKey) + `] | @tsv' 2>/dev/null; ` +
		ephemeralRead +
		`} | ` +
		`while IFS="$(printf '\t')" read -r id run_target routed_to; do ` +
		`[ -z "$id" ] && continue; ` +
		`if [ -n "$run_target" ] || [ -n "$routed_to" ]; then ` +
		`if ! err=$(bd update "$id" --assignee "" --status open 2>&1 >/dev/null); then printf 'gc-recovery: on_death release failed for %s: %s\n' "$id" "$err"; fi; ` +
		`else if ! err=$(bd update "$id" --assignee "" --status open --set-metadata ` + shellquote.Quote(beadmeta.RunTargetMetadataKey+"="+route) + ` 2>&1 >/dev/null); then printf 'gc-recovery: on_death release failed for %s: %s\n' "$id" "$err"; fi; ` +
		`fi; ` +
		`done`
}

func oldEffectiveOnBoot(a *Agent, includeEphemeralInProgress bool) string {
	if a.OnBoot != "" {
		return a.OnBoot
	}
	template := a.QualifiedName()
	if a.PoolName != "" {
		template = a.PoolName
	}
	_ = includeEphemeralInProgress
	ephemeralRead := bdQueryEphemeralStatusQuietShell("in_progress") + ` | ` +
		`jq -r --arg template "$template" '.[] | select((.assignee // "") == "") | select((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == $template) or ((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "") and (` + jqMeta(beadmeta.RunTargetMetadataKey) + ` == $template) and (` + jqMeta(beadmeta.KindMetadataKey) + ` == "` + beadmeta.KindWorkflow + `"))) | .id' 2>/dev/null; `
	return `template=` + shellquote.Quote(template) + `; ` +
		`{ ` +
		`bd list --metadata-field "` + beadmeta.RoutedToMetadataKey + `=$template" --status=in_progress --no-assignee --json 2>/dev/null | ` +
		`jq -r '.[].id' 2>/dev/null; ` +
		`bd list --metadata-field "` + beadmeta.RunTargetMetadataKey + `=$template" --metadata-field "` + beadmeta.KindMetadataKey + `=` + beadmeta.KindWorkflow + `" --status=in_progress --no-assignee --json 2>/dev/null | ` +
		`jq -r '.[] | select(` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "") | .id' 2>/dev/null; ` +
		ephemeralRead +
		`} | awk 'NF && !seen[$0]++' | ` +
		`xargs -rI{} sh -c 'if ! err=$(bd update "$1" --status open 2>&1 >/dev/null); then printf "gc-recovery: on_boot reopen failed for %s: %s\n" "$1" "$err"; fi' _ {}`
}

// parityVariant binds an exported query kind's accessors to its frozen oracle.
type parityVariant struct {
	name     string
	plain    func(*Agent) string
	forBeads func(*Agent, BeadsConfig) string
	old      func(*Agent, bool) string
}

func parityVariants() []parityVariant {
	return []parityVariant{
		{"Work", (*Agent).EffectiveWorkQuery, (*Agent).EffectiveWorkQueryForBeads, oldEffectiveWorkQuery},
		{"AssignedInProgress", (*Agent).EffectiveAssignedInProgressQuery, (*Agent).EffectiveAssignedInProgressQueryForBeads, oldEffectiveAssignedInProgressQuery},
		{"AssignedReady", (*Agent).EffectiveAssignedReadyQuery, (*Agent).EffectiveAssignedReadyQueryForBeads, oldEffectiveAssignedReadyQuery},
		{"RoutedPool", (*Agent).EffectiveRoutedPoolQuery, (*Agent).EffectiveRoutedPoolQueryForBeads, oldEffectiveRoutedPoolQuery},
		{"PoolDemand", (*Agent).EffectivePoolDemandQuery, (*Agent).EffectivePoolDemandQueryForBeads, oldEffectivePoolDemandQuery},
		{"OnDeath", (*Agent).EffectiveOnDeath, (*Agent).EffectiveOnDeathForBeads, oldEffectiveOnDeath},
		{"OnBoot", (*Agent).EffectiveOnBoot, (*Agent).EffectiveOnBootForBeads, oldEffectiveOnBoot},
	}
}

type parityShape struct {
	name  string
	agent *Agent
}

func parityAgentShapes() []parityShape {
	return []parityShape{
		{"plain", &Agent{Name: "worker"}},
		{"pool", &Agent{Name: "worker", PoolName: "worker-pool"}},
		{"legacyBare", &Agent{Name: ControlDispatcherAgentName}},
		{"legacyPrefixed", &Agent{Name: ControlDispatcherAgentName, Dir: "rig"}},
		{"overrideWorkQuery", &Agent{Name: "worker", WorkQuery: "custom-work"}},
		{"overrideScaleCheck", &Agent{Name: "worker", ScaleCheck: "custom-scale"}},
		{"overrideOnDeath", &Agent{Name: "worker", OnDeath: "custom-death"}},
		{"overrideOnBoot", &Agent{Name: "worker", OnBoot: "custom-boot"}},
		{"overrideWorkQueryEmptyScaleCheck", &Agent{Name: "worker", WorkQuery: "", ScaleCheck: ""}},
	}
}

func TestEffectiveQueryParity(t *testing.T) {
	bd104 := BeadsConfig{}
	bd105 := BeadsConfig{BDCompatibility: BeadsBDCompatibility105}
	if bd104.UsesBD105ReadySemantics() {
		t.Fatal("bd104 stub unexpectedly reports BD105 ready semantics")
	}
	if !bd105.UsesBD105ReadySemantics() {
		t.Fatal("bd105 stub must report BD105 ready semantics")
	}

	for _, shape := range parityAgentShapes() {
		for _, v := range parityVariants() {
			shape, v := shape, v
			t.Run(shape.name+"/"+v.name, func(t *testing.T) {
				if got, want := v.plain(shape.agent), v.old(shape.agent, false); got != want {
					t.Fatalf("plain mismatch\n got=%q\nwant=%q", got, want)
				}
				if got, want := v.forBeads(shape.agent, bd104), v.old(shape.agent, false); got != want {
					t.Fatalf("forBeads(bd104) mismatch\n got=%q\nwant=%q", got, want)
				}
				if got, want := v.forBeads(shape.agent, bd105), v.old(shape.agent, true); got != want {
					t.Fatalf("forBeads(bd105) mismatch\n got=%q\nwant=%q", got, want)
				}
			})
		}
	}
}

// TestQueryTableCoversAllKinds guards against a queryKind added to the enum
// but not the table: a missing row would panic via a nil spec.override at
// runtime. Every declared kind must have both funcs set.
func TestQueryTableCoversAllKinds(t *testing.T) {
	kinds := []queryKind{
		queryWork, queryAssignedInProgress, queryAssignedReady,
		queryRoutedPool, queryPoolDemand, queryOnDeath, queryOnBoot,
	}
	if len(queryTable) != len(kinds) {
		t.Fatalf("queryTable has %d rows, expected %d kinds", len(queryTable), len(kinds))
	}
	for _, k := range kinds {
		spec, ok := queryTable[k]
		if !ok {
			t.Errorf("queryKind %d missing from queryTable", k)
			continue
		}
		if spec.override == nil {
			t.Errorf("queryKind %d has nil override", k)
		}
		if spec.build == nil {
			t.Errorf("queryKind %d has nil build", k)
		}
	}
}

// TestOnDeathOnBootFlagBlind pins invariant I6: OnDeath/OnBoot ignore the
// includeEphemeral flag, so their ForBeads variant equals the plain variant.
func TestOnDeathOnBootFlagBlind(t *testing.T) {
	bd105 := BeadsConfig{BDCompatibility: BeadsBDCompatibility105}
	a := &Agent{Name: "worker"}
	if a.EffectiveOnDeathForBeads(bd105) != a.EffectiveOnDeath() {
		t.Error("EffectiveOnDeathForBeads must equal EffectiveOnDeath (flag-blind)")
	}
	if a.EffectiveOnBootForBeads(bd105) != a.EffectiveOnBoot() {
		t.Error("EffectiveOnBootForBeads must equal EffectiveOnBoot (flag-blind)")
	}
}

// TestNotDisarmedFilterJQMatchesGoDecoder pins the one predicate in this file
// that has no Go oracle above: notDisarmedFilterJQ re-implements
// beadmeta.IsDisarmedRaw in jq, and the two must agree row for row.
//
// TestWorkQueryGolden below cannot catch drift here. It pins the filter's
// source text, so a semantically wrong rewrite still passes once the golden is
// re-recorded with -update. This test pins the behavior instead, feeding both
// implementations the same JSON and comparing their verdicts.
//
// The stakes are the reason it exists: if the shell filter and the Go claim
// path ever disagree, the reconciler counts demand the worker then refuses, and
// the pool spawns slots forever for work nobody can take — the exact
// spin/strand loop the disarm flag was added to close.
func TestNotDisarmedFilterJQMatchesGoDecoder(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}

	k := beadmeta.DisarmedMetadataKey
	cases := []struct {
		name string
		row  map[string]any
	}{
		// Shapes bd actually emits: --set-metadata gc.disarmed=true type-infers
		// to a JSON boolean; StringMap-coerced rows carry the string spellings.
		{"bool true", map[string]any{"id": "b1", "metadata": map[string]any{k: true}}},
		{"bool false", map[string]any{"id": "b1", "metadata": map[string]any{k: false}}},
		{"string true", map[string]any{"id": "b1", "metadata": map[string]any{k: "true"}}},
		{"string false", map[string]any{"id": "b1", "metadata": map[string]any{k: "false"}}},
		{"string empty", map[string]any{"id": "b1", "metadata": map[string]any{k: ""}}},
		// Case-fold and trim: both sides normalize, in opposite orders.
		{"string TRUE uppercase", map[string]any{"id": "b1", "metadata": map[string]any{k: "TRUE"}}},
		{"string FALSE uppercase", map[string]any{"id": "b1", "metadata": map[string]any{k: "FALSE"}}},
		{"string true padded", map[string]any{"id": "b1", "metadata": map[string]any{k: " true "}}},
		{"string false padded", map[string]any{"id": "b1", "metadata": map[string]any{k: " false "}}},
		// Unrecognized values fail closed on both sides.
		{"string unrecognized", map[string]any{"id": "b1", "metadata": map[string]any{k: "banana"}}},
		{"string yes", map[string]any{"id": "b1", "metadata": map[string]any{k: "yes"}}},
		{"number", map[string]any{"id": "b1", "metadata": map[string]any{k: 1}}},
		{"object", map[string]any{"id": "b1", "metadata": map[string]any{k: map[string]any{"nested": true}}}},
		// An explicit null and an absent key deliberately collide: the Go string
		// decoders collapse null to "", so both mean not-disarmed and both are
		// kept (see beadmeta/disarm.go). wantKept below derives from the Go
		// reader, so this row pins jq to whatever beadmeta decides.
		{"explicit null", map[string]any{"id": "b1", "metadata": map[string]any{k: nil}}},
		{"key absent", map[string]any{"id": "b1", "metadata": map[string]any{"gc.other": "x"}}},
		// Structural shapes: every non-bd work_query override omits metadata.
		{"metadata absent", map[string]any{"id": "b1"}},
		{"metadata not an object", map[string]any{"id": "b1", "metadata": "nope"}},
		{"metadata null", map[string]any{"id": "b1", "metadata": nil}},
	}

	// Run the filter as its own quoted jq stage so this covers the shell quoting
	// too, not just the jq expression.
	jqStage := shellquote.Join([]string{"jq", notDisarmedFilterJQ()})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal([]any{tc.row})
			if err != nil {
				t.Fatalf("marshal row: %v", err)
			}

			// Round-trip before consulting the Go decoder so it sees the same
			// decoded types the hook path does (a JSON number lands as float64).
			var decoded []map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal row: %v", err)
			}
			md, addressable := decoded[0]["metadata"].(map[string]any)
			// Mirrors isDisarmedHookCandidate: metadata we cannot address at all
			// is no marker, so the row is kept.
			wantKept := !addressable || !beadmeta.IsDisarmedRaw(md)

			script := "printf '%s' " + shellquote.Quote(string(raw)) + " | " + jqStage
			out := []byte(runShellWithFakeBd(t, script, nil, "#!/bin/sh\nprintf '[]'\n"))
			var kept []map[string]any
			if err := json.Unmarshal(out, &kept); err != nil {
				t.Fatalf("unmarshal jq output %q: %v", out, err)
			}

			if gotKept := len(kept) == 1; gotKept != wantKept {
				t.Errorf("jq and Go disagree for %s\n jq kept=%v\n Go kept=%v\n input=%s\n jq out=%s",
					tc.name, gotKept, wantKept, raw, out)
			}
		})
	}
}

// TestPoolDemandCountShellExcludesDisarmedFromEverySource executes the real
// generated count-form against a stub bd and asserts the disarm exclusion holds
// for every source it unions, not just the canonical ready tier.
//
// The count-form pulls from three probes — the canonical routed ready tier, the
// gc.run_target migration probe for pre-gc.routed_to workflow roots, and the
// legacy ephemeral query — and adds them together. The filter used to sit on the
// canonical tier alone, so a disarmed legacy root or ephemeral bead reached the
// union unfiltered and counted as demand the claim path then refused.
//
// TestNotDisarmedFilterJQMatchesGoDecoder pins what the filter decides per row;
// this pins where it runs. Neither is redundant: the goldens freeze the script
// text but re-record with -update, so only executing the thing catches a filter
// that is correct in isolation and attached to the wrong pipe.
func TestPoolDemandCountShellExcludesDisarmedFromEverySource(t *testing.T) {
	for _, bin := range []string{"jq", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	const target = "worker"
	disarmed := map[string]any{beadmeta.DisarmedMetadataKey: true}
	legacyRoot := func(id string, extra map[string]any) map[string]any {
		md := map[string]any{
			beadmeta.RunTargetMetadataKey: target,
			beadmeta.KindMetadataKey:      beadmeta.KindWorkflow,
		}
		for k, v := range extra {
			md[k] = v
		}
		return map[string]any{"id": id, "metadata": md}
	}
	routedBead := func(id string, extra map[string]any) map[string]any {
		md := map[string]any{beadmeta.RoutedToMetadataKey: target}
		for k, v := range extra {
			md[k] = v
		}
		return map[string]any{"id": id, "metadata": md}
	}

	cases := []struct {
		name      string
		routed    []any // canonical `bd ready --metadata-field gc.routed_to=...`
		legacy    []any // migration `bd ready --metadata-field gc.run_target=...`
		ephemeral []any // legacy `bd query ephemeral=true AND status=open`
		want      string
	}{
		// The two gaps measured on the rejected revision: each returned 1.
		{name: "disarmed legacy workflow root", legacy: []any{legacyRoot("L1", disarmed)}, want: "0"},
		{name: "disarmed legacy ephemeral", ephemeral: []any{routedBead("E1", disarmed)}, want: "0"},
		// The tier that was already filtered, kept as a regression pin.
		{name: "disarmed canonical ready", routed: []any{routedBead("R1", disarmed)}, want: "0"},
		// The filter must not over-drop: armed work on every source still counts.
		{name: "armed legacy workflow root", legacy: []any{legacyRoot("L2", nil)}, want: "1"},
		{name: "armed legacy ephemeral", ephemeral: []any{routedBead("E2", nil)}, want: "1"},
		{name: "armed canonical ready", routed: []any{routedBead("R2", nil)}, want: "1"},
		{name: "cleared flag re-arms", routed: []any{routedBead("R3", map[string]any{beadmeta.DisarmedMetadataKey: ""})}, want: "1"},
		// An armed bead must survive a disarmed sibling on the same source.
		{name: "armed peer of disarmed legacy root", legacy: []any{legacyRoot("L3", disarmed), legacyRoot("L4", nil)}, want: "1"},
		// Fail-closed survives the move to the union.
		{name: "unparseable value fails closed", routed: []any{routedBead("R4", map[string]any{beadmeta.DisarmedMetadataKey: "banana"})}, want: "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marshalRows := func(name string, rows []any) string {
				if rows == nil {
					rows = []any{}
				}
				raw, err := json.Marshal(rows)
				if err != nil {
					t.Fatalf("marshal %s: %v", name, err)
				}
				return shellquote.Quote(string(raw))
			}

			// Stub bd dispatches on the flags each probe is built with. The
			// canonical probe is the only one carrying gc.routed_to, and the
			// migration probe the only one carrying gc.run_target, so matching on
			// those keys routes each probe to its own fixture.
			stub := "#!/bin/sh\ncase \"$*\" in\n" +
				"  *" + beadmeta.RoutedToMetadataKey + "=" + target + "*) printf '%s' " + marshalRows("routed rows", tc.routed) + " ;;\n" +
				"  *" + beadmeta.RunTargetMetadataKey + "=" + target + "*) printf '%s' " + marshalRows("legacy rows", tc.legacy) + " ;;\n" +
				"  *ephemeral=true*) printf '%s' " + marshalRows("ephemeral rows", tc.ephemeral) + " ;;\n" +
				"  *) printf '[]' ;;\nesac\n"

			// includeEphemeralReady=false keeps the legacy ephemeral probe live;
			// the true form short-circuits it to printf "[]".
			out := runShellWithFakeBd(t, poolDemandCountShell(target, false), nil, stub)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Errorf("demand count = %s, want %s\n(a disarmed bead counted as demand spawns a slot the claim path refuses)", got, tc.want)
			}
		})
	}
}

// TestEffectiveAssignedTiersExcludeDisarmedRow is the assigned-tier sibling
// of TestPoolDemandCountShellExcludesDisarmedFromEverySource. Unlike the pool
// tiers, these run bd with --limit=20, so filtering the single returned row IS
// the whole fix: a disarmed bead must not be served as this session's own
// assigned work. There is deliberately no "armed peer falls through" case
// here — with limit=1 there is no second row within this tier to fall
// through to; widening past limit=1 so a disarmed head has assigned work
// behind it to serve instead is tracked separately (gc-ewk4), not by this
// bead.
func TestEffectiveAssignedTiersExcludeDisarmedRow(t *testing.T) {
	a := Agent{Name: "worker", Dir: "hello-world"}
	const disarmedRow = `[{"id":"assigned-row","metadata":{"gc.disarmed":true}}]`
	const armedRow = `[{"id":"assigned-row"}]`

	cases := []struct {
		name    string
		query   func(*Agent) string
		bdMatch string
	}{
		{"AssignedReady", (*Agent).EffectiveAssignedReadyQuery, `"ready --assignee=worker-session --json --limit=20"`},
		{"AssignedInProgress", (*Agent).EffectiveAssignedInProgressQuery, `"list --status in_progress --assignee=worker-session --json --limit=20"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bdScript := func(row string) string {
				return "#!/bin/sh\nset -eu\ncase \"$*\" in\n  " + tc.bdMatch + ") printf '" + row + "' ;;\n  *) printf '[]' ;;\nesac\n"
			}
			env := map[string]string{"GC_SESSION_NAME": "worker-session"}

			out := runShellWithFakeBd(t, tc.query(&a), env, bdScript(disarmedRow))
			if got := strings.TrimSpace(out); got != "[]" {
				t.Fatalf("%s must exclude a disarmed assignee row, got %q", tc.name, got)
			}

			out = runShellWithFakeBd(t, tc.query(&a), env, bdScript(armedRow))
			if got := strings.TrimSpace(out); got != armedRow {
				t.Fatalf("%s must still serve an armed assignee row, got %q want %q", tc.name, got, armedRow)
			}
		})
	}
}

// TestEffectiveRoutedPoolQueryExcludesDisarmedRow is the row-returning
// sibling of TestPoolDemandCountShellExcludesDisarmedFromEverySource: it pins
// where notDisarmedFilterJQ runs for the tier that backs
// EffectiveWorkQuery/EffectiveRoutedPoolQuery — the query a prompt template
// can run directly, bypassing gc hook's own Go-side
// filterUnreadyHookCandidates (gc-u6an finding 3).
func TestEffectiveRoutedPoolQueryExcludesDisarmedRow(t *testing.T) {
	a := Agent{Name: "worker", Dir: "hello-world"}
	got := a.EffectiveRoutedPoolQuery()

	out := runShellWithFakeBd(t, got, nil, `#!/bin/sh
set -eu
case "$*" in
  ready*"--metadata-field gc.routed_to=hello-world/worker"*)
    printf '[{"id":"disarmed","metadata":{"gc.disarmed":true}},{"id":"armed"}]'
    ;;
  *) printf '[]' ;;
esac
`)
	if got := strings.TrimSpace(out); got != `[{"id":"armed"}]` {
		t.Fatalf("EffectiveRoutedPoolQuery() = %q, want only the armed peer of a disarmed row", got)
	}
}

// TestEffectiveRoutedPoolQueryExcludesDisarmedRow_FallbackTiers is the
// fallback-tier sibling of TestEffectiveRoutedPoolQueryExcludesDisarmedRow.
// That test only exercised the canonical gc.routed_to tier; a code-reviewer
// and security-reviewer pass on this bead's cycle-3 fix independently found
// (with runnable repros) that the two sibling fallback probes in the same
// probe_pool_demand function — the gc.run_target migration probe and the
// legacy ephemeral-store probe — were left unfiltered, reproducing the exact
// gap this bead exists to close on two call sites instead of one. Both
// probes only fire when the canonical tier returns no rows.
func TestEffectiveRoutedPoolQueryExcludesDisarmedRow_FallbackTiers(t *testing.T) {
	a := Agent{Name: "worker", Dir: "hello-world"}

	t.Run("migration probe", func(t *testing.T) {
		out := runShellWithFakeBd(t, a.EffectiveRoutedPoolQuery(), nil, `#!/bin/sh
set -eu
case "$*" in
  *"--metadata-field gc.routed_to=hello-world/worker"*) printf '[]' ;;
  *"--metadata-field gc.run_target=hello-world/worker"*)
    printf '[{"id":"disarmed-legacy-root","metadata":{"gc.run_target":"hello-world/worker","gc.kind":"workflow","gc.disarmed":true}},{"id":"armed-legacy-root","metadata":{"gc.run_target":"hello-world/worker","gc.kind":"workflow"}}]'
    ;;
  *) printf '[]' ;;
esac
`)
		if got := strings.TrimSpace(out); !strings.Contains(got, "armed-legacy-root") || strings.Contains(got, "disarmed-legacy-root") {
			t.Fatalf("EffectiveRoutedPoolQuery() migration fallback = %q, want disarmed-legacy-root excluded and armed-legacy-root kept", got)
		}
	})

	t.Run("ephemeral probe", func(t *testing.T) {
		out := runShellWithFakeBd(t, a.EffectiveRoutedPoolQuery(), nil, `#!/bin/sh
set -eu
case "$*" in
  *"--metadata-field gc.routed_to=hello-world/worker"*) printf '[]' ;;
  *"--metadata-field gc.run_target=hello-world/worker"*) printf '[]' ;;
  query*"ephemeral=true AND status=open"*)
    printf '[{"id":"disarmed-ephemeral","assignee":"","status":"open","metadata":{"gc.routed_to":"hello-world/worker","gc.disarmed":true}}]'
    ;;
  *) printf '[]' ;;
esac
`)
		if got := strings.TrimSpace(out); got != "[]" {
			t.Fatalf("EffectiveRoutedPoolQuery() ephemeral fallback = %q, want disarmed-ephemeral excluded", got)
		}
	})
}

// TestEffectiveAssignedTiersExcludeDisarmedRow_EphemeralFallback is the
// ephemeral-fallback sibling of TestEffectiveAssignedTiersExcludeDisarmedRow.
// That test only exercised the primary bd list/ready --limit=20 row; the
// ephemeral probe each assigned tier falls through to when that row is empty
// (bd query ephemeral=true AND status=...) was left unfiltered — reachable
// through the same EffectiveAssignedInProgressQuery/EffectiveAssignedReadyQuery
// commands a prompt template can run directly.
func TestEffectiveAssignedTiersExcludeDisarmedRow_EphemeralFallback(t *testing.T) {
	a := Agent{Name: "worker", Dir: "hello-world"}
	env := map[string]string{"GC_SESSION_NAME": "worker-session"}

	cases := []struct {
		name      string
		query     func(*Agent) string
		primaryBd string
		ephStatus string
	}{
		{"AssignedInProgress", (*Agent).EffectiveAssignedInProgressQuery, "list --status in_progress --assignee=worker-session --json --limit=20", "in_progress"},
		{"AssignedReady", (*Agent).EffectiveAssignedReadyQuery, "ready --assignee=worker-session --json --limit=20", "open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bdScript := "#!/bin/sh\nset -eu\ncase \"$*\" in\n" +
				"  \"" + tc.primaryBd + "\") printf '[]' ;;\n" +
				"  query*\"ephemeral=true AND status=" + tc.ephStatus + "\"*)\n" +
				"    printf '[{\"id\":\"disarmed-ephemeral\",\"assignee\":\"worker-session\",\"status\":\"" + tc.ephStatus + "\",\"metadata\":{\"gc.disarmed\":true}}]'\n" +
				"    ;;\n" +
				"  *) printf '[]' ;;\n" +
				"esac\n"

			out := runShellWithFakeBd(t, tc.query(&a), env, bdScript)
			if got := strings.TrimSpace(out); got != "[]" {
				t.Fatalf("%s ephemeral fallback = %q, want disarmed-ephemeral excluded", tc.name, got)
			}
		})
	}
}

// TestWorkQueryGolden pins the literal generated shell per kind × flag ×
// {normal, pool, legacy-control} so accidental script drift shows up as
// golden churn in the diff. Run with -update to regenerate.
func TestWorkQueryGolden(t *testing.T) {
	shapes := []parityShape{
		{"normal", &Agent{Name: "worker"}},
		{"pool", &Agent{Name: "worker", PoolName: "worker-pool"}},
		{"legacy", &Agent{Name: ControlDispatcherAgentName, Dir: "rig"}},
	}
	for _, shape := range shapes {
		for _, v := range parityVariants() {
			for _, flag := range []struct {
				name  string
				beads BeadsConfig
			}{
				{"bd104", BeadsConfig{}},
				{"bd105", BeadsConfig{BDCompatibility: BeadsBDCompatibility105}},
			} {
				got := v.forBeads(shape.agent, flag.beads)
				name := shape.name + "_" + v.name + "_" + flag.name + ".golden"
				path := filepath.Join("testdata", "workquery", name)
				if *updateGolden {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read golden %s: %v (run with -update to create)", name, err)
				}
				if got != string(want) {
					t.Errorf("golden mismatch for %s\n got=%q\nwant=%q", name, got, string(want))
				}
			}
		}
	}
}
