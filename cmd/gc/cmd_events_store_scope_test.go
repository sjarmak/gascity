package main

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/events"
)

func TestLocalWireEventPreservesStoreScope(t *testing.T) {
	e := events.Event{
		Type: events.ExecutionWorkAssociated, Subject: "same-id", RunID: "same-id",
		SubjectStoreRef: "rig:alpha", RunStoreRef: "class:graph",
	}
	encoded, err := json.Marshal(localWireEvent(e, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	var got events.Event
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.SubjectStoreRef != e.SubjectStoreRef || got.RunStoreRef != e.RunStoreRef {
		t.Fatalf("store scope lost: subject=%q run=%q", got.SubjectStoreRef, got.RunStoreRef)
	}
}

func TestTypedWireEventsPreserveStoreScope(t *testing.T) {
	input := []byte(`{"type":"execution.work_associated","seq":1,"actor":"producer","ts":"2026-09-10T00:00:00Z","subject":"same-id","run_id":"same-id","subject_store_ref":"rig:alpha","run_store_ref":"class:graph","city":"test-city","payload":{}}`)
	var city genclient.TypedEventStreamEnvelope
	var supervisor genclient.TypedTaggedEventStreamEnvelope
	for _, target := range []any{&city, &supervisor} {
		if err := json.Unmarshal(input, target); err != nil {
			t.Fatal(err)
		}
	}
	cityEvent, err := cityWireEventFromTyped(city)
	if err != nil {
		t.Fatal(err)
	}
	supervisorEvent, err := supervisorWireEventFromTyped(supervisor)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{cityEvent, supervisorEvent} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got events.Event
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got.SubjectStoreRef != "rig:alpha" || got.RunStoreRef != "class:graph" {
			t.Errorf("%T store scope lost: subject=%q run=%q", value, got.SubjectStoreRef, got.RunStoreRef)
		}
	}
}
