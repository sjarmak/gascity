package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/nudgequeue"
)

const (
	nudgeDeadlineInstrumentRetry = time.Second
	nudgeDeadlineRescanInterval  = time.Second
)

func nudgeDeadlineDelay(deadline, now time.Time) time.Duration {
	if !deadline.After(now) {
		return 0
	}
	return deadline.Sub(now)
}

func loadNextNudgeDeadline(cityPath string, now time.Time) (time.Time, time.Duration, error) {
	state, err := nudgequeue.LoadState(cityPath)
	if err != nil {
		return time.Time{}, 0, err
	}
	deadline, ok := nudgequeue.NextDeadline(state)
	if !ok {
		return time.Time{}, 0, nil
	}
	return deadline, nudgeDeadlineDelay(deadline, now), nil
}

func maintainNudgeDeadlinesWithStore(cityPath string, store beads.NudgesStore, now time.Time) ([]queuedNudge, error) {
	var expired []queuedNudge
	var terminalErrs []error
	err := withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		state.Pending, expired = partitionExpiredQueuedNudges(state.Pending, expired, now)
		state.InFlight, expired = partitionExpiredQueuedNudges(state.InFlight, expired, now)
		front := nudgeFrontDoor(store)
		for _, item := range expired {
			state.Dead = append(state.Dead, item)
			if err := front.Terminalize(item, "expired", item.LastError, "deadline-timer", now); err != nil {
				terminalErrs = append(terminalErrs, fmt.Errorf("terminalize %s: %w", item.ID, err))
			}
		}
		sortQueuedNudges(state)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return expired, errors.Join(terminalErrs...)
}

func partitionExpiredQueuedNudges(items, expired []queuedNudge, now time.Time) ([]queuedNudge, []queuedNudge) {
	if len(items) == 0 {
		return items, expired
	}
	remaining := make([]queuedNudge, 0, len(items))
	for _, item := range items {
		if item.ExpiresAt.IsZero() || item.ExpiresAt.After(now) {
			remaining = append(remaining, item)
			continue
		}
		item.DeadAt = now.UTC()
		if item.LastError == "" {
			item.LastError = "expired"
		}
		expired = append(expired, item)
	}
	return remaining, expired
}

func (cr *CityRuntime) nudgeDeadlineTick() error {
	expired, err := maintainNudgeDeadlinesWithStore(cr.cityPath, cr.nudgesBeadStore(), time.Now())
	for _, item := range expired {
		fmt.Fprintf(cr.stderr, "%s: nudge deadline expired: id=%s agent=%s session=%s source=%s expires_at=%s\n", //nolint:errcheck
			cr.logPrefix, item.ID, item.Agent, item.SessionID, item.Source, item.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		fmt.Fprintf(cr.stderr, "%s: nudge deadline instrument failure: %v\n", cr.logPrefix, err) //nolint:errcheck
	}
	return err
}

// runNudgeDeadlineLoop owns deadline timing outside the serial reconciler.
// It starts before reconciliation startup, so a slow provider/session operation
// cannot defer a sender-visible failure. Enqueue wakes normally rearm it
// immediately; bounded rescans preserve the deadline instrument if the local
// wake socket is unavailable or a wake is missed during listener restart.
func (cr *CityRuntime) runNudgeDeadlineLoop(ctx context.Context) {
	timer := time.NewTimer(nudgeDeadlineRescanInterval)
	defer timer.Stop()

	for {
		deadline, delay, err := loadNextNudgeDeadline(cr.cityPath, time.Now())
		wait := nudgeDeadlineRescanInterval
		if err != nil {
			fmt.Fprintf(cr.stderr, "%s: nudge deadline instrument failure: %v\n", cr.logPrefix, err) //nolint:errcheck
			wait = nudgeDeadlineInstrumentRetry
		} else if !deadline.IsZero() && delay < wait {
			wait = delay
		}
		resetNudgeDeadlineTimer(timer, wait)

		select {
		case <-ctx.Done():
			return
		case <-cr.nudgeDeadlineWakeCh:
			continue
		case <-timer.C:
		}

		if err != nil || deadline.IsZero() || deadline.After(time.Now()) {
			continue
		}
		if err := cr.nudgeDeadlineTick(); err != nil {
			resetNudgeDeadlineTimer(timer, nudgeDeadlineInstrumentRetry)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}
}

func resetNudgeDeadlineTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}
