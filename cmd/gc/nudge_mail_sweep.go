package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/nudgequeue"
)

const (
	nudgeMailSweepDefaultNudgeTTL     = 10 * time.Minute
	nudgeMailSweepDefaultMailTTL      = 60 * time.Minute
	nudgeMailSweepCloseBudget         = 50
	nudgeMailSweepWatchdogInterval    = 5 * time.Minute
	nudgeMailSweepWatchdogCloseBudget = 500

	// nudgeMailSweepNudgeCloseReason is the close_reason stamped on stale nudge
	// beads before close. The 20-character floor satisfies validation.on-close=error.
	nudgeMailSweepNudgeCloseReason = "nudge gc-swept: stale nudge bead past gc retention window"

	// nudgeMailSweepMailCloseReason is the close_reason stamped on read mail
	// beads before close. It is beadmail.RetentionSweepCloseReason so beadmail's
	// direct-ID gate recognizes these beads as retention-swept (system-aged,
	// still addressable until purge) rather than user-removed.
	nudgeMailSweepMailCloseReason = beadmail.RetentionSweepCloseReason
)

// nudgeMailSweepResult holds per-category close counts from sweepStaleNudgeMail.
type nudgeMailSweepResult struct {
	NudgeClosed int
	MailClosed  int
}

// sweepStaleNudgeMail closes stale consumed nudge beads and read mail beads.
//
// Nudge candidates are open beads with label gc:nudge created before now-nudgeTTL
// whose nudge_id is not present in nudgeState.Pending or nudgeState.InFlight.
// Terminal metadata is stamped via nudgequeue.Store.SweepStale before each close
// so the bead audit trail is intact.
//
// Mail candidates are open message beads with label "read" created before now-mailTTL.
//
// limit caps total closes (nudge + mail combined). Pass 0 for no cap.
// Per-bead errors do not abort the sweep; they are returned via errors.Join so
// the caller can report them without treating the sweep as fatal.
//
// The nudge phase is sourced from the strongly-typed nudgeStore (the nudges
// class); the mail phase from the strongly-typed mailStore (the messaging class).
// Both wrap the same underlying work store until either class relocates, so
// behavior is unchanged today.
func sweepStaleNudgeMail(nudgeStore beads.NudgesStore, mailStore beads.MailStore, nudgeState *nudgequeue.State, now time.Time, nudgeTTL, mailTTL time.Duration, limit int) (nudgeMailSweepResult, error) {
	var result nudgeMailSweepResult
	var beadErrs []error

	liveIDs := liveNudgeIDSet(nudgeState)
	nq := nudgequeue.NewStore(nudgeStore)

	// Phase 1: close stale nudge beads. The live flock-queue exclusion is carried
	// inside StaleShadowsBefore; the cross-phase close budget stays in this loop.
	nudgeCutoff := now.Add(-nudgeTTL)
	// nudge/mail beads are NoHistory (wisp-tier); StaleShadowsBefore reads both tiers.
	nudgeShadows, err := nq.StaleShadowsBefore(nudgeCutoff, limit, liveIDs)
	if err != nil {
		return result, fmt.Errorf("nudge-mail-sweep: listing stale nudge beads: %w", err)
	}

	for _, shadow := range nudgeShadows {
		if limit > 0 && result.NudgeClosed+result.MailClosed >= limit {
			break
		}
		if !shadow.Open {
			continue
		}
		if err := nq.SweepStale(shadow.BeadID, nudgeMailSweepNudgeCloseReason, now); err != nil {
			beadErrs = append(beadErrs, err)
			continue
		}
		result.NudgeClosed++
	}

	// Phase 2: close read mail beads. The candidate query + close-with-reason
	// loop live inside the messaging edge (beadmail); only the shared close
	// budget is passed in. mailBudget is the remaining share of the combined
	// limit, so a fatal listing failure early-returns (discarding accumulated
	// per-bead errors) exactly as the inline loop did.
	mailCutoff := now.Add(-mailTTL)
	remaining := limit - result.NudgeClosed - result.MailClosed
	if limit == 0 || remaining > 0 {
		mailBudget := remaining
		if limit == 0 {
			mailBudget = 0
		}
		mailClosed, mailCloseErrs, mailListErr := beadmail.SweepReadMessagesBefore(mailStore, mailCutoff, mailBudget, nudgeMailSweepMailCloseReason)
		if mailListErr != nil {
			return result, fmt.Errorf("nudge-mail-sweep: listing read mail beads: %w", mailListErr)
		}
		result.MailClosed += mailClosed
		beadErrs = append(beadErrs, mailCloseErrs...)
	}

	return result, errors.Join(beadErrs...)
}

// countStaleNudgeMail returns what sweepStaleNudgeMail would close without
// making any changes. Used by --dry-run to report candidate count without side
// effects. The limit parameter caps the count the same way sweepStaleNudgeMail
// caps closes; pass 0 for no cap. The nudge phase is counted from the typed
// nudgeStore (nudges class); the mail phase from the typed mailStore (messaging class).
func countStaleNudgeMail(nudgeStore beads.NudgesStore, mailStore beads.MailStore, nudgeState *nudgequeue.State, now time.Time, nudgeTTL, mailTTL time.Duration, limit int) (nudgeMailSweepResult, error) {
	var result nudgeMailSweepResult

	liveIDs := liveNudgeIDSet(nudgeState)
	nq := nudgequeue.NewStore(nudgeStore)

	// Dry-run twin of the sweep: same typed read, same cross-phase budget, no writes.
	nudgeCutoff := now.Add(-nudgeTTL)
	nudgeShadows, err := nq.StaleShadowsBefore(nudgeCutoff, limit, liveIDs)
	if err != nil {
		return result, fmt.Errorf("nudge-mail-sweep (dry-run): listing stale nudge beads: %w", err)
	}
	for _, shadow := range nudgeShadows {
		if limit > 0 && result.NudgeClosed+result.MailClosed >= limit {
			break
		}
		if !shadow.Open {
			continue
		}
		result.NudgeClosed++
	}

	mailCutoff := now.Add(-mailTTL)
	remaining := limit - result.NudgeClosed - result.MailClosed
	if limit == 0 || remaining > 0 {
		mailBudget := remaining
		if limit == 0 {
			mailBudget = 0
		}
		mailCount, err := beadmail.CountReadMessagesBefore(mailStore, mailCutoff, mailBudget)
		if err != nil {
			return result, fmt.Errorf("nudge-mail-sweep (dry-run): listing read mail beads: %w", err)
		}
		result.MailClosed += mailCount
	}
	return result, nil
}

// nudgeMailRetentionDeleteBudget caps closed nudge + mail bead deletions per
// retention sweep invocation. It mirrors the order-tracking deletion budget
// precedent: a bounded batch keeps each run's contention proportional while the
// recurring sweep drains any backlog over successive ticks (report #3342 section
// 5 gate #5).
const nudgeMailRetentionDeleteBudget = 500

// defaultNudgeDeleteAfterClose and defaultMailDeleteAfterClose are the runtime
// fallbacks for the nudge/mail retention TTLs, derived from the canonical config
// constants so load-time defaults and the runtime fallback stay in sync.
var (
	defaultNudgeDeleteAfterClose = config.BeadPolicyConfig{
		DeleteAfterClose: config.DefaultNudgeDeleteAfterClose,
	}.DeleteAfterCloseDuration()
	defaultMailDeleteAfterClose = config.BeadPolicyConfig{
		DeleteAfterClose: config.DefaultMailDeleteAfterClose,
	}.DeleteAfterCloseDuration()
)

// nudgeMailRetentionResult holds per-category delete counts from
// sweepClosedNudgeMailRetention.
type nudgeMailRetentionResult struct {
	NudgeDeleted int
	MailDeleted  int
}

// nudgeMailRetentionPolicy holds the resolved delete-after-close TTLs for the
// nudge and mail bead families. A non-positive duration disables deletion for
// that family.
type nudgeMailRetentionPolicy struct {
	nudgeDeleteAfterClose time.Duration
	mailDeleteAfterClose  time.Duration
}

// nudgeMailRetentionPolicyForConfig resolves delete-after-close TTLs from the
// [beads.policies.nudge] and [beads.policies.mail] config sections. Unset or
// non-positive durations fall back to the controller defaults (nudge 24h, mail
// 72h) so retention is on by default, matching the order-tracking policy
// precedent.
func nudgeMailRetentionPolicyForConfig(cfg *config.City) nudgeMailRetentionPolicy {
	policy := nudgeMailRetentionPolicy{
		nudgeDeleteAfterClose: defaultNudgeDeleteAfterClose,
		mailDeleteAfterClose:  defaultMailDeleteAfterClose,
	}
	if cfg == nil {
		return policy
	}
	if configured, ok := cfg.Beads.Policies[config.BeadPolicyNudge]; ok {
		if d := configured.DeleteAfterCloseDuration(); d > 0 {
			policy.nudgeDeleteAfterClose = d
		}
	}
	if configured, ok := cfg.Beads.Policies[config.BeadPolicyMail]; ok {
		if d := configured.DeleteAfterCloseDuration(); d > 0 {
			policy.mailDeleteAfterClose = d
		}
	}
	return policy
}

// sweepClosedNudgeMailRetention deletes closed nudge and read mail beads whose
// close time is older than the configured delete-after-close TTL. It is the
// archive-then-delete tail that runs after the close phase (sweepStaleNudgeMail):
// the periodic jsonl export captures every closed row on a 5-minute cadence,
// and the delete TTLs (nudge 24h, mail 72h) dwarf that cadence, so the jsonl is
// the system of record before any row is deleted here. Deleting a bead cascades
// its events and labels rows at the storage layer, so the per-bead row
// multipliers do not survive their parents.
//
// NDI safety gates (report #3342 section 5):
//   - closed-only + close-time cutoff — open/in_progress work is never touched;
//   - live-nudge protection — a nudge whose ID is still pending or in-flight is
//     never deleted, even past its TTL, so a consumed-signal/cooldown window is
//     respected;
//   - ownership-edge skip — a bead another bead still depends on is left for a
//     graph-aware reaper rather than severed here.
//
// limit caps total deletions (nudge + mail combined); pass 0 for no cap.
// Per-bead errors do not abort the sweep; they are joined and returned so the
// caller can report them without treating the sweep as fatal.
func sweepClosedNudgeMailRetention(store beads.Store, nudgeState *nudgequeue.State, now time.Time, policy nudgeMailRetentionPolicy, limit int) (nudgeMailRetentionResult, error) {
	return nudgeMailRetentionSweep(store, nudgeState, now, policy, limit, false)
}

// countClosedNudgeMailRetention returns what sweepClosedNudgeMailRetention would
// delete without making any changes. Used by --dry-run to report the candidate
// count, including the ownership-edge and live-nudge skips, without side effects.
func countClosedNudgeMailRetention(store beads.Store, nudgeState *nudgequeue.State, now time.Time, policy nudgeMailRetentionPolicy, limit int) (nudgeMailRetentionResult, error) {
	return nudgeMailRetentionSweep(store, nudgeState, now, policy, limit, true)
}

func nudgeMailRetentionSweep(store beads.Store, nudgeState *nudgequeue.State, now time.Time, policy nudgeMailRetentionPolicy, limit int, dryRun bool) (nudgeMailRetentionResult, error) {
	var result nudgeMailRetentionResult
	if store == nil {
		return result, fmt.Errorf("nudge-mail-retention: bead store unavailable")
	}
	var beadErrs []error
	liveIDs := liveNudgeIDSet(nudgeState)
	// Read via the Live handle: close time (UpdatedAt) is a mutated timestamp, so
	// purge callers must bypass any cache to avoid acting on stale values.
	live := beads.HandlesFor(store).Live

	// Phase 1: delete closed nudge beads past their delete TTL.
	if policy.nudgeDeleteAfterClose > 0 {
		cutoff := now.Add(-policy.nudgeDeleteAfterClose)
		queryLimit := limit
		if queryLimit < 0 {
			queryLimit = 0
		}
		candidates, err := live.List(beads.ListQuery{
			Status:        "closed",
			Label:         nudgeBeadLabel,
			UpdatedBefore: cutoff,
			Limit:         queryLimit,
			Sort:          beads.SortCreatedAsc,
			TierMode:      beads.TierBoth,
		})
		if err != nil {
			return result, fmt.Errorf("nudge-mail-retention: listing closed nudge beads: %w", err)
		}
		for _, b := range candidates {
			if limit > 0 && result.NudgeDeleted+result.MailDeleted >= limit {
				break
			}
			if b.Status != "closed" {
				continue
			}
			nudgeID := strings.TrimSpace(b.Metadata["nudge_id"])
			if nudgeID != "" && liveIDs[nudgeID] {
				continue
			}
			deleted, err := deleteRetiredCoordinationBead(store, b.ID, dryRun)
			if err != nil {
				beadErrs = append(beadErrs, fmt.Errorf("nudge %s: %w", b.ID, err))
				continue
			}
			if deleted {
				result.NudgeDeleted++
			}
		}
	}

	// Phase 2: delete closed read mail beads past their delete TTL.
	remaining := limit - result.NudgeDeleted - result.MailDeleted
	if policy.mailDeleteAfterClose > 0 && (limit == 0 || remaining > 0) {
		cutoff := now.Add(-policy.mailDeleteAfterClose)
		queryLimit := remaining
		if limit == 0 {
			queryLimit = 0
		}
		candidates, err := live.List(beads.ListQuery{
			Status:        "closed",
			Type:          "message",
			Label:         "read",
			UpdatedBefore: cutoff,
			Limit:         queryLimit,
			Sort:          beads.SortCreatedAsc,
			TierMode:      beads.TierBoth,
		})
		if err != nil {
			return result, fmt.Errorf("nudge-mail-retention: listing closed mail beads: %w", err)
		}
		for _, b := range candidates {
			if limit > 0 && result.NudgeDeleted+result.MailDeleted >= limit {
				break
			}
			if b.Status != "closed" {
				continue
			}
			deleted, err := deleteRetiredCoordinationBead(store, b.ID, dryRun)
			if err != nil {
				beadErrs = append(beadErrs, fmt.Errorf("mail %s: %w", b.ID, err))
				continue
			}
			if deleted {
				result.MailDeleted++
			}
		}
	}

	return result, errors.Join(beadErrs...)
}

// deleteRetiredCoordinationBead deletes a single closed coordination bead after
// the NDI ownership-edge gate. A bead another bead still depends on (a non-empty
// inbound/"up" dependency set) is left in place for a graph-aware reaper rather
// than severed here, so deleting it can never orphan a surviving dependent.
//
// Beads with no inbound edge are deleted directly via store.Delete. In
// production (BdStore → `bd delete --force` → DoltLite) that cascades the bead's
// events, labels, comments and its own dependency rows at the storage layer —
// the events/labels tables declare FOREIGN KEY (issue_id) ... ON DELETE CASCADE,
// and the single-delete path additionally clears both dependency directions
// (steveyegge/beads internal/storage/issueops/delete.go DeleteIssueInTx) — so
// the ~2.5x events / ~3x labels row multipliers do not survive their parents.
//
// It returns (false, nil) — not an error — when the bead is skipped by the gate.
// In dryRun mode it still performs the gate check so the reported count matches
// a real sweep, but makes no deletion.
//
// Note: this deliberately uses an edge *skip* rather than deleteWorkflowBead's
// edge *removal*. deleteWorkflowBead is built for workflow roots whose whole
// ownership tree is being collected; for a stray coordination bead, severing a
// surviving dependent's edge would violate the NDI gate, so the gate skips it.
func deleteRetiredCoordinationBead(store beads.Store, id string, dryRun bool) (bool, error) {
	dependents, err := store.DepList(id, "up")
	if err != nil {
		return false, fmt.Errorf("list dependents: %w", err)
	}
	if len(dependents) > 0 {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := store.Delete(id); err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return true, nil
}

// liveNudgeIDSet returns the set of nudge IDs currently in pending or in-flight state.
// Returns nil (no live IDs) when nudgeState is nil.
func liveNudgeIDSet(state *nudgequeue.State) map[string]bool {
	if state == nil {
		return nil
	}
	live := make(map[string]bool, len(state.Pending)+len(state.InFlight))
	for _, item := range state.Pending {
		live[item.ID] = true
	}
	for _, item := range state.InFlight {
		live[item.ID] = true
	}
	return live
}
