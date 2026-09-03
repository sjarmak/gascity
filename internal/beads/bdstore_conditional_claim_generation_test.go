package beads_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// generationVerbRunner records every bd invocation, answers the exact-ID
// preflight (`bd show --json <id>`) with the bead the caller named, and
// answers the claim-generation verb with reply.
type generationVerbRunner struct {
	mu    sync.Mutex
	calls [][]string
	reply func(args []string) ([]byte, error)
	show  func(id string) ([]byte, error)
}

func (r *generationVerbRunner) run(_, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	if id, ok := showedID(args); ok {
		if r.show != nil {
			return r.show(id)
		}
		return []byte(`[{"id":"` + id + `"}]`), nil
	}
	if r.reply != nil {
		return r.reply(args)
	}
	return nil, nil
}

func (r *generationVerbRunner) argv() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// generationVerbArgv returns the recorded calls with the exact-ID preflight
// reads dropped, so a cell can pin the mutation argv without restating the
// guard.
func (r *generationVerbRunner) generationVerbArgv() [][]string {
	var mutations [][]string
	for _, call := range r.argv() {
		if _, ok := showedID(call[1:]); ok {
			continue
		}
		mutations = append(mutations, call)
	}
	return mutations
}

// sequencedShow answers successive `bd show` calls with successive generation
// values: ClaimWithGeneration's pre-write read sees generations[0], and a
// later ConfirmClaimGeneration call sees generations[1], and so on. A test
// that wants to simulate the stored generation actually changing between
// those reads (a concurrent writer landing a different value) needs a stub
// that varies by call, not a fixed reply.
func sequencedShow(t *testing.T, generations ...string) func(id string) ([]byte, error) {
	t.Helper()
	var mu sync.Mutex
	i := 0
	return func(id string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(generations) {
			t.Fatalf("sequencedShow: more show calls (%d) than configured generations (%d)", i+1, len(generations))
		}
		gen := generations[i]
		i++
		return []byte(`[{"id":"` + id + `","assignee":"worker-1","metadata":{"gc.claim_generation":"` + gen + `"}}]`), nil
	}
}

// TestClaimWithGenerationMintsFromAbsentInOneCall pins the exact argv for the
// first-ever claim of a bead never claimed through this fence before — the
// empty string reads as generation 0, so the first mint is "1" — and pins
// that the claim and the mint travel in ONE bd invocation. This is the
// gc-3ohe47 HIGH fix: a separate second write here would reopen the window
// the exact-head review flagged against a prior, two-write shape of this fix.
func TestClaimWithGenerationMintsFromAbsentInOneCall(t *testing.T) {
	runner := &generationVerbRunner{
		show: sequencedShow(t, ""),
		reply: func(_ []string) ([]byte, error) {
			return []byte(`[{"id":"bd-42","assignee":"worker-1","metadata":{"gc.claim_generation":"1"}}]`), nil
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err != nil {
		t.Fatalf("ClaimWithGeneration: %v", err)
	}
	if !ok {
		t.Fatal("ClaimWithGeneration reported no claim, want a win")
	}
	if next != "1" {
		t.Fatalf("next = %q, want %q", next, "1")
	}
	if claimed.ID != "bd-42" {
		t.Fatalf("claimed.ID = %q, want %q", claimed.ID, "bd-42")
	}
	want := []string{"bd", "update", "bd-42", "--claim", "--set-metadata", "gc.claim_generation=1", "--json"}
	calls := runner.generationVerbArgv()
	if len(calls) != 1 {
		t.Fatalf("mutation calls = %v, want exactly one (claim and mint in the SAME bd invocation)", calls)
	}
	if strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q\nwant  %q", calls[0], want)
	}
}

// TestClaimWithGenerationAdvancesFromExisting proves the monotonic-counter
// contract survives the merge into one call: a bead already carrying a
// generation (from an earlier claim/release cycle) advances by exactly one.
func TestClaimWithGenerationAdvancesFromExisting(t *testing.T) {
	runner := &generationVerbRunner{
		show: sequencedShow(t, "7"),
		reply: func(_ []string) ([]byte, error) {
			return []byte(`[{"id":"bd-42","assignee":"worker-1","metadata":{"gc.claim_generation":"8"}}]`), nil
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	_, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err != nil {
		t.Fatalf("ClaimWithGeneration: %v", err)
	}
	if !ok {
		t.Fatal("ClaimWithGeneration reported no claim, want a win")
	}
	if next != "8" {
		t.Fatalf("next = %q, want %q", next, "8")
	}
	want := []string{"bd", "update", "bd-42", "--claim", "--set-metadata", "gc.claim_generation=8", "--json"}
	calls := runner.generationVerbArgv()
	if len(calls) != 1 || strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %v\nwant  %q", calls, want)
	}
}

// TestClaimWithGenerationReportsLostRaceWithoutWritingAnything is the atomic
// heart of the fix: when bd reports the claim itself lost the race, NOTHING
// was written — the metadata mint travels inside the same precondition as
// the ownership CAS, so a losing caller cannot have minted a competing
// generation against a claim it never won.
func TestClaimWithGenerationReportsLostRaceWithoutWritingAnything(t *testing.T) {
	runner := &generationVerbRunner{
		show: sequencedShow(t, ""),
		reply: func(_ []string) ([]byte, error) {
			return []byte("Error: bd-42 is already claimed by worker-2"), fmt.Errorf("exit status 1")
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err != nil {
		t.Fatalf("a lost claim race must not be an error, got %v", err)
	}
	if ok {
		t.Fatalf("ClaimWithGeneration reported a win on a lost race: %+v", claimed)
	}
	if next != "" {
		t.Fatalf("next = %q on a lost race, want empty: nothing may be fabricated", next)
	}
}

// TestClaimWithGenerationRefusesAnUnsupportedBd covers a bd build predating
// --claim or --set-metadata: the whole claim is refused as an error rather
// than delivered without a fenced generation, which is exactly the
// gc-3ohe47 defect this file exists to close.
func TestClaimWithGenerationRefusesAnUnsupportedBd(t *testing.T) {
	runner := &generationVerbRunner{
		show: sequencedShow(t, ""),
		reply: func(_ []string) ([]byte, error) {
			return []byte("Error: unknown flag: --set-metadata"), fmt.Errorf("exit status 1")
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err == nil {
		t.Fatal("an unsupported bd must refuse the claim as an error, not deliver an ungenerationed claim")
	}
	if ok || next != "" || claimed.ID != "" {
		t.Fatalf("claimed=%+v next=%q ok=%v on an unsupported bd, want all empty", claimed, next, ok)
	}
}

// TestClaimWithGenerationRefusesAFuzzyIDCollision mirrors the gcy-g4o guard
// ReleaseIfCurrent already carries: bd's resolver prefix/substring-matches an
// id with no exact hit, and the fence would otherwise be evaluated against —
// and mint a generation onto — the wrong bead entirely.
func TestClaimWithGenerationRefusesAFuzzyIDCollision(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(string) ([]byte, error) { return []byte(`[{"id":"bd-42-wisp-7"}]`), nil },
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err == nil {
		t.Fatal("an id collision must error, not silently claim the wrong bead")
	}
	if ok || next != "" || claimed.ID != "" {
		t.Fatalf("claimed=%+v next=%q ok=%v on a collision, want all empty", claimed, next, ok)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("a collision must never reach bd: %v", mutations)
	}
}

// TestClaimWithGenerationRefusesAnUnparsableGeneration is the fail-closed
// guard on the pre-read side: a present-but-corrupt generation must refuse
// rather than guess a restart point, and must never issue the claim.
func TestClaimWithGenerationRefusesAnUnparsableGeneration(t *testing.T) {
	runner := &generationVerbRunner{show: sequencedShow(t, "not-a-number")}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42")
	if err == nil {
		t.Fatal("an unparsable current generation must error, not guess")
	}
	if ok || next != "" || claimed.ID != "" {
		t.Fatalf("claimed=%+v next=%q ok=%v on error, want all empty", claimed, next, ok)
	}
	if len(runner.generationVerbArgv()) != 0 {
		t.Fatalf("an unparsable generation must never reach bd: %v", runner.argv())
	}
}

// TestConfirmClaimGenerationConfirmsExactMatch is the confirm-only path a
// caller uses after ClaimWithGeneration already minted the generation
// atomically: a pure read that finds the assignee and generation exactly as
// expected reports Advanced without writing anything.
func TestConfirmClaimGenerationConfirmsExactMatch(t *testing.T) {
	runner := &generationVerbRunner{show: sequencedShow(t, "1")}
	s := beads.NewBdStore("/city", runner.run)

	outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "1")
	if err != nil {
		t.Fatalf("ConfirmClaimGeneration: %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationAdvanced {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationAdvanced)
	}
	if len(runner.generationVerbArgv()) != 0 {
		t.Fatalf("ConfirmClaimGeneration must never write: %v", runner.argv())
	}
}

// TestConfirmClaimGenerationReportsStaleOnAssigneeMismatch discriminates the
// confirm path from a blind "generation matches" check: even an exact
// generation match must be refused if the current assignee has moved on,
// since a fencing token that outlived its owner is not current authority.
func TestConfirmClaimGenerationReportsStaleOnAssigneeMismatch(t *testing.T) {
	runner := &generationVerbRunner{show: func(id string) ([]byte, error) {
		return []byte(`[{"id":"` + id + `","assignee":"worker-2","metadata":{"gc.claim_generation":"1"}}]`), nil
	}}
	s := beads.NewBdStore("/city", runner.run)

	outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "1")
	if err != nil {
		t.Fatalf("an assignee mismatch must not be an error, got %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationStale {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationStale)
	}
}

// TestConfirmClaimGenerationReportsStaleOnGenerationMismatch is the other
// half: same assignee, but the stored generation no longer matches what the
// caller expects — a concurrent writer landed a different value.
func TestConfirmClaimGenerationReportsStaleOnGenerationMismatch(t *testing.T) {
	runner := &generationVerbRunner{show: sequencedShow(t, "40")}
	s := beads.NewBdStore("/city", runner.run)

	outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "7")
	if err != nil {
		t.Fatalf("a generation mismatch must not be an error, got %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationStale {
		t.Fatalf("outcome = %q, want %q — a stale generation must never confirm as advanced", outcome, beads.AdvanceClaimGenerationStale)
	}
}

// TestConfirmClaimGenerationRefusesAnEmptyExpectedGeneration guards the
// degenerate call shape: confirming "" would trivially match a bead that was
// never claimed through this fence at all, silently treating absence of a
// generation as a confirmed one.
func TestConfirmClaimGenerationRefusesAnEmptyExpectedGeneration(t *testing.T) {
	runner := &generationVerbRunner{reply: func(args []string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected call %v", args)
	}}
	s := beads.NewBdStore("/city", runner.run)

	outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "")
	if err != nil {
		t.Fatalf("an empty expected generation must not be an error, got %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationStale {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationStale)
	}
	if len(runner.argv()) != 0 {
		t.Fatalf("an empty expected generation must never reach bd: %v", runner.argv())
	}
}

// TestConfirmClaimGenerationRefusesAFuzzyIDCollision mirrors the same guard
// on the confirm side.
func TestConfirmClaimGenerationRefusesAFuzzyIDCollision(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(string) ([]byte, error) { return []byte(`[{"id":"bd-42-wisp-7"}]`), nil },
	}
	s := beads.NewBdStore("/city", runner.run)

	outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "1")
	if err == nil {
		t.Fatal("an id collision must error, not silently confirm against the wrong bead")
	}
	if outcome != "" {
		t.Fatalf("outcome = %q on a collision, want empty", outcome)
	}
}
