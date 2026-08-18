package gitpushfenced

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runGit runs a git command in dir and fails the test on error. Mirrors the
// helper in internal/git/git_test.go so fixtures read the same way across
// packages.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
	return string(out)
}

// revParse resolves ref to its full commit oid inside dir.
func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "rev-parse", ref))
}

// initTestRepo creates a git repo with one commit in a temp directory.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

// initBareRemote creates an empty bare repository that acts as the "remote"
// side of a fenced push.
func initBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--bare", "-b", "main")
	return dir
}

// cloneRepo clones src into a fresh temp directory and returns its path.
func cloneRepo(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", src, ".")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	return dir
}

// commitFile adds a file with the given content and commits it, returning
// the new commit's oid.
func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", message)
	return revParse(t, dir, "HEAD")
}

// installFakeGit prepends a scripted "git" onto PATH that can, on request via
// environment variables: race a competing push in ahead of our own push (to
// deterministically reproduce a lease conflict that would otherwise require
// real concurrency), force the exit code of `git push` after letting it run
// for real, force a specific ls-remote invocation (by 1-based call count) to
// fail, or force `git merge-base --is-ancestor` to exit with an arbitrary
// code. Every other invocation, and every invocation where the relevant env
// var is unset, passes straight through to the real git binary. This is how
// the "push exit code lied", "verify readback itself failed", and "remote
// moved between our read and our write" cases get reproduced against a real
// repository instead of asserted by inspection.
func installFakeGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-git shim is POSIX-shell only")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locating real git: %v", err)
	}
	binDir := t.TempDir()
	script := `#!/bin/sh
REAL="$GC_TEST_REAL_GIT"

if [ "$1" = "push" ] && [ -n "${GC_TEST_RACE_PUSH_SRC:-}" ]; then
  "$REAL" -C "$GC_TEST_RACE_PUSH_SRC" push --force "$GC_TEST_RACE_PUSH_REMOTE" "HEAD:$GC_TEST_RACE_PUSH_REF" >/dev/null 2>&1
fi

if [ "$1" = "push" ] && [ -n "${GC_TEST_FORCE_PUSH_EXIT:-}" ]; then
  "$REAL" "$@"
  rc=$?
  if [ "$rc" -eq 0 ]; then
    exit "$GC_TEST_FORCE_PUSH_EXIT"
  fi
  exit "$rc"
fi

if [ "$1" = "ls-remote" ] && [ -n "${GC_TEST_LSREMOTE_FAIL_AT:-}" ]; then
  n=0
  if [ -f "$GC_TEST_LSREMOTE_COUNT_FILE" ]; then n=$(cat "$GC_TEST_LSREMOTE_COUNT_FILE"); fi
  n=$((n + 1))
  echo "$n" > "$GC_TEST_LSREMOTE_COUNT_FILE"
  if [ "$n" = "$GC_TEST_LSREMOTE_FAIL_AT" ]; then
    echo "simulated ls-remote failure" >&2
    exit 1
  fi
fi

if [ "$1" = "merge-base" ] && [ "$2" = "--is-ancestor" ] && [ -n "${GC_TEST_FORCE_ISANCESTOR_EXIT:-}" ]; then
  exit "$GC_TEST_FORCE_ISANCESTOR_EXIT"
fi

exec "$REAL" "$@"
`
	scriptPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake git: %v", err)
	}
	t.Setenv("GC_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func lsRemoteCountFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lsremote-count")
}

// TestPushAlreadyLanded pins the repeat collapse: when the remote ref already
// points at the intended commit, a re-run must be a pure read, not a second
// write. This is the whole reason a retry is safe.
func TestPushAlreadyLanded(t *testing.T) {
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	oid := commitFile(t, clone, "a.txt", "one", "first")
	runGit(t, clone, "push", "origin", "HEAD:refs/heads/main")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeAlreadyLanded {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeAlreadyLanded)
	}
	if result.Intended != oid {
		t.Fatalf("Intended = %q, want %q", result.Intended, oid)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true for an already-landed ref; the repeat collapse must not write")
	}
}

// TestPushRefAbsentFirstPush pins the empty-expect lease form for a brand new
// branch: the remote has no ref yet, so the lease must mean "must not already
// exist", and the push must land.
func TestPushRefAbsentFirstPush(t *testing.T) {
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	// The bare remote has no refs at all yet: `git init --bare -b main` sets
	// the default branch symref but creates no commits, so refs/heads/main
	// does not exist until something is pushed to it. Pin the local branch
	// name explicitly rather than relying on how a given git version infers
	// a default branch when cloning an empty repository.
	runGit(t, clone, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, clone, "commit", "--allow-empty", "-m", "local only")
	oid := revParse(t, clone, "HEAD")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomePushed {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomePushed)
	}
	if got := revParse(t, bare, "refs/heads/main"); got != oid {
		t.Fatalf("remote refs/heads/main = %q, want %q", got, oid)
	}
}

// TestPushSucceedsFastForward pins the ordinary case: the remote ref exists
// and is an ancestor of the intended commit, so the lease is pinned to the
// observed oid and the push lands.
func TestPushSucceedsFastForward(t *testing.T) {
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	base := commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	oid := commitFile(t, clone, "next.txt", "next", "next")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomePushed {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomePushed)
	}
	if result.ObservedBefore != base {
		t.Fatalf("ObservedBefore = %q, want %q", result.ObservedBefore, base)
	}
	if got := revParse(t, bare, "refs/heads/main"); got != oid {
		t.Fatalf("remote refs/heads/main = %q, want %q", got, oid)
	}
}

// TestPushLandedDespiteNonZeroExit is the single most important case: `git
// push` exits non-zero (simulating a dropped connection after the remote
// applied the update), but the verify readback shows the intended commit DID
// land. The old bare-retry-loop bug treated the non-zero exit as proof the
// push failed and pushed again; this must report success and must not
// attempt a second push.
func TestPushLandedDespiteNonZeroExit(t *testing.T) {
	installFakeGit(t)
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	oid := commitFile(t, clone, "a.txt", "one", "first")

	t.Setenv("GC_TEST_FORCE_PUSH_EXIT", "1")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeLandedDespiteNonZero {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeLandedDespiteNonZero)
	}
	if !result.PushAttempted {
		t.Fatal("PushAttempted = false, want true")
	}
	if result.PushExitCode != 1 {
		t.Fatalf("PushExitCode = %d, want 1 (the misleading exit code is still reported as a hint)", result.PushExitCode)
	}
	if got := revParse(t, bare, "refs/heads/main"); got != oid {
		t.Fatalf("remote refs/heads/main = %q, want %q (the push actually landed)", got, oid)
	}
}

// TestPushVerifyReadbackFailsAfterPush pins that a failed post-push readback
// is UNKNOWN (exit 2), never assumed-failed (exit 1). Assuming failure here
// is exactly what would cause a caller to retry a push that may have already
// landed.
func TestPushVerifyReadbackFailsAfterPush(t *testing.T) {
	installFakeGit(t)
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	commitFile(t, clone, "a.txt", "one", "first")

	t.Setenv("GC_TEST_LSREMOTE_FAIL_AT", "2")
	t.Setenv("GC_TEST_LSREMOTE_COUNT_FILE", lsRemoteCountFile(t))

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeVerifyReadFailed {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeVerifyReadFailed)
	}
	if !result.PushAttempted {
		t.Fatal("PushAttempted = false, want true (the push itself ran; only the verify read failed)")
	}
}

// TestPushPrePushReadbackFails pins that an unreadable remote is UNKNOWN, not
// "absent" — treating a failed lookup as absent is exactly what turns a
// fenced push back into a blind one.
func TestPushPrePushReadbackFails(t *testing.T) {
	repo := initTestRepo(t)

	result := Push(context.Background(), Options{
		RepoDir: repo,
		Remote:  filepath.Join(t.TempDir(), "does-not-exist"),
		Branch:  "main",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomePrePushReadFailed {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomePrePushReadFailed)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; an unreadable ref must refuse before any write")
	}
}

// TestPushRemoteAheadNotAncestor pins the "would-discard" case: the remote
// ref is not an ancestor of the intended commit and --allow-non-ff was not
// passed, so the push must refuse (exit 1, safe to retry with a fresh
// fetch+rebase) rather than force-discard remote history.
func TestPushRemoteAheadNotAncestor(t *testing.T) {
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")
	// A second, divergent commit on the remote that our clone never fetches.
	remoteOnly := commitFile(t, seed, "remote-only.txt", "remote", "remote-only")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	// Diverge locally from the ORIGINAL base commit, before remote-only.txt,
	// by resetting to the parent and committing something else.
	runGit(t, clone, "reset", "--hard", "HEAD~1")
	commitFile(t, clone, "local-only.txt", "local", "local-only")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeNotAncestor {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeNotAncestor)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; a non-ancestor remote must refuse before any write")
	}
	if got := revParse(t, bare, "refs/heads/main"); got != remoteOnly {
		t.Fatalf("remote refs/heads/main moved to %q, want unchanged %q", got, remoteOnly)
	}
}

// TestPushRemoteAheadAncestorUnreadableLocally pins the case where the
// remote's observed commit is not present in the local object database at
// all: ancestry cannot be answered either way, so this must be UNKNOWN (exit
// 2), not folded into "not an ancestor" (exit 1).
func TestPushRemoteAheadAncestorUnreadableLocally(t *testing.T) {
	bare := initBareRemote(t)

	// Populate the remote from a repo our test clone never fetches from.
	stranger := initTestRepo(t)
	runGit(t, stranger, "remote", "add", "origin", bare)
	strangerOid := commitFile(t, stranger, "stranger.txt", "stranger", "stranger commit")
	runGit(t, stranger, "push", "origin", "HEAD:refs/heads/main")

	// A wholly unrelated local repo with its own unconnected history.
	clone := initTestRepo(t)
	commitFile(t, clone, "local.txt", "local", "local commit")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeAncestryUnreadableLocally {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeAncestryUnreadableLocally)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; unreadable ancestry must refuse before any write")
	}
	if result.ObservedBefore != strangerOid {
		t.Fatalf("ObservedBefore = %q, want %q", result.ObservedBefore, strangerOid)
	}
}

// TestPushAncestryCheckErrorsIsUnknownNotDiscard pins that only is-ancestor
// exit code 1 specifically means "definitively not an ancestor". Any other
// non-zero exit is an error, not an answer, and must map to unknown (exit 2)
// rather than being folded into "would discard" (exit 1).
func TestPushAncestryCheckErrorsIsUnknownNotDiscard(t *testing.T) {
	installFakeGit(t)
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	commitFile(t, clone, "next.txt", "next", "next")

	t.Setenv("GC_TEST_FORCE_ISANCESTOR_EXIT", "129")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeAncestryUnknown {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeAncestryUnknown)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; an inconclusive ancestry check must refuse before any write")
	}
}

// TestPushCreateOnlyRefusesExistingRef pins --create-only: it must refuse
// (exit 1) the moment the remote ref exists at all, even at a commit the
// local repo has and could otherwise fast-forward past.
func TestPushCreateOnlyRefusesExistingRef(t *testing.T) {
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	base := commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	commitFile(t, clone, "next.txt", "next", "next")

	result := Push(context.Background(), Options{
		RepoDir:    clone,
		Remote:     bare,
		Branch:     "main",
		CreateOnly: true,
	})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeCreateOnlyExists {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeCreateOnlyExists)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; --create-only must refuse before any write")
	}
	if got := revParse(t, bare, "refs/heads/main"); got != base {
		t.Fatalf("remote refs/heads/main moved to %q, want unchanged %q", got, base)
	}
}

// TestPushRejectedWhenVerifyShowsNoLanding pins the definitive-failure path:
// the remote ref moves between our pre-push read and our actual push
// command (a genuine race, reproduced deterministically via the fake-git
// race hook rather than real concurrency), so the pinned lease is refused
// and the verify readback confirms the intended commit did not land. This
// must be a clean exit 1, safe to retry.
func TestPushRejectedWhenVerifyShowsNoLanding(t *testing.T) {
	installFakeGit(t)
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	base := commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	commitFile(t, clone, "mine.txt", "mine", "mine")

	// Committed locally but deliberately NOT pushed yet: the fake-git race
	// hook pushes this in from underneath us, between our own pre-push read
	// (which still observes base) and our own push command.
	competitor := cloneRepo(t, bare)
	competitorOid := commitFile(t, competitor, "theirs.txt", "theirs", "theirs")

	t.Setenv("GC_TEST_RACE_PUSH_SRC", competitor)
	t.Setenv("GC_TEST_RACE_PUSH_REMOTE", bare)
	t.Setenv("GC_TEST_RACE_PUSH_REF", "refs/heads/main")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
	})

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeRejected {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeRejected)
	}
	if !result.PushAttempted {
		t.Fatal("PushAttempted = false, want true")
	}
	if result.ObservedBefore != base {
		t.Fatalf("ObservedBefore = %q, want %q", result.ObservedBefore, base)
	}
	if result.ObservedAfter != competitorOid {
		t.Fatalf("ObservedAfter = %q, want %q", result.ObservedAfter, competitorOid)
	}
	if got := revParse(t, bare, "refs/heads/main"); got != competitorOid {
		t.Fatalf("remote refs/heads/main = %q, want unchanged %q (rejected push must not move it)", got, competitorOid)
	}
}

// TestPushDryRunDoesNotPush pins --dry-run: it must report success without
// running git push at all.
func TestPushDryRunDoesNotPush(t *testing.T) {
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	commitFile(t, clone, "a.txt", "one", "first")

	result := Push(context.Background(), Options{
		RepoDir: clone,
		Remote:  bare,
		Branch:  "main",
		DryRun:  true,
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeDryRun {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeDryRun)
	}
	if result.PushAttempted {
		t.Fatal("PushAttempted = true; --dry-run must not push")
	}
	if _, err := exec.Command("git", "-C", bare, "rev-parse", "refs/heads/main").CombinedOutput(); err == nil {
		t.Fatal("refs/heads/main exists on the remote after --dry-run; nothing should have been pushed")
	}
}

// TestPushAllowNonFFSkipsAncestryCheck pins --allow-non-ff: it must rewrite a
// remote ref that is not an ancestor of the intended commit instead of
// refusing.
func TestPushAllowNonFFSkipsAncestryCheck(t *testing.T) {
	bare := initBareRemote(t)

	seed := cloneRepo(t, bare)
	commitFile(t, seed, "base.txt", "base", "base")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")
	remoteOid := commitFile(t, seed, "remote-only.txt", "remote", "remote-only")
	runGit(t, seed, "push", "origin", "HEAD:refs/heads/main")

	clone := cloneRepo(t, bare)
	runGit(t, clone, "reset", "--hard", "HEAD~1")
	oid := commitFile(t, clone, "local-only.txt", "local", "local-only")

	result := Push(context.Background(), Options{
		RepoDir:    clone,
		Remote:     bare,
		Branch:     "main",
		AllowNonFF: true,
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomePushed {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomePushed)
	}
	if got := revParse(t, bare, "refs/heads/main"); got != oid {
		t.Fatalf("remote refs/heads/main = %q, want %q (--allow-non-ff must rewrite it)", got, oid)
	}
	_ = remoteOid
}

// TestPushInvalidCommitIsInvocationError pins that an unresolvable commit-ish
// is a bad-invocation error (exit 2), not a push outcome.
func TestPushInvalidCommitIsInvocationError(t *testing.T) {
	repo := initTestRepo(t)

	result := Push(context.Background(), Options{
		RepoDir: repo,
		Remote:  "origin",
		Branch:  "main",
		Commit:  "not-a-real-commit-ish",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeInvalidInvocation {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeInvalidInvocation)
	}
}

// TestPushMissingBranchIsInvocationError pins that an empty Branch and
// RemoteBranch is a bad invocation, not silently treated as some default ref.
func TestPushMissingBranchIsInvocationError(t *testing.T) {
	repo := initTestRepo(t)

	result := Push(context.Background(), Options{
		RepoDir: repo,
		Remote:  "origin",
	})

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; note=%q", result.ExitCode, result.Note)
	}
	if result.Outcome != OutcomeInvalidInvocation {
		t.Fatalf("Outcome = %q, want %q", result.Outcome, OutcomeInvalidInvocation)
	}
}

// TestPushRemoteBranchOverridesBranch pins that --remote-branch takes
// precedence over --branch when both are set, matching the reference
// script's REMOTE_BRANCH:-BRANCH fallback.
func TestPushRemoteBranchOverridesBranch(t *testing.T) {
	bare := initBareRemote(t)
	clone := cloneRepo(t, bare)
	commitFile(t, clone, "a.txt", "one", "first")

	result := Push(context.Background(), Options{
		RepoDir:      clone,
		Remote:       bare,
		Branch:       "wrong-branch",
		RemoteBranch: "main",
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; note=%q", result.ExitCode, result.Note)
	}
	if result.Ref != "refs/heads/main" {
		t.Fatalf("Ref = %q, want refs/heads/main", result.Ref)
	}
	if _, err := exec.Command("git", "-C", bare, "rev-parse", "refs/heads/wrong-branch").CombinedOutput(); err == nil {
		t.Fatal("refs/heads/wrong-branch exists on the remote; RemoteBranch should have overridden Branch")
	}
}

// TestOutcomeExitCodesMatchContract is a table-driven guard against silently
// widening the exit-code contract: 0/1/2 are the only values Push may return,
// and each Outcome is pinned to exactly one of them.
func TestOutcomeExitCodesMatchContract(t *testing.T) {
	cases := []struct {
		outcome Outcome
		exit    int
	}{
		{OutcomeAlreadyLanded, 0},
		{OutcomePushed, 0},
		{OutcomeLandedDespiteNonZero, 0},
		{OutcomeDryRun, 0},
		{OutcomeCreateOnlyExists, 1},
		{OutcomeNotAncestor, 1},
		{OutcomeRejected, 1},
		{OutcomePrePushReadFailed, 2},
		{OutcomeVerifyReadFailed, 2},
		{OutcomeAncestryUnknown, 2},
		{OutcomeAncestryUnreadableLocally, 2},
		{OutcomeInvalidInvocation, 2},
	}
	for _, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			got := ExitCodeForOutcome(tc.outcome)
			if got != tc.exit {
				t.Fatalf("ExitCodeForOutcome(%q) = %d, want %d", tc.outcome, got, tc.exit)
			}
			if got < 0 || got > 2 {
				t.Fatalf("ExitCodeForOutcome(%q) = %d, out of the 0/1/2 contract", tc.outcome, got)
			}
		})
	}
}

func TestMain(m *testing.M) {
	// Keep git subprocesses hermetic against the host's global/user config
	// (which may set unrelated aliases, hooks, or signing requirements) for
	// every test in this package, not just the ones using installFakeGit.
	if err := os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(os.TempDir(), "gc-test-empty-gitconfig")); err != nil {
		fmt.Fprintln(os.Stderr, "gitpushfenced tests: setenv GIT_CONFIG_GLOBAL:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
