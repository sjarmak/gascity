package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/worktree"
	"github.com/spf13/cobra"
)

// worktreeCmdOpts carries the flag values for gc worktree subcommands.
type worktreeCmdOpts struct {
	Repo   string
	Path   string
	Branch string
	Base   string
	DryRun bool
	JSON   bool

	// Provenance identifies who provisioned the workspace and for what.
	// gc does not persist it; the caller records it on the work item. It
	// is accepted here so a single provisioning call carries identity and
	// workspace state together, and echoed back in the JSON report so the
	// caller can confirm what this command was told.
	Prov worktreeProvenance
}

// worktreeProvenance is the workspace identity a provisioning caller
// supplies. Every field is optional to gc: an incomplete set is the
// caller's problem to adjudicate, and refusing here would only move the
// decision away from the actor that can make it.
// Every field is emitted unconditionally, including empty ones: callers
// assert on provenance equality, and a key omitted when empty reads as null
// rather than as the empty value the caller passed.
type worktreeProvenance struct {
	BeadID     string `json:"bead_id"`
	Root       string `json:"root"`
	Creator    string `json:"creator"`
	Owner      string `json:"owner"`
	Generation string `json:"generation"`
	StoreRef   string `json:"store_ref"`
	Rig        string `json:"rig"`
	BaseRef    string `json:"base_ref"`
	BaseSHA    string `json:"base_sha"`
	Lifecycle  string `json:"lifecycle"`
}

// newWorktreeCmd returns the gc worktree command group. It is the CLI face
// of internal/worktree — the transactional workspace owner (gc-r9fx) that
// sling and formula-managed workspace setup can route through instead of
// running competing ad hoc provisioning.
func newWorktreeCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Ensure, verify, or clean up agent workspace worktrees",
		Long: `Ensure or verify agent workspace worktrees.

gc worktree is the single transactional owner for workspace provisioning.
Postconditions: the path is the root of a worktree of the given repository,
with the given branch checked out on an attached HEAD (never detached).
A new branch is created from --base, resolved verbatim against the local
repository. Failed creation rolls back everything it created; --dry-run
plans without mutating anything.`,
	}
	cmd.AddCommand(newWorktreeEnsureCmd(stdout, stderr))
	cmd.AddCommand(newWorktreeVerifyCmd(stdout, stderr))
	cmd.AddCommand(newWorktreeCleanupCmd(stdout, stderr))
	return cmd
}

func worktreeFlagSet(cmd *cobra.Command, opts *worktreeCmdOpts) {
	cmd.Flags().StringVar(&opts.Repo, "repo", "", "repository directory the worktree belongs to (required)")
	cmd.Flags().StringVar(&opts.Path, "path", "", "worktree path (required)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "branch that must be checked out (required)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit the report as JSON")
	worktreeProvenanceFlagSet(cmd, &opts.Prov)
	_ = cmd.MarkFlagRequired("repo")   //nolint:errcheck // flag exists
	_ = cmd.MarkFlagRequired("path")   //nolint:errcheck // flag exists
	_ = cmd.MarkFlagRequired("branch") //nolint:errcheck // flag exists
}

func newWorktreeEnsureCmd(stdout, stderr io.Writer) *cobra.Command {
	var opts worktreeCmdOpts
	cmd := &cobra.Command{
		Use:   "ensure",
		Short: "Ensure the worktree exists and satisfies all postconditions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if runWorktreeEnsure(opts, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	worktreeFlagSet(cmd, &opts)
	cmd.Flags().StringVar(&opts.Base, "base", "", "base ref for creating a new branch (resolved verbatim locally)")
	cmd.Flags().BoolVarP(&opts.DryRun, "dry-run", "n", false, "plan without mutating anything")
	return cmd
}

func newWorktreeVerifyCmd(stdout, stderr io.Writer) *cobra.Command {
	var opts worktreeCmdOpts
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify the worktree satisfies all postconditions without mutating",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if runWorktreeVerify(opts, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	worktreeFlagSet(cmd, &opts)
	return cmd
}

// provenance returns the workspace identity as reported on the wire. The
// base ref is carried on the shared --base flag rather than a second
// provenance flag, so it is folded in here rather than stored twice.
func (o worktreeCmdOpts) provenance() worktreeProvenance {
	p := o.Prov
	p.BaseRef = o.Base
	return p
}

// provenanceWith reports the base commit this call actually established
// when it created the branch, falling back to the caller's stated value
// when no branch was created and no base was consulted.
func (o worktreeCmdOpts) provenanceWith(rep worktree.Report) worktreeProvenance {
	p := o.provenance()
	if rep.BaseSHA != "" {
		p.BaseSHA = rep.BaseSHA
	}
	return p
}

func (o worktreeCmdOpts) spec() (worktree.Spec, error) {
	repo, err := filepath.Abs(o.Repo)
	if err != nil {
		return worktree.Spec{}, fmt.Errorf("resolving --repo %q: %w", o.Repo, err)
	}
	path, err := filepath.Abs(o.Path)
	if err != nil {
		return worktree.Spec{}, fmt.Errorf("resolving --path %q: %w", o.Path, err)
	}
	return worktree.Spec{
		RepoDir: repo,
		Path:    path,
		Branch:  o.Branch,
		Base:    o.Base,
		BaseSHA: o.Prov.BaseSHA,
		DryRun:  o.DryRun,
	}, nil
}

func runWorktreeEnsure(opts worktreeCmdOpts, stdout, stderr io.Writer) int {
	spec, err := opts.spec()
	if err != nil {
		fmt.Fprintf(stderr, "gc worktree ensure: %v\n", err) //nolint:errcheck
		return 1
	}
	rep, err := worktree.Ensure(spec)
	if err != nil {
		fmt.Fprintf(stderr, "gc worktree ensure: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeWorktreeReport("ensure", rep, opts, stdout, stderr)
}

func runWorktreeVerify(opts worktreeCmdOpts, stdout, stderr io.Writer) int {
	spec, err := opts.spec()
	if err != nil {
		fmt.Fprintf(stderr, "gc worktree verify: %v\n", err) //nolint:errcheck
		return 1
	}
	rep, err := worktree.Verify(spec)
	if err != nil {
		fmt.Fprintf(stderr, "gc worktree verify: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeWorktreeReport("verify", rep, opts, stdout, stderr)
}

type worktreeJSONResult struct {
	SchemaVersion string             `json:"schema_version"`
	OK            bool               `json:"ok"`
	Command       string             `json:"command"`
	Action        string             `json:"action"`
	Provenance    worktreeProvenance `json:"provenance"`
	worktree.Report
}

func writeWorktreeReport(action string, rep worktree.Report, opts worktreeCmdOpts, stdout, stderr io.Writer) int {
	if opts.JSON {
		result := worktreeJSONResult{
			SchemaVersion: "1",
			OK:            true,
			Command:       "worktree " + action,
			Action:        action,
			Provenance:    opts.provenanceWith(rep),
			Report:        rep,
		}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "gc worktree: encoding report: %v\n", err) //nolint:errcheck
			return 1
		}
		return 0
	}
	switch {
	case len(rep.Planned) > 0:
		fmt.Fprintf(stdout, "would run (dry-run):\n") //nolint:errcheck
		for _, action := range rep.Planned {
			fmt.Fprintf(stdout, "  %s\n", action) //nolint:errcheck
		}
	case rep.Created:
		fmt.Fprintf(stdout, "created worktree %s on branch %s at %s\n", rep.Path, rep.Branch, rep.Head) //nolint:errcheck
	default:
		fmt.Fprintf(stdout, "worktree %s on branch %s at %s\n", rep.Path, rep.Branch, rep.Head) //nolint:errcheck
	}
	return 0
}

// worktreeProvenanceFlagSet registers the workspace-identity flags shared by
// every gc worktree subcommand. They are declared on all of them so a
// provisioning caller and the teardown caller that follows it can pass one
// identical argument set.
func worktreeProvenanceFlagSet(cmd *cobra.Command, p *worktreeProvenance) {
	cmd.Flags().StringVar(&p.BeadID, "bead", "", "work item this workspace belongs to")
	cmd.Flags().StringVar(&p.Root, "root", "", "directory the workspace roots live under")
	cmd.Flags().StringVar(&p.Creator, "creator", "", "actor that provisioned the workspace")
	cmd.Flags().StringVar(&p.Owner, "owner", "", "actor responsible for work held in the workspace")
	cmd.Flags().StringVar(&p.Generation, "generation", "", "provisioning generation for this work item")
	cmd.Flags().StringVar(&p.StoreRef, "store-ref", "", "store the work item lives in")
	cmd.Flags().StringVar(&p.Rig, "rig", "", "rig the workspace belongs to")
	cmd.Flags().StringVar(&p.BaseSHA, "base-sha", "", "commit the workspace branch was created from")
	cmd.Flags().StringVar(&p.Lifecycle, "lifecycle", "", "lifecycle state the caller believes the workspace is in")
}

func newWorktreeCleanupCmd(stdout, stderr io.Writer) *cobra.Command {
	var opts worktreeCmdOpts
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove the worktree, refusing when it holds work that would be lost",
		Long: `Remove the worktree at --path after proving it is the named worktree.

Cleanup refuses rather than destroying state: uncommitted changes, stashes,
or commits no remote reaches each decline the removal and name the gate that
refused, so the caller records an actionable retention. There is no force
mode and no dry-run; a refusal is resolved by the workspace owner deciding
the held work no longer matters, not by a stronger flag.

A path that is already gone is a success, reported as already_absent so the
caller does not record a removal this run did not perform.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if runWorktreeCleanup(opts, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	worktreeFlagSet(cmd, &opts)
	// Accepted so a caller can pass teardown the same argument set it
	// passed provisioning. Cleanup creates no branch, so the base is
	// provenance here, not an instruction.
	cmd.Flags().StringVar(&opts.Base, "base", "", "base ref the workspace branch was created from (recorded, not acted on)")
	return cmd
}

// worktreeCleanupJSONResult is the teardown wire contract. Callers branch on
// ok/already_absent/error.code, so the shape is stable on both outcomes: a
// refusal emits the same document with ok false and the gate named.
type worktreeCleanupJSONResult struct {
	SchemaVersion string                    `json:"schema_version"`
	OK            bool                      `json:"ok"`
	Command       string                    `json:"command"`
	Action        string                    `json:"action"`
	Provenance    worktreeProvenance        `json:"provenance"`
	Error         *worktreeCleanupJSONError `json:"error,omitempty"`
	worktree.CleanupReport
}

type worktreeCleanupJSONError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ExitCode int    `json:"exit_code"`
}

func runWorktreeCleanup(opts worktreeCmdOpts, stdout, stderr io.Writer) int {
	spec, err := opts.spec()
	if err != nil {
		fmt.Fprintf(stderr, "gc worktree cleanup: %v\n", err) //nolint:errcheck
		return 1
	}
	rep, cleanupErr := worktree.Cleanup(worktree.CleanupSpec{
		RepoDir: spec.RepoDir,
		Path:    spec.Path,
		Branch:  spec.Branch,
	})
	return writeWorktreeCleanupReport(rep, cleanupErr, opts, stdout, stderr)
}

// writeWorktreeCleanupReport emits the machine document on stdout and the
// human line on stderr. The streams stay separate because a caller parses
// stdout as one JSON document; a human line printed in front of it would
// make every read of the document fail.
func writeWorktreeCleanupReport(rep worktree.CleanupReport, cleanupErr error, opts worktreeCmdOpts, stdout, stderr io.Writer) int {
	code := 0
	var jsonErr *worktreeCleanupJSONError
	if cleanupErr != nil {
		code = 1
		var refusal *worktree.RefusalError
		if errors.As(cleanupErr, &refusal) {
			jsonErr = &worktreeCleanupJSONError{Code: refusal.Code, Message: refusal.Message, ExitCode: code}
		} else {
			jsonErr = &worktreeCleanupJSONError{Code: "cleanup_failed", Message: cleanupErr.Error(), ExitCode: code}
		}
		fmt.Fprintf(stderr, "gc worktree cleanup: %s\n", jsonErr.Code+": "+jsonErr.Message) //nolint:errcheck
	}

	if opts.JSON {
		result := worktreeCleanupJSONResult{
			SchemaVersion: "1",
			OK:            code == 0,
			Command:       "worktree cleanup",
			Action:        "cleanup",
			Provenance:    opts.provenance(),
			Error:         jsonErr,
			CleanupReport: rep,
		}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "gc worktree: encoding report: %v\n", err) //nolint:errcheck
			return 1
		}
		return code
	}
	if code == 0 {
		switch {
		case rep.AlreadyAbsent:
			fmt.Fprintf(stdout, "worktree %s was already absent\n", rep.Path) //nolint:errcheck
		default:
			fmt.Fprintf(stdout, "removed worktree %s on branch %s\n", rep.Path, rep.Branch) //nolint:errcheck
		}
	}
	return code
}
