package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderMilestoneBlocks(t *testing.T) {
	payload := slackStatusPayload{
		Title:   "Polecat 7 reached green",
		Summary: "All gates passed in 4m32s.",
		Fields: []slackStatusField{
			{Label: "rig", Value: "polecat-7"},
			{Label: "duration", Value: "4m32s"},
		},
	}
	blocks, err := renderStatusBlocks(slackStatusKindMilestone, payload)
	if err != nil {
		t.Fatalf("renderStatusBlocks: %v", err)
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least header + section, got %d blocks", len(blocks))
	}
	if blocks[0].Type != "header" {
		t.Errorf("first block: want header, got %q", blocks[0].Type)
	}
	if blocks[0].Text == nil || !strings.Contains(blocks[0].Text.Text, "Polecat 7 reached green") {
		t.Errorf("header text missing title: %+v", blocks[0].Text)
	}
	// Round-trip JSON to ensure marshaling produces what Slack expects.
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"header"`) {
		t.Errorf("rendered JSON missing header type: %s", raw)
	}
	if !strings.Contains(string(raw), "Polecat 7") {
		t.Errorf("rendered JSON missing title: %s", raw)
	}
}

func TestRenderProgressBlocks(t *testing.T) {
	payload := slackStatusPayload{
		Title:    "Convoy 12 progress",
		Summary:  "Step 3/5: lint",
		Progress: &slackStatusProgress{Current: 3, Total: 5},
	}
	blocks, err := renderStatusBlocks(slackStatusKindProgress, payload)
	if err != nil {
		t.Fatalf("renderStatusBlocks: %v", err)
	}
	// Find the progress section's text and verify a bar is rendered.
	var seen bool
	for _, b := range blocks {
		if b.Type != "section" || b.Text == nil {
			continue
		}
		if strings.Contains(b.Text.Text, "3/5") {
			seen = true
		}
	}
	if !seen {
		t.Errorf("expected progress fraction 3/5 in a section block, got blocks=%+v", blocks)
	}
}

func TestRenderProgressBlocksRejectsBadFraction(t *testing.T) {
	payload := slackStatusPayload{
		Title:    "Bad",
		Progress: &slackStatusProgress{Current: 10, Total: 5},
	}
	if _, err := renderStatusBlocks(slackStatusKindProgress, payload); err == nil {
		t.Fatalf("expected error for current>total, got nil")
	}
	payload2 := slackStatusPayload{
		Title:    "Bad2",
		Progress: &slackStatusProgress{Current: 1, Total: 0},
	}
	if _, err := renderStatusBlocks(slackStatusKindProgress, payload2); err == nil {
		t.Fatalf("expected error for total=0, got nil")
	}
}

func TestRenderRollupBlocks(t *testing.T) {
	payload := slackStatusPayload{
		Title:   "Daily rollup",
		Summary: "3 rigs healthy, 1 stalled.",
		Items: []slackStatusItem{
			{Label: "polecat-7", Value: "healthy"},
			{Label: "polecat-8", Value: "stalled"},
		},
	}
	blocks, err := renderStatusBlocks(slackStatusKindRollup, payload)
	if err != nil {
		t.Fatalf("renderStatusBlocks: %v", err)
	}
	raw, _ := json.Marshal(blocks)
	for _, want := range []string{"Daily rollup", "polecat-7", "polecat-8"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("rendered JSON missing %q: %s", want, raw)
		}
	}
}

func TestRenderStatusBlocksUnknownKind(t *testing.T) {
	if _, err := renderStatusBlocks("nonsense", slackStatusPayload{Title: "x"}); err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func TestRenderStatusBlocksRequiresTitle(t *testing.T) {
	if _, err := renderStatusBlocks(slackStatusKindMilestone, slackStatusPayload{}); err == nil {
		t.Fatalf("expected error when title is empty")
	}
}
