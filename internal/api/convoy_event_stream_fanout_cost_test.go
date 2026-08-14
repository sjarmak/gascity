package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// This file measures the thing the fix exists to change: how many `bd`
// SUBPROCESSES one projected bead event costs, on a city shaped like the real
// one. It drives real BdStores (the type that actually forks) through a runner
// that answers and counts, so the number here is forks, not an abstraction of
// forks.
//
// It is a test, not a benchmark, because the count is a contract: a regression
// that reintroduces the fan-out is a correctness problem for the bead store's
// connection budget, and it must fail CI rather than show up as a slower graph.

const fanoutCostRigCount = 21

// forkCountingRunner stands in for exec'ing bd. It records every argv with the
// directory it would have run in, so the test can count subprocesses per store.
type forkCountingRunner struct {
	mu sync.Mutex
	// beadsByDir[dir][id] is the durable-tier row that store would return.
	beadsByDir map[string]map[string]beads.Bead
	forks      []string
}

func (r *forkCountingRunner) run(dir, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.forks = append(r.forks, dir+"\x00"+name+" "+strings.Join(args, " "))
	r.mu.Unlock()

	if name != "bd" || len(args) < 3 {
		return nil, errors.New("issue not found")
	}
	switch args[0] {
	case "show":
		bead, ok := r.beadsByDir[dir][args[2]]
		if !ok {
			return nil, errors.New("issue not found")
		}
		raw, err := json.Marshal([]map[string]any{{
			"id":       bead.ID,
			"title":    bead.Title,
			"status":   "open",
			"metadata": bead.Metadata,
		}})
		if err != nil {
			return nil, err
		}
		return raw, nil
	case "query":
		// Wisps tier: nothing in this fixture is ephemeral, so every wisp
		// query is a miss — which is exactly the shape of the live fleet's
		// cross-store probes.
		return []byte("[]"), nil
	}
	return nil, errors.New("issue not found")
}

func (r *forkCountingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.forks)
}

func (r *forkCountingRunner) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forks = nil
}

// breakdown summarizes forks by subcommand, for the report.
func (r *forkCountingRunner) breakdown() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, f := range r.forks {
		argv := f[strings.IndexByte(f, 0)+1:]
		fields := strings.Fields(argv)
		if len(fields) >= 2 {
			out[fields[0]+" "+fields[1]]++
		}
	}
	return out
}

// fanoutCostCity is a 22-store city (1 city store + 21 rig stores), matching
// the live fleet's shape, with every store a real BdStore carrying its
// configured ID prefix.
type fanoutCostCity struct {
	state  *fakeState
	runner *forkCountingRunner
	// step is a workflow step bead living in exactly one rig store.
	step beads.Bead
}

func newFanoutCostCity(t *testing.T) *fanoutCostCity {
	t.Helper()
	root := t.TempDir()
	runner := &forkCountingRunner{beadsByDir: map[string]map[string]beads.Bead{}}

	state := newFakeState(t)
	state.cityName = "testcity"
	state.stores = map[string]beads.Store{}

	cityDir := filepath.Join(root, "city")
	runner.beadsByDir[cityDir] = map[string]beads.Bead{}
	state.cityBeadStore = beads.NewBdStoreWithPrefix(cityDir, runner.run, "tc")

	cfg := &config.City{Workspace: config.Workspace{Name: "testcity", Prefix: "tc"}}
	for i := range fanoutCostRigCount {
		name := fmt.Sprintf("rig%02d", i)
		prefix := fmt.Sprintf("r%02d", i)
		dir := filepath.Join(root, name)
		runner.beadsByDir[dir] = map[string]beads.Bead{}
		cfg.Rigs = append(cfg.Rigs, config.Rig{Name: name, Path: dir, Prefix: prefix})
		state.stores[name] = beads.NewBdStoreWithPrefix(dir, runner.run, prefix)
	}
	state.cfg = cfg

	// Seed one workflow (root + step) into the LAST rig, so a first-hit
	// short-circuit could not flatter the number.
	owner := cfg.Rigs[len(cfg.Rigs)-1]
	ownerDir, ownerPrefix := owner.Path, owner.Prefix
	rootBead := beads.Bead{
		ID:    ownerPrefix + "-root",
		Title: "Workflow",
		Metadata: map[string]string{
			"gc.kind":           "workflow",
			"gc.workflow_id":    "wf-cost",
			"gc.scope_kind":     "rig",
			"gc.scope_ref":      owner.Name,
			"gc.root_store_ref": "rig:" + owner.Name,
		},
	}
	step := beads.Bead{
		ID:    ownerPrefix + "-step",
		Title: "Step",
		Metadata: map[string]string{
			"gc.root_bead_id":    rootBead.ID,
			"gc.root_store_ref":  "rig:" + owner.Name,
			"gc.logical_bead_id": "node-1",
		},
	}
	runner.beadsByDir[ownerDir][rootBead.ID] = rootBead
	runner.beadsByDir[ownerDir][step.ID] = step

	return &fanoutCostCity{state: state, runner: runner, step: step}
}

// TestProjectWorkflowEventForkCostIsBoundedToTheOwningStore is the budget gate.
//
// Before the scoping fix this cost 2 forks per store: `bd show` missed, then a
// supplemental `bd query` missed, across all 22 stores — 43 subprocesses to
// answer a question only one store could answer. Measured on the live fleet
// (2026-08-11), that pattern was ~86% of all bd spawns and the dominant source
// of the store's ~150 connections/sec arrival rate.
//
// The budget below is deliberately a hard upper bound and not an exact equality:
// it must fail on a regression to fan-out (which multiplies by 22) while not
// churning on an unrelated extra read of the owning store.
func TestProjectWorkflowEventForkCostIsBoundedToTheOwningStore(t *testing.T) {
	c := newFanoutCostCity(t)

	payload := nonWorkflowPayload(t, c.step.ID)
	projection := projectWorkflowEvent(c.state, events.Event{
		Type:    events.BeadUpdated,
		Seq:     1,
		Ts:      time.Unix(1711300000, 0).UTC(),
		Subject: c.step.ID,
		Payload: payload,
	})
	if projection == nil {
		t.Fatal("projection = nil; the event must still resolve")
	}
	if projection.Bead.ID != c.step.ID {
		t.Fatalf("bead.id = %q, want %q", projection.Bead.ID, c.step.ID)
	}

	const budget = 4
	got := c.runner.count()
	t.Logf("bd forks for one projected event across %d stores: %d (breakdown: %v)",
		fanoutCostRigCount+1, got, c.runner.breakdown())
	if got > budget {
		t.Fatalf("bd forks = %d, want <= %d; a lookup that costs more than the owning store's own reads means the store scan is fanning out again (breakdown: %v)",
			got, budget, c.runner.breakdown())
	}
}

// TestProjectWorkflowEventForkCostDoesNotScaleWithRigCount is the structural
// version of the same claim, and the one that cannot be satisfied by tuning a
// constant: the cost of resolving an event must be INDEPENDENT of how many rigs
// the city has. It compares a 22-store city against a 2-store one.
func TestProjectWorkflowEventForkCostDoesNotScaleWithRigCount(t *testing.T) {
	big := newFanoutCostCity(t)
	ev := func(c *fanoutCostCity) events.Event {
		return events.Event{
			Type:    events.BeadUpdated,
			Seq:     1,
			Ts:      time.Unix(1711300000, 0).UTC(),
			Subject: c.step.ID,
			Payload: nonWorkflowPayload(t, c.step.ID),
		}
	}
	if projectWorkflowEvent(big.state, ev(big)) == nil {
		t.Fatal("22-store projection = nil")
	}
	bigForks := big.runner.count()

	// Same city, trimmed to the city store plus the one rig that owns the bead.
	small := newFanoutCostCity(t)
	owner := small.state.cfg.Rigs[len(small.state.cfg.Rigs)-1]
	small.state.cfg = &config.City{
		Workspace: config.Workspace{Name: "testcity", Prefix: "tc"},
		Rigs:      []config.Rig{owner},
	}
	small.state.stores = map[string]beads.Store{owner.Name: small.state.stores[owner.Name]}
	small.runner.reset()
	if projectWorkflowEvent(small.state, ev(small)) == nil {
		t.Fatal("2-store projection = nil")
	}
	smallForks := small.runner.count()

	t.Logf("bd forks: 22-store city = %d, 2-store city = %d", bigForks, smallForks)
	if bigForks != smallForks {
		t.Fatalf("bd forks = %d on a 22-store city vs %d on a 2-store city; resolution cost must not scale with rig count",
			bigForks, smallForks)
	}
}
