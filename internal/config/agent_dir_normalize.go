package config

import (
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/pathutil"
)

// NormalizeAgentRigDirs rewrites each agent Dir that is an ABSOLUTE path
// SamePath-matching a configured rig's path to that rig's NAME, so every
// identity derived from Dir — QualifiedName, QualifiedInstanceName, and
// through them session-bead agent_name/alias, GC_AGENT/GC_ALIAS/GC_TEMPLATE,
// the startup beacon, and gc.routed_to — is rig-qualified
// ("decisions/decisions-worker-1") instead of path-shaped
// ("/home/ds/projects/decisions/decisions-worker-1"). Path-shaped identities
// read like workspace directories to the seat they are presented to, which
// strands demand-spawned workers primed-idle (dec-a5ar) and registers
// path-shaped agent_name on pool session beads (dr-2x9x).
//
// Normalization rule and edge cases:
//   - Only absolute dirs are considered; relative dirs (including the
//     canonical rig-name form) pass through untouched.
//   - Matching uses pathutil.SamePath, so trailing slashes and symlinked
//     spellings of a rig path normalize too.
//   - A dir equal to a SUBpath of a rig, or matching no configured rig, is
//     never rewritten (no false rewrites; identity stays as configured).
//   - Rigs with an empty name or empty path are skipped; a relative rig path
//     is resolved against cityRoot before comparison. First matching rig in
//     config order wins.
//   - If the rewrite would collide with another agent's (dir, name) key —
//     e.g. a provider-derived implicit agent injected under the rig name —
//     the rewrite is skipped so the config never gains duplicate identities.
//
// Must run AFTER ApplySiteBindings (rig paths are only authoritative once
// site bindings overlay them) and AFTER ApplyPatches (city.toml
// [[patches.agent]] selectors may key on the absolute dir form). Persisted
// identities in the old absolute form remain resolvable via the
// legacy-rig-path read fallback in cmd/gc (findAgentByTemplate).
func NormalizeAgentRigDirs(cfg *City, cityRoot string) {
	if cfg == nil || len(cfg.Agents) == 0 || len(cfg.Rigs) == 0 {
		return
	}
	taken := make(map[agentKey]bool, len(cfg.Agents))
	for _, a := range cfg.Agents {
		taken[agentKey{a.Dir, a.Name}] = true
	}
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if !filepath.IsAbs(strings.TrimSpace(a.Dir)) {
			continue
		}
		rigName := rigNameForAbsPath(cfg.Rigs, cityRoot, a.Dir)
		if rigName == "" {
			continue
		}
		newKey := agentKey{rigName, a.Name}
		if taken[newKey] {
			continue
		}
		delete(taken, agentKey{a.Dir, a.Name})
		taken[newKey] = true
		a.Dir = rigName
	}
}

// rigNameForAbsPath returns the name of the first configured rig whose
// resolved path SamePath-matches dir, or "" when none does.
func rigNameForAbsPath(rigs []Rig, cityRoot, dir string) string {
	for _, rig := range rigs {
		name := strings.TrimSpace(rig.Name)
		path := strings.TrimSpace(rig.Path)
		if name == "" || path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cityRoot, path)
		}
		if pathutil.SamePath(dir, path) {
			return name
		}
	}
	return ""
}
