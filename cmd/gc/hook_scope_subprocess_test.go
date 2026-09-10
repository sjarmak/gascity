package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// Use a real child process as the bd protocol fixture, without replacing the
// claim runner. Its independent log proves the mutation and projection reads
// used the selected leg rather than cwd or ambient BEADS_DIR. The city journal
// then proves the scope survived the ordinary emission path.
func TestOrdinaryClaimScopeMatchesSubprocessAndJournal(t *testing.T) {
	city := oneShotCLICity(t, "")
	rig := t.TempDir()
	selected := filepath.Join(rig, ".beads")
	logPath := filepath.Join(t.TempDir(), "reads")
	bin := filepath.Join(t.TempDir(), "bd")
	script := fmt.Sprintf(`#!/bin/sh
test "$BEADS_DIR" = %q || exit 71
printf '%%s|%%s\n' "$BEADS_DIR" "$*" >> %q
case "$*" in
  'update step-1 --claim --json'|'show --json step-1')
    printf '%%s\n' '[{"id":"step-1","status":"in_progress","assignee":"worker","metadata":{"gc.root_bead_id":"root-1","gc.step_id":"build","gc.session_id":"session-1"}}]' ;;
  'show --json root-1')
    printf '%%s\n' '[{"id":"root-1","metadata":{"gc.kind":"workflow","gc.formula_contract":"graph.v2"}}]' ;;
  *) exit 72 ;;
esac
`, selected, logPath)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", filepath.Join(t.TempDir(), "wrong-store"))
	cfg := &config.City{Rigs: []config.Rig{{Name: "selected", Path: rig}}}
	a := &config.Agent{Name: "worker", Dir: "selected"}
	constructed := map[string]string{
		"BEADS_DIR": selected, "BD_BIN": bin,
		"GC_STORE_ROOT": rig, "GC_STORE_SCOPE": "rig", "GC_RIG": "selected", "GC_RIG_ROOT": rig,
	}
	env := mergeRuntimeEnv(nil, constructed)
	leg := hookWorkQueryStores(city, cfg, a, "worker", city, env, constructed)[0]
	if leg.storeRef != "rig:selected" {
		t.Fatalf("selected scope = %q", leg.storeRef)
	}
	step, claimed, err := hookClaimWithBdStore(context.Background(), leg.dir, leg.env, "step-1", "worker")
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	hookEmitExecutionStepStarted(step, leg.dir, leg.env, "worker", leg.storeRef)
	var started []events.Event
	for _, event := range readCityJournal(t, city) {
		if event.Type == events.ExecutionStepStarted {
			started = append(started, event)
		}
	}
	if len(started) != 1 || started[0].SubjectStoreRef != "rig:selected" || started[0].RunStoreRef != "rig:selected" {
		t.Fatalf("started events = %#v", started)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 4 {
		t.Fatalf("subprocess calls = %q, want claim, canonical readback, step and root reads", log)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, selected+"|") {
			t.Fatalf("subprocess escaped selected store: %q", line)
		}
	}
}
