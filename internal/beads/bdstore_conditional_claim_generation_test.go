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
// answers the claim-generation CAS verb with reply.
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

// TestAdvanceClaimGenerationIfCurrentMintsFromAbsent pins the exact argv for
// the first-ever claim of a bead never claimed through this fence before: the
// empty string reads as generation 0, so the first mint is "1". This is the
// gc-3ohe47 case — gc-ue0tsw was claimed only through gc hook --claim, which
// never called this at all, leaving gc.claim_generation entirely absent.
func TestAdvanceClaimGenerationIfCurrentMintsFromAbsent(t *testing.T) {
	runner := &generationVerbRunner{}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "")
	if err != nil {
		t.Fatalf("AdvanceClaimGenerationIfCurrent: %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationAdvanced {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationAdvanced)
	}
	if next != "1" {
		t.Fatalf("next = %q, want %q", next, "1")
	}
	want := []string{"bd", "update", "bd-42", "--if-assignee", "worker-1", "--set-metadata", "gc.claim_generation=1"}
	calls := runner.generationVerbArgv()
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", calls)
	}
	if strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q\nwant  %q", calls[0], want)
	}
}

// TestAdvanceClaimGenerationIfCurrentAdvancesFromExisting proves the
// monotonic-counter contract: a bead already carrying a generation advances by
// exactly one, never resets and never jumps.
func TestAdvanceClaimGenerationIfCurrentAdvancesFromExisting(t *testing.T) {
	runner := &generationVerbRunner{}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-2", "7")
	if err != nil {
		t.Fatalf("AdvanceClaimGenerationIfCurrent: %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationAdvanced {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationAdvanced)
	}
	if next != "8" {
		t.Fatalf("next = %q, want %q", next, "8")
	}
	want := []string{"bd", "update", "bd-42", "--if-assignee", "worker-2", "--set-metadata", "gc.claim_generation=8"}
	calls := runner.generationVerbArgv()
	if len(calls) != 1 || strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %v\nwant  %q", calls, want)
	}
}

// TestAdvanceClaimGenerationIfCurrentReadsPreconditionMissFromTheExitCode is
// the fencing heart of the fix: a stale caller (the assignee moved between the
// hook claim and this call) is refused via bd's dedicated exit code, never a
// parse of prose, and nothing is written.
func TestAdvanceClaimGenerationIfCurrentReadsPreconditionMissFromTheExitCode(t *testing.T) {
	runner := &generationVerbRunner{reply: func(_ []string) ([]byte, error) {
		return []byte("Error updating bd-42: assignee mismatch"), exitErrorWithCode(t, 13)
	}}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "")
	if err != nil {
		t.Fatalf("a precondition miss must not be an error, got %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationStale {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationStale)
	}
	if next != "" {
		t.Fatalf("next = %q on a stale refusal, want empty: nothing may be fabricated", next)
	}
}

// TestAdvanceClaimGenerationIfCurrentRefusesAnUnparsableGeneration is the
// fail-closed guard on the read side: a present-but-corrupt generation value
// must refuse rather than guess a restart point.
func TestAdvanceClaimGenerationIfCurrentRefusesAnUnparsableGeneration(t *testing.T) {
	runner := &generationVerbRunner{reply: func(args []string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected call %v", args)
	}}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "not-a-number")
	if err == nil {
		t.Fatal("an unparsable current generation must error, not guess")
	}
	if outcome != "" || next != "" {
		t.Fatalf("outcome=%q next=%q on error, want both empty", outcome, next)
	}
	if len(runner.generationVerbArgv()) != 0 {
		t.Fatalf("an unparsable generation must never reach bd: %v", runner.argv())
	}
}

// TestAdvanceClaimGenerationIfCurrentRefusesNonPositiveOrCeilingGenerations
// covers nextClaimGeneration's other two fail-closed branches, alongside the
// non-decimal case above: a non-positive counter (corrupt/tampered metadata)
// and the int64 ceiling (would silently wrap on overflow if incremented).
// Both must error before ever reaching bd, the same as an unparsable string.
func TestAdvanceClaimGenerationIfCurrentRefusesNonPositiveOrCeilingGenerations(t *testing.T) {
	for _, from := range []string{"0", "-3", "9223372036854775807"} {
		t.Run(from, func(t *testing.T) {
			runner := &generationVerbRunner{reply: func(args []string) ([]byte, error) {
				return nil, fmt.Errorf("unexpected call %v", args)
			}}
			s := beads.NewBdStore("/city", runner.run)

			next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", from)
			if err == nil {
				t.Fatalf("generation %q must error, not silently advance", from)
			}
			if outcome != "" || next != "" {
				t.Fatalf("outcome=%q next=%q on error, want both empty", outcome, next)
			}
			if len(runner.generationVerbArgv()) != 0 {
				t.Fatalf("generation %q must never reach bd: %v", from, runner.argv())
			}
		})
	}
}

// TestAdvanceClaimGenerationIfCurrentTreatsAnUnsupportedFlagAsRefusal covers a
// bd build predating --if-assignee/--set-metadata: refused, not silently
// downgraded to an unfenced write.
func TestAdvanceClaimGenerationIfCurrentTreatsAnUnsupportedFlagAsRefusal(t *testing.T) {
	runner := &generationVerbRunner{reply: func(_ []string) ([]byte, error) {
		return []byte("Error: unknown flag: --if-assignee"), exitErrorWithCode(t, 1)
	}}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "")
	if err != nil {
		t.Fatalf("an unsupported bd must not be an error, got %v", err)
	}
	if outcome != beads.AdvanceClaimGenerationUnsupported {
		t.Fatalf("outcome = %q, want %q", outcome, beads.AdvanceClaimGenerationUnsupported)
	}
	if next != "" {
		t.Fatalf("next = %q on unsupported, want empty", next)
	}
}

// TestAdvanceClaimGenerationIfCurrentRefusesAFuzzyIDCollision mirrors the
// gcy-g4o guard ReleaseIfCurrent already carries: bd's resolver
// prefix/substring-matches an id with no exact hit, and the fence would
// otherwise be evaluated against the wrong bead entirely.
func TestAdvanceClaimGenerationIfCurrentRefusesAFuzzyIDCollision(t *testing.T) {
	runner := &generationVerbRunner{
		show:  func(string) ([]byte, error) { return []byte(`[{"id":"bd-42-wisp-7"}]`), nil },
		reply: func([]string) ([]byte, error) { return nil, nil },
	}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "")
	if err == nil {
		t.Fatal("an id collision must error, not silently advance the wrong bead")
	}
	if outcome != "" || next != "" {
		t.Fatalf("outcome=%q next=%q on a collision, want both empty", outcome, next)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("a collision must never reach bd: %v", mutations)
	}
}

// TestAdvanceClaimGenerationIfCurrentSurfacesInfraFailuresAsErrors guards the
// last branch: a failure that is neither the dedicated precondition-miss exit
// code nor an unsupported-flag message must surface as an error, never as a
// silent stale/unsupported verdict that could mask an infrastructure outage.
func TestAdvanceClaimGenerationIfCurrentSurfacesInfraFailuresAsErrors(t *testing.T) {
	runner := &generationVerbRunner{reply: func(_ []string) ([]byte, error) {
		return []byte("database connection refused"), exitErrorWithCode(t, 1)
	}}
	s := beads.NewBdStore("/city", runner.run)

	next, outcome, err := s.AdvanceClaimGenerationIfCurrent("bd-42", "worker-1", "")
	if err == nil {
		t.Fatal("an infra failure must surface as an error")
	}
	if outcome != "" || next != "" {
		t.Fatalf("outcome=%q next=%q on an infra error, want both empty", outcome, next)
	}
}
