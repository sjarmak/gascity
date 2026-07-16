package beadmeta

import "strings"

// The disarm contract: gc.disarmed=true marks a bead as permanently
// do-not-execute. Unlike status=blocked — which is only a soft disarm, since a
// status transition or a cache reconcile can flip a step back to open — the flag
// is a durable property of the bead itself. Nothing derives it from prose: the
// check is purely structural, so no judgment lives in Go.
//
// Two decoders read this key and they disagree on its Go type:
//
//   - The hook path unmarshals raw work_query JSON into map[string]any. bd
//     type-infers `--set-metadata gc.disarmed=true` into a JSON boolean, so the
//     value arrives as bool(true).
//   - The controller demand path reads beads.Bead, whose StringMap decoder
//     coerces that same boolean to the string "true".
//
// Both shapes must mean the same thing, so both helpers below funnel into
// disarmedString/disarmedValue rather than re-deciding per call site.

// IsDisarmed reports whether metadata read through beads.Bead's StringMap
// decoder marks the bead do-not-execute.
func IsDisarmed(md map[string]string) bool {
	v, ok := md[DisarmedMetadataKey]
	if !ok {
		return false
	}
	return disarmedString(v)
}

// IsDisarmedRaw reports whether metadata decoded from raw JSON (map[string]any)
// marks the bead do-not-execute. It accepts bd's boolean shape as well as the
// string form.
func IsDisarmedRaw(md map[string]any) bool {
	v, ok := md[DisarmedMetadataKey]
	if !ok {
		return false
	}
	return disarmedValue(v)
}

// disarmedValue resolves a raw JSON value. An unexpected type fails closed: the
// key's presence means an operator deliberately wrote a do-not-execute marker,
// and running a bead whose marker we could not parse is the exact failure the
// contract exists to prevent.
func disarmedValue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return disarmedString(t)
	default:
		return true
	}
}

// disarmedString resolves the string form. An absent key never reaches here —
// callers treat absence as not-disarmed, because every non-bd work_query
// override and test fixture omits the key and failing closed there would strand
// the fleet.
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
