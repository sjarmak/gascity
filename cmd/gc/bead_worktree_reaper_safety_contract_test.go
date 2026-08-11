package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
)

func TestReapClosedBeadWorktreesBoundsRemovalPerRun(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	var closed []beads.Bead
	var paths []string
	for i := 0; i < 11; i++ {
		id := "ga-bound" + string(rune('a'+i))
		paths = append(paths, addClosedWorktree(t, rigRoot, cityPath, "builder", id))
		closed = append(closed, beads.Bead{ID: id, Status: "closed"})
	}
	store := beads.NewMemStoreFrom(1, closed, nil)
	injectLiveness(t, liveWorktreeState{scanned: true})

	report := reapClosedBeadWorktrees(cityPath, reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, nil, false, events.Discard, nil, &bytes.Buffer{})
	if len(report.Reaped) != 10 {
		t.Fatalf("reaped=%d, want default max 10", len(report.Reaped))
	}
	remaining := 0
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("remaining=%d, want 1 after bounded run", remaining)
	}
}

type manifestWriteFailStore struct{ *beads.MemStore }

func (s manifestWriteFailStore) SetMetadataBatch(id string, values map[string]string) error {
	if values[worktreeCleanupManifestVersionKey] != "" {
		return os.ErrPermission
	}
	return s.MemStore.SetMetadataBatch(id, values)
}

func TestReapClosedBeadWorktreesRefusesWhenManifestCannotBeWritten(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-manif01")
	base := beads.NewMemStoreFrom(1, []beads.Bead{{ID: "ga-manif01", Status: "closed"}}, nil)
	store := manifestWriteFailStore{base}
	injectLiveness(t, liveWorktreeState{scanned: true})
	var stderr bytes.Buffer

	report := reapClosedBeadWorktrees(cityPath, reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, nil, false, events.Discard, nil, &stderr)
	if len(report.Reaped) != 0 {
		t.Fatalf("Reaped=%+v, want manifest failure to refuse", report.Reaped)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree removed despite manifest failure: %v", err)
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Fatalf("stderr=%q Protected=%+v, want loud manifest failure", stderr.String(), report.Protected)
	}
}

func TestReapClosedBeadWorktreesProtectsRegisteredParentWithChild(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	parent := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-parent1")
	child := filepath.Join(parent, "nested", "ga-child01")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, rigRoot, "worktree", "add", "-b", "wt-ga-child01", child)
	backdateWorktreeGitFile(t, child, 4*24*time.Hour)
	store := beads.NewMemStoreFrom(1, []beads.Bead{
		{ID: "ga-parent1", Status: "closed"},
		{ID: "ga-child01", Status: "in_progress"},
	}, nil)
	injectLiveness(t, liveWorktreeState{scanned: true})

	report := reapClosedBeadWorktrees(cityPath, reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, nil, false, events.Discard, nil, &bytes.Buffer{})
	if len(report.Reaped) != 0 || len(report.Protected) != 1 || !strings.Contains(report.Protected[0].Reason, "child worktree") {
		t.Fatalf("Reaped=%+v Protected=%+v, want topology refusal", report.Reaped, report.Protected)
	}
}
