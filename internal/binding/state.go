// Package binding commits the atomic ready→bound transition: one ready
// workload fenced to one reserved unit of capacity.
//
// A Binding is the durable record that a workload has been admitted to run. It
// is written once, by the scheduler, and it is the single atomic admission
// point — the property that makes a second concurrent bind for the same
// workload impossible rather than merely unlikely.
//
// Binding is at-most-once and fail-closed (work-layer semantics): unless the
// scheduler can prove both that the workload is unbound and that a unit of
// capacity is reserved for it, it does not bind. A missed bind is cheap — the
// workload stays ready and the next pass reconsiders it — whereas a double
// bind is the fan-out race the whole design exists to forbid.
//
// The opposite semantic — lease expiry, crash recovery, retry — is
// watchdog-layer, at-least-once, and idempotent. It lives on a separate path
// deliberately: mixing the two would make one of them wrong. This package
// commits the Binding and exposes Release as the seam a terminal outcome or a
// retry calls; it does not decide when to retry, renew a lease, or reject a
// stale executor.
package binding

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/fsys"
)

// Binding is the durable record fencing one workload to one reserved unit of
// capacity.
type Binding struct {
	WorkloadID string `json:"workload_id"`
	Agent      string `json:"agent"`
	Rig        string `json:"rig,omitempty"`
	Provider   string `json:"provider,omitempty"`
	// ReservationRef names the capacity.Reservation backing this Binding. It
	// projects to the bead-metadata key gc.reservation_ref: "reservation"
	// rather than "slot" because a slot is already the worker identity index,
	// and the unit reserved here is not that.
	ReservationRef string `json:"reservation_ref"`
	// Generation rises on every bind of a workload and is the fencing token:
	// monotonic per workload, so a rebind strictly outranks the Binding it
	// replaces. Enforcing the fence against a stale executor is a separate
	// concern on the recovery path.
	Generation int `json:"generation"`
	// Attempt counts executions of this workload, starting at 1.
	Attempt int       `json:"attempt"`
	BoundAt time.Time `json:"bound_at"`
}

// State is the persisted set of committed Bindings.
//
// One bucket, not the lifecycle buckets a queue carries: a Binding's later
// stages (starting, running, terminal) are the execution controller's and the
// recovery scan's to record, and inventing their storage here would be
// guessing at their shape.
type State struct {
	Bound []Binding `json:"bound,omitempty"`
}

// SortState orders Bindings deterministically.
func SortState(state *State) {
	sort.SliceStable(state.Bound, func(i, j int) bool {
		if !state.Bound[i].BoundAt.Equal(state.Bound[j].BoundAt) {
			return state.Bound[i].BoundAt.Before(state.Bound[j].BoundAt)
		}
		return state.Bound[i].WorkloadID < state.Bound[j].WorkloadID
	})
}

// WithState locks, loads, mutates, and atomically rewrites the binding store.
// A non-nil error from fn abandons the write entirely, which is the rollback
// primitive Bind relies on to refuse a binding without disturbing the store.
//
// Callers that also take the capacity ledger's lock must take this one first.
// The lock order is binding outer, capacity inner, never reversed.
func WithState(cityPath string, fn func(*State) error) error {
	dir := filepath.Dir(StatePath(cityPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating binding store dir: %w", err)
	}

	// The lock lives in its own file: the atomic rename below swaps the state
	// file's inode, which would drop a lock held on it.
	lockFile, err := os.OpenFile(LockPath(cityPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening binding store lock: %w", err)
	}
	defer lockFile.Close() //nolint:errcheck

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking binding store: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	// Loading inside the lock is what makes the transaction linearizable
	// across processes.
	state, err := LoadState(cityPath)
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	SortState(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal binding store: %w", err)
	}
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, StatePath(cityPath), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write binding store: %w", err)
	}
	return nil
}

// LoadState reads the persisted Bindings from disk. A missing file is an empty
// store; a corrupt one is an error, because silently resetting it would unbind
// every workload it was fencing and let a second worker launch for each.
func LoadState(cityPath string) (State, error) {
	data, err := os.ReadFile(StatePath(cityPath))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read binding store: %w", err)
	}
	if len(data) == 0 {
		return State{}, nil
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse binding store: %w", err)
	}
	SortState(&state)
	return state, nil
}

// StatePath returns the persisted binding store path for a city.
func StatePath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "binding", "state.json")
}

// LockPath returns the binding store lock path for a city.
func LockPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "binding", "state.lock")
}

// findLive returns a copy of a workload's committed Binding. It returns a copy
// rather than a pointer into the state so callers cannot mutate the pending
// transaction through it.
func findLive(s *State, workloadID string) (Binding, bool) {
	for _, b := range s.Bound {
		if b.WorkloadID == workloadID {
			return b, true
		}
	}
	return Binding{}, false
}
