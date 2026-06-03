package main

import (
	"context"
	"log"
	"path"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/molecule"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/sling"
)

// sessionBeadAssigneeIdentities returns every identifier under which a work
// bead could be assigned to this session: the session bead ID, session_name,
// configured_named_identity, current alias, and any prior aliases preserved
// in alias_history. Pool polecat aliases (e.g. "nux") are first-class
// assignment identities, so leaving them out of orphan-detection causes
// in-progress work to be reset under a live owner — see the
// SkipsLiveSessionAssignedByAlias regression tests.
func sessionBeadAssigneeIdentities(sb beads.Bead) []string {
	identities := make([]string, 0, 5)
	if id := strings.TrimSpace(sb.ID); id != "" {
		identities = append(identities, id)
	}
	if sn := strings.TrimSpace(sb.Metadata["session_name"]); sn != "" {
		identities = append(identities, sn)
	}
	if ni := strings.TrimSpace(sb.Metadata["configured_named_identity"]); ni != "" {
		identities = append(identities, ni)
	}
	if al := strings.TrimSpace(sb.Metadata["alias"]); al != "" {
		identities = append(identities, al)
	}
	for _, prior := range session.AliasHistory(sb.Metadata) {
		if prior = strings.TrimSpace(prior); prior != "" {
			identities = append(identities, prior)
		}
	}
	return identities
}

type releasedPoolAssignment struct {
	ID    string
	Index int
}

// PoolSessionName derives the tmux session name for a pool worker session.
// Format: {basename(template)}-{beadID} (e.g., "claude-mc-xyz").
// Named sessions with an alias use the alias instead.
func PoolSessionName(template, beadID string) string {
	base := path.Base(template)
	return agent.SanitizeQualifiedNameForSession(base) + "-" + beadID
}

// GCSweepSessionBeads closes open session beads that have no remaining
// open/in-progress work beads anywhere — primary store OR any attached
// rig store. Work-bead assignment is verified by a live cross-store
// query inside closeSessionBeadIfUnassigned, so the caller does not
// pass a work snapshot — that pattern was retired to prevent pre-close
// tick snapshots from poisoning close decisions. Returns the IDs of
// session beads that were closed.
func GCSweepSessionBeads(store beads.Store, rigStores map[string]beads.Store, sessionBeads []beads.Bead) []string {
	var closed []string
	for _, sb := range sessionBeads {
		if sb.Status == "closed" {
			continue
		}
		if !closeSessionBeadIfUnassigned(store, rigStores, nil, sb, "gc_swept", time.Now().UTC(), nil) {
			continue
		}
		closed = append(closed, sb.ID)
	}
	return closed
}

// releaseOrphanedPoolAssignmentsWhenSnapshotsComplete skips orphan release
// unless both the assigned-work and open-session snapshots are complete.
func releaseOrphanedPoolAssignmentsWhenSnapshotsComplete(
	store beads.Store,
	cfg *config.City,
	cityPath string,
	openSessionBeads []beads.Bead,
	result DesiredStateResult,
	rigStores map[string]beads.Store,
) []releasedPoolAssignment {
	// Partial input snapshots can make active work look orphaned for this
	// tick only: missing work affects drain decisions, and missing sessions
	// affects assigned-work orphan release.
	if result.snapshotQueryPartial() {
		return nil
	}
	return releaseOrphanedPoolAssignments(store, cfg, cityPath, openSessionBeads, result.AssignedWorkBeads, result.AssignedWorkStores, result.AssignedWorkStoreRefs, rigStores)
}

// releaseOrphanedPoolAssignments reopens active pool-routed work whose
// assignee no longer maps to any open session bead. This also recovers
// pool-routed work left in_progress with no assignee, which cannot be claimed
// again until it is moved back to open.
func releaseOrphanedPoolAssignments(
	store beads.Store,
	cfg *config.City,
	cityPath string,
	openSessionBeads []beads.Bead,
	assignedWorkBeads []beads.Bead,
	assignedWorkStores []beads.Store,
	assignedWorkStoreRefs []string,
	rigStores map[string]beads.Store,
) []releasedPoolAssignment {
	if store == nil || cfg == nil || len(assignedWorkBeads) == 0 {
		return nil
	}
	storeAware := len(assignedWorkStores) > 0
	if storeAware && len(assignedWorkStores) != len(assignedWorkBeads) {
		log.Printf("releaseOrphanedPoolAssignments: assigned work/store length mismatch: work=%d stores=%d", len(assignedWorkBeads), len(assignedWorkStores))
	}
	storeRefAware := len(assignedWorkStoreRefs) == len(assignedWorkBeads)
	if len(assignedWorkStoreRefs) > 0 && !storeRefAware {
		log.Printf("releaseOrphanedPoolAssignments: assigned work/store-ref length mismatch: work=%d storeRefs=%d", len(assignedWorkBeads), len(assignedWorkStoreRefs))
	}

	openIdentifiers := makeOpenSessionStoreRefIndex(cityPath, cfg, openSessionBeads, storeRefAware)
	legacyOpenIdentifiers := openSessionAssigneeIdentitySet(openSessionBeads)

	var released []releasedPoolAssignment
	// Workflow roots whose slot binding we have re-evaluated this pass, deduped
	// so a multi-step run's shared root is cleared at most once.
	clearedAffinityRoots := map[string]struct{}{}
	for i, wb := range assignedWorkBeads {
		if wb.Status != "open" && wb.Status != "in_progress" {
			continue
		}
		assignee := strings.TrimSpace(wb.Assignee)
		template := routedToOrLegacyWorkflowTarget(wb)
		if template == "" {
			continue
		}
		agentCfg := findAgentByTemplate(cfg, template)
		if agentCfg == nil || !agentCfg.SupportsGenericEphemeralSessions() {
			continue
		}
		if assignee == "" {
			if wb.Status != "in_progress" {
				continue
			}
		} else {
			workStoreRef := ""
			if storeRefAware {
				workStoreRef = assignedWorkStoreRefs[i]
			}
			if openSessionOwnsWork(legacyOpenIdentifiers, openIdentifiers, assignee, workStoreRef, storeRefAware) {
				continue
			}
			if assigneePreservesNamedSessionRoute(cfg, cityPath, template, assignee, workStoreRef, storeRefAware) {
				continue
			}
			if liveOpenSessionAssignmentExists(store, assignee) {
				continue
			}
		}

		var ownerStore beads.Store
		if storeAware {
			if i >= len(assignedWorkStores) || assignedWorkStores[i] == nil {
				log.Printf("releaseOrphanedPoolAssignments: missing owner store for assigned work %q at index %d", wb.ID, i)
				continue
			}
			ownerStore = assignedWorkStores[i]
		} else {
			ownerStore = storeForPoolAssignment(cfg, store, rigStores, wb)
			if ownerStore == nil {
				continue
			}
		}
		if !liveWorkAssignmentStillReleasable(ownerStore, wb.ID, assignee) {
			continue
		}
		allowsRelease, clearDetached := detachedProbeAllowsOrphanRelease(wb)
		if !allowsRelease {
			continue
		}
		if !releaseOrphanedPoolAssignment(ownerStore, wb.ID, clearDetached) {
			continue
		}
		released = append(released, releasedPoolAssignment{ID: wb.ID, Index: i})
		// If this orphaned step belonged to a multi-step workflow bound to a now
		// dead slot, release the binding so its remaining steps return to
		// unbound pool demand instead of waiting forever on a slot that is gone
		// (#2978).
		clearDeadWorkflowAffinity(ownerStore, wb, legacyOpenIdentifiers, clearedAffinityRoots)
	}
	return released
}

// clearDeadWorkflowAffinity releases a multi-step workflow's slot binding when
// the bound slot no longer has any open session, returning the workflow's steps
// to unbound pool demand (#2978). It is called after an in-progress step is
// orphan-released: if the step's workflow root (gc.root_bead_id) is bound to a
// slot identity that no open session still holds, the binding is cleared from
// the root and its open steps.
//
// A slot rotation keeps the binding: rotation is idle-gated (a slot mid-step is
// not recycled), so an orphaned in-progress step means the slot genuinely died
// rather than rotated; and if the successor session is already open under the
// same slot identity, that identity is in liveIdentifiers and the binding is
// preserved so the successor reclaims the steps. Best-effort and deduped per
// root per pass; a missing root or write error is logged and skipped.
func clearDeadWorkflowAffinity(store beads.Store, wb beads.Bead, liveIdentifiers map[string]struct{}, clearedRoots map[string]struct{}) {
	if store == nil {
		return
	}
	rootID := strings.TrimSpace(wb.Metadata["gc.root_bead_id"])
	if rootID == "" || rootID == wb.ID {
		return
	}
	if _, done := clearedRoots[rootID]; done {
		return
	}
	root, err := store.Get(rootID)
	if err != nil {
		return
	}
	slot := strings.TrimSpace(root.Metadata[molecule.SessionAffinitySlotMetadataKey])
	if slot == "" {
		clearedRoots[rootID] = struct{}{}
		return
	}
	if _, live := liveIdentifiers[slot]; live {
		return
	}
	clearedRoots[rootID] = struct{}{}
	if err := store.SetMetadata(rootID, molecule.SessionAffinitySlotMetadataKey, ""); err != nil {
		log.Printf("clearDeadWorkflowAffinity: root %q: %v", rootID, err)
	}
	siblings, err := store.ListByMetadata(map[string]string{"gc.root_bead_id": rootID}, 0)
	if err != nil {
		return
	}
	for _, sib := range siblings {
		if strings.TrimSpace(sib.Metadata[molecule.SessionAffinitySlotMetadataKey]) == "" {
			continue
		}
		if err := store.SetMetadata(sib.ID, molecule.SessionAffinitySlotMetadataKey, ""); err != nil {
			log.Printf("clearDeadWorkflowAffinity: step %q: %v", sib.ID, err)
		}
	}
}

// openSessionAssigneeIdentitySet returns every assignment identity held by an
// open session bead. This is the "live slot" set: a workflow whose
// gc.session_affinity_slot is in this set still has a session that can claim its
// continuation steps, so its binding must be preserved. Closed session beads are
// skipped.
func openSessionAssigneeIdentitySet(openSessionBeads []beads.Bead) map[string]struct{} {
	live := make(map[string]struct{}, len(openSessionBeads)*5)
	for _, sb := range openSessionBeads {
		if sb.Status == "closed" {
			continue
		}
		for _, id := range sessionBeadAssigneeIdentities(sb) {
			live[id] = struct{}{}
		}
	}
	return live
}

// releaseDeadAffinityWorkflowSteps clears multi-step workflow slot bindings whose
// bound slot no longer has any open session, returning the workflow's open steps
// to unbound pool demand (#2978).
//
// It closes the scale-to-zero gap that releaseOrphanedPoolAssignments cannot
// reach. That sweep only acts on assigned work and early-returns on an empty
// assigned-work snapshot, so it never fires for the failure shape below:
//
//   - A Min=0 pool slot claims step 1 of a multi-step workflow; binding stamps
//     the root and its open steps with the slot's gc.session_affinity_slot.
//   - Step 1 closes cleanly. Step 2 is now OPEN + UNASSIGNED but still bound.
//   - sessionHasOpenAssignedWorkForConfig sees no ASSIGNED work (step 2 is
//     unassigned), so the now-idle slot is retired and scaled to zero.
//   - No orphaned ASSIGNED step exists, so clearDeadWorkflowAffinity never runs,
//     and poolDemandCountShell excludes bound steps from the spawn count — so the
//     pool never respawns a slot and step 2 strands permanently.
//
// This standalone sweep is the counterpart: it scans ready, unassigned, bound
// steps across every work store and releases any whose slot identity holds no
// open session. Releasing one step releases its whole molecule, because
// clearDeadWorkflowAffinity clears the root and every sibling.
//
// Idempotent and best-effort: a slot still backed by an open session keeps its
// binding (so an in-flight or rotating slot reclaims its own steps via Tier 3a),
// and a store query failure skips that store for this tick — the next tick
// retries. Callers MUST skip this sweep on a partial session snapshot: a missing
// open session would make a live slot look dead and wrongly unbind its steps.
func releaseDeadAffinityWorkflowSteps(store beads.Store, rigStores map[string]beads.Store, openSessionBeads []beads.Bead, cfg *config.City) {
	if store == nil {
		return
	}
	live := openSessionAssigneeIdentitySet(openSessionBeads)
	clearedRoots := map[string]struct{}{}
	stores := make([]beads.Store, 0, len(rigStores)+1)
	stores = append(stores, store)
	for _, rs := range rigStores {
		if rs != nil {
			stores = append(stores, rs)
		}
	}
	limit := assignedWorkReadyLimit(cfg)
	for _, s := range stores {
		ready, err := liveReadyForControllerDemandQuery(s, beads.ReadyQuery{Limit: limit})
		if err != nil {
			log.Printf("releaseDeadAffinityWorkflowSteps: ready query: %v", err)
			continue
		}
		for _, step := range ready {
			if strings.TrimSpace(step.Assignee) != "" {
				continue
			}
			slot := strings.TrimSpace(step.Metadata[molecule.SessionAffinitySlotMetadataKey])
			if slot == "" {
				continue
			}
			if _, isLive := live[slot]; isLive {
				continue
			}
			clearDeadWorkflowAffinity(s, step, live, clearedRoots)
		}
	}
}

func detachedProbeAllowsOrphanRelease(wb beads.Bead) (bool, bool) {
	spec := strings.TrimSpace(wb.Metadata[detachedProbeMetadataKey])
	if spec == "" {
		clearDetachedProbeErrorCount(wb.ID)
		return true, false
	}

	result := probeDetachedWork(context.Background(), spec)
	switch result.Status {
	case detachedProbeAlive:
		clearDetachedProbeErrorCount(wb.ID)
		log.Printf("releaseOrphanedPoolAssignments: skipping release: detached probe alive for %s: %s", wb.ID, spec)
		return false, false
	case detachedProbeDead:
		clearDetachedProbeErrorCount(wb.ID)
		log.Printf("releaseOrphanedPoolAssignments: releasing %s: detached probe dead: %s", wb.ID, spec)
		return true, true
	case detachedProbeError, detachedProbeTimeout:
		count := incrementDetachedProbeErrorCount(wb.ID)
		if count < detachedProbeErrorThreshold {
			log.Printf("releaseOrphanedPoolAssignments: detached probe %s for %s: %v (error %d/%d)", result.Status, wb.ID, result.Err, count, detachedProbeErrorThreshold)
			return false, false
		}
		clearDetachedProbeErrorCount(wb.ID)
		log.Printf("releaseOrphanedPoolAssignments: releasing %s: detached probe %s after %d errors: %v", wb.ID, result.Status, count, result.Err)
		return true, true
	default:
		count := incrementDetachedProbeErrorCount(wb.ID)
		if count < detachedProbeErrorThreshold {
			log.Printf("releaseOrphanedPoolAssignments: detached probe unknown result for %s: %q (error %d/%d)", wb.ID, result.Status, count, detachedProbeErrorThreshold)
			return false, false
		}
		clearDetachedProbeErrorCount(wb.ID)
		return true, true
	}
}

func clearDetachedProbeMetadata(store beads.Store, id string) {
	if store == nil || id == "" {
		return
	}
	if err := store.SetMetadata(id, detachedProbeMetadataKey, ""); err != nil {
		log.Printf("clearing detached probe metadata for %s: %v", id, err)
	}
}

const unresolvedOpenSessionStoreRef = "\x00unresolved"

func makeOpenSessionStoreRefIndex(cityPath string, cfg *config.City, openSessionBeads []beads.Bead, storeRefAware bool) map[string]map[string]struct{} {
	index := make(map[string]map[string]struct{}, len(openSessionBeads)*5)
	if !storeRefAware {
		return index
	}
	for _, sb := range openSessionBeads {
		if sb.Status == "closed" {
			continue
		}
		storeRef, ok := assignedWorkStoreRefForSession(cityPath, cfg, sb)
		if !ok {
			storeRef = unresolvedOpenSessionStoreRef
		}
		for _, id := range sessionBeadAssigneeIdentities(sb) {
			addOpenSessionStoreRef(index, id, storeRef)
		}
	}
	return index
}

func addOpenSessionStoreRef(index map[string]map[string]struct{}, identifier, storeRef string) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return
	}
	refs := index[identifier]
	if refs == nil {
		refs = make(map[string]struct{}, 1)
		index[identifier] = refs
	}
	refs[storeRef] = struct{}{}
}

func openSessionOwnsWork(legacyIdentifiers map[string]struct{}, scopedIdentifiers map[string]map[string]struct{}, assignee, workStoreRef string, storeRefAware bool) bool {
	if !storeRefAware {
		_, ok := legacyIdentifiers[assignee]
		return ok
	}
	refs := scopedIdentifiers[assignee]
	if refs == nil {
		return false
	}
	if _, ok := refs[unresolvedOpenSessionStoreRef]; ok {
		return true
	}
	_, ok := refs[workStoreRef]
	return ok
}

func storeForPoolAssignment(cfg *config.City, cityStore beads.Store, rigStores map[string]beads.Store, wb beads.Bead) beads.Store {
	if cfg == nil || len(rigStores) == 0 {
		return cityStore
	}
	routed := routedToOrLegacyWorkflowTarget(wb)
	if routed != "" {
		if slash := strings.IndexByte(routed, '/'); slash > 0 {
			if store := rigStores[routed[:slash]]; store != nil {
				return store
			}
		}
	}
	idPrefix := sling.BeadPrefixForCity(cfg, wb.ID)
	for _, rig := range cfg.Rigs {
		if strings.EqualFold(idPrefix, rig.EffectivePrefix()) {
			if store := rigStores[rig.Name]; store != nil {
				return store
			}
		}
	}
	return cityStore
}

func isRecoverableUnassignedInProgressPoolWork(cfg *config.City, wb beads.Bead) bool {
	if wb.Status != "in_progress" || strings.TrimSpace(wb.Assignee) != "" {
		return false
	}
	template := routedToOrLegacyWorkflowTarget(wb)
	if template == "" {
		return false
	}
	agentCfg := findAgentByTemplate(cfg, template)
	return agentCfg != nil && agentCfg.SupportsGenericEphemeralSessions()
}

func releaseOrphanedPoolAssignment(store beads.Store, id string, clearDetached bool) bool {
	if store == nil || id == "" {
		return false
	}
	opts := beads.UpdateOpts{
		Assignee: stringPtr(""),
		Status:   stringPtr("open"),
	}
	if clearDetached {
		opts.Metadata = map[string]string{detachedProbeMetadataKey: ""}
	}
	if err := store.Update(id, opts); err != nil {
		log.Printf("releaseOrphanedPoolAssignments: releasing orphaned pool assignment %s: %v", id, err)
		return false
	}
	return true
}

func liveOpenSessionAssignmentExists(store beads.Store, assignee string) bool {
	assignee = strings.TrimSpace(assignee)
	if store == nil || assignee == "" {
		return false
	}
	if liveSessionBeadExistsByIdentity(store, assignee) {
		return true
	}
	// NOTE: this call site intentionally keeps a label-only query — not
	// the Type+Label union from session.ListAllSessionBeads. The
	// orphan-release tests (TestReleaseOrphanedPoolAssignments_*) set up
	// city session beads with Type=session but no gc:session label and
	// assert that rig work pointing at a session_name only reachable via
	// the typed bead IS released. Switching this query to the union
	// would surface those typed beads as "live" and cause the work to
	// be skipped instead of released, regressing
	// ReopensRigStoreMissingPoolAssignee and
	// ReleasesRigWorkAssignedToUnreachableOpenSession. The label-loss
	// bug this PR is fixing manifests in the snapshot/list/reconciler
	// paths; orphan release continues to treat the label as the
	// authoritative liveness signal.
	sessions, err := store.List(beads.ListQuery{
		Label: sessionBeadLabel,
		Live:  true,
	})
	if err != nil {
		log.Printf("releaseOrphanedPoolAssignments: live session validation failed for assignee %q: %v", assignee, err)
		return true
	}
	for _, sb := range sessions {
		if sb.Status == "closed" || !isSessionBead(sb) {
			continue
		}
		for _, id := range sessionBeadAssigneeIdentities(sb) {
			if assignee == id {
				return true
			}
		}
	}
	return false
}

func liveSessionBeadExistsByIdentity(store beads.Store, assignee string) bool {
	for _, id := range directSessionBeadIDCandidates(assignee) {
		sb, err := store.Get(id)
		if err != nil {
			continue
		}
		if sb.Status == "closed" || !isSessionBead(sb) {
			continue
		}
		for _, candidate := range sessionBeadAssigneeIdentities(sb) {
			if assignee == candidate {
				return true
			}
		}
	}
	return false
}

func directSessionBeadIDCandidates(assignee string) []string {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil
	}
	candidates := []string{assignee}
	if idx := strings.LastIndex(assignee, "-mc-"); idx >= 0 {
		candidates = append(candidates, assignee[idx+1:])
	}
	return candidates
}

func liveWorkAssignmentStillReleasable(store beads.Store, id, assignee string) bool {
	id = strings.TrimSpace(id)
	if store == nil || id == "" {
		return false
	}
	work, err := store.List(beads.ListQuery{
		Status:   "in_progress",
		Live:     true,
		TierMode: beads.TierBoth,
	})
	if err != nil {
		log.Printf("releaseOrphanedPoolAssignments: live work validation failed for %q: %v", id, err)
		return false
	}
	for _, wb := range work {
		if wb.ID != id {
			continue
		}
		return strings.TrimSpace(wb.Assignee) == strings.TrimSpace(assignee)
	}
	return false
}

func assigneePreservesNamedSessionRoute(cfg *config.City, cityPath, template, assignee, workStoreRef string, storeRefAware bool) bool {
	if cfg == nil {
		return false
	}
	spec, ok := findNamedSessionSpec(cfg, cfg.EffectiveCityName(), assignee)
	if !ok {
		return false
	}
	if namedSessionBackingTemplate(spec) != template {
		return false
	}
	if !storeRefAware {
		return true
	}
	return assignedWorkStoreRefForAgent(cityPath, cfg, spec.Agent) == workStoreRef
}

func stringPtr(s string) *string { return &s }
