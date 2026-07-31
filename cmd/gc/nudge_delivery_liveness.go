package main

import (
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// nudgeAckDeadline bounds how long a delivered nudge may go unacknowledged — no
// forward live-session activity — before delivery is treated as stalled. It is a
// mechanical timeout at the delivery trust boundary, not a judgment: past it, a
// routed-and-delivered session that has not advanced is an actionable fault, not
// silent progress.
const nudgeAckDeadline = 90 * time.Second

// nudgeDeliveryState is the independently-observed delivery lifecycle of a
// routed nudge, computed by cross-checking the route/assignment against the
// recorded delivery time and live session activity. It deliberately never
// collapses "routed" into "active": routed-and-assigned is evidence of intent,
// not of execution. The nudge-starvation incident (gc-snrfp) happened because
// routed/assigned metadata was trusted as proof the live target was working.
type nudgeDeliveryState string

const (
	// nudgeDeliveryUnrouted: no nudge routed to this session.
	nudgeDeliveryUnrouted nudgeDeliveryState = "unrouted"
	// nudgeDeliveryQueued: routed/assigned but not yet delivered to the transport.
	nudgeDeliveryQueued nudgeDeliveryState = "queued"
	// nudgeDeliveryAwaitingAck: delivered to the transport but no forward live
	// activity yet, still within the acknowledgement deadline.
	nudgeDeliveryAwaitingAck nudgeDeliveryState = "awaiting_ack"
	// nudgeDeliveryAcknowledged: live session activity advanced past delivery —
	// the target received the nudge and is executing. The only active-execution state.
	nudgeDeliveryAcknowledged nudgeDeliveryState = "acknowledged"
	// nudgeDeliveryStalled: delivered, no forward activity, past the ack deadline.
	// The actionable stalled-delivery fault.
	nudgeDeliveryStalled nudgeDeliveryState = "stalled"
)

// nudgeDeliveryObservation is the independent evidence set for one routed
// session: the route, the last transport delivery time, the last observed live
// activity, and whether the runtime confirms the process is live. Each is
// gathered from a distinct source so no single feed can fake execution.
type nudgeDeliveryObservation struct {
	Routed      bool      // a nudge is routed/assigned to the session
	DeliveredAt time.Time // last transport delivery time (zero = never delivered)
	LastActive  time.Time // last observed live session activity (zero = unknown)
	Running     bool      // runtime confirms the session process is live
}

// classifyNudgeDelivery reports the independently-observed delivery state from
// the route, the recorded delivery time, and live session activity. The inputs
// are cross-checked rather than trusted individually: a routed nudge is
// "acknowledged" only when the live process advanced its activity past delivery,
// and never on the strength of routing metadata alone. A delivery that has gone
// unacknowledged past ackDeadline is stalled — an actionable fault, not progress.
func classifyNudgeDelivery(obs nudgeDeliveryObservation, now time.Time, ackDeadline time.Duration) nudgeDeliveryState {
	if !obs.Routed {
		return nudgeDeliveryUnrouted
	}
	if obs.DeliveredAt.IsZero() {
		return nudgeDeliveryQueued
	}
	if obs.Running && obs.LastActive.After(obs.DeliveredAt) {
		return nudgeDeliveryAcknowledged
	}
	if now.Sub(obs.DeliveredAt) > ackDeadline {
		return nudgeDeliveryStalled
	}
	return nudgeDeliveryAwaitingAck
}

// isActiveExecution reports whether the observed state is genuine execution.
// Routed-only states (queued / awaiting_ack / stalled) are never active — this
// is the invariant the health and status surfaces enforce so routed-but-idle
// work is never displayed as running.
func (s nudgeDeliveryState) isActiveExecution() bool {
	return s == nudgeDeliveryAcknowledged
}

// isStalledFault reports whether the state is the actionable stalled-delivery
// fault that health surfaces raise and bounded recovery acts on.
func (s nudgeDeliveryState) isStalledFault() bool {
	return s == nudgeDeliveryStalled
}

// sessionStateIsLive reports whether a persisted session state is a live one
// (the process is meant to be running). Only live states can carry the forward
// activity that acknowledges a delivery; a dormant/terminal state never reads as
// executing.
func sessionStateIsLive(s session.State) bool {
	return s == session.StateActive || s == session.StateAwake
}

// nudgeDeliveryObservationFromInfo assembles the independent evidence set from
// the routed flag and the persisted session projection: the last-nudge-delivered
// marker, the last observed activity, and whether the persisted state is live.
// It reads only the persisted projection — no runtime provider is constructed —
// so the status/health read stays side-effect free and errs toward never
// over-reporting execution when liveness is unknown.
func nudgeDeliveryObservationFromInfo(routed bool, info session.Info) nudgeDeliveryObservation {
	return nudgeDeliveryObservation{
		Routed:      routed,
		DeliveredAt: info.LastNudgeDeliveredAt.UTC(),
		LastActive:  info.LastActive.UTC(),
		Running:     sessionStateIsLive(info.State),
	}
}

// nudgeTargetDeliveryState independently cross-checks a target's route, its last
// transport delivery time, and its persisted session activity to report whether
// routed work is merely queued, awaiting acknowledgement, acknowledged
// (executing), or stalled. Routed/assigned state alone is never reported as
// active. The read is best-effort: a missing store or session projection
// degrades toward the safe direction — never over-reporting execution — rather
// than failing the caller. now is injected for testability.
func nudgeTargetDeliveryState(target nudgeTarget, routed bool, now time.Time) nudgeDeliveryState {
	if !routed {
		return nudgeDeliveryUnrouted
	}
	obs := nudgeDeliveryObservation{Routed: routed}
	store := openNudgeBeadStore(target.cityPath)
	if store.Store != nil {
		defer closeBeadStoreHandle(store.Store) //nolint:errcheck // best-effort read
		sessStore := cliSessionStore(store.Store, target.cfg, target.cityPath)
		if info, ok := nudgeTargetSessionInfo(sessStore, target); ok {
			obs = nudgeDeliveryObservationFromInfo(routed, info)
		}
	}
	return classifyNudgeDelivery(obs, now, nudgeAckDeadline)
}

// nudgeTargetSessionInfo resolves the open session bead a nudge target points
// at, preferring the current same-name session (so a continued session's stale
// twin is not read) and falling back to an exact session-ID match. Returns false
// when no open session matches.
func nudgeTargetSessionInfo(sessStore beads.Store, target nudgeTarget) (session.Info, bool) {
	infos, err := loadOpenSessionInfos(sessStore)
	if err != nil {
		return session.Info{}, false
	}
	if target.sessionName != "" {
		if info, ok := currentSessionInfoForName(infos, target.sessionName, target.sessionID); ok {
			return info, true
		}
	}
	if target.sessionID != "" {
		for _, info := range infos {
			if info.ID == target.sessionID {
				return info, true
			}
		}
	}
	return session.Info{}, false
}
