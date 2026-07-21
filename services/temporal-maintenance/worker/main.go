// Command worker runs the maintenance worker against the self-hosted Temporal
// server (the temporal-server.service systemd --user unit, or a local
// `temporal server start-dev`).
//
// Connection (env): TEMPORAL_ADDRESS (default 127.0.0.1:7233),
// TEMPORAL_NAMESPACE (default "maintenance").
//
// Mode:
//   - default — binds DryRunAdapter: every mutation is RECORDED, never executed
//     (shadow).
//   - TEMPORAL_MAINT_ARMED=1 — binds the persisted at-most-once RealAdapter and a
//     real create+sling selection (the Phase-5 cutover). Requires:
//     GC_CITY_DIR              working dir for gc/gc-sling (e.g. /home/ds/gas-city)
//     TEMPORAL_MAINT_STORE_DIR persisted idempotency store dir
//     TEMPORAL_MAINT_POLECAT   sling target for selection
//     TEMPORAL_MAINT_PROMPT_REVIEW / _AUTHOR  absolute prompt template paths
//     TEMPORAL_MAINT_PRIORITY  bead priority (default "2", matching legacy)
package main

import (
	"log"
	"os"
	"path/filepath"

	tm "github.com/sjarmak/gas-city/services/temporal-maintenance"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("armed worker: %s is required when TEMPORAL_MAINT_ARMED=1", key)
	}
	return v
}

// activities builds the Activities for the configured mode. Default is the
// shadow DryRunAdapter; armed mode binds the persisted RealAdapter + real
// selection dispatch.
func activities() *tm.Activities {
	if os.Getenv("TEMPORAL_MAINT_ARMED") != "1" {
		return &tm.Activities{Adapter: tm.NewDryRunAdapter()}
	}

	cityDir := mustEnv("GC_CITY_DIR")
	storeDir := mustEnv("TEMPORAL_MAINT_STORE_DIR")
	store, err := tm.NewKeyStore(storeDir)
	if err != nil {
		log.Fatalf("armed worker: open store %s: %v", storeDir, err)
	}
	rig := envOr("TEMPORAL_MAINT_RIG", "gascity")
	runner := tm.NewExecRunner(cityDir)
	runner.Rig = rig             // the --rig gc actually targets (not just the label)
	runner.ScratchRoot = cityDir // prompt templates live under the repo

	// A poisoned pending claim (worker SIGKILLed mid-sling) is quarantined and
	// escalated to a durable log so the dropped cycle is loud, not silent. The
	// log sits beside the idempotency store by default; override with
	// TEMPORAL_MAINT_ESCALATIONS_PATH.
	escPath := envOr("TEMPORAL_MAINT_ESCALATIONS_PATH", filepath.Join(storeDir, "escalations.jsonl"))
	escalator, err := tm.NewFileEscalator(escPath)
	if err != nil {
		log.Fatalf("armed worker: open escalation log %s: %v", escPath, err)
	}

	log.Printf("armed worker: RealAdapter bound (store=%s, cityDir=%s, escalations=%s)", storeDir, cityDir, escPath)
	return &tm.Activities{
		Adapter: tm.NewArmedRealAdapter(store, runner, tm.WithEscalator(escalator)),
		Selection: &tm.SelectionConfig{
			Polecat:  mustEnv("TEMPORAL_MAINT_POLECAT"),
			Rig:      rig,
			Priority: envOr("TEMPORAL_MAINT_PRIORITY", "2"),
			PromptFile: map[string]string{
				"review": mustEnv("TEMPORAL_MAINT_PROMPT_REVIEW"),
				"author": mustEnv("TEMPORAL_MAINT_PROMPT_AUTHOR"),
			},
			SlackChannel: os.Getenv("TEMPORAL_MAINT_SLACK_CHANNEL"),
			PLAgent:      os.Getenv("TEMPORAL_MAINT_PL_AGENT"),
		},
	}
}

func main() {
	address := envOr("TEMPORAL_ADDRESS", client.DefaultHostPort)
	namespace := envOr("TEMPORAL_NAMESPACE", "maintenance")

	c, err := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	if err != nil {
		log.Fatalf("dial temporal at %s ns=%s (is temporal-server.service running?): %v", address, namespace, err)
	}
	defer c.Close()

	mode := "shadow (DryRunAdapter)"
	if os.Getenv("TEMPORAL_MAINT_ARMED") == "1" {
		mode = "ARMED (RealAdapter)"
	}

	w := worker.New(c, tm.TaskQueue, worker.Options{})
	w.RegisterWorkflow(tm.MaintenanceCycleWorkflow)
	w.RegisterActivity(activities())

	log.Printf("maintenance worker listening on task queue %q (ns=%s, %s) — %s", tm.TaskQueue, namespace, address, mode)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker exited: %v", err)
	}
}
