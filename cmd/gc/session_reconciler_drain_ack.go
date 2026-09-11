package main

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/telemetry"
)

// The drain-ack / reset async-stop state machine, extracted verbatim from
// session_reconciler.go so that file retains orchestration only. A session that
// acknowledges a drain parks in stop-pending, its stop is queued and confirmed
// dead asynchronously, and the finalize path folds the result back onto the
// reconciler's typed Info snapshot. Reset-stall accounting rides along because it
// shares the same tracker entries and the same cancellation preconditions.
//
// No behavior change: same package, same identifiers, no new interface.

// isDrainAckStopPendingInfo reports whether a session is parked in the drain-ack
// stop-pending state from the typed Info.MetadataState (raw "state") /
// Info.StateReason mirrors, with TrimSpace compares.
func isDrainAckStopPendingInfo(info sessionpkg.Info) bool {
	return strings.TrimSpace(info.MetadataState) == string(sessionpkg.StateDraining) &&
		strings.TrimSpace(info.StateReason) == sessionpkg.DrainAckStopPendingReason
}

// markDrainAckStopPending persists the drain-ack stop-pending transition through
// the session front door and returns the refreshed Info as a LOCAL fold
// (write-returns-Info, Step 6d): ApplyPatchInfo emits DrainAckStopPendingPatch and
// folds the same patch onto the caller's coherent snapshot Info in one step, so
// the two callers assign the returned Info directly instead of reconstructing the
// patch. It no longer mirrors onto a raw *beads.Bead: no later this-tick reader
// consumes the raw bead for these keys — a drain-acked session `continue`s before
// the wakeTargets/startCandidates append, and the post-loop scans read only
// orderedBeads[i].ID. On a persist error the input Info is returned unchanged with a
// false ok, so the caller skips the fold (identical to the old bool-return).
func markDrainAckStopPending(info sessionpkg.Info, sessFront *sessionpkg.Store, clk clock.Clock, stderr io.Writer) (sessionpkg.Info, bool) {
	if info.ID == "" || sessFront == nil {
		return info, false
	}
	if stderr == nil {
		stderr = io.Discard
	}
	updated, err := sessFront.ApplyPatchInfo(info, sessionpkg.DrainAckStopPendingPatch(clk.Now().UTC()))
	if err != nil {
		name := strings.TrimSpace(info.SessionNameMetadata)
		if name == "" {
			name = info.ID
		}
		fmt.Fprintf(stderr, "session reconciler: marking drain-ack stop-pending %s: %v\n", name, err) //nolint:errcheck
		return info, false
	}
	return updated, true
}

func clearDrainTrackerForStopPending(id string, dt *drainTracker) {
	if id == "" || dt == nil {
		return
	}
	dt.clearIdleProbe(id)
	dt.remove(id)
}

// cancelSelfInitiatedDrainAckAtMinFloor cancels a SELF-INITIATED drain-ack when
// honoring it would strand the template's min_active_sessions floor empty, and
// reports whether it did. It is the floor-aware twin of the assigned-work cancel
// (cancelSessionDrainForAssignedWorkInfo) at the same two drain-ack sites.
//
// THE LOOP IT CURES (sc-j27j0d, measured live on dip/refinery.refinery
// 2026-08-21 17:44-18:06Z, 8 sessions in 29 minutes at a 75-170s period):
// min_active_sessions=1 guarantees a session unconditionally; the seat boots,
// its selector correctly finds zero ready work, and the pool-worker protocol
// makes it run `gc runtime drain-ack`. The reconciler honored the ack, stopped
// the process and closed the session; the pool was then below its floor, so
// min_fill booted a fresh session that repeated the cycle for as long as the
// ready queue stayed empty. Two contracts, neither yielding. Deleting the floor
// is NOT the cure — the floor is what cured the orphan-flap (dip-ep6me2).
//
// The cure keeps the floor session WARM: cancel the ack, let it idle under
// idle_timeout (where isMinFloorExemptIdleSession already exempts exactly this
// session), and let the next wake deliver work to the live seat instead of
// destroying and cold-recreating one every couple of minutes.
//
// SELF-INITIATED IS THE WHOLE GATE, and it is what keeps this from swallowing
// drains that must be honored. Every precondition fails CLOSED — on any doubt
// the function returns false and the caller stops the session exactly as before:
//
//   - only an ack whose GC_DRAIN_ACK_SOURCE is exactly "agent" qualifies. A
//     RECONCILER-OWNED ack (orphaned, no-wake-reason, config-drift) was minted
//     from the desired-state view rather than chosen by the agent, and its own
//     cancel/stop rules live at the call sites. Reading the SOURCE rather than
//     reconcilerDrainAckMatchesSessionInfo is deliberate: that helper also
//     matches the generation, so a reconciler ack gone STALE would come back
//     "not reconciler-owned" and slip through this gate. An unreadable or
//     absent source is refused for the same reason.
//   - an OUTSTANDING DRAIN REQUEST is refused, whether it is visible as GC_DRAIN
//     on the runtime (an operator `gc agents drain`, a config-drift drain) or as
//     a live drainTracker entry. Somebody asked this session to stop; the floor
//     is not a veto over that order. An UNREADABLE GC_DRAIN is refused too.
//   - a session that is not a deterministic floor member is refused, so elastic
//     sessions above the floor retire on their own ack as they always have.
//
// The clear is the last act and its failure is refused as well: leaving the ack
// set while returning true would park the session and re-enter this branch every
// tick, so a failed clear falls through to the ordinary stop.
func cancelSelfInitiatedDrainAckAtMinFloor(
	infoByID map[string]sessionpkg.Info,
	cfg *config.City,
	dops drainOps,
	sp runtime.Provider,
	dt *drainTracker,
	template, name, id string,
	stderr io.Writer,
) bool {
	if dops == nil || cfg == nil || sp == nil || name == "" || id == "" {
		return false
	}
	if _, ok := infoByID[id]; !ok {
		return false
	}
	// Agent-sourced acks only; anything else keeps its existing semantics.
	source, sourceErr := sp.GetMeta(name, reconcilerDrainAckSourceKey)
	if sourceErr != nil || strings.TrimSpace(source) != drainAckSourceAgentValue {
		return false
	}
	// An outstanding drain request outranks the floor — and an unreadable one
	// is treated as outstanding.
	draining, drainErr := dops.isDraining(name)
	if drainErr != nil || draining {
		return false
	}
	if dt != nil && dt.get(id) != nil {
		return false
	}
	if !isMinFloorProtectedDrainAckSession(infoByID, cfg, template, id) {
		return false
	}
	if err := dops.clearDrain(name); err != nil {
		fmt.Fprintf(stderr, "session reconciler: clearing min-floor drain-ack for '%s': %v\n", name, err) //nolint:errcheck
		return false
	}
	telemetry.RecordDrainTransition(context.Background(), name, "min-floor", "cancel")
	return true
}

func assignedWorkDrainCancelReason(session beads.Bead, sp runtime.Provider, dt *drainTracker, name string) string {
	if dt != nil {
		if ds := dt.get(session.ID); ds != nil && assignedWorkDrainReasonCancelable(ds.reason) {
			return ds.reason
		}
	}
	if reason, ok := reconcilerDrainAckMatchesSession(session, sp, name); ok && assignedWorkDrainReasonCancelable(reason) {
		return reason
	}
	return "orphaned"
}

// assignedWorkDrainCancelReasonInfo is the session.Info sibling of
// assignedWorkDrainCancelReason for the reconciler forward pass. It reads the
// session id off Info (dt keying) and the generation via
// reconcilerDrainAckMatchesSessionInfo; the drain tracker and provider are shared
// verbatim, so it is byte-identical to the raw form.
func assignedWorkDrainCancelReasonInfo(info sessionpkg.Info, sp runtime.Provider, dt *drainTracker, name string) string {
	if dt != nil {
		if ds := dt.get(info.ID); ds != nil && assignedWorkDrainReasonCancelable(ds.reason) {
			return ds.reason
		}
	}
	if reason, ok := reconcilerDrainAckMatchesSessionInfo(info, sp, name); ok && assignedWorkDrainReasonCancelable(reason) {
		return reason
	}
	return "orphaned"
}

// resetPendingCommittedAtInfo reads the raw continuation_reset_pending and
// reset_committed_at markers (Info.ContinuationResetPending / Info.ResetCommittedAt)
// with trim + RFC3339 parse rules.
func resetPendingCommittedAtInfo(info sessionpkg.Info) (string, time.Time, bool) {
	if strings.TrimSpace(info.ContinuationResetPending) != "true" {
		return "", time.Time{}, false
	}
	raw := strings.TrimSpace(info.ResetCommittedAt)
	if raw == "" {
		return "", time.Time{}, false
	}
	committedAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", time.Time{}, false
	}
	return raw, committedAt, true
}

func recordResetStallIfDue(
	cityPath string,
	store beads.Store,
	sp runtime.Provider,
	cfg *config.City,
	info sessionpkg.Info,
	template string,
	name string,
	running bool,
	alive bool,
	startupTimeout time.Duration,
	now time.Time,
	dt *drainTracker,
	rec events.Recorder,
	stderr io.Writer,
	trace *sessionReconcilerTraceCycle,
) {
	resetCommittedAt, committedAt, pending := resetPendingCommittedAtInfo(info)
	if !pending {
		if dt != nil {
			dt.clearResetStall(info.ID)
		}
		return
	}
	if alive || startupTimeout <= 0 {
		return
	}
	elapsed := now.Sub(committedAt)
	if elapsed <= startupTimeout {
		return
	}
	// The dedup mark gates the DIAGNOSTIC and the event, not the eviction:
	// a transient kill failure must not disarm the fix for the rest of the
	// episode (the mark only clears when continuation_reset_pending clears,
	// which a wedged session never does).
	first := dt == nil || dt.markResetStall(info.ID)

	if stderr == nil {
		stderr = io.Discard
	}

	// The occupying tmux runtime is stale: continuation reset has been
	// pending longer than the startup timeout, and the runtime the
	// reconciler is waiting on for the reset never came back alive. When the
	// tmux session is still running underneath (the classic wedge in #5355:
	// the old runtime exited cleanly but its tmux session survived), waiting
	// forever leaves the session parked in reset-pending under the OLD
	// config indefinitely. Evict it so the normal spawn path on a later tick
	// recreates the session under the current config. Attempted on every
	// overdue tick while a stale runtime is still running — once the kill
	// lands, `running` goes false and this stops attempting. Gated on
	// running: if the tmux session is already gone too, there is nothing to
	// evict, and the reconciler's other paths handle a fully-dead bead.
	// Best-effort: a session that disappears between the observation above
	// and this kill (IsSessionGone) is the expected steady state, not a
	// failure.
	if running && sp != nil {
		if err := workerKillSessionTargetWithConfig(cityPath, store, sp, cfg, name); err != nil && !runtime.IsSessionGone(err) {
			fmt.Fprintf(stderr, "session reconciler: evicting stale reset-pending runtime %s: %v\n", name, err) //nolint:errcheck
		}
	}

	if !first {
		return
	}

	elapsedSeconds := int(elapsed / time.Second)
	msg := fmt.Sprintf(
		"session reconciler: reset stalled for %s: elapsed_s=%d reset_committed_at=%s bead_id=%s",
		name, elapsedSeconds, resetCommittedAt, info.ID,
	)
	fmt.Fprintln(stderr, msg) //nolint:errcheck

	if rec != nil {
		rec.Record(events.Event{
			Type:      events.SessionResetStalled,
			Actor:     "gc",
			Subject:   name,
			Message:   msg,
			SessionID: info.ID,
			Payload:   events.SessionResetStalledPayloadJSON(name, template, resetCommittedAt, elapsedSeconds),
		})
	}
	if trace != nil {
		trace.RecordDecision(
			TraceSiteReconcilerResetStalled,
			TraceReasonResetStalled,
			TraceOutcomeFailed,
			template,
			name,
			map[string]any{
				"bead_id":            info.ID,
				"elapsed_s":          elapsedSeconds,
				"reset_committed_at": resetCommittedAt,
				"startup_timeout_s":  int(startupTimeout / time.Second),
			},
		)
	}
}

func drainAckAsyncStopKey(sessionID, name string) string {
	if id := strings.TrimSpace(sessionID); id != "" {
		return "id:" + id
	}
	return "name:" + strings.TrimSpace(name)
}

// drainAckAsyncStopPokeController is a mutable test seam over pokeController
// for the async drain-ack stop path (see queueDrainAckAsyncStop).
var drainAckAsyncStopPokeController = pokeController

// drainAckStopConfirmDeadTimeout/Poll bound the post-kill confirm-dead loop in
// queueDrainAckAsyncStop. Package vars so tests can shrink them.
var (
	drainAckStopConfirmDeadTimeout = 6 * time.Second
	drainAckStopConfirmDeadPoll    = 250 * time.Millisecond
)

func queueDrainAckAsyncStop(cityPath string, store beads.Store, sp runtime.Provider, cfg *config.City, sessionID, name, expectedToken string, processNames []string, tracker *asyncStartTracker, stderr io.Writer) {
	name = strings.TrimSpace(name)
	if name == "" || sp == nil {
		return
	}
	if stderr == nil {
		stderr = io.Discard
	}
	key := drainAckAsyncStopKey(sessionID, name)
	done, tracking := tracker.startDrainAckStop(key)
	if !tracking {
		return
	}
	// Bind the poke seam on the caller's goroutine, at queue time. The async
	// goroutine below may outlive its reconcile invocation (see the poke
	// comment), and re-reading the mutable package-global seam from a detached
	// goroutine races with tests that swap it — and lets a goroutine queued by
	// one test poke a later test's swapped-in counter. Capturing the value here
	// confines each goroutine to the seam that was live when its stop was queued.
	poke := drainAckAsyncStopPokeController
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s panicked: %v\n%s", name, r, debug.Stack()) //nolint:errcheck
			}
			done()
		}()
		// Token fence (mirrors verifiedStop): this kill targets the session by
		// NAME and may fire long after it was queued. If the name was reused by
		// a re-woken replacement in the meantime, its GC_INSTANCE_TOKEN differs
		// from the one we intended to stop; killing it would take out a live,
		// working session. Skip on a definite mismatch. An empty expected or
		// live token means "cannot verify" and falls through to the kill,
		// matching verifiedStop's conservative posture.
		if expectedToken != "" {
			if actualToken, _ := sp.GetMeta(name, "GC_INSTANCE_TOKEN"); actualToken != "" && actualToken != expectedToken {
				fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s skipped: instance token mismatch (session was replaced)\n", name) //nolint:errcheck
				return
			}
		}
		if err := workerKillSessionTargetWithConfig(cityPath, store, sp, cfg, name); err != nil && !runtime.IsSessionGone(err) {
			fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s: %v\n", name, err) //nolint:errcheck
			return
		}
		// The kill above is best-effort and does not verify the agent actually
		// exited: a claude that ignores SIGHUP, reparents, or races the kill
		// grace period can survive it. Re-observe liveness and re-issue the kill
		// until the runtime is confirmed dead or a bounded deadline passes — a
		// survivor here would otherwise keep occupying the pool slot forever
		// (the reassigned next step stays runtime-missing). The expected token is
		// threaded through so each re-kill stays fenced against a re-woken
		// same-name replacement. Mirrors #4089's confirm-dead contract.
		confirmDrainAckRuntimeDead(cityPath, store, sp, cfg, name, expectedToken, processNames, stderr)
		// The runtime session is now confirmed dead (or the confirm-dead
		// deadline passed and we proceed best-effort), but its pool session
		// bead stays open (occupying the pool slot) until
		// finalizeDrainAckStopPendingSessions closes it on a subsequent tick.
		// Poke the controller so finalize + pool respawn runs on the next
		// event-driven tick instead of waiting up to a full patrol interval
		// (ga-ryhnhd). Mirrors the drain-ack CLI poke.
		// Poke is best-effort: a failure is not logged because the goroutine may
		// outlive its reconcile invocation and write to stderr concurrently with
		// the caller's subsequent writes on the same writer (data race on
		// non-goroutine-safe buffers). The controller reconciles on the next
		// patrol tick regardless.
		_ = poke(cityPath)
	}()
}

// confirmDrainAckRuntimeDead re-observes a killed runtime and re-issues the
// kill until liveness is false or the deadline passes. The async drain-ack
// stop's kill is best-effort and does not verify the agent exited; a survivor
// keeps the pool slot occupied so the reassigned next step stays
// runtime-missing. Each re-kill is token-fenced against expectedToken (mirrors
// verifiedStop and the first-kill fence): session names are reused across
// incarnations, so once the original target dies a re-woken same-name
// replacement must not be killed. Returns true if confirmed dead — including
// when a definite token mismatch shows the name now belongs to a replacement —
// and false if it outlived the deadline (caller proceeds best-effort). Mirrors
// #4089's confirm-dead contract.
func confirmDrainAckRuntimeDead(cityPath string, store beads.Store, sp runtime.Provider, cfg *config.City, name, expectedToken string, processNames []string, stderr io.Writer) bool {
	deadline := time.Now().Add(drainAckStopConfirmDeadTimeout)
	for {
		running, alive, livenessErr := observeRuntimeProviderLiveness(sp, name, processNames)
		if livenessErr != nil {
			fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s: deferring confirm-dead because liveness observation failed: %v\n", name, livenessErr) //nolint:errcheck
			return false
		}
		if !running && !alive {
			return true
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s: runtime still alive after confirm-dead deadline; slot may stay occupied\n", name) //nolint:errcheck
			return false
		}
		// Token fence before every re-kill (mirrors the first-kill fence above
		// and verifiedStop): the re-kill targets the session by NAME, and a
		// survivor that finally exits can be replaced by a freshly re-woken
		// same-name session carrying a different GC_INSTANCE_TOKEN before this
		// loop next observes it. A definite live-token mismatch means our
		// intended target is already gone and the name now belongs to a live
		// replacement — treat the original as confirmed dead and stop rather than
		// killing the replacement. An empty expected or live token means "cannot
		// verify" and falls through to the re-kill, matching verifiedStop.
		if expectedToken != "" {
			if actualToken, _ := sp.GetMeta(name, "GC_INSTANCE_TOKEN"); actualToken != "" && actualToken != expectedToken {
				fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s confirm-dead skipped re-kill: instance token mismatch (session was replaced)\n", name) //nolint:errcheck
				return true
			}
		}
		if err := workerKillSessionTargetWithConfig(cityPath, store, sp, cfg, name); err != nil && !runtime.IsSessionGone(err) {
			fmt.Fprintf(stderr, "session reconciler: async drain-ack stop %s re-kill: %v\n", name, err) //nolint:errcheck
		}
		time.Sleep(drainAckStopConfirmDeadPoll)
	}
}

func recordDrainAckAssignedWorkEvent(
	cityPath string,
	cfg *config.City,
	store beads.Store,
	rigStores map[string]beads.Store,
	info sessionpkg.Info,
	subject string,
	template string,
	name string,
	rec events.Recorder,
	stderr io.Writer,
) {
	if rec == nil {
		return
	}
	strandedBead, found, beadLookupErr := firstOpenAssignedWorkBeadForReachableStore(cityPath, cfg, store, rigStores, info)
	if beadLookupErr != nil {
		fmt.Fprintf(stderr, "session reconciler: locating stranded bead for drain-acked %s: %v\n", name, beadLookupErr) //nolint:errcheck
	}
	if !found {
		return
	}
	rec.Record(events.Event{
		Type:      events.SessionDrainAckedWithAssignedWork,
		Actor:     "gc",
		Subject:   subject,
		Message:   "session drain-acked while still assigned to work bead",
		SessionID: info.ID,
		Payload: api.SessionDrainAckedWithAssignedWorkPayloadJSON(
			info.ID,
			strandedBead.ID,
			template,
			strandedBead.Status,
			"drain_acked_with_assigned_work",
		),
	})
}

// drainAckFinalizeResult captures the Info-snapshot effect of a
// finalizeDrainAckStoppedSession call so the reconciler can refresh its typed
// infoByID snapshot from the write it just performed (front-door migration Step
// 6d write-returns-Info) instead of re-projecting the raw working bead. The zero
// value is a no-op — the call mutated nothing (async/early-return/persist-error)
// so applyTo returns the snapshot Info unchanged.
type drainAckFinalizeResult struct {
	// batch is the metadata patch for the Path-A close (ClosePatch), whose persist
	// happens inside closeSessionBeadIfReachableStoreUnassigned (a helper); the
	// caller folds it onto the snapshot via ApplyPatch. nil when the call took no
	// close/metadata path.
	batch sessionpkg.MetadataPatch
	// closed reports that the call closed the bead in memory
	// (session.Status = "closed"); the snapshot must fold that status close via
	// MarkClosed, which no metadata patch can carry (Info.Closed derives from
	// Status, not metadata).
	closed bool
	// folded carries the coherent post-write Info for the non-close drain-ack path:
	// finalizeDrainAckStoppedSession persists the drain-ack batch through
	// ApplyPatchInfo and folds it onto the pre-call snapshot in one step
	// (write-returns-Info, Step 6d), so the caller assigns this Info directly
	// instead of re-folding a returned batch. nil on the close/witness/no-op paths.
	folded *sessionpkg.Info
	// witnessInfo carries a full reprojection for the NDI witness close, where the
	// call adopts the store's authoritative metadata wholesale
	// (session.Metadata = latest.Metadata) rather than applying a known patch, so
	// the post-Info cannot be folded from batch and is reprojected instead.
	witnessInfo *sessionpkg.Info
}

// applyTo folds the finalize result onto the coherent pre-call snapshot Info,
// byte-identically to re-projecting the mutated bead (the raw refreshSessionInfo
// path): the witness reprojection wins outright; the non-close folded Info
// (already ApplyPatchInfo-folded inside the call) wins next; otherwise the Path-A
// ClosePatch folds via ApplyPatch and its in-memory close folds via MarkClosed.
// The caller must pass the session's coherent snapshot entry — infoByID[id] equal
// to the pre-call Info projection of *session — which holds at every finalize
// call site (top-of-loop / post-heal / post-zombie refresh, no un-refreshed
// *session mutation reaches the call).
func (r drainAckFinalizeResult) applyTo(info sessionpkg.Info) sessionpkg.Info {
	if r.witnessInfo != nil {
		return *r.witnessInfo
	}
	if r.folded != nil {
		return *r.folded
	}
	if r.batch != nil {
		info = info.ApplyPatch(r.batch)
	}
	if r.closed {
		info = info.MarkClosed()
	}
	return info
}

func finalizeDrainAckStoppedSession(
	cityPath string,
	cfg *config.City,
	store beads.Store,
	rigStores map[string]beads.Store,
	info sessionpkg.Info,
	template string,
	closeIfUnassigned bool,
	dops drainOps,
	dt *drainTracker,
	clk clock.Clock,
	rec events.Recorder,
	stderr io.Writer,
) drainAckFinalizeResult {
	if store == nil || info.ID == "" {
		return drainAckFinalizeResult{}
	}
	// Every decision read comes off the typed Info; the whole-bead raw-by-design
	// helpers (sessionHasOpenAssignedWorkForReachableStore,
	// closeSessionBeadIfReachableStoreUnassigned, recordDrainAckAssignedWorkEvent)
	// take Info too, and the one genuine post-mutation re-read is the front-door
	// Get NDI witness below. Callers pass the coherent infoByID[id].
	name := strings.TrimSpace(info.SessionNameMetadata)
	if template == "" {
		template = normalizedSessionTemplateInfo(info, cfg)
	}
	if template == "" {
		template = info.Template
	}
	recordStopped := func(performedStop bool) {
		// gc.agent.stops.total counts the stop action, so only the observer
		// that actually performs the stop transition records it. Under NDI
		// multiple observers process the same drain-ack; the witness branch
		// (the bead was already closed by another observer) still re-emits the
		// SessionStopped event for parity with existing event semantics (events
		// dedupe downstream by session id) but must not inflate the monotonic
		// action counter.
		if performedStop {
			telemetry.RecordAgentStop(context.Background(), name, sessionAgentMetricIdentityInfo(info, cfg), "drain-ack", nil)
		}
		if rec == nil {
			return
		}
		rec.Record(events.Event{
			Type:      events.SessionStopped,
			Actor:     "gc",
			Subject:   template,
			Message:   "drain acknowledged by agent",
			SessionID: info.ID,
			Payload:   api.SessionLifecyclePayloadJSON(info.ID, template, "drain acknowledged"),
		})
	}
	hasAssignedWork, assignedErr := sessionHasOpenAssignedWorkForReachableStoreForCloseGate(cityPath, cfg, store, rigStores, info)
	if assignedErr != nil {
		fmt.Fprintf(stderr, "session reconciler: checking assigned work for drain-acked %s: %v\n", name, assignedErr) //nolint:errcheck
		hasAssignedWork = true
	}
	if closeIfUnassigned && !hasAssignedWork {
		if closeSessionBeadIfReachableStoreUnassigned(cityPath, cfg, store, rigStores, info, "drained", clk.Now().UTC(), stderr, true) {
			closePatch := sessionpkg.ClosePatch(clk.Now().UTC(), "drained")
			if dops != nil {
				_ = dops.clearDrain(name)
			}
			if dt != nil {
				dt.clearIdleProbe(info.ID)
				dt.remove(info.ID)
			}
			recordStopped(true)
			// write-returns-Info (Step 6d): the caller's snapshot fold is ApplyPatch(the
			// ClosePatch) + MarkClosed (closed:true). The raw session.Status="closed"
			// mirror is deleted — the caller's MarkClosed fold is the sole same-tick
			// close reader now, and the telemetry close-path test re-pins on it.
			return drainAckFinalizeResult{batch: closePatch, closed: true}
		}
		if witnessInfo, err := sessionFrontDoor(store).Get(info.ID); err == nil && witnessInfo.Closed {
			// NDI witness close: another observer already closed the bead. The
			// session-front-door Get returns the authoritative closed Info directly —
			// the one documented status-close Store.Get refresh (a metadata patch
			// cannot express a status close, so no local fold reproduces it). It is a
			// rare non-fast-path branch, so it does not affect the tick Get budget.
			// The witness Info (already Closed) is the caller's snapshot fold; the raw
			// session.Status="closed" mirror is deleted. Behaviorally equivalent to the
			// old raw store.Get + reproject on well-formed session beads, with two
			// intentional front-door deltas: Get applies the IsSessionBeadOrRepairable
			// class gate (a corrupt non-session bead admitted only by a stale session
			// label now errs → falls through to the assigned-work close gate instead of
			// witnessing) and projects the fully-latest bead fields — both confined to
			// corrupt-class / concurrent-mutation edges.
			if dops != nil {
				_ = dops.clearDrain(name)
			}
			if dt != nil {
				dt.clearIdleProbe(info.ID)
				dt.remove(info.ID)
			}
			recordStopped(false)
			return drainAckFinalizeResult{witnessInfo: &witnessInfo}
		}
		assignedAfterCloseGate, closeGateAssignedErr := sessionHasOpenAssignedWorkForReachableStoreForCloseGate(cityPath, cfg, store, rigStores, info)
		if closeGateAssignedErr != nil {
			fmt.Fprintf(stderr, "session reconciler: checking assigned work after failed drain-ack close gate for %s: %v\n", name, closeGateAssignedErr) //nolint:errcheck
			assignedAfterCloseGate = true
		}
		if assignedAfterCloseGate {
			hasAssignedWork = true
		}
	}
	batch := sessionpkg.AcknowledgeDrainPatch(clk.Now().UTC(), info.WakeMode == "fresh")
	if hasAssignedWork {
		batch = sessionpkg.CompleteDrainPatch(clk.Now().UTC(), string(sessionpkg.SleepReasonIdle), info.WakeMode == "fresh")
	}
	// A drain-ack that completes a restart-request cycle (gc session reset →
	// agent drain-ack) must also consume restart_requested. The drain-ack
	// branch handles the stop and continues before the restart-requested
	// branch runs, so nothing else clears the flag; if it survives in the
	// store, a later cache-reconcile re-emission resurrects it and the
	// controller honors it as a fresh restart request — a phantom second
	// restart that rotates session_key and destroys resume continuity (#2574).
	if info.RestartRequested == "true" {
		batch["restart_requested"] = ""
	}
	foldedInfo, err := sessionFrontDoor(store).ApplyPatchInfo(info, batch)
	if err != nil {
		fmt.Fprintf(stderr, "session reconciler: finalizing drain-ack stopped %s: %v\n", name, err) //nolint:errcheck
		// Store write failed, so nothing changed — the snapshot must stay unchanged
		// (zero result → applyTo no-op).
		return drainAckFinalizeResult{}
	}
	// The raw metadata mirror loop is dropped (Step 5b): ApplyPatchInfo persisted
	// the drain-ack batch and folded it onto the caller's coherent Info in one step
	// (write-returns-Info, Step 6d), and no later this-tick reader consumes the raw
	// bead metadata for these keys (a drain-acked session `continue`s before the
	// wakeTargets/startCandidates append; recordStopped/recordDrainAckAssignedWorkEvent
	// below read identity + store-query results, not the drain-ack batch keys).
	if dops != nil {
		_ = dops.clearDrain(name)
	}
	if dt != nil {
		dt.clearIdleProbe(info.ID)
		dt.remove(info.ID)
	}
	recordStopped(true)
	if hasAssignedWork {
		recordDrainAckAssignedWorkEvent(cityPath, cfg, store, rigStores, info, template, template, name, rec, stderr)
	}
	// Non-close drain-ack: the snapshot fold is the ApplyPatchInfo result above.
	return drainAckFinalizeResult{folded: &foldedInfo}
}

func reconcileDrainAckStopPending(
	cityPath string,
	cfg *config.City,
	sp runtime.Provider,
	store beads.Store,
	rigStores map[string]beads.Store,
	info sessionpkg.Info,
	tp TemplateParams,
	desired bool,
	dops drainOps,
	dt *drainTracker,
	asyncStopTracker *asyncStartTracker,
	clk clock.Clock,
	rec events.Recorder,
	stderr io.Writer,
) (bool, drainAckFinalizeResult) {
	if info.ID == "" || !isDrainAckStopPendingInfo(info) {
		return false, drainAckFinalizeResult{}
	}
	name := strings.TrimSpace(info.SessionNameMetadata)
	obs, err := workerObserveSessionTargetWithRuntimeHintsWithConfig(cityPath, store, sp, cfg, info.ID, tp.Hints.ProcessNames)
	if err != nil || obs.Running || obs.Alive {
		// Async-stop: queueDrainAckAsyncStop takes the session ID and mutates only
		// the async tracker, so the snapshot stays coherent — a zero result (applyTo
		// no-op) matches the unmutated session. The token fence reads the typed
		// instance_token off the Info snapshot (mirrors verifiedStop).
		queueDrainAckAsyncStop(cityPath, store, sp, cfg, info.ID, name, info.InstanceToken, tp.Hints.ProcessNames, asyncStopTracker, stderr)
		return true, drainAckFinalizeResult{}
	}
	return true, finalizeDrainAckStoppedSession(
		cityPath, cfg, store, rigStores, info, tp.TemplateName,
		!desired || isPoolManagedSessionInfo(info),
		dops, dt, clk, rec, stderr,
	)
}

// drainAckStopPendingProcessNames resolves the configured agent process-name
// hints for a persisted stop-pending session so the finalizer path observes and
// confirm-dead-kills it with the same process liveness the reset-driven path
// gets from tp.Hints.ProcessNames. The finalizer only has the session Info (no
// resolved TemplateParams), so it recovers the hints from config via the
// normalized template/agent. Returns nil when the agent cannot be resolved,
// leaving the prior nil-hint behavior for unmanaged sessions.
func drainAckStopPendingProcessNames(cfg *config.City, info sessionpkg.Info) []string {
	if cfg == nil {
		return nil
	}
	agent := findAgentByTemplate(cfg, normalizedSessionTemplateInfo(info, cfg))
	if agent == nil {
		return nil
	}
	return processHints(cfg, agent)
}

func finalizeDrainAckStopPendingSessions(
	cityPath string,
	cfg *config.City,
	sp runtime.Provider,
	sessStore beads.SessionStore,
	rigStores map[string]beads.Store,
	infos []sessionpkg.Info,
	dops drainOps,
	dt *drainTracker,
	asyncStopTracker *asyncStartTracker,
	clk clock.Clock,
	rec events.Recorder,
	stderr io.Writer,
) int {
	// Session class typed at the boundary; the drain-ack helpers below take the
	// unwrapped beads.Store. Same underlying store value, behavior unchanged. This
	// caller-fed pass takes the snapshot's OpenInfos() directly — no per-bead codec
	// projection (§2.6).
	store := sessStore.Store
	if store == nil || sp == nil || len(infos) == 0 {
		return 0
	}
	finalized := 0
	for _, info := range infos {
		if !isDrainAckStopPendingInfo(info) {
			continue
		}
		name := strings.TrimSpace(info.SessionNameMetadata)
		// Resolve the configured agent process-name hints for this persisted
		// stop-pending session, exactly as the reset-driven path threads
		// tp.Hints.ProcessNames (see reconcileDrainAckStopPending). Without them
		// the queued confirm-dead loop observes with nil hints, so once the
		// runtime pane disappears ObserveLiveness collapses to IsRunning alone and
		// a reparented/surviving agent process is misread as dead — freeing the
		// pool slot while the agent still runs.
		processNames := drainAckStopPendingProcessNames(cfg, info)
		obs, err := workerObserveSessionTargetWithRuntimeHintsWithConfig(cityPath, store, sp, cfg, info.ID, processNames)
		if err != nil {
			// Observation unavailable, not "still alive". Re-queue the stop and
			// say nothing to the agent: a reminder is a claim about the row's
			// state, and this tick has none.
			queueDrainAckAsyncStop(cityPath, store, sp, cfg, info.ID, name, info.InstanceToken, processNames, asyncStopTracker, stderr)
			continue
		}
		if obs.Running || obs.Alive {
			// The stop was already queued and the runtime is still here. This is
			// the one drain state with no exit of its own: the loop re-queues the
			// same stop every tick, nothing in it ever tells the AGENT anything,
			// and the only thing that has ever cleared such a row is an operator
			// killing the pane. Ask the agent to acknowledge and leave.
			// See drain_reminder.go.
			remindStopPendingDrain(sp, store, info, clk, stderr)
			queueDrainAckAsyncStop(cityPath, store, sp, cfg, info.ID, name, info.InstanceToken, processNames, asyncStopTracker, stderr)
			continue
		}
		// Pool-managed stop-pending beads close here instead of staying open as
		// state=drained: open pool session beads occupy slots in the next demand
		// calculation, while closed beads remain only as lifecycle history.
		finalizeDrainAckStoppedSession(
			cityPath, cfg, store, rigStores, info,
			normalizedSessionTemplateInfo(info, cfg),
			isPoolManagedSessionInfo(info),
			dops, dt, clk, rec, stderr,
		)
		finalized++
	}
	return finalized
}
