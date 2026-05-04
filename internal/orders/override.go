package orders

import (
	"fmt"
	"sort"
	"strings"
)

// Override modifies a scanned order's scheduling fields.
// Uses pointer fields to distinguish "not set" from "set to zero value."
// Mirrors config.OrderOverride but lives in the orders package
// to avoid a circular dependency.
type Override struct {
	Name     string
	Rig      string
	Enabled  *bool
	Trigger  *string
	Interval *string
	Schedule *string
	Check    *string
	On       *string
	Pool     *string
	Timeout  *string
}

// ApplyOverrides applies each override to the matching order in aa.
// Matching is by name, optionally scoped by rig. Returns an error if an
// override targets a nonexistent order (following the agent override
// pattern where unmatched targets are errors, not silent no-ops).
func ApplyOverrides(aa []Order, overrides []Override) error {
	for i, ov := range overrides {
		if ov.Name == "" {
			return fmt.Errorf("orders.overrides[%d]: name is required", i)
		}
		found := false
		for j := range aa {
			if aa[j].Name != ov.Name {
				continue
			}
			// Scope matching: when ov.Rig is set, only match that rig.
			// When ov.Rig is empty, only match city-level orders
			// (those with no rig), not rig-scoped ones.
			if aa[j].Rig != ov.Rig {
				continue
			}
			applyOverride(&aa[j], &ov)
			found = true
		}
		if !found {
			if ov.Rig != "" {
				return fmt.Errorf("orders.overrides[%d]: order %q (rig %q) not found", i, ov.Name, ov.Rig)
			}
			// Rigless override that didn't match: collect the rigs where
			// this name does exist so we can hint that the override needs
			// to be rig-scoped. There is no wildcard syntax — to override
			// the order on multiple rigs the user must add one
			// [[orders.overrides]] block per rig.
			if rigs := matchingRigs(aa, ov.Name); len(rigs) > 0 {
				return fmt.Errorf(
					"orders.overrides[%d]: order %q not found at city scope; "+
						"if you meant the per-rig instance(s), add one "+
						"[[orders.overrides]] block per rig with rig = \"<rig-name>\" "+
						"(matching rigs: %s)",
					i, ov.Name, strings.Join(rigs, ", "))
			}
			return fmt.Errorf("orders.overrides[%d]: order %q not found", i, ov.Name)
		}
	}
	return nil
}

// matchingRigs returns a deterministically sorted list of rig names where an
// order with the given name exists. Empty if no per-rig instance matches.
// Used to enrich the "not found" error so the user sees the exact rig names
// they need to put on the override.
func matchingRigs(aa []Order, name string) []string {
	seen := make(map[string]struct{})
	for _, a := range aa {
		if a.Name != name || a.Rig == "" {
			continue
		}
		seen[a.Rig] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func applyOverride(a *Order, ov *Override) {
	if ov.Enabled != nil {
		a.Enabled = ov.Enabled
	}
	if ov.Trigger != nil {
		a.Trigger = *ov.Trigger
	}
	if ov.Interval != nil {
		a.Interval = *ov.Interval
	}
	if ov.Schedule != nil {
		a.Schedule = *ov.Schedule
	}
	if ov.Check != nil {
		a.Check = *ov.Check
	}
	if ov.On != nil {
		a.On = *ov.On
	}
	if ov.Pool != nil {
		a.Pool = *ov.Pool
	}
	if ov.Timeout != nil {
		a.Timeout = *ov.Timeout
	}
}
