package main

import (
	"bytes"
	"strings"
	"testing"
)

// execSlackMapChannelCmd executes the verb directly against a temp city.
func execSlackMapChannelCmd(t *testing.T, cityRoot string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cityRoot)
	var stdout, stderr bytes.Buffer
	cmd := newSlackMapChannelCmd(&stdout)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSlackMapChannelHappyPathRig(t *testing.T) {
	cityRoot := newTestCity(t)

	stdout, stderr, err := execSlackMapChannelCmd(t, cityRoot,
		"C0123", "--workspace-id", "T123", "--rig", "alpha",
	)
	if err != nil {
		t.Fatalf("map-channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "C0123") || !strings.Contains(stdout, "alpha") {
		t.Errorf("stdout should mention channel and rig: %q", stdout)
	}

	reg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := reg.Get("T123", "C0123")
	if !ok {
		t.Fatal("registry missing record after map-channel")
	}
	if rec.TargetKind != "rig" || rec.TargetID != "alpha" {
		t.Errorf("record mismatch: %+v", rec)
	}
}

func TestSlackMapChannelHappyPathSession(t *testing.T) {
	cityRoot := newTestCity(t)
	_, stderr, err := execSlackMapChannelCmd(t, cityRoot,
		"C9", "--workspace-id", "T1", "--session", "gc-2568",
	)
	if err != nil {
		t.Fatalf("map-channel --session: %v\nstderr=%s", err, stderr)
	}
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "C9")
	if !ok {
		t.Fatal("missing record")
	}
	if rec.TargetKind != "session" || rec.TargetID != "gc-2568" {
		t.Errorf("record mismatch: %+v", rec)
	}
}

func TestSlackMapChannelMissingWorkspaceID(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "")
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--rig", "alpha",
	)
	if err == nil {
		t.Fatal("expected error for missing --workspace-id")
	}
}

func TestSlackMapChannelMutuallyExclusiveTargets(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "alpha", "--session", "gc-1",
	)
	if err == nil {
		t.Fatal("expected error for both --rig and --session")
	}
}

func TestSlackMapChannelMissingTarget(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1",
	)
	if err == nil {
		t.Fatal("expected error when neither --rig nor --session nor --remove given")
	}
}

func TestSlackMapChannelRemoveExisting(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "alpha",
	); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove existing: %v", err)
	}
	if !strings.Contains(stdout, "Removed channel mapping C1") {
		t.Errorf("stdout = %q, want substring 'Removed channel mapping C1'", stdout)
	}
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if _, ok := reg.Get("T1", "C1"); ok {
		t.Errorf("record still present after --remove")
	}
}

func TestSlackMapChannelRemoveMissing(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove missing should succeed (idempotent): %v", err)
	}
	if !strings.Contains(stdout, "No binding for channel C1") {
		t.Errorf("stdout = %q, want substring 'No binding for channel C1'", stdout)
	}
}

func TestSlackMapChannelRemoveWithTargetIsError(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove", "--rig", "alpha",
	)
	if err == nil {
		t.Fatal("expected error for --remove with --rig")
	}
	_, _, err = execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove", "--session", "gc-1",
	)
	if err == nil {
		t.Fatal("expected error for --remove with --session")
	}
}

// slackMapChannelRestartHint is the trailing reminder appended by
// every success-path output of `gc slack map-channel`, parallel to
// the cby.4 rig-mapping CLI.
const slackMapChannelRestartHint = "Send SIGHUP to slack-pack adapter"

func TestSlackMapChannelHappyPathIncludesRestartReminder(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C0123", "--workspace-id", "T123", "--rig", "alpha",
	)
	if err != nil {
		t.Fatalf("map-channel: %v", err)
	}
	if !strings.Contains(stdout, slackMapChannelRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
}

func TestSlackMapChannelRemoveExistingIncludesRestartReminder(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "alpha",
	); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove: %v", err)
	}
	if !strings.Contains(stdout, slackMapChannelRestartHint) {
		t.Errorf("--remove existing missing restart hint: %q", stdout)
	}
}

func TestSlackMapChannelRemoveMissingIncludesRestartReminder(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove missing: %v", err)
	}
	if !strings.Contains(stdout, slackMapChannelRestartHint) {
		t.Errorf("--remove missing missing restart hint: %q", stdout)
	}
}

func TestSlackMapChannelSessionIncludesRestartReminder(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--session", "gc-1",
	)
	if err != nil {
		t.Fatalf("map-channel --session: %v", err)
	}
	if !strings.Contains(stdout, slackMapChannelRestartHint) {
		t.Errorf("session map missing restart hint: %q", stdout)
	}
}

func TestSlackMapChannelCrossStoreConflictDifferentRig(t *testing.T) {
	cityRoot := newTestCity(t)
	// Pre-write rig alpha owning C1 in cby.4.
	rigReg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	if err := rigReg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
	}); err != nil {
		t.Fatal(err)
	}
	// Trying to map-channel C1 → rig beta should be rejected.
	_, _, err = execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "beta",
	)
	if err == nil {
		t.Fatal("expected cross-store conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error should mention conflicting rig %q: %v", "alpha", err)
	}
}

func TestSlackMapChannelCrossStoreSameRigOK(t *testing.T) {
	cityRoot := newTestCity(t)
	rigReg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	_ = rigReg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
	})
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "alpha",
	); err != nil {
		t.Fatalf("same-rig override should be OK: %v", err)
	}
}

func TestSlackMapChannelCrossStoreSessionIgnoresRigStore(t *testing.T) {
	cityRoot := newTestCity(t)
	rigReg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	_ = rigReg.Set(slackRigMappingRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
	})
	// --session is an explicit override, even on top of an existing
	// rig binding, so it must succeed without touching the rig
	// store.
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--session", "gc-1",
	); err != nil {
		t.Fatalf("session override should not be blocked by rig binding: %v", err)
	}
}

// slackMapChannelRigDeprecationHint is the substring cobra's
// MarkDeprecated emits whenever an operator invokes `gc slack
// map-channel --rig`. Option-1 unification (gc-cby.25): per-channel
// rig bindings are deprecated in favor of `gc slack map-rig`. The
// flag remains functional (back-compat) and cobra steers operators
// to the canonical verb. Match a minimal, stable substring so the
// flag-deprecation message wording can evolve without breaking the
// assertion. Cobra writes the warning to OutOrStderr → cmd.outWriter
// (i.e., stdout in this codebase, where root.SetOut(stdout) is set);
// see cmd_events.go --json deprecation for the same pattern.
const slackMapChannelRigDeprecationHint = "deprecated"

func TestSlackMapChannelRigFlagEmitsDeprecationWarning(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C0123", "--workspace-id", "T123", "--rig", "alpha",
	)
	if err != nil {
		t.Fatalf("map-channel --rig should still succeed (soft deprecation): %v", err)
	}
	if !strings.Contains(stdout, slackMapChannelRigDeprecationHint) {
		t.Errorf("stdout missing cobra deprecation warning: %q", stdout)
	}
	if !strings.Contains(stdout, "map-rig") {
		t.Errorf("stdout deprecation should redirect to 'map-rig': %q", stdout)
	}
	// Operation must still complete: registry record persisted.
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if rec, ok := reg.Get("T123", "C0123"); !ok || rec.TargetID != "alpha" {
		t.Errorf("record missing/wrong after deprecated --rig: %+v ok=%v", rec, ok)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("stdout missing rig name: %q", stdout)
	}
}

func TestSlackMapChannelSessionDoesNotEmitDeprecationWarning(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--session", "gc-1",
	)
	if err != nil {
		t.Fatalf("map-channel --session: %v", err)
	}
	if strings.Contains(stdout, slackMapChannelRigDeprecationHint) {
		t.Errorf("--session must NOT emit --rig deprecation warning: %q", stdout)
	}
}

func TestSlackMapChannelRigFlagHiddenFromHelp(t *testing.T) {
	cmd := newSlackMapChannelCmd(nil)
	flag := cmd.Flags().Lookup("rig")
	if flag == nil {
		t.Fatal("--rig flag missing entirely; soft deprecation must keep the flag")
	}
	if !flag.Hidden {
		t.Errorf("--rig flag must be Hidden:true after deprecation, got Hidden=%v", flag.Hidden)
	}
}

func TestSlackMapChannelIdempotentReSetPreservesCreatedAt(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "alpha",
	); err != nil {
		t.Fatal(err)
	}
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	rec1, _ := reg.Get("T1", "C1")
	createdAt := rec1.CreatedAt

	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T1", "--rig", "beta",
	); err != nil {
		t.Fatal(err)
	}
	reg2, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	rec2, _ := reg2.Get("T1", "C1")
	if !rec2.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt advanced on re-set: was %v, now %v", createdAt, rec2.CreatedAt)
	}
	if rec2.TargetID != "beta" {
		t.Errorf("re-set TargetID = %q, want beta", rec2.TargetID)
	}
}
