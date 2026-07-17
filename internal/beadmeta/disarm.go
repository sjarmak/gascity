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
// decoder marks the bead do-not-execute. A missing key indexes to "", which
// disarmedString already reads as not-disarmed, so absence needs no guard here.
func IsDisarmed(md map[string]string) bool {
	return disarmedString(md[DisarmedMetadataKey])
}

// IsDisarmedRaw reports whether metadata decoded from raw JSON (map[string]any)
// marks the bead do-not-execute. It accepts bd's boolean shape as well as the
// string form.
//
// The presence guard is load-bearing here, unlike in IsDisarmed: a JSON null
// decodes to a present key holding a nil value, and disarmedValue fails closed
// on it. Drop the guard and an absent key becomes indistinguishable from that
// null, disarming every bead that never carried the marker.
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

// disarmedString resolves the string form. The empty string covers both an
// absent key and an explicitly cleared one, and both read as not-disarmed:
// every non-bd work_query override and test fixture omits the key, so failing
// closed on absence would strand the fleet.
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
