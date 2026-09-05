package beads

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// TestSQLiteStoreClaimWithGenerationMintsAtomicallyOnFreshClaim is the
// gc-3ohe47 P1 regression: a fresh claim must return the newly minted
// beadmeta.ClaimGenerationMetadataKey in the SAME call that transitions
// ownership, with the store's canonical row agreeing immediately — no window
// in which ownership has moved but the generation has not.
func TestSQLiteStoreClaimWithGenerationMintsAtomicallyOnFreshClaim(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{Title: "claim target"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := strings.TrimSpace(bead.Metadata[beadmeta.ClaimGenerationMetadataKey]); got != "" {
		t.Fatalf("freshly created bead already carries gc.claim_generation=%q, want absent", got)
	}

	claimed, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err != nil || !ok {
		t.Fatalf("ClaimWithGeneration = (%+v, %q, %v, %v), want success", claimed, generation, ok, err)
	}
	if generation != "1" {
		t.Fatalf("minted generation = %q, want %q", generation, "1")
	}
	if got := strings.TrimSpace(claimed.Metadata[beadmeta.ClaimGenerationMetadataKey]); got != "1" {
		t.Fatalf("returned bead's gc.claim_generation = %q, want %q", got, "1")
	}
	if claimed.Assignee != "worker" || claimed.Status != "in_progress" {
		t.Fatalf("ownership transition = (assignee=%q status=%q), want (worker in_progress)", claimed.Assignee, claimed.Status)
	}

	stored, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get after ClaimWithGeneration: %v", err)
	}
	if got := strings.TrimSpace(stored.Metadata[beadmeta.ClaimGenerationMetadataKey]); got != "1" {
		t.Fatalf("canonical row's gc.claim_generation = %q, want %q — ownership and generation must commit in one transaction", got, "1")
	}
	if stored.Assignee != "worker" || stored.Status != "in_progress" {
		t.Fatalf("canonical row ownership = (assignee=%q status=%q), want (worker in_progress)", stored.Assignee, stored.Status)
	}
}

// TestSQLiteStoreClaimWithGenerationAdvancesFromExisting proves the minted
// value is always current-plus-one, read from the same row the ownership CAS
// just locked, never caller-supplied — so a preset generation is advanced
// monotonically, not reset or ignored.
func TestSQLiteStoreClaimWithGenerationAdvancesFromExisting(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{
		Title:    "claim target",
		Metadata: map[string]string{beadmeta.ClaimGenerationMetadataKey: "5"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err != nil || !ok {
		t.Fatalf("ClaimWithGeneration = (%+v, %q, %v, %v), want success", claimed, generation, ok, err)
	}
	if generation != "6" {
		t.Fatalf("minted generation = %q, want %q — must advance from the preset 5", generation, "6")
	}
	if got := strings.TrimSpace(claimed.Metadata[beadmeta.ClaimGenerationMetadataKey]); got != "6" {
		t.Fatalf("returned bead's gc.claim_generation = %q, want %q", got, "6")
	}
}

func TestSQLiteStoreClaimWithGenerationRefusesFifteenDigitCeiling(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{
		Title:    "claim target",
		Metadata: map[string]string{beadmeta.ClaimGenerationMetadataKey: "999999999999999"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err == nil || ok || generation != "" || claimed.ID != "" {
		t.Fatalf("ClaimWithGeneration at 15-digit ceiling = (%+v, %q, %v, %v), want fail-closed", claimed, generation, ok, err)
	}
	stored, getErr := store.Get(bead.ID)
	if getErr != nil {
		t.Fatalf("Get after refusal: %v", getErr)
	}
	if stored.Assignee != "" || stored.Status != "open" || stored.Metadata[beadmeta.ClaimGenerationMetadataKey] != "999999999999999" {
		t.Fatalf("refused ceiling claim mutated row: %+v", stored)
	}
}

// TestSQLiteStoreClaimWithGenerationSameOwnerIsNoop matches Claim's own
// no-op contract (TestSQLiteStoreClaimSameOwnerIsNoopAndReturnsCurrentRevision):
// a same-owner reclaim of an already in_progress bead consumes neither a
// revision, a claim fence, nor a claim generation. Minting on every reclaim
// would let an idle holder silently invalidate its own outstanding fence.
func TestSQLiteStoreClaimWithGenerationSameOwnerIsNoop(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{Title: "claim target"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	first, firstGeneration, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err != nil || !ok {
		t.Fatalf("first ClaimWithGeneration = (%+v, %q, %v, %v), want success", first, firstGeneration, ok, err)
	}
	if firstGeneration != "1" {
		t.Fatalf("first minted generation = %q, want %q", firstGeneration, "1")
	}

	second, secondGeneration, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err != nil || !ok {
		t.Fatalf("same-owner ClaimWithGeneration = (%+v, %q, %v, %v), want success no-op", second, secondGeneration, ok, err)
	}
	if secondGeneration != firstGeneration {
		t.Fatalf("same-owner reclaim generation = %q, want unchanged %q — a no-op reclaim must not mint", secondGeneration, firstGeneration)
	}
	if second.Revision != first.Revision {
		t.Fatalf("same-owner reclaim revision = %d, want unchanged %d", second.Revision, first.Revision)
	}
}

func TestSQLiteStoreClaimWithGenerationRefusesNormalizedStoredAuthority(t *testing.T) {
	tests := []struct {
		name       string
		assignee   string
		generation string
	}{
		{name: "owner", assignee: " worker", generation: "7"},
		{name: "generation", assignee: "worker", generation: " 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newSQLiteGraphApplyStore(t, t.TempDir())
			bead, err := store.Create(Bead{
				Title:    "claim target",
				Status:   "in_progress",
				Assignee: tt.assignee,
				Metadata: map[string]string{beadmeta.ClaimGenerationMetadataKey: tt.generation},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			claimed, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
			if tt.name == "owner" {
				if err != nil || ok || generation != "" || claimed.ID != "" {
					t.Fatalf("whitespace-different stored owner = (claimed=%+v generation=%q ok=%v err=%v), want read-only conflict", claimed, generation, ok, err)
				}
				return
			}
			if err == nil || ok || generation != "" || claimed.ID != "" {
				t.Fatalf("whitespace-different stored generation = (claimed=%+v generation=%q ok=%v err=%v), want fail-closed", claimed, generation, ok, err)
			}
		})
	}
}

func TestSQLiteStoreClaimWithGenerationRefusesNormalizedGenerationBeforeMutation(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{
		Title:    "claim target",
		Metadata: map[string]string{beadmeta.ClaimGenerationMetadataKey: " 7"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker")
	if err == nil || ok || generation != "" || claimed.ID != "" {
		t.Fatalf("whitespace-different stored generation = (claimed=%+v generation=%q ok=%v err=%v), want fail-closed", claimed, generation, ok, err)
	}
	stored, getErr := store.Get(bead.ID)
	if getErr != nil {
		t.Fatalf("Get after refusal: %v", getErr)
	}
	if stored.Assignee != "" || stored.Status != "open" || stored.Metadata[beadmeta.ClaimGenerationMetadataKey] != " 7" {
		t.Fatalf("refused normalized generation mutated row: %+v", stored)
	}
}

// TestSQLiteStoreClaimWithGenerationConflictLeavesGenerationUntouched proves a
// lost race never fabricates or advances the generation: the conflicting
// caller gets ok=false with no generation, and the bead the winner holds is
// unaffected.
func TestSQLiteStoreClaimWithGenerationConflictLeavesGenerationUntouched(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{Title: "claim target"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker-a"); err != nil || !ok || generation != "1" {
		t.Fatalf("winning ClaimWithGeneration = (_, %q, %v, %v), want (1, true, nil)", generation, ok, err)
	}

	loser, generation, ok, err := store.ClaimWithGeneration(bead.ID, "worker-b")
	if err != nil || ok {
		t.Fatalf("conflicting ClaimWithGeneration = (%+v, %q, %v, %v), want a conflict (ok=false, no error)", loser, generation, ok, err)
	}
	if generation != "" {
		t.Fatalf("conflicting ClaimWithGeneration returned generation=%q, want empty — a lost race must never fabricate a token", generation)
	}

	stored, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get after conflict: %v", err)
	}
	if got := strings.TrimSpace(stored.Metadata[beadmeta.ClaimGenerationMetadataKey]); got != "1" {
		t.Fatalf("canonical gc.claim_generation after a lost race = %q, want unchanged %q", got, "1")
	}
	if stored.Assignee != "worker-a" {
		t.Fatalf("canonical assignee after a lost race = %q, want unchanged %q", stored.Assignee, "worker-a")
	}
}

// TestSQLiteStoreClaimNeverMintsAGeneration pins Claim (the pre-existing,
// generation-agnostic acquire path) as unaffected by ClaimWithGeneration's
// addition: a plain Claim must never touch beadmeta.ClaimGenerationMetadataKey,
// so every caller that still uses the two-argument Claim keeps its current
// behavior byte-for-byte.
func TestSQLiteStoreClaimNeverMintsAGeneration(t *testing.T) {
	store := newSQLiteGraphApplyStore(t, t.TempDir())
	bead, err := store.Create(Bead{Title: "claim target"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimed, ok, err := store.Claim(bead.ID, "worker")
	if err != nil || !ok {
		t.Fatalf("Claim = (%+v, %v, %v), want success", claimed, ok, err)
	}
	if got, present := claimed.Metadata[beadmeta.ClaimGenerationMetadataKey]; present && strings.TrimSpace(got) != "" {
		t.Fatalf("plain Claim wrote gc.claim_generation=%q, want it left untouched", got)
	}
	stored, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get after Claim: %v", err)
	}
	if got, present := stored.Metadata[beadmeta.ClaimGenerationMetadataKey]; present && strings.TrimSpace(got) != "" {
		t.Fatalf("canonical row after plain Claim carries gc.claim_generation=%q, want absent", got)
	}
}
