// Package beads provides the guarded multi-key metadata clear capability seam.
//
// CompareAndSetMetadataKey (metadata_cas.go) fences exactly the one key it
// writes. A caller that needs to clear SEVERAL keys once a guard key still
// holds an expected value cannot get there by composing CompareAndSetMetadataKey
// on the guard key with a second, unconditional write of the rest: the second
// write has no fence at all, so a fresh writer landing between the two calls
// gets its own keys clobbered by the first caller's unconditional write. That
// composition only narrows the race; it does not close it. See the dr-y6ndy
// notes on bin/gc-worktree-finalize for the concrete shape (an attempt-scoped
// worktree pointer clear racing a fresh `gc worktree ensure`).
//
// MetadataGuardedClearer is the narrow capability that closes it: guard check
// and every key mutation happen as ONE indivisible store operation. It is
// soundly implementable on stores with real in-process (or single-transaction)
// mutual exclusion — MemStore and FileStore, both lock-protected end to end —
// but NOT on BdStore: bd's CLI exposes no metadata-value precondition at all
// (checked exhaustively against the installed bd 1.2.1: `bd update --help`
// offers only --if-assignee and --if-status, no --if-revision and no
// metadata-scoped equivalent), so BdStore declares no implementation and
// MetadataGuardedClearerFor reports the capability absent. A caller must treat
// that absence as a hard failure (ErrConditionalWriteUnsupported) rather than
// falling back to an unconditional write — the whole reason this seam exists
// is that the unconditional fallback is the bug.
package beads

import "fmt"

// GuardedClearOutcome is the durable classification of one guarded clear
// attempt. Skipped is an ordinary race result, not an error: the guard no
// longer matched, so nothing was written.
type GuardedClearOutcome string

const (
	// GuardedClearApplied means the guard matched and every key was cleared
	// (with any terminal key/values set) in one indivisible write.
	GuardedClearApplied GuardedClearOutcome = "cleared"
	// GuardedClearSkipped means the guard key's current value no longer
	// matched the expected value. Nothing was written.
	GuardedClearSkipped GuardedClearOutcome = "skipped"
)

// MetadataGuardedClearer is implemented by stores that can atomically clear a
// set of metadata keys, and set a terminal set of key/values, as one
// indivisible operation gated on a single guard key's current value.
//
// ClearMetadataIfKeyMatches clears every key in clearKeys and sets every
// key/value in terminal, atomically, iff metadata[guardKey] == guardExpected
// at the instant of the write. expected == "" matches a key that is absent or
// present with the empty value, matching CompareAndSetMetadataKey's contract.
// Returns (true, nil) when the guard matched and the write applied, (false,
// nil) on a genuine guard mismatch (nothing written), and (false, err) for
// anything else (nothing written).
type MetadataGuardedClearer interface {
	ClearMetadataIfKeyMatches(id, guardKey, guardExpected string, clearKeys []string, terminal map[string]string) (bool, error)
}

// MetadataGuardedClearerHandleProvider exposes a guarded-clear handle for
// stores whose capability depends on wrapped runtime state, mirroring
// MetadataCASWriterHandleProvider.
type MetadataGuardedClearerHandleProvider interface {
	MetadataGuardedClearerHandle() (MetadataGuardedClearer, bool)
}

// MetadataGuardedClearerFor returns the guarded-clear capability for store
// when one is available. It follows wrapper-declared resolution targets for
// the same reason MetadataCASWriterFor does.
func MetadataGuardedClearerFor(store Store) (MetadataGuardedClearer, bool) {
	if store == nil {
		return nil, false
	}
	store = followConditionalWritesResolveTarget(store)
	if writer, ok := store.(MetadataGuardedClearer); ok {
		return writer, true
	}
	if provider, ok := store.(MetadataGuardedClearerHandleProvider); ok {
		return provider.MetadataGuardedClearerHandle()
	}
	return nil, false
}

// ApplyGuardedMetadataClear performs one capability-backed guarded metadata
// clear. A capability-absent store returns ErrConditionalWriteUnsupported
// without attempting any write — the caller must fail closed, never fall back
// to an unconditional clear.
//
// A skip is classified via a live List read (ListQuery{IDs: [id], Live: true}),
// never Get: Get may be served from a CachingStore's cache, and a lifecycle
// gate deciding whether it is safe to have skipped a clear must observe the
// backing store's current state, not a stale snapshot (dr-y6ndy A2). The live
// read result is authoritative, not a liveness probe: classifying a skip
// requires exactly one bead back for id, and that bead's CURRENT guardKey
// value must differ from guardExpected. Zero beads, more than one, or a
// guard value that still equals guardExpected all contradict the writer's own
// "not applied" verdict, so they fail closed with an error rather than being
// reported as an ordinary Skipped result.
func ApplyGuardedMetadataClear(store Store, id, guardKey, guardExpected string, clearKeys []string, terminal map[string]string) (GuardedClearOutcome, error) {
	writer, ok := MetadataGuardedClearerFor(store)
	if !ok {
		return "", fmt.Errorf("guarded metadata clear for %q: %w", id, ErrConditionalWriteUnsupported)
	}

	applied, err := writer.ClearMetadataIfKeyMatches(id, guardKey, guardExpected, clearKeys, terminal)
	if err != nil {
		return "", fmt.Errorf("guarded metadata clear for %q key %q: %w", id, guardKey, err)
	}
	if applied {
		return GuardedClearApplied, nil
	}

	// Skip confirmed via a live List, not Get: see the doc comment above.
	found, err := store.List(ListQuery{IDs: []string{id}, Live: true})
	if err != nil {
		return "", fmt.Errorf("verify guarded metadata clear skip for %q: %w", id, err)
	}
	if len(found) != 1 {
		return "", fmt.Errorf("verify guarded metadata clear skip for %q: live List returned %d beads, want exactly 1", id, len(found))
	}
	if current := found[0].Metadata[guardKey]; current == guardExpected {
		return "", fmt.Errorf("verify guarded metadata clear skip for %q: guard key %q still equals expected %q after a not-applied writer result", id, guardKey, guardExpected)
	}
	return GuardedClearSkipped, nil
}
