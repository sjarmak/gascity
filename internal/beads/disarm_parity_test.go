package beads

import (
	"context"
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

// TestMergeCacheMetadataPreservingDisarm is the direct unit test for the
// cache-merge function underlying AC3 (gc-u6an): "reconciliation cannot erase
// a durable disarm." It covers the normal-preserve path (a metadata patch
// that never mentions gc.disarmed must not drop a cached disarm) and the
// explicit-overwrite path (a patch that carries the key, any value, always
// wins) side by side, since a fix to one that breaks the other reproduces
// exactly the class of gap the gc-u6an review cycles kept finding.
func TestMergeCacheMetadataPreservingDisarm(t *testing.T) {
	tests := []struct {
		name    string
		current StringMap
		patch   StringMap
		want    StringMap
	}{
		{
			// This is also the wire shape of `bd update <id> --unset-metadata
			// gc.disarmed`: the key is removed, so the patch omits it — the
			// same shape as an unrelated touch. mergeCacheMetadataPreservingDisarm
			// cannot distinguish the two, and resolves toward the documented
			// safe direction (preserve). See
			// TestApplyEventOperatorUnsetDisarmedIsNotReflectedUntilReconcile
			// for the end-to-end version and gc-efqz for the tracked gap.
			name:    "unrelated key patch preserves cached disarm (also the unset-metadata wire shape, gc-efqz)",
			current: StringMap{beadmeta.DisarmedMetadataKey: "true"},
			patch:   StringMap{"gc.other": "x"},
			want:    StringMap{"gc.other": "x", beadmeta.DisarmedMetadataKey: "true"},
		},
		{
			name:    "nil patch preserves cached disarm",
			current: StringMap{beadmeta.DisarmedMetadataKey: "true"},
			patch:   nil,
			want:    StringMap{beadmeta.DisarmedMetadataKey: "true"},
		},
		{
			name:    "explicit overwrite to empty clears",
			current: StringMap{beadmeta.DisarmedMetadataKey: "true"},
			patch:   StringMap{beadmeta.DisarmedMetadataKey: ""},
			want:    StringMap{beadmeta.DisarmedMetadataKey: ""},
		},
		{
			name:    "explicit overwrite to false clears",
			current: StringMap{beadmeta.DisarmedMetadataKey: "true"},
			patch:   StringMap{beadmeta.DisarmedMetadataKey: "false"},
			want:    StringMap{beadmeta.DisarmedMetadataKey: "false"},
		},
		{
			name:    "not currently disarmed, unrelated patch stays untouched",
			current: StringMap{"gc.other": "old"},
			patch:   StringMap{"gc.other": "new"},
			want:    StringMap{"gc.other": "new"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeCacheMetadataPreservingDisarm(tc.current, tc.patch)
			if len(got) != len(tc.want) {
				t.Fatalf("mergeCacheMetadataPreservingDisarm() = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("mergeCacheMetadataPreservingDisarm()[%q] = %q, want %q (full got=%v)", k, got[k], v, got)
				}
			}
		})
	}
}

// TestApplyEventPreservesDisarmedAcrossUnrelatedMetadataPatch is the
// end-to-end (ApplyEvent) version of AC3: a metadata-touching cache event for
// an unrelated key must not silently drop a cached gc.disarmed=true, since
// that cache feeds the controller's demand snapshot and a dropped flag hands
// the bead to a worker (gc-u6an).
func TestApplyEventPreservesDisarmedAcrossUnrelatedMetadataPatch(t *testing.T) {
	backing := NewMemStore()
	created, err := backing.Create(Bead{
		Title:  "disarmed work",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.DisarmedMetadataKey: "true",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cs := NewCachingStoreForTest(backing, nil)
	if err := cs.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	cs.ApplyEvent("bead.updated", json.RawMessage(`{"id":"`+created.ID+`","status":"open","metadata":{"gc.other":"x"}}`))

	got, err := cs.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !beadmeta.IsDisarmed(got.Metadata) {
		t.Fatalf("gc.disarmed dropped by an unrelated metadata patch: metadata=%v", got.Metadata)
	}
}

// TestApplyEventOperatorUnsetDisarmedIsNotReflectedUntilReconcile pins the
// gc-efqz gap (split out of gc-u6an cycle-2 review finding 4): a metadata
// event whose payload is wire-identical to an operator's genuine
// `bd update <id> --unset-metadata gc.disarmed` (key absent from the patch)
// is indistinguishable, at this cache layer, from a patch that simply never
// touched gc.disarmed. mergeCacheMetadataPreservingDisarm resolves that
// ambiguity toward the safe direction (preserve), so the cache keeps serving
// disarmed=true here — the clear only takes effect once a full reconcile
// re-reads bd directly (or once ApplyEvent sees the key with an explicit
// overwrite value, e.g. an operator using `--set-metadata gc.disarmed=false`
// instead of `--unset-metadata`).
//
// This test intentionally pins the CURRENT behavior, not the desired end
// state — see gc-efqz for the design work needed (an UpdatedAt-based
// staleness comparison or an explicit-deletion wire signal from bd) before
// this can safely flip to "trust the absence."
func TestApplyEventOperatorUnsetDisarmedIsNotReflectedUntilReconcile(t *testing.T) {
	backing := NewMemStore()
	created, err := backing.Create(Bead{
		Title:  "disarmed work",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.DisarmedMetadataKey: "true",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cs := NewCachingStoreForTest(backing, nil)
	if err := cs.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	// Simulate bd's hook payload for `bd update <id> --unset-metadata
	// gc.disarmed`: the key is removed rather than set, so the event's
	// metadata object is present but does not carry gc.disarmed at all —
	// wire-identical to a patch that simply never touched the flag.
	cs.ApplyEvent("bead.updated", json.RawMessage(`{"id":"`+created.ID+`","status":"open","metadata":{}}`))

	got, err := cs.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !beadmeta.IsDisarmed(got.Metadata) {
		t.Fatalf("expected the known gc-efqz gap (cache still reports disarmed after an unset-shaped event); "+
			"metadata=%v — if this now fails, the gap may be fixed: update this test and close gc-efqz instead of loosening it", got.Metadata)
	}
}
