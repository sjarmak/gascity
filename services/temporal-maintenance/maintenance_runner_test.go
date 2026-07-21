package temporalmaintenance

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An action outside the approved gated vocabulary is refused before any exec.
func TestExecRunner_RejectsUnknownAction(t *testing.T) {
	r := NewExecRunner(t.TempDir())
	_, err := r.Run(context.Background(), ProposedMutation{
		IdempotencyKey: "k", Action: "gh repo delete", Target: "acme/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "vocabulary") {
		t.Fatalf("unknown action err = %v, want vocabulary rejection", err)
	}
}

// The runner builds argv itself, so a caller-supplied Argv can never smuggle an
// unapproved subcommand — the free-form Argv is ignored entirely.
func TestExecRunner_IgnoresCallerArgv(t *testing.T) {
	r := NewExecRunner(t.TempDir())
	_, err := r.Run(context.Background(), ProposedMutation{
		IdempotencyKey: "k", Action: "git -c alias.x=!id x", Target: "1",
		Argv: []string{"git", "-c", "alias.x=!id", "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "vocabulary") {
		t.Fatalf("smuggled action err = %v, want vocabulary rejection", err)
	}
}

// An option-shaped or non-numeric target for a PR action is rejected.
func TestExecRunner_RejectsInvalidTarget(t *testing.T) {
	r := NewExecRunner(t.TempDir())
	for _, target := range []string{"--mirror", "", "1712; rm", "abc"} {
		_, err := r.Run(context.Background(), ProposedMutation{
			IdempotencyKey: "k", Action: "gh pr merge", Target: target,
		})
		if err == nil || !strings.Contains(err.Error(), "invalid target") {
			t.Fatalf("target %q err = %v, want invalid target", target, err)
		}
	}
}

// The binary allowlist is defence-in-depth beneath the vocabulary: even a
// configured action whose builder names a non-allowlisted binary is refused.
func TestExecRunner_AllowlistGuardsBuiltArgv(t *testing.T) {
	r := NewExecRunner(t.TempDir())
	r.Gated = map[string]gatedAction{
		"danger": {build: func(string) []string { return []string{"rm", "-rf", "/"} }, validTarget: func(string) bool { return true }},
	}
	_, err := r.Run(context.Background(), ProposedMutation{IdempotencyKey: "k", Action: "danger"})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("built argv with non-allowlisted binary err = %v, want allowlist rejection", err)
	}
}

// A real allowlisted command executes and returns its output, through a
// configured vocabulary entry. git is on PATH in every dev/CI env for this repo.
func TestExecRunner_RunsAllowlistedCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := NewExecRunner(t.TempDir())
	r.Gated = map[string]gatedAction{
		"git version": {build: func(string) []string { return []string{"git", "version"} }, validTarget: func(string) bool { return true }},
	}
	out, err := r.Run(context.Background(), ProposedMutation{IdempotencyKey: "v", Action: "git version"})
	if err != nil {
		t.Fatalf("git version err = %v", err)
	}
	if !strings.HasPrefix(out, "git version") {
		t.Fatalf("git version out = %q", out)
	}
}

// Selection validates its required inputs before shelling out.
func TestExecRunner_SelectionRequiresParams(t *testing.T) {
	r := NewExecRunner(t.TempDir())
	_, err := r.Run(context.Background(), ProposedMutation{
		IdempotencyKey: "s", Action: ActionSelection, Params: map[string]string{"polecat": "polecat"},
	})
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("selection missing title err = %v, want title required", err)
	}
}

// A selection BodyFile outside the configured scratch root is rejected — defence
// against a future caller pointing --body-file at a secret file.
func TestExecRunner_SelectionConfinesBodyFile(t *testing.T) {
	scratch := t.TempDir()
	r := NewExecRunner(t.TempDir())
	r.ScratchRoot = scratch
	base := ProposedMutation{
		IdempotencyKey: "s", Action: ActionSelection,
		Params: map[string]string{"polecat": "p", "title": "t"},
	}

	// escape attempt
	m := base
	m.BodyFile = "/etc/passwd"
	if _, err := r.Run(context.Background(), m); err == nil || !strings.Contains(err.Error(), "scratch root") {
		t.Fatalf("out-of-root BodyFile err = %v, want scratch-root rejection", err)
	}

	// relative path rejected
	m.BodyFile = "body.md"
	if _, err := r.Run(context.Background(), m); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative BodyFile err = %v, want absolute-path rejection", err)
	}

	// a path inside the scratch root passes validation (then fails later at exec,
	// which is fine — we only assert it got past confinement).
	m.BodyFile = filepath.Join(scratch, "body.md")
	if _, err := r.Run(context.Background(), m); err != nil && strings.Contains(err.Error(), "scratch root") {
		t.Fatalf("in-root BodyFile wrongly rejected: %v", err)
	}
}

func TestLoopCloseMetadata(t *testing.T) {
	if got := loopCloseMetadata("", "pl"); got != "" {
		t.Fatalf("no channel should yield no metadata, got %q", got)
	}
	got := loopCloseMetadata("C0B25SS12CD", "gascity-maintenance-pl")
	for _, want := range []string{`"channel_id":"C0B25SS12CD"`, `"originating_pl_agent":"gascity-maintenance-pl"`, `"loop_close_top_level":"true"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("metadata %q missing %q", got, want)
		}
	}
}
