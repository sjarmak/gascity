package beadmeta

import (
	"encoding/json"
	"testing"
)

// TestIsDisarmedRawAcceptsBdBooleanShape is the regression that anchors the
// whole contract. bd type-infers `--set-metadata gc.disarmed=true` into a JSON
// boolean, so a reader that only accepts the string "true" never fires. Verified
// against live bd: both `bd show --json` and `bd ready --json` emit
// "gc.disarmed":true as a boolean.
//
// The fixture is a RAW JSON literal on purpose. Decoding through beads.Bead
// would coerce the boolean to "true" via StringMap and the test would pass while
// production failed.
func TestIsDisarmedRawAcceptsBdBooleanShape(t *testing.T) {
	const row = `{"id":"gc-1","status":"open","metadata":{"gc.disarmed":true}}`

	var decoded map[string]any
	if err := json.Unmarshal([]byte(row), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	md, ok := decoded["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not an object: %T", decoded["metadata"])
	}
	if !IsDisarmedRaw(md) {
		t.Fatal("IsDisarmedRaw = false for bd's boolean shape; the disarm flag would never fire on the hook path")
	}
}

func TestIsDisarmedRaw(t *testing.T) {
	tests := []struct {
		name string
		val  any
		set  bool
		want bool
	}{
		{name: "bd boolean true", val: true, set: true, want: true},
		{name: "bd boolean false", val: false, set: true, want: false},
		{name: "string true", val: "true", set: true, want: true},
		{name: "string TRUE case-insensitive", val: "TRUE", set: true, want: true},
		{name: "string padded", val: "  true  ", set: true, want: true},
		{name: "string false", val: "false", set: true, want: false},
		{name: "cleared to empty string", val: "", set: true, want: false},
		// Absent -> NOT disarmed. Required: every non-bd work_query override and
		// test fixture omits the key; failing closed here would strand the fleet.
		{name: "key absent", set: false, want: false},
		// Explicit null -> NOT disarmed. Required for the same reason, one layer
		// down: the string-shaped decoders collapse null to "" (proven by
		// beads.TestStringMapCollapsesJSONNullToPresentEmpty), so failing closed
		// here would be reachable only on the raw side — demand would count the
		// bead while claim refused it, which is the spawn loop itself.
		{name: "explicit JSON null", val: nil, set: true, want: false},
		// Present but unreadable -> disarmed (fail closed). Someone deliberately
		// wrote a do-not-execute marker we could not parse; executing anyway is
		// the exact failure this contract exists to prevent.
		{name: "unrecognized string fails closed", val: "yes", set: true, want: true},
		{name: "unexpected number fails closed", val: float64(1), set: true, want: true},
		{name: "unexpected object fails closed", val: map[string]any{"a": "b"}, set: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md := map[string]any{"gc.routed_to": "worker"}
			if tc.set {
				md[DisarmedMetadataKey] = tc.val
			}
			if got := IsDisarmedRaw(md); got != tc.want {
				t.Errorf("IsDisarmedRaw(%#v) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestIsDisarmedStringMapShape covers the OTHER decoder. beads.Bead carries
// Metadata as a StringMap whose coercing decoder turns bd's boolean true into
// the string "true", so the Go demand path sees a different type than the hook
// filter does for the very same key.
func TestIsDisarmedStringMapShape(t *testing.T) {
	tests := []struct {
		name string
		val  string
		set  bool
		want bool
	}{
		{name: "coerced from bd boolean", val: "true", set: true, want: true},
		{name: "coerced false", val: "false", set: true, want: false},
		{name: "padded and cased", val: " True ", set: true, want: true},
		{name: "cleared", val: "", set: true, want: false},
		{name: "absent", set: false, want: false},
		{name: "unrecognized fails closed", val: "banana", set: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md := map[string]string{"gc.routed_to": "worker"}
			if tc.set {
				md[DisarmedMetadataKey] = tc.val
			}
			if got := IsDisarmed(md); got != tc.want {
				t.Errorf("IsDisarmed(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestIsDisarmedNilMaps guards the hot reconcile path: a bead with no metadata
// at all omits the key entirely (confirmed against live bd — 11 of 38 ready rows
// carried no metadata object).
func TestIsDisarmedNilMaps(t *testing.T) {
	if IsDisarmed(nil) {
		t.Error("IsDisarmed(nil) = true, want false")
	}
	if IsDisarmedRaw(nil) {
		t.Error("IsDisarmedRaw(nil) = true, want false")
	}
}

// TestDisarmedDecodersAgree is a local unit pin on the cross-decoder invariant:
// the same bd value, read through either decoder, must mean the same thing.
//
// It is deliberately limited to the shapes its hand-rolled mirror below can
// represent faithfully — string, bool, and nil. beadmeta cannot import beads to
// use the real StringMap (beads imports beadmeta, so that would cycle), and a
// mirror is exactly what let the null gap through: it modeled bool and string
// and silently defaulted everything else to "". Do not extend this table with
// numbers or objects; the mirror would render them "" and report a disagreement
// production does not have.
//
// beads.TestDisarmDecoderParityThroughRealDecoders is the authoritative parity
// test — it feeds raw JSON through the production decoders and covers the full
// shape table.
func TestDisarmedDecodersAgree(t *testing.T) {
	for _, raw := range []any{true, false, "true", "false", "", "banana", nil} {
		rawMD := map[string]any{DisarmedMetadataKey: raw}

		// Mirror the string decoders' coercion: booleans render as
		// "true"/"false", and a JSON null collapses to "" (the zero value left
		// by encoding/json's no-op null unmarshal).
		var coerced string
		switch v := raw.(type) {
		case bool:
			if v {
				coerced = "true"
			} else {
				coerced = "false"
			}
		case string:
			coerced = v
		}
		strMD := map[string]string{DisarmedMetadataKey: coerced}

		if got, want := IsDisarmedRaw(rawMD), IsDisarmed(strMD); got != want {
			t.Errorf("decoders disagree for %#v: IsDisarmedRaw=%v IsDisarmed=%v", raw, got, want)
		}
	}
}
