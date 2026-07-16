package storehealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestStorePath(t *testing.T) {
	got := StorePath("/tmp/citysvc")
	want := filepath.Join("/tmp/citysvc", ".beads", "dolt")
	if got != want {
		t.Fatalf("StorePath = %q, want %q", got, want)
	}
}

func TestStorePath_DoltliteMetadata(t *testing.T) {
	cityPath := t.TempDir()
	beadsDir := filepath.Join(cityPath, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"doltlite","database":"doltlite","dolt_database":"hq"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := StorePath(cityPath)
	want := filepath.Join(cityPath, ".beads", "doltlite")
	if got != want {
		t.Fatalf("StorePath = %q, want %q", got, want)
	}
}

func TestComputeWarningHighRatio(t *testing.T) {
	// 11.2 GB (decimal) / 221 rows = ~50.68 MB/row, warning.
	const size = 11_200_000_000
	h := Compute("/c", size, 221, time.Time{}, "")
	if !h.Warning {
		t.Fatalf("Warning = false, want true for size=%d rows=221", size)
	}
	if h.RatioMB < 50 || h.RatioMB > 51 {
		t.Fatalf("RatioMB = %v, want ~50.7", h.RatioMB)
	}
	if h.ThresholdMB != DefaultThresholdMB {
		t.Fatalf("ThresholdMB = %v, want %v", h.ThresholdMB, DefaultThresholdMB)
	}
	if h.Path != "/c/.beads/dolt" {
		t.Fatalf("Path = %q, want /c/.beads/dolt", h.Path)
	}
}

func TestComputeNoWarningLowRatio(t *testing.T) {
	// 50 MB / 221 rows = ~0.23 MB/row, no warning.
	const size = 50_000_000
	h := Compute("/c", size, 221, time.Time{}, "")
	if h.Warning {
		t.Fatalf("Warning = true, want false for size=%d rows=221", size)
	}
	if h.RatioMB > 0.5 {
		t.Fatalf("RatioMB = %v, want < 0.5", h.RatioMB)
	}
}

func TestComputeZeroRowsNonZeroBytesWarns(t *testing.T) {
	// Degenerate case: bytes on disk with zero live rows. The literal
	// threshold expression (size > 1M * rows) warns; the ratio is left
	// at its zero value since dividing by zero is meaningless.
	h := Compute("/c", 1_000_001, 0, time.Time{}, "")
	if !h.Warning {
		t.Fatalf("Warning = false, want true when bytes > 0 and rows = 0")
	}
	if h.RatioMB != 0 {
		t.Fatalf("RatioMB = %v, want 0 when rows = 0", h.RatioMB)
	}
}

func TestComputeZeroEverything(t *testing.T) {
	h := Compute("/c", 0, 0, time.Time{}, "")
	if h.Warning {
		t.Fatalf("Warning = true, want false for all-zero inputs")
	}
}

func TestComputeBoundary(t *testing.T) {
	// Exactly at the threshold: size = 1M * rows should NOT warn
	// (the inequality is strict ">", not ">=").
	const rows = 10
	h := Compute("/c", int64(DefaultThresholdMB*bytesPerMB)*int64(rows), rows, time.Time{}, "")
	if h.Warning {
		t.Fatalf("Warning = true at exact threshold, want false")
	}
	h = Compute("/c", int64(DefaultThresholdMB*bytesPerMB)*int64(rows)+1, rows, time.Time{}, "")
	if !h.Warning {
		t.Fatalf("Warning = false one byte over threshold, want true")
	}
}

func TestComputeCarriesLastGC(t *testing.T) {
	ts := time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC)
	h := Compute("/c", 1, 1, ts, "success")
	if !h.LastGCAt.Equal(ts) {
		t.Fatalf("LastGCAt = %v, want %v", h.LastGCAt, ts)
	}
	if h.LastGCStatus != "success" {
		t.Fatalf("LastGCStatus = %q, want success", h.LastGCStatus)
	}
}

func TestWalkSizeMissingPath(t *testing.T) {
	got := WalkSize(filepath.Join(t.TempDir(), "nonexistent"))
	if got != 0 {
		t.Fatalf("WalkSize(missing) = %d, want 0", got)
	}
}

func TestWalkSizeSumsFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel string, size int) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	mustWrite("a.bin", 100)
	mustWrite("sub/b.bin", 250)
	mustWrite("sub/deeper/c.bin", 17)
	got := WalkSize(dir)
	if got != 367 {
		t.Fatalf("WalkSize = %d, want 367", got)
	}
}

func TestLastMaintenanceEmptyCityPath(t *testing.T) {
	ts, status := LastMaintenance(fsys.OSFS{}, "", events.NewFake())
	if !ts.IsZero() || status != "" {
		t.Fatalf("LastMaintenance(empty cityPath) = (%v,%q), want (zero,\"\")", ts, status)
	}
}

func TestLastMaintenanceNilProvider(t *testing.T) {
	city := t.TempDir()
	ts, status := LastMaintenance(fsys.OSFS{}, city, nil)
	if !ts.IsZero() || status != "" {
		t.Fatalf("LastMaintenance(nil provider) = (%v,%q), want (zero,\"\")", ts, status)
	}
}

// TestLastMaintenanceReadOnlyDoesNotWrite locks in the property that resolved
// the two-process race: the CLI-facing read path never writes the shared
// sidecar, even on the absent-sidecar scan fallback. Only the supervisor
// (SeedMaintenanceProjection / RecordMaintenanceEvent) may write it.
func TestLastMaintenanceReadOnlyDoesNotWrite(t *testing.T) {
	city := t.TempDir()
	ep := events.NewFake()
	at := time.Date(2026, 4, 8, 3, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(events.StoreMaintenanceDonePayload{DurationSeconds: 1})
	ep.Record(events.Event{Type: events.StoreMaintenanceDone, Ts: at, Payload: payload})

	// Correct answer from the read-only history scan...
	ts, status := LastMaintenance(fsys.OSFS{}, city, ep)
	if !ts.Equal(at) || status != "success" {
		t.Fatalf("LastMaintenance = (%v,%q), want (%v,success)", ts, status, at)
	}
	// ...but the sidecar must NOT have been created.
	if _, ok, err := LoadMaintenanceProjection(fsys.OSFS{}, city); err != nil || ok {
		t.Fatalf("read-only path wrote the sidecar: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(MaintenanceProjectionPath(city)); !os.IsNotExist(err) {
		t.Fatalf("sidecar exists after read-only call: err=%v", err)
	}
}

func TestSeedMaintenanceProjectionAcrossTypes(t *testing.T) {
	city := t.TempDir()
	ep := events.NewFake()
	older := time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 8, 3, 0, 0, 0, time.UTC)

	payloadDone, _ := json.Marshal(events.StoreMaintenanceDonePayload{DurationSeconds: 1})
	payloadFail, _ := json.Marshal(events.StoreMaintenanceFailedPayload{Stage: "gc"})

	ep.Record(events.Event{Type: events.StoreMaintenanceDone, Ts: older, Payload: payloadDone})
	ep.Record(events.Event{Type: events.StoreMaintenanceFailed, Ts: newer, Payload: payloadFail})

	ts, status := SeedMaintenanceProjection(fsys.OSFS{}, city, ep)
	if !ts.Equal(newer) || status != "failed" {
		t.Fatalf("SeedMaintenanceProjection = (%v,%q), want (%v,failed)", ts, status, newer)
	}

	// The seed persisted, so a follow-up read needs no provider: pass nil and
	// require the same answer. If the read re-scanned, nil would yield zero.
	ts2, status2 := LastMaintenance(fsys.OSFS{}, city, nil)
	if !ts2.Equal(newer) || status2 != "failed" {
		t.Fatalf("post-seed read = (%v,%q), want (%v,failed) — projection was re-scanned", ts2, status2, newer)
	}
}

func TestSeedMaintenanceProjectionOnlyDoneEvents(t *testing.T) {
	city := t.TempDir()
	ep := events.NewFake()
	t1 := time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 8, 3, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(events.StoreMaintenanceDonePayload{DurationSeconds: 2})
	ep.Record(events.Event{Type: events.StoreMaintenanceDone, Ts: t1, Payload: payload})
	ep.Record(events.Event{Type: events.StoreMaintenanceDone, Ts: t2, Payload: payload})

	ts, status := SeedMaintenanceProjection(fsys.OSFS{}, city, ep)
	if !ts.Equal(t2) || status != "success" {
		t.Fatalf("SeedMaintenanceProjection = (%v,%q), want (%v,success)", ts, status, t2)
	}
}

func TestSeedMaintenanceProjectionNoEvents(t *testing.T) {
	city := t.TempDir()
	ts, status := SeedMaintenanceProjection(fsys.OSFS{}, city, events.NewFake())
	if !ts.IsZero() || status != "" {
		t.Fatalf("SeedMaintenanceProjection(empty) = (%v,%q), want (zero,\"\")", ts, status)
	}
}

// TestSeedMaintenanceProjectionFindsEventAfterRotation is the regression the
// previous ListTail-based fix failed: a real FileRecorder whose maintenance
// event has rotated into a .gz archive. The active file holds only the
// rotation anchor, so an active-file-only tail sees zero matches and reports
// "never run". The seed scans the provider (archives included), so the
// pre-rotation event is still found and persisted. The read-only path finds
// it too (via the scan) without writing.
func TestSeedMaintenanceProjectionFindsEventAfterRotation(t *testing.T) {
	city := t.TempDir()
	logPath := filepath.Join(city, ".gc", "events.jsonl")
	rec, err := events.NewFileRecorder(logPath, os.Stderr)
	if err != nil {
		t.Fatalf("NewFileRecorder: %v", err)
	}
	defer rec.Close() //nolint:errcheck // test cleanup

	doneAt := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(events.StoreMaintenanceDonePayload{DurationSeconds: 3})
	rec.Record(events.Event{Type: events.StoreMaintenanceDone, Ts: doneAt, Payload: payload})

	// Force the maintenance event into a .gz archive; the fresh active file
	// then carries only the events.rotated anchor.
	res, err := rec.ForceRotate()
	if err != nil {
		t.Fatalf("ForceRotate: %v", err)
	}
	if !res.Rotated {
		t.Fatalf("ForceRotate did not rotate: %s", res.Reason)
	}
	rec.WaitForRotations()

	// Sanity: a bare tail of the active file must NOT see the event (this is
	// exactly the blindness that made the ListTail approach unsafe).
	tail, err := events.ReadFilteredTail(logPath, events.Filter{Type: events.StoreMaintenanceDone}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredTail: %v", err)
	}
	if len(tail) != 0 {
		t.Fatalf("active-file tail unexpectedly found the rotated event: %+v", tail)
	}

	ts, status := SeedMaintenanceProjection(fsys.OSFS{}, city, rec)
	if !ts.Equal(doneAt) || status != "success" {
		t.Fatalf("SeedMaintenanceProjection = (%v,%q), want (%v,success) — event must survive rotation into an archive", ts, status, doneAt)
	}
}

// TestSeedMaintenanceProjectionNoMatchAfterRotationSeedsOnce covers the
// production profile: a real, rotated history with zero maintenance events.
// The seed scans once and persists an empty projection; a second read must be
// answerable without the provider (nil), proving the expensive full-archive
// scan does not repeat on every no-match /status call.
func TestSeedMaintenanceProjectionNoMatchAfterRotationSeedsOnce(t *testing.T) {
	city := t.TempDir()
	logPath := filepath.Join(city, ".gc", "events.jsonl")
	rec, err := events.NewFileRecorder(logPath, os.Stderr)
	if err != nil {
		t.Fatalf("NewFileRecorder: %v", err)
	}
	defer rec.Close() //nolint:errcheck // test cleanup

	// Non-maintenance history, then rotation — no maintenance event anywhere.
	rec.Record(events.Event{Type: events.SessionWoke, Ts: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	rec.Record(events.Event{Type: events.OrderFired, Ts: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)})
	if _, err := rec.ForceRotate(); err != nil {
		t.Fatalf("ForceRotate: %v", err)
	}
	rec.WaitForRotations()

	ts, status := SeedMaintenanceProjection(fsys.OSFS{}, city, rec)
	if !ts.IsZero() || status != "" {
		t.Fatalf("SeedMaintenanceProjection(no-match) = (%v,%q), want (zero,\"\")", ts, status)
	}
	if _, ok, err := LoadMaintenanceProjection(fsys.OSFS{}, city); err != nil || !ok {
		t.Fatalf("no-match seed did not persist a marker: ok=%v err=%v", ok, err)
	}
	// Second read with a nil provider must still return zero from the
	// persisted marker — never a re-scan.
	ts2, status2 := LastMaintenance(fsys.OSFS{}, city, nil)
	if !ts2.IsZero() || status2 != "" {
		t.Fatalf("second no-match read = (%v,%q), want (zero,\"\")", ts2, status2)
	}
}

// TestRecordMaintenanceEventUpkeep proves the append-time path: on a fresh
// install RecordMaintenanceEvent alone creates and keeps the projection
// current — a later read with a nil provider reflects the recorded run.
func TestRecordMaintenanceEventUpkeep(t *testing.T) {
	city := t.TempDir()

	done := time.Date(2026, 7, 1, 4, 0, 0, 0, time.UTC)
	if err := RecordMaintenanceEvent(fsys.OSFS{}, city, done, "success"); err != nil {
		t.Fatalf("RecordMaintenanceEvent: %v", err)
	}
	ts, status := LastMaintenance(fsys.OSFS{}, city, nil)
	if !ts.Equal(done) || status != "success" {
		t.Fatalf("after upkeep = (%v,%q), want (%v,success)", ts, status, done)
	}

	// A later failure supersedes; an older success does not lower it.
	fail := done.Add(time.Hour)
	if err := RecordMaintenanceEvent(fsys.OSFS{}, city, fail, "failed"); err != nil {
		t.Fatalf("RecordMaintenanceEvent(failed): %v", err)
	}
	if err := RecordMaintenanceEvent(fsys.OSFS{}, city, done.Add(-time.Hour), "success"); err != nil {
		t.Fatalf("RecordMaintenanceEvent(older success): %v", err)
	}
	ts, status = LastMaintenance(fsys.OSFS{}, city, nil)
	if !ts.Equal(fail) || status != "failed" {
		t.Fatalf("after failure = (%v,%q), want (%v,failed)", ts, status, fail)
	}
}
