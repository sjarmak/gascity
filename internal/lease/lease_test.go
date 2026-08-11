package lease

import (
	"math"
	"strings"
	"testing"
	"time"
)

func mustHolder(t *testing.T, agent, session, token string) Holder {
	t.Helper()
	h, err := NewHolder(agent, session, token)
	if err != nil {
		t.Fatalf("NewHolder(%q, %q, %q): %v", agent, session, token, err)
	}
	return h
}

func TestRoundTripHeld(t *testing.T) {
	expires := time.Date(2026, 8, 11, 4, 5, 6, 123456789, time.UTC)
	rec := Record{
		Epoch:     7,
		Holder:    mustHolder(t, "worker-a", "seat-3", "inst-01"),
		ExpiresAt: expires,
		State:     StateHeld,
		Attempts:  2,
	}
	encoded, err := Encode(rec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "v1|7|worker-a@seat-3#inst-01|2026-08-11T04:05:06.123456789Z|held|2"
	if encoded != want {
		t.Fatalf("Encode = %q, want %q", encoded, want)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(%q): %v", encoded, err)
	}
	if got != rec {
		t.Fatalf("round trip = %+v, want %+v", got, rec)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
}

func TestRoundTripHeldNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("test-offset", 5*3600)
	rec := Record{
		Epoch:     1,
		Holder:    mustHolder(t, "worker-a", "seat-1", "inst-01"),
		ExpiresAt: time.Date(2026, 8, 11, 9, 0, 0, 0, zone),
		State:     StateHeld,
	}
	encoded, err := Encode(rec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(encoded, "2026-08-11T04:00:00Z") {
		t.Fatalf("Encode = %q, want a UTC timestamp", encoded)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(%q): %v", encoded, err)
	}
	if !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Fatalf("ExpiresAt = %v, want the same instant as %v", got.ExpiresAt, rec.ExpiresAt)
	}
	if got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("ExpiresAt location = %v, want UTC", got.ExpiresAt.Location())
	}
}

func TestRoundTripParkedAndDead(t *testing.T) {
	for _, tc := range []struct {
		state State
		want  string
	}{
		{StateParked, "v1|4|worker-b@seat-9#inst-77|-|parked|3"},
		{StateDead, "v1|4|worker-b@seat-9#inst-77|-|dead|3"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			rec := Record{
				Epoch:    4,
				Holder:   mustHolder(t, "worker-b", "seat-9", "inst-77"),
				State:    tc.state,
				Attempts: 3,
			}
			encoded, err := Encode(rec)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if encoded != tc.want {
				t.Fatalf("Encode = %q, want %q", encoded, tc.want)
			}
			got, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode(%q): %v", encoded, err)
			}
			if got != rec {
				t.Fatalf("round trip = %+v, want %+v", got, rec)
			}
			if !got.ExpiresAt.IsZero() {
				t.Fatalf("ExpiresAt = %v, want the zero time for %s", got.ExpiresAt, tc.state)
			}
		})
	}
}

func TestDecodeMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"empty string", ""},
		{"too few fields", "v1|1|worker-a@seat-1#inst-01|-|parked"},
		{"too many fields", "v1|1|worker-a@seat-1#inst-01|-|parked|1|extra"},
		{"trailing separator", "v1|1|worker-a@seat-1#inst-01|-|parked|1|"},
		{"wrong version tag", "v2|1|worker-a@seat-1#inst-01|-|parked|1"},
		{"empty version tag", "|1|worker-a@seat-1#inst-01|-|parked|1"},
		{"unknown state", "v1|1|worker-a@seat-1#inst-01|-|zombie|1"},
		{"empty state", "v1|1|worker-a@seat-1#inst-01|-||1"},
		{"uppercase state", "v1|1|worker-a@seat-1#inst-01|-|PARKED|1"},
		{"time on parked", "v1|1|worker-a@seat-1#inst-01|2026-08-11T04:05:06Z|parked|1"},
		{"time on dead", "v1|1|worker-a@seat-1#inst-01|2026-08-11T04:05:06Z|dead|1"},
		{"dash on held", "v1|1|worker-a@seat-1#inst-01|-|held|1"},
		{"empty expiry on held", "v1|1|worker-a@seat-1#inst-01||held|1"},
		{"empty expiry on parked", "v1|1|worker-a@seat-1#inst-01||parked|1"},
		{"unparseable expiry on held", "v1|1|worker-a@seat-1#inst-01|tomorrow|held|1"},
		{"non-RFC3339 expiry on held", "v1|1|worker-a@seat-1#inst-01|2026-08-11 04:05:06|held|1"},
		{"zero expiry on held", "v1|1|worker-a@seat-1#inst-01|0001-01-01T00:00:00Z|held|1"},
		{"non-numeric epoch", "v1|abc|worker-a@seat-1#inst-01|-|parked|1"},
		{"empty epoch", "v1||worker-a@seat-1#inst-01|-|parked|1"},
		{"negative epoch", "v1|-3|worker-a@seat-1#inst-01|-|parked|1"},
		{"negative one epoch", "v1|-1|worker-a@seat-1#inst-01|-|parked|1"},
		{"epoch past the signed maximum", "v1|9223372036854775808|worker-a@seat-1#inst-01|-|parked|1"},
		{"epoch past the unsigned maximum", "v1|18446744073709551616|worker-a@seat-1#inst-01|-|parked|1"},
		{"epoch with plus sign", "v1|+3|worker-a@seat-1#inst-01|-|parked|1"},
		{"epoch with whitespace", "v1| 3|worker-a@seat-1#inst-01|-|parked|1"},
		{"epoch with leading zero", "v1|00|worker-a@seat-1#inst-01|-|parked|0"},
		{"epoch with leading zeroes", "v1|007|worker-a@seat-1#inst-01|-|parked|0"},
		{"negative zero epoch", "v1|-0|worker-a@seat-1#inst-01|-|parked|0"},
		{"attempts with leading zero", "v1|1|worker-a@seat-1#inst-01|-|parked|00"},
		{"attempts with leading zeroes", "v1|1|worker-a@seat-1#inst-01|-|parked|007"},
		{"negative zero attempts", "v1|1|worker-a@seat-1#inst-01|-|parked|-0"},
		{"attempts with plus sign", "v1|1|worker-a@seat-1#inst-01|-|parked|+3"},
		{"hex epoch", "v1|0x10|worker-a@seat-1#inst-01|-|parked|1"},
		{"non-numeric attempts", "v1|1|worker-a@seat-1#inst-01|-|parked|many"},
		{"empty attempts", "v1|1|worker-a@seat-1#inst-01|-|parked|"},
		{"negative attempts", "v1|1|worker-a@seat-1#inst-01|-|parked|-1"},
		{"negative three attempts", "v1|1|worker-a@seat-1#inst-01|-|parked|-3"},
		{"attempts past the signed maximum", "v1|1|worker-a@seat-1#inst-01|-|parked|9223372036854775808"},
		{"attempts past the unsigned maximum", "v1|1|worker-a@seat-1#inst-01|-|parked|18446744073709551616"},
		{"comma fraction on held", "v1|1|worker-a@seat-1#inst-01|2026-08-11T04:05:06,5Z|held|0"},
		{"sub-nanosecond fraction on held", "v1|1|worker-a@seat-1#inst-01|2026-08-11T04:05:06.1234567899Z|held|0"},
		{"empty holder", "v1|1||-|parked|1"},
		{"holder without instance token", "v1|1|worker-a@seat-1|-|parked|1"},
		{"holder without session", "v1|1|worker-a#inst-01|-|parked|1"},
		{"holder with empty agent", "v1|1|@seat-1#inst-01|-|parked|1"},
		{"holder with empty session", "v1|1|worker-a@#inst-01|-|parked|1"},
		{"holder with empty instance token", "v1|1|worker-a@seat-1#|-|parked|1"},
		{"holder with extra at sign", "v1|1|worker-a@seat@1#inst-01|-|parked|1"},
		{"holder with extra hash", "v1|1|worker-a@seat-1#inst#01|-|parked|1"},
		{"holder separators swapped", "v1|1|worker-a#seat-1@inst-01|-|parked|1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.raw)
			if err == nil {
				t.Fatalf("Decode(%q) = %+v, want an error", tc.raw, got)
			}
			if !strings.Contains(err.Error(), "decoding lease") {
				t.Fatalf("Decode(%q) error = %v, want it to name the operation", tc.raw, err)
			}
			if got != (Record{}) {
				t.Fatalf("Decode(%q) = %+v, want the zero Record alongside the error", tc.raw, got)
			}
		})
	}
}

// Every string Decode accepts must re-encode to itself. The record is compared
// as a string by the compare-and-set that fences claims, so an accepted alias
// spelling would make a CAS fail against the canonical bytes of the same lease.
func TestDecodedFormsAreCanonical(t *testing.T) {
	for _, raw := range []string{
		"v1|0|worker-a@seat-1#inst-01|-|parked|0",
		"v1|1|worker-a@seat-1#inst-01|-|parked|1",
		"v1|7|worker-a@seat-1#inst-01|-|dead|3",
		"v1|9223372036854775807|worker-a@seat-1#inst-01|-|parked|9223372036854775807",
		"v1|2|worker-a@seat-1#inst-01|2026-08-11T04:05:06Z|held|1",
		"v1|2|worker-a@seat-1#inst-01|2026-08-11T04:05:06.5Z|held|1",
	} {
		t.Run(raw, func(t *testing.T) {
			rec, err := Decode(raw)
			if err != nil {
				t.Fatalf("Decode(%q): %v", raw, err)
			}
			got, err := Encode(rec)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if got != raw {
				t.Fatalf("Encode(Decode(%q)) = %q, want the input unchanged", raw, got)
			}
		})
	}
}

// The counts are nonnegative int64, mirroring the durable claim-fence
// representation, so the maximum is math.MaxInt64 and one past it is rejected.
func TestCountOverflowBoundary(t *testing.T) {
	maxRec := Record{
		Epoch:    math.MaxInt64,
		Holder:   mustHolder(t, "worker-a", "seat-1", "inst-01"),
		State:    StateParked,
		Attempts: math.MaxInt64,
	}
	encoded, err := Encode(maxRec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := "v1|9223372036854775807|worker-a@seat-1#inst-01|-|parked|9223372036854775807"; encoded != want {
		t.Fatalf("Encode = %q, want %q", encoded, want)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(%q): %v", encoded, err)
	}
	if got != maxRec {
		t.Fatalf("round trip = %+v, want %+v", got, maxRec)
	}
	if _, err := Decode("v1|9223372036854775808|worker-a@seat-1#inst-01|-|parked|1"); err == nil {
		t.Fatal("Decode of an epoch one past math.MaxInt64 succeeded, want an error")
	}
	if _, err := Decode("v1|1|worker-a@seat-1#inst-01|-|parked|9223372036854775808"); err == nil {
		t.Fatal("Decode of an attempt count one past math.MaxInt64 succeeded, want an error")
	}
}

// Epoch zero is a legal wire value. Absence of the record, not a zero epoch,
// is the unleased condition, and the durable claim fence this epoch tracks
// reads 0 until a fence has been emitted.
func TestEpochZeroIsLegal(t *testing.T) {
	const raw = "v1|0|worker-a@seat-1#inst-01|-|parked|0"
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode(%q): %v", raw, err)
	}
	want := Record{
		Epoch:  0,
		Holder: mustHolder(t, "worker-a", "seat-1", "inst-01"),
		State:  StateParked,
	}
	if got != want {
		t.Fatalf("Decode(%q) = %+v, want %+v", raw, got, want)
	}
	encoded, err := Encode(got)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded != raw {
		t.Fatalf("Encode = %q, want %q", encoded, raw)
	}

	positive := Record{
		Epoch:    1,
		Holder:   mustHolder(t, "worker-a", "seat-1", "inst-01"),
		State:    StateParked,
		Attempts: 0,
	}
	encodedPositive, err := Encode(positive)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := "v1|1|worker-a@seat-1#inst-01|-|parked|0"; encodedPositive != want {
		t.Fatalf("Encode = %q, want %q", encodedPositive, want)
	}
	decodedPositive, err := Decode(encodedPositive)
	if err != nil {
		t.Fatalf("Decode(%q): %v", encodedPositive, err)
	}
	if decodedPositive != positive {
		t.Fatalf("round trip = %+v, want %+v", decodedPositive, positive)
	}
}

// A negative count is unrepresentable on the wire, so Encode must refuse it
// rather than emit a string Decode would reject.
func TestEncodeRejectsNegativeCounts(t *testing.T) {
	holder := mustHolder(t, "worker-a", "seat-1", "inst-01")
	for _, tc := range []struct {
		name string
		rec  Record
	}{
		{"negative epoch", Record{Epoch: -1, Holder: holder, State: StateParked}},
		{"negative attempts", Record{Epoch: 1, Holder: holder, State: StateParked, Attempts: -1}},
		{"most negative epoch", Record{Epoch: math.MinInt64, Holder: holder, State: StateParked}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.rec)
			if err == nil {
				t.Fatalf("Encode(%+v) = %q, want an error", tc.rec, got)
			}
			if got != "" {
				t.Fatalf("Encode(%+v) = %q, want the empty string alongside the error", tc.rec, got)
			}
		})
	}
}

// A time from time.Now carries a monotonic reading. The wire form has no place
// for one, so the decoded value must be a plain UTC wall-clock instant that
// still compares equal to the original.
func TestRoundTripStripsMonotonicClock(t *testing.T) {
	now := time.Now()
	if now.String() == now.Round(0).String() {
		t.Skip("time.Now did not carry a monotonic reading on this platform")
	}
	rec := Record{
		Epoch:     1,
		Holder:    mustHolder(t, "worker-a", "seat-1", "inst-01"),
		ExpiresAt: now,
		State:     StateHeld,
	}
	encoded, err := Encode(rec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(encoded, "m=") {
		t.Fatalf("Encode = %q, want no monotonic reading", encoded)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(%q): %v", encoded, err)
	}
	if got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("ExpiresAt location = %v, want UTC", got.ExpiresAt.Location())
	}
	if got.ExpiresAt.String() != got.ExpiresAt.Round(0).String() {
		t.Fatalf("ExpiresAt = %v still carries a monotonic reading", got.ExpiresAt)
	}
	if !got.ExpiresAt.Equal(now) {
		t.Fatalf("ExpiresAt = %v, want the same instant as %v", got.ExpiresAt, now)
	}
}

func TestEncodeRejectsInvalidRecords(t *testing.T) {
	holder := mustHolder(t, "worker-a", "seat-1", "inst-01")
	stamp := time.Date(2026, 8, 11, 4, 5, 6, 0, time.UTC)
	for _, tc := range []struct {
		name string
		rec  Record
	}{
		{"unknown state", Record{Epoch: 1, Holder: holder, State: State("zombie")}},
		{"empty state", Record{Epoch: 1, Holder: holder}},
		{"zero holder", Record{Epoch: 1, State: StateParked}},
		{"held without expiry", Record{Epoch: 1, Holder: holder, State: StateHeld}},
		{"parked with expiry", Record{Epoch: 1, Holder: holder, ExpiresAt: stamp, State: StateParked}},
		{"dead with expiry", Record{Epoch: 1, Holder: holder, ExpiresAt: stamp, State: StateDead}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.rec)
			if err == nil {
				t.Fatalf("Encode(%+v) = %q, want an error", tc.rec, got)
			}
			if got != "" {
				t.Fatalf("Encode(%+v) = %q, want the empty string alongside the error", tc.rec, got)
			}
		})
	}
}

func TestNewHolderRejectsMalformedParts(t *testing.T) {
	for _, tc := range []struct{ name, agent, session, token string }{
		{"empty agent", "", "seat-1", "inst-01"},
		{"empty session", "worker-a", "", "inst-01"},
		{"empty token", "worker-a", "seat-1", ""},
		{"agent carries the field separator", "work|er", "seat-1", "inst-01"},
		{"session carries the holder separator", "worker-a", "seat@1", "inst-01"},
		{"token carries the holder separator", "worker-a", "seat-1", "inst#01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHolder(tc.agent, tc.session, tc.token)
			if err == nil {
				t.Fatalf("NewHolder(%q, %q, %q) = %+v, want an error", tc.agent, tc.session, tc.token, h)
			}
			if h != (Holder{}) {
				t.Fatalf("NewHolder = %+v, want the zero Holder alongside the error", h)
			}
		})
	}
}

// A holder is an incarnation, not an identity: a restarted seat at the same
// agent and session name is a different holder and must not be treated as the
// lease owner.
func TestHolderInequalityOnInstanceTokenAlone(t *testing.T) {
	first := mustHolder(t, "worker-a", "seat-1", "inst-01")
	second := mustHolder(t, "worker-a", "seat-1", "inst-02")
	if first == second {
		t.Fatal("holders differing only in instance token compared equal")
	}
	if first.Agent != second.Agent || first.Session != second.Session {
		t.Fatal("fixtures must differ only in the instance token")
	}

	expires := time.Date(2026, 8, 11, 4, 5, 6, 0, time.UTC)
	encodeHeld := func(h Holder) Record {
		rec := Record{Epoch: 1, Holder: h, ExpiresAt: expires, State: StateHeld}
		encoded, err := Encode(rec)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode(%q): %v", encoded, err)
		}
		return decoded
	}
	a, b := encodeHeld(first), encodeHeld(second)
	if a.Holder == b.Holder {
		t.Fatal("decoded holders differing only in instance token compared equal")
	}
	if a == b {
		t.Fatal("decoded records differing only in instance token compared equal")
	}
	if a.Holder.String() == b.Holder.String() {
		t.Fatal("holder strings differing only in instance token collided")
	}
}

func TestUnleasedIsDistinctFromEveryState(t *testing.T) {
	unleased := Unleased()
	if !unleased.IsUnleased() {
		t.Fatal("Unleased().IsUnleased() = false")
	}
	if rec, ok := unleased.Record(); ok {
		t.Fatalf("Unleased().Record() = %+v, true; want the absent form", rec)
	}
	for _, state := range []State{StateHeld, StateParked, StateDead} {
		rec := Record{Epoch: 1, Holder: mustHolder(t, "worker-a", "seat-1", "inst-01"), State: state}
		if state == StateHeld {
			rec.ExpiresAt = time.Date(2026, 8, 11, 4, 5, 6, 0, time.UTC)
		}
		leased, err := Present(rec)
		if err != nil {
			t.Fatalf("Present(%+v): %v", rec, err)
		}
		if leased.IsUnleased() {
			t.Fatalf("Present(%+v).IsUnleased() = true", rec)
		}
		got, ok := leased.Record()
		if !ok || got != rec {
			t.Fatalf("Present(%+v).Record() = %+v, %v", rec, got, ok)
		}
		if leased == unleased {
			t.Fatalf("a %s lease compared equal to the unleased condition", state)
		}
	}
}

// The zero Lease is the unleased condition, so a caller that forgets to
// populate it cannot accidentally claim ownership.
func TestZeroLeaseIsUnleased(t *testing.T) {
	var zero Lease
	if !zero.IsUnleased() {
		t.Fatal("the zero Lease is not unleased")
	}
	if _, ok := zero.Record(); ok {
		t.Fatal("the zero Lease yielded a record")
	}
	if zero != Unleased() {
		t.Fatal("the zero Lease differs from Unleased()")
	}
}

func TestLookupDistinguishesAbsentFromEmpty(t *testing.T) {
	const key = "lease"
	held := "v1|1|worker-a@seat-1#inst-01|2026-08-11T04:05:06Z|held|0"

	absent, err := Lookup(map[string]string{"other": held}, key)
	if err != nil {
		t.Fatalf("Lookup on an absent key: %v", err)
	}
	if !absent.IsUnleased() {
		t.Fatal("an absent key did not decode to the unleased condition")
	}

	empty, err := Lookup(map[string]string{key: ""}, key)
	if err == nil {
		t.Fatalf("Lookup on an empty value = %+v, want an error", empty)
	}
	if !empty.IsUnleased() {
		t.Fatal("a failed Lookup must not report a lease")
	}

	nilMap, err := Lookup(nil, key)
	if err != nil {
		t.Fatalf("Lookup on a nil map: %v", err)
	}
	if !nilMap.IsUnleased() {
		t.Fatal("a nil map did not decode to the unleased condition")
	}

	present, err := Lookup(map[string]string{key: held}, key)
	if err != nil {
		t.Fatalf("Lookup on a present key: %v", err)
	}
	rec, ok := present.Record()
	if !ok {
		t.Fatal("a present, well-formed value did not yield a record")
	}
	if rec.State != StateHeld {
		t.Fatalf("State = %q, want %q", rec.State, StateHeld)
	}
}

func TestDecodeLeaseCarriesPresence(t *testing.T) {
	got, err := DecodeLease("", false)
	if err != nil {
		t.Fatalf("DecodeLease(absent): %v", err)
	}
	if !got.IsUnleased() {
		t.Fatal("an absent value did not decode to the unleased condition")
	}

	if _, err := DecodeLease("", true); err == nil {
		t.Fatal("DecodeLease of a present empty string succeeded, want an error")
	}
}

func TestStateStringsAreStable(t *testing.T) {
	for state, want := range map[State]string{
		StateHeld:   "held",
		StateParked: "parked",
		StateDead:   "dead",
	} {
		if string(state) != want {
			t.Fatalf("state %v renders as %q, want %q", state, string(state), want)
		}
	}
}
