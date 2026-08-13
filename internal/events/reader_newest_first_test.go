package events

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeTypedJSONL writes one JSONL line per seq, all carrying typ, so a test can
// filter on type the way the real controller-start lookup does.
func writeTypedJSONL(t *testing.T, path string, typ string, seqs ...uint64) {
	t.Helper()
	var b strings.Builder
	for _, s := range seqs {
		fmt.Fprintf(&b, `{"seq":%d,"type":%q,"subject":"s%d"}`+"\n", s, typ, s)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeGzArchive gzips a throwaway source of controller starts into a canonical
// archive for the given seq window.
func writeGzArchive(t *testing.T, dir string, ts time.Time, seqs ...uint64) {
	t.Helper()
	src := filepath.Join(dir, fmt.Sprintf("archive-source-%d.jsonl", seqs[0]))
	writeTypedJSONL(t, src, ControllerStarted, seqs...)
	archive := filepath.Join(dir, formatArchiveBasename(ts, seqs[0], seqs[len(seqs)-1]))
	var stderr bytes.Buffer
	if err := gzipAndArchive(src, archive, &stderr); err != nil {
		t.Fatalf("gzipAndArchive: %v (stderr: %s)", err, stderr.String())
	}
}

// writeBoobyTrappedArchive writes a file with a canonical archive basename whose
// contents are not gzip. Opening it fails; ignoring it succeeds. That asymmetry
// is the whole measurement: a test that only checks the returned events passes
// even when every archive is read, so it cannot catch a regression back to the
// full walk.
//
// Scoped honestly, since this file is about not overclaiming: it proves that
// archives placed BEYOND the answer are not opened. It does not measure bytes,
// so it would not catch an implementation that read every archive and merely
// suppressed errors raised after the answer was already in hand. No realistic
// regression has that shape; a byte-level bound would need an instrumented
// open seam in production code, which is not worth its cost here.
func writeBoobyTrappedArchive(t *testing.T, dir string, ts time.Time, first, last uint64) {
	t.Helper()
	path := filepath.Join(dir, formatArchiveBasename(ts, first, last))
	if err := os.WriteFile(path, []byte("this is not gzip and must never be opened\n"), 0o644); err != nil {
		t.Fatalf("write booby-trapped archive: %v", err)
	}
}

// TestReadFilteredNewestFirstStopsAtNewestMatch is the boundedness guard. The
// two older archives are unreadable, so the read completing without error is
// proof they were never opened — not merely that the answer was right.
//
// This is the defect behind dr-6ew80: doctor's controller-start lookup fell back
// to an unbounded ReadFiltered, which walks archives oldest-first, so on a city
// with a long-lived event log it gunzipped every archive (measured: 37 files /
// 2.16 GB) on essentially every run and blew the check's 15s budget.
func TestReadFilteredNewestFirstStopsAtNewestMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC), 3, 4)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC), 5, 6)
	// The active file holds unrelated traffic only, which is the measured
	// steady state: it covers minutes, and controller starts are rare.
	writeTypedJSONL(t, path, BeadCreated, 7, 8)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst opened an older archive: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{6}) {
		t.Fatalf("seqs = %v, want [6] (the newest controller start)", seqs)
	}
}

// TestReadFilteredNewestFirstPrefersNewestOverOldest pins the ordering contract
// against ReadFiltered, whose Limit takes the OLDEST match. A caller asking for
// "the most recent controller start" cannot express that as a Filter.Limit, and
// that gap is why this function exists.
func TestReadFilteredNewestFirstPrefersNewestOverOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC), 3, 4)
	writeTypedJSONL(t, path, BeadCreated, 5)

	newest, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(newest); !reflect.DeepEqual(seqs, []uint64{4}) {
		t.Fatalf("newest-first seqs = %v, want [4]", seqs)
	}

	oldest, err := ReadFiltered(path, Filter{Type: ControllerStarted, Limit: 1})
	if err != nil {
		t.Fatalf("ReadFiltered: %v", err)
	}
	if seqs := seqsOf(oldest); !reflect.DeepEqual(seqs, []uint64{1}) {
		t.Fatalf("ReadFiltered limit seqs = %v, want [1]; the contrast this function exists for is gone", seqs)
	}
}

// TestReadFilteredNewestFirstAnswersFromActiveFile covers the cheap path: when
// the active file already satisfies the limit, no archive is opened at all.
// Every archive here is unreadable, so any archive access fails the test.
func TestReadFilteredNewestFirstAnswersFromActiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, ControllerStarted, 3, 4)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst read an archive it did not need: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{4}) {
		t.Fatalf("seqs = %v, want [4]", seqs)
	}
}

// TestReadFilteredNewestFirstSpansActiveAndArchives checks that a limit larger
// than the active file's supply is completed from the newest archive backwards,
// and that the merged result stays in chronological order.
func TestReadFilteredNewestFirstSpansActiveAndArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC), 1, 2)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 3, 4, 5)
	writeTypedJSONL(t, path, ControllerStarted, 6)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 3)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{4, 5, 6}) {
		t.Fatalf("seqs = %v, want [4 5 6] (newest three, chronological)", seqs)
	}
}

// TestReadFilteredNewestFirstUnboundedDelegates pins the documented fallback:
// with a non-positive limit there is no newest-first work to do, so the call is
// ReadFiltered and returns the whole matching history in chronological order.
func TestReadFilteredNewestFirstUnboundedDelegates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, ControllerStarted, 3)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 0)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{1, 2, 3}) {
		t.Fatalf("unbounded seqs = %v, want [1 2 3]", seqs)
	}
}

// TestReadFilteredNewestFirstReportsUnreadableArchive is the negative control
// for the guard above: when a corrupt archive IS on the path to the answer, the
// error surfaces rather than being swallowed into a short result. Without this,
// the boobytrap tests would also pass against an implementation that ignored
// every archive error.
func TestReadFilteredNewestFirstReportsUnreadableArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, BeadCreated, 3)

	_, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err == nil {
		t.Fatal("want an error for an unreadable archive on the path to the answer, got nil")
	}
	if !strings.Contains(err.Error(), "reading archive") {
		t.Fatalf("error = %v, want it to name the archive it failed on", err)
	}
}

// TestReadFilteredNewestFirstCombinesTwoArchives closes the gap where a single
// archive supplies every match: stopping after the first readable archive, even
// when it comes up short of the limit, has to be caught.
func TestReadFilteredNewestFirstCombinesTwoArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 8, 0, 0, 0, time.UTC), 1, 2)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC), 3, 4)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 5)
	writeTypedJSONL(t, path, BeadCreated, 6)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 3)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{3, 4, 5}) {
		t.Fatalf("seqs = %v, want [3 4 5] (drawn from two archives, chronological)", seqs)
	}
}

// TestReadFilteredNewestFirstLimitExceedsSupply pins the short-result contract:
// asking for more than exists returns everything available rather than erroring
// or dropping the partial result.
func TestReadFilteredNewestFirstLimitExceedsSupply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1)
	writeTypedJSONL(t, path, ControllerStarted, 2)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 50)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{1, 2}) {
		t.Fatalf("seqs = %v, want [1 2] (everything available)", seqs)
	}
}

// TestReadFilteredNewestFirstNegativeLimitDelegates covers the other half of the
// non-positive branch; only limit 0 was exercised before.
func TestReadFilteredNewestFirstNegativeLimitDelegates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, ControllerStarted, 3)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, -1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{1, 2, 3}) {
		t.Fatalf("negative-limit seqs = %v, want [1 2 3]", seqs)
	}
}

// TestReadFilteredNewestFirstNonPositiveLimitKeepsFilterLimit pins the doc
// comment's claim that delegating on a non-positive limit is not a way to force
// an unbounded read: ReadFiltered still enforces filter.Limit.
func TestReadFilteredNewestFirstNonPositiveLimitKeepsFilterLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, ControllerStarted, 3)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted, Limit: 1}, 0)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{1}) {
		t.Fatalf("seqs = %v, want [1]; filter.Limit must survive the delegation", seqs)
	}
}

// TestReadFilteredNewestFirstNoMatchAnywhere pins the empty-result contract:
// nothing matching anywhere returns no events and no error.
//
// Scoped honestly: this asserts the RESULT, not that any particular archive was
// opened. It would pass against an implementation that gave up after the active
// file. The doc comment's claim that a no-match read opens every non-excluded
// archive is a cost statement, and the cost is what the other guards measure.
func TestReadFilteredNewestFirstNoMatchAnywhere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 1, 2)
	writeTypedJSONL(t, path, BeadCreated, 3)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: OrderFired}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no events", seqsOf(got))
	}
}

// TestReadFilteredNewestFirstUnlistableDirErrors is the guard for the failure
// mode where "could not look" is returned as "looked and found nothing". A
// caller cannot tell an empty result from a real absence, so a listing failure
// that is not a missing directory has to surface.
func TestReadFilteredNewestFirstUnlistableDirErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "gc")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	writeTypedJSONL(t, path, BeadCreated, 1)

	// Searchable but not listable: opening the known active path still works,
	// while os.ReadDir fails.
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err == nil {
		t.Fatal("want an error when the archive directory cannot be listed, got nil")
	}
	if !strings.Contains(err.Error(), "listing event archives") {
		t.Fatalf("error = %v, want it to name the listing failure", err)
	}
}

// TestReadFilteredNewestFirstMissingDirIsNotAnError keeps the negative control
// on the case above: a directory that does not exist is a legitimate "no
// archives", not a failure.
func TestReadFilteredNewestFirstMissingDirIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "events.jsonl")

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no events", seqsOf(got))
	}
}

// TestReadFilteredNewestFirstSkipsDuplicateAcrossRotation covers the rotation
// race: an event present in BOTH the active tail and a just-promoted archive
// must not consume a second slot, or the real next-newest match is never
// fetched and the caller silently gets one event where it asked for two.
//
// The duplicate and its predecessor share ONE archive on purpose. Split across
// two archives the test passes even against a filter applied AFTER the
// retention ring, because the reader just walks on to the next archive. Only a
// shared archive exercises the eviction: with post-ring filtering the duplicate
// displaces seq 4 inside the ring and the result comes back short.
func TestReadFilteredNewestFirstSkipsDuplicateAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// One archive holding [4, 5]; seq 5 is also still visible in the active
	// file, which is the rotation coexistence window.
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC), 4, 5)
	writeTypedJSONL(t, path, ControllerStarted, 5)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 2)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{4, 5}) {
		t.Fatalf("seqs = %v, want [4 5]; a duplicated seq consumed a slot", seqs)
	}
}

// TestReadFilteredNewestFirstIgnoresPostSnapshotArrivals pins the clamp that
// keeps the result chronological under a concurrent rotation. The active tail
// is read first; if events newer than it are appended and rotated into an
// archive before the listing, the walk must not pick them up. Without the
// clamp they land newer-than-newest, producing an out-of-order result like
// [7 5] and displacing the older match the caller actually asked for.
func TestReadFilteredNewestFirstIgnoresPostSnapshotArrivals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Models the post-snapshot state: an archive that already contains the
	// tail's event (5) plus later arrivals (6, 7), and an older archive
	// holding the genuine predecessor (4).
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC), 4)
	writeGzArchive(t, dir, time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC), 5, 6, 7)
	writeTypedJSONL(t, path, ControllerStarted, 5)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 2)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{4, 5}) {
		t.Fatalf("seqs = %v, want [4 5]; archives must contribute only events older than the tail snapshot", seqs)
	}
}

// writeGzArchiveNamed gzips arbitrary JSONL into an archive with a caller-chosen
// basename, so a test can build a seq window the normal helpers would not.
func writeGzArchiveNamed(t *testing.T, dir, basename, body string) {
	t.Helper()
	src := filepath.Join(dir, "src-"+basename+".jsonl")
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write archive source: %v", err)
	}
	var stderr bytes.Buffer
	if err := gzipAndArchive(src, filepath.Join(dir, basename), &stderr); err != nil {
		t.Fatalf("gzipAndArchive %s: %v (stderr: %s)", basename, err, stderr.String())
	}
}

func controllerStartLine(seq uint64) string {
	if seq == 0 {
		return `{"type":"controller.started","subject":"unpositioned"}` + "\n"
	}
	return fmt.Sprintf(`{"seq":%d,"type":"controller.started","subject":"s%d"}`+"\n", seq, seq)
}

// TestReadFilteredNewestFirstDropsUnpositionedArchiveEvents covers the hole the
// seq clamp would otherwise leave. An event carrying no Seq has no position, so
// once the result holds anything, an archived copy of it cannot be shown to
// predate what we have. Admitting it duplicates the occurrence.
//
// Reachable via rotation: the active file holds two matching Seq-less events,
// then that file is promoted into an archive before the listing. The clamp
// ceiling is 0 because nothing in hand carries a Seq, so a ceiling test alone
// lets both copies back in and returns the same occurrence twice.
func TestReadFilteredNewestFirstDropsUnpositionedArchiveEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	body := controllerStartLine(0) + controllerStartLine(0) +
		`{"seq":1,"type":"bead.created","subject":"x"}` + "\n"
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T120000Z-seq-1-1.gz", body)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 3)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2; an unpositioned archived event was admitted and duplicated the occurrence", len(got))
	}
}

// TestReadFilteredNewestFirstSurvivesOverlappingArchiveWindows keeps the clamp
// lowering as the walk proceeds. Archive seq windows are supposed to be
// disjoint and descending, but nothing enforces that across archives:
// parseArchiveBasename validates only that each window is internally ordered.
// Two windows overlapping at seq 7 must not yield it twice.
func TestReadFilteredNewestFirstSurvivesOverlappingArchiveWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-6-7.gz",
		controllerStartLine(6)+controllerStartLine(7))
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T110000Z-seq-7-8.gz",
		controllerStartLine(7)+controllerStartLine(8))
	writeTypedJSONL(t, path, BeadCreated, 9)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 3)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{6, 7, 8}) {
		t.Fatalf("seqs = %v, want [6 7 8]; overlapping archive windows re-admitted an event already taken", seqs)
	}
}

// TestReadFilteredNewestFirstRejectsArchivesWhenNothingInHandIsPositioned is the
// other half of the unpositioned case. When every event already in hand lacks a
// Seq there is no position to compare against, so NO archived event can be shown
// to predate what we hold -- not even one that carries a Seq of its own.
//
// Admitting them is worse than coming up short. Rotation can promote the active
// file into an archive together with events appended after the snapshot, so the
// admitted event may be NEWER than what the tail returned; prepending it then
// reports a post-snapshot arrival as the oldest result and hides the genuinely
// older match sitting in the next archive.
func TestReadFilteredNewestFirstRejectsArchivesWhenNothingInHandIsPositioned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-5-5.gz", controllerStartLine(5))
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T110000Z-seq-10-10.gz", controllerStartLine(10))
	if err := os.WriteFile(path, []byte(controllerStartLine(0)), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 2)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if len(got) != 1 || got[0].Seq != 0 {
		t.Fatalf("got seqs %v (%d events), want exactly the one unpositioned active-file event; "+
			"nothing in hand carries a Seq, so no archived event can be shown to predate it",
			seqsOf(got), len(got))
	}
}

// TestReadFilteredNewestFirstDropsUnpositionedArchiveEventUnderPositionedCeiling
// covers the converse: the result IS positioned, and the archive offers a match
// with no Seq. That event cannot be placed relative to the ceiling either, so it
// is dropped rather than guessed at.
func TestReadFilteredNewestFirstDropsUnpositionedArchiveEventUnderPositionedCeiling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-1-4.gz",
		controllerStartLine(0)+controllerStartLine(3))
	if err := os.WriteFile(path, []byte(controllerStartLine(5)), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 3)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{3, 5}) {
		t.Fatalf("seqs = %v, want [3 5]; an archived event carrying no Seq cannot be placed under a positioned ceiling", seqs)
	}
}

// TestReadFilteredNewestFirstHandlesNestedArchiveWindows extends the
// overlapping-window case to NESTED windows, where one archive's whole seq
// range sits inside another's. archiveFilesIn sorts on FirstSeq alone, so
// reversing that order is newest-first only while windows are disjoint: with
// [50,60] nested inside [1,100] the reverse walk visits [50,60] first, meets
// the limit there, and reports seq 60 as the newest match when 100 is.
//
// Reachable for the same reason overlapping windows are: parseArchiveBasename
// validates each window internally and never compares two of them.
func TestReadFilteredNewestFirstHandlesNestedArchiveWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-1-100.gz", controllerStartLine(100))
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T110000Z-seq-50-60.gz", controllerStartLine(60))
	writeTypedJSONL(t, path, BeadCreated, 200)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{100}) {
		t.Fatalf("seqs = %v, want [100]; a nested archive window was visited before the archive holding the newest match", seqs)
	}
}

// beadLine is a non-matching filler event, used to place a MATCH away from the
// edges of an archive's seq window.
func beadLine(seq uint64) string {
	return fmt.Sprintf(`{"seq":%d,"type":"bead.created","subject":"b%d"}`+"\n", seq, seq)
}

// TestReadFilteredNewestFirstFindsMatchBuriedMidWindow is the case that broke
// every metadata-ordering rule tried before it. LastSeq bounds an archive's
// newest EVENT, not its newest MATCH: archive [1,100] ends higher than [50,90]
// yet holds no match newer than seq 10, while [50,90] holds one at 90.
//
// Ordering the walk by LastSeq and stopping at the first archive that fills the
// limit therefore returns seq 10. Correctness requires continuing while an
// unopened archive's LastSeq can still beat the lowest match retained.
func TestReadFilteredNewestFirstFindsMatchBuriedMidWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-1-100.gz",
		beadLine(1)+controllerStartLine(10)+beadLine(100))
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T110000Z-seq-50-90.gz",
		beadLine(50)+controllerStartLine(90))
	writeTypedJSONL(t, path, BeadCreated, 101)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{90}) {
		t.Fatalf("seqs = %v, want [90]; the walk stopped at an archive whose window ends higher but whose newest MATCH is older", seqs)
	}
}

// TestReadFilteredNewestFirstBreaksLastSeqTieByOpeningBoth pins the tie case.
// Two archives share LastSeq 100, so the basenames cannot say which holds the
// newer match; only opening both can. The tie-break order must not decide the
// answer.
func TestReadFilteredNewestFirstBreaksLastSeqTieByOpeningBoth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-1-100.gz",
		beadLine(1)+controllerStartLine(100))
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T110000Z-seq-50-100.gz",
		controllerStartLine(90)+beadLine(100))
	writeTypedJSONL(t, path, BeadCreated, 101)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{100}) {
		t.Fatalf("seqs = %v, want [100]; a LastSeq tie was decided by walk order instead of by opening both archives", seqs)
	}
}

// TestReadFilteredNewestFirstStopsOnceNoArchiveCanBeatTheResult is the other
// side of the continuation rule: it must not degrade into reading everything.
// The boobytrap archive has a canonical basename over non-gzip bytes, and its
// window ends below the match already retained, so a correct walk never opens
// it. Opening it fails loudly.
func TestReadFilteredNewestFirstStopsOnceNoArchiveCanBeatTheResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC), 1, 40)
	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-41-80.gz",
		beadLine(41)+controllerStartLine(50)+beadLine(80))
	writeTypedJSONL(t, path, BeadCreated, 101)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst opened an archive that could not beat the retained match: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{50}) {
		t.Fatalf("seqs = %v, want [50]", seqs)
	}
}

// TestReadFilteredNewestFirstOpensTheHighestWindowFirst guards the walk ORDER,
// which no result assertion can see. The continuation rule made results
// order-independent -- any order retains matches globally and stops on the same
// condition -- so ordering now buys boundedness rather than correctness, and
// only an open-level guard can catch its loss.
//
// The nested [50,60] archive is unreadable. Ordering by LastSeq opens [1,100]
// first, retains seq 70, and then stops because 60 <= 70, never touching it.
// Ordering by FirstSeq opens it first and the read fails.
func TestReadFilteredNewestFirstOpensTheHighestWindowFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	writeGzArchiveNamed(t, dir, "events.jsonl.archive-20260507T100000Z-seq-1-100.gz",
		beadLine(1)+controllerStartLine(70)+beadLine(100))
	writeBoobyTrappedArchive(t, dir, time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC), 50, 60)
	writeTypedJSONL(t, path, BeadCreated, 101)

	got, err := ReadFilteredNewestFirst(path, Filter{Type: ControllerStarted}, 1)
	if err != nil {
		t.Fatalf("ReadFilteredNewestFirst opened the nested lower window before the archive that could hold a newer match: %v", err)
	}
	if seqs := seqsOf(got); !reflect.DeepEqual(seqs, []uint64{70}) {
		t.Fatalf("seqs = %v, want [70]", seqs)
	}
}
