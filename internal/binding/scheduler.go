package binding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/capacity"
)

// errSkipCandidate marks a candidate the pass declines and moves past: a
// decision, not a failure. It never escapes Bind.
var errSkipCandidate = errors.New("skip candidate")

// errLostRace triggers the rollback of a commit that another binder won. It
// never escapes commit.
var errLostRace = errors.New("bound concurrently")

// HealthCheck reports whether a provider is fit to receive work.
//
// It returns (healthy, present). A present=false answer means the health
// registry has nothing fresh to say about the provider, and the caller admits
// the work anyway: an unknown provider is not evidence of a sick one. This is
// the contract the reconciler's existing gate already implements, and
// production must inject that same snapshot rather than a second
// implementation — two health gates that drift apart would let the ledger and
// the binder disagree about which providers are usable.
type HealthCheck func(provider string) (healthy, present bool)

// ReadyWorkload is one admission-cleared candidate for binding.
//
// Every field is structured: a band, a clock, an identifier. Choosing among
// candidates is mechanical here by design — the judgment about whether a
// workload deserves to run at all is made upstream, and the scheduler only
// applies the order that judgment implies.
type ReadyWorkload struct {
	ID       string
	Agent    string
	Rig      string
	Provider string
	// PriorityBand orders candidates; lower binds first.
	PriorityBand int
	// EnqueuedAt is the aging clock, and orders candidates within a band.
	EnqueuedAt time.Time
	// PriorGeneration and PriorAttempt are the workload's current values, which
	// the Binding succeeds. Both are zero for a workload that has never run.
	PriorGeneration int
	PriorAttempt    int
}

// BindRequest is one scheduling pass.
type BindRequest struct {
	Candidates []ReadyWorkload
	Caps       capacity.Caps
}

// Scheduler commits the ready→bound transition for one city.
type Scheduler struct {
	cityPath string
	ledger   *capacity.Ledger
	now      func() time.Time
	health   HealthCheck
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithClock overrides the time source.
func WithClock(fn func() time.Time) Option { return func(s *Scheduler) { s.now = fn } }

// WithHealthCheck supplies the provider health gate. Without one the scheduler
// has no health dimension and every candidate's provider is treated as fit.
func WithHealthCheck(fn HealthCheck) Option { return func(s *Scheduler) { s.health = fn } }

// NewScheduler returns a scheduler binding workloads against ledger for the
// city at cityPath.
func NewScheduler(cityPath string, ledger *capacity.Ledger, opts ...Option) *Scheduler {
	s := &Scheduler{
		cityPath: cityPath,
		ledger:   ledger,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Bind commits at most one Binding: the first candidate, in band then
// first-come order, that is unbound, whose provider is fit, and for which a
// unit of capacity can be reserved.
//
// A nil Binding with a nil error is the ordinary "nothing to bind" answer —
// the pass found no candidate it could prove admissible. That is not a
// failure: the workloads stay ready and the next pass reconsiders them. Bind
// returns an error only when it cannot tell whether binding is safe, and it
// binds nothing in that case.
//
// One Binding per pass, deliberately. Committing a batch would mean either
// holding the lock across every reservation in it or accepting a partial
// batch, and a pass that binds one workload and loses the rest is exactly the
// partial state this transition exists to make unreachable.
func (s *Scheduler) Bind(ctx context.Context, req BindRequest) (*Binding, error) {
	if err := validate(req.Candidates); err != nil {
		return nil, err
	}
	// Clone before sorting: the caller's slice is theirs.
	candidates := slices.Clone(req.Candidates)
	sortCandidates(candidates)

	// One unlocked read serves every candidate's phase-1 pre-check: the store
	// only changes on a successful commit, which ends this loop immediately, so
	// every candidate in one pass sees the same state. Phase 3 stays the
	// authority under the lock; this snapshot is advisory, same as before.
	state, err := LoadState(s.cityPath)
	if err != nil {
		return nil, err
	}

	for _, w := range candidates {
		b, err := s.tryBind(ctx, w, req.Caps, &state)
		if errors.Is(err, errSkipCandidate) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, nil
}

// Release drops a workload's Binding and returns its unit of capacity. It is
// the seam a terminal outcome or a retry calls; the decision to call it
// belongs to those paths, not to this one.
//
// Release is idempotent and treats an unbound workload as success: the
// recovery scan that calls it is at-least-once, and failing a second call
// would turn a healthy repair into a spurious error.
func (s *Scheduler) Release(workloadID string) error {
	return WithState(s.cityPath, func(st *State) error {
		b, ok := findLive(st, workloadID)
		if !ok {
			return nil
		}
		// Capacity's lock is taken inside this one, never the reverse. Ordering
		// the release this way also means a failure here leaves the Binding
		// intact and the whole call retryable, rather than dropping the fence
		// while the unit stays booked.
		if err := s.ledger.Release(b.ReservationRef); err != nil {
			return fmt.Errorf("releasing reservation %q for workload %q: %w", b.ReservationRef, workloadID, err)
		}
		st.Bound = removeByWorkload(st.Bound, workloadID)
		return nil
	})
}

// tryBind runs the three-phase bind for one candidate, mirroring the ledger's
// own reserve: a cheap unlocked pre-check, the slow work outside every lock,
// then a locked re-check that is the authority.
func (s *Scheduler) tryBind(ctx context.Context, w ReadyWorkload, caps capacity.Caps, state *State) (*Binding, error) {
	// The health gate sits in front of the reservation so a candidate bound for
	// a sick provider never spends a unit to discover it.
	if s.health != nil {
		if healthy, present := s.health(w.Provider); present && !healthy {
			return nil, fmt.Errorf("%w: provider %q is unhealthy", errSkipCandidate, w.Provider)
		}
	}

	// Phase 1 (unlocked): an already-bound workload is not a candidate. This is
	// only an optimization — phase 3 re-checks under the lock — so a stale read
	// here costs a wasted reservation, never a double bind.
	if _, bound := findLive(state, w.ID); bound {
		return nil, fmt.Errorf("%w: workload %q is already bound", errSkipCandidate, w.ID)
	}

	// Phase 2 (unlocked): the ledger takes its own lock, and the account
	// selector behind it may shell out and reach the network. Neither may run
	// while this package's lock is held.
	rsv, err := s.ledger.Reserve(ctx, capacity.ReserveRequest{
		WorkloadID: w.ID,
		Agent:      w.Agent,
		Rig:        w.Rig,
		Provider:   w.Provider,
		Caps:       caps,
	})
	if err != nil {
		// A refusal names the scope that is full: this candidate cannot run
		// right now, but another one may still fit, so the pass moves on.
		if isCapacityRejection(err) {
			return nil, fmt.Errorf("%w: %w", errSkipCandidate, err)
		}
		return nil, fmt.Errorf("reserving capacity for workload %q: %w", w.ID, err)
	}

	// Phase 3 (locked): re-check against the present store and commit.
	return s.commit(w, rsv)
}

// commit writes the Binding under the lock, or writes nothing.
func (s *Scheduler) commit(w ReadyWorkload, rsv capacity.Reservation) (*Binding, error) {
	var (
		out    Binding
		strand bool
	)
	err := WithState(s.cityPath, func(st *State) error {
		if existing, ok := findLive(st, w.ID); ok {
			// Only release a unit that is not the winner's. The ledger's
			// per-workload idempotence hands concurrent binders the same
			// reservation, so releasing it blindly here would strip the unit
			// out from under the Binding that just won.
			strand = existing.ReservationRef != rsv.ID
			return errLostRace
		}
		// Consuming inside this lock is the interlock that makes the bind
		// fail-closed: it proves the unit is still ours. A hold that expired
		// and was reclaimed while phase 2 ran fails here, and the closure's
		// error abandons the write, so no Binding is ever fenced to capacity
		// the ledger has already given away.
		if err := s.ledger.Consume(rsv.ID); err != nil {
			return fmt.Errorf("consuming reservation %q for workload %q: %w", rsv.ID, w.ID, err)
		}
		out = Binding{
			WorkloadID:     w.ID,
			Agent:          w.Agent,
			Rig:            w.Rig,
			Provider:       w.Provider,
			ReservationRef: rsv.ID,
			Generation:     w.PriorGeneration + 1,
			Attempt:        w.PriorAttempt + 1,
			BoundAt:        s.now(),
		}
		st.Bound = append(st.Bound, out)
		return nil
	})
	if err != nil {
		lost := errors.Is(err, errLostRace)
		if !lost || strand {
			if relErr := s.ledger.Release(rsv.ID); relErr != nil {
				return nil, fmt.Errorf("releasing reservation %q after a refused bind of workload %q: %w", rsv.ID, w.ID, relErr)
			}
		}
		if lost {
			return nil, fmt.Errorf("%w: workload %q was bound concurrently", errSkipCandidate, w.ID)
		}
		return nil, err
	}
	return &out, nil
}

// validate rejects a malformed candidate. A candidate with no identity is a
// caller bug rather than a workload that cannot run, so it fails the pass
// instead of being quietly skipped.
func validate(ws []ReadyWorkload) error {
	for _, w := range ws {
		if strings.TrimSpace(w.ID) == "" {
			return errors.New("binding: candidate requires a workload id")
		}
		if strings.TrimSpace(w.Agent) == "" {
			return fmt.Errorf("binding: candidate %q requires an agent", w.ID)
		}
	}
	return nil
}

// sortCandidates puts the next workload to bind first: band ascending, then
// oldest first within a band. This is the whole scheduling policy — a strict
// superset of oldest-first that keeps a high band from starving behind an old
// low-priority backlog, while aging still decides among peers.
func sortCandidates(ws []ReadyWorkload) {
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].PriorityBand != ws[j].PriorityBand {
			return ws[i].PriorityBand < ws[j].PriorityBand
		}
		if !ws[i].EnqueuedAt.Equal(ws[j].EnqueuedAt) {
			return ws[i].EnqueuedAt.Before(ws[j].EnqueuedAt)
		}
		return ws[i].ID < ws[j].ID
	})
}

// isCapacityRejection reports whether the ledger declined to grant a unit, as
// opposed to failing to determine whether it could.
func isCapacityRejection(err error) bool {
	for _, rejection := range []error{
		capacity.ErrAgentCapReached,
		capacity.ErrRigCapReached,
		capacity.ErrWorkspaceCapReached,
		capacity.ErrAccountCapReached,
		capacity.ErrNoAccountAvailable,
		capacity.ErrReservationMismatch,
	} {
		if errors.Is(err, rejection) {
			return true
		}
	}
	return false
}

func removeByWorkload(bs []Binding, workloadID string) []Binding {
	out := bs[:0]
	for _, b := range bs {
		if b.WorkloadID != workloadID {
			out = append(out, b)
		}
	}
	return out
}
