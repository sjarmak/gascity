package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/mail"
)

// TestPrimeInjectMailContentShowsNewestUnreadWithinPriority pins dr-0hiq: a
// SessionStart banner with more unread mail than its display window must show
// newly arrived work direction instead of keeping an old backlog permanently
// in front. The read remains non-destructive; this test only constrains which
// equal-priority messages occupy the preview window.
func TestPrimeInjectMailContentShowsNewestUnreadWithinPriority(t *testing.T) {
	clearGCEnv(t)
	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[workspace]\nname = \"demo\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")
	t.Setenv("GC_CITY", cityDir)
	t.Setenv("GC_CITY_PATH", cityDir)
	t.Setenv("GC_ALIAS", "mayor")

	mp, code := openCityMailProvider(io.Discard, "test seed")
	if mp == nil {
		t.Fatalf("openCityMailProvider returned nil (code=%d)", code)
	}
	bodies := []string{
		"stale direction one",
		"stale direction two",
		"stale direction three",
		"new work direction",
	}
	for i, body := range bodies {
		if _, err := mp.Send("worker", "mayor", fmt.Sprintf("message %d", i+1), body); err != nil {
			t.Fatalf("seed Send(%d): %v", i+1, err)
		}
		// File-backed mail preserves CreatedAt at subsecond precision. Keep the
		// integration fixture's arrival order explicit rather than depending on
		// the host clock returning distinct adjacent readings.
		time.Sleep(time.Millisecond)
	}

	got := primeInjectMailContent()
	if !strings.Contains(got, "You have 4 unread message(s).") {
		t.Fatalf("prime mail injection missing full backlog count:\n%s", got)
	}
	if !strings.Contains(got, "new work direction") {
		t.Fatalf("prime mail injection hid newest work direction behind backlog:\n%s", got)
	}
	if strings.Contains(got, "stale direction one") {
		t.Fatalf("prime mail injection kept oldest equal-priority message in clamped window:\n%s", got)
	}
}

func TestSortSessionStartUnreadMailPrioritizesThenShowsNewest(t *testing.T) {
	base := time.Date(2026, time.August, 14, 5, 0, 0, 0, time.UTC)
	messages := []mail.Message{
		{ID: "old-low", Priority: 0, CreatedAt: base},
		{ID: "old-high", Priority: 2, CreatedAt: base.Add(time.Minute)},
		{ID: "new-low", Priority: 0, CreatedAt: base.Add(2 * time.Minute)},
		{ID: "new-high", Priority: 2, CreatedAt: base.Add(3 * time.Minute)},
	}

	got := sortSessionStartUnreadMail(messages)
	want := []string{"new-high", "old-high", "new-low", "old-low"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("sorted[%d].ID = %q, want %q; sorted=%v", i, got[i].ID, id, got)
		}
	}
	if messages[0].ID != "old-low" || messages[1].ID != "old-high" {
		t.Fatalf("sort mutated caller input: %v", messages)
	}
}
