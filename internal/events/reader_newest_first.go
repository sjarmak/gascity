package events

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ReadFilteredNewestFirst returns at most limit matching events (for a positive
// limit), preferring the NEWEST ones, in chronological order. It reads the
// active file's tail first, then opens sibling archives until no unopened
// archive could still improve the answer.
//
// "Newest" means latest in LOG ORDER, not largest Ts. The log is append-only
// and seq-ordered (see the ReadAll contract). FileRecorder stamps a zero Ts
// inside the same locked write that assigns the Seq, so for events recorded
// that way the two orders agree unless the wall clock steps backwards. They can
// diverge freely for events recorded with a caller-supplied Ts, which the
// recorder preserves. A caller that must order by Ts has to sort what it gets.
//
// This exists because Filter.Limit cannot express "the newest N". ReadFiltered
// walks archives in FirstSeq-ascending order and stops once Limit matches are
// collected, so it yields whichever match that walk reaches first. For disjoint
// ascending windows that is the OLDEST match. For the nested and overlapping
// windows the writer also permits it can be the oldest, the newest, or neither:
// with [1,100] holding a match at seq 100 and a nested [50,60] holding one at
// seq 60, the walk reaches [1,100] first and returns the NEWER of the two.
// Either way a caller wanting the most recent event of some type cannot say so,
// and pays for the walk up to whatever it does get. On a city with a long-lived event log that walk
// is the dominant cost: 37 archives / 2.16 GB / 2.9M lines measured on
// ds-research, where gzip decompression alone (30.2s) is twice the 15s budget
// of the check that issued the read.
//
// # Which archives get opened
//
// Archive basenames carry a [FirstSeq, LastSeq] window, and LastSeq bounds the
// newest EVENT an archive holds. It does NOT bound the newest MATCH: under a
// filter, an archive whose window ends at 100 may hold no match newer than seq
// 10 and lose to one ending at 90 whose match is 90. So window order alone
// cannot decide which archive to open, and stopping at the first archive that
// fills the limit returns the wrong event.
//
// The rule is stated against the result instead: an unopened archive can
// improve the answer only if its LastSeq exceeds the lowest match retained so
// far, so the walk runs in LastSeq-descending order and stops once that is no
// longer true. This is correct for arbitrary windows -- overlapping, nested, or
// sharing a LastSeq -- none of which the archive writer forbids
// (parseArchiveBasename validates each window internally and never compares
// two).
//
// What is and is not bounded, stated precisely because overclaiming here is the
// defect this function was written to fix:
//
//   - THE COST IS SET BY HOW FAR BACK THE NEWEST MATCH IS, NOT BY limit. The
//     stopping rule can only fire once a match is in hand, so every newer
//     archive that contains no match is decompressed on the way to one. For a
//     RARE event type this is the dominant cost and it is not a small number:
//     asking for the newest controller.started on a city whose controller last
//     started long ago reads every archive rotated since. It is not a constant
//     bound, and a caller on a deadline must not assume one.
//   - Finding a match does NOT end the walk. The stopping rule compares an
//     archive's LastSeq against the lowest match retained so far, and a wide
//     window can hold its match far below its own LastSeq: given [1,100] whose
//     only match is seq 10, the next window [50,90] still has LastSeq 90 > 10
//     and is opened. A caller pays for one archive only when the match it
//     finds sits high enough in its window to dominate every window below.
//   - There is no bound WITHIN an archive. Finding the newest match means
//     reaching the end, so a matching archive is decompressed in full even when
//     the match is on its first line.
//   - When no match exists anywhere it opens every archive
//     archiveOverlapsFilter cannot rule out from the basename alone (AfterSeq,
//     BeforeSeq, and Since, the last with a one-second guard for timestamp
//     truncation). Type, Actor and Until prune nothing, so absence under those
//     costs a full walk.
//
// The only predicates evaluated without opening anything are AfterSeq,
// BeforeSeq and Since, so they are the only way a caller can shrink the walk.
// They shrink the CANDIDATE SET rather than cap the work: what remains is
// however many archives fall in the requested range, which is a bound the
// caller chose and can reason about, not a constant this function supplies.
//
// # What the snapshot guard refuses
//
// The active file is read once, up to the size observed when it was opened. An
// event appended after that is normally invisible, but it is not guaranteed to
// be: a rotation completing before the archive listing can promote it into an
// archive the walk then reads. Archives may therefore contribute only events
// strictly older than the tail snapshot.
//
// That is a guarantee about ordering WITHIN a result, so it binds only when the
// tail actually supplied a match. When the tail returns nothing there is no
// snapshot to be older than, and a post-snapshot event promoted by rotation can
// legitimately be returned -- it is simply a read that linearized after the
// append. What the guard rules out is a late arrival displacing or preceding a
// match the tail already produced.
//
// That guard needs a position on both sides and REFUSES rather than guesses
// when it lacks one. An archived event carrying no Seq is never returned, and
// if the tail itself holds only Seq-less events then archives contribute
// nothing at all. This trades recall for correctness: the result can come back
// short of limit while a Seq-less older match exists. Under-returning degrades
// a caller to "unknown", which is honest; a duplicated or non-chronological
// result reports the wrong event silently. FileRecorder always assigns a Seq,
// so the normal path pays nothing.
//
// The guard is fixed from the tail snapshot and never tightened by what
// archives return, because a later archive may legitimately hold a newer match
// than an earlier one. Duplicates across overlapping windows are dropped by
// Seq instead.
//
// Events still in an in-flight rotation file are invisible here, exactly as
// they are to ReadFiltered.
//
// A limit that is zero or negative delegates to ReadFiltered, where newest-first
// selection is meaningless. That path caps the RESULT COUNT only by
// filter.Limit, so with both unset every matching event in the candidate files
// comes back: a non-positive limit is not a bound. Which files are candidates
// can still be narrowed by AfterSeq, BeforeSeq and Since, the only predicates
// archiveOverlapsFilter can evaluate from a basename.
func ReadFilteredNewestFirst(path string, filter Filter, limit int) ([]Event, error) {
	if limit <= 0 {
		return ReadFiltered(path, filter)
	}

	tail, err := ReadFilteredTail(path, filter, limit)
	if err != nil {
		// Deliberately unwrapped, and deliberately inconsistent with the two
		// archive sites below that add %q. An audit flagged the inconsistency
		// and wrapping it was wrong: every error ReadFilteredTail returns here
		// wraps an *os.PathError from os.Open, f.Stat or f.ReadAt, each of which
		// already carries this path. Adding it again prints the path twice in
		// one message. The archive sites differ because they name a file this
		// function chose, which the caller never passed in.
		return tail, err
	}
	if len(tail) >= limit {
		return tail, nil
	}

	dir := filepath.Dir(path)
	archives, err := archiveFilesIn(dir)
	if err != nil {
		// A missing directory legitimately means "no archives". Anything else
		// (permissions, I/O) would silently turn "could not look" into "looked
		// and found nothing", and the caller cannot tell that apart from a real
		// absence.
		if os.IsNotExist(err) {
			return tail, nil
		}
		return tail, fmt.Errorf("listing event archives in %q: %w", dir, err)
	}

	guard := snapshotGuard{taken: len(tail) > 0, ceiling: lowestSeq(tail)}

	// archiveFilesIn sorts ascending by FirstSeq, which orders archives by where
	// they START. What decides whether an archive is still worth opening is the
	// newest event it could hold, so order by LastSeq descending instead.
	// No tie-break. Among archives sharing a LastSeq the order cannot change the
	// EVENTS RETURNED, since matches are retained globally rather than committed
	// per archive. It can still change which archives get opened, and therefore
	// both the work done and whether an unreadable archive is reached at all: of
	// two archives tied at [1,100] where one holds a match at 100 and the other
	// cannot be opened, visiting the readable one first answers without touching
	// the other, and the reverse order surfaces its error. That is why walk order
	// is guarded by an open-level trap rather than by an assertion on the result.
	//
	// SliceStable holds ties in archiveFilesIn's order, which is
	// FirstSeq-ascending -- except between archives sharing a FirstSeq as well,
	// where that function's sort.Slice is not stable and the relative order is
	// unspecified. No returned event depends on that; a tie-break here would only
	// pick which of two equally-ranked archives is opened first.
	walk := make([]archiveInfo, len(archives))
	copy(walk, archives)
	sort.SliceStable(walk, func(i, j int) bool {
		return walk[i].LastSeq > walk[j].LastSeq
	})

	kept := newMatchSet(limit - len(tail))
	for _, info := range walk {
		// The walk is LastSeq-descending, so once one archive cannot beat the
		// lowest retained match, neither can any that follow.
		if kept.full() && info.LastSeq <= kept.lowest() {
			break
		}
		if !archiveOverlapsFilter(info, filter) {
			continue
		}
		err := streamArchive(filepath.Join(dir, info.Basename), filter, func(e Event) bool {
			if matchesFilter(e, filter) && guard.admits(e) {
				kept.add(e)
			}
			return true
		})
		if err != nil {
			return tail, fmt.Errorf("reading archive %q: %w", info.Basename, err)
		}
	}

	// Everything retained is strictly older than the tail snapshot, so
	// prepending preserves chronological order.
	return append(kept.events(), tail...), nil
}

// snapshotGuard decides which archived events may join a result built from a
// tail snapshot. Ordering has to be provable in BOTH directions: an
// unpositioned event on either side leaves the comparison undecidable, and an
// undecidable comparison is refused rather than guessed at.
type snapshotGuard struct {
	// taken reports that the tail read returned events, so archives may
	// contribute only events strictly older than those.
	taken bool
	// ceiling is the lowest Seq in the tail snapshot. Zero means the tail
	// carried no Seq at all, so no comparison against it is possible.
	ceiling uint64
}

// admits reports whether e may join the result.
func (g snapshotGuard) admits(e Event) bool {
	// An event with no Seq has no position, so it can be neither ordered
	// against the rest of the result nor recognized as a duplicate of itself
	// arriving from an overlapping archive.
	if e.Seq == 0 {
		return false
	}
	if !g.taken {
		return true
	}
	// A zero ceiling means the tail holds only unpositioned events, so nothing
	// can be shown to predate it -- including an event carrying a Seq, which
	// rotation may have appended AFTER the snapshot was taken. The comparison
	// below already refuses everything in that case, since no Seq is below
	// zero; it is called out here because the reason is not obvious from the
	// arithmetic.
	return e.Seq < g.ceiling
}

// lowestSeq returns the smallest nonzero Seq in evts, or 0 if there is none.
func lowestSeq(evts []Event) uint64 {
	var low uint64
	for _, e := range evts {
		if e.Seq == 0 {
			continue
		}
		if low == 0 || e.Seq < low {
			low = e.Seq
		}
	}
	return low
}

// matchSet retains the newest need events offered to it, ordered ascending by
// Seq and deduplicated, so that overlapping archive windows offering the same
// event consume one slot rather than two. Memory is bounded by need rather than
// by how many events are streamed past it.
//
// Two contracts the single call site is responsible for, stated because nothing
// here enforces them:
//
//   - need must be at least 1. A zero-need set reports full() immediately and
//     lowest() would index an empty slice.
//   - events offered must carry a nonzero Seq. Identity here IS the Seq, so two
//     distinct Seq-less events collide in seen and one is silently dropped.
//     snapshotGuard.admits refuses them before they reach add.
type matchSet struct {
	need int
	evts []Event
	seen map[uint64]bool
}

func newMatchSet(need int) *matchSet {
	return &matchSet{need: need, evts: make([]Event, 0, need), seen: make(map[uint64]bool, need)}
}

func (m *matchSet) full() bool { return len(m.evts) == m.need }

// lowest returns the Seq of the oldest retained event. Only meaningful when
// full; an unfilled set can still be improved by any archive.
func (m *matchSet) lowest() uint64 { return m.evts[0].Seq }

// add offers e to the set, keeping it if it is new and among the newest need.
func (m *matchSet) add(e Event) {
	if m.seen[e.Seq] {
		return
	}
	if m.full() {
		if e.Seq <= m.evts[0].Seq {
			return
		}
		delete(m.seen, m.evts[0].Seq)
		m.evts = m.evts[1:]
	}
	at := sort.Search(len(m.evts), func(i int) bool { return m.evts[i].Seq > e.Seq })
	m.evts = append(m.evts, Event{})
	copy(m.evts[at+1:], m.evts[at:])
	m.evts[at] = e
	m.seen[e.Seq] = true
}

// events returns the retained events in chronological order.
func (m *matchSet) events() []Event {
	out := make([]Event, len(m.evts))
	copy(out, m.evts)
	return out
}
