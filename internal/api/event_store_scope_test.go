package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/events"
)

func TestEventStoreScopeSurvivesWireProjections(t *testing.T) {
	e := events.Event{
		Type: events.ExecutionWorkAssociated, Subject: "same-id", RunID: "same-id",
		SubjectStoreRef: "rig:alpha", RunStoreRef: "class:graph",
	}
	listed, ok := toWireEvent(e)
	if !ok {
		t.Fatal("list projection rejected event")
	}
	streamed, err := wireEventFrom(e, nil)
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := wireTaggedEventFrom(events.TaggedEvent{Event: e}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{listed, streamed, tagged} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"subject_store_ref": `"rig:alpha"`, "run_store_ref": `"class:graph"`} {
			if string(fields[key]) != want {
				t.Errorf("%T %s = %s, want %s", value, key, fields[key], want)
			}
		}
	}
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	_ = typedEventStreamEnvelopeSchema{}.Schema(registry)
	_ = typedTaggedEventStreamEnvelopeSchema{}.Schema(registry)
	for name, schema := range registry.Map() {
		if !strings.HasPrefix(name, "TypedEventStreamEnvelope") && !strings.HasPrefix(name, "TypedTaggedEventStreamEnvelope") {
			continue
		}
		if schema.Properties["run_id"] == nil {
			continue
		}
		for _, key := range []string{"subject_store_ref", "run_store_ref"} {
			if schema.Properties[key] == nil {
				t.Errorf("%s lacks %s", name, key)
			}
		}
	}
}
