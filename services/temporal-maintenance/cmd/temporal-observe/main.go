// Command temporal-observe is the gc-4zf.7 OBSERVE-mode one-shot: it reads the
// [durable] config block, connects to Temporal READ-ONLY, computes the
// observe-bridge metrics against the live workflow history and bead store, and
// appends one JSONL record to the metrics file. It mutates nothing anywhere.
//
// Fail-soft contract (mirrors bin/temporal-maintenance-signal-bead-closed): an
// unreachable Temporal server or a failed read pass logs and exits 0, so a
// down server can never wedge the order system. A CONFIG error exits non-zero:
// mode=execute is gated behind the memory gate (bead gc-qaid) and must fail
// loud, not soft.
//
// Usage:
//
//	temporal-observe [--config /home/ds/gas-city/city.toml] \
//	  [--address 127.0.0.1:7233] [--namespace maintenance] [--rig gascity] \
//	  [--window 72h] [--out /home/ds/gas-city/.gc/temporal-observe-metrics.jsonl]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	tm "github.com/sjarmak/gas-city/services/temporal-maintenance"

	"go.temporal.io/sdk/client"
)

func main() {
	var (
		cityDir   = flag.String("city", envOr("GC_STORE_ROOT", "/home/ds/gas-city"), "city root (gc working dir, default paths)")
		config    = flag.String("config", "", "TOML file carrying the optional [durable] block (default <city>/city.toml)")
		address   = flag.String("address", envOr("TEMPORAL_ADDRESS", client.DefaultHostPort), "Temporal frontend host:port")
		ns        = flag.String("namespace", envOr("TEMPORAL_NAMESPACE", "maintenance"), "Temporal namespace")
		rig       = flag.String("rig", envOr("TEMPORAL_MAINT_RIG", "gascity"), "beads rig to read")
		window    = flag.Duration("window", tm.ObserveWindowDefault, "how far back to scan")
		out       = flag.String("out", "", "metrics JSONL path (default <city>/.gc/temporal-observe-metrics.jsonl)")
		sigState  = flag.String("signal-state", "", "disabled bridge's dedup state file (default <city>/.gc/temporal-maintenance-signal-state.json)")
		timeoutFl = flag.Duration("timeout", 60*time.Second, "overall run deadline")
	)
	flag.Parse()

	if *config == "" {
		*config = filepath.Join(*cityDir, "city.toml")
	}
	if *out == "" {
		*out = filepath.Join(*cityDir, ".gc", "temporal-observe-metrics.jsonl")
	}
	if *sigState == "" {
		*sigState = filepath.Join(*cityDir, ".gc", "temporal-maintenance-signal-state.json")
	}

	// Config errors are LOUD: mode=execute (or a typo'd block) must stop the
	// run, never degrade into an unconfigured observe pass.
	cfg, err := tm.LoadDurableConfig(*config)
	if err != nil {
		log.Fatalf("temporal-observe: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFl)
	defer cancel()

	// Fail-soft from here down: reads against a live server and store.
	c, err := client.Dial(client.Options{HostPort: *address, Namespace: *ns})
	if err != nil {
		log.Printf("temporal-observe: temporal unreachable at %s ns=%s (fail-soft, exit 0): %v", *address, *ns, err)
		return
	}
	defer c.Close()

	// A corrupt dedup state file must not zero out metric 5: the error rides
	// the record (state_file_error + incomplete) instead of vanishing.
	signalled, sigErr := tm.LoadSignalledState(*sigState)
	if sigErr != nil {
		log.Printf("temporal-observe: %v (metric 5 will be marked incomplete)", sigErr)
	}

	bridge := &tm.ObserveBridge{
		Executions:        &tm.TemporalExecutionLister{Client: c, Namespace: *ns},
		States:            tm.NewClientRunStateReader(c),
		Beads:             tm.NewGCBeadReader(*cityDir, *rig),
		Window:            *window,
		AlreadySignalled:  signalled,
		SignalledStateErr: sigErr,
	}
	rec, err := bridge.Observe(ctx)
	if err != nil {
		log.Printf("temporal-observe: observe pass failed (fail-soft, exit 0): %v", err)
		return
	}

	if err := appendRecord(*out, rec, cfg); err != nil {
		log.Printf("temporal-observe: write %s (fail-soft, exit 0): %v", *out, err)
		return
	}
	fmt.Printf("temporal-observe: recorded window=%.0fh executions=%d tagged_beads=%d waiting_workflows=%d -> %s\n",
		rec.WindowHours, rec.HistoryGrowth.Executions, rec.BeadsWithTemporalMetadata,
		rec.WorkflowsInWaitingPhase, *out)
}

// observeSchemaVersion identifies the JSONL record layout. Bump it whenever
// the envelope or metric semantics change so gate-time consumers can tell
// layouts apart instead of misreading old records.
const observeSchemaVersion = 1

// observeLine is the JSONL envelope: the schema version, the metrics record,
// plus the [durable] flags in force when it was taken, so a later regime
// change is attributable.
type observeLine struct {
	Schema int `json:"schema"`
	tm.ObserveRecord
	DurableEnabled bool   `json:"durable_enabled"`
	DurableMode    string `json:"durable_mode"`
}

func appendRecord(path string, rec tm.ObserveRecord, cfg tm.DurableConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(observeLine{Schema: observeSchemaVersion, ObserveRecord: rec, DurableEnabled: cfg.Enabled, DurableMode: cfg.Mode})
	if err != nil {
		return err
	}
	// Torn-tail self-heal: a previous append cut short (ENOSPC, interrupt)
	// leaves the file ending mid-line; opening this write with a newline turns
	// that fragment into one invalid line instead of fusing it into this
	// record. Single writer (one-shot binary under the order floor), so the
	// check-then-append gap is not racy.
	torn, err := tornTail(path)
	if err != nil {
		return err
	}
	if torn {
		line = append([]byte{'\n'}, line...)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// tornTail reports whether path ends mid-line (last byte not '\n') — the
// signature of a torn previous append. An absent or empty file is a clean
// tail.
func tornTail(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() == 0 {
		return false, nil
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, st.Size()-1); err != nil {
		return false, err
	}
	return buf[0] != '\n', nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
