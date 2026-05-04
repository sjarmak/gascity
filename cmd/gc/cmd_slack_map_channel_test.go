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
	cmd := newSlackMapChannelCmd(&stdout, &stderr)
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
