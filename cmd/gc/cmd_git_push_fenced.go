package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/gastownhall/gascity/internal/gitpushfenced"
	"github.com/spf13/cobra"
)

// gitPushFencedFlags holds the parsed flags for `gc git-push-fenced`.
type gitPushFencedFlags struct {
	remote       string
	branch       string
	remoteBranch string
	commit       string
	repo         string
	createOnly   bool
	allowNonFF   bool
	dryRun       bool
	jsonOut      bool
}

// newGitPushFencedCmd builds the hidden `gc git-push-fenced` verb: a
// read-back-verified, idempotent push formula steps call instead of a bare
// `git push`. It exists because a plain push's exit code cannot distinguish
// "the remote rejected this" from "the remote accepted this and the
// connection dropped before we saw it" — a bare retry loop on either of
// those confuses the two and can push the same commit twice, or attempt to
// rebase onto a branch that already has the commit. See
// internal/gitpushfenced for the full decision tree.
//
// The process exit code carries the three-way answer callers must not
// collapse into ordinary success/failure: 0 means the intended commit IS on
// the remote ref (pushed now, or already there); 1 means it definitively is
// NOT there and it is safe to retry (typically after a fetch + rebase); 2
// means the answer could not be determined and callers must NOT retry blind
// — this is a stop-and-escalate condition.
func newGitPushFencedCmd(stdout, stderr io.Writer) *cobra.Command {
	var flags gitPushFencedFlags
	cmd := &cobra.Command{
		Use:          "git-push-fenced",
		Short:        "Push a branch as a read-back-verified, idempotent effect (invoked by formula steps, not directly)",
		Hidden:       true,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return exitForCode(doGitPushFenced(flags, stdout, stderr))
		},
	}
	cmd.Flags().StringVar(&flags.remote, "remote", "origin", "remote name or URL to push to")
	cmd.Flags().StringVar(&flags.branch, "branch", "", "branch name to push to on the remote (required unless --remote-branch is set)")
	cmd.Flags().StringVar(&flags.remoteBranch, "remote-branch", "", "remote branch name, overriding --branch")
	cmd.Flags().StringVar(&flags.commit, "commit", "HEAD", "commit-ish to push")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "path to the git repository (default: current directory)")
	cmd.Flags().BoolVar(&flags.createOnly, "create-only", false, "refuse if the remote ref already exists at all")
	cmd.Flags().BoolVar(&flags.allowNonFF, "allow-non-ff", false, "push even when the observed remote commit is not an ancestor of the intended commit")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "perform every read but do not push")
	cmd.Flags().BoolVar(&flags.jsonOut, "json", false, "emit a structured JSON result")
	return cmd
}

// doGitPushFenced runs the fenced push and reports the result, returning the
// process exit code (0, 1, or 2) per the gitpushfenced contract. It always
// writes a structured Result as JSON when flags.jsonOut is set, on every
// code path, so the caller's JSON output is never replaced by the generic
// framework failure envelope.
func doGitPushFenced(flags gitPushFencedFlags, stdout, stderr io.Writer) int {
	repoDir := flags.repo
	if repoDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "gc git-push-fenced: resolving current directory: %v\n", err) //nolint:errcheck
			return 2
		}
		repoDir = wd
	}

	result := gitpushfenced.Push(context.Background(), gitpushfenced.Options{
		RepoDir:      repoDir,
		Remote:       flags.remote,
		Branch:       flags.branch,
		RemoteBranch: flags.remoteBranch,
		Commit:       flags.commit,
		CreateOnly:   flags.createOnly,
		AllowNonFF:   flags.allowNonFF,
		DryRun:       flags.dryRun,
	})

	if flags.jsonOut {
		if err := writeCLIJSONLine(stdout, result); err != nil {
			fmt.Fprintf(stderr, "gc git-push-fenced: encode JSON: %v\n", err) //nolint:errcheck
			return 2
		}
		return result.ExitCode
	}

	out := stdout
	if result.ExitCode != 0 {
		out = stderr
	}
	fmt.Fprintf(out, "gc git-push-fenced: %s: %s\n", result.Outcome, result.Note) //nolint:errcheck
	return result.ExitCode
}
