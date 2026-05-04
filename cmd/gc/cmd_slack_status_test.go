package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func execSlackStatusCmd(t *testing.T, cityRoot string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cityRoot)
	var stdout, stderr bytes.Buffer
	cmd := newSlackStatusCmd(&stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSlackStatusEmptyStore(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackStatusCmd(t, cityRoot)
	if err != nil {
		t.Fatalf("status on empty city: %v", err)
	}
	// Should mention both registries are empty (in some form).
	if !strings.Contains(strings.ToLower(stdout), "no slack apps") &&
		!strings.Contains(stdout, "0 apps") {
		t.Errorf("stdout should signal empty apps section: %q", stdout)
	}
	if !strings.Contains(strings.ToLower(stdout), "no channel mappings") &&
		!strings.Contains(stdout, "0 channel mappings") {
		t.Errorf("stdout should signal empty mappings section: %q", stdout)
	}
}

func TestSlackStatusShowsAppAndMapping(t *testing.T) {
	cityRoot := newTestCity(t)
	// Seed an app via the import flow's registry directly.
	appReg, err := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	if err := appReg.Set(slackAppRecord{
		WorkspaceID: "T1", AppID: "A1",
		DisplayName: "gc-oversight",
		Scopes:      []string{"commands", "chat:write"},
		ImportedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mapReg, err := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := mapReg.Set(slackChannelMappingRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "rig", TargetID: "alpha",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execSlackStatusCmd(t, cityRoot)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"T1/A1", "gc-oversight", "T1/C1", "rig", "alpha"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q: %s", want, stdout)
		}
	}
}

func TestSlackStatusFilterByChannel(t *testing.T) {
	cityRoot := newTestCity(t)
	mapReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	for _, ch := range []string{"C1", "C2"} {
		if err := mapReg.Set(slackChannelMappingRecord{
			WorkspaceID: "T1", ChannelID: ch,
			TargetKind: "rig", TargetID: "r",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--channel", "C2")
	if err != nil {
		t.Fatalf("status --channel: %v", err)
	}
	if strings.Contains(stdout, "C1") {
		t.Errorf("--channel C2 leaked C1: %s", stdout)
	}
	if !strings.Contains(stdout, "C2") {
		t.Errorf("--channel C2 missing C2: %s", stdout)
	}
}

func TestSlackStatusFilterByWorkspace(t *testing.T) {
	cityRoot := newTestCity(t)
	appReg, _ := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	_ = appReg.Set(slackAppRecord{WorkspaceID: "T1", AppID: "A1", DisplayName: "alpha", ImportedAt: time.Now().UTC()})
	_ = appReg.Set(slackAppRecord{WorkspaceID: "T2", AppID: "A2", DisplayName: "beta", ImportedAt: time.Now().UTC()})

	mapReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	_ = mapReg.Set(slackChannelMappingRecord{WorkspaceID: "T1", ChannelID: "C1", TargetKind: "rig", TargetID: "r", CreatedAt: now, UpdatedAt: now})
	_ = mapReg.Set(slackChannelMappingRecord{WorkspaceID: "T2", ChannelID: "C2", TargetKind: "rig", TargetID: "r", CreatedAt: now, UpdatedAt: now})

	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--workspace-id", "T2")
	if err != nil {
		t.Fatalf("status --workspace-id: %v", err)
	}
	if strings.Contains(stdout, "T1/") {
		t.Errorf("--workspace-id T2 leaked T1: %s", stdout)
	}
	if !strings.Contains(stdout, "T2/") {
		t.Errorf("--workspace-id T2 missing T2 entries: %s", stdout)
	}
}

func TestSlackStatusJSONShape(t *testing.T) {
	cityRoot := newTestCity(t)
	appReg, _ := newSlackAppRegistry(slackAppsRegistryPath(cityRoot))
	_ = appReg.Set(slackAppRecord{WorkspaceID: "T1", AppID: "A1", DisplayName: "x", ImportedAt: time.Now().UTC()})
	mapReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	_ = mapReg.Set(slackChannelMappingRecord{WorkspaceID: "T1", ChannelID: "C1", TargetKind: "session", TargetID: "gc-1", CreatedAt: now, UpdatedAt: now})

	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var parsed struct {
		Apps     []slackAppRecord            `json:"apps"`
		Mappings []slackChannelMappingRecord `json:"channel_mappings"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\noutput=%s", err, stdout)
	}
	if len(parsed.Apps) != 1 || len(parsed.Mappings) != 1 {
		t.Errorf("apps=%d mappings=%d, want 1/1", len(parsed.Apps), len(parsed.Mappings))
	}
}

func TestSlackStatusJSONEmpty(t *testing.T) {
	cityRoot := newTestCity(t)
	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--json")
	if err != nil {
		t.Fatalf("status --json on empty: %v", err)
	}
	var parsed struct {
		Apps     []slackAppRecord            `json:"apps"`
		Mappings []slackChannelMappingRecord `json:"channel_mappings"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\noutput=%s", err, stdout)
	}
	// Expect empty arrays (or nil — both unmarshal to len 0) but the keys must exist.
	if !strings.Contains(stdout, `"apps"`) || !strings.Contains(stdout, `"channel_mappings"`) {
		t.Errorf("JSON should contain top-level keys: %s", stdout)
	}
}

func TestSlackStatusJSONChannelFilter(t *testing.T) {
	cityRoot := newTestCity(t)
	mapReg, _ := newSlackChannelMappingRegistry(slackChannelMappingsPath(cityRoot))
	now := time.Now().UTC()
	for _, ch := range []string{"C1", "C2"} {
		_ = mapReg.Set(slackChannelMappingRecord{
			WorkspaceID: "T1", ChannelID: ch,
			TargetKind: "rig", TargetID: "r",
			CreatedAt: now, UpdatedAt: now,
		})
	}
	stdout, _, err := execSlackStatusCmd(t, cityRoot, "--channel", "C1", "--json")
	if err != nil {
		t.Fatalf("status --channel --json: %v", err)
	}
	var parsed struct {
		Apps     []slackAppRecord            `json:"apps"`
		Mappings []slackChannelMappingRecord `json:"channel_mappings"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nout=%s", err, stdout)
	}
	if len(parsed.Mappings) != 1 || parsed.Mappings[0].ChannelID != "C1" {
		t.Errorf("filter mismatch: %+v", parsed.Mappings)
	}
}
