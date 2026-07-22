package temporalmaintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBinDir writes fake `gc` and `gc-sling` scripts that log their argv, and
// prepends the dir to PATH for the test. gcListOut controls the in-flight guard's
// `gc bd list --json` output; while a `dolt-down` flag file exists next to the
// log, the list call fails like a dolt circuit-breaker-open outage instead.
func fakeBinDir(t *testing.T, gcListOut string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	downFlag := filepath.Join(dir, "dolt-down")

	gc := "#!/usr/bin/env bash\n" +
		"echo \"gc $*\" >> \"" + logPath + "\"\n" +
		"case \"$*\" in\n" +
		"  *\" list \"*)\n" +
		"    if [ -e \"" + downFlag + "\" ]; then echo 'dolt circuit breaker is open: server appears down (cooldown 5s)' >&2; exit 1; fi\n" +
		"    printf '%s' '" + gcListOut + "' ;;\n" +
		"  *\" create \"*) echo 'created gc-fake1' ;;\n" +
		"esac\n"
	slng := "#!/usr/bin/env bash\necho \"gc-sling $*\" >> \"" + logPath + "\"\n"

	for name, body := range map[string]string{"gc": gc, "gc-sling": slng} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func selectionMutation(t *testing.T) (ProposedMutation, string) {
	t.Helper()
	scratch := t.TempDir()
	body := filepath.Join(scratch, "prompt.md")
	if err := os.WriteFile(body, []byte("do a review pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ProposedMutation{
		IdempotencyKey: "k", Action: ActionSelection, BodyFile: body,
		Params: map[string]string{
			"branch": "review", "polecat": "/pool/polecat", "title": "maintenance-cycle review",
			"priority": "2", "labels": "maintenance-cycle,maintenance-cycle:review,rig:gascity",
			"half_label": "maintenance-cycle:review", "slack_channel": "C0B25SS12CD", "pl_agent": "gascity-maintenance-pl",
		},
	}, scratch
}

// Selection end-to-end (fake gc/gc-sling), through Preflight then Run the way
// RealAdapter orchestrates them: guard passes -> create with --metadata +
// --body-file + labels -> sling -> returns the real bead id.
func TestRunSelection_EndToEnd_CreatesAndSlings(t *testing.T) {
	logPath := fakeBinDir(t, "[]") // nothing open -> guard passes
	m, scratch := selectionMutation(t)
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

	skip, err := r.Preflight(context.Background(), m)
	if err != nil || skip {
		t.Fatalf("Preflight = (skip=%v, err=%v), want pass", skip, err)
	}
	bead, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("runSelection err = %v", err)
	}
	if bead != "gc-fake1" {
		t.Fatalf("bead = %q, want gc-fake1", bead)
	}
	log, _ := os.ReadFile(logPath)
	calls := string(log)
	for _, want := range []string{
		"list --status open --label maintenance-cycle:review --json", // in-flight guard
		"create maintenance-cycle review",                            // create
		"--metadata",                                                 // loop-close metadata
		"--body-file",                                                // prompt
		"gc-sling /pool/polecat gc-fake1 --no-formula --nudge",       // sling
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("call log missing %q\n---\n%s", want, calls)
		}
	}
}

// When a same-half bead is already open, Preflight skips: no create, no sling.
func TestPreflight_InFlightGuardSkips(t *testing.T) {
	logPath := fakeBinDir(t, `[{"id":"gc-open"}]`) // one open -> guard fires
	m, scratch := selectionMutation(t)
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

	skip, err := r.Preflight(context.Background(), m)
	if err != nil {
		t.Fatalf("Preflight err = %v", err)
	}
	if !skip {
		t.Fatal("Preflight skip = false, want true with an open same-half bead")
	}
	log, _ := os.ReadFile(logPath)
	calls := string(log)
	if strings.Contains(calls, " create ") || strings.Contains(calls, "gc-sling") {
		t.Fatalf("guard skip must not create or sling; got:\n%s", calls)
	}
}

// A transient backend outage during the in-flight read surfaces as an error
// from Preflight — before any claim, so nothing is stamped and the caller
// retries — and no mutation command runs.
func TestPreflight_TransientListErrorSurfaces(t *testing.T) {
	logPath := fakeBinDir(t, "[]")
	if err := os.WriteFile(filepath.Join(filepath.Dir(logPath), "dolt-down"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, scratch := selectionMutation(t)
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

	skip, err := r.Preflight(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "circuit breaker") {
		t.Fatalf("Preflight during outage = (skip=%v, err=%v), want the backend error surfaced", skip, err)
	}
	log, _ := os.ReadFile(logPath)
	calls := string(log)
	if strings.Contains(calls, " create ") || strings.Contains(calls, "gc-sling") {
		t.Fatalf("a failed preflight must not create or sling; got:\n%s", calls)
	}
}

// Validation beats the guard: a permanent configuration defect (missing title)
// surfaces as a PermanentPreflightError even when an open same-half bead would
// otherwise skip the cycle — a config error must never be masked as a skip.
// The guard read never runs.
func TestPreflight_ValidationBeatsInFlightSkip(t *testing.T) {
	logPath := fakeBinDir(t, `[{"id":"gc-open"}]`) // guard WOULD skip
	m, scratch := selectionMutation(t)
	delete(m.Params, "title") // permanent config defect
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

	skip, err := r.Preflight(context.Background(), m)
	if skip {
		t.Fatal("Preflight skipped despite a permanent validation error")
	}
	var perm *PermanentPreflightError
	if !errors.As(err, &perm) {
		t.Fatalf("Preflight err = %v, want PermanentPreflightError", err)
	}
	if log, _ := os.ReadFile(logPath); len(log) != 0 {
		t.Fatalf("validation failure must precede the guard read; got:\n%s", log)
	}
}

// Non-selection actions and valid selections without a half label have no
// guard to run: no exec at all, never a skip.
func TestPreflight_NoGuardConfigured(t *testing.T) {
	logPath := fakeBinDir(t, "[]")
	r := NewExecRunner("/tmp")

	for _, m := range []ProposedMutation{
		{IdempotencyKey: "k", Action: "gh pr merge", Target: "1712"},
		{IdempotencyKey: "k", Action: ActionSelection, BodyFile: "/abs/prompt.md",
			Params: map[string]string{"polecat": "p", "title": "t"}},
	} {
		skip, err := r.Preflight(context.Background(), m)
		if err != nil || skip {
			t.Fatalf("Preflight(%q) = (skip=%v, err=%v), want no-op pass", m.Action, skip, err)
		}
	}
	if log, _ := os.ReadFile(logPath); len(log) != 0 {
		t.Fatalf("no-guard preflight must not exec anything; got:\n%s", log)
	}
}
