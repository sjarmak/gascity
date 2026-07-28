package formula

import (
	"encoding/json"
	"fmt"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// Direction is the structural applicability axis distinguishing incoming work
// (for example maintainer review of a PR from someone else) from outgoing work
// (for example self-review of a PR you authored). It is compared literally: no
// direction is ever inferred from a formula or goal name, description, or any
// keyword. Inferring direction would be a Zero Framework Cognition violation.
type Direction string

const (
	// DirectionIncoming marks work that flows toward this actor from outside.
	DirectionIncoming Direction = "incoming"
	// DirectionOutgoing marks work that flows outward from this actor.
	DirectionOutgoing Direction = "outgoing"
)

// Applicability declares structural constraints on which goals a formula may
// bind to. It is a sibling of [Requirements]: pack-declarable, validated
// structurally with unknown keys and values rejected, and default-safe when
// absent. Absence on either the formula or the goal side is always compatible,
// so existing formulas and goals keep their current binding behavior.
type Applicability struct {
	// Direction, when set, restricts binding to goals declaring the same
	// direction. Empty means the formula places no direction constraint.
	Direction Direction `json:"direction,omitempty" toml:"direction,omitempty"`
}

// UnmarshalTOML decodes the [applicability] table and rejects unknown axes and
// invalid direction values so formula authors do not get a false sense of
// compatibility.
func (a *Applicability) UnmarshalTOML(data interface{}) error {
	raw, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Errorf("formula.applicability_invalid: applicability must be a table")
	}
	for key, value := range raw {
		switch key {
		case "direction":
			text, ok := value.(string)
			if !ok {
				return invalidDirectionTypeError()
			}
			dir, err := ParseDirection(text)
			if err != nil {
				return err
			}
			a.Direction = dir
		default:
			return unknownApplicabilityError(key)
		}
	}
	return nil
}

// UnmarshalJSON decodes legacy JSON formulas consistently with TOML formulas.
func (a *Applicability) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("formula.applicability_invalid: applicability must be an object: %w", err)
	}
	for key, value := range raw {
		switch key {
		case "direction":
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return invalidDirectionTypeError()
			}
			dir, err := ParseDirection(text)
			if err != nil {
				return err
			}
			a.Direction = dir
		default:
			return unknownApplicabilityError(key)
		}
	}
	return nil
}

// ParseDirection validates a raw direction string. Only the exact literals
// "incoming" and "outgoing" are accepted; every other value, including the
// empty string, is rejected. Callers reading an absent value must not call this
// (an absent declaration is compatible, not invalid).
func ParseDirection(raw string) (Direction, error) {
	switch Direction(raw) {
	case DirectionIncoming, DirectionOutgoing:
		return Direction(raw), nil
	default:
		return "", invalidDirectionError(raw)
	}
}

// DirectionOf returns the direction a formula declares, or the empty
// Direction when the formula places no direction constraint.
func DirectionOf(f *Formula) Direction {
	if f == nil || f.Applicability == nil {
		return ""
	}
	return f.Applicability.Direction
}

// GoalDirection extracts the goal-side declared direction from bead metadata.
// An absent key yields the empty Direction with no error (undeclared goals keep
// their legacy behavior). A present but invalid value is a structured error so a
// malformed goal fails closed rather than binding to everything.
func GoalDirection(metadata map[string]string) (Direction, error) {
	raw, ok := metadata[beadmeta.ApplicabilityDirectionMetadataKey]
	if !ok || raw == "" {
		return "", nil
	}
	return ParseDirection(raw)
}

// DirectionsCompatible reports whether a formula-side direction and a goal-side
// direction may bind. Absence on either side is compatible; two declared
// directions bind only when identical. This is the single, purely structural
// matcher shared by sling formula selection and dispatch fallback.
func DirectionsCompatible(formulaDir, goalDir Direction) bool {
	if formulaDir == "" || goalDir == "" {
		return true
	}
	return formulaDir == goalDir
}

// LoadFormulaDirection resolves the named formula (parse + inheritance only, not
// a full compile) and returns its declared applicability direction, or the empty
// Direction when the formula places no direction constraint. Selection sites use
// this to read the formula side of the match before instantiating.
func LoadFormulaDirection(name string, searchPaths []string) (Direction, error) {
	parser := NewParser(searchPaths...).SetSource(SourceFromEnv())
	f, err := parser.LoadByName(name)
	if err != nil {
		return "", fmt.Errorf("loading formula %q: %w", name, err)
	}
	resolved, err := parser.Resolve(f)
	if err != nil {
		return "", fmt.Errorf("resolving formula %q: %w", name, err)
	}
	return DirectionOf(resolved), nil
}

// DirectionIncompatibleError is returned when an explicitly selected formula's
// declared direction conflicts with the goal's declared direction. Explicit
// selection fails closed rather than silently substituting another formula.
type DirectionIncompatibleError struct {
	Formula          string
	FormulaDirection Direction
	GoalDirection    Direction
}

func (e *DirectionIncompatibleError) Error() string {
	return fmt.Sprintf("formula.applicability_incompatible: formula %q declares direction %q but goal declares direction %q",
		e.Formula, e.FormulaDirection, e.GoalDirection)
}

// NoCompatibleFormulaError is returned when default or fallback selection
// excludes every candidate formula on the direction axis and none remains.
type NoCompatibleFormulaError struct {
	GoalDirection Direction
	// Excluded lists the candidate formula names dropped for direction
	// incompatibility, in the order they were considered.
	Excluded []string
}

func (e *NoCompatibleFormulaError) Error() string {
	return fmt.Sprintf("formula.no_compatible_formula: no formula is compatible with goal direction %q (excluded on direction: %v)",
		e.GoalDirection, e.Excluded)
}

// validateApplicabilityDeclarations reports structural errors in a formula's
// applicability block. Parsing already rejects invalid TOML/JSON declarations;
// this covers formulas constructed programmatically. An absent block or an
// empty direction (no constraint) is valid.
func validateApplicabilityDeclarations(f *Formula) []string {
	if f == nil || f.Applicability == nil {
		return nil
	}
	switch f.Applicability.Direction {
	case "", DirectionIncoming, DirectionOutgoing:
		return nil
	default:
		return []string{invalidDirectionError(string(f.Applicability.Direction)).Error()}
	}
}

func cloneApplicability(a *Applicability) *Applicability {
	if a == nil {
		return nil
	}
	return &Applicability{Direction: a.Direction}
}

func unknownApplicabilityError(key string) error {
	return fmt.Errorf("formula.applicability_unknown: unknown formula applicability constraint %q; supported constraints: direction", key)
}

func invalidDirectionError(raw string) error {
	return fmt.Errorf("formula.applicability_direction_invalid: direction must be %q or %q, got %q",
		DirectionIncoming, DirectionOutgoing, raw)
}

func invalidDirectionTypeError() error {
	return fmt.Errorf("formula.applicability_direction_invalid: direction must be a string %q or %q",
		DirectionIncoming, DirectionOutgoing)
}
