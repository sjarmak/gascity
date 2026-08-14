package beads

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// getWispRunner records every argv so a test can assert how many subprocesses a
// single Get spent, not just what it returned.
type getWispRunner struct {
	mu       sync.Mutex
	commands []string
	// wisps is the JSON `bd query` answers with, keyed by the exact argv.
	wisps map[string]string
}

func (r *getWispRunner) run(_, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.commands = append(r.commands, key)
	r.mu.Unlock()
	if strings.HasPrefix(key, "bd show ") {
		// The durable tier never has it; this is the miss that used to buy a
		// second subprocess unconditionally.
		return nil, errors.New("issue not found")
	}
	if out, ok := r.wisps[key]; ok {
		return []byte(out), nil
	}
	return []byte("[]"), nil
}

func (r *getWispRunner) countPrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.commands {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// TestGetSkipsWispFallbackForForeignPrefix is the regression gate for the
// doubled cost of a cross-store probe. The assertion is on the `bd query`
// COUNT, because the return value is ErrNotFound either way: a Get that spends
// two subprocesses to reach the same answer looks identical from the outside,
// which is how this stayed unnoticed while it ran ~13M times a day.
func TestGetSkipsWispFallbackForForeignPrefix(t *testing.T) {
	r := &getWispRunner{}
	store := NewBdStoreWithPrefix(t.TempDir(), r.run, "alp")

	_, err := store.Get("bet-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(bet-1) error = %v, want ErrNotFound", err)
	}
	if got := r.countPrefix("bd show "); got != 1 {
		t.Fatalf("bd show invocations = %d, want 1", got)
	}
	if got := r.countPrefix("bd query "); got != 0 {
		t.Fatalf("bd query invocations = %d, want 0; an \"alp\" store cannot hold a \"bet-\" id in EITHER tier, so the supplemental wisp query can only miss (commands: %v)", got, r.commands)
	}
}

// TestGetStillRunsWispFallbackForOwnedPrefix is the other half: the fallback
// must still fire, and still find the bead, for an id this store does own.
// Without this a "fix" that deleted the fallback outright would pass the test
// above.
func TestGetStillRunsWispFallbackForOwnedPrefix(t *testing.T) {
	r := &getWispRunner{wisps: map[string]string{
		`bd query --json ephemeral=true AND id=alp-9 --all --limit 1`: `[{"id":"alp-9","title":"wisp","status":"open"}]`,
	}}
	store := NewBdStoreWithPrefix(t.TempDir(), r.run, "alp")

	bead, err := store.Get("alp-9")
	if err != nil {
		t.Fatalf("Get(alp-9) = %v, want the wisp-tier bead", err)
	}
	if bead.ID != "alp-9" {
		t.Fatalf("bead.ID = %q, want alp-9", bead.ID)
	}
	if !bead.Ephemeral {
		t.Error("bead.Ephemeral = false, want true for a wisp-tier read")
	}
	if got := r.countPrefix("bd query "); got != 1 {
		t.Fatalf("bd query invocations = %d, want 1 (commands: %v)", got, r.commands)
	}
}

// TestGetWispFallbackFailsOpenWithoutDeclaredPrefix pins the fail-open half.
// A store with no declared prefix owns everything, so its behavior must be
// byte-identical to before the change — otherwise every plain NewBdStore
// (hook claims, scoped stores, the t3 bridge) silently loses wisp reads.
func TestGetWispFallbackFailsOpenWithoutDeclaredPrefix(t *testing.T) {
	r := &getWispRunner{wisps: map[string]string{
		`bd query --json ephemeral=true AND id=bet-1 --all --limit 1`: `[{"id":"bet-1","title":"wisp","status":"open"}]`,
	}}
	store := NewBdStore(t.TempDir(), r.run)

	bead, err := store.Get("bet-1")
	if err != nil {
		t.Fatalf("Get(bet-1) = %v, want the wisp found via the unconditional fallback", err)
	}
	if bead.ID != "bet-1" {
		t.Fatalf("bead.ID = %q, want bet-1", bead.ID)
	}
	if got := r.countPrefix("bd query "); got != 1 {
		t.Fatalf("bd query invocations = %d, want 1 (commands: %v)", got, r.commands)
	}
}

func TestMayOwnID(t *testing.T) {
	for _, tc := range []struct {
		prefix, id string
		want       bool
	}{
		{prefix: "alp", id: "alp-1", want: true},
		{prefix: "alp", id: "alp", want: true},
		{prefix: "alp", id: "bet-1", want: false},
		// "alpha-1" is NOT in the "alp-" namespace: the separator matters, or a
		// store would claim every prefix it happens to be a string prefix of.
		{prefix: "alp", id: "alpha-1", want: false},
		{prefix: "", id: "anything-1", want: true},
	} {
		store := NewBdStoreWithPrefix(t.TempDir(), func(string, string, ...string) ([]byte, error) {
			return nil, errors.New("not found")
		}, tc.prefix)
		if got := store.mayOwnID(tc.id); got != tc.want {
			t.Errorf("prefix %q mayOwnID(%q) = %v, want %v", tc.prefix, tc.id, got, tc.want)
		}
	}
}
