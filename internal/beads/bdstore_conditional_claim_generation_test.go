package beads_test

import (
	"encoding/json"
	"fmt"
	"strconv"
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
		return []byte(`[{"id":"` + id + `","status":"open","assignee":"worker-1","revision":11,"metadata":{"gc.claim_generation":"` + gen + `"}}]`), nil
	}
}

func exactShow(id, status, assignee, revision, generationJSON string, generationPresent bool) []byte {
	metadata := `{}`
	if generationPresent {
		metadata = `{"gc.claim_generation":` + generationJSON + `}`
	}
	return []byte(`[{"id":` + strconv.Quote(id) + `,"status":` + strconv.Quote(status) +
		`,"assignee":` + strconv.Quote(assignee) + `,"revision":` + revision + `,"metadata":` + metadata + `}]`)
}

func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	value, ok := findFlagValue(args, flag)
	if ok {
		return value
	}
	t.Fatalf("flag %s not found in argv: %v", flag, args)
	return ""
}

func findFlagValue(args []string, flag string) (string, bool) {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// generationFromSetMetadataArg extracts the minted value from a recorded
// `--set-metadata gc.claim_generation=<value>` pair, so a reply stub can echo
// back whatever value production code actually chose without hardcoding it.
func generationFromSetMetadataArg(t *testing.T, args []string) string {
	t.Helper()
	const prefix = "gc.claim_generation="
	for i, a := range args {
		if a == "--set-metadata" && i+1 < len(args) && strings.HasPrefix(args[i+1], prefix) {
			return strings.TrimPrefix(args[i+1], prefix)
		}
	}
	t.Fatalf("no --set-metadata gc.claim_generation=<value> in argv: %v", args)
	return ""
}

// TestClaimWithGenerationMintsInOneCall pins that the claim and the mint
// travel in ONE bd invocation carrying a valid positive decimal generation.
// This is the gc-3ohe47 HIGH #1 fix: a separate second write here would
// reopen the window the exact-head review flagged against a prior, two-write
// shape of this fix. It does not pin an exact minted value — see
// TestClaimWithGenerationDoesNotReuseAGenerationAcrossEpisodes for why
// pinning a value derived from a pre-claim read would itself reintroduce the
// round-3 ABA finding this file exists to close.
func TestClaimWithGenerationMintsInOneCall(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(id string) ([]byte, error) { return exactShow(id, "open", "", "11", "", false), nil },
		reply: func(args []string) ([]byte, error) {
			mint := generationFromSetMetadataArg(t, args)
			return []byte(`[{"id":"bd-42","assignee":"worker-1","metadata":{"gc.claim_generation":"` + mint + `"}}]`), nil
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err != nil {
		t.Fatalf("ClaimWithGeneration: %v", err)
	}
	if !ok {
		t.Fatal("ClaimWithGeneration reported no claim, want a win")
	}
	if claimed.ID != "bd-42" {
		t.Fatalf("claimed.ID = %q, want %q", claimed.ID, "bd-42")
	}
	n, err := strconv.ParseInt(next, 10, 64)
	if err != nil || n <= 0 {
		t.Fatalf("next = %q, want a positive decimal integer (err=%v)", next, err)
	}
	calls := runner.generationVerbArgv()
	if len(calls) != 1 {
		t.Fatalf("mutation calls = %v, want exactly one (claim and mint in the SAME bd invocation)", calls)
	}
	want := []string{"bd", "update", "bd-42", "--actor", "worker-1", "--if-version", "11", "--if-metadata-absent", "gc.claim_generation", "--claim", "--set-metadata", "gc.claim_generation=" + next, "--json"}
	if strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q\nwant  %q", calls[0], want)
	}
}

func TestClaimWithGenerationPreservesRawMetadataPredicate(t *testing.T) {
	tests := []struct {
		name              string
		generationJSON    string
		generationPresent bool
		revision          string
		wantGuardFlag     string
		wantGuardValue    string
		wantNext          string
	}{
		{name: "missing", generationPresent: false, revision: "23", wantGuardFlag: "--if-metadata-absent", wantGuardValue: "gc.claim_generation", wantNext: "1"},
		{name: "JSON string", generationJSON: `"7"`, generationPresent: true, revision: "23", wantGuardFlag: "--if-metadata", wantGuardValue: `gc.claim_generation="7"`, wantNext: "8"},
		{name: "JSON number and negative signed revision", generationJSON: `7`, generationPresent: true, revision: "-23", wantGuardFlag: "--if-metadata", wantGuardValue: `gc.claim_generation=7`, wantNext: "8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &generationVerbRunner{
				show: func(id string) ([]byte, error) {
					return exactShow(id, "open", "", tt.revision, tt.generationJSON, tt.generationPresent), nil
				},
				reply: func(_ []string) ([]byte, error) {
					return exactShow("bd-42", "in_progress", "worker-1", "24", strconv.Quote(tt.wantNext), true), nil
				},
			}
			s := beads.NewBdStore("/city", runner.run)

			_, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
			if err != nil || !ok || next != tt.wantNext {
				t.Fatalf("ClaimWithGeneration = (next=%q ok=%v err=%v), want (%q true nil)", next, ok, err, tt.wantNext)
			}
			mutation := runner.generationVerbArgv()
			if len(mutation) != 1 {
				t.Fatalf("mutation calls = %v, want one", mutation)
			}
			if got := flagValue(t, mutation[0], "--if-version"); got != tt.revision {
				t.Fatalf("--if-version = %q, want %s", got, tt.revision)
			}
			if got := flagValue(t, mutation[0], tt.wantGuardFlag); got != tt.wantGuardValue {
				t.Fatalf("%s = %q, want %q", tt.wantGuardFlag, got, tt.wantGuardValue)
			}
		})
	}
}

func TestClaimWithGenerationRefusesUnrepresentableSnapshot(t *testing.T) {
	tests := []struct {
		name string
		show []byte
	}{
		{name: "missing revision", show: []byte(`[{"id":"bd-42","status":"open","metadata":{}}]`)},
		{name: "null revision", show: []byte(`[{"id":"bd-42","status":"open","revision":null,"metadata":{}}]`)},
		{name: "present empty generation", show: exactShow("bd-42", "open", "", "4", `""`, true)},
		{name: "null generation", show: exactShow("bd-42", "open", "", "4", "null", true)},
		{name: "boolean generation", show: exactShow("bd-42", "open", "", "4", "true", true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &generationVerbRunner{show: func(string) ([]byte, error) { return tt.show, nil }}
			s := beads.NewBdStore("/city", runner.run)
			claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
			if err == nil || ok || next != "" || claimed.ID != "" {
				t.Fatalf("ClaimWithGeneration = (claimed=%+v next=%q ok=%v err=%v), want fail-closed", claimed, next, ok, err)
			}
			if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
				t.Fatalf("unrepresentable snapshot mutated destination: %v", mutations)
			}
		})
	}
}

func TestClaimWithGenerationSameOwnerReplayIsReadOnly(t *testing.T) {
	runner := &generationVerbRunner{show: func(id string) ([]byte, error) {
		return exactShow(id, "in_progress", "worker-1", "31", `"7"`, true), nil
	}}
	s := beads.NewBdStore("/city", runner.run)

	claimed, generation, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err != nil || !ok || generation != "7" || claimed.Assignee != "worker-1" {
		t.Fatalf("same-owner replay = (claimed=%+v generation=%q ok=%v err=%v)", claimed, generation, ok, err)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("same-owner replay mutated destination: %v", mutations)
	}
}

func TestClaimWithGenerationSameOwnerReplayAcceptsMaxGeneration(t *testing.T) {
	runner := &generationVerbRunner{show: func(id string) ([]byte, error) {
		return exactShow(id, "in_progress", "worker-1", "31", `"999999999999999"`, true), nil
	}}
	s := beads.NewBdStore("/city", runner.run)

	claimed, generation, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err != nil || !ok || generation != "999999999999999" || claimed.Assignee != "worker-1" {
		t.Fatalf("same-owner max replay = (claimed=%+v generation=%q ok=%v err=%v), want read-only success", claimed, generation, ok, err)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("same-owner max replay mutated destination: %v", mutations)
	}
}

func TestClaimWithGenerationRefusesNormalizedStoredOwner(t *testing.T) {
	runner := &generationVerbRunner{show: func(id string) ([]byte, error) {
		return exactShow(id, "in_progress", " worker-1", "31", `"7"`, true), nil
	}}
	s := beads.NewBdStore("/city", runner.run)

	claimed, generation, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err != nil || ok || generation != "" || claimed.Assignee != " worker-1" {
		t.Fatalf("whitespace-different stored owner = (claimed=%+v generation=%q ok=%v err=%v), want read-only conflict", claimed, generation, ok, err)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("whitespace-different stored owner mutated destination: %v", mutations)
	}
}

func TestClaimWithGenerationRefusesNormalizedMutationOwner(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(id string) ([]byte, error) {
			return exactShow(id, "open", "", "31", `"7"`, true), nil
		},
		reply: func([]string) ([]byte, error) {
			return exactShow("bd-42", "in_progress", " worker-1", "32", `"8"`, true), nil
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, generation, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err == nil || ok || generation != "" || claimed.ID != "" {
		t.Fatalf("whitespace-different mutation owner = (claimed=%+v generation=%q ok=%v err=%v), want fail-closed", claimed, generation, ok, err)
	}
}

// TestClaimWithGenerationRejectsAStaleSnapshotAfterAnotherClaimEpisode is the
// distributed ABA regression. A and B both read revision 7 / generation 7.
// B claims generation 8 and releases it before A submits its candidate. A's
// exact version+metadata predicates must reject that stale candidate; merely
// computing a locally different token would not prove destination fencing.
func TestClaimWithGenerationRejectsAStaleSnapshotAfterAnotherClaimEpisode(t *testing.T) {
	var (
		mu         sync.Mutex
		revision   int64 = 7
		owner            = ""
		status           = "open"
		generation       = json.RawMessage(`"7"`)
		accepted   []string
	)
	aRead := make(chan struct{})
	bDone := make(chan struct{})
	makeRunner := func(actor string, pauseAfterRead bool) beads.CommandRunner {
		return func(_ string, name string, args ...string) ([]byte, error) {
			if name != "bd" {
				return nil, fmt.Errorf("unexpected command %s", name)
			}
			if id, ok := showedID(args); ok {
				mu.Lock()
				out := exactShow(id, status, owner, strconv.FormatInt(revision, 10), string(generation), true)
				mu.Unlock()
				if pauseAfterRead {
					close(aRead)
					<-bDone
				}
				return out, nil
			}

			mu.Lock()
			defer mu.Unlock()
			expectedVersion, haveVersion := findFlagValue(args, "--if-version")
			expectedMetadata, haveMetadata := findFlagValue(args, "--if-metadata")
			wantMetadata := "gc.claim_generation=" + string(generation)
			// Legacy bd accepted the original unguarded --claim/--set-metadata
			// shape whenever the bead was unassigned. New guarded calls instead
			// require both predicates to match the exact current snapshot.
			guarded := haveVersion || haveMetadata
			if (guarded && (!haveVersion || !haveMetadata || expectedVersion != strconv.FormatInt(revision, 10) || expectedMetadata != wantMetadata)) || (!guarded && owner != "") {
				return []byte(`{"code":"precondition_failed","expected_revision":7,"current_revision":` + strconv.FormatInt(revision, 10) + `}`), fmt.Errorf("exit status 1")
			}
			setMetadata, haveSet := findFlagValue(args, "--set-metadata")
			if !haveSet || !strings.HasPrefix(setMetadata, "gc.claim_generation=") {
				return nil, fmt.Errorf("missing generation mutation in %v", args)
			}
			next := strings.TrimPrefix(setMetadata, "gc.claim_generation=")
			accepted = append(accepted, next)
			revision++
			generation = json.RawMessage(next)
			owner, status = actor, "in_progress"
			claimed := exactShow("bd-42", status, owner, strconv.FormatInt(revision, 10), string(generation), true)
			if actor == "worker-b" {
				// Model B's completed release before A's delayed mutation arrives.
				revision++
				owner, status = "", "open"
			}
			return claimed, nil
		}
	}
	type result struct {
		generation string
		ok         bool
		err        error
	}
	aResult := make(chan result, 1)
	go func() {
		_, generation, ok, err := beads.NewBdStore("/city", makeRunner("worker-a", true)).ClaimWithGeneration("bd-42", "worker-a")
		aResult <- result{generation: generation, ok: ok, err: err}
	}()
	<-aRead
	_, bGeneration, bOK, bErr := beads.NewBdStore("/city", makeRunner("worker-b", false)).ClaimWithGeneration("bd-42", "worker-b")
	close(bDone)
	a := <-aResult

	if bErr != nil || !bOK || bGeneration != "8" {
		t.Fatalf("B claim = (generation=%q ok=%v err=%v), want (8 true nil)", bGeneration, bOK, bErr)
	}
	if a.err != nil || a.ok || a.generation != "" {
		t.Fatalf("stale A claim = (generation=%q ok=%v err=%v), want refused without fabricated generation", a.generation, a.ok, a.err)
	}
	if len(accepted) != 1 || accepted[0] != "8" {
		t.Fatalf("destination accepted generations = %v, want only B's 8", accepted)
	}
}

// TestClaimWithGenerationReportsLostRaceWithoutWritingAnything is the atomic
// heart of the fix: when bd reports the claim itself lost the race, NOTHING
// was written — the metadata mint travels inside the same precondition as
// the ownership CAS, so a losing caller cannot have minted a competing
// generation against a claim it never won.
func TestClaimWithGenerationReportsLostRaceWithoutWritingAnything(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(id string) ([]byte, error) { return exactShow(id, "open", "", "11", "", false), nil },
		reply: func(_ []string) ([]byte, error) {
			return []byte("Error: bd-42 is already claimed by worker-2"), fmt.Errorf("exit status 1")
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
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

func TestClaimWithGenerationReportsTypedGuardMismatchAsLostRace(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(id string) ([]byte, error) {
			return exactShow(id, "open", "", "-3724874958137045744", `"7"`, true), nil
		},
		reply: func(_ []string) ([]byte, error) {
			return []byte(`{"error":"1 of 1 issues failed to update","failed":[{"id":"bd-42","error":"updating issue: version mismatch","guard_mismatch":true}],"schema_version":1}`), fmt.Errorf("exit status 13")
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err != nil || ok || next != "" || claimed.ID != "" {
		t.Fatalf("guard mismatch = (claimed=%+v next=%q ok=%v err=%v), want lost race", claimed, next, ok, err)
	}
}

// TestClaimWithGenerationRefusesAnUnsupportedBd covers a bd build predating
// --claim or --set-metadata: the whole claim is refused as an error rather
// than delivered without a fenced generation, which is exactly the
// gc-3ohe47 defect this file exists to close.
func TestClaimWithGenerationRefusesAnUnsupportedBd(t *testing.T) {
	runner := &generationVerbRunner{
		show: func(id string) ([]byte, error) { return exactShow(id, "open", "", "11", "", false), nil },
		reply: func(_ []string) ([]byte, error) {
			return []byte("Error: unknown flag: --set-metadata"), fmt.Errorf("exit status 1")
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
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

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
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

func TestClaimWithGenerationRefusesFifteenDigitCeiling(t *testing.T) {
	assertClaimWithGenerationRefusesFloor(t, "999999999999999")
}

func TestClaimWithGenerationRefusesInt64Ceiling(t *testing.T) {
	assertClaimWithGenerationRefusesFloor(t, "9223372036854775807")
}

func assertClaimWithGenerationRefusesFloor(t *testing.T, floor string) {
	t.Helper()
	runner := &generationVerbRunner{
		show: sequencedShow(t, floor),
		reply: func(args []string) ([]byte, error) {
			return nil, fmt.Errorf("unexpected mutation at unadvanceable floor: %v", args)
		},
	}
	s := beads.NewBdStore("/city", runner.run)

	claimed, next, ok, err := s.ClaimWithGeneration("bd-42", "worker-1")
	if err == nil {
		t.Fatalf("ClaimWithGeneration at floor %s must fail closed", floor)
	}
	if ok || next != "" || claimed.ID != "" {
		t.Fatalf("ClaimWithGeneration at floor %s = (claimed=%+v next=%q ok=%v), want empty refusal", floor, claimed, next, ok)
	}
	if mutations := runner.generationVerbArgv(); len(mutations) != 0 {
		t.Fatalf("ClaimWithGeneration at floor %s mutated destination: %v", floor, mutations)
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

func TestConfirmClaimGenerationRejectsWhitespaceDifferentAuthority(t *testing.T) {
	tests := []struct {
		name       string
		assignee   string
		generation string
	}{
		{name: "owner", assignee: " worker-1", generation: "1"},
		{name: "generation", assignee: "worker-1", generation: " 1 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &generationVerbRunner{show: func(id string) ([]byte, error) {
				return exactShow(id, "in_progress", tt.assignee, "7", strconv.Quote(tt.generation), true), nil
			}}
			s := beads.NewBdStore("/city", runner.run)
			outcome, err := s.ConfirmClaimGeneration("bd-42", "worker-1", "1")
			if err != nil || outcome != beads.AdvanceClaimGenerationStale {
				t.Fatalf("whitespace-different %s authority = (outcome=%q err=%v), want stale", tt.name, outcome, err)
			}
		})
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
