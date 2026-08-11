package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	gitcore "github.com/gastownhall/gascity/internal/git"
	"github.com/gastownhall/gascity/internal/pathutil"
)

const (
	worktreeCleanupPendingKey = "gc.worktree_cleanup_pending"
	worktreeCleanupPathKey    = "gc.worktree_cleanup_path"
	worktreeCleanupRepoKey    = "gc.worktree_cleanup_repo"
	worktreeCleanupBranchKey  = "gc.worktree_cleanup_branch"
	worktreeCleanupHeadKey    = "gc.worktree_cleanup_head"
	worktreeCleanupReasonKey  = "gc.worktree_cleanup_reason"

	worktreeCleanupManifestVersionKey  = "gc.worktree_cleanup_manifest_version"
	worktreeCleanupManifestCriteriaKey = "gc.worktree_cleanup_manifest_criteria"
)

const worktreeCleanupCriteriaV1 = "registered,repo-branch-no-drift,clean,landed,process-visible-idle,no-live-session,no-active-bead-reference,topology-safe,stale,rescue-ref-not-landing"

// recordClosedWorktreeCleanupDispositions forward-reconciles closed work
// beads into durable, creator-recorded cleanup dispositions. Running this on
// every controller tick covers both ordinary closes and a close followed by
// process death before bookkeeping. Recording is best effort: a failed probe
// or write is loud but never blocks terminal lifecycle progress.
func recordClosedWorktreeCleanupDispositions(cfg *config.City, rigStores map[string]beads.Store, stderr io.Writer) int {
	if cfg == nil || len(rigStores) == 0 {
		return 0
	}
	if stderr == nil {
		stderr = io.Discard
	}

	recorded := 0
	for rigName, store := range rigStores {
		if store == nil {
			continue
		}
		repo := rigRootByName(cfg, rigName)
		if repo == "" {
			continue
		}
		registered, err := gitcore.New(repo).WorktreeList()
		if err != nil {
			fmt.Fprintf(stderr, "worktree disposition: listing registered worktrees for rig %s: %v\n", rigName, err) //nolint:errcheck
			continue
		}
		byPath := make(map[string]gitcore.Worktree, len(registered))
		for _, wt := range registered {
			byPath[pathutil.NormalizePathForCompare(wt.Path)] = wt
		}

		items, err := store.List(beads.ListQuery{AllowScan: true, IncludeClosed: true, Live: true, TierMode: beads.TierBoth})
		if err != nil {
			fmt.Fprintf(stderr, "worktree disposition: listing beads for rig %s: %v\n", rigName, err) //nolint:errcheck
			continue
		}
		for _, work := range items {
			if work.Status != "closed" {
				continue
			}
			path, ok := terminalWorktreePath(work)
			if !ok {
				continue
			}
			wt, ok := byPath[pathutil.NormalizePathForCompare(path)]
			if !ok {
				fmt.Fprintf(stderr, "worktree disposition: bead %s names unregistered worktree %s; refusing to record cleanup evidence\n", work.ID, path) //nolint:errcheck
				continue
			}
			reason := strings.TrimSpace(work.Metadata["close_reason"])
			if reason == "" {
				reason = "closed"
			}
			evidence := map[string]string{
				worktreeCleanupPendingKey: "true",
				worktreeCleanupPathKey:    wt.Path,
				worktreeCleanupRepoKey:    repo,
				worktreeCleanupBranchKey:  wt.Branch,
				worktreeCleanupHeadKey:    wt.Head,
				worktreeCleanupReasonKey:  reason,
			}
			if cleanupDispositionMatches(work.Metadata, evidence) {
				continue
			}
			if err := store.SetMetadataBatch(work.ID, evidence); err != nil {
				fmt.Fprintf(stderr, "worktree disposition: recording terminal evidence for bead %s: %v\n", work.ID, err) //nolint:errcheck
				continue
			}
			recorded++
		}
	}
	return recorded
}

func terminalWorktreePath(work beads.Bead) (string, bool) {
	canonical := strings.TrimSpace(work.Metadata["gc.work_dir"])
	legacy := strings.TrimSpace(work.Metadata["work_dir"])
	if canonical != "" && legacy != "" && !pathutil.SamePath(canonical, legacy) {
		return "", false
	}
	path := canonical
	if path == "" {
		path = legacy
	}
	return path, path != ""
}

func cleanupDispositionMatches(metadata map[string]string, want map[string]string) bool {
	for key, value := range want {
		if metadata[key] != value {
			return false
		}
	}
	return true
}

func cleanupDispositionProtectReason(work beads.Bead, repo string, wt gitcore.Worktree) string {
	if work.Metadata[worktreeCleanupPendingKey] != "true" {
		// Legacy beads created before work-dir publication cannot carry a
		// creator-recorded disposition because the creator never durably linked
		// them to a path. Preserve the existing conservative classifier for
		// those rows. Once either work-dir key is present, however, absence of
		// the marker is a failed terminal transition and must protect the tree.
		if path, ok := terminalWorktreePath(work); !ok || path == "" {
			return ""
		}
		return "terminal cleanup disposition missing (failing closed)"
	}
	want := map[string]string{
		worktreeCleanupPendingKey: "true",
		worktreeCleanupPathKey:    wt.Path,
		worktreeCleanupRepoKey:    repo,
		worktreeCleanupBranchKey:  wt.Branch,
		worktreeCleanupHeadKey:    wt.Head,
	}
	if !cleanupDispositionMatches(work.Metadata, want) || strings.TrimSpace(work.Metadata[worktreeCleanupReasonKey]) == "" {
		return "terminal cleanup disposition drifted or incomplete (failing closed)"
	}
	return ""
}

func manifestWorktreeCleanup(store beads.Store, work beads.Bead, repo string, wt gitcore.Worktree) error {
	reason := strings.TrimSpace(work.Metadata["close_reason"])
	if reason == "" {
		reason = strings.TrimSpace(work.Metadata[worktreeCleanupReasonKey])
	}
	if reason == "" {
		reason = "closed"
	}
	return store.SetMetadataBatch(work.ID, map[string]string{
		worktreeCleanupPendingKey:          "true",
		worktreeCleanupPathKey:             wt.Path,
		worktreeCleanupRepoKey:             repo,
		worktreeCleanupBranchKey:           wt.Branch,
		worktreeCleanupHeadKey:             wt.Head,
		worktreeCleanupReasonKey:           reason,
		worktreeCleanupManifestVersionKey:  "1",
		worktreeCleanupManifestCriteriaKey: worktreeCleanupCriteriaV1,
	})
}
