package temporalmaintenance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStaging_RealExecRunner_CreatesThrowawayBeadOnce exercises the *real*
// ExecRunner against the live gascity bead store — the "real gc bd create"
// half of the P2 gate — without waking a polecat or touching GitHub. It creates
// one throwaway bead through Propose, proves a duplicate Propose does not create
// a second bead (persisted at-most-once around a real command), and reports the
// bead id so the operator can close it.
//
// It is guarded behind TEMPORAL_MAINT_STAGING=1 so it never runs in normal CI:
// it mutates the real bead store. Run from /home/ds/gas-city:
//
//	TEMPORAL_MAINT_STAGING=1 GC_CITY_DIR=/home/ds/gas-city \
//	  go test -run TestStaging_RealExecRunner -v ./...
func TestStaging_RealExecRunner_CreatesThrowawayBeadOnce(t *testing.T) {
	if os.Getenv("TEMPORAL_MAINT_STAGING") != "1" {
		t.Skip("staging test: set TEMPORAL_MAINT_STAGING=1 (mutates the live bead store) to run")
	}
	cityDir := os.Getenv("GC_CITY_DIR")
	if cityDir == "" {
		cityDir = "/home/ds/gas-city"
	}

	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("staging throwaway bead for temporal-maintenance P2 — safe to close.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The runner is authoritative on argv; register a throwaway create action in
	// its vocabulary so the real `gc bd create` runs through the approved path.
	runner := NewExecRunner(cityDir)
	runner.Gated = map[string]gatedAction{
		"gc bd create (staging)": {
			build: func(string) []string {
				return []string{
					"gc", "bd", "--rig", "gascity", "create",
					"[staging] temporal-maintenance P2 throwaway — safe to close",
					"--priority", "3",
					"--label", "staging-temporal,rig:gascity",
					"--body-file", body,
				}
			},
			validTarget: func(string) bool { return true },
		},
	}
	adapter := NewArmedRealAdapter(store, runner)

	// The staging/* cycle key keeps the idempotency key namespaced.
	key := idempotencyKey("gastownhall-gascity", "staging-p2", "cycle", "gc bd create", "throwaway")
	m := ProposedMutation{IdempotencyKey: key, Action: "gc bd create (staging)"}

	rec1, created1, err := adapter.Propose(context.Background(), m)
	if err != nil || !created1 {
		t.Fatalf("first Propose = (created=%v, err=%v), want created=true", created1, err)
	}
	stored, _, _ := store.Load(key)
	bead := beadIDRe.FindString(stored.ResultRef)
	t.Logf("created throwaway bead %s (close with: gc bd --rig gascity close %s)", bead, bead)
	if bead == "" {
		t.Fatalf("no bead id in create output: %q", stored.ResultRef)
	}

	// Duplicate Propose must NOT create a second bead.
	_, created2, err := adapter.Propose(context.Background(), m)
	if err != nil {
		t.Fatalf("second Propose err = %v", err)
	}
	if created2 {
		t.Fatalf("duplicate Propose created a second bead — at-most-once broken around a real command")
	}
	_ = rec1
}
