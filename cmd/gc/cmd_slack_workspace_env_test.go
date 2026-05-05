package main

import (
	"bytes"
	"strings"
	"testing"
)

// Coverage for the SLACK_WORKSPACE_ID env-var default applied to every
// `gc slack` verb's --workspace-id flag (gc-cby.24).
//
// Tests use t.Setenv (no t.Parallel) to isolate the env between cases.

// --- map-channel ---------------------------------------------------------

func TestSlackMapChannelUsesEnvWhenFlagOmitted(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--rig", "alpha",
	); err != nil {
		t.Fatalf("map-channel without --workspace-id but with env: %v", err)
	}
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if _, ok := reg.Get("T_ENV", "C1"); !ok {
		t.Errorf("registry missing record for env-default workspace T_ENV")
	}
}

func TestSlackMapChannelFlagOverridesEnv(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--workspace-id", "T_FLAG", "--rig", "alpha",
	); err != nil {
		t.Fatalf("map-channel: %v", err)
	}
	reg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if _, ok := reg.Get("T_FLAG", "C1"); !ok {
		t.Errorf("flag value should win; want record for T_FLAG, got none")
	}
	if _, ok := reg.Get("T_ENV", "C1"); ok {
		t.Errorf("env value should not have been used when flag is set")
	}
}

func TestSlackMapChannelWhitespaceEnvTreatedAsUnset(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "   ")
	cityRoot := newTestCity(t)
	_, _, err := execSlackMapChannelCmd(t, cityRoot,
		"C1", "--rig", "alpha",
	)
	if err == nil {
		t.Fatal("expected required-flag error when env is whitespace-only")
	}
}

// --- map-rig -------------------------------------------------------------

func TestSlackMapRigUsesEnvWhenFlagOmitted(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--channel", "C1",
	); err != nil {
		t.Fatalf("map-rig without --workspace-id but with env: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if _, ok := reg.Get("T_ENV", "alpha"); !ok {
		t.Errorf("registry missing rig record for env-default workspace T_ENV")
	}
}

func TestSlackMapRigFlagOverridesEnv(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	cityRoot := newTestCity(t)
	if _, _, err := execSlackMapRigCmd(t, cityRoot,
		"alpha", "--workspace-id", "T_FLAG", "--channel", "C1",
	); err != nil {
		t.Fatalf("map-rig: %v", err)
	}
	reg, _ := newSlackRigMappingRegistry(slackRigMappingsPath(cityRoot))
	if _, ok := reg.Get("T_FLAG", "alpha"); !ok {
		t.Errorf("flag value should win; want record for T_FLAG")
	}
	if _, ok := reg.Get("T_ENV", "alpha"); ok {
		t.Errorf("env value should not have been used when flag is set")
	}
}

// --- import-app ----------------------------------------------------------

func execSlackImportAppCmdNoCity(t *testing.T, args ...string) (string, string, error) { //nolint:unparam // helper returns stdout for callers that may grow to inspect it
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newSlackImportAppCmd(&stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSlackImportAppMissingWorkspaceIDWhenEnvUnset(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "")
	// app-id provided so the failure isolates to workspace-id.
	_, _, err := execSlackImportAppCmdNoCity(t,
		"manifest.json", "--app-id", "A1",
	)
	if err == nil {
		t.Fatal("expected required-flag error for missing --workspace-id with empty env")
	}
	if !strings.Contains(err.Error(), "workspace-id") {
		t.Errorf("err should reference workspace-id flag: %v", err)
	}
}

func TestSlackImportAppFlagOptionalWhenEnvSet(t *testing.T) {
	// With env set, omitting --workspace-id should NOT trigger
	// MarkFlagRequired. The verb will still fail (missing manifest
	// file), but the failure must come from the runner, not from
	// cobra's required-flag check.
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	_, _, err := execSlackImportAppCmdNoCity(t,
		"/nonexistent/manifest.json", "--app-id", "A1",
	)
	if err == nil {
		t.Fatal("expected error from missing manifest file")
	}
	if strings.Contains(err.Error(), "workspace-id") {
		t.Errorf("err should NOT reference workspace-id (env default should satisfy required): %v", err)
	}
}

// --- sync-commands -------------------------------------------------------

func execSlackSyncCommandsCmdNoCity(t *testing.T, args ...string) (string, string, error) { //nolint:unparam // helper returns stdout for callers that may grow to inspect it
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newSlackSyncCommandsCmd(&stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSlackSyncCommandsMissingWorkspaceIDWhenEnvUnset(t *testing.T) {
	t.Setenv(slackWorkspaceIDEnv, "")
	_, _, err := execSlackSyncCommandsCmdNoCity(t,
		"--app-id", "A1",
	)
	if err == nil {
		t.Fatal("expected required-flag error")
	}
	if !strings.Contains(err.Error(), "workspace-id") {
		t.Errorf("err should reference workspace-id: %v", err)
	}
}

func TestSlackSyncCommandsFlagOptionalWhenEnvSet(t *testing.T) {
	// Env satisfies required; verb fails later on missing token /
	// no-such-app — but NOT on workspace-id.
	t.Setenv(slackWorkspaceIDEnv, "T_ENV")
	t.Setenv(slackConfigTokenEnv, "")
	_, _, err := execSlackSyncCommandsCmdNoCity(t,
		"--app-id", "A1",
	)
	if err == nil {
		t.Fatal("expected error from missing token")
	}
	if strings.Contains(err.Error(), "workspace-id") {
		t.Errorf("err should NOT reference workspace-id: %v", err)
	}
}

// --- status --------------------------------------------------------------

// Status's --workspace-id is a FILTER, not a required input. With the
// env default set, an unflagged status invocation should silently scope
// to that workspace. This test seeds two workspaces and verifies the
// env-default scoping.
func TestSlackStatusFiltersByEnvDefault(t *testing.T) {
	cityRoot := newTestCity(t)
	appReg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"T_A", "T_B"} {
		if err := appReg.Set(slackAppRecord{
			WorkspaceID: ws, AppID: "A_" + ws, DisplayName: "app-" + ws,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// With env set to T_A, status should show T_A only.
	t.Setenv(slackWorkspaceIDEnv, "T_A")
	stdout, _, err := execSlackStatusCmd(t, cityRoot)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "T_A") {
		t.Errorf("status output should include T_A: %q", stdout)
	}
	if strings.Contains(stdout, "T_B") {
		t.Errorf("status with env=T_A should NOT include T_B: %q", stdout)
	}
}

func TestSlackStatusFlagOverridesEnv(t *testing.T) {
	cityRoot := newTestCity(t)
	appReg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"T_A", "T_B"} {
		if err := appReg.Set(slackAppRecord{
			WorkspaceID: ws, AppID: "A_" + ws, DisplayName: "app-" + ws,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(slackWorkspaceIDEnv, "T_A")
	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--workspace-id", "T_B")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "T_B") {
		t.Errorf("status with --workspace-id=T_B should include T_B: %q", stdout)
	}
	if strings.Contains(stdout, "T_A") {
		t.Errorf("status with --workspace-id=T_B should NOT include T_A: %q", stdout)
	}
}

// TestSlackStatusEmptyFlagShowsAllWorkspaces pins the escape hatch
// documented in the --workspace-id help text. With $SLACK_WORKSPACE_ID
// set, an explicit --workspace-id="" must override the env default and
// show records across all workspaces. Operators without this contract
// would have no way to see other workspaces while their env is set.
func TestSlackStatusEmptyFlagShowsAllWorkspaces(t *testing.T) {
	cityRoot := newTestCity(t)
	appReg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"T_A", "T_B"} {
		if err := appReg.Set(slackAppRecord{
			WorkspaceID: ws, AppID: "A_" + ws, DisplayName: "app-" + ws,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(slackWorkspaceIDEnv, "T_A")
	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--workspace-id", "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "T_A") {
		t.Errorf(`status with --workspace-id="" should include T_A: %q`, stdout)
	}
	if !strings.Contains(stdout, "T_B") {
		t.Errorf(`status with --workspace-id="" should include T_B: %q`, stdout)
	}
}
