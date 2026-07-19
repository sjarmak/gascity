package formula

import "strings"

// CanonicalTOMLExt is the canonical extension for formula TOML files under a
// formulas/ directory (formulas/<name>.toml).
const CanonicalTOMLExt = ".toml"

// LegacyTOMLExt is the supported infixed extension for formula TOML files
// (formulas/<name>.formula.toml). The exported name is retained for existing
// internal callers, but this filename spelling is intentionally accepted.
const LegacyTOMLExt = ".formula.toml"

// IsTOMLFilename reports whether path names a TOML formula file in either
// supported TOML form.
func IsTOMLFilename(path string) bool {
	// Check legacy suffix first to stay symmetric with TrimTOMLFilename; the
	// result is the same either way (both suffixes end in ".toml"), but the
	// symmetry avoids a future-reordering hazard.
	return strings.HasSuffix(path, LegacyTOMLExt) || strings.HasSuffix(path, CanonicalTOMLExt)
}

// TrimTOMLFilename returns the formula name encoded in a TOML filename.
// The optional ".formula" infix is not part of the symbolic formula name.
func TrimTOMLFilename(path string) (string, bool) {
	switch {
	case strings.HasSuffix(path, LegacyTOMLExt):
		return strings.TrimSuffix(path, LegacyTOMLExt), true
	case strings.HasSuffix(path, CanonicalTOMLExt):
		return strings.TrimSuffix(path, CanonicalTOMLExt), true
	default:
		return "", false
	}
}

// CanonicalName returns the symbolic formula name for any accepted spelling:
// resolution treats "work", "work.toml", "work.formula.toml", and
// "work.formula.json" as the same formula, so every identity derived from a
// formula name (locks, convoy invocation keys, workflow root keys) must go
// through this same strip or aliased spellings fracture the identity.
func CanonicalName(name string) string {
	if trimmed, ok := TrimTOMLFilename(name); ok {
		return trimmed
	}
	return strings.TrimSuffix(name, FormulaExtJSON)
}
