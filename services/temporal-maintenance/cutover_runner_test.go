package temporalmaintenance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBinDir writes fake `gc` and `gc-sling` scripts that log their argv, and
// prepends the dir to PATH for the test. gcListOut controls the in-flight guard's
// `gc bd list --json` output.
func fakeBinDir(t *testing.T, gcListOut string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")

	gc := "#!/usr/bin/env bash\n" +
		"echo \"gc $*\" >> \"" + logPath + "\"\n" +
		"case \"$*\" in\n" +
		"  *\" list \"*) printf '%s' '" + gcListOut + "' ;;\n" +
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

// runSelection end-to-end (fake gc/gc-sling): guard passes -> create with
// --metadata + --body-file + labels -> sling -> returns the real bead id.
func TestRunSelection_EndToEnd_CreatesAndSlings(t *testing.T) {
	logPath := fakeBinDir(t, "[]") // nothing open -> guard passes
	m, scratch := selectionMutation(t)
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

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

// When a same-half bead is already open, the guard skips: no create, no sling.
func TestRunSelection_InFlightGuardSkips(t *testing.T) {
	logPath := fakeBinDir(t, `[{"id":"gc-open"}]`) // one open -> guard fires
	m, scratch := selectionMutation(t)
	r := NewExecRunner("/tmp")
	r.ScratchRoot = scratch

	bead, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("runSelection err = %v", err)
	}
	if bead != "skipped-inflight" {
		t.Fatalf("bead = %q, want skipped-inflight", bead)
	}
	log, _ := os.ReadFile(logPath)
	calls := string(log)
	if strings.Contains(calls, " create ") || strings.Contains(calls, "gc-sling") {
		t.Fatalf("guard skip must not create or sling; got:\n%s", calls)
	}
}
