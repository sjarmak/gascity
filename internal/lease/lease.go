// Package lease encodes and decodes the lease record that marks a unit of
// work as owned.
//
// The wire form is a single string field:
//
//	v1|<epoch>|<holder>|<expires_at>|<state>|<attempts>
//
// The package is a pure value codec. It holds no store handle, performs no
// I/O, and imports nothing from the domain packages, so any layer that needs
// to read ownership can depend on it without an import cycle.
//
// Four conditions are representable, not three. Beyond the held, parked and
// dead states, the *absence* of the record is the distinct unleased
// condition: nothing owns the work and no state has ever been written. Lease
// carries that distinction so absence can never be mistaken for a zero-valued
// state, and the zero Lease is the unleased condition rather than a held one.
package lease

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// State is the lifecycle position recorded in a lease. Exactly one of the
// three constants below is a legal value on the wire.
type State string

const (
	// StateHeld means the holder currently owns the work until ExpiresAt.
	StateHeld State = "held"
	// StateParked means the holder released the work without completing it.
	StateParked State = "parked"
	// StateDead means the lease was retired: the holder recorded in the record
	// no longer owns the work and the record is not a claim. This codec is
	// stateless and says nothing about what may follow a dead record;
	// whether a dead lease can be succeeded is a question for the transition
	// layer, which does not exist yet.
	StateDead State = "dead"
)

const (
	// version is the literal record tag, checked exactly as the first field.
	version = "v1"
	// fieldSep separates the six record fields.
	fieldSep = "|"
	// noExpiry is the literal that stands in for expires_at whenever the
	// state carries no deadline.
	noExpiry = "-"
	// fieldCount is the exact number of fields a record has.
	fieldCount = 6

	holderSessionSep  = "@"
	holderInstanceSep = "#"
)

// Holder identifies an incarnation of a seat, not a durable identity. A
// restarted seat reuses its agent and session names but takes a fresh
// InstanceToken, which makes it a different Holder that does not inherit the
// previous incarnation's ownership. Holder is comparable, so `==` is the
// ownership check.
type Holder struct {
	// Agent is the configured worker name.
	Agent string
	// Session is the seat the worker occupies.
	Session string
	// InstanceToken distinguishes one run of that seat from the next.
	InstanceToken string
}

// NewHolder builds a Holder from its three parts, rejecting empty parts and
// parts containing any separator used by the wire form. It returns the zero
// Holder and an error when the input cannot round-trip.
func NewHolder(agent, session, instanceToken string) (Holder, error) {
	for name, part := range map[string]string{
		"agent":          agent,
		"session":        session,
		"instance token": instanceToken,
	} {
		if part == "" {
			return Holder{}, fmt.Errorf("building holder: %s is empty", name)
		}
		if strings.ContainsAny(part, fieldSep+holderSessionSep+holderInstanceSep) {
			return Holder{}, fmt.Errorf("building holder: %s %q contains a separator", name, part)
		}
	}
	return Holder{Agent: agent, Session: session, InstanceToken: instanceToken}, nil
}

// String renders the holder as <agent>@<session>#<instance_token>.
func (h Holder) String() string {
	return h.Agent + holderSessionSep + h.Session + holderInstanceSep + h.InstanceToken
}

// parseHolder reads the holder field. It returns an error rather than a bool
// so the caller can report which part of the record was malformed.
func parseHolder(raw string) (Holder, error) {
	seat, instanceToken, ok := strings.Cut(raw, holderInstanceSep)
	if !ok {
		return Holder{}, fmt.Errorf("holder %q has no %q separator", raw, holderInstanceSep)
	}
	agent, session, ok := strings.Cut(seat, holderSessionSep)
	if !ok {
		return Holder{}, fmt.Errorf("holder %q has no %q separator", raw, holderSessionSep)
	}
	holder, err := NewHolder(agent, session, instanceToken)
	if err != nil {
		return Holder{}, fmt.Errorf("holder %q: %w", raw, err)
	}
	if holder.String() != raw {
		return Holder{}, fmt.Errorf("holder %q does not round-trip", raw)
	}
	return holder, nil
}

// Record is a decoded lease. It is comparable, so two records are equal only
// when every field matches, including the holder's instance token.
//
// Comparing with == is only meaningful between canonical records. time.Time
// compares its wall clock, monotonic reading and location, so a record built
// from time.Now() or from a non-UTC zone is != the record it decodes back to,
// even though both encode to the identical wire string. Decode always yields
// canonical records; a caller-built record does not until it passes through
// Canonical. Comparing a caller-built record against a decoded one with ==
// silently reports "the lease changed" when nothing changed, which is the
// wrong answer for a fence check.
type Record struct {
	// Epoch is the fencing counter. Zero is a legal epoch: absence of the
	// record, not a zero epoch, is the unleased condition. Negative values
	// are not representable on the wire.
	Epoch int64
	// Holder is the incarnation that wrote the record.
	Holder Holder
	// ExpiresAt is the deadline, carried only by StateHeld. It is the zero
	// time for StateParked and StateDead, which encode as "-".
	ExpiresAt time.Time
	// State is the lifecycle position.
	State State
	// Attempts is the retry count, the only retry concept at this layer.
	// Zero is legal and means no attempt has been retried. Negative values
	// are not representable on the wire.
	Attempts int64
}

// Canonical returns the record with its expiry in the form Decode produces:
// UTC, with any monotonic reading stripped. It is the fixed point that makes
// == meaningful, so a caller comparing a record it built against one it
// decoded should canonicalize the built one first. The zero expiry carried by
// StateParked and StateDead is already canonical and is left alone.
func (r Record) Canonical() Record {
	if !r.ExpiresAt.IsZero() {
		r.ExpiresAt = r.ExpiresAt.UTC().Round(0)
	}
	return r
}

// Validate reports whether the record can be encoded: a known state, a
// nonnegative epoch and attempt count, a well-formed holder, and an expiry
// that is present exactly when the state is StateHeld.
func (r Record) Validate() error {
	switch r.State {
	case StateHeld, StateParked, StateDead:
	default:
		return fmt.Errorf("state %q is not one of %q, %q, %q", r.State, StateHeld, StateParked, StateDead)
	}
	if r.Epoch < 0 {
		return fmt.Errorf("epoch %d is negative", r.Epoch)
	}
	if r.Attempts < 0 {
		return fmt.Errorf("attempts %d is negative", r.Attempts)
	}
	if _, err := NewHolder(r.Holder.Agent, r.Holder.Session, r.Holder.InstanceToken); err != nil {
		return err
	}
	if r.State == StateHeld {
		if r.ExpiresAt.IsZero() {
			return fmt.Errorf("state %q carries no expiry", StateHeld)
		}
		if err := checkExpiryRoundTrips(r.ExpiresAt); err != nil {
			return err
		}
		return nil
	}
	if !r.ExpiresAt.IsZero() {
		return fmt.Errorf("state %q carries expiry %s, want none", r.State, formatExpiry(r.ExpiresAt))
	}
	return nil
}

// Encode renders a record in the wire form. It validates first and returns
// the empty string with an error for any record that is not encodable, so a
// malformed record can never reach storage.
func Encode(r Record) (string, error) {
	if err := r.Validate(); err != nil {
		return "", fmt.Errorf("encoding lease: %w", err)
	}
	expiry := noExpiry
	if r.State == StateHeld {
		expiry = formatExpiry(r.ExpiresAt)
	}
	return strings.Join([]string{
		version,
		strconv.FormatInt(r.Epoch, 10),
		r.Holder.String(),
		expiry,
		string(r.State),
		strconv.FormatInt(r.Attempts, 10),
	}, fieldSep), nil
}

// Decode parses the wire form of a present lease record.
//
// It returns an error rather than the (T, bool) idiom used by the sibling
// metadata scalars. That idiom folds "this string is not ours" into the same
// signal as "there is nothing here", and this codec must never do that:
// absence is the unleased condition and is carried by Lease, so a malformed
// value has to stay loudly distinguishable from it. A false here would let a
// corrupt record be read as unowned work and handed to a second worker.
func Decode(s string) (Record, error) {
	rec, err := decode(s)
	if err != nil {
		return Record{}, fmt.Errorf("decoding lease %q: %w", s, err)
	}
	return rec, nil
}

func decode(s string) (Record, error) {
	parts := strings.Split(s, fieldSep)
	if len(parts) != fieldCount {
		return Record{}, fmt.Errorf("got %d fields, want %d", len(parts), fieldCount)
	}
	if parts[0] != version {
		return Record{}, fmt.Errorf("version tag %q, want %q", parts[0], version)
	}
	epoch, err := parseCount(parts[1])
	if err != nil {
		return Record{}, fmt.Errorf("epoch: %w", err)
	}
	holder, err := parseHolder(parts[2])
	if err != nil {
		return Record{}, err
	}
	state := State(parts[4])
	switch state {
	case StateHeld, StateParked, StateDead:
	default:
		return Record{}, fmt.Errorf("state %q is not one of %q, %q, %q", state, StateHeld, StateParked, StateDead)
	}
	expiresAt, err := parseExpiry(parts[3], state)
	if err != nil {
		return Record{}, err
	}
	attempts, err := parseCount(parts[5])
	if err != nil {
		return Record{}, fmt.Errorf("attempts: %w", err)
	}
	return Record{
		Epoch:     epoch,
		Holder:    holder,
		ExpiresAt: expiresAt,
		State:     state,
		Attempts:  attempts,
	}, nil
}

// parseCount reads a decimal count field, rejecting empty values, whitespace,
// non-decimal notation, negative values, values past math.MaxInt64, and every
// noncanonical spelling of a legal value ("+1", "007", "-0"). The signed range
// mirrors the durable representation these counts are stored alongside, which
// is a nonnegative int64.
//
// The strictness is a correctness requirement, not fussiness. This record lives
// in bead metadata and is updated through a compare-and-set primitive that
// compares metadata values as strings, so two byte-spellings of the same lease
// compare unequal: a CAS against "v1|00|..." silently fails against a stored
// canonical "v1|0|...", and the fence quietly stops fencing. Canonical bytes are
// what make the compare-and-set sound, so - like parseExpiry - this requires the
// input to re-encode to exactly itself.
func parseCount(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("field is empty")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal count: %w", raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%q is negative", raw)
	}
	if rendered := strconv.FormatInt(n, 10); rendered != raw {
		return 0, fmt.Errorf("count %q is not in the encoded form, which is %q", raw, rendered)
	}
	return n, nil
}

// parseExpiry reads the expires_at field. Only StateHeld carries a real time;
// the other states must carry the literal "-".
func parseExpiry(raw string, state State) (time.Time, error) {
	if state != StateHeld {
		if raw != noExpiry {
			return time.Time{}, fmt.Errorf("state %q carries expiry %q, want %q", state, raw, noExpiry)
		}
		return time.Time{}, nil
	}
	if raw == noExpiry {
		return time.Time{}, fmt.Errorf("state %q carries %q, want a timestamp", StateHeld, noExpiry)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("expiry %q is not RFC3339: %w", raw, err)
	}
	if parsed.IsZero() {
		return time.Time{}, fmt.Errorf("expiry %q is the zero time", raw)
	}
	// time.Parse is more permissive than the encoded form: it accepts a comma
	// as the fractional separator and silently truncates fractions beyond
	// nanosecond precision. Both would decode to a record that re-encodes to a
	// different string, so require the input to be exactly what Encode writes.
	utc := parsed.UTC()
	if rendered := formatExpiry(utc); rendered != raw {
		return time.Time{}, fmt.Errorf("expiry %q is not in the encoded form, which is %q", raw, rendered)
	}
	return utc, nil
}

// formatExpiry renders a timestamp in UTC so it parses back to the same
// instant.
func formatExpiry(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339Nano)
}

// checkExpiryRoundTrips rejects timestamps that RFC3339Nano cannot represent,
// such as years outside the four-digit range.
func checkExpiryRoundTrips(ts time.Time) error {
	rendered := formatExpiry(ts)
	parsed, err := time.Parse(time.RFC3339Nano, rendered)
	if err != nil || !parsed.Equal(ts) {
		return fmt.Errorf("expiry %v is not representable as RFC3339", ts)
	}
	return nil
}

// Lease is a lease record or the absence of one. The zero Lease is the
// unleased condition: no record has been written and nothing owns the work.
// This is a fourth condition alongside the three states, and it is the reason
// the codec does not decode absence into a zero-valued Record.
type Lease struct {
	present bool
	record  Record
}

// Unleased returns the absent lease: no record, nothing owns the work.
func Unleased() Lease {
	return Lease{}
}

// opaque is the fail-closed lease returned alongside every error. It is
// present, so IsUnleased reports false, and it carries the zero Record, whose
// holder can never equal a real holder because NewHolder rejects empty parts.
//
// The direction matters more than the value. A caller that ignores or defers
// the error must not be able to read an error result as "nothing owns this":
// that turns one malformed byte in the store into two workers claiming one
// bead, which is the failure this package exists to prevent. Reading it as
// "held by someone I am not" costs a stalled claim and a loud downstream
// mismatch instead, and both are recoverable.
func opaque() Lease {
	return Lease{present: true}
}

// Present wraps a record as a present lease. It validates the record and
// returns the fail-closed lease with an error when the record is not
// encodable, so an invalid record cannot masquerade as ownership OR as
// absence.
func Present(r Record) (Lease, error) {
	if err := r.Validate(); err != nil {
		return opaque(), fmt.Errorf("building lease: %w", err)
	}
	return Lease{present: true, record: r}, nil
}

// IsUnleased reports whether the record was absent.
func (l Lease) IsUnleased() bool {
	return !l.present
}

// Record returns the decoded record and true when a lease is present, and the
// zero Record and false when the lease is unleased. Callers must check the
// bool: the zero Record is not a meaningful lease.
func (l Lease) Record() (Record, bool) {
	if !l.present {
		return Record{}, false
	}
	return l.record, true
}

// DecodeLease turns a raw value plus its presence flag into a Lease. Take both
// results of the map index expression first, then pass them in:
//
//	raw, ok := meta[key]
//	l, err := DecodeLease(raw, ok)
//
// An absent value is the unleased condition; a present value is decoded, and a
// present empty string is malformed rather than absent.
//
// A decode failure returns the fail-closed lease, never the unleased one. Only
// a genuinely absent key decodes to unleased.
func DecodeLease(raw string, present bool) (Lease, error) {
	if !present {
		return Unleased(), nil
	}
	rec, err := Decode(raw)
	if err != nil {
		return opaque(), err
	}
	return Lease{present: true, record: rec}, nil
}

// Lookup reads the lease stored under key. A missing key (including a nil
// map) is the unleased condition; a present but malformed value is an error,
// never silently downgraded to unleased.
func Lookup(meta map[string]string, key string) (Lease, error) {
	raw, ok := meta[key]
	return DecodeLease(raw, ok)
}
