package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// execSlackMapRigCmd executes the verb directly against a temp city.
func execSlackMapRigCmd(t *testing.T, cityRoot string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cityRoot)
	var stdout, stderr bytes.Buffer
	cmd := newSlackMapRigCmd(&stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

const slackMapRigRestartHint = "Send SIGHUP to slack-pack adapter"

func TestSlackMapRigHappyPath(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, stderr, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1", "--channel", "C2",
	)
	if err != nil {
		t.Fatalf("map-rig: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("stdout should mention rig alpha: %q", stdout)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
	reg, err := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	rec, _, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("rig mapping missing after map-rig")
	}
	if rec.RigName != "alpha" {
		t.Errorf("RigName = %q, want alpha", rec.RigName)
	}
	if len(rec.ChannelIDs) != 2 || rec.ChannelIDs[0] != "C1" || rec.ChannelIDs[1] != "C2" {
		t.Errorf("ChannelIDs = %v, want [C1 C2] (sorted)", rec.ChannelIDs)
	}
}

func TestSlackMapRigCommaSeparatedChannels(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2",
	); err != nil {
		t.Fatalf("map-rig comma-separated: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, _, ok := reg.LookupRigForChannel("T1", "C1")
	if !ok {
		t.Fatal("missing record")
	}
	if len(rec.ChannelIDs) != 2 {
		t.Errorf("ChannelIDs = %v, want 2", rec.ChannelIDs)
	}
}

func TestSlackMapRigMixedFlagAndCommas(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1", "--channel", "C2,C3",
	); err != nil {
		t.Fatalf("map-rig mixed: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, _, _ := reg.LookupRigForChannel("T1", "C1")
	if len(rec.ChannelIDs) != 3 {
		t.Errorf("ChannelIDs = %v, want 3", rec.ChannelIDs)
	}
}

func TestSlackMapRigDedupesChannels(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1", "--channel", "C1",
	); err != nil {
		t.Fatalf("map-rig: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, _, _ := reg.LookupRigForChannel("T1", "C1")
	if len(rec.ChannelIDs) != 1 {
		t.Errorf("ChannelIDs = %v, want 1 (deduped)", rec.ChannelIDs)
	}
}

func TestSlackMapRigMissingWorkspaceID(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "")
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--channel", "C1",
	)
	if err == nil {
		t.Fatal("expected error for missing --workspace-id")
	}
}

func TestSlackMapRigMissingChannel(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1",
	)
	if err == nil {
		t.Fatal("expected error when --channel missing without --remove")
	}
}

func TestSlackMapRigRemoveExisting(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove existing: %v", err)
	}
	if !strings.Contains(stdout, "Removed rig mapping alpha") {
		t.Errorf("stdout = %q, want substring 'Removed rig mapping alpha'", stdout)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if _, _, ok := reg.LookupRigForChannel("T1", "C1"); ok {
		t.Errorf("byChannel still has C1 after --remove")
	}
}

func TestSlackMapRigRemoveMissing(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove",
	)
	if err != nil {
		t.Fatalf("--remove missing should succeed (idempotent): %v", err)
	}
	if !strings.Contains(stdout, "No rig mapping alpha") {
		t.Errorf("stdout = %q, want substring 'No rig mapping alpha'", stdout)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
}

func TestSlackMapRigRemoveWithChannelIsError(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove", "--channel", "C1",
	)
	if err == nil {
		t.Fatal("expected error for --remove with --channel")
	}
}

func TestSlackMapRigReplaceWithDropsWarning(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2,C3",
	); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C2,C3",
	)
	if err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if !strings.Contains(stderr, "dropped: C1") {
		t.Errorf("stderr should warn about dropped channels: %q", stderr)
	}
}

func TestSlackMapRigCrossStoreConflictDifferentRig(t *testing.T) {
	cityRoot := newTestCity(t)
	// Pre-write cby.3 channel mapping C1 → rig beta.
	chanReg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := chanReg.Set(slackChannelMappingRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "rig", TargetID: "beta",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Now try to map-rig alpha to include C1 → should fail.
	_, _, err = execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	)
	if err == nil {
		t.Fatal("expected cross-store conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "beta") {
		t.Errorf("error should mention conflicting rig %q: %v", "beta", err)
	}
}

func TestSlackMapRigCrossStoreSameRigOK(t *testing.T) {
	cityRoot := newTestCity(t)
	// Pre-write cby.3 channel mapping C1 → rig alpha.
	chanReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	_ = chanReg.Set(slackChannelMappingRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "rig", TargetID: "alpha",
		CreatedAt: now, UpdatedAt: now,
	})
	// Now map-rig alpha including C1 — same rig, should succeed.
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatalf("same-rig should be OK: %v", err)
	}
}

func TestSlackMapRigCrossStoreSessionMappingOK(t *testing.T) {
	cityRoot := newTestCity(t)
	// Pre-write cby.3 channel mapping C1 → session gc-1 (an explicit override).
	chanReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	_ = chanReg.Set(slackChannelMappingRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "session", TargetID: "gc-1",
		CreatedAt: now, UpdatedAt: now,
	})
	// map-rig alpha including C1 should succeed; the per-channel
	// session mapping is the explicit override (the intended
	// composition pattern).
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatalf("session override should not block map-rig: %v", err)
	}
}

func TestSlackMapRigCrossRigConflictWithinCBy4(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatal(err)
	}
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"beta", "--workspace-id", "T1", "--channel", "C1",
	)
	if err == nil {
		t.Fatal("expected cross-rig conflict, got nil")
	}
}

func TestSlackMapRigRemoveChannelsPartial(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2,C3",
	); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C2",
	)
	if err != nil {
		t.Fatalf("--remove-channels partial: %v", err)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "alpha")
	if !ok {
		t.Fatal("record vanished after partial removal")
	}
	if len(rec.ChannelIDs) != 2 || rec.ChannelIDs[0] != "C1" || rec.ChannelIDs[1] != "C3" {
		t.Errorf("ChannelIDs = %v, want [C1 C3]", rec.ChannelIDs)
	}
}

func TestSlackMapRigRemoveChannelsMultiple(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2,C3",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C2,C3",
	); err != nil {
		t.Fatalf("--remove-channels multi: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "alpha")
	if !ok {
		t.Fatal("record missing after multi removal")
	}
	if len(rec.ChannelIDs) != 1 || rec.ChannelIDs[0] != "C1" {
		t.Errorf("ChannelIDs = %v, want [C1]", rec.ChannelIDs)
	}
}

func TestSlackMapRigRemoveChannelsRepeatedFlag(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2,C3",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C2", "--remove-channels", "C3",
	); err != nil {
		t.Fatalf("--remove-channels repeat: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "alpha")
	if !ok {
		t.Fatal("record missing")
	}
	if len(rec.ChannelIDs) != 1 || rec.ChannelIDs[0] != "C1" {
		t.Errorf("ChannelIDs = %v, want [C1]", rec.ChannelIDs)
	}
}

func TestSlackMapRigRemoveChannelsEmptyAfterDeletesRecord(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2",
	); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C1,C2",
	)
	if err != nil {
		t.Fatalf("--remove-channels empty-after: %v", err)
	}
	if !strings.Contains(stdout, "Removed rig mapping alpha") {
		t.Errorf("stdout = %q, want substring 'Removed rig mapping alpha' (record deleted because channel set became empty)", stdout)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint after empty-after deletion: %q", stdout)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if _, ok := reg.Get("T1", "alpha"); ok {
		t.Errorf("record should be deleted after channel set became empty")
	}
}

func TestSlackMapRigRemoveChannelsIdempotentForUnknownChannels(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C99,C100",
	); err != nil {
		t.Fatalf("--remove-channels for unknown channels should be a silent no-op: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "alpha")
	if !ok {
		t.Fatal("record vanished")
	}
	if len(rec.ChannelIDs) != 1 || rec.ChannelIDs[0] != "C1" {
		t.Errorf("ChannelIDs = %v, want [C1] (unchanged)", rec.ChannelIDs)
	}
}

func TestSlackMapRigRemoveChannelsMissingRigIsNoOp(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "C1",
	)
	if err != nil {
		t.Fatalf("--remove-channels for missing rig should succeed (idempotent): %v", err)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("stdout = %q, want substring 'alpha'", stdout)
	}
	if !strings.Contains(stdout, slackMapRigRestartHint) {
		t.Errorf("stdout missing restart hint: %q", stdout)
	}
}

func TestSlackMapRigRemoveChannelsWithRemoveIsError(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove", "--remove-channels", "C1",
	)
	if err == nil {
		t.Fatal("expected error for --remove with --remove-channels")
	}
}

func TestSlackMapRigRemoveChannelsWithChannelIsError(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1", "--remove-channels", "C2",
	)
	if err == nil {
		t.Fatal("expected error for --channel with --remove-channels")
	}
}

func TestSlackMapRigRemoveChannelsEmptyValueIsError(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--remove-channels", "",
	)
	if err == nil {
		t.Fatal("expected error for --remove-channels with no non-empty values")
	}
}

func TestSlackMapRigIdempotentReSetPreservesCreatedAt(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
	); err != nil {
		t.Fatal(err)
	}
	reg1, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec1, _, _ := reg1.LookupRigForChannel("T1", "C1")
	createdAt := rec1.CreatedAt

	time.Sleep(2 * time.Millisecond)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2",
	); err != nil {
		t.Fatal(err)
	}
	reg2, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec2, _, _ := reg2.LookupRigForChannel("T1", "C1")
	if !rec2.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt advanced on re-set: was %v, now %v", createdAt, rec2.CreatedAt)
	}
	if !rec2.UpdatedAt.After(createdAt) {
		t.Errorf("UpdatedAt did not advance: %v vs %v", rec2.UpdatedAt, createdAt)
	}
}

// TestSlackMapRigSlingTargetAndFixFormulaPersisted exercises the
// cby.18.a flags: --sling-target and --fix-formula are persisted on
// the rig record so the adapter's /slack/interactions handler can
// route without hardcoded role names.
func TestSlackMapRigSlingTargetAndFixFormulaPersisted(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
		"--sling-target", "alpha/polecat",
		"--fix-formula", "mol-slack-fix-issue",
	); err != nil {
		t.Fatalf("map-rig with --sling-target/--fix-formula: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, ok := reg.Get("T1", "alpha")
	if !ok {
		t.Fatal("missing record")
	}
	if rec.SlingTarget != "alpha/polecat" {
		t.Errorf("SlingTarget = %q, want alpha/polecat", rec.SlingTarget)
	}
	if rec.FixFormula != "mol-slack-fix-issue" {
		t.Errorf("FixFormula = %q, want mol-slack-fix-issue", rec.FixFormula)
	}
}

// TestSlackMapRigInvalidSlingTargetIsRejected ensures the CLI refuses a
// malformed --sling-target before touching disk.
func TestSlackMapRigInvalidSlingTargetIsRejected(t *testing.T) {
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
		"--sling-target", "no-slash",
	)
	if err == nil {
		t.Fatal("expected error for malformed --sling-target, got nil")
	}
}

// TestSlackMapRigPreservesSlingTargetOnReSet ensures an idempotent
// re-bind that omits --sling-target/--fix-formula keeps the previously
// stored values rather than clearing them. This matches the existing
// CreatedAt-preservation behavior — operators who only want to update
// the channel set don't have to re-supply every field.
func TestSlackMapRigPreservesSlingTargetOnReSet(t *testing.T) {
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1",
		"--sling-target", "alpha/polecat",
		"--fix-formula", "mol-slack-fix-issue",
	); err != nil {
		t.Fatal(err)
	}
	// Re-bind with new channel set, no --sling-target/--fix-formula.
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T1", "--channel", "C1,C2",
	); err != nil {
		t.Fatalf("re-bind without flags: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	rec, _ := reg.Get("T1", "alpha")
	if rec.SlingTarget != "alpha/polecat" {
		t.Errorf("SlingTarget cleared on re-bind: got %q", rec.SlingTarget)
	}
	if rec.FixFormula != "mol-slack-fix-issue" {
		t.Errorf("FixFormula cleared on re-bind: got %q", rec.FixFormula)
	}
}
