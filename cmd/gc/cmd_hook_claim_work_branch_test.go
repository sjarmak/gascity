package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// Workspace provenance at claim time (gc-j4sr). gc.work_branch must name the
// branch of the CLAIMING WORKER'S OWN checkout — the bead's gc.work_dir — and
// never the branch of the store the work query happened to be answered from.
// The two are routinely different: a rig-scoped worker's federated claim selects
// the shared rig checkout as its store, and that tree sits on whatever branch a
// human last left it on.

// stampWorkBranchProbe captures the branch a claim stamps, and the store dir the
// stamp write was addressed to, so a test can assert on provenance (the branch)
// and store selection (the dir) as the separate inputs they are.
type stampWorkBranchProbe struct {
	calls    int
	beadID   string
	branch   string
	storeDir string
}

// runClaimForWorkBranch drives a single claim of bead through the production
// stamp path with the REAL git branch resolver (ResolveWorkBranch left unset so
// applyDefaults binds hookResolveWorkBranch), against storeDir as the store the
// work query was answered from. Only the store writes are faked.
func runClaimForWorkBranch(t *testing.T, bead beads.Bead, storeDir string) *stampWorkBranchProbe {
	t.Helper()

	rows, err := json.Marshal([]beads.Bead{bead})
	if err != nil {
		t.Fatalf("marshaling work-query row: %v", err)
	}
	probe := &stampWorkBranchProbe{}
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(rows), nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			claimed := bead
			claimed.ID, claimed.Assignee, claimed.Status = beadID, assignee, "in_progress"
			return claimed, true, nil
		},
		// ResolveWorkBranch intentionally unset: exercise the real git resolver.
		StampWorkBranch: func(_ context.Context, dir string, _ []string, beadID, _, branch string) error {
			probe.calls++
			probe.beadID, probe.branch, probe.storeDir = beadID, branch, dir
			return nil
		},
		RecordSessionPointers: func(context.Context, string, []string, string, string, string, string) error { return nil },
	}
	opts := hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", storeDir, opts, ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim() = %d, want 0; stderr=%s", code, stderr.String())
	}
	return probe
}

// initRepoOnBranch creates a real git repo at dir with one commit, checked out
// on branch. Returns dir for call-site brevity.
func initRepoOnBranch(t *testing.T, dir, branch string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating repo dir %s: %v", dir, err)
	}
	mustGit(t, dir, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seeding repo %s: %v", dir, err)
	}
	mustGit(t, dir, "add", "seed.txt")
	mustGit(t, dir, "commit", "--quiet", "-m", "seed")
	mustGit(t, dir, "checkout", "--quiet", "-B", branch)
	return dir
}

// TestHookClaimStampsWorkerBranchNotStoreBranch is the gc-j4sr regression: the
// store checkout is on one branch and the worker's own checkout on another, and
// the claim must stamp the worker's branch — the tree the commits actually land
// in. Before the fix the branch was resolved from the store dir, so the shared
// rig checkout's unrelated branch was stamped onto every claimed bead.
func TestHookClaimStampsWorkerBranchNotStoreBranch(t *testing.T) {
	root := t.TempDir()
	storeDir := initRepoOnBranch(t, filepath.Join(root, "rig-store"), "_pr1945_check")
	workerDir := initRepoOnBranch(t, filepath.Join(root, "worker"), "bd-gc-j4sr")

	probe := runClaimForWorkBranch(t, beads.Bead{
		ID:     "wb-worker",
		Status: "open",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "worker",
			beadmeta.WorkDirMetadataKey:  workerDir,
		},
	}, storeDir)

	if probe.calls != 1 {
		t.Fatalf("stamp writes = %d, want 1", probe.calls)
	}
	if probe.branch != "bd-gc-j4sr" {
		t.Errorf("stamped gc.work_branch = %q, want %q (the worker checkout's branch)", probe.branch, "bd-gc-j4sr")
	}
	if probe.branch == "_pr1945_check" {
		t.Errorf("stamped the STORE checkout's branch %q — work provenance is poisoned", probe.branch)
	}
	// Store selection and workspace provenance are separate inputs: the write is
	// still addressed to the store, only the branch comes from the worker.
	if probe.storeDir != storeDir {
		t.Errorf("stamp write addressed to %q, want the store dir %q", probe.storeDir, storeDir)
	}
}

// TestHookClaimSkipsStampWithoutAuthoritativeWorkerCheckout covers the beads a
// stamp must decline rather than guess at: no gc.work_dir recorded, a
// gc.work_dir that is not a git repo, and one whose HEAD is detached. In every
// case the store checkout sits on a perfectly readable branch, and inferring
// from it is exactly the bug. Leaving gc.work_branch unset is the intended
// outcome — the work-record close gate then fails loudly on a shipped bead
// rather than validating a commit against an unrelated branch.
func TestHookClaimSkipsStampWithoutAuthoritativeWorkerCheckout(t *testing.T) {
	root := t.TempDir()
	storeDir := initRepoOnBranch(t, filepath.Join(root, "rig-store"), "_pr1945_check")

	nonRepo := filepath.Join(root, "scaffold")
	if err := os.MkdirAll(nonRepo, 0o755); err != nil {
		t.Fatalf("creating non-repo dir: %v", err)
	}
	detached := initRepoOnBranch(t, filepath.Join(root, "detached"), "tmp")
	mustGit(t, detached, "checkout", "--quiet", "--detach", "HEAD")

	for _, tc := range []struct {
		name    string
		workDir string
	}{
		{name: "no work_dir recorded", workDir: ""},
		{name: "work_dir is not a repo", workDir: nonRepo},
		{name: "work_dir is missing entirely", workDir: filepath.Join(root, "gone")},
		{name: "work_dir has detached HEAD", workDir: detached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]string{beadmeta.RoutedToMetadataKey: "worker"}
			if tc.workDir != "" {
				meta[beadmeta.WorkDirMetadataKey] = tc.workDir
			}
			probe := runClaimForWorkBranch(t, beads.Bead{
				ID: "wb-none", Status: "open", Metadata: meta,
			}, storeDir)

			if probe.calls != 0 {
				t.Fatalf("stamp writes = %d (branch %q), want 0: no authoritative worker checkout exists, so the store branch must never be inferred", probe.calls, probe.branch)
			}
		})
	}
}

// TestHookClaimFallsBackToLegacyWorkDir covers the beads this fork actually
// produces: gc.work_dir holds the canonical per-bead worktree path the workspace
// provisioner intends, which may never have been created, while the legacy
// work_dir still records the tree the worker really used (observed live on
// gc-6mdz: gc.work_dir named a path that does not exist, work_dir named the
// worktree on the bead's own branch). Falling back recovers that provenance.
// The fallback is self-validating rather than trusting: a legacy value written
// under the old artifact-dir semantics is not a repo, so it resolves to no
// branch and is skipped like any other unusable candidate.
func TestHookClaimFallsBackToLegacyWorkDir(t *testing.T) {
	root := t.TempDir()
	storeDir := initRepoOnBranch(t, filepath.Join(root, "rig-store"), "_pr1945_check")
	realWorktree := initRepoOnBranch(t, filepath.Join(root, "polecat-4-6mdz"), "bd-gc-6mdz")
	artifactDir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("creating artifact dir: %v", err)
	}

	for _, tc := range []struct {
		name       string
		workDir    string
		legacyDir  string
		wantBranch string
	}{
		{
			name:       "canonical work_dir never created, legacy holds the real worktree",
			workDir:    filepath.Join(root, "polecat", "gc-6mdz"), // never provisioned
			legacyDir:  realWorktree,
			wantBranch: "bd-gc-6mdz",
		},
		{
			name:       "canonical work_dir absent entirely, legacy holds the real worktree",
			workDir:    "",
			legacyDir:  realWorktree,
			wantBranch: "bd-gc-6mdz",
		},
		{
			name:       "legacy value is an artifact dir, not a repo: no branch to recover",
			workDir:    filepath.Join(root, "polecat", "gc-6mdz"),
			legacyDir:  artifactDir,
			wantBranch: "", // no stamp
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]string{beadmeta.RoutedToMetadataKey: "worker"}
			if tc.workDir != "" {
				meta[beadmeta.WorkDirMetadataKey] = tc.workDir
			}
			meta[beadmeta.LegacyWorkDirMetadataKey] = tc.legacyDir

			probe := runClaimForWorkBranch(t, beads.Bead{
				ID: "wb-legacy", Status: "open", Metadata: meta,
			}, storeDir)

			if tc.wantBranch == "" {
				if probe.calls != 0 {
					t.Fatalf("stamp writes = %d (branch %q), want 0", probe.calls, probe.branch)
				}
				return
			}
			if probe.branch != tc.wantBranch {
				t.Errorf("stamped %q, want %q", probe.branch, tc.wantBranch)
			}
			if probe.branch == "_pr1945_check" {
				t.Errorf("fell through to the STORE branch %q", probe.branch)
			}
		})
	}
}

// TestHookClaimPrefersCanonicalWorkDirOverLegacy pins the precedence: when both
// keys name a usable repo, the canonical gc.work_dir wins. The legacy key is a
// recovery path for beads the canonical key fails to resolve, never an override.
func TestHookClaimPrefersCanonicalWorkDirOverLegacy(t *testing.T) {
	root := t.TempDir()
	storeDir := initRepoOnBranch(t, filepath.Join(root, "rig-store"), "_pr1945_check")
	canonical := initRepoOnBranch(t, filepath.Join(root, "canonical"), "bd-canonical")
	legacy := initRepoOnBranch(t, filepath.Join(root, "legacy"), "bd-legacy")

	probe := runClaimForWorkBranch(t, beads.Bead{
		ID: "wb-prec", Status: "open",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:      "worker",
			beadmeta.WorkDirMetadataKey:       canonical,
			beadmeta.LegacyWorkDirMetadataKey: legacy,
		},
	}, storeDir)

	if probe.branch != "bd-canonical" {
		t.Errorf("stamped %q, want bd-canonical (canonical gc.work_dir takes precedence)", probe.branch)
	}
}

// TestHookClaimReusedSessionStampsEachBeadsOwnBranch guards the pool-reuse case:
// one long-lived session claims a second bead in a different worktree. Each bead
// must be stamped from its OWN gc.work_dir, so the prior bead's branch cannot be
// retained. Resolving from the (unchanged) store dir would stamp both beads
// identically and silently misattribute the second one's work.
func TestHookClaimReusedSessionStampsEachBeadsOwnBranch(t *testing.T) {
	root := t.TempDir()
	storeDir := initRepoOnBranch(t, filepath.Join(root, "rig-store"), "_pr1945_check")
	firstDir := initRepoOnBranch(t, filepath.Join(root, "first"), "bd-first")
	secondDir := initRepoOnBranch(t, filepath.Join(root, "second"), "bd-second")

	first := runClaimForWorkBranch(t, beads.Bead{
		ID: "wb-first", Status: "open",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "worker",
			beadmeta.WorkDirMetadataKey:  firstDir,
		},
	}, storeDir)
	if first.branch != "bd-first" {
		t.Fatalf("first claim stamped %q, want bd-first", first.branch)
	}

	second := runClaimForWorkBranch(t, beads.Bead{
		ID: "wb-second", Status: "open",
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey: "worker",
			beadmeta.WorkDirMetadataKey:  secondDir,
		},
	}, storeDir)
	if second.branch != "bd-second" {
		t.Errorf("second claim stamped %q, want bd-second (each bead resolves its own worktree)", second.branch)
	}
	if second.branch == first.branch {
		t.Errorf("reused session retained the prior bead's branch %q", first.branch)
	}
}
