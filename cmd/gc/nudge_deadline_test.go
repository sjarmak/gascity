package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestNextNudgeDeadlineChoosesEarliestLiveItem(t *testing.T) {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	state := nudgequeue.State{
		Pending: []nudgequeue.Item{
			{ID: "later", ExpiresAt: now.Add(2 * time.Hour)},
			{ID: "no-deadline"},
		},
		InFlight: []nudgequeue.Item{{ID: "earliest", ExpiresAt: now.Add(time.Hour)}},
		Dead:     []nudgequeue.Item{{ID: "dead", ExpiresAt: now.Add(-time.Hour)}},
	}

	got, ok := nudgequeue.NextDeadline(state)
	if !ok {
		t.Fatal("NextDeadline reported no live deadline")
	}
	if want := now.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("NextDeadline = %s, want %s", got, want)
	}
}

func TestNudgeDeadlineDelayIsImmediateOnlyWhenDue(t *testing.T) {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	if got := nudgeDeadlineDelay(now.Add(time.Minute), now); got != time.Minute {
		t.Fatalf("future delay = %s, want 1m", got)
	}
	if got := nudgeDeadlineDelay(now, now); got != 0 {
		t.Fatalf("at-deadline delay = %s, want 0", got)
	}
	if got := nudgeDeadlineDelay(now.Add(-time.Minute), now); got != 0 {
		t.Fatalf("overdue delay = %s, want 0", got)
	}
}

func TestDeadLetterRetentionOutlivesStandingAlarmWindow(t *testing.T) {
	const standingAlarmInterval = 6 * time.Hour
	if defaultQueuedNudgeDeadRetention < 24*time.Hour+standingAlarmInterval {
		t.Fatalf("dead retention = %s, want >=30h so a 6h standing alarm observes work after it crosses 24h", defaultQueuedNudgeDeadRetention)
	}
}

func TestNudgeDeadlineLoopExpiresOverdueStateInEveryDispatcherMode(t *testing.T) {
	for _, mode := range []string{"legacy", "supervisor"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			store := beads.NudgesStore{Store: beads.NewMemStore()}

			item := queuedNudge{
				ID:        "nudge-overdue-" + mode,
				Agent:     "retired",
				SessionID: "gc-retired",
				Source:    "session",
				CreatedAt: time.Now().Add(-24 * time.Hour),
				ExpiresAt: time.Now().Add(-time.Second),
			}
			seedDeadlineNudge(t, dir, store, item)

			ctx, cancel := context.WithCancel(context.Background())
			cr := &CityRuntime{
				cityPath:            dir,
				cfg:                 &config.City{Daemon: config.DaemonConfig{NudgeDispatcher: mode}},
				standaloneCityStore: store.Store,
				nudgeDeadlineWakeCh: make(chan struct{}, 1),
				stderr:              &bytes.Buffer{},
				logPrefix:           "test",
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				cr.runNudgeDeadlineLoop(ctx)
			}()

			awaitDeadlineBucket(t, dir, item.ID, "dead")
			cancel()
			awaitClose(t, done, "nudge deadline loop shutdown")
		})
	}
}

func TestNudgeDeadlineLoopRearmsForEarlierEnqueue(t *testing.T) {
	dir := t.TempDir()
	tracking := &deadlineTrackingStore{Store: beads.NewMemStore()}
	store := beads.NudgesStore{Store: tracking}
	previousOpen := openNudgeBeadStore
	openNudgeBeadStore = func(string) beads.NudgesStore {
		panic("runtime deadline loop opened a second CLI nudge store")
	}
	defer func() { openNudgeBeadStore = previousOpen }()

	seedDeadlineNudge(t, dir, store, queuedNudge{
		ID: "nudge-later", Agent: "worker", Source: "session",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cr := &CityRuntime{
		cityPath:            dir,
		cfg:                 &config.City{},
		standaloneCityStore: store.Store,
		nudgeDeadlineWakeCh: make(chan struct{}, 1),
		stderr:              &bytes.Buffer{},
		logPrefix:           "test",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cr.runNudgeDeadlineLoop(ctx)
	}()

	earlier := queuedNudge{
		ID: "nudge-earlier", Agent: "retired", Source: "mail",
		CreatedAt: time.Now().Add(-24 * time.Hour), ExpiresAt: time.Now().Add(-time.Second),
	}
	seedDeadlineNudge(t, dir, store, earlier)
	cr.nudgeDeadlineWakeCh <- struct{}{}
	awaitDeadlineBucket(t, dir, earlier.ID, "dead")

	cancel()
	awaitClose(t, done, "rearmed nudge deadline loop shutdown")
	if tracking.closed {
		t.Fatal("runtime deadline loop closed the runtime-owned nudge store")
	}
}

func TestCityRuntimeDeadlineExpiresWhileStartupReconciliationIsBlocked(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "city.toml")
	writeCityRuntimeConfig(t, tomlPath, "fake")
	cfg, err := config.Load(osFS{}, tomlPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	item := queuedNudge{
		ID: "nudge-startup-blocked", Agent: "retired", Source: "session",
		CreatedAt: time.Now().Add(-24 * time.Hour), ExpiresAt: time.Now().Add(-time.Second),
	}
	seedDeadlineNudge(t, dir, store, item)

	enteredBuild := make(chan struct{})
	releaseBuild := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	sp := runtime.NewFake()
	var stderr lockedBuffer
	cr := newTestCityRuntime(t, CityRuntimeParams{
		CityPath: dir,
		CityName: "test-city",
		TomlPath: tomlPath,
		Cfg:      cfg,
		SP:       sp,
		BuildFn: func(*config.City, runtime.Provider, beads.Store) DesiredStateResult {
			close(enteredBuild)
			<-releaseBuild
			return DesiredStateResult{State: map[string]TemplateParams{}}
		},
		Dops:      newDrainOps(sp),
		Rec:       events.Discard,
		OnStarted: cancel,
		Stdout:    io.Discard,
		Stderr:    &stderr,
	})
	cs := newControllerState(context.Background(), cfg, sp, events.NewFake(), "test-city", dir)
	cs.cityBeadStore = store.Store
	cr.setControllerState(cs)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cr.run(ctx)
	}()
	awaitClose(t, enteredBuild, "startup reconciliation entering blocked build")
	awaitDeadlineBucket(t, dir, item.ID, "dead")
	close(releaseBuild)
	awaitClose(t, done, "city runtime shutdown after blocked-startup deadline")
}

func TestMaintainNudgeDeadlinesExpiresZeroAttemptRetiredSession(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 14, 0, 27, 13, 0, time.UTC)
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	item := queuedNudge{
		ID:        "nudge-retired",
		Agent:     "mayor",
		SessionID: "gc-retired",
		Source:    "mail",
		Message:   "You have mail from city-infra-pl",
		CreatedAt: now.Add(-24 * time.Hour),
		ExpiresAt: now,
	}
	beadID, _, err := ensureQueuedNudgeBead(store, item)
	if err != nil {
		t.Fatalf("ensureQueuedNudgeBead: %v", err)
	}
	item.BeadID = beadID
	if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
		state.Pending = append(state.Pending, item)
		return nil
	}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	expired, err := maintainNudgeDeadlinesWithStore(dir, store, now)
	if err != nil {
		t.Fatalf("maintainNudgeDeadlinesWithStore: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != item.ID {
		t.Fatalf("expired = %+v, want [%s]", expired, item.ID)
	}
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 0 || len(state.Dead) != 1 {
		t.Fatalf("pending/dead = %d/%d, want 0/1", len(state.Pending), len(state.Dead))
	}
	if state.Dead[0].Attempts != 0 || !state.Dead[0].DeadAt.Equal(now) {
		t.Fatalf("dead item = %+v, want zero attempts and dead_at=%s", state.Dead[0], now)
	}
	shadow, ok, err := nudgeFrontDoor(store).FindIncludingTerminal(item.ID)
	if err != nil {
		t.Fatalf("FindIncludingTerminal: %v", err)
	}
	if !ok || shadow.State != "expired" || shadow.Open {
		t.Fatalf("terminal shadow = %+v, ok=%t", shadow, ok)
	}
}

func TestMaintainNudgeDeadlinesExpiresInFlightDirection(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 14, 0, 27, 13, 0, time.UTC)
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	item := queuedNudge{
		ID:            "nudge-in-flight",
		Agent:         "retired",
		SessionID:     "gc-retired",
		Source:        "session",
		CreatedAt:     now.Add(-24 * time.Hour),
		ExpiresAt:     now,
		Attempts:      1,
		LastAttemptAt: now.Add(-time.Minute),
		ClaimedAt:     now.Add(-time.Minute),
		LeaseUntil:    now.Add(time.Minute),
	}
	beadID, _, err := ensureQueuedNudgeBead(store, item)
	if err != nil {
		t.Fatalf("ensureQueuedNudgeBead: %v", err)
	}
	item.BeadID = beadID
	if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
		state.InFlight = append(state.InFlight, item)
		return nil
	}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	expired, err := maintainNudgeDeadlinesWithStore(dir, store, now)
	if err != nil {
		t.Fatalf("maintainNudgeDeadlinesWithStore: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != item.ID {
		t.Fatalf("expired = %+v, want [%s]", expired, item.ID)
	}
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.InFlight) != 0 || len(state.Dead) != 1 {
		t.Fatalf("in-flight/dead = %d/%d, want 0/1", len(state.InFlight), len(state.Dead))
	}
	if state.Dead[0].Attempts != 1 || !state.Dead[0].DeadAt.Equal(now) {
		t.Fatalf("dead item = %+v, want one attempt and dead_at=%s", state.Dead[0], now)
	}
	shadow, ok, err := nudgeFrontDoor(store).FindIncludingTerminal(item.ID)
	if err != nil {
		t.Fatalf("FindIncludingTerminal: %v", err)
	}
	if !ok || shadow.State != "expired" || shadow.Open {
		t.Fatalf("terminal shadow = %+v, ok=%t", shadow, ok)
	}
}

func TestNudgesBeadStoreSynchronizesWithConfigReload(t *testing.T) {
	storeA := beads.NewMemStore()
	storeB := beads.NewMemStore()
	cr := &CityRuntime{
		cfg:                 &config.City{},
		standaloneCityStore: storeA,
	}
	ready := make(chan struct{})
	begin := make(chan struct{})
	done := make(chan struct{})
	unexpected := make(chan beads.Store, 1)
	go func() {
		defer close(done)
		close(ready)
		<-begin
		for i := 0; i < 10_000; i++ {
			got := cr.nudgesBeadStore().Store
			if got != storeA && got != storeB {
				unexpected <- got
				return
			}
		}
	}()
	<-ready
	close(begin)
	for i := 0; i < 10_000; i++ {
		cr.serviceStateMu.Lock()
		cr.cfg = &config.City{}
		if i%2 == 0 {
			cr.standaloneCityStore = storeA
		} else {
			cr.standaloneCityStore = storeB
		}
		cr.serviceStateMu.Unlock()
	}
	<-done
	select {
	case got := <-unexpected:
		t.Fatalf("nudges store = %T, want one of the reload stores", got)
	default:
	}
}

func TestMaintainNudgeDeadlinesDoesNotExpireBeforeDeadline(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 14, 0, 27, 12, 0, time.UTC)
	item := queuedNudge{ID: "nudge-future", Agent: "worker", Source: "session", ExpiresAt: now.Add(time.Second)}
	if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
		state.Pending = append(state.Pending, item)
		return nil
	}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	expired, err := maintainNudgeDeadlinesWithStore(dir, beads.NudgesStore{}, now)
	if err != nil {
		t.Fatalf("maintainNudgeDeadlinesWithStore: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired = %+v, want none before deadline", expired)
	}
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 1 || len(state.Dead) != 0 {
		t.Fatalf("pending/dead = %d/%d, want 1/0", len(state.Pending), len(state.Dead))
	}
}

func TestArmNudgeDeadlineDistinguishesInstrumentFailure(t *testing.T) {
	dir := t.TempDir()
	writeCorruptNudgeQueueState(t, dir)
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	_, _, err := loadNextNudgeDeadline(dir, now)
	if err == nil {
		t.Fatal("loadNextNudgeDeadline accepted a corrupt queue")
	}
	if !strings.Contains(err.Error(), "parse nudge queue") {
		t.Fatalf("error = %v, want parse nudge queue", err)
	}
}

func TestNudgeDeadlineLoopReportsInstrumentFailureDistinctFromExpiry(t *testing.T) {
	dir := t.TempDir()
	writeCorruptNudgeQueueState(t, dir)
	var stderr lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	cr := &CityRuntime{
		cityPath:            dir,
		nudgeDeadlineWakeCh: make(chan struct{}, 1),
		stderr:              &stderr,
		logPrefix:           "test",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cr.runNudgeDeadlineLoop(ctx)
	}()
	awaitCond(t, func() bool {
		return strings.Contains(stderr.String(), "nudge deadline instrument failure")
	}, "distinct nudge deadline instrument failure")
	if strings.Contains(stderr.String(), "nudge deadline expired") {
		t.Fatalf("stderr = %q, corrupt instrument must not be reported as a missing acknowledgement", stderr.String())
	}
	cancel()
	awaitClose(t, done, "instrument-failure nudge deadline loop shutdown")
}

func seedDeadlineNudge(t *testing.T, dir string, store beads.NudgesStore, item queuedNudge) {
	t.Helper()
	beadID, _, err := ensureQueuedNudgeBead(store, item)
	if err != nil {
		t.Fatalf("ensureQueuedNudgeBead: %v", err)
	}
	item.BeadID = beadID
	if err := withNudgeQueueState(dir, func(state *nudgeQueueState) error {
		state.Pending = append(state.Pending, item)
		return nil
	}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
}

type deadlineTrackingStore struct {
	beads.Store
	closed bool
}

//nolint:unparam // error return is required by the runtime store-close interface
func (store *deadlineTrackingStore) CloseStore() error {
	store.closed = true
	return nil
}

func awaitDeadlineBucket(t *testing.T, dir, id, bucket string) {
	t.Helper()
	awaitCond(t, func() bool {
		state, err := nudgequeue.LoadState(dir)
		if err != nil {
			return false
		}
		var items []nudgequeue.Item
		switch bucket {
		case "pending":
			items = state.Pending
		case "dead":
			items = state.Dead
		default:
			t.Fatalf("unknown bucket %q", bucket)
		}
		for _, item := range items {
			if item.ID == id {
				return true
			}
		}
		return false
	}, "nudge "+id+" entering "+bucket)
}
