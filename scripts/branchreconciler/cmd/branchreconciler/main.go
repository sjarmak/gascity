// Command branchreconciler reports, per branch on a git remote, how many
// commits it carries that are not in main and whether it ever appeared as a
// pull request head ref against the given repo. It is a structural-fact
// report: deciding what to do with a surfaced branch (open a PR, rebase,
// delete it) is left to whoever reads the report.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/scripts/branchreconciler"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "branchreconciler: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("branchreconciler", flag.ContinueOnError)
	remote := fs.String("remote", "fork", "git remote whose branches to audit")
	base := fs.String("base", "origin/main", "ref to diff each branch against")
	repo := fs.String("repo", "gastownhall/gascity", "GitHub repo (owner/name) to search for pull requests")
	forkOwner := fs.String("fork-owner", "sjarmak", "GitHub login that owns --remote, used to match pull request head refs")
	format := fs.String("format", "json", "output format: json or markdown")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "json" && *format != "markdown" {
		return fmt.Errorf("--format must be json or markdown, got %q", *format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	branches, err := listBranches(ctx, *remote, *base)
	if err != nil {
		return fmt.Errorf("listing branches on remote %q: %w", *remote, err)
	}
	prHeadRefs, err := listPRHeadRefs(ctx, *repo, *forkOwner)
	if err != nil {
		return fmt.Errorf("listing pull request head refs on %q: %w", *repo, err)
	}

	reports := branchreconciler.Reconcile(branches, prHeadRefs)
	summary := branchreconciler.Summarize(reports)

	if *format == "markdown" {
		_, err := fmt.Fprint(stdout, branchreconciler.RenderMarkdown(reports, summary))
		return err
	}
	out, err := branchreconciler.RenderJSON(reports, summary)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(out))
	return err
}

// listBranches lists every branch on remote and, for each, counts commits
// reachable from it that are not reachable from base.
func listBranches(ctx context.Context, remote, base string) ([]branchreconciler.BranchInfo, error) {
	out, err := exec.CommandContext(ctx, "git", "for-each-ref", "--format=%(refname:short)", "refs/remotes/"+remote+"/").Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	names := branchreconciler.ParseForEachRef(string(out), remote)

	infos := make([]branchreconciler.BranchInfo, 0, len(names))
	for _, name := range names {
		rangeSpec := base + ".." + remote + "/" + name
		countOut, err := exec.CommandContext(ctx, "git", "rev-list", "--count", rangeSpec).Output()
		if err != nil {
			return nil, fmt.Errorf("git rev-list --count %s: %w", rangeSpec, err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(countOut)))
		if err != nil {
			return nil, fmt.Errorf("parsing commit count for %s: %w", name, err)
		}
		infos = append(infos, branchreconciler.BranchInfo{Name: name, CommitsAheadOfMain: n})
	}
	return infos, nil
}

// listPRHeadRefs lists every pull request ever opened against repo,
// regardless of state, and returns the set of head ref names whose source
// fork is owned by forkOwner.
func listPRHeadRefs(ctx context.Context, repo, forkOwner string) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", repo,
		"--state", "all",
		"--limit", "5000",
		"--json", "headRefName,headRepositoryOwner",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	return branchreconciler.ParsePRHeadRefs(out, forkOwner)
}
