package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/gastownhall/gascity/internal/git"
	"github.com/spf13/cobra"
)

func newWorktreeCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Create and classify git worktrees",
		Long: `Create git worktrees through the front door that records their provenance.

Whether an unattended agent may auto-accept a worktree's CLAUDE.md/AGENTS.md
imports depends on where the tree came from, and nothing about a live worktree
reveals that: a tree staged to review an incoming pull request is as much a
worktree of this repository as one the pool created, and neither its path nor
its checked-out ref is evidence. The class is recorded when the tree is created
and read back when trust is decided.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stderr, "gc worktree: missing subcommand (create, provenance)") //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stderr, "gc worktree: unknown subcommand %q\n", args[0]) //nolint:errcheck // best-effort stderr
			}
			return errExit
		},
	}
	cmd.AddCommand(newWorktreeCreateCmd(stdout, stderr))
	cmd.AddCommand(newWorktreeProvenanceCmd(stdout, stderr))
	return cmd
}

func newWorktreeCreateCmd(stdout, stderr io.Writer) *cobra.Command {
	var base, branch, class, note string

	cmd := &cobra.Command{
		Use:   "create <path>",
		Short: "Create a worktree and record its provenance",
		Long: `Create a git worktree and record, as one operation, whether its content is
first-party.

  --class managed          created from a base this orchestration controls;
                           its instruction files may be auto-accepted
  --class external-review  staged to review a ref from outside (a PR head);
                           its instruction files are left for a human

The class is required and has no default. A worktree created any other way —
including by a bare "git worktree add" — carries no class, and an unclassified
worktree is never trusted.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			if err := git.New(".").WorktreeAdd(git.WorktreeAddOptions{
				Path:       path,
				Base:       base,
				Branch:     branch,
				Provenance: git.Provenance(class),
				Note:       note,
			}); err != nil {
				fmt.Fprintf(stderr, "gc worktree create: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			fmt.Fprintf(stdout, "created %s (provenance: %s)\n", path, class) //nolint:errcheck // best-effort stdout
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "ref to check out (default: HEAD)")
	cmd.Flags().StringVar(&branch, "branch", "", "create this branch at --base instead of checking out detached")
	cmd.Flags().StringVar(&class, "class", "", "provenance class: managed | external-review (required)")
	cmd.Flags().StringVar(&note, "note", "", "evidence behind the classification")
	_ = cmd.MarkFlagRequired("class")
	return cmd
}

func newWorktreeProvenanceCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provenance",
		Short: "Inspect and record worktree provenance",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stderr, "gc worktree provenance: missing subcommand (list, set)") //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stderr, "gc worktree provenance: unknown subcommand %q\n", args[0]) //nolint:errcheck // best-effort stderr
			}
			return errExit
		},
	}
	cmd.AddCommand(newWorktreeProvenanceListCmd(stdout, stderr))
	cmd.AddCommand(newWorktreeProvenanceSetCmd(stdout, stderr))
	return cmd
}

func newWorktreeProvenanceListCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every worktree of this repository and its recorded provenance",
		Long: `List each working tree of this repository with the provenance on record.

Worktrees reported as "unclassified" predate the front door or were created by
bare "git worktree add". They are not trusted, which for a pool worker shows up
as an unattended session parked at the external-imports prompt. Classify the
ones this orchestration actually created with "gc worktree provenance set".`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			worktrees, err := git.New(".").WorktreeList()
			if err != nil {
				fmt.Fprintf(stderr, "gc worktree provenance list: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			for _, wt := range worktrees {
				fmt.Fprintf(stdout, "%-16s %s\n", describeProvenance(wt.Path), wt.Path) //nolint:errcheck // best-effort stdout
			}
			return nil
		},
	}
}

// describeProvenance renders one worktree's trust state for the operator. It
// reports rather than decides: every failure reads as unclassified, which is
// how the trust check treats it too.
func describeProvenance(path string) string {
	g := git.New(path)
	if main, err := g.IsMainWorktree(); err == nil && main {
		return "main"
	}
	p, err := g.ReadWorktreeProvenance()
	if errors.Is(err, git.ErrNoProvenance) {
		return "unclassified"
	}
	if err != nil {
		return "unreadable"
	}
	return string(p.Class)
}

func newWorktreeProvenanceSetCmd(stdout, stderr io.Writer) *cobra.Command {
	var class, note string

	cmd := &cobra.Command{
		Use:   "set <path>",
		Short: "Record the provenance of an existing worktree",
		Long: `Record the provenance of a worktree that has none.

This is the migration path for worktrees created before the front door existed.
It is deliberately manual: the classification cannot be derived from the tree
itself — path shape and current ref are not evidence, since a review tree may
sit anywhere and hold any ref — so an operator must supply what they know.

--note is required and records that evidence. Marking a tree managed tells every
future unattended session that its instruction files may be auto-accepted
without a human, so the reason belongs on the record.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			g := git.New(path)

			// Backfill only. Reclassifying a tree that already has a class would
			// turn this into a way to promote a review tree to first-party, and
			// the obvious caller for that is a script or an agent rather than the
			// operator the command is written for. This is a guard rail, not a
			// security boundary: the stamp is an ordinary file, so anything
			// running as this user could write it directly. What actually keeps a
			// reviewed ref from classifying itself is that the stamp lives
			// outside the checkout (see git.WorktreeAdd).
			if existing, err := g.ReadWorktreeProvenance(); err == nil {
				fmt.Fprintf(stderr, "gc worktree provenance set: %s is already recorded as %q; recreate the worktree through `gc worktree create` to change it\n", path, existing.Class) //nolint:errcheck // best-effort stderr
				return errExit
			} else if !errors.Is(err, git.ErrNoProvenance) {
				fmt.Fprintf(stderr, "gc worktree provenance set: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}

			if err := g.WriteWorktreeProvenance(git.WorktreeProvenance{
				Class: git.Provenance(class),
				Note:  note,
			}); err != nil {
				fmt.Fprintf(stderr, "gc worktree provenance set: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			fmt.Fprintf(stdout, "recorded %s for %s\n", class, path) //nolint:errcheck // best-effort stdout
			return nil
		},
	}
	cmd.Flags().StringVar(&class, "class", "", "provenance class: managed | external-review (required)")
	cmd.Flags().StringVar(&note, "note", "", "evidence for the classification (required)")
	_ = cmd.MarkFlagRequired("class")
	_ = cmd.MarkFlagRequired("note")
	return cmd
}
