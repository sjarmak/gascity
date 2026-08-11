package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	gitcore "github.com/gastownhall/gascity/internal/git"
)

func TestRecordClosedWorktreeCleanupDispositionsPersistsTerminalEvidence(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-term001")
	branch := "wt-ga-term001"
	head := registeredWorktreeHead(t, rigRoot, wt)

	reasons := []string{"success", "rejection", "cancel", "drain", "finalize"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			store := beads.NewMemStoreFrom(1, []beads.Bead{{
				ID:     "ga-term001",
				Status: "closed",
				Metadata: map[string]string{
					"gc.work_dir":  wt,
					"close_reason": reason,
				},
			}}, nil)
			var stderr bytes.Buffer

			if got := recordClosedWorktreeCleanupDispositions(reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, &stderr); got != 1 {
				t.Fatalf("recorded = %d, want 1; stderr=%s", got, stderr.String())
			}
			work, err := store.Get("ga-term001")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				worktreeCleanupPendingKey: "true",
				worktreeCleanupPathKey:    wt,
				worktreeCleanupRepoKey:    rigRoot,
				worktreeCleanupBranchKey:  branch,
				worktreeCleanupHeadKey:    head,
				worktreeCleanupReasonKey:  reason,
			}
			for key, value := range want {
				if work.Metadata[key] != value {
					t.Errorf("metadata[%q] = %q, want %q", key, work.Metadata[key], value)
				}
			}
		})
	}
}

func registeredWorktreeHead(t *testing.T, repo, path string) string {
	t.Helper()
	worktrees, err := gitcore.New(repo).WorktreeList()
	if err != nil {
		t.Fatal(err)
	}
	for _, worktree := range worktrees {
		if filepath.Clean(worktree.Path) == filepath.Clean(path) {
			return worktree.Head
		}
	}
	t.Fatalf("registered worktree %s not found in %s", path, repo)
	return ""
}

func TestRecordClosedWorktreeCleanupDispositionsRecoversAfterProcessDeath(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-crash01")
	store := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "ga-crash01", Status: "closed",
		Metadata: map[string]string{"gc.work_dir": wt, "close_reason": "dead-runtime"},
	}}, nil)

	// The bead is already durably closed when this controller instance starts:
	// this is the state left when the prior process died before bookkeeping.
	if got := recordClosedWorktreeCleanupDispositions(reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, &bytes.Buffer{}); got != 1 {
		t.Fatalf("recorded after restart = %d, want 1", got)
	}
	work, _ := store.Get("ga-crash01")
	if work.Metadata[worktreeCleanupPendingKey] != "true" {
		t.Fatal("restart reconciliation did not persist cleanup disposition")
	}
}

func TestRecordClosedWorktreeCleanupDispositionsSkipsNonTerminalWork(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-live002")
	store := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "ga-live002", Status: "in_progress", Metadata: map[string]string{"gc.work_dir": wt},
	}}, nil)

	if got := recordClosedWorktreeCleanupDispositions(reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, &bytes.Buffer{}); got != 0 {
		t.Fatalf("recorded = %d, want 0", got)
	}
	work, _ := store.Get("ga-live002")
	if _, exists := work.Metadata[worktreeCleanupPendingKey]; exists {
		t.Fatal("non-terminal work acquired a cleanup disposition")
	}
}

type dispositionWriteFailStore struct{ *beads.MemStore }

func (s dispositionWriteFailStore) SetMetadataBatch(string, map[string]string) error {
	return os.ErrPermission
}

func TestRecordClosedWorktreeCleanupDispositionsLogsWriteFailureAndContinues(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-fail002")
	base := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "ga-fail002", Status: "closed", Metadata: map[string]string{"gc.work_dir": wt},
	}}, nil)
	var stderr bytes.Buffer

	if got := recordClosedWorktreeCleanupDispositions(reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: dispositionWriteFailStore{base}}, &stderr); got != 0 {
		t.Fatalf("recorded = %d, want 0", got)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("permission denied")) {
		t.Fatalf("stderr = %q, want loud marker-write failure", stderr.String())
	}
}

func TestReapClosedBeadWorktreesRefusesClosedTreeWithoutDisposition(t *testing.T) {
	cityPath, rigRoot := initReapRig(t)
	wt := addClosedWorktree(t, rigRoot, cityPath, "builder", "ga-nomark1")
	store := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "ga-nomark1", Status: "closed", Metadata: map[string]string{"gc.work_dir": wt},
	}}, nil)
	injectLiveness(t, liveWorktreeState{scanned: true})

	report := reapClosedBeadWorktrees(cityPath, reapTestConfig(rigRoot), map[string]beads.Store{reapTestRigName: store}, nil, false, nil, nil, &bytes.Buffer{})
	if len(report.Reaped) != 0 || len(report.Protected) != 1 {
		t.Fatalf("Reaped=%+v Protected=%+v, want missing-marker refusal", report.Reaped, report.Protected)
	}
	if _, err := os.Stat(filepath.Clean(wt)); err != nil {
		t.Fatalf("missing-marker worktree was removed: %v", err)
	}
}
