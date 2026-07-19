package binding

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testTime() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) }

func TestWithState_PersistsAndReloads(t *testing.T) {
	city := t.TempDir()
	now := testTime()

	if err := WithState(city, func(s *State) error {
		s.Bound = append(s.Bound, Binding{
			WorkloadID: "gc-1", Agent: "worker", ReservationRef: "rsv-1",
			Generation: 1, Attempt: 1, BoundAt: now,
		})
		return nil
	}); err != nil {
		t.Fatalf("WithState: %v", err)
	}

	got, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Bound) != 1 {
		t.Fatalf("Bound = %d, want 1", len(got.Bound))
	}
	if got.Bound[0].WorkloadID != "gc-1" || got.Bound[0].ReservationRef != "rsv-1" {
		t.Fatalf("Bound[0] = %+v, want gc-1/rsv-1", got.Bound[0])
	}
	if !got.Bound[0].BoundAt.Equal(now) {
		t.Fatalf("BoundAt = %v, want %v", got.Bound[0].BoundAt, now)
	}
}

func TestLoadState_MissingFileIsEmpty(t *testing.T) {
	got, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Bound) != 0 {
		t.Fatalf("state = %+v, want empty", got)
	}
}

// The rollback primitive: a callback error must leave the file untouched.
func TestWithState_ErrorAbortsWrite(t *testing.T) {
	city := t.TempDir()
	now := testTime()

	if err := WithState(city, func(s *State) error {
		s.Bound = append(s.Bound, Binding{WorkloadID: "gc-keep", Agent: "worker", ReservationRef: "rsv-keep", Generation: 1, Attempt: 1, BoundAt: now})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sentinel := errors.New("boom")
	err := WithState(city, func(s *State) error {
		s.Bound = append(s.Bound, Binding{WorkloadID: "gc-discard", Agent: "worker", ReservationRef: "rsv-discard", Generation: 1, Attempt: 1, BoundAt: now})
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}

	got, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Bound) != 1 || got.Bound[0].WorkloadID != "gc-keep" {
		t.Fatalf("Bound = %+v, want only gc-keep (mutation must not persist)", got.Bound)
	}
}

func TestWithState_RejectsInvalidMutationWithoutWrite(t *testing.T) {
	city := t.TempDir()
	if err := WithState(city, func(s *State) error {
		s.Bound = append(s.Bound, Binding{
			WorkloadID: "gc-keep", Agent: "worker", ReservationRef: "rsv-keep",
			Generation: 1, Attempt: 1, BoundAt: testTime(),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := WithState(city, func(s *State) error {
		s.Bound[0].Generation = 0
		return nil
	}); err == nil {
		t.Fatal("WithState error = nil, want post-callback validation error")
	}
	got, err := LoadState(city)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bound) != 1 || got.Bound[0].Generation != 1 {
		t.Fatalf("state = %+v, want original valid state", got)
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
		t.Fatal("LoadState error = nil, want parse error (must not silently unbind every workload)")
	}
}

func TestLoadState_RejectsContradictoryState(t *testing.T) {
	valid := func(workload, reservation string) Binding {
		return Binding{WorkloadID: workload, Agent: "worker", ReservationRef: reservation, Generation: 1, Attempt: 1, BoundAt: testTime()}
	}
	for _, tc := range []struct {
		name  string
		state State
	}{
		{"duplicate reservation refs", State{Bound: []Binding{valid("gc-1", "rsv-1"), valid("gc-2", "rsv-1")}}},
		{"duplicate workload in bucket", State{Bound: []Binding{valid("gc-1", "rsv-1"), valid("gc-1", "rsv-2")}}},
		{"duplicate workload across buckets", State{Pending: []Binding{valid("gc-1", "rsv-1")}, Bound: []Binding{valid("gc-1", "rsv-2")}}},
		{"missing required field", State{Bound: []Binding{{WorkloadID: "gc-1", Agent: "worker", Generation: 1, Attempt: 1, BoundAt: testTime()}}}},
		{"invalid generation", State{Bound: []Binding{{WorkloadID: "gc-1", Agent: "worker", ReservationRef: "rsv-1", Attempt: 1, BoundAt: testTime()}}}},
		{"invalid attempt", State{Bound: []Binding{{WorkloadID: "gc-1", Agent: "worker", ReservationRef: "rsv-1", Generation: 1, BoundAt: testTime()}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			city := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(StatePath(city)), 0o755); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(tc.state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(StatePath(city), data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadState(city); err == nil {
				t.Fatalf("LoadState(%s) error = nil, want fail-closed validation", data)
			}
		})
	}
}

func TestSortState_DeterministicOrder(t *testing.T) {
	now := testTime()
	s := State{
		Bound: []Binding{
			{WorkloadID: "b", BoundAt: now.Add(time.Second)},
			{WorkloadID: "a", BoundAt: now},
			{WorkloadID: "c", BoundAt: now},
		},
	}
	SortState(&s)
	// BoundAt ascending, WorkloadID as tiebreak.
	want := []string{"a", "c", "b"}
	for i, id := range want {
		if s.Bound[i].WorkloadID != id {
			t.Fatalf("Bound[%d].WorkloadID = %q, want %q (order %+v)", i, s.Bound[i].WorkloadID, id, s.Bound)
		}
	}
}

func TestStatePath_UsesCityRuntimeRoot(t *testing.T) {
	// Sibling of the capacity ledger's .gc/capacity, per citylayout.RuntimeRoot.
	if got := StatePath("/city"); got != "/city/.gc/binding/state.json" {
		t.Fatalf("StatePath = %q", got)
	}
	if got := LockPath("/city"); got != "/city/.gc/binding/state.lock" {
		t.Fatalf("LockPath = %q", got)
	}
}
