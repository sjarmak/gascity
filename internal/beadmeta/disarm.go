package beadmeta

import (
	"fmt"
	"strings"
)

// The disarm contract: gc.disarmed=true marks a bead as permanently
// do-not-execute. Unlike status=blocked — which is only a soft disarm, since a
// status transition or a cache reconcile can flip a step back to open — the flag
// is a durable property of the bead itself. Nothing derives it from prose: the
// check is purely structural, so no judgment lives in Go.
//
// The key is read through two Go shapes and they must never disagree. The
// reconciler's demand path counts beads.Bead metadata (a string map); the
// worker's claim path reads raw work_query JSON (map[string]any). If demand
// counts a bead the claim path refuses, the pool spawns a session that finds
// nothing, idle-exits, and gets respawned forever — the spawn loop this flag
// exists to prevent.
//
// Three decoders produce the string shape — beads.StringMap, beads.parseMetadata
// (the doltlite SQL read path), and cmd/gc's hookBeadMetadata — and all three
// coerce identically: a non-string JSON value keeps its raw text, so bd's
// type-inferred boolean from `--set-metadata gc.disarmed=true` arrives as
// "true". The lone exception is the literal null, which SUCCEEDS encoding/json's
// string unmarshal as a documented no-op and lands as "", indistinguishable from
// a flag an operator explicitly cleared (pinned by
// beads.TestStringMapCollapsesJSONNullToPresentEmpty).
//
// So IsDisarmedRaw renders its value into that same string representation and
// both helpers share disarmedString. Agreement is structural rather than a
// property two switch statements must keep remembering — which is exactly what
// the null arm previously got wrong.

// IsDisarmed reports whether metadata read through one of the string-shaped
// decoders marks the bead do-not-execute. A missing key indexes to "", which
// disarmedString already reads as not-disarmed, so absence needs no guard here.
func IsDisarmed(md map[string]string) bool {
	return disarmedString(md[DisarmedMetadataKey])
}

// IsDisarmedRaw reports whether metadata decoded from raw JSON (map[string]any)
// marks the bead do-not-execute. It funnels through the same decision function
// as IsDisarmed so the two cannot drift;
// beads.TestDisarmDecoderParityThroughRealDecoders pins that against the
// production decoders.
func IsDisarmedRaw(md map[string]any) bool {
	return disarmedString(disarmedRawText(md[DisarmedMetadataKey]))
}

// disarmedRawText renders a raw JSON value as the string the string-shaped
// decoders would have produced for it.
//
// nil covers an absent key (indexing a map yields the zero value) and an
// explicit JSON null alike, and both render "". Reading null as not-disarmed is
// not a policy preference: the string decoders destroy the null/cleared
// distinction before any reader runs, and "" must stay not-disarmed because
// operators clear the flag to re-arm a bead. Failing closed on null would
// therefore only be reachable on this side of the contract, and that asymmetry
// IS the demand-counts/claim-refuses split.
//
// Every other unparseable shape keeps a non-empty rendering and still fails
// closed in disarmedString, so the interlock survives on every value where it is
// representable at all.
func disarmedRawText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

// disarmedString resolves the string form. The empty string covers an absent
// key, an explicitly cleared one, and a JSON null, and all three read as
// not-disarmed: every non-bd work_query override and test fixture omits the key,
// so failing closed on absence would strand the fleet.
//
// Anything else present but unreadable fails closed: someone deliberately wrote
// a do-not-execute marker we could not parse, and running the bead anyway is the
// exact failure the contract exists to prevent.
func disarmedString(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true
	case "", "false":
		return false
	default:
		return true
	}
}
