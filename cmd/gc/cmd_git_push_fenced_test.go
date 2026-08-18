package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// runGitPushFencedFixture runs a git command in dir and fails the test on
// error. It mirrors the fixture helpers in internal/gitpushfenced's own test
// file; the two packages don't share test helpers across package boundaries,
// so this is a deliberate, minimal, local copy.
func runGitPushFencedFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
}

// gitPushFencedFixture creates a bare "remote" and a clone with one commit
// pushed to main, returning the clone directory (from which tests run `gc
// git-push-fenced`) and the bare directory (the --remote target).
func gitPushFencedFixture(t *testing.T) (clone, bare string) {
	t.Helper()
	bare = t.TempDir()
	runGitPushFencedFixture(t, bare, "init", "--bare", "-b", "main")

	clone = t.TempDir()
	runGitPushFencedFixture(t, clone, "clone", bare, ".")
	runGitPushFencedFixture(t, clone, "config", "user.email", "test@test.com")
	runGitPushFencedFixture(t, clone, "config", "user.name", "Test")
	runGitPushFencedFixture(t, clone, "symbolic-ref", "HEAD", "refs/heads/main")
	runGitPushFencedFixture(t, clone, "commit", "--allow-empty", "-m", "init")
	return clone, bare
}

// TestGitPushFencedExitCodeFirstPush pins the CLI wiring for the ordinary
// success path: a first push to an absent ref exits 0.
func TestGitPushFencedExitCodeFirstPush(t *testing.T) {
	clone, bare := gitPushFencedFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc git-push-fenced = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on success", stderr.String())
	}
	if !strings.Contains(stdout.String(), "pushed") {
		t.Fatalf("stdout = %q, want it to report the pushed outcome", stdout.String())
	}
}

// TestGitPushFencedExitCodeUnresolvableCommitIsTwo pins that a bad
// invocation reaches the process boundary as exit code 2, not 1 — proving
// exitForCode's commandExitError path (not just the errExit sentinel) is
// wired up for this command.
func TestGitPushFencedExitCodeUnresolvableCommitIsTwo(t *testing.T) {
	clone, bare := gitPushFencedFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main", "--commit", "not-a-real-commit-ish"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("gc git-push-fenced = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on failure (human output goes to stderr)", stdout.String())
	}
}

// TestGitPushFencedExitCodeNotAncestorIsOne pins exit code 1 (definitively
// did not happen, safe to retry) for the not-an-ancestor refusal.
func TestGitPushFencedExitCodeNotAncestorIsOne(t *testing.T) {
	clone, bare := gitPushFencedFixture(t)
	runGitPushFencedFixture(t, clone, "push", "origin", "HEAD:refs/heads/main")

	// Diverge: reset to an empty history and commit something unrelated.
	runGitPushFencedFixture(t, clone, "checkout", "--orphan", "diverged")
	runGitPushFencedFixture(t, clone, "commit", "--allow-empty", "-m", "unrelated history")

	var stdout, stderr bytes.Buffer
	code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("gc git-push-fenced = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

// TestGitPushFencedAlreadyLandedIsRepeatSafe pins the CLI-level repeat
// collapse: running the same push twice must exit 0 both times without a
// second write.
func TestGitPushFencedAlreadyLandedIsRepeatSafe(t *testing.T) {
	clone, bare := gitPushFencedFixture(t)

	var first bytes.Buffer
	if code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main"}, &first, &bytes.Buffer{}); code != 0 {
		t.Fatalf("first gc git-push-fenced = %d, want 0", code)
	}

	var second, secondErr bytes.Buffer
	code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main"}, &second, &secondErr)
	if code != 0 {
		t.Fatalf("second gc git-push-fenced = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, second.String(), secondErr.String())
	}
	if !strings.Contains(second.String(), "already_landed") {
		t.Fatalf("second run stdout = %q, want it to report already_landed", second.String())
	}
}

// TestGitPushFencedJSONOutputValidatesAgainstSchema pins that --json output
// (a) is valid JSON, (b) always carries ok:true, and (c) validates against
// the checked-in schemas/git-push-fenced/result.schema.json — this is what
// makes --json usable at all for a hidden command per the framework's
// json_unsupported short-circuit.
func TestGitPushFencedJSONOutputValidatesAgainstSchema(t *testing.T) {
	clone, bare := gitPushFencedFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"git-push-fenced", "--repo", clone, "--remote", bare, "--branch", "main", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc git-push-fenced --json = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	data := bytes.TrimSpace(stdout.Bytes())
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, data)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("payload[\"ok\"] = %v, want true", payload["ok"])
	}
	if payload["outcome"] != "pushed" {
		t.Fatalf("payload[\"outcome\"] = %v, want %q", payload["outcome"], "pushed")
	}

	validateGitPushFencedJSONSchema(t, data)
}

func validateGitPushFencedJSONSchema(t *testing.T, data []byte) {
	t.Helper()
	rawSchema, err := readBuiltinSchema([]string{"git-push-fenced"}, jsonSchemaResultRole)
	if err != nil {
		t.Fatalf("read git-push-fenced schema: %v", err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawSchema))
	if err != nil {
		t.Fatalf("parse git-push-fenced schema: %v", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse git-push-fenced payload: %v\n%s", err, string(data))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("git-push-fenced/result.schema.json", schemaDoc); err != nil {
		t.Fatalf("add git-push-fenced schema: %v", err)
	}
	compiled, err := compiler.Compile("git-push-fenced/result.schema.json")
	if err != nil {
		t.Fatalf("compile git-push-fenced schema: %v", err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("git-push-fenced payload does not validate: %v\n%s", err, string(data))
	}
}
