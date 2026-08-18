// Package gitpushfenced implements a read-back-verified, idempotent git push.
//
// A plain `git push` is unsafe to retry blindly: a non-zero exit can mean the
// remote rejected the update, OR it can mean the remote accepted the update
// and the connection dropped before the client saw the acknowledgement. A
// caller that treats every non-zero exit as "definitely failed" and retries
// can push the same commit twice, or — worse, when the retry rebases first —
// silently attempt to push over a landed commit believing the branch is
// still behind.
//
// Push implements the three-part fenced-push rule:
//
//  1. Read back first. The remote ref is resolved before it is touched. An
//     unreadable remote is UNKNOWN, never treated as absent.
//  2. Collapse the repeat. If the remote ref already points at the intended
//     commit, this is a repeat of a landed attempt: report success with zero
//     writes.
//  3. Pin the lease. The push uses `--force-with-lease=<ref>:<observed-oid>`
//     (or the empty-expect form when the ref is absent) so the write is
//     conditioned on the exact state that was just read, never a bare
//     `--force-with-lease`.
//
// After the push, the ref is read back a SECOND time. The push command's own
// exit code is treated only as a hint: a non-zero exit that the verify
// readback shows landed anyway is reported as success. If the verify
// readback itself fails, the outcome is UNKNOWN — Push never guesses.
//
// Every outcome maps to exactly one of three exit codes, matched by
// ExitCodeForOutcome:
//
//	0 — the intended commit IS on the remote ref (pushed now, or already
//	    there before this call).
//	1 — definitively did NOT happen (lease refused, rejected, or the remote
//	    moved out from under an ancestry check). Safe to retry, typically
//	    after a fetch + rebase.
//	2 — could not be determined (a readback failed, or the invocation itself
//	    was invalid). NEVER retry blind on a 2 — this is the one case a
//	    caller must stop and escalate instead of looping.
package gitpushfenced

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/gastownhall/gascity/internal/git"
)

// Outcome names the specific branch of the decision tree Push took. It is
// finer-grained than ExitCode: several outcomes share one exit code but
// describe different situations (for example OutcomeAlreadyLanded and
// OutcomePushed are both exit 0).
type Outcome string

// The complete set of outcomes Push can report. See ExitCodeForOutcome for
// the exit code each one maps to.
const (
	// OutcomeAlreadyLanded means the remote ref already pointed at the
	// intended commit before this call did anything. No write occurred.
	OutcomeAlreadyLanded Outcome = "already_landed"
	// OutcomePushed means the push ran, exited zero, and the verify readback
	// confirms the intended commit landed.
	OutcomePushed Outcome = "pushed"
	// OutcomeLandedDespiteNonZero means the push command itself exited
	// non-zero, but the verify readback shows the intended commit landed
	// anyway. This is the case a bare retry-on-nonzero loop gets wrong.
	OutcomeLandedDespiteNonZero Outcome = "landed_despite_nonzero"
	// OutcomeDryRun means DryRun was set; Push returned before running any
	// write.
	OutcomeDryRun Outcome = "dry_run"
	// OutcomeCreateOnlyExists means CreateOnly was set and the remote ref
	// already exists (regardless of what it points at). Refused before any
	// write.
	OutcomeCreateOnlyExists Outcome = "create_only_exists"
	// OutcomeNotAncestor means the observed remote commit is definitively
	// not an ancestor of the intended commit, and AllowNonFF was not set.
	// Pushing would discard remote history, so Push refused before writing.
	OutcomeNotAncestor Outcome = "not_ancestor"
	// OutcomeRejected means the push ran, and the verify readback confirms
	// the intended commit definitively did not land (something else holds
	// the ref).
	OutcomeRejected Outcome = "rejected"
	// OutcomePrePushReadFailed means the initial ls-remote lookup, before
	// any write, could not be completed. The remote state is UNKNOWN, not
	// absent.
	OutcomePrePushReadFailed Outcome = "pre_push_read_failed"
	// OutcomeVerifyReadFailed means the push ran, but the post-push readback
	// used to confirm the outcome could not be completed. Whether the
	// commit landed is UNKNOWN; Push never assumes failure here.
	OutcomeVerifyReadFailed Outcome = "verify_read_failed"
	// OutcomeAncestryUnknown means `git merge-base --is-ancestor` exited
	// with a code other than 0 (ancestor) or 1 (not an ancestor) — an
	// error, not an answer. Distinct from OutcomeNotAncestor.
	OutcomeAncestryUnknown Outcome = "ancestry_unknown"
	// OutcomeAncestryUnreadableLocally means the remote's observed commit
	// is not present in the local object database, so ancestry cannot be
	// evaluated either way.
	OutcomeAncestryUnreadableLocally Outcome = "ancestry_unreadable_locally"
	// OutcomeInvalidInvocation means the call itself was malformed (for
	// example, an unresolvable commit-ish, or neither Branch nor
	// RemoteBranch set) — this is a caller bug, not a push outcome.
	OutcomeInvalidInvocation Outcome = "invalid_invocation"
)

// ExitCodeForOutcome returns the exit code (0, 1, or 2) that corresponds to
// outcome, per the contract documented on Outcome and on Push. Result.ExitCode
// is always set from this function, so callers needing only the exit code
// never need to duplicate this mapping.
func ExitCodeForOutcome(outcome Outcome) int {
	switch outcome {
	case OutcomeAlreadyLanded, OutcomePushed, OutcomeLandedDespiteNonZero, OutcomeDryRun:
		return 0
	case OutcomeCreateOnlyExists, OutcomeNotAncestor, OutcomeRejected:
		return 1
	default:
		// OutcomePrePushReadFailed, OutcomeVerifyReadFailed,
		// OutcomeAncestryUnknown, OutcomeAncestryUnreadableLocally,
		// OutcomeInvalidInvocation, and any future unknown outcome all map
		// to "could not determine" rather than a guess.
		return 2
	}
}

// Result is the complete, structured outcome of a Push call. It carries
// enough detail for both a human-readable report and a machine-readable one
// (JSON encoding included).
type Result struct {
	// Outcome is the specific branch of the decision tree that was taken.
	Outcome Outcome `json:"outcome"`
	// ExitCode is ExitCodeForOutcome(Outcome); repeated here so JSON
	// consumers do not need the mapping table.
	ExitCode int `json:"exit_code"`
	// Note is a short, human-readable explanation of what happened and why.
	Note string `json:"note"`

	// Remote is the remote name or URL that was targeted.
	Remote string `json:"remote"`
	// Ref is the fully-qualified remote ref that was targeted, for example
	// "refs/heads/main".
	Ref string `json:"ref"`

	// Intended is the oid of the commit Push tried to land on Ref. Empty
	// only when the invocation was invalid before the commit could be
	// resolved.
	Intended string `json:"intended,omitempty"`
	// ObservedBefore is the oid Ref pointed at when it was read back before
	// any write, or empty if Ref did not exist at that point.
	ObservedBefore string `json:"observed_before,omitempty"`
	// ObservedAfter is the oid Ref pointed at when it was read back after
	// the push attempt, or empty if that readback could not be completed or
	// was not reached.
	ObservedAfter string `json:"observed_after,omitempty"`

	// PushAttempted is true only when `git push` was actually invoked.
	// False for every outcome decided by a read (repeat collapse,
	// create-only, ancestry refusal, dry-run, or a failed pre-push read).
	PushAttempted bool `json:"push_attempted"`
	// PushExitCode is the exit code `git push` itself returned, when
	// PushAttempted is true. It is a HINT ONLY — see OutcomeLandedDespiteNonZero.
	PushExitCode int `json:"push_exit_code,omitempty"`
	// PushStderr is the stderr `git push` produced, when PushAttempted is
	// true and it wrote anything.
	PushStderr string `json:"push_stderr,omitempty"`
}

// Options configures a Push call.
type Options struct {
	// RepoDir is the local git repository the push runs from. Required.
	RepoDir string
	// Remote is the remote name (e.g. "origin") or URL to push to. Required.
	Remote string
	// Branch is the branch name Push resolves to a remote ref
	// (refs/heads/<Branch>) when RemoteBranch is empty. Either Branch or
	// RemoteBranch must be set.
	Branch string
	// RemoteBranch, when set, overrides Branch for naming the remote ref.
	// This lets a caller push a local branch under a different remote name.
	RemoteBranch string
	// Commit is the commit-ish to push. Defaults to "HEAD" when empty.
	Commit string
	// CreateOnly, when true, refuses the push if the remote ref already
	// exists at all, regardless of what it points at.
	CreateOnly bool
	// AllowNonFF, when true, skips the local ancestry check and pushes even
	// when the observed remote commit is not an ancestor of Intended. Use
	// with care: this is the one path that can discard remote history.
	AllowNonFF bool
	// DryRun, when true, performs every read but returns before running the
	// actual push.
	DryRun bool
}

// Push performs a fenced push per the package-level decision tree. It never
// panics on a git failure; every failure mode is captured in the returned
// Result.
func Push(ctx context.Context, opts Options) Result {
	r := runner{dir: opts.RepoDir}

	remoteBranch := strings.TrimSpace(opts.RemoteBranch)
	if remoteBranch == "" {
		remoteBranch = strings.TrimSpace(opts.Branch)
	}
	if remoteBranch == "" {
		return invalidInvocation(opts, "", "neither --branch nor --remote-branch was set")
	}
	ref := remoteBranch
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}

	commitish := strings.TrimSpace(opts.Commit)
	if commitish == "" {
		commitish = "HEAD"
	}
	intended, ok := r.resolveCommit(ctx, commitish)
	if !ok {
		return invalidInvocation(opts, ref, fmt.Sprintf("could not resolve commit-ish %q in %q", commitish, opts.RepoDir))
	}

	base := Result{Remote: opts.Remote, Ref: ref, Intended: intended}

	// Step 1: read back first.
	before := r.readRemoteRef(ctx, opts.Remote, ref)
	if before.failed {
		return finish(base, OutcomePrePushReadFailed,
			fmt.Sprintf("could not read %s from %s before pushing; remote state is unknown", ref, opts.Remote))
	}
	if before.exists {
		base.ObservedBefore = before.oid
	}

	// Step 2: collapse the repeat.
	if before.exists && before.oid == intended {
		return finish(base, OutcomeAlreadyLanded,
			fmt.Sprintf("%s already points at %s; nothing to push", ref, intended))
	}

	// create-only refuses any existing ref outright.
	if opts.CreateOnly && before.exists {
		return finish(base, OutcomeCreateOnlyExists,
			fmt.Sprintf("--create-only set and %s already exists at %s", ref, before.oid))
	}

	// Ancestry check, unless the caller explicitly allows a non-fast-forward
	// push or the ref does not exist yet (nothing to be an ancestor of).
	if before.exists && !opts.AllowNonFF {
		if !r.localHasCommit(ctx, before.oid) {
			return finish(base, OutcomeAncestryUnreadableLocally,
				fmt.Sprintf("observed remote commit %s for %s is not present locally; cannot evaluate ancestry", before.oid, ref))
		}
		switch r.isAncestor(ctx, before.oid, intended) {
		case ancestryNo:
			return finish(base, OutcomeNotAncestor,
				fmt.Sprintf("remote %s at %s is not an ancestor of %s; pushing would discard remote history", ref, before.oid, intended))
		case ancestryUnknown:
			return finish(base, OutcomeAncestryUnknown,
				fmt.Sprintf("could not determine whether remote %s at %s is an ancestor of %s", ref, before.oid, intended))
		case ancestryYes:
			// proceed
		}
	}

	if opts.DryRun {
		return finish(base, OutcomeDryRun, fmt.Sprintf("--dry-run set; would push %s to %s at %s", intended, opts.Remote, ref))
	}

	// Step 3: pin the lease and push.
	lease := ref + ":" + before.oid
	if !before.exists {
		lease = ref + ":" // empty-expect: ref must not exist
	}
	pushArgs := []string{"push", "--force-with-lease=" + lease, opts.Remote, intended + ":" + ref}
	pushStdout, pushStderr, pushExit, runErr := r.run(ctx, pushArgs...)
	_ = pushStdout
	base.PushAttempted = true
	base.PushExitCode = pushExit
	base.PushStderr = strings.TrimSpace(pushStderr)
	if runErr != nil && pushExit == 0 {
		// The command could not even be started (e.g. git binary missing).
		// Treat identically to a non-zero exit: the verify readback below is
		// the authority, not this local process error.
		base.PushExitCode = -1
	}

	// Verify readback — authoritative over the push command's own exit code.
	after := r.readRemoteRef(ctx, opts.Remote, ref)
	if after.failed {
		return finish(base, OutcomeVerifyReadFailed,
			fmt.Sprintf("push to %s exited %d, but the verify readback of %s failed; landing state is unknown — do not retry blind", opts.Remote, pushExit, ref))
	}
	if after.exists {
		base.ObservedAfter = after.oid
	}

	if after.exists && after.oid == intended {
		if pushExit == 0 {
			return finish(base, OutcomePushed, fmt.Sprintf("%s now points at %s", ref, intended))
		}
		return finish(base, OutcomeLandedDespiteNonZero,
			fmt.Sprintf("git push exited %d, but the verify readback shows %s landed at %s anyway", pushExit, ref, intended))
	}

	return finish(base, OutcomeRejected,
		fmt.Sprintf("push to %s did not land: %s is at %q, want %q", opts.Remote, ref, after.oid, intended))
}

// finish stamps outcome and its exit code onto result and a human Note,
// returning the completed Result. Centralizing this keeps ExitCode and
// Outcome from drifting apart across the many return points in Push.
func finish(result Result, outcome Outcome, note string) Result {
	result.Outcome = outcome
	result.ExitCode = ExitCodeForOutcome(outcome)
	result.Note = note
	return result
}

// invalidInvocation builds the Result for a malformed call, before enough
// state is known to run any git command.
func invalidInvocation(opts Options, ref, note string) Result {
	return finish(Result{Remote: opts.Remote, Ref: ref}, OutcomeInvalidInvocation, note)
}

// runner scopes git subprocess invocations to a single repository directory.
type runner struct {
	dir string
}

// run executes git with args in r.dir, using the same sanitized environment
// internal/git uses for worktree operations, and returns stdout, stderr, the
// process exit code, and any error starting the process. A non-zero exit
// code is reported via exitCode, not via the returned error: callers that
// need to distinguish "ran and exited N" from "could not run at all" check
// err.
func (r runner) run(ctx context.Context, args ...string) (stdoutStr, stderrStr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	cmd.Env = git.SanitizedEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		return stdout.String(), stderr.String(), -1, runErr
	}
	return stdout.String(), stderr.String(), code, nil
}

// refRead is the outcome of reading a single remote ref.
type refRead struct {
	// failed is true when the lookup itself could not be completed (the
	// remote is unreachable, misnamed, etc.) — distinct from the ref simply
	// not existing.
	failed bool
	// exists is true when the remote reports a match for ref.
	exists bool
	// oid is the commit ref points at, valid only when exists is true.
	oid string
}

// readRemoteRef resolves ref on remote via `git ls-remote`, without
// --exit-code: under plain ls-remote, exit 0 always means the lookup itself
// succeeded (whether or not a line matched), and any other exit code means
// the lookup failed. That split is what lets refRead distinguish "absent"
// (exit 0, no matching line — trustworthy) from "unknown" (exit != 0, or a
// line that could not be parsed).
func (r runner) readRemoteRef(ctx context.Context, remote, ref string) refRead {
	stdout, _, exitCode, err := r.run(ctx, "ls-remote", remote, ref)
	if err != nil || exitCode != 0 {
		return refRead{failed: true}
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return refRead{failed: true}
		}
		oid := fields[0]
		if len(oid) != 40 {
			// Not a well-formed full oid; refuse to guess.
			return refRead{failed: true}
		}
		return refRead{exists: true, oid: oid}
	}
	return refRead{exists: false}
}

// resolveCommit resolves commitish to a full commit oid within r.dir. The
// ^{commit} suffix rejects anything that does not dereference to a commit
// object (an invalid or dangling ref resolves to nothing).
func (r runner) resolveCommit(ctx context.Context, commitish string) (string, bool) {
	stdout, _, exitCode, err := r.run(ctx, "rev-parse", "--verify", commitish+"^{commit}")
	if err != nil || exitCode != 0 {
		return "", false
	}
	oid := strings.TrimSpace(stdout)
	if len(oid) != 40 {
		return "", false
	}
	return oid, true
}

// localHasCommit reports whether oid is present (readable) in the local
// object database.
func (r runner) localHasCommit(ctx context.Context, oid string) bool {
	_, _, exitCode, err := r.run(ctx, "cat-file", "-e", oid+"^{commit}")
	return err == nil && exitCode == 0
}

// ancestryAnswer is the trichotomy `git merge-base --is-ancestor` actually
// supports: yes, no, or "the check itself failed" — which must never be
// folded into "no".
type ancestryAnswer int

const (
	// ancestryUnknown means the check could not be answered either way
	// (merge-base exited with something other than 0 or 1).
	ancestryUnknown ancestryAnswer = iota
	// ancestryYes means ancestor IS an ancestor of descendant (exit 0).
	ancestryYes
	// ancestryNo means ancestor is definitively NOT an ancestor of
	// descendant (exit 1).
	ancestryNo
)

// isAncestor reports whether ancestor is an ancestor of descendant. Per git's
// own contract for `merge-base --is-ancestor`, only exit code 0 (yes) and
// exit code 1 (no) are answers; any other exit code is an error and maps to
// ancestryUnknown here, never silently folded into ancestryNo.
func (r runner) isAncestor(ctx context.Context, ancestor, descendant string) ancestryAnswer {
	_, _, exitCode, err := r.run(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return ancestryUnknown
	}
	switch exitCode {
	case 0:
		return ancestryYes
	case 1:
		return ancestryNo
	default:
		return ancestryUnknown
	}
}
