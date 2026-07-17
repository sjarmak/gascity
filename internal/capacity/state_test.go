package capacity

import (
	"errors"
	"os"
	"testing"
	"time"
)

func testTime() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) }

func TestWithState_PersistsAndReloads(t *testing.T) {
	city := t.TempDir()
	now := testTime()

	if err := WithState(city, func(s *State) error {
		s.Held = append(s.Held, Reservation{
			ID: "rsv-1", WorkloadID: "gc-1", Agent: "worker",
			CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		})
		return nil
	}); err != nil {
		t.Fatalf("WithState: %v", err)
	}

	got, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Held) != 1 {
		t.Fatalf("Held = %d, want 1", len(got.Held))
	}
	if got.Held[0].ID != "rsv-1" || got.Held[0].WorkloadID != "gc-1" {
		t.Fatalf("Held[0] = %+v, want rsv-1/gc-1", got.Held[0])
	}
	if !got.Held[0].ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", got.Held[0].ExpiresAt, now.Add(time.Minute))
	}
}

func TestLoadState_MissingFileIsEmpty(t *testing.T) {
	got, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Held) != 0 || len(got.Consumed) != 0 {
		t.Fatalf("state = %+v, want empty", got)
	}
}

// The rollback primitive: a callback error must leave the file untouched.
func TestWithState_ErrorAbortsWrite(t *testing.T) {
	city := t.TempDir()
	now := testTime()

	if err := WithState(city, func(s *State) error {
		s.Held = append(s.Held, Reservation{ID: "rsv-keep", WorkloadID: "gc-1", CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sentinel := errors.New("boom")
	err := WithState(city, func(s *State) error {
		s.Held = append(s.Held, Reservation{ID: "rsv-discard", WorkloadID: "gc-2", CreatedAt: now})
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}

	got, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Held) != 1 || got.Held[0].ID != "rsv-keep" {
		t.Fatalf("Held = %+v, want only rsv-keep (mutation must not persist)", got.Held)
	}
}

func TestLoadState_CorruptFileErrors(t *testing.T) {
	city := t.TempDir()
	if err := WithState(city, func(_ *State) error { return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(StatePath(city), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := LoadState(city); err == nil {
		t.Fatal("LoadState error = nil, want parse error (must not silently reset the ledger)")
	}
}

func TestSortState_DeterministicOrder(t *testing.T) {
	now := testTime()
	s := State{
		Held: []Reservation{
			{ID: "b", CreatedAt: now.Add(time.Second)},
			{ID: "a", CreatedAt: now},
			{ID: "c", CreatedAt: now},
		},
	}
	SortState(&s)
	// CreatedAt ascending, ID as tiebreak.
	want := []string{"a", "c", "b"}
	for i, id := range want {
		if s.Held[i].ID != id {
			t.Fatalf("Held[%d].ID = %q, want %q (order %+v)", i, s.Held[i].ID, id, s.Held)
		}
	}
}

func TestStatePath_UsesCityRuntimeRoot(t *testing.T) {
	// Sibling of the nudge queue's .gc/nudges, per citylayout.RuntimeRoot.
	if got := StatePath("/city"); got != "/city/.gc/capacity/state.json" {
		t.Fatalf("StatePath = %q", got)
	}
	if got := LockPath("/city"); got != "/city/.gc/capacity/state.lock" {
		t.Fatalf("LockPath = %q", got)
	}
}
