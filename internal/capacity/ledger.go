package capacity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DefaultTTL bounds how long an unconsumed hold survives. It only needs to
// outlast the gap between reserving and binding; a scheduler that dies in
// that window has its unit returned by the next Reclaim.
const DefaultTTL = 10 * time.Minute

// Reservation rejections. Each names the scope that refused, so a caller can
// attribute the decision without re-deriving it.
var (
	ErrAgentCapReached     = errors.New("agent capacity cap reached")
	ErrRigCapReached       = errors.New("rig capacity cap reached")
	ErrWorkspaceCapReached = errors.New("workspace capacity cap reached")
	ErrAccountCapReached   = errors.New("account capacity cap reached")
	ErrNoAccountAvailable  = errors.New("no account available")
	ErrNotFound            = errors.New("reservation not found")
	// ErrReservationMismatch reports that a workload already holds a
	// reservation placed somewhere other than where the caller is now asking
	// for one.
	ErrReservationMismatch = errors.New("workload already reserved elsewhere")
)

// Caps bounds concurrent reservations at each scope. A nil pointer, an absent
// map entry, or a negative value means unlimited, matching the *int convention
// config already uses for max_active_sessions.
type Caps struct {
	Agent     map[string]*int
	Rig       map[string]*int
	Workspace *int
	// Account caps concurrent reservations per account. It is enforced only
	// for reservations that carry an account, which requires a selector.
	Account *int
}

// ReserveRequest asks for one unit of capacity for one workload.
type ReserveRequest struct {
	WorkloadID string
	Agent      string
	Rig        string
	Provider   string
	Caps       Caps
}

// Snapshot is the observable ledger state.
type Snapshot struct {
	Held      []Reservation
	Consumed  []Reservation
	ByAgent   map[string]int
	ByRig     map[string]int
	ByAccount map[string]int
	Total     int
}

// Ledger is the durable reservation ledger for one city.
type Ledger struct {
	cityPath string
	selector AccountSelector
	now      func() time.Time
	newID    func() (string, error)
	ttl      time.Duration
}

// Option configures a Ledger.
type Option func(*Ledger)

// WithSelector supplies the account selector. Without one the ledger has no
// account dimension: reservations carry no account and no account cap applies.
func WithSelector(s AccountSelector) Option { return func(l *Ledger) { l.selector = s } }

// WithClock overrides the time source.
func WithClock(fn func() time.Time) Option { return func(l *Ledger) { l.now = fn } }

// WithIDFunc overrides reservation ID generation.
func WithIDFunc(fn func() (string, error)) Option { return func(l *Ledger) { l.newID = fn } }

// WithTTL overrides how long an unconsumed hold survives.
func WithTTL(d time.Duration) Option { return func(l *Ledger) { l.ttl = d } }

// NewLedger returns a ledger over the city at cityPath.
func NewLedger(cityPath string, opts ...Option) *Ledger {
	l := &Ledger{
		cityPath: cityPath,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    randomID,
		ttl:      DefaultTTL,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading random bytes for reservation id: %w", err)
	}
	return "rsv-" + hex.EncodeToString(b[:]), nil
}

// Reserve holds one unit of capacity for a workload.
//
// Reserve is fail-closed: unless it can prove the unit is within every cap it
// refuses. A refusal is cheap — the workload stays eligible and the next
// scheduling pass reconsiders it — whereas an over-granted unit oversubscribes
// a real account.
//
// Reserve is idempotent per workload: a workload that already holds a live
// reservation at the requested placement gets that reservation back rather
// than a second unit. Asking for a different placement is an error rather
// than a silent no-op, because a caller that believes it holds capacity
// somewhere the ledger does not would oversubscribe that scope; release the
// reservation first to move a workload.
func (l *Ledger) Reserve(ctx context.Context, req ReserveRequest) (Reservation, error) {
	if strings.TrimSpace(req.WorkloadID) == "" {
		return Reservation{}, errors.New("capacity: reserve requires a workload id")
	}
	if strings.TrimSpace(req.Agent) == "" {
		return Reservation{}, errors.New("capacity: reserve requires an agent")
	}

	// Phase 1 (locked): reclaim expired holds, short-circuit an existing
	// reservation, and pre-check caps so an over-cap request never pays for
	// a selector subprocess.
	existing, saturated, err := l.prepare(req)
	if err != nil {
		return Reservation{}, err
	}
	if existing != nil {
		return *existing, nil
	}

	// Phase 2 (unlocked): pick an account. The selector may shell out to
	// install-side tooling and refresh a usage cache over the network, so it
	// must not run while the ledger lock is held.
	account, err := l.pickAccount(ctx, req, saturated)
	if err != nil {
		return Reservation{}, err
	}

	// Phase 3 (locked): re-check against the current ledger and commit. The
	// pre-check in phase 1 is only an optimization; this is the authority.
	return l.commit(req, account)
}

// prepare runs the locked pre-pass: reclaim, existing-reservation lookup, and
// a cap pre-check. It returns the accounts already at their cap so the
// selector can be asked to avoid them.
func (l *Ledger) prepare(req ReserveRequest) (existing *Reservation, saturated []string, err error) {
	var rejection error
	txErr := WithState(l.cityPath, func(s *State) error {
		// Amortize reclamation onto the reservation path so an abandoned
		// hold never wedges a cap until a separate sweep happens to run.
		// The closure returns nil even when the request is rejected below,
		// so this reclamation persists regardless of this request's fate.
		reclaimExpired(s, l.now())

		if r, ok := findLive(s, req.WorkloadID); ok {
			if rejection = checkPlacement(r, req); rejection == nil {
				existing = &r
			}
			return nil
		}
		if rejection = checkCaps(s, req); rejection != nil {
			return nil
		}
		saturated = saturatedAccounts(s, req.Caps.Account)
		return nil
	})
	if txErr != nil {
		return nil, nil, txErr
	}
	if rejection != nil {
		return nil, nil, rejection
	}
	return existing, saturated, nil
}

// pickAccount resolves the account for a reservation. With no selector the
// ledger has no account dimension and the reservation carries none.
func (l *Ledger) pickAccount(ctx context.Context, req ReserveRequest, saturated []string) (string, error) {
	if l.selector == nil {
		return "", nil
	}
	account, err := l.selector(ctx, PickRequest{
		Agent:    req.Agent,
		Provider: req.Provider,
		Exclude:  saturated,
	})
	if err != nil {
		return "", fmt.Errorf("%w: selecting account for agent %q: %w", ErrNoAccountAvailable, req.Agent, err)
	}
	if strings.TrimSpace(account) == "" {
		return "", fmt.Errorf("%w: selector returned no account for agent %q", ErrNoAccountAvailable, req.Agent)
	}
	return account, nil
}

// commit re-verifies under lock and appends the reservation. Every rejection
// here returns from the closure, which abandons the write.
func (l *Ledger) commit(req ReserveRequest, account string) (Reservation, error) {
	var out Reservation
	err := WithState(l.cityPath, func(s *State) error {
		// Recount against the present ledger: a hold may have expired while
		// the selector ran, and another reserver may have taken a unit.
		reclaimExpired(s, l.now())

		if r, ok := findLive(s, req.WorkloadID); ok {
			if err := checkPlacement(r, req); err != nil {
				return err
			}
			out = r
			return nil
		}
		if err := checkCaps(s, req); err != nil {
			return err
		}
		// The selector is advisory: it does not hold the ledger lock and
		// cannot see a concurrent grant, so its choice is re-checked here.
		if err := checkAccountCap(s, account, req.Caps.Account); err != nil {
			return err
		}
		id, err := l.newID()
		if err != nil {
			return err
		}
		now := l.now()
		out = Reservation{
			ID:         id,
			WorkloadID: req.WorkloadID,
			Agent:      req.Agent,
			Rig:        req.Rig,
			Provider:   req.Provider,
			Account:    account,
			CreatedAt:  now,
			ExpiresAt:  now.Add(l.ttl),
		}
		s.Held = append(s.Held, out)
		return nil
	})
	if err != nil {
		return Reservation{}, err
	}
	return out, nil
}

// Consume marks a reservation as backing a committed binding. The unit stays
// occupied; only its expiry stops applying, because from here the binding's
// lease governs the lifetime. Consume is idempotent.
func (l *Ledger) Consume(id string) error {
	var missing bool
	err := WithState(l.cityPath, func(s *State) error {
		for _, r := range s.Consumed {
			if r.ID == id {
				return nil // already consumed
			}
		}
		for i, r := range s.Held {
			if r.ID != id {
				continue
			}
			r.ConsumedAt = l.now()
			r.ExpiresAt = time.Time{}
			s.Held = append(s.Held[:i], s.Held[i+1:]...)
			s.Consumed = append(s.Consumed, r)
			return nil
		}
		missing = true
		return nil
	})
	if err != nil {
		return err
	}
	if missing {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return nil
}

// Release returns a reservation's unit to the pool. It is called when a
// binding reaches a terminal outcome or is retried.
//
// Release is idempotent and treats an unknown ID as success: the recovery
// scan that calls it is at-least-once, and an error on a second call would
// turn a healthy repair into a spurious failure.
func (l *Ledger) Release(id string) error {
	return WithState(l.cityPath, func(s *State) error {
		s.Held = removeByID(s.Held, id)
		s.Consumed = removeByID(s.Consumed, id)
		return nil
	})
}

// Reclaim returns the units of holds that expired before being consumed and
// reports what it reclaimed. This is the repair for a scheduler that died
// between reserving and binding. It is idempotent.
func (l *Ledger) Reclaim() ([]Reservation, error) {
	var reclaimed []Reservation
	err := WithState(l.cityPath, func(s *State) error {
		reclaimed = reclaimExpired(s, l.now())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reclaimed, nil
}

// Snapshot reports the current ledger for observability.
func (l *Ledger) Snapshot() (Snapshot, error) {
	state, err := LoadState(l.cityPath)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Held:      state.Held,
		Consumed:  state.Consumed,
		ByAgent:   map[string]int{},
		ByRig:     map[string]int{},
		ByAccount: map[string]int{},
	}
	forEachLive(&state, func(r Reservation) {
		snap.Total++
		snap.ByAgent[r.Agent]++
		if r.Rig != "" {
			snap.ByRig[r.Rig]++
		}
		if r.Account != "" {
			snap.ByAccount[r.Account]++
		}
	})
	return snap, nil
}

// checkPlacement rejects a re-reservation that names a different placement
// than the workload's live reservation holds. A workload's placement is fixed
// for the reservation's life; moving it means releasing and reserving again.
func checkPlacement(r Reservation, req ReserveRequest) error {
	if r.Agent == req.Agent && r.Rig == req.Rig && r.Provider == req.Provider {
		return nil
	}
	return fmt.Errorf("%w: workload %q holds reservation %q on agent %q rig %q provider %q, not agent %q rig %q provider %q",
		ErrReservationMismatch, req.WorkloadID, r.ID, r.Agent, r.Rig, r.Provider, req.Agent, req.Rig, req.Provider)
}

// checkCaps returns the rejection for the first scope that refuses, or nil.
func checkCaps(s *State, req ReserveRequest) error {
	var agentN, rigN, totalN int
	forEachLive(s, func(r Reservation) {
		totalN++
		if r.Agent == req.Agent {
			agentN++
		}
		if req.Rig != "" && r.Rig == req.Rig {
			rigN++
		}
	})

	if limit, bounded := capLimit(req.Caps.Agent[req.Agent]); bounded && agentN >= limit {
		return fmt.Errorf("%w: agent %q at %d/%d", ErrAgentCapReached, req.Agent, agentN, limit)
	}
	if req.Rig != "" {
		if limit, bounded := capLimit(req.Caps.Rig[req.Rig]); bounded && rigN >= limit {
			return fmt.Errorf("%w: rig %q at %d/%d", ErrRigCapReached, req.Rig, rigN, limit)
		}
	}
	if limit, bounded := capLimit(req.Caps.Workspace); bounded && totalN >= limit {
		return fmt.Errorf("%w: workspace at %d/%d", ErrWorkspaceCapReached, totalN, limit)
	}
	return nil
}

// checkAccountCap enforces the per-account cap. It applies only to
// reservations that carry an account.
func checkAccountCap(s *State, account string, accountCap *int) error {
	if account == "" {
		return nil
	}
	limit, bounded := capLimit(accountCap)
	if !bounded {
		return nil
	}
	var n int
	forEachLive(s, func(r Reservation) {
		if r.Account == account {
			n++
		}
	})
	if n >= limit {
		return fmt.Errorf("%w: account %q at %d/%d", ErrAccountCapReached, account, n, limit)
	}
	return nil
}

// saturatedAccounts lists accounts already at their cap, sorted for a stable
// selector invocation.
func saturatedAccounts(s *State, accountCap *int) []string {
	limit, bounded := capLimit(accountCap)
	if !bounded {
		return nil
	}
	counts := map[string]int{}
	forEachLive(s, func(r Reservation) {
		if r.Account != "" {
			counts[r.Account]++
		}
	})
	var out []string
	for account, n := range counts {
		if n >= limit {
			out = append(out, account)
		}
	}
	sort.Strings(out)
	return out
}

// reclaimExpired drops held reservations past their expiry and returns them.
// Consumed reservations are never touched: their lifetime belongs to the
// binding's lease, on the recovery path, not to this TTL.
func reclaimExpired(s *State, now time.Time) []Reservation {
	var (
		kept      []Reservation
		reclaimed []Reservation
	)
	for _, r := range s.Held {
		if !r.ExpiresAt.IsZero() && now.After(r.ExpiresAt) {
			reclaimed = append(reclaimed, r)
			continue
		}
		kept = append(kept, r)
	}
	s.Held = kept
	return reclaimed
}

// capLimit normalizes a cap pointer. bounded=false means unlimited.
func capLimit(p *int) (limit int, bounded bool) {
	if p == nil || *p < 0 {
		return 0, false
	}
	return *p, true
}

// forEachLive visits every reservation occupying a unit, held or consumed.
func forEachLive(s *State, fn func(Reservation)) {
	for _, r := range s.Held {
		fn(r)
	}
	for _, r := range s.Consumed {
		fn(r)
	}
}

// findLive returns a copy of the workload's live reservation. It returns a
// copy rather than a pointer into the state so callers cannot mutate the
// pending transaction through it.
func findLive(s *State, workloadID string) (Reservation, bool) {
	for _, r := range s.Held {
		if r.WorkloadID == workloadID {
			return r, true
		}
	}
	for _, r := range s.Consumed {
		if r.WorkloadID == workloadID {
			return r, true
		}
	}
	return Reservation{}, false
}

func removeByID(rs []Reservation, id string) []Reservation {
	out := rs[:0]
	for _, r := range rs {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}
