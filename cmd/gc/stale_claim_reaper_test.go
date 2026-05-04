package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/citylayout"
)

// TestStaleClaimReaperScriptSyntax checks that the materialized script
// parses as bash. Caught build errors at the script level before runtime.
func TestStaleClaimReaperScriptSyntax(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeBuiltinPacks(dir); err != nil {
		t.Fatalf("MaterializeBuiltinPacks() error: %v", err)
	}
	script := filepath.Join(dir, citylayout.SystemPacksRoot, "maintenance", "assets", "scripts", "stale-claim-reaper.sh")
	cmd := exec.Command("bash", "-n", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
}

// TestStaleClaimReaperDormantWithoutPolicy verifies that when a rig has no
// .beads/stale-claim-policy.yaml, the reaper skips the rig silently (no
// audit log entry, exit 0).
func TestStaleClaimReaperDormantWithoutPolicy(t *testing.T) {
	skipSlowCmdGCTest(t, "stale-claim-reaper invokes shell tools")
	cityDir := t.TempDir()
	if err := MaterializeBuiltinPacks(cityDir); err != nil {
		t.Fatalf("MaterializeBuiltinPacks() error: %v", err)
	}

	rigDir := filepath.Join(cityDir, "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	// Make it a git repo so the rig path check passes if the script tries
	// to walk it; the rig should be skipped before any git work.
	if out, err := exec.Command("git", "-C", rigDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	binDir := setupReaperShimBin(t, rigShim{path: rigDir}, []shimBead{})

	script := filepath.Join(cityDir, citylayout.SystemPacksRoot, "maintenance", "assets", "scripts", "stale-claim-reaper.sh")
	stateDir := filepath.Join(cityDir, ".gc", "runtime", "packs", "maintenance")
	cmd := exec.Command("bash", script)
	cmd.Env = sanitizedBaseEnv(
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GC_CITY="+cityDir,
		"GC_PACK_STATE_DIR="+stateDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	auditPath := filepath.Join(stateDir, "stale-claim-audit.jsonl")
	if _, err := os.Stat(auditPath); err == nil {
		// File may exist but should contain no reap-relevant line for a
		// dormant rig. Reading instead of statting to allow scan markers.
		data, _ := os.ReadFile(auditPath)
		if bytes.Contains(data, []byte(`"action":"reap-`)) {
			t.Fatalf("audit log contains reap action while dormant:\n%s", data)
		}
	}
}

// TestStaleClaimReaperDryRunDoesNotMutate seeds a stale in_progress bead and
// verifies the reaper records a dry-run audit line WITHOUT issuing
// `bd update`.
func TestStaleClaimReaperDryRunDoesNotMutate(t *testing.T) {
	skipSlowCmdGCTest(t, "stale-claim-reaper invokes shell tools")
	cityDir := t.TempDir()
	if err := MaterializeBuiltinPacks(cityDir); err != nil {
		t.Fatalf("MaterializeBuiltinPacks() error: %v", err)
	}

	rigDir := filepath.Join(cityDir, "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	if out, err := exec.Command("git", "-C", rigDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// Drop a policy file so the rig is in-scope.
	beadsDir := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	policy := "stale_threshold: \"1h\"\nexclude_metadata: long_running\nmatch_commit_pattern: \"{bead_id}\"\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "stale-claim-policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	// Seed a stale in_progress bead via the bd shim.
	stale := shimBead{
		ID:        "rig-a-stale1",
		Status:    "in_progress",
		Assignee:  "worker-1",
		UpdatedAt: "2020-01-01T00:00:00Z",
	}
	binDir := setupReaperShimBin(t, rigShim{path: rigDir}, []shimBead{stale})

	script := filepath.Join(cityDir, citylayout.SystemPacksRoot, "maintenance", "assets", "scripts", "stale-claim-reaper.sh")
	stateDir := filepath.Join(cityDir, ".gc", "runtime", "packs", "maintenance")
	cmd := exec.Command("bash", script)
	cmd.Env = sanitizedBaseEnv(
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GC_CITY="+cityDir,
		"GC_PACK_STATE_DIR="+stateDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	// Verify NO bd update was issued.
	bdLog := filepath.Join(binDir, "bd-calls.log")
	if data, err := os.ReadFile(bdLog); err == nil {
		if strings.Contains(string(data), "update") && strings.Contains(string(data), "--status=open") {
			t.Fatalf("bd update was called in dry-run mode:\n%s", data)
		}
	}

	// Verify audit log has a reap-dry-run entry.
	auditPath := filepath.Join(stateDir, "stale-claim-audit.jsonl")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit log missing: %v", err)
	}
	if !strings.Contains(string(data), `"action":"reap-dry-run"`) {
		t.Fatalf("expected reap-dry-run audit entry, got:\n%s", data)
	}
	if !strings.Contains(string(data), `"bead_id":"rig-a-stale1"`) {
		t.Fatalf("expected bead_id in audit entry, got:\n%s", data)
	}
	// Validate it parses as JSON.
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("audit line not valid JSON: %q: %v", line, err)
		}
	}
}

// TestStaleClaimReaperApplyIssuesUpdate seeds a stale bead and verifies that
// with STALE_CLAIM_REAPER_APPLY=1 the script invokes `bd update --status=open
// --assignee= <id>` and records reap-applied in the audit log.
func TestStaleClaimReaperApplyIssuesUpdate(t *testing.T) {
	skipSlowCmdGCTest(t, "stale-claim-reaper invokes shell tools")
	cityDir := t.TempDir()
	if err := MaterializeBuiltinPacks(cityDir); err != nil {
		t.Fatalf("MaterializeBuiltinPacks() error: %v", err)
	}

	rigDir := filepath.Join(cityDir, "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	if out, err := exec.Command("git", "-C", rigDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	beadsDir := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	policy := "stale_threshold: \"1h\"\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "stale-claim-policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	stale := shimBead{
		ID:        "rig-a-stale2",
		Status:    "in_progress",
		Assignee:  "worker-3",
		UpdatedAt: "2020-01-01T00:00:00Z",
	}
	binDir := setupReaperShimBin(t, rigShim{path: rigDir}, []shimBead{stale})

	script := filepath.Join(cityDir, citylayout.SystemPacksRoot, "maintenance", "assets", "scripts", "stale-claim-reaper.sh")
	stateDir := filepath.Join(cityDir, ".gc", "runtime", "packs", "maintenance")
	cmd := exec.Command("bash", script)
	cmd.Env = sanitizedBaseEnv(
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GC_CITY="+cityDir,
		"GC_PACK_STATE_DIR="+stateDir,
		"STALE_CLAIM_REAPER_APPLY=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	bdLog := filepath.Join(binDir, "bd-calls.log")
	data, err := os.ReadFile(bdLog)
	if err != nil {
		t.Fatalf("bd-calls.log missing: %v", err)
	}
	logStr := string(data)
	if !strings.Contains(logStr, "update rig-a-stale2") {
		t.Fatalf("bd update was not called for stale bead:\n%s", logStr)
	}
	if !strings.Contains(logStr, "--status=open") || !strings.Contains(logStr, "--assignee=") {
		t.Fatalf("bd update missing required flags:\n%s", logStr)
	}

	auditPath := filepath.Join(stateDir, "stale-claim-audit.jsonl")
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit log missing: %v", err)
	}
	if !strings.Contains(string(auditData), `"action":"reap-applied"`) {
		t.Fatalf("expected reap-applied audit entry, got:\n%s", auditData)
	}
}

// TestStaleClaimReaperSkipsRecentCommit verifies that when git log finds a
// commit mentioning the bead ID since the claim, the bead is NOT reaped.
func TestStaleClaimReaperSkipsRecentCommit(t *testing.T) {
	skipSlowCmdGCTest(t, "stale-claim-reaper invokes shell tools")
	cityDir := t.TempDir()
	if err := MaterializeBuiltinPacks(cityDir); err != nil {
		t.Fatalf("MaterializeBuiltinPacks() error: %v", err)
	}

	rigDir := filepath.Join(cityDir, "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "fix(stuff): work on rig-a-busy1 in progress"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", rigDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	beadsDir := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "stale-claim-policy.yaml"), []byte("stale_threshold: \"1h\"\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	// Bead is "stale" (old updated_at) but a commit mentions it after the
	// claim — should be skipped.
	bead := shimBead{
		ID:        "rig-a-busy1",
		Status:    "in_progress",
		Assignee:  "worker-2",
		UpdatedAt: "2020-01-01T00:00:00Z",
	}
	binDir := setupReaperShimBin(t, rigShim{path: rigDir, useRealGit: true}, []shimBead{bead})

	script := filepath.Join(cityDir, citylayout.SystemPacksRoot, "maintenance", "assets", "scripts", "stale-claim-reaper.sh")
	stateDir := filepath.Join(cityDir, ".gc", "runtime", "packs", "maintenance")
	cmd := exec.Command("bash", script)
	cmd.Env = sanitizedBaseEnv(
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GC_CITY="+cityDir,
		"GC_PACK_STATE_DIR="+stateDir,
		"STALE_CLAIM_REAPER_APPLY=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	bdLog := filepath.Join(binDir, "bd-calls.log")
	if data, err := os.ReadFile(bdLog); err == nil {
		if strings.Contains(string(data), "update rig-a-busy1") {
			t.Fatalf("bd update was called even though commit mentions the bead:\n%s", data)
		}
	}
	auditPath := filepath.Join(stateDir, "stale-claim-audit.jsonl")
	auditData, _ := os.ReadFile(auditPath)
	if !strings.Contains(string(auditData), `"action":"reap-skipped-recent-commit"`) {
		t.Fatalf("expected reap-skipped-recent-commit audit entry, got:\n%s", auditData)
	}
}

// shimBead is the JSON shape written by the bd shim.
type shimBead struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Assignee  string `json:"assignee"`
	UpdatedAt string `json:"updated_at"`
}

type rigShim struct {
	path       string
	useRealGit bool // if true, leave git alone (use system git on PATH)
}

// setupReaperShimBin creates a directory with `bd`, `gc`, and (optionally)
// `git` shims for use as PATH. Returns the bin dir.
func setupReaperShimBin(t *testing.T, rig rigShim, beads []shimBead) string {
	t.Helper()
	binDir := t.TempDir()

	beadsJSON, err := json.Marshal(beads)
	if err != nil {
		t.Fatalf("marshal beads: %v", err)
	}
	rigsJSON, err := json.Marshal([]map[string]string{{"path": rig.path}})
	if err != nil {
		t.Fatalf("marshal rigs: %v", err)
	}

	bdLog := filepath.Join(binDir, "bd-calls.log")

	bdShim := `#!/usr/bin/env bash
echo "$@" >> "` + bdLog + `"
case "$1" in
  list)
    # If --metadata-field <key>=true (or --metadata-field=<key>=true) is
    # present, return an empty result so the script's exclude path fires.
    prev=""
    for arg in "$@"; do
      case "$arg" in
        --metadata-field=*=true)
          echo "[]"
          exit 0
          ;;
      esac
      if [ "$prev" = "--metadata-field" ]; then
        case "$arg" in
          *=true)
            echo "[]"
            exit 0
            ;;
        esac
      fi
      prev="$arg"
    done
    cat <<'BEADJSON'
` + string(beadsJSON) + `
BEADJSON
    ;;
  update)
    # Mutation acknowledged.
    echo "ok"
    ;;
  *)
    echo "[]"
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(bdShim), 0o755); err != nil {
		t.Fatalf("write bd shim: %v", err)
	}

	gcShim := `#!/usr/bin/env bash
case "$1 $2" in
  "rig list")
    cat <<'RIGSJSON'
` + string(rigsJSON) + `
RIGSJSON
    ;;
  *)
    echo "[]"
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "gc"), []byte(gcShim), 0o755); err != nil {
		t.Fatalf("write gc shim: %v", err)
	}

	// Provide a `git` shim only when caller explicitly asks for the no-commit
	// case (default). When useRealGit is true, the test wants real git on
	// PATH so callers can actually create commits in the rig fixture.
	if !rig.useRealGit {
		gitShim := `#!/usr/bin/env bash
# git shim: only handles the queries the reaper makes. For 'log --grep',
# always print nothing (no commits found). For everything else, exec real git.
for ((i=1; i<=$#; i++)); do
  if [ "${!i}" = "log" ]; then
    echo ""
    exit 0
  fi
done
exec /usr/bin/git "$@"
`
		if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(gitShim), 0o755); err != nil {
			t.Fatalf("write git shim: %v", err)
		}
	}

	return binDir
}
