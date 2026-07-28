package sling

import (
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/formula"
)

// decideDirectionBind is the pure fail-closed decision, given both already
// resolved directions, for the sling-side use of the shared structural
// applicability matcher (internal/formula). explicit selects the error
// semantics required by the approved design (gc-mpxxi):
//
//   - Explicit selection (gc sling --on <formula>): an incompatible formula was
//     named directly, so it fails with *formula.DirectionIncompatibleError
//     ("formula.applicability_incompatible") rather than silently substituting
//     another formula.
//   - Default/fallback selection (the agent's default_sling_formula, which the
//     reconciler uses to auto-dispatch ready goals): the single candidate is
//     excluded and none remains, so it fails with
//     *formula.NoCompatibleFormulaError ("formula.no_compatible_formula").
func decideDirectionBind(formulaName string, formulaDir, goalDir formula.Direction, explicit bool) error {
	if formula.DirectionsCompatible(formulaDir, goalDir) {
		return nil
	}
	if explicit {
		return &formula.DirectionIncompatibleError{
			Formula:          formulaName,
			FormulaDirection: formulaDir,
			GoalDirection:    goalDir,
		}
	}
	return &formula.NoCompatibleFormulaError{
		GoalDirection: goalDir,
		Excluded:      []string{formulaName},
	}
}

// checkGoalDirection gates binding formulaName (whose direction is already
// resolved to formulaDir) to a goal carrying goalMeta. It is the per-goal check
// used by the batch path, which resolves the formula direction once per batch
// rather than once per child. Absence on either side is compatible; a malformed
// gc.applicability.direction on the goal fails closed.
func checkGoalDirection(goalMeta map[string]string, formulaName string, formulaDir formula.Direction, explicit bool) error {
	goalDir, err := formula.GoalDirection(goalMeta)
	if err != nil {
		return err
	}
	if goalDir == "" {
		return nil // undeclared goal keeps legacy binding behavior
	}
	return decideDirectionBind(formulaName, formulaDir, goalDir, explicit)
}

// checkGoalFormulaDirection gates a single-bead bind, resolving the formula
// direction lazily: when the goal declares no direction the formula is never
// loaded, keeping the common undeclared path free of extra formula parses. A
// formula that cannot be resolved to read its direction is left to the
// downstream attach, which fails with the canonical "instantiating formula"
// error, so the direction axis is simply not evaluated here.
func checkGoalFormulaDirection(deps SlingDeps, a config.Agent, goalMeta map[string]string, formulaName string, explicit bool) error {
	if formulaName == "" {
		return nil
	}
	goalDir, err := formula.GoalDirection(goalMeta)
	if err != nil {
		return err
	}
	if goalDir == "" {
		return nil
	}
	formulaDir, err := formula.LoadFormulaDirection(formulaName, SlingFormulaSearchPaths(deps, a))
	if err != nil {
		return nil
	}
	return decideDirectionBind(formulaName, formulaDir, goalDir, explicit)
}

// filterBurnableByDirection returns the subset of open batch children eligible
// for the pre-attach molecule burn, excluding every child the per-child
// direction gate will reject. checkBatchNoMoleculeChildren auto-closes an
// unassigned child's existing molecule, so a child that is about to fail closed
// on the direction axis must be kept out of that burn to preserve the "no store
// mutation on a rejected sling" guarantee (gc-ebyq8). attachBatchFormula still
// rejects the excluded children in the batch loop; this only spares their
// existing attachments. A child with a malformed goal direction is also
// excluded — it fails closed in the loop and must not be burned first.
func filterBurnableByDirection(open []beads.Bead, formulaDir formula.Direction) []beads.Bead {
	burnable := make([]beads.Bead, 0, len(open))
	for _, child := range open {
		goalDir, err := formula.GoalDirection(child.Metadata)
		if err != nil {
			continue
		}
		if formula.DirectionsCompatible(formulaDir, goalDir) {
			burnable = append(burnable, child)
		}
	}
	return burnable
}

// gateSlingDirection enforces the applicability direction axis for the single-
// bead sling paths from DoSling, before preflight runs. It mirrors DoSling's
// own formula-selection switch so the same explicit-vs-default distinction
// drives the error semantics: an explicit --on formula fails with
// DirectionIncompatibleError, the agent default with NoCompatibleFormulaError.
// Running here (ahead of preflight's --reassign reopen) keeps a direction
// rejection fail-closed with zero store mutation. Standalone formula launches
// (IsFormula) have no goal to match; the plain-bead and --no-formula paths
// select no formula, so both are unconstrained.
func gateSlingDirection(opts SlingOpts, deps SlingDeps, querier BeadQuerier) error {
	if opts.IsFormula {
		return nil
	}
	switch {
	case opts.OnFormula != "":
		return enforceFormulaGoalDirection(deps, opts.Target, querier, opts.BeadOrFormula, opts.OnFormula, true)
	case !opts.NoFormula && opts.Target.EffectiveDefaultSlingFormula() != "":
		return enforceFormulaGoalDirection(deps, opts.Target, querier, opts.BeadOrFormula, opts.Target.EffectiveDefaultSlingFormula(), false)
	default:
		return nil
	}
}

// enforceFormulaGoalDirection reads the goal bead through querier and applies
// checkGoalFormulaDirection. It is the entry point for the single-bead sling
// paths (--on formula and the agent default formula), where the caller has a
// bead ID rather than the bead itself.
func enforceFormulaGoalDirection(deps SlingDeps, a config.Agent, querier BeadQuerier, beadID, formulaName string, explicit bool) error {
	if querier == nil || beadID == "" || formulaName == "" {
		return nil
	}
	bead, err := querier.Get(beadID)
	if err != nil {
		// Existence is already validated in preflight for the formula-attach
		// paths; a read failure here is surfaced by the downstream attach with
		// its canonical message, so do not double-report it.
		return nil
	}
	if err := checkGoalFormulaDirection(deps, a, bead.Metadata, formulaName, explicit); err != nil {
		return fmt.Errorf("goal %s: %w", beadID, err)
	}
	return nil
}
