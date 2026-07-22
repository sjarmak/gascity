package temporalmaintenance

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// naming.go is the fleet-wide durable naming convention (gc-4zf.6): typed
// constructors for idempotency keys and Workflow IDs, adopted BEFORE a second
// workflow type exists so every later adopter inherits one convention instead
// of inventing its own. Full rationale and examples:
// docs/design/durable-naming-convention.md.
//
// The convention:
//
//	idempotency keys:  order/{scopedOrderName}/run/{scheduledTime}/{operation}
//	                   molecule/{rootBeadID}/{operation}
//	workflow IDs:      gc/{cityID}/bead/{beadID}
//	                   gc/{cityID}/molecule/{moleculeID}
//	                   gc/{cityID}/order/{scopedOrderName}/{runKey}
//
// Every segment derives from stable episode identity (order name + nominal
// fire time, root bead id) — never wall-clock "now", never random data — so a
// retry, a redelivery, or a restarted worker re-derives the identical key.
//
// MIGRATION RULE — the legacy maintenance-cycle formats stay. The live
// formats (WorkflowID's "gascity-maintenance/{repo}/{cycleKey}" and
// idempotencyKey's "temporal-shadow/..." prefix) are in production mid-soak:
// the running Schedule is keyed on them and the persisted KeyStore dedup
// records carry them, so changing them would orphan both. They remain
// untouched until after the gc-372 clean-week soak closes; migrating them is
// a separate, gated change. These constructors are for Track D and future
// adopters only.

// WorkflowKind is the identity segment of a durable workflow ID: what the
// orchestration episode is keyed off. Per the epic's non-negotiables there is
// no workflow-per-bead-lifetime — a workflow is one EPISODE, and its kind
// names the identity that episode is derived from.
type WorkflowKind string

const (
	// KindBead keys an episode off a single bead's identity.
	KindBead WorkflowKind = "bead"
	// KindMolecule keys an episode off a molecule's root bead.
	KindMolecule WorkflowKind = "molecule"
	// KindOrder keys an episode off one fire of a gc order.
	KindOrder WorkflowKind = "order"
)

// scheduledTimeLayout renders a Schedule fire time as a key segment: UTC,
// second granularity, ISO 8601 basic with an explicit trailing Z (e.g.
// "20260721T193000Z"). No slashes or colons, so the rendered time is always
// exactly one segment. Matches the granularity of the legacy cycleKey format.
const scheduledTimeLayout = "20060102T150405Z"

// FormatScheduledTime is the single time-formatting rule for durable keys:
// convert to UTC, drop sub-second precision, render scheduledTimeLayout.
// Callers must pass the Schedule's NOMINAL fire time (or workflow.Now inside
// a workflow), never wall-clock time read in an Activity — a retry of the
// same fire must re-derive the identical string.
func FormatScheduledTime(t time.Time) string {
	return t.UTC().Format(scheduledTimeLayout)
}

// validateSegment enforces the rules shared by every constructor: a segment
// must be non-empty, must not contain '/' (the reserved separator), and must
// not contain whitespace or control characters (keys land in logs, CLI
// arguments, and KeyStore file names). role names the offending input in the
// error.
func validateSegment(role, s string) error {
	if s == "" {
		return fmt.Errorf("durable naming: %s must not be empty", role)
	}
	if strings.ContainsRune(s, '/') {
		return fmt.Errorf("durable naming: %s %q must not contain '/'", role, s)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("durable naming: %s %q must not contain whitespace or control characters", role, s)
		}
	}
	return nil
}

// OrderRunKey derives the idempotency key for one operation inside one fire
// of a gc order:
//
//	order/{scopedOrderName}/run/{scheduledTime}/{operation}
//
// scopedOrder is the order's rig-qualified name (gc Order.ScopedName: e.g.
// "dolt-health" city-level, "dolt-health:rig:demo-repo" rig-level).
// scheduledTime is the Schedule's nominal fire time — the identity of the
// run, not the moment execution happened. operation names the single side
// effect being deduplicated (e.g. "sling-selection").
func OrderRunKey(scopedOrder string, scheduledTime time.Time, operation string) (string, error) {
	if err := validateSegment("scoped order name", scopedOrder); err != nil {
		return "", err
	}
	if scheduledTime.IsZero() {
		return "", fmt.Errorf("durable naming: scheduled time must not be the zero time")
	}
	if err := validateSegment("operation", operation); err != nil {
		return "", err
	}
	return fmt.Sprintf("order/%s/run/%s/%s", scopedOrder, FormatScheduledTime(scheduledTime), operation), nil
}

// MoleculeKey derives the idempotency key for one operation of a molecule
// episode:
//
//	molecule/{rootBeadID}/{operation}
//
// rootBead is the molecule's root bead id (e.g. "gc-4zf.3") — the durable
// identity the episode is keyed off. The root bead already carries deps,
// status, and history; the key only borrows its identity.
func MoleculeKey(rootBead, operation string) (string, error) {
	if err := validateSegment("root bead id", rootBead); err != nil {
		return "", err
	}
	if err := validateSegment("operation", operation); err != nil {
		return "", err
	}
	return fmt.Sprintf("molecule/%s/%s", rootBead, operation), nil
}

// WorkflowIDFor derives the durable workflow ID for an episode:
//
//	gc/{cityID}/bead/{beadID}
//	gc/{cityID}/molecule/{moleculeID}
//	gc/{cityID}/order/{scopedOrderName}/{runKey}
//
// cityID is the workspace name from pack.toml [pack] (here "ds-research"), so
// two cities sharing one Temporal namespace can never collide. KindBead and
// KindMolecule take exactly one trailing id; KindOrder takes exactly two —
// the scoped order name, then the run key (FormatScheduledTime of the fire
// time for Schedule-fired runs; any slash-free token for manual runs).
//
// A stable ID is what makes a duplicate start a server-side no-op and lets an
// operator find an episode's history from a bead id alone; see the design
// note for why that replaces hand-rolled dedup caches.
func WorkflowIDFor(cityID string, kind WorkflowKind, ids ...string) (string, error) {
	if err := validateSegment("city id", cityID); err != nil {
		return "", err
	}
	var want int
	switch kind {
	case KindBead, KindMolecule:
		want = 1
	case KindOrder:
		want = 2
	default:
		return "", fmt.Errorf("durable naming: unknown workflow kind %q", kind)
	}
	if len(ids) != want {
		return "", fmt.Errorf("durable naming: kind %q takes %d id segment(s), got %d", kind, want, len(ids))
	}
	for _, id := range ids {
		if err := validateSegment("id segment", id); err != nil {
			return "", err
		}
	}
	return "gc/" + cityID + "/" + string(kind) + "/" + strings.Join(ids, "/"), nil
}

// BeadWorkflowID is WorkflowIDFor(cityID, KindBead, beadID) with the arity
// checked at compile time.
func BeadWorkflowID(cityID, beadID string) (string, error) {
	return WorkflowIDFor(cityID, KindBead, beadID)
}

// MoleculeWorkflowID is WorkflowIDFor(cityID, KindMolecule, moleculeID) with
// the arity checked at compile time.
func MoleculeWorkflowID(cityID, moleculeID string) (string, error) {
	return WorkflowIDFor(cityID, KindMolecule, moleculeID)
}

// OrderRunWorkflowID is WorkflowIDFor(cityID, KindOrder, scopedOrder, runKey)
// with the arity checked at compile time.
func OrderRunWorkflowID(cityID, scopedOrder, runKey string) (string, error) {
	return WorkflowIDFor(cityID, KindOrder, scopedOrder, runKey)
}
