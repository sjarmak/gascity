// Package capacity maintains the durable reservation ledger that bounds how
// many workers may run concurrently.
//
// A Reservation is a hold on one unit of worker capacity, taken before a
// workload is bound to an executor and released when that binding reaches a
// terminal outcome or is retried. Because the ledger is durable, a scheduler
// that dies between reserving and binding cannot leak the unit: the hold
// carries a TTL and a later Reclaim returns it.
//
// The ledger deliberately owns only capacity. It does not bind workloads to
// executors, and it does not expire binding leases — those are separate
// concerns on separate paths, because reserving is a work-layer action
// (at-most-once, fail-closed) while lease-expiry recovery is a watchdog-layer
// action (at-least-once, idempotent). Mixing the two would make one of them
// wrong. Consume and Release are the seams a binder and a recovery scan call.
//
// Account identifiers are opaque. The SDK never parses one, never enumerates
// the available set, and holds no provider-specific account knowledge;
// selection belongs to install-side tooling reached through AccountSelector.
package capacity

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

// Reservation is a durable hold on one unit of worker capacity.
type Reservation struct {
	ID         string    `json:"id"`
	WorkloadID string    `json:"workload_id"`
	Agent      string    `json:"agent"`
	Rig        string    `json:"rig,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Account    string    `json:"account,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// ExpiresAt bounds how long an unconsumed hold survives. It governs the
	// held bucket only; once consumed, the binding's own lease governs.
	ExpiresAt  time.Time `json:"expires_at"`
	ConsumedAt time.Time `json:"consumed_at,omitempty"`
}

// State is the persisted ledger snapshot. A reservation's lifecycle stage is
// its bucket membership rather than a status field, mirroring
// internal/nudgequeue.
type State struct {
	// Held reservations are granted but not yet bound; they expire.
	Held []Reservation `json:"held,omitempty"`
	// Consumed reservations back a committed binding and are released
	// explicitly, never by TTL.
	Consumed []Reservation `json:"consumed,omitempty"`
}

// SortState orders reservations deterministically inside each bucket.
func SortState(state *State) {
	byCreation := func(rs []Reservation) func(i, j int) bool {
		return func(i, j int) bool {
			if !rs[i].CreatedAt.Equal(rs[j].CreatedAt) {
				return rs[i].CreatedAt.Before(rs[j].CreatedAt)
			}
			return rs[i].ID < rs[j].ID
		}
	}
	sort.SliceStable(state.Held, byCreation(state.Held))
	sort.SliceStable(state.Consumed, byCreation(state.Consumed))
}

// WithState locks, loads, mutates, and atomically rewrites the ledger.
// A non-nil error from fn abandons the write entirely, which is the
// rollback primitive callers rely on to reject a reservation without
// disturbing the ledger.
func WithState(cityPath string, fn func(*State) error) error {
	dir := filepath.Dir(StatePath(cityPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating capacity ledger dir: %w", err)
	}

	// The lock lives in its own file: the atomic rename below swaps the
	// state file's inode, which would drop a lock held on it.
	lockFile, err := os.OpenFile(LockPath(cityPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening capacity ledger lock: %w", err)
	}
	defer lockFile.Close() //nolint:errcheck

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking capacity ledger: %w", err)
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
		return fmt.Errorf("marshal capacity ledger: %w", err)
	}
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, StatePath(cityPath), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write capacity ledger: %w", err)
	}
	return nil
}

// LoadState reads the persisted ledger from disk. A missing file is an empty
// ledger; a corrupt one is an error, because silently resetting the ledger
// would double-book every account it was tracking.
func LoadState(cityPath string) (State, error) {
	data, err := os.ReadFile(StatePath(cityPath))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read capacity ledger: %w", err)
	}
	if len(data) == 0 {
		return State{}, nil
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse capacity ledger: %w", err)
	}
	SortState(&state)
	return state, nil
}

// StatePath returns the persisted ledger path for a city.
func StatePath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "capacity", "state.json")
}

// LockPath returns the ledger lock path for a city.
func LockPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "capacity", "state.lock")
}
