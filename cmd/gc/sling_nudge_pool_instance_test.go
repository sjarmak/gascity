package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type poolWakeFailOnceStore struct {
	beads.Store
	failed   bool
	attempts int
}

type poolWakeRejectingStore struct{ beads.Store }

func TestPoolWakePartialErrorPreservesCause(t *testing.T) {
	cause := errors.New("enqueue rejected")
	if err := (&partialSlingWakeError{cause: cause}); !errors.Is(err, cause) {
		t.Fatalf("partial delivery error lost its cause: %v", err)
	}
}

func (s *poolWakeRejectingStore) Create(b beads.Bead) (beads.Bead, error) {
	if b.Type == "chore" {
		return beads.Bead{}, errors.New("permanent enqueue refusal")
	}
	return s.Store.Create(b)
}

// Promoted from the independent review's post-route mixed-acceptance probe.
func TestPoolWakePartialAcceptanceJSON(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	acceptedID := seedPoolWakeSession(t, primary)
	rejectedID := seedPoolWakeSession(t, city)
	if err := city.SetMetadata(rejectedID, "continuation_epoch", "2"); err != nil {
		t.Fatal(err)
	}
	cfg, path, pokes := poolWakeFixture(t, &poolWakeRejectingStore{Store: city})
	work, err := primary.Create(beads.Bead{Title: "partial wake", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath, deps.Store, deps.StoreRef = path, primary, "rig:gascity"
	cfg.Rigs = []config.Rig{{Name: "gascity", Path: "gascity", Prefix: "gc"}}
	opts := testOpts(cfg.Agents[0], work.ID)
	opts.NoFormula, opts.Nudge, opts.Force, opts.SkipPoke = true, true, true, true
	var out, human, stderr bytes.Buffer
	code := doSlingBatchWithJSON(opts, deps, primary, true, &human, &out, &stderr)
	var result slingJSONResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	pending := pendingPoolWakes(t, path)
	if len(pending) != 1 || pending[0].SessionID != acceptedID || pending[0].ContinuationEpoch != "" {
		t.Fatalf("accepted fenced Pending=%+v", pending)
	}
	if code == 0 || result.Success || !result.Routed || !result.Queued {
		t.Fatalf("partial acceptance misreported: code=%d result=%+v pending=%+v", code, result, pending)
	}
	if !strings.Contains(stderr.String(), "partially queued") || strings.Contains(human.String(), "Nudged ") || *pokes != 1 {
		t.Fatalf("stdout=%s stderr=%s pokes=%d", human.String(), stderr.String(), *pokes)
	}
	if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
		if len(s.InFlight) != 0 || len(s.Dead) != 1 {
			t.Fatalf("queue=%+v", s)
		}
		for _, item := range s.Pending {
			if item.ContinuationEpoch == "2" {
				t.Fatalf("rejected fence accepted: %+v", item)
			}
		}
		if s.Dead[0].Reference == nil || s.Dead[0].Reference.ID != work.ID || !strings.Contains(s.Dead[0].LastError, "partially queued") {
			t.Fatalf("failure=%+v", s.Dead[0])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readback, err := primary.Get(work.ID)
	if err != nil || readback.Assignee != "" || readback.Metadata["gc.routed_to"] != cfg.Agents[0].QualifiedName() {
		t.Fatalf("source=%+v err=%v", readback, err)
	}
}

func TestPoolWakeMultipleFailuresHaveStableDisposition(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	seedPoolWakeSession(t, primary)
	first := seedPoolWakeSession(t, city)
	if err := city.SetMetadata(first, "continuation_epoch", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := city.Create(beads.Bead{Title: "gascity/gastown.rictus", Type: sessionBeadType, Labels: []string{sessionBeadLabel}, Metadata: map[string]string{
		"template": "gascity/gastown.polecat", "session_name": "second-session", "pool_slot": "2", "continuation_epoch": "3",
	}}); err != nil {
		t.Fatal(err)
	}
	cfg, path, _ := poolWakeFixture(t, &poolWakeRejectingStore{Store: city})
	work, err := primary.Create(beads.Bead{Title: "stable partial wake", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath, deps.Store, deps.StoreRef = path, primary, "rig:gascity"
	cfg.Rigs = []config.Rig{{Name: "gascity", Path: "gascity", Prefix: "gc"}}
	opts := testOpts(cfg.Agents[0], work.ID)
	opts.NoFormula, opts.Nudge, opts.Force, opts.SkipPoke, opts.NoConvoy = true, true, true, true, true
	// Multiple map traversals expose order-dependent receipt identity. The
	// corrected structural order must stay fixed on every iteration.
	for attempt := range 64 {
		var out, human, stderr bytes.Buffer
		code := doSlingBatchWithJSON(opts, deps, primary, true, &human, &out, &stderr)
		var result slingJSONResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if code == 0 || result.Success || !result.Queued || !result.Routed {
			t.Fatalf("attempt=%d result=%+v code=%d", attempt, result, code)
		}
		if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
			if len(s.Dead) != 1 {
				t.Fatalf("attempt=%d duplicate failure dispositions=%+v", attempt, s.Dead)
			}
			if !strings.Contains(s.Dead[0].LastError, "furiosa") || !strings.Contains(s.Dead[0].LastError, "rictus") {
				t.Fatalf("missing failed fence: %+v", s.Dead[0])
			}
			for _, item := range s.Pending {
				if item.ContinuationEpoch == "2" || item.ContinuationEpoch == "3" {
					t.Fatalf("failed epoch accepted: %+v", item)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *poolWakeFailOnceStore) Create(b beads.Bead) (beads.Bead, error) {
	if b.Type == "chore" {
		s.attempts++
		if !s.failed {
			s.failed = true
			return beads.Bead{}, errors.New("enqueue unavailable")
		}
	}
	return s.Store.Create(b)
}

func poolWakeFixture(t *testing.T, store beads.Store) (*config.City, string, *int) {
	t.Helper()
	a := namepoolAgentForNudgeTest()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}, Agents: []config.Agent{a}}
	cityPath := t.TempDir()
	writeSlingTestCity(t, cityPath, "[workspace]\nname = \"test-city\"\n")
	prevOpen, prevPoke, prevPoller := slingOpenCityStore, slingPokeController, startNudgePoller
	slingOpenCityStore = func(string) (beads.Store, error) { return store, nil }
	pokes := 0
	slingPokeController = func(string) error { pokes++; return nil }
	startNudgePoller = func(string, string, string) error { return nil }
	t.Cleanup(func() { slingOpenCityStore = prevOpen; slingPokeController = prevPoke; startNudgePoller = prevPoller })
	return cfg, cityPath, &pokes
}

func seedPoolWakeSession(t *testing.T, store beads.Store) string {
	t.Helper()
	b, err := store.Create(beads.Bead{Title: "gascity/gastown.furiosa", Type: sessionBeadType, Labels: []string{sessionBeadLabel}, Metadata: map[string]string{
		"template": "gascity/gastown.polecat", "session_name": "pool-session", "pool_slot": "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func pendingPoolWakes(t *testing.T, path string) []queuedNudge {
	t.Helper()
	var pending []queuedNudge
	if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
		for _, item := range s.Pending {
			if item.SessionID != "" {
				pending = append(pending, item)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestPoolWakeRetriesAfterFailedEnqueue(t *testing.T) {
	base := beads.NewMemStore()
	seedPoolWakeSession(t, base)
	store := &poolWakeFailOnceStore{Store: base}
	cfg, path, pokes := poolWakeFixture(t, store)
	var out, errOut bytes.Buffer
	if err := doSlingNudge(&cfg.Agents[0], "test-city", path, cfg, runtime.NewFake(), store, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := len(pendingPoolWakes(t, path)); got != 1 {
		t.Fatalf("pending=%d want1 after failed first enqueue; attempts=%d stderr=%s", got, store.attempts, errOut.String())
	}
	if *pokes != 1 {
		t.Fatalf("pokes=%d want1", *pokes)
	}
}

func TestPoolWakeRefusesAmbiguousDifferentStores(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	first, second := seedPoolWakeSession(t, primary), seedPoolWakeSession(t, city)
	if first != second {
		t.Fatal("fixture must collide session IDs")
	}
	cfg, path, pokes := poolWakeFixture(t, city)
	var out, errOut bytes.Buffer
	if err := doSlingNudge(&cfg.Agents[0], "test-city", path, cfg, runtime.NewFake(), primary, &out, &errOut); err == nil {
		t.Fatal("ambiguous wake accepted")
	}
	if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
		if len(s.Pending) != 0 || len(s.InFlight) != 0 {
			t.Fatalf("ambiguous queue=%+v", s)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if *pokes != 0 {
		t.Fatalf("pokes=%d want0", *pokes)
	}
}

func TestPoolWakeAmbiguityIsDurableRoutingFailure(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	sessionID := seedPoolWakeSession(t, primary)
	seedPoolWakeSession(t, city)
	work, err := primary.Create(beads.Bead{Title: "assigned work", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, path, pokes := poolWakeFixture(t, city)
	deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath, deps.Store, deps.StoreRef = path, primary, "rig:gascity"
	cfg.Rigs = []config.Rig{{Name: "gascity", Path: "gascity", Prefix: "gc"}}
	opts := testOpts(cfg.Agents[0], work.ID)
	opts.NoFormula, opts.Nudge, opts.Force, opts.SkipPoke = true, true, true, true
	for range 2 {
		var out, human, errOut bytes.Buffer
		code := doSlingBatchWithJSON(opts, deps, primary, true, &human, &out, &errOut)
		var result slingJSONResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("JSON: %v; code=%d stderr=%s", err, code, errOut.String())
		}
		if code == 0 || result.Success || result.Queued || !result.Routed {
			t.Fatalf("code=%d result=%+v stderr=%s", code, result, errOut.String())
		}
	}
	if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
		if len(s.Pending) != 0 || len(s.InFlight) != 0 || len(s.Dead) != 1 {
			t.Fatalf("queue=%+v", s)
		}
		dead := s.Dead[0]
		wantCause := fmt.Sprintf("source StoreRef=%q bead=%q: ambiguous pool wake: rig:gascity/sessions and city:test-city/sessions both own agent=%q session=%q epoch=%q runtime=%q; resolve session-store authority before retry", "rig:gascity", work.ID, "gascity/gastown.furiosa", sessionID, "", "pool-session")
		if dead.Reference == nil || dead.Reference.Kind != "bead" || dead.Reference.ID != work.ID || dead.LastError != wantCause || dead.DeadAt.IsZero() {
			t.Fatalf("failure receipt=%+v", dead)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if *pokes != 0 {
		t.Fatalf("pokes=%d", *pokes)
	}
	readback, err := primary.Get(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readback.Assignee != "" || readback.Metadata["gc.routed_to"] != cfg.Agents[0].QualifiedName() {
		t.Fatalf("source=%+v", readback)
	}
}

func TestPoolWakeProductionSessionLegAliases(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		t.Run(fmt.Sprintf("relocated=%t", relocated), func(t *testing.T) {
			var path string
			var cfg *config.City
			if relocated {
				path, cfg = migratedOneShotCLICity(t)
			} else {
				path = oneShotCLICity(t, "")
				var err error
				cfg, err = loadCityConfigWithoutBuiltinPackRefresh(path, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
			}
			a := namepoolAgentForNudgeTest()
			cfg.Agents = []config.Agent{a}
			primary, err := openCityStoreAt(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = closeBeadStoreHandle(primary) })
			sessions := cliSessionStore(primary, cfg, path)
			seedPoolWakeSession(t, sessions)
			previousOpen, previousPoke := slingOpenCityStore, slingPokeController
			pokes := 0
			slingOpenCityStore = openCityStoreAt
			slingPokeController = func(string) error { pokes++; return nil }
			t.Cleanup(func() { slingOpenCityStore, slingPokeController = previousOpen, previousPoke })
			var out, errOut bytes.Buffer
			if relocated {
				if err := doSlingNudge(&a, "storage-cli-city", path, cfg, runtime.NewFake(), primary, &out, &errOut); err != nil {
					t.Fatal(err)
				}
			} else {
				// Exercise the routing caller's actual resolved source pair, not
				// a target-derived scope hint passed straight to the wake helper.
				work, err := primary.Create(beads.Bead{Title: "city source work", Type: "task"})
				if err != nil {
					t.Fatal(err)
				}
				deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
				deps.CityName, deps.CityPath, deps.Store, deps.StoreRef = "storage-cli-city", path, primary, "city:storage-cli-city"
				opts := testOpts(a, work.ID)
				opts.NoFormula, opts.Nudge, opts.Force, opts.SkipPoke = true, true, true, true
				var human bytes.Buffer
				if code := doSlingBatchWithJSON(opts, deps, primary, true, &human, &out, &errOut); code != 0 {
					t.Fatalf("code=%d stderr=%s", code, errOut.String())
				}
				var result slingJSONResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if !result.Success || !result.Queued || !result.Routed {
					t.Fatalf("result=%+v", result)
				}
			}
			if got := len(pendingPoolWakes(t, path)); got != 1 || pokes != 1 {
				t.Fatalf("pending=%d pokes=%d", got, pokes)
			}
		})
	}
}

func TestPoolWakeForeignSourceDoesNotSuppressCityScan(t *testing.T) {
	for _, ref := range []string{"city:foreign", "rig:gascity", ""} {
		t.Run(ref, func(t *testing.T) {
			primary, city := beads.NewMemStore(), beads.NewMemStore()
			seedPoolWakeSession(t, primary)
			seedPoolWakeSession(t, city)
			cfg, path, pokes := poolWakeFixture(t, city)
			deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
			deps.CityPath, deps.Store, deps.StoreRef = path, primary, ref
			var out, errOut bytes.Buffer
			if err := doSlingNudgeFromRoutedSource(&cfg.Agents[0], deps, &out, &errOut); err == nil || !strings.Contains(err.Error(), "ambiguous pool wake") {
				t.Fatalf("error=%v", err)
			}
			if err := withNudgeQueueState(path, func(s *nudgeQueueState) error {
				if len(s.Pending) != 0 || len(s.InFlight) != 0 || *pokes != 0 {
					t.Fatalf("queue=%+v pokes=%d", s, *pokes)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPoolWakeDifferentEpochsRemainDistinct(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	seedPoolWakeSession(t, primary)
	second := seedPoolWakeSession(t, city)
	if err := city.SetMetadata(second, "continuation_epoch", "2"); err != nil {
		t.Fatal(err)
	}
	cfg, path, pokes := poolWakeFixture(t, city)
	var out, errOut bytes.Buffer
	if err := doSlingNudge(&cfg.Agents[0], "test-city", path, cfg, runtime.NewFake(), primary, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	items := pendingPoolWakes(t, path)
	if len(items) != 2 || items[0].ContinuationEpoch == items[1].ContinuationEpoch {
		t.Fatalf("pending=%+v", items)
	}
	if *pokes != 1 {
		t.Fatalf("pokes=%d", *pokes)
	}
}

func TestPoolWakeFailureToPersistIsUnknown(t *testing.T) {
	primary, city := beads.NewMemStore(), beads.NewMemStore()
	seedPoolWakeSession(t, primary)
	seedPoolWakeSession(t, city)
	work, err := primary.Create(beads.Bead{Title: "assigned work", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, path, _ := poolWakeFixture(t, city)
	cfg.Rigs = []config.Rig{{Name: "gascity", Path: "gascity", Prefix: "gc"}}
	deps, _, _ := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath, deps.Store, deps.StoreRef = path, primary, "rig:gascity"
	opts := testOpts(cfg.Agents[0], work.ID)
	opts.NoFormula, opts.Nudge, opts.Force, opts.SkipPoke = true, true, true, true
	queuePath := nudgequeue.StatePath(path)
	if err := os.MkdirAll(filepath.Dir(queuePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queuePath, []byte("unreadable queue"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var out, human, errOut bytes.Buffer
		code := doSlingBatchWithJSON(opts, deps, primary, true, &human, &out, &errOut)
		var result slingJSONResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if code == 0 || result.Success || result.Queued || !result.Routed || !strings.Contains(errOut.String(), "unrecorded (state unknown)") {
			t.Fatalf("code=%d result=%+v stderr=%s", code, result, errOut.String())
		}
	}
	data, err := os.ReadFile(queuePath)
	if err != nil || string(data) != "unreadable queue" {
		t.Fatalf("queue changed=%q err=%v", data, err)
	}
}

func TestPoolWakePokeFailureRetainsQueuedAttention(t *testing.T) {
	for _, pool := range []bool{true, false} {
		t.Run(fmt.Sprintf("pool=%t", pool), func(t *testing.T) {
			store := beads.NewMemStore()
			id := seedPoolWakeSession(t, store)
			cfg, path, pokes := poolWakeFixture(t, store)
			slingPokeController = func(string) error { (*pokes)++; return errors.New("controller unavailable") }
			var out, errOut bytes.Buffer
			var err error
			if pool {
				err = doSlingNudge(&cfg.Agents[0], "test-city", path, cfg, runtime.NewFake(), store, &out, &errOut)
			} else {
				target := buildSlingNudgeTarget(cfg.Agents[0], "test-city", path, cfg, store, "pool-session")
				err = deliverSlingNudge(target, runtime.NewFake(), store, path, &out, &errOut)
			}
			if err != nil {
				t.Fatal(err)
			}
			if *pokes != 1 || len(pendingPoolWakes(t, path)) != 1 {
				t.Fatalf("pokes=%d pending=%+v", *pokes, pendingPoolWakes(t, path))
			}
			if !strings.Contains(errOut.String(), "poke failed") || strings.Contains(out.String(), "Nudged ") {
				t.Fatalf("stdout=%s stderr=%s", out.String(), errOut.String())
			}
			readback, err := store.Get(id)
			if err != nil || readback.Assignee != "" {
				t.Fatalf("session ownership changed: %+v err=%v", readback, err)
			}
		})
	}
}

type poolWakeExitsAtNudge struct {
	*runtime.Fake
	attempts int
}

func (p *poolWakeExitsAtNudge) NudgeNow(name string, _ []runtime.ContentBlock) error {
	p.attempts++
	if err := p.Stop(name); err != nil {
		return err
	}
	return runtime.ErrSessionNotFound
}

func TestPoolWakeExitInsideNudgeRequestsWake(t *testing.T) {
	store := beads.NewMemStore()
	_, path, pokes := poolWakeFixture(t, store)
	sp := &poolWakeExitsAtNudge{Fake: runtime.NewFake()}
	mgr := newSessionManagerWithConfig(path, store, sp, nil)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{
		Template: "worker", Title: "Worker", Command: "claude", WorkDir: path, Provider: "claude",
		Hints: runtime.Config{WorkDir: path}, ExtraMeta: map[string]string{"session_origin": "manual", "pool_slot": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background(), info.ID, "", runtime.Config{WorkDir: path}); err != nil {
		t.Fatal(err)
	}
	sp.WaitForIdleErrors[info.SessionName] = nil
	var out, errOut bytes.Buffer
	target := nudgeTarget{cityPath: path, agent: config.Agent{Name: "worker"}, resolved: &config.ResolvedProvider{Name: "claude"}, sessionID: info.ID, sessionName: info.SessionName}
	if err := deliverSlingNudge(target, sp, store, path, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if sp.attempts != 1 {
		t.Fatalf("actual NudgeNow attempts=%d stdout=%s stderr=%s", sp.attempts, out.String(), errOut.String())
	}
	if *pokes != 1 || len(pendingPoolWakes(t, path)) != 1 {
		t.Fatalf("pokes=%d pending=%+v stdout=%s stderr=%s", *pokes, pendingPoolWakes(t, path), out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "Nudged ") {
		t.Fatal("failed delivery reported success")
	}
}

type poolWakeDiesDuringObservation struct {
	*runtime.Fake
	attempts int
}

func (p *poolWakeDiesDuringObservation) IsRunning(name string) bool {
	running := p.Fake.IsRunning(name)
	p.attempts++
	if p.attempts == 3 {
		_ = p.Stop(name)
	}
	return running
}

func TestPoolWakeExitBeforeDeliveryQueuesWake(t *testing.T) {
	store := beads.NewMemStore()
	seedPoolWakeSession(t, store)
	cfg, path, pokes := poolWakeFixture(t, store)
	sp := &poolWakeDiesDuringObservation{Fake: runtime.NewFake()}
	if err := sp.Start(context.Background(), "pool-session", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := doSlingNudge(&cfg.Agents[0], "test-city", path, cfg, sp, store, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if *pokes != 1 {
		t.Fatalf("pokes=%d want1 after live target exited; attempts=%d stdout=%s stderr=%s", *pokes, sp.attempts, out.String(), errOut.String())
	}
	if got := len(pendingPoolWakes(t, path)); got != 1 {
		t.Fatalf("pending=%d want1", got)
	}
	if strings.Contains(out.String(), "Nudged ") {
		t.Fatal("queued wake reported delivered")
	}
}

// namepoolAgentForNudgeTest builds the pool shape that reproduces ga-mzq6: a
// pool whose members are named from a namepool file, so live instances are
// addressed as "dir/binding.<namepool-name>" rather than "pool-N". Those
// instance identities are not config entries.
func namepoolAgentForNudgeTest() config.Agent {
	return config.Agent{
		Name:              "polecat",
		Dir:               "gascity",
		BindingName:       "gastown",
		NamepoolNames:     []string{"furiosa", "rictus"},
		MinActiveSessions: intPtr(0),
		MaxActiveSessions: intPtr(2),
	}
}

// TestDoSlingNudgeNamepoolInstanceReachesRunningSession covers ga-mzq6
// acceptance criterion 1: when a namepool-named pool member is already
// running, the nudge resolves the live instance and is delivered instead of
// failing the config lookup on the instance name.
func TestDoSlingNudgeNamepoolInstanceReachesRunningSession(t *testing.T) {
	previousPoke := slingPokeController
	slingPokeController = func(string) error { return nil }
	t.Cleanup(func() { slingPokeController = previousPoke })
	runner := newFakeRunner()
	sp := runtime.NewFake()
	const sessionName = "gastown__polecat-th-dpcg1"
	if err := sp.Start(context.Background(), sessionName, runtime.Config{}); err != nil {
		t.Fatalf("Start(%q): %v", sessionName, err)
	}
	sp.Calls = nil

	a := namepoolAgentForNudgeTest()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents:    []config.Agent{a},
	}

	deps, stdout, stderr := testDeps(cfg, sp, runner.run)
	deps.CityPath = t.TempDir()
	if _, err := deps.Store.Create(beads.Bead{
		Title:  "gascity/gastown.furiosa",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"template":     "gascity/gastown.polecat",
			"session_name": sessionName,
			"pool_slot":    "1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	prev := startNudgePoller
	startNudgePoller = func(_, _, _ string) error { return nil }
	t.Cleanup(func() { startNudgePoller = prev })

	if err := doSlingNudge(&a, deps.CityName, deps.CityPath, cfg, sp, deps.Store, stdout, stderr); err != nil {
		t.Fatalf("doSlingNudge: %v", err)
	}

	if strings.Contains(stderr.String(), "not found in config") {
		t.Fatalf("stderr = %q, want no config lookup failure for the live pool instance", stderr.String())
	}
	if strings.Contains(stdout.String(), "No running sessions") || strings.Contains(stderr.String(), "No running sessions") {
		t.Fatalf("stdout=%q stderr=%q, want the live instance nudged, not the zero-instance wake path", stdout.String(), stderr.String())
	}
	var observedLiveSession bool
	for _, call := range sp.Calls {
		if call.Method == "IsRunning" && call.Name == sessionName {
			observedLiveSession = true
			break
		}
	}
	if !observedLiveSession {
		t.Fatalf("runtime calls = %#v, want IsRunning for namepool instance session %q", sp.Calls, sessionName)
	}
	if !strings.Contains(stdout.String(), "gascity/gastown.furiosa") {
		t.Fatalf("stdout = %q, want nudge output naming the namepool pool instance", stdout.String())
	}
}

// TestDoSlingNudgeNamepoolNoRunningInstancePokesController covers ga-mzq6
// acceptance criterion 2: with no live pool member, the zero-instance path
// still pokes the controller for a wake.
func TestDoSlingNudgeNamepoolNoRunningInstancePokesController(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	a := namepoolAgentForNudgeTest()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents:    []config.Agent{a},
	}

	deps, stdout, stderr := testDeps(cfg, sp, runner.run)
	deps.CityPath = t.TempDir()
	previousPoke := slingPokeController
	slingPokeController = func(string) error { return nil }
	t.Cleanup(func() { slingPokeController = previousPoke })

	if err := doSlingNudge(&a, deps.CityName, deps.CityPath, cfg, sp, deps.Store, stdout, stderr); err != nil {
		t.Fatalf("doSlingNudge: %v", err)
	}

	// Both poke outcomes (socket present or absent) print "No running sessions
	// for <pool>"; asserting on the shared prefix keeps this independent of
	// whether a controller socket exists on the test host.
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, `No running sessions for "gascity/gastown.polecat"`) {
		t.Fatalf("stdout=%q stderr=%q, want the zero-instance controller wake path", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "not found in config") {
		t.Fatalf("stderr = %q, want no config lookup failure on the zero-instance path", stderr.String())
	}
}

// TestSlingNudgeFailureReadsAsWarning covers ga-mzq6 acceptance criterion 3:
// a nudge-delivery failure must not be phrased like a sling failure, because
// the bead was already routed by the time the nudge runs.
func TestSlingNudgeFailureReadsAsWarning(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	a := config.Agent{Name: "mayor", Suspended: true, MaxActiveSessions: intPtr(1)}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents:    []config.Agent{a},
	}

	deps, stdout, stderr := testDeps(cfg, sp, runner.run)
	deps.CityPath = t.TempDir()

	if err := doSlingNudge(&a, deps.CityName, deps.CityPath, cfg, sp, deps.Store, stdout, stderr); err == nil {
		t.Fatal("doSlingNudge returned nil for suspended agent")
	}

	got := stderr.String()
	if !strings.HasPrefix(got, "warning: ") {
		t.Fatalf("stderr = %q, want a 'warning: ' prefix so the routed sling is not misread as failed", got)
	}
	if !strings.Contains(got, "bead routed") {
		t.Fatalf("stderr = %q, want the message to state the bead was still routed", got)
	}
}
