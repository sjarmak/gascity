package beads

import (
	"encoding/json"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// TestStringMapCollapsesJSONNullToPresentEmpty pins the decoder fact the entire
// gc.disarmed null contract rests on. Unmarshalling the JSON literal null into a
// string is a documented no-op in encoding/json: it leaves the zero value and
// returns a NIL error. StringMap's happy path (bdstore.go) therefore takes the
// err==nil branch and stores the key PRESENT with "" — byte-identical to a flag
// an operator explicitly cleared.
//
// That indistinguishability is why beadmeta cannot fail closed on null: the
// information is destroyed here, before any disarm reader runs. If this test
// ever fails, the null arm of the disarm contract is rebuildable and
// beadmeta.IsDisarmedRaw should be revisited alongside it.
func TestStringMapCollapsesJSONNullToPresentEmpty(t *testing.T) {
	var sm StringMap
	if err := json.Unmarshal([]byte(`{"k":null}`), &sm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := sm["k"]
	if !ok {
		t.Fatal("null key decoded as ABSENT; the disarm contract assumes present-with-empty")
	}
	if v != "" {
		t.Errorf("null key decoded to %q, want %q", v, "")
	}
}

// TestDisarmDecoderParityThroughRealDecoders is the cross-decoder invariant that
// actually matters: the same bd value, read through the two production shapes,
// must mean the same thing. The reconciler's demand path reads beads.Bead
// (StringMap); the worker's claim path reads raw work_query JSON
// (map[string]any). If they disagree, demand counts a bead claim refuses, the
// pool spawns a session that finds nothing and idle-exits, and the reconciler
// respawns it forever — the spawn loop the disarm flag exists to prevent.
//
// This lives in package beads on purpose. beadmeta's own TestDisarmedDecodersAgree
// can only hand-roll StringMap's coercion (beadmeta cannot import beads —
// caching_store_events.go imports beadmeta, so the reverse would cycle), and a
// hand-rolled mirror is exactly the thing that let the null gap through: it
// modeled bool and string and silently defaulted everything else. Here the real
// StringMap.UnmarshalJSON does the coercing, so no mirror can drift.
func TestDisarmDecoderParityThroughRealDecoders(t *testing.T) {
	tests := []struct {
		name string
		val  string // raw JSON value text, spliced into a real metadata object
	}{
		{name: "bd boolean true", val: `true`},
		{name: "bd boolean false", val: `false`},
		{name: "string true", val: `"true"`},
		{name: "string false", val: `"false"`},
		{name: "cleared to empty string", val: `""`},
		{name: "padded and cased", val: `"  TRUE  "`},
		// The shape that broke parity: null succeeds StringMap's string
		// unmarshal (-> present, "") but survives as a nil in map[string]any.
		{name: "explicit JSON null", val: `null`},
		// Unparseable shapes fail the string unmarshal on BOTH sides and keep
		// their text, so fail-closed still holds for them.
		{name: "unrecognized string", val: `"banana"`},
		{name: "unexpected number", val: `1`},
		{name: "unexpected object", val: `{"a":"b"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := []byte(`{"` + beadmeta.DisarmedMetadataKey + `":` + tc.val + `}`)

			var viaStringMap StringMap
			if err := json.Unmarshal(row, &viaStringMap); err != nil {
				t.Fatalf("StringMap unmarshal: %v", err)
			}
			var viaRaw map[string]any
			if err := json.Unmarshal(row, &viaRaw); err != nil {
				t.Fatalf("raw unmarshal: %v", err)
			}

			demand := beadmeta.IsDisarmed(viaStringMap)
			claim := beadmeta.IsDisarmedRaw(viaRaw)
			if demand != claim {
				t.Errorf("decoders disagree for %s: IsDisarmed(StringMap)=%v IsDisarmedRaw(raw)=%v; demand and claim must agree or the pool spawn-loops",
					tc.val, demand, claim)
			}
		})
	}
}
