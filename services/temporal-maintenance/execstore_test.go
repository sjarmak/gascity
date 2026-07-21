package temporalmaintenance

import (
	"os"
	"sync"
	"testing"
)

func mut(key string) ProposedMutation {
	return ProposedMutation{IdempotencyKey: key, Action: "gh pr merge", Target: "1712"}
}

func TestKeyStore_ClaimIsOwnedOnce(t *testing.T) {
	s, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec, claimed, err := s.Claim(mut("k1"))
	if err != nil || !claimed {
		t.Fatalf("first Claim = (%+v, %v, %v), want claimed=true", rec, claimed, err)
	}
	if rec.Status != ExecPending {
		t.Fatalf("first claim status = %q, want pending", rec.Status)
	}
	_, claimed2, err := s.Claim(mut("k1"))
	if err != nil {
		t.Fatalf("second Claim err = %v", err)
	}
	if claimed2 {
		t.Fatalf("second Claim reported ownership; a key must be claimed at most once")
	}
}

func TestKeyStore_CompleteAndLoad(t *testing.T) {
	s, _ := NewKeyStore(t.TempDir())
	if _, _, err := s.Claim(mut("k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete("k1", "merged"); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Load("k1")
	if err != nil || !ok {
		t.Fatalf("Load = (%v, %v)", ok, err)
	}
	if rec.Status != ExecDone || rec.ResultRef != "merged" {
		t.Fatalf("record = %+v, want done/merged", rec)
	}
}

// A pending record (worker crashed mid-execution) must survive as pending and be
// re-claimable-as-owned by nobody: the safety direction is at-most-once.
func TestKeyStore_PendingIsNotReclaimed(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewKeyStore(dir)
	if _, claimed, _ := s.Claim(mut("k1")); !claimed {
		t.Fatal("expected first claim to own")
	}
	// simulate a crash: never Complete. A brand-new store over the same dir
	// (fresh worker) must still see the key as claimed.
	s2, _ := NewKeyStore(dir)
	rec, claimed, err := s2.Claim(mut("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatalf("a fresh store re-owned a pending key; at-most-once broken")
	}
	if rec.Status != ExecPending {
		t.Fatalf("status = %q, want pending", rec.Status)
	}
}

func TestKeyStore_ConcurrentClaimSingleOwner(t *testing.T) {
	s, _ := NewKeyStore(t.TempDir())
	const n = 32
	var wg sync.WaitGroup
	owners := make([]bool, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, claimed, err := s.Claim(mut("hot"))
			if err != nil {
				t.Errorf("claim %d err: %v", i, err)
			}
			owners[i] = claimed
		}(i)
	}
	wg.Wait()
	count := 0
	for _, o := range owners {
		if o {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exactly one goroutine must own the claim, got %d", count)
	}
}

// A present-but-unparseable record is real corruption (the atomic link/rename
// design makes half-writes impossible), so it must surface as an error rather
// than a fabricated pending record that hides the problem.
func TestKeyStore_CorruptRecordSurfaces(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewKeyStore(dir)
	if err := os.WriteFile(s.file("k1"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("k1"); err == nil {
		t.Fatalf("Load of corrupt record returned nil error")
	}
	if _, _, err := s.Claim(mut("k1")); err == nil {
		t.Fatalf("Claim over corrupt record returned nil error")
	}
}

// finish refuses to overwrite an already-terminal record: a double-Complete or a
// Fail-after-Complete is a caller bug, not a silent history rewrite.
func TestKeyStore_FinishRejectsNonPending(t *testing.T) {
	s, _ := NewKeyStore(t.TempDir())
	if _, _, err := s.Claim(mut("k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete("k1", "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete("k1", "again"); err == nil {
		t.Fatalf("second Complete on a done key returned nil error")
	}
	if err := s.Fail("k1", "", "boom"); err == nil {
		t.Fatalf("Fail on a done key returned nil error")
	}
}

// The real cross-process guarantee is the exclusive os.Link, not the in-process
// mutex. Race N independent stores (separate mutexes, simulating separate
// worker processes) over the same directory: exactly one may own the key.
func TestKeyStore_CrossProcessSingleOwner(t *testing.T) {
	dir := t.TempDir()
	const n = 24
	var wg sync.WaitGroup
	owners := make([]bool, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			s, err := NewKeyStore(dir) // independent store == independent process
			if err != nil {
				t.Errorf("store %d: %v", i, err)
				return
			}
			_, claimed, err := s.Claim(mut("hot"))
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
			owners[i] = claimed
		}(i)
	}
	wg.Wait()
	count := 0
	for _, o := range owners {
		if o {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exactly one independent store may own the key, got %d", count)
	}
}

func TestKeyStore_AllStableOrder(t *testing.T) {
	s, _ := NewKeyStore(t.TempDir())
	for _, k := range []string{"a", "b", "c"} {
		if _, _, err := s.Claim(mut(k)); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("All len = %d, want 3", len(recs))
	}
	// repeated calls are identically ordered
	recs2, _ := s.All()
	for i := range recs {
		if recs[i].Key != recs2[i].Key {
			t.Fatalf("All ordering not stable at %d: %q vs %q", i, recs[i].Key, recs2[i].Key)
		}
	}
}
