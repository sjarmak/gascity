package doctor

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/orders"
)

// eventReadCall records one event-log read issued by the check.
type eventReadCall struct {
	filter events.Filter
	limit  int
}

// spyEventReader wraps the real reader and records every call so a test can
// assert the read shape (bounded vs unbounded) rather than only its result.
func spyEventReader(calls *[]eventReadCall) orderFiringEventReadFunc {
	return func(path string, filter events.Filter, limit int) ([]events.Event, error) {
		*calls = append(*calls, eventReadCall{filter: filter, limit: limit})
		return events.ReadFilteredTail(path, filter, limit)
	}
}

// spyNewestEventReader is spyEventReader for the newest-first seam, recording
// into the same call log so one assertion loop covers every read the check makes.
func spyNewestEventReader(calls *[]eventReadCall) orderFiringEventReadFunc {
	return func(path string, filter events.Filter, limit int) ([]events.Event, error) {
		*calls = append(*calls, eventReadCall{filter: filter, limit: limit})
		return events.ReadFilteredNewestFirst(path, filter, limit)
	}
}

// TestOrderFiringCurrent_EventReadsAreBounded is the regression guard for
// ga-klv: the check must never issue an unbounded read against the city event
// log. On a busy city that log reaches hundreds of megabytes, and a full scan
// (36s per read, measured on a 161MB/253k-line log) blows the 15s check budget
// and turns this check permanently red for a reason unrelated to order firing.
//
// The check needs only the newest firing per order, so every read it issues
// must carry a positive limit — with no exceptions. The controller-start read
// used to be a sanctioned unbounded fallback, on the theory that it fired only
// on a log with no controller start in its active file. That case turned out to
// be the normal one, not the exception, and the unbounded read it reached for
// gunzipped every archive on the city (dr-6ew80). It now reaches the archives
// newest-first under a limit instead.
func TestOrderFiringCurrent_EventReadsAreBounded(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "cleanup-cooldown", Ts: now.Add(-10 * time.Minute)},
	)

	var calls []eventReadCall
	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.readEvents = spyEventReader(&calls)
	check.readEventsNewest = spyNewestEventReader(&calls)

	result := check.Run(&CheckContext{CityPath: cityPath})
	if result.Status != StatusOK {
		t.Fatalf("status = %v, want ok; msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
	if len(calls) == 0 {
		t.Fatal("check issued no event-log reads; the spy seam is not wired")
	}

	var sawFired, sawStarted bool
	for i, call := range calls {
		switch call.filter.Type {
		case events.OrderFired:
			sawFired = true
			if call.limit <= 0 {
				t.Fatalf("call %d: order.fired read is unbounded (limit=%d); a full event-log scan blows the check budget", i, call.limit)
			}
		case events.ControllerStarted:
			if call.limit <= 0 {
				t.Fatalf("call %d: controller.started read is unbounded (limit=%d); it walks every archive on the city", i, call.limit)
			}
			sawStarted = true
		default:
			t.Fatalf("call %d: unexpected event filter type %q", i, call.filter.Type)
		}
	}
	if !sawFired {
		t.Fatal("check never read order.fired events")
	}
	if !sawStarted {
		t.Fatal("check never read controller.started events")
	}
}

// TestOrderFiringCurrent_LargeEventLogStaysInsideBudget is the behavioral half
// of the guard: with a log large enough that a full scan is measurably slow, the
// check must still finish well inside its budget. It fails if anyone reinstates
// an unbounded read, independent of the call-shape assertions above.
func TestOrderFiringCurrent_LargeEventLogStaysInsideBudget(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")

	// Oldest-first: the controller start and a large body of unrelated noise,
	// then the firing we care about last. A tail read reaches the firing after
	// a few lines; a full scan pays for every one of them.
	evts := []events.Event{{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)}}
	for i := 0; i < 40000; i++ {
		evts = append(evts, events.Event{
			Type:    events.OrderFired,
			Subject: fmt.Sprintf("noise-order-%d", i%64),
			Ts:      now.Add(-12 * time.Hour),
		})
	}
	evts = append(evts, events.Event{Type: events.OrderFired, Subject: "cleanup-cooldown", Ts: now.Add(-10 * time.Minute)})
	writeOrderFiringTestEvents(t, cityPath, evts...)

	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.lastRun = func(orders.Order) (time.Time, error) {
		return time.Time{}, fmt.Errorf("lastRun must not be consulted: the firing is in the event tail")
	}

	start := time.Now()
	result := check.Run(&CheckContext{CityPath: cityPath})
	elapsed := time.Since(start)

	if result.Status != StatusOK {
		t.Fatalf("status = %v, want ok; msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
	// Generous relative to a bounded read (milliseconds) and far under the 15s
	// budget, but tight enough that a full-scan regression trips it.
	if budget := 5 * time.Second; elapsed > budget {
		t.Fatalf("check took %s on a large event log, want under %s; the event read is likely unbounded again", elapsed, budget)
	}
}

// TestOrderFiringCurrent_FiringOlderThanTailFallsBackToLastRun pins the
// correctness contract that makes the bounded read safe: an order whose newest
// firing predates the tail window is not silently reported as never-fired — the
// check falls through to the authoritative (already bounded) order-run lookup.
func TestOrderFiringCurrent_FiringOlderThanTailFallsBackToLastRun(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "cleanup-cooldown", Ts: now.Add(-10 * time.Minute)},
	)

	lastRunCalled := false
	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	// Simulate a firing that fell outside the tail window: the event read
	// returns nothing for this order, so only order-run history can answer.
	check.readEvents = func(path string, filter events.Filter, limit int) ([]events.Event, error) {
		if filter.Type == events.OrderFired {
			return nil, nil
		}
		return events.ReadFilteredTail(path, filter, limit)
	}
	check.lastRun = func(orders.Order) (time.Time, error) {
		lastRunCalled = true
		return now.Add(-10 * time.Minute), nil
	}

	result := check.Run(&CheckContext{CityPath: cityPath})
	if !lastRunCalled {
		t.Fatal("lastRun was not consulted for a firing outside the event tail; the bounded read would report a false stale")
	}
	if result.Status != StatusOK {
		t.Fatalf("status = %v, want ok (order-run history has a fresh run); msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
}

// TestOrderFiringCurrent_TimeoutHintNamesQueryCost pins the corrected hint. The
// old text blamed "beads/Dolt connectivity", which sent triage at the data
// plane while the data plane was healthy and cost a full triage cycle (ga-klv).
// A timeout here is a query-cost problem, so the hint must say so.
func TestOrderFiringCurrent_TimeoutHintNamesQueryCost(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "mol-dog-stalled-history", "cron", "0 */4 * * *")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "mol-dog-stalled-history", Ts: now.Add(-13 * time.Hour)},
	)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.historyTimeout = 20 * time.Millisecond
	check.lastRun = func(orders.Order) (time.Time, error) {
		<-release
		return time.Time{}, nil
	}

	result := check.Run(&CheckContext{CityPath: cityPath})
	if result.Status != StatusError {
		t.Fatalf("status = %v, want error; msg = %s", result.Status, result.Message)
	}
	if strings.Contains(strings.ToLower(result.FixHint), "connectivity") {
		t.Fatalf("FixHint = %q, must not blame connectivity: a timeout here is a query-cost problem", result.FixHint)
	}
	for _, want := range []string{"gc order history", "--limit"} {
		if !strings.Contains(result.FixHint, want) {
			t.Fatalf("FixHint = %q, want it to mention %q", result.FixHint, want)
		}
	}
}

// TestOrderFiringCurrent_LastRunLookupsRunInParallel is the regression guard
// for the second half of ga-klv. Each order-run lookup is a store round-trip
// costing about a second on a busy city; issued serially across the monitored
// orders they exceed the check budget on their own, and the check then reports
// a blocking failure that says nothing about whether orders are firing.
func TestOrderFiringCurrent_LastRunLookupsRunInParallel(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)

	// Every order is stale by events, so all of them need the lookup.
	const orderCount = 8
	var evts []events.Event
	evts = append(evts, events.Event{Type: events.ControllerStarted, Ts: now.Add(-240 * time.Hour)})
	for i := 0; i < orderCount; i++ {
		name := fmt.Sprintf("cooldown-order-%d", i)
		writeOrderFiringTestOrder(t, cityPath, name, "cooldown", "1h")
		evts = append(evts, events.Event{Type: events.OrderFired, Subject: name, Ts: now.Add(-9 * time.Hour)})
	}
	writeOrderFiringTestEvents(t, cityPath, evts...)

	// Prove the fan-out overlaps deterministically instead of racing a wall
	// clock: every lookup rendezvouses at a barrier and only returns once
	// wantConcurrent of them are in flight at the same time. A serial fan-out
	// can never gather the quorum, so it trips the failsafe and fails the
	// maxInFlight assertion below rather than passing by luck. The failsafe
	// never fires while the lookups genuinely run in parallel; it only bounds a
	// future regression to serial so the test fails fast instead of hanging.
	const wantConcurrent = 2
	const barrierFailsafe = 5 * time.Second
	var inFlight, maxInFlight int32
	rendezvous := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(rendezvous) }) }
	failsafe := time.AfterFunc(barrierFailsafe, release)
	defer failsafe.Stop()

	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.lastRun = func(orders.Order) (time.Time, error) {
		cur := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		for {
			observed := atomic.LoadInt32(&maxInFlight)
			if cur <= observed || atomic.CompareAndSwapInt32(&maxInFlight, observed, cur) {
				break
			}
		}
		if cur >= wantConcurrent {
			release()
		}
		<-rendezvous
		return now.Add(-30 * time.Minute), nil
	}

	result := check.Run(&CheckContext{CityPath: cityPath})

	if result.Status != StatusOK {
		t.Fatalf("status = %v, want ok (every order has a fresh run); msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
	if got := atomic.LoadInt32(&maxInFlight); got < wantConcurrent {
		t.Fatalf("max concurrent order-run lookups = %d, want at least %d; the fan-out is still serial", got, wantConcurrent)
	}
}

// TestOrderFiringCurrent_PrefetchPreservesLookupErrors makes sure moving the
// lookups off the classification loop did not swallow their failures: a lookup
// error must still surface as a blocking check error, exactly as it did when
// the loop called the resolver inline.
func TestOrderFiringCurrent_PrefetchPreservesLookupErrors(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-240 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "cleanup-cooldown", Ts: now.Add(-9 * time.Hour)},
	)

	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.lastRun = func(orders.Order) (time.Time, error) {
		return time.Time{}, fmt.Errorf("store unreachable")
	}

	result := check.Run(&CheckContext{CityPath: cityPath})
	if result.Status != StatusError {
		t.Fatalf("status = %v, want error when the order-run lookup fails", result.Status)
	}
	if joined := strings.Join(result.Details, "\n"); !strings.Contains(joined, "store unreachable") {
		t.Fatalf("details = %v, want the lookup error surfaced", result.Details)
	}
	if result.Severity != SeverityBlocking {
		t.Fatalf("Severity = %v, want SeverityBlocking for a failed lookup", result.Severity)
	}
}

// TestOrderFiringCurrent_PrefetchSkipsOrdersTheEventLogAnswers keeps the
// parallel pre-pass from turning into a store stampede: an order the event log
// already proves current must not be looked up at all.
func TestOrderFiringCurrent_PrefetchSkipsOrdersTheEventLogAnswers(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "fresh-cooldown", "cooldown", "1h")
	writeOrderFiringTestOrder(t, cityPath, "stale-cooldown", "cooldown", "1h")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-240 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "fresh-cooldown", Ts: now.Add(-10 * time.Minute)},
		events.Event{Type: events.OrderFired, Subject: "stale-cooldown", Ts: now.Add(-9 * time.Hour)},
	)

	var mu sync.Mutex
	var lookedUp []string
	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.lastRun = func(o orders.Order) (time.Time, error) {
		mu.Lock()
		lookedUp = append(lookedUp, o.ScopedName())
		mu.Unlock()
		return now.Add(-30 * time.Minute), nil
	}

	check.Run(&CheckContext{CityPath: cityPath})

	mu.Lock()
	defer mu.Unlock()
	if len(lookedUp) != 1 || lookedUp[0] != "stale-cooldown" {
		t.Fatalf("looked up %v, want only the order the event log cannot answer", lookedUp)
	}
}

// TestOrderFiringEventTailLimitIsPositive keeps the tail bound from being
// zeroed out, which would silently restore the unbounded read: the reader
// treats a non-positive limit as "read everything".
func TestOrderFiringEventTailLimitIsPositive(t *testing.T) {
	if orderFiringEventTailLimit <= 0 {
		t.Fatalf("orderFiringEventTailLimit = %d, want positive; a non-positive limit means an unbounded read", orderFiringEventTailLimit)
	}
}

// TestOrderFiringCurrent_ReadsCityEventLogPath guards against the check reading
// a path other than the city event log; it keeps the bounded read pointed at the
// file the rest of the suite writes.
func TestOrderFiringCurrent_ReadsCityEventLogPath(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "cleanup-cooldown", Ts: now.Add(-10 * time.Minute)},
	)

	want := filepath.Join(cityPath, ".gc", "events.jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("event log not written where the suite expects: %v", err)
	}

	var paths []string
	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }
	check.readEvents = func(path string, filter events.Filter, limit int) ([]events.Event, error) {
		paths = append(paths, path)
		return events.ReadFilteredTail(path, filter, limit)
	}
	check.Run(&CheckContext{CityPath: cityPath})

	if len(paths) == 0 {
		t.Fatal("check issued no event-log reads")
	}
	for _, got := range paths {
		if got != want {
			t.Fatalf("read path = %q, want %q", got, want)
		}
	}
}

// writeOrderFiringArchive gzips one JSONL event into a canonical events archive
// beside the city's active log.
func writeOrderFiringArchive(t *testing.T, cityPath, basename string, evt events.Event) {
	t.Helper()
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal archived event: %v", err)
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(append(line, '\n')); err != nil {
		t.Fatalf("gzip archived event: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	path := filepath.Join(cityPath, ".gc", basename)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive %s: %v", basename, err)
	}
}

// TestOrderFiringCurrent_ControllerStartReadStopsAtNewestArchive is the
// end-to-end half of the dr-6ew80 guard, against a real archive layout rather
// than a call-shape spy. It is deliberately two-sided.
//
// The city's active log holds no controller.started — the measured steady state,
// since the active file covers minutes and a controller start is emitted only on
// controller/supervisor start. The newest archive holds one; the older archive is
// unreadable.
//
// The order has NEVER fired, so classifyOrderFiring's verdict depends entirely
// on the controller start: with it, "never fired since controller start 24h ago"
// (StatusError); without it, "controller start unknown" (StatusOK). That makes
// the assertion catch a regression in either direction —
//
//   - reading too much: the unbounded ReadFiltered walks archives oldest-first
//     and dies on the unreadable one, which on the real city meant gunzipping
//     all 37 archives (2.16 GB) on every run;
//   - reading too little: an active-file-only read never sees the archived
//     start and silently downgrades a real never-fired outage to OK.
//
// A test with a freshly-fired order would assert neither: classifyOrderFiring
// takes the lastFired branch and never consults the controller start at all.
func TestOrderFiringCurrent_ControllerStartReadStopsAtNewestArchive(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringTestOrder(t, cityPath, "cleanup-cooldown", "cooldown", "1h")
	// Unrelated traffic only: cleanup-cooldown itself has never fired.
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.OrderFired, Subject: "some-other-order", Ts: now.Add(-10 * time.Minute)},
	)

	// Older archive: a canonical basename over non-gzip bytes. Opening it fails.
	if err := os.WriteFile(
		filepath.Join(cityPath, ".gc", "events.jsonl.archive-20260517T090000Z-seq-1-2.gz"),
		[]byte("not gzip; opening this archive is the regression\n"), 0o644,
	); err != nil {
		t.Fatalf("write unreadable archive: %v", err)
	}
	// Newer archive: the controller start the check is looking for.
	writeOrderFiringArchive(t, cityPath, "events.jsonl.archive-20260517T110000Z-seq-3-4.gz",
		events.Event{Seq: 3, Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)})

	check := NewOrderFiringCurrentCheck(cfg, cityPath)
	check.clock = func() time.Time { return now }

	result := check.Run(&CheckContext{CityPath: cityPath})

	if result.Status == StatusError && strings.Contains(result.Message, "timed out") {
		t.Fatalf("check timed out: %s", result.Message)
	}
	details := strings.Join(result.Details, " | ")
	if strings.Contains(details, "controller start unknown") {
		t.Fatalf("the archived controller start was not read; the lookup is bounded to the active file. details = %s", details)
	}
	if !strings.Contains(details, "never fired since controller start") {
		t.Fatalf("details = %s; want the never-fired-since-start verdict, which only the archived controller start can produce", details)
	}
	if result.Status != StatusError {
		t.Fatalf("status = %v, want error (cooldown order never fired in 24h of uptime); details = %s", result.Status, details)
	}
}
