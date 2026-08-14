package runproj

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
)

// TestProjectionIsUnchangedByDroppingRedundantReEmissions is the safety proof
// for the dr-l09kl fix. That fix stops the cache reconciler from re-emitting a
// payload it has already emitted for a bead; the risk it has to clear is that
// ColdLoad folds this log as an event-sourced projection with no store
// fallback, so anything the stream stops carrying is simply gone from the run
// view.
//
// The fold is last-write-wins over full snapshots, and first-seen order is set
// by a bead's FIRST event, which the fix never suppresses. So a stream with the
// byte-identical repeats removed must reconstruct exactly what the full stream
// reconstructs: same beads, same field values, same order, same cursor
// semantics. The check is written against the projector rather than argued,
// because "the repeats were redundant" is exactly the claim under test.
func TestProjectionIsUnchangedByDroppingRedundantReEmissions(t *testing.T) {
	full := floodedStream(t)
	deduped := dropRepeatedPayloads(full)

	if len(deduped) >= len(full) {
		t.Fatalf("fixture built no redundancy: %d events deduped from %d; "+
			"the test would pass trivially", len(deduped), len(full))
	}

	fullProj := NewProjector()
	if changed := fullProj.Apply(full); !changed {
		t.Fatal("full stream folded to no change")
	}
	dedupProj := NewProjector()
	if changed := dedupProj.Apply(deduped); !changed {
		t.Fatal("deduped stream folded to no change")
	}

	if !reflect.DeepEqual(fullProj.Beads(), dedupProj.Beads()) {
		t.Fatalf("projection differs after dropping byte-identical re-emissions\nfull:    %+v\ndeduped: %+v",
			fullProj.Beads(), dedupProj.Beads())
	}
	if fullProj.DecodeMisses() != dedupProj.DecodeMisses() {
		t.Fatalf("decode misses = %d full / %d deduped", fullProj.DecodeMisses(), dedupProj.DecodeMisses())
	}
}

// floodedStream mirrors the production shape the reconcile echo produced: an
// interleaved series of full bead snapshots in which most events for a bead
// repeat the previous one verbatim, with real transitions (a status change, a
// new bead, a close, a delete) scattered among them so the fold has something
// to get wrong.
func floodedStream(t *testing.T) []events.Event {
	t.Helper()
	blocked := true
	unblocked := false
	priority := 1

	alphaOpen := beads.Bead{
		ID: "gpk-alpha", Title: "Finalize the work item", Type: "task", Status: "open",
		Priority: &priority, Description: "a long description of the kind that made up 70% of the log",
		IsBlocked: &blocked, Metadata: map[string]string{"gc.root_bead_id": "gpk-root"},
	}
	alphaRunning := alphaOpen
	alphaRunning.Status = "in_progress"
	alphaRunning.IsBlocked = &unblocked
	betaOpen := beads.Bead{ID: "gpk-beta", Title: "Second item", Type: "task", Status: "open", IsBlocked: &unblocked}
	gammaOpen := beads.Bead{ID: "gpk-gamma", Title: "Third item", Type: "task", Status: "open", IsBlocked: &unblocked}
	gammaClosed := gammaOpen
	gammaClosed.Status = "closed"

	plan := []struct {
		typ    string
		bead   beads.Bead
		repeat int
	}{
		{events.BeadCreated, alphaOpen, 0},
		{events.BeadUpdated, alphaOpen, 6},
		{events.BeadCreated, betaOpen, 0},
		{events.BeadUpdated, betaOpen, 4},
		{events.BeadUpdated, alphaRunning, 5},
		{events.BeadCreated, gammaOpen, 0},
		{events.BeadUpdated, gammaOpen, 3},
		{events.BeadClosed, gammaClosed, 2},
		{events.BeadUpdated, betaOpen, 3},
	}

	var out []events.Event
	seq := uint64(0)
	for _, step := range plan {
		payload, err := json.Marshal(step.bead)
		if err != nil {
			t.Fatalf("Marshal %s: %v", step.bead.ID, err)
		}
		for i := 0; i <= step.repeat; i++ {
			seq++
			out = append(out, events.Event{
				Seq:     seq,
				Type:    step.typ,
				Actor:   "cache-reconcile",
				Subject: step.bead.ID,
				Payload: append(json.RawMessage(nil), payload...),
			})
		}
	}
	// A delete of a bead that was never otherwise touched, so the two streams
	// have to agree about removal ordering too.
	seq++
	deleted, err := json.Marshal(beads.Bead{ID: "gpk-delta", Title: "Removed", Type: "task", Status: "open"})
	if err != nil {
		t.Fatalf("Marshal delta: %v", err)
	}
	out = append(out,
		events.Event{Seq: seq, Type: events.BeadCreated, Actor: "cache-reconcile", Subject: "gpk-delta", Payload: deleted},
		events.Event{Seq: seq + 1, Type: events.BeadDeleted, Actor: "cache-reconcile", Subject: "gpk-delta", Payload: deleted},
	)
	return out
}

// dropRepeatedPayloads removes every event whose (type, subject, payload) is
// byte-identical to the last event already kept for that subject — the exact
// class of event the fix stops the reconciler from writing.
func dropRepeatedPayloads(in []events.Event) []events.Event {
	last := make(map[string]string, len(in))
	out := make([]events.Event, 0, len(in))
	for _, e := range in {
		key := e.Type + "\x00" + string(e.Payload)
		if last[e.Subject] == key {
			continue
		}
		last[e.Subject] = key
		out = append(out, e)
	}
	return out
}
