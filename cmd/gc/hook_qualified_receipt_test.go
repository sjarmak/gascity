package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/storeref"
)

func TestClaimHookWorkCarriesTheWinningStoreRefIntoTheReceipt(t *testing.T) {
	stores := []hookStore{
		{dir: "city", env: []string{"GC_STORE=city", "GC_SESSION_ID=mc-sess1", "GC_SESSION_NAME=gc__role-mc-sess1"}, storeRef: "city:test-city"},
		{dir: "rig-alpha", env: []string{"GC_STORE=rig-alpha", "GC_SESSION_ID=mc-sess1", "GC_SESSION_NAME=gc__role-mc-sess1"}, storeRef: "rig:alpha"},
	}
	run := func(_, dir string, _ []string) (string, error) {
		if dir == "rig-alpha" {
			return `[{"id":"hw-rig","status":"open","priority":0,"metadata":{"gc.routed_to":"worker"}}]`, nil
		}
		return `[{"id":"hw-city","status":"open","priority":2,"metadata":{"gc.routed_to":"worker"}}]`, nil
	}
	var gotSession string
	var gotReceipt session.CurrentClaimReceipt
	ops := poolClaimOps("", map[string]string{"gc.routed_to": "worker"}, "bd-hw", &stampMetaSpy{})
	ops.StampSessionClaimReceipt = func(sessionID string, receipt session.CurrentClaimReceipt) error {
		gotSession, gotReceipt = sessionID, receipt
		return nil
	}
	opts := poolClaimOpts()

	var stdout, stderr bytes.Buffer
	code := claimHookWorkWithRunner("bd ready --json", "city", stores[0].env, stores, opts, ops, run, func(string, error) {}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claimHookWorkWithRunner = %d, want 0; stderr=%s", code, stderr.String())
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("claim result: %v\n%s", err, stdout.String())
	}
	if result.BeadID != "hw-rig" {
		t.Fatalf("winning bead = %q, want hw-rig", result.BeadID)
	}
	want := session.CurrentClaimReceipt{BeadID: "hw-rig", StoreRef: "rig:alpha"}
	if gotSession != hookCurrentSessionID || gotReceipt != want {
		t.Fatalf("receipt = (%q, %#v), want (%q, %#v)", gotSession, gotReceipt, hookCurrentSessionID, want)
	}
}

func TestHookCurrentProductionFrontDoorSelectsExactRigFileStore(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS", "file")
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "rig-alpha")
	darkRigPath := filepath.Join(cityPath, "rig-dark")
	for _, path := range []string{rigPath, darkRigPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureScopedFileStoreLayout(cityPath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cityPath, rigPath} {
		if err := ensurePersistedScopeLocalFileStore(path); err != nil {
			t.Fatal(err)
		}
	}
	// A configured but unreadable unrelated leg: opening it would fail ENOTDIR.
	if err := os.WriteFile(filepath.Join(darkRigPath, ".gc"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cityTOML := fmt.Sprintf(`[workspace]
name = "test-city"
prefix = "gc"

[beads]
provider = "file"

[[rigs]]
name = "alpha"
path = %q

[[rigs]]
name = "dark"
path = %q
`, rigPath, darkRigPath)
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cityStore, err := openScopeLocalFileStore(cityPath)
	if err != nil {
		t.Fatal(err)
	}
	rigStore, err := openScopeLocalFileStore(rigPath)
	if err != nil {
		t.Fatal(err)
	}
	sessionBead := mustCreateHookCurrentBead(t, cityStore, beads.Bead{
		Title: "session", Type: session.BeadType, Labels: []string{session.LabelSession},
		Metadata: map[string]string{"state": string(session.StateActive)},
	})
	cityRoot := mustCreateHookRoot(t, cityStore, "rig:alpha", "must-not-win")
	cityStep := mustCreateHookStep(t, cityStore, cityRoot.ID, "rig:alpha", sessionBead.ID)
	// Consume the rig store's first ID so its root and step collide exactly with
	// the city copies despite the city session bead occupying that slot.
	mustCreateHookCurrentBead(t, rigStore, beads.Bead{Title: "sequence spacer", Type: "task"})
	rigRoot := mustCreateHookRoot(t, rigStore, "rig:alpha", "printf 'selected rig\\n'")
	rigStep := mustCreateHookStep(t, rigStore, rigRoot.ID, "rig:alpha", sessionBead.ID)
	if cityRoot.ID != rigRoot.ID || cityStep.ID != rigStep.ID {
		t.Fatalf("fixture IDs are not colliding: city=(%s,%s) rig=(%s,%s)", cityRoot.ID, cityStep.ID, rigRoot.ID, rigStep.ID)
	}
	sessFront := session.NewStore(beads.SessionStore{Store: cityStore})
	if _, err := sessFront.SetCurrentClaimReceipt(sessionBead.ID, session.CurrentClaimReceipt{BeadID: rigStep.ID, StoreRef: "rig:alpha"}); err != nil {
		t.Fatalf("stamping rig receipt: %v", err)
	}

	useProductionHookCurrentSeams(t)
	t.Setenv("GC_CITY_PATH", cityPath)
	t.Setenv("GC_SESSION_ID", sessionBead.ID)
	got := runHookCurrentRootVar(t)
	if got != "printf 'selected rig\\n'" {
		t.Fatalf("decoded test_command = %q, want selected rig bytes; city same-ID copy won", got)
	}
}

func TestHookCurrentProductionFrontDoorResolvesRelocatedSQLiteClass(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	e := newSplitEnvWithRig(t, true)
	if err := ensureScopedFileStoreLayout(e.cityPath); err != nil {
		t.Fatal(err)
	}
	if err := ensurePersistedScopeLocalFileStore(e.cityPath); err != nil {
		t.Fatal(err)
	}
	seedCLIStorageRoutes(t, e.cityPath, e.routes)
	classRef := string(storeref.ClassRef(wholeSplitClasses()))
	claimRoute, err := hookClaimClassRouteForCity(e.cityPath)
	if err != nil {
		t.Fatalf("resolving production class claim route: %v", err)
	}
	if claimRoute == nil {
		t.Fatal("production class claim route is nil")
	}
	if claimRoute.storeRef != classRef {
		t.Fatalf("class claim route ref = %q, want stable logical %q", claimRoute.storeRef, classRef)
	}
	sessionBead := mustCreateHookCurrentBead(t, e.class, beads.Bead{
		Title: "session", Type: session.BeadType, Labels: []string{session.LabelSession},
		Metadata: map[string]string{"state": string(session.StateActive)},
	})
	// The qualified claim ref identifies the relocated class leg. The workflow
	// root ref remains its logical city scope; neither is a physical endpoint.
	rootRef := "city:" + e.cfg.Workspace.Name
	root := mustCreateHookRoot(t, e.class, rootRef, "printf 'selected class\\n'")
	step := mustCreateHookStep(t, e.class, root.ID, rootRef, sessionBead.ID)
	sessFront := session.NewStore(beads.SessionStore{Store: e.class})
	if _, err := sessFront.SetCurrentClaimReceipt(sessionBead.ID, session.CurrentClaimReceipt{BeadID: step.ID, StoreRef: classRef}); err != nil {
		t.Fatalf("stamping class receipt: %v", err)
	}

	useProductionHookCurrentSeams(t)
	t.Setenv("GC_CITY_PATH", e.cityPath)
	t.Setenv("GC_SESSION_ID", sessionBead.ID)
	got := runHookCurrentRootVar(t)
	if got != "printf 'selected class\\n'" {
		t.Fatalf("decoded test_command = %q, want relocated SQLite class bytes", got)
	}
}

func useProductionHookCurrentSeams(t *testing.T) {
	t.Helper()
	oldFront, oldResolver := hookCurrentSessionFrontDoor, hookCurrentClaimStore
	hookCurrentSessionFrontDoor = sessionCurrentClaimFrontDoor
	hookCurrentClaimStore = resolveHookCurrentClaimStore
	t.Cleanup(func() {
		hookCurrentSessionFrontDoor = oldFront
		hookCurrentClaimStore = oldResolver
	})
}

func runHookCurrentRootVar(t *testing.T) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmdHookCurrentWithOptions(hookCurrentOptions{JSON: true, RootVar: "test_command"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc hook current --root-var=test_command --json = %d; stderr=%s", code, stderr.String())
	}
	var result hookCurrentRootVarJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decoding hook current result: %v\n%s", err, stdout.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(result.RootVar.Value)
	if err != nil {
		t.Fatalf("decoding root variable: %v", err)
	}
	return string(decoded)
}

func mustCreateHookRoot(t *testing.T, store beads.Store, rootRef, command string) beads.Bead {
	t.Helper()
	return mustCreateHookCurrentBead(t, store, beads.Bead{
		Title: "workflow root", Type: "epic",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
			beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
			beadmeta.RootStoreRefMetadataKey:    rootRef,
			beadmeta.RuntimeVarsMetadataKey:     `{"test_command":` + string(mustJSON(t, command)) + `}`,
		},
	})
}

func mustCreateHookStep(t *testing.T, store beads.Store, rootID, rootRef, sessionID string) beads.Bead {
	t.Helper()
	step := mustCreateHookCurrentBead(t, store, beads.Bead{
		Title: "test step", Type: "task",
		Metadata: map[string]string{
			beadmeta.SessionIDMetadataKey:    sessionID,
			beadmeta.RootBeadIDMetadataKey:   rootID,
			beadmeta.RootStoreRefMetadataKey: rootRef,
		},
	})
	status, assignee := "in_progress", sessionID
	if err := store.Update(step.ID, beads.UpdateOpts{Status: &status, Assignee: &assignee}); err != nil {
		t.Fatalf("claiming hook-current step %s: %v", step.ID, err)
	}
	step.Status, step.Assignee = status, assignee
	return step
}

func mustCreateHookCurrentBead(t *testing.T, store beads.Store, bead beads.Bead) beads.Bead {
	t.Helper()
	created, err := store.Create(bead)
	if err != nil {
		t.Fatalf("creating %s: %v", bead.Title, err)
	}
	return created
}
