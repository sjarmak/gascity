package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

const (
	mixedCityAltPrefix = "dr"
	mixedCityWorker    = "worker"
)

func mixedCityRootFixture(t *testing.T, altReadyIDs ...string) (string, *config.City, beads.Store) {
	t.Helper()
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS_FORCE_FALLBACK", "1")
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityTOML := `[workspace]
name = "ds-research"

[beads]
provider = "file"
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".beads", "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".beads", "config.yaml"), []byte("issue_prefix: "+mixedCityAltPrefix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readScopeIssuePrefix(cityPath); got != mixedCityAltPrefix {
		t.Fatalf("fixture issue prefix = %q, want %q", got, mixedCityAltPrefix)
	}

	ready := make([]string, 0, len(altReadyIDs))
	for _, id := range altReadyIDs {
		ready = append(ready, fmt.Sprintf(`{"id":%q,"title":"alternate work","status":"open","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","metadata":{"gc.routed_to":%q}}`, id, mixedCityWorker))
	}
	fakeDir := t.TempDir()
	readyPath := filepath.Join(fakeDir, "ready.json")
	if err := os.WriteFile(readyPath, []byte("["+strings.Join(ready, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	bdPath := filepath.Join(fakeDir, "bd")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
id=
for arg in "$@"; do
  case "$arg" in
    gc-*|%s-*) id="$arg" ;;
  esac
done
case " $* " in
  *" ready "*) cat %q ;;
  *" show "*)
    printf '[{"id":"%%s","title":"alternate work","status":"in_progress","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","assignee":"worker","metadata":{"gc.routed_to":"worker"}}]' "$id"
    ;;
  *" --claim "*)
    case "$id" in
      gc-*) printf 'Error: issue %%s not found\n' "$id" >&2; exit 1 ;;
      *) printf '[{"id":"%%s","title":"alternate work","status":"in_progress","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","assignee":"worker","metadata":{"gc.routed_to":"worker"}}]' "$id" ;;
    esac
    ;;
  *) printf '[]' ;;
esac
`, mixedCityAltPrefix, readyPath)
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.City{
		Workspace: config.Workspace{Name: "ds-research"},
		Beads:     config.BeadsConfig{Provider: "file"},
		Agents: []config.Agent{{
			Name:              mixedCityWorker,
			StartCommand:      "true",
			MinActiveSessions: intPtr(0),
			MaxActiveSessions: intPtr(5),
		}},
	}
	store, err := openStoreAtForCity(cityPath, cityPath)
	if err != nil {
		t.Fatalf("open configured file store: %v", err)
	}
	return cityPath, cfg, store
}

func createMixedCityFileWork(t *testing.T, store beads.Store, title string) beads.Bead {
	t.Helper()
	bead, err := store.Create(beads.Bead{
		Title:  title,
		Type:   "task",
		Status: "open",
		Metadata: map[string]string{
			"gc.routed_to": mixedCityWorker,
		},
	})
	if err != nil {
		t.Fatalf("create configured-store work: %v", err)
	}
	return bead
}

func mixedCityDemand(t *testing.T, cityPath string, cfg *config.City, store beads.Store) DesiredStateResult {
	t.Helper()
	return buildDesiredStateWithSessionBeads(
		"test-city", cityPath, time.Now().UTC(), cfg, runtime.NewFake(),
		store, nil, nil, nil, io.Discard,
	)
}

func TestMixedCityRootDemandSeesAlternateStore(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t, "dr-ready")
	got := mixedCityDemand(t, cityPath, cfg, store)
	if got.ScaleCheckCounts[mixedCityWorker] != 1 {
		t.Fatalf("ScaleCheckCounts[%s] = %d, want 1 from alternate city-root store (all=%v)", mixedCityWorker, got.ScaleCheckCounts[mixedCityWorker], got.ScaleCheckCounts)
	}
}

func TestMixedCityRootControlReadySourcesSeeAlternateStore(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t, "dr-control", "dr-control")
	fileBead := createMixedCityFileWork(t, store, "configured control")
	sources, _, err := controlReadyCacheSources(cityPath, cityPath, cfg)
	if err != nil {
		t.Fatalf("controlReadyCacheSources: %v", err)
	}
	var legs [][]beads.Bead
	for _, source := range sources {
		ready, readyErr := source.Ready()
		if readyErr != nil {
			t.Fatalf("source Ready: %v", readyErr)
		}
		legs = append(legs, ready)
	}
	got := mergeControlReadyLegs(legs...)
	if !containsID(got, "dr-control") {
		t.Fatalf("merged ready sources = %v, want dr-control", controlLegIDs(got))
	}
	if countID(got, "dr-control") != 1 {
		t.Fatalf("merged ready sources = %v, want dr-control exactly once", controlLegIDs(got))
	}
	fallback, err := controlReadyFallbackReady(cityPath, cityPath, cfg, nil, false)
	if err != nil {
		t.Fatalf("controlReadyFallbackReady: %v", err)
	}
	if !containsID(fallback, "dr-control") || !containsID(fallback, fileBead.ID) || countID(fallback, "dr-control") != 1 {
		t.Fatalf("fallback ready = %v, want one dr-control plus %s", controlLegIDs(fallback), fileBead.ID)
	}
}

func TestMixedCityRootControlLedgerUsesExistingResolutionBeforeAlternatePrefix(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t, "dr-control")
	fileBead := createMixedCityFileWork(t, store, "configured control")

	altOwner, gotAlt, err := controlBeadLedger(cityPath, cityPath, cfg, store, "dr-control")
	if err != nil {
		t.Fatalf("alternate prefix: %v", err)
	}
	altInner, _, _ := unwrapBeadPolicyStore(altOwner)
	if _, ok := altInner.(*beads.BdStore); !ok || gotAlt.ID != "dr-control" {
		t.Fatalf("alternate prefix resolved (%T, %q), want *beads.BdStore and dr-control", altOwner, gotAlt.ID)
	}

	fileOwner, gotFile, err := controlBeadLedger(cityPath, cityPath, cfg, store, fileBead.ID)
	if err != nil {
		t.Fatalf("configured store first: %v", err)
	}
	if fileOwner != store || gotFile.ID != fileBead.ID {
		t.Fatalf("configured store resolved (%T, %q), want original file store and %q", fileOwner, gotFile.ID, fileBead.ID)
	}

	emptyPrefixPath, emptyPrefixCfg, emptyPrefixStore := mixedCityRootFixture(t)
	originalOpen := openCityRootAltStoreFn
	openCityRootAltStoreFn = func(storePath, cityPath string) (beads.Store, error) {
		if samePath(storePath, emptyPrefixPath) {
			return beads.NewBdStoreWithPrefix(emptyPrefixPath, nil, ""), nil
		}
		return originalOpen(storePath, cityPath)
	}
	t.Cleanup(func() { openCityRootAltStoreFn = originalOpen })
	_, _, err = controlBeadLedger(emptyPrefixPath, emptyPrefixPath, emptyPrefixCfg, emptyPrefixStore, "dr-missing")
	if err == nil || !strings.Contains(err.Error(), "declares no issue prefix") {
		t.Fatalf("missing alternate prefix error = %v, want a fail-closed declaration error", err)
	}
}

func TestMixedCityRootControlLedgerKeepsGraphBindingFirst(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t)
	binding := beads.NewMemStoreFrom(100, nil, nil)
	seedCLIStorageRoutes(t, cityPath, messagingSplitRoutes(binding))
	resident, err := binding.Create(beads.Bead{Title: "binding-resident", Type: "task"})
	if err != nil {
		t.Fatalf("create binding bead: %v", err)
	}

	gotStore, gotBead, err := controlBeadLedger(cityPath, cityPath, cfg, store, resident.ID)
	if err != nil {
		t.Fatalf("controlBeadLedger for a mixed-root binding id: %v", err)
	}
	if gotBead.ID != resident.ID {
		t.Fatalf("resolved bead = %q, want %q", gotBead.ID, resident.ID)
	}
	probe, err := gotStore.Create(beads.Bead{Title: "probe", Type: "task"})
	if err != nil {
		t.Fatalf("write through resolved graph store: %v", err)
	}
	if _, err := binding.Get(probe.ID); err != nil {
		t.Fatalf("resolved store did not write to graph binding: %v", err)
	}
}

func TestCityRootAltStoreOpensOnceAcrossMixedRootReaders(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t, "dr-control")
	originalOpen := openCityRootAltStoreFn
	opens := 0
	openCityRootAltStoreFn = func(storePath, cityPath string) (beads.Store, error) {
		opens++
		return originalOpen(storePath, cityPath)
	}
	t.Cleanup(func() { openCityRootAltStoreFn = originalOpen })

	beforeClaimOps := opens
	for range 3 {
		if _, err := mixedCityRootHookClaimOps(cityPath, hookClaimOps{}); err != nil {
			t.Fatalf("claim ops: %v", err)
		}
	}
	if claimOpens := opens - beforeClaimOps; claimOpens != 0 {
		t.Errorf("claim-op construction opened the alternate store %d times, want zero", claimOpens)
	}
	for range 3 {
		_ = mixedCityDemand(t, cityPath, cfg, store)
	}
	for range 3 {
		if _, err := controlReadyFallbackReady(cityPath, cityPath, cfg, nil, false); err != nil {
			t.Fatalf("fallback ready: %v", err)
		}
	}
	for range 3 {
		if _, _, err := controlReadyCacheSources(cityPath, cityPath, cfg); err != nil {
			t.Fatalf("cache sources: %v", err)
		}
	}
	for range 3 {
		_, _, _ = controlBeadLedger(cityPath, cityPath, cfg, store, "dr-control")
	}
	if opens != 1 {
		t.Fatalf("alternate store open count = %d, want exactly one across all readers", opens)
	}
}

func TestCityRootAltStoreRetriesAfterOpenError(t *testing.T) {
	cityPath, _, _ := mixedCityRootFixture(t)
	originalOpen := openCityRootAltStoreFn
	opens := 0
	openCityRootAltStoreFn = func(storePath, cityPath string) (beads.Store, error) {
		opens++
		if opens == 1 {
			return nil, fmt.Errorf("injected open failure")
		}
		return originalOpen(storePath, cityPath)
	}
	t.Cleanup(func() { openCityRootAltStoreFn = originalOpen })

	if _, err := cityRootAltStore(cityPath); err == nil || !strings.Contains(err.Error(), "injected open failure") {
		t.Fatalf("first open error = %v, want injected failure", err)
	}
	second, err := cityRootAltStore(cityPath)
	if err != nil || second == nil {
		t.Fatalf("retry = (%T, %v), want a store", second, err)
	}
	third, err := cityRootAltStore(cityPath)
	if err != nil || third == nil {
		t.Fatalf("memoized read = (%T, %v), want a store", third, err)
	}
	if opens != 2 {
		t.Fatalf("alternate store open count = %d, want two (one failed attempt and one memoized success)", opens)
	}
	if second != third {
		t.Fatalf("memoized store identity changed: %p then %p", second, third)
	}
}

func claimOneMixedCityBead(t *testing.T, cityPath, beadID string) {
	t.Helper()
	originalRunner := hookClaimCommandRunnerWithEnvContext
	hookClaimCommandRunnerWithEnvContext = func(context.Context, map[string]string) beads.CommandRunner {
		return func(_ string, _ string, args ...string) ([]byte, error) {
			if len(args) >= 3 && args[0] == "show" {
				id := args[2]
				return []byte(fmt.Sprintf(`[{"id":%q,"title":"alternate work","status":"in_progress","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","assignee":"worker","metadata":{"gc.routed_to":"worker"}}]`, id)), nil
			}
			if len(args) < 2 || args[0] != "update" {
				return nil, fmt.Errorf("unexpected claim bd args: %v", args)
			}
			id := args[1]
			if strings.HasPrefix(id, "gc-") {
				return nil, fmt.Errorf("claiming %s: %w", id, beads.ErrNotFound)
			}
			return []byte(fmt.Sprintf(`[{"id":%q,"title":"alternate work","status":"in_progress","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","assignee":"worker","metadata":{"gc.routed_to":"worker"}}]`, id)), nil
		}
	}
	defer func() { hookClaimCommandRunnerWithEnvContext = originalRunner }()
	query := fmt.Sprintf("printf '[{\"id\":\"%s\",\"title\":\"work\",\"status\":\"open\",\"issue_type\":\"task\",\"metadata\":{\"gc.routed_to\":\"worker\"}}]'", beadID)
	var stdout, stderr bytes.Buffer
	code := claimHookWork(cityPath, query, cityPath, nil, []hookStore{{dir: cityPath}}, hookClaimOptions{
		Assignee:     mixedCityWorker,
		RouteTargets: []string{mixedCityWorker},
	}, func(string, error) {}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claim %s code=%d stdout=%q stderr=%q", beadID, code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), beadID) {
		t.Fatalf("claim %s stdout=%q", beadID, stdout.String())
	}
}

func TestMixedCityRootClaimFallsBackToConfiguredFileStore(t *testing.T) {
	cityPath, _, store := mixedCityRootFixture(t)
	fileBead := createMixedCityFileWork(t, store, "configured claim")
	claimOneMixedCityBead(t, cityPath, fileBead.ID)
	got, err := store.Get(fileBead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" || got.Assignee != mixedCityWorker {
		t.Fatalf("claimed file bead = status %q assignee %q", got.Status, got.Assignee)
	}
}

func TestMixedCityRootDemandCountsExactlyClaimableWork(t *testing.T) {
	cityPath, cfg, store := mixedCityRootFixture(t, "dr-ready")
	fileBead := createMixedCityFileWork(t, store, "configured ready")
	alt, err := openAuthoritativeStoreAtForCity(cityPath, cityPath)
	if err != nil || alt == nil {
		t.Fatalf("openAuthoritativeStoreAtForCity = (%T, %v), want alternate store", alt, err)
	}
	counts, demand, partial, errs := defaultScaleCheckCountsAndDemand(cfg, []defaultScaleCheckTarget{
		{template: mixedCityWorker, storeKey: "city", store: store},
		{template: mixedCityWorker, storeKey: "city", store: alt},
	})
	if len(errs) != 0 || len(partial) != 0 || counts[mixedCityWorker] != 2 {
		t.Fatalf("dual-store demand = counts %v partial %v errs %v, want exactly two healthy rows", counts, partial, errs)
	}
	ids := append([]string(nil), demand[mixedCityWorker].WorkBeadIDs...)
	slices.Sort(ids)
	want := []string{"dr-ready", fileBead.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("demand bead ids = %v, want exactly %v", ids, want)
	}
	for _, id := range ids {
		claimOneMixedCityBead(t, cityPath, id)
	}
}

func TestSingleStoreCityRootRetainsExistingResults(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname=\"test-city\"\nprefix=\"gc\"\n[beads]\nprovider=\"file\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city", Prefix: "gc"}, Beads: config.BeadsConfig{Provider: "file"}, Agents: []config.Agent{{Name: mixedCityWorker, StartCommand: "true"}}}
	store, err := openStoreAtForCity(cityPath, cityPath)
	if err != nil {
		t.Fatal(err)
	}
	work := createMixedCityFileWork(t, store, "single-store work")
	got := mixedCityDemand(t, cityPath, cfg, store)
	wantCounts, _, wantErrs := defaultScaleCheckCounts([]defaultScaleCheckTarget{{template: mixedCityWorker, storeKey: "city", store: store}})
	if len(wantErrs) != 0 || got.ScaleCheckCounts[mixedCityWorker] != wantCounts[mixedCityWorker] {
		t.Fatalf("single-store demand = %v, want %v (errs=%v)", got.ScaleCheckCounts, wantCounts, wantErrs)
	}
	sources, _, err := controlReadyCacheSources(cityPath, cityPath, cfg)
	if err != nil || len(sources) != 1 {
		t.Fatalf("single-store ready sources = %d, err=%v; want one", len(sources), err)
	}
	owner, resolved, err := controlBeadLedger(cityPath, cityPath, cfg, store, work.ID)
	if err != nil || owner != store || resolved.ID != work.ID {
		t.Fatalf("single-store ledger = (%T, %q, %v), want original store and %q", owner, resolved.ID, err, work.ID)
	}

	mixedPath, mixedCfg, _ := mixedCityRootFixture(t)
	mixedSources, _, err := controlReadyCacheSources(mixedPath, mixedPath, mixedCfg)
	if err != nil || len(mixedSources) != 2 {
		t.Fatalf("mixed-store gate sources = %d, err=%v; want two after the single-store no-op assertions", len(mixedSources), err)
	}
}

func countID(rows []beads.Bead, id string) int {
	count := 0
	for _, row := range rows {
		if row.ID == id {
			count++
		}
	}
	return count
}
