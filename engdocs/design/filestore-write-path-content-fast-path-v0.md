---
title: "FileStore Write-Path Content-Check Fast Path"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-09-19 |
| Author(s) | gascity-worker-3 |
| Issue | gc-q9m9tk |
| Supersedes | N/A |

## Summary

Every mutating `FileStore` operation (`Create`, `Update`, `Close`, `Delete`,
`SetMetadata`, `DepAdd`, ...) called `reloadFromDisk()` unconditionally before
mutating, which re-reads and fully JSON-unmarshals the entire store file. On a
79 MB city store, with the order dispatcher creating runs continuously, this
made `json.Unmarshal` (and the matching `json.MarshalIndent` on save) the
dominant cost of a live supervisor process, measured at 77.7% of one core
sustained over 10h14m (gc-q9m9tk).

## Invariant

`reloadFromDisk` must never build a write on a stale in-memory copy when
another process (or another `FileStore` handle) has mutated the file since
this instance last synced with it. This invariant is unconditional: it holds
regardless of how the mutation happened to look from the outside (same size,
same mtime, or neither).

## Staleness detector

`FileStore` now caches `lastRawBytes`: the exact bytes it last synced its
in-memory state with, from either a prior `reloadFromDisk` parse or its own
`save()`. On every `reloadFromDisk` call it still reads the file (cheap; the
profile attributes ~1% of cost to the read itself), but only re-parses when
the freshly read bytes are **not byte-identical** to `lastRawBytes`. `nil`
(unknown) always forces a parse.

This is a content check, not a metadata heuristic: it compares the actual
bytes, not `mtime`/`size`. The read path's own fast path
(`refreshReadStateLocked`) already uses an `mtime`+`size` shortcut to decide
*whether to re-read at all*, and keeps that lighter but coarser signal
unchanged — it does not need the same guarantee, because a read that misses a
same-size/same-mtime coincidence just serves the previous state a little
longer, not a lost mutation. The write path cannot accept that risk, so it
was given a stronger, always-correct detector instead of the read path's
detector.

## Why this is safe even in the coincidence case

A detector based on `mtime`+`size` cannot distinguish "unchanged" from "an
external write that happened to land on the same size and the same mtime
tick" — this is a real, constructible case (see
`TestFileStoreMutatorContentCheckSurvivesIdenticalStat`), not a hypothetical.
Comparing full byte content has no such blind spot: two byte-identical files
are the same content by definition, and any real external write — however it
happens to compare on size or mtime — always changes at least one byte
(bead ID counters, timestamps, or the changed field itself).

## What happens on each way the detector can be wrong

- **False "unchanged" is impossible.** The comparison is exact byte equality
  over the whole file; there is no metadata proxy to be fooled.
- **False "changed" (unnecessary parse)** happens whenever `lastRawBytes` is
  `nil` (first mutation after open, or after a read error) or whenever
  anything outside this handle's own last write touched the file. This costs
  one avoidable `json.Unmarshal` and is always safe — it is exactly today's
  (pre-fix) behavior, never worse.

## Scope

- No on-disk format change: `fileData`'s shape and `MarshalIndent` formatting
  are untouched, per gc-q9m9tk's directive not to bundle that decision here.
- No new caching layer that outlives the lock: `lastRawBytes` is populated
  and consumed only inside the existing `fmu`+cross-process-lock critical
  section, exactly like `freshness` already is.
- The read path (`refreshReadStateLocked`, `fileFreshness`) is unchanged.

## Measurement

`TestFileStoreReloadSkipsParseWhenFileUnchanged` proves two consecutive
mutating operations with no external change between them parse the file once
(via the internal `reloadParses` counter), not twice.
`TestFileStoreReloadParsesOnExternalWrite` proves a genuine external mutation
between two mutating operations is observed and not lost. Both tests were
run against a deliberately unconditional-skip variant of the fix and failed
as expected, confirming they exercise the safety property and not just the
happy path.
