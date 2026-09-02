package sling

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// reservedVarNames maps a --var key that collides with a bead-metadata guard
// flag to the metadata key it is commonly confused with. --var values are
// formula template substitutions: stampFormulaVars (internal/molecule) writes
// them as gc.var.<name> on the molecule *root* bead, purely for template
// discoverability. A guard that reads gc.no_land or gc.finalize_mode directly
// off the *target* bead (a common finalize-mode convention: see gc-8umql)
// never sees a --var of the same name -- wrong bead, wrong key namespace.
// The operator believes the guard is set; nothing is actually guarded.
var reservedVarNames = map[string]string{
	"no_land":       beadmeta.NoLandMetadataKey,
	"finalize_mode": beadmeta.FinalizeModeMetadataKey,
}

// ReservedVarNameError reports a --var key that collides with a bead-metadata
// guard flag it cannot actually set.
type ReservedVarNameError struct {
	Key     string
	MetaKey string
}

func (e *ReservedVarNameError) Error() string {
	return fmt.Sprintf(
		"--var %s is reserved: it stamps gc.var.%s on the molecule root bead, not %s on the target bead, so a guard reading %s directly never sees it -- use `bd update <bead> --set-metadata %s=<value>` instead",
		e.Key, e.Key, e.MetaKey, e.MetaKey, e.MetaKey,
	)
}

// validateReservedVarNames rejects --var assignments whose key collides with
// a known bead-metadata guard flag (reservedVarNames). vars are "key=value"
// strings in the CLI/API --var convention; malformed entries are left for
// downstream parsing to reject.
func validateReservedVarNames(vars []string) error {
	for _, v := range vars {
		key, _, ok := strings.Cut(v, "=")
		if !ok {
			continue
		}
		if metaKey, reserved := reservedVarNames[key]; reserved {
			return &ReservedVarNameError{Key: key, MetaKey: metaKey}
		}
	}
	return nil
}
