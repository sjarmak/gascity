package config

import (
	"testing"
)

func TestResolveOptions_ExplicitValues(t *testing.T) {
	schema := []ProviderOption{
		{
			Key: "permission_mode", Label: "Permission Mode", Type: "select",
			Default: "auto-edit",
			Choices: []OptionChoice{
				{Value: "auto-edit", Label: "Edit automatically", FlagArgs: []string{"--permission-mode", "auto-edit"}},
				{Value: "plan", Label: "Plan mode", FlagArgs: []string{"--permission-mode", "plan"}},
			},
		},
		{
			Key: "thinking", Label: "Thinking", Type: "select",
			Default: "",
			Choices: []OptionChoice{
				{Value: "", Label: "Default", FlagArgs: nil},
				{Value: "high", Label: "High", FlagArgs: []string{"--thinking", "high"}},
			},
		},
	}

	args, meta, err := ResolveOptions(schema, map[string]string{
		"permission_mode": "plan",
		"thinking":        "high",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Args must be in schema declaration order (deterministic).
	wantArgs := []string{"--permission-mode", "plan", "--thinking", "high"}
	if len(args) != len(wantArgs) {
		t.Fatalf("got args=%v, want %v", args, wantArgs)
	}
	for i, w := range wantArgs {
		if args[i] != w {
			t.Errorf("args[%d]=%q, want %q (full: %v)", i, args[i], w, args)
		}
	}

	// Metadata should have explicit choices.
	if meta["opt_permission_mode"] != "plan" {
		t.Errorf("got meta opt_permission_mode=%q, want plan", meta["opt_permission_mode"])
	}
	if meta["opt_thinking"] != "high" {
		t.Errorf("got meta opt_thinking=%q, want high", meta["opt_thinking"])
	}
}

func TestResolveOptions_DefaultsApplied(t *testing.T) {
	schema := []ProviderOption{
		{
			Key: "permission_mode", Label: "Permission Mode", Type: "select",
			Default: "auto-edit",
			Choices: []OptionChoice{
				{Value: "auto-edit", Label: "Edit automatically", FlagArgs: []string{"--permission-mode", "auto-edit"}},
			},
		},
		{
			Key: "thinking", Label: "Thinking", Type: "select",
			Default: "", // empty default — no args injected
			Choices: []OptionChoice{
				{Value: "", Label: "Default", FlagArgs: nil},
			},
		},
	}

	args, meta, err := ResolveOptions(schema, nil)
	if err != nil {
		t.Fatal(err)
	}

	// permission_mode default should inject args.
	if len(args) != 2 || args[0] != "--permission-mode" || args[1] != "auto-edit" {
		t.Errorf("got args=%v, want [--permission-mode auto-edit]", args)
	}

	// Defaults should NOT be in metadata.
	if len(meta) != 0 {
		t.Errorf("got meta=%v, want empty (defaults not persisted)", meta)
	}
}

func TestResolveOptions_UnknownOption(t *testing.T) {
	schema := []ProviderOption{
		{Key: "mode", Choices: []OptionChoice{{Value: "a"}}},
	}
	_, _, err := ResolveOptions(schema, map[string]string{"bogus": "val"})
	if err == nil {
		t.Fatal("expected error for unknown option")
	}
}

func TestResolveOptions_InvalidValue(t *testing.T) {
	schema := []ProviderOption{
		{
			Key: "mode", Choices: []OptionChoice{
				{Value: "a", Label: "A"},
				{Value: "b", Label: "B"},
			},
		},
	}
	_, _, err := ResolveOptions(schema, map[string]string{"mode": "c"})
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestResolveOptions_EmptyStringChoice(t *testing.T) {
	schema := []ProviderOption{
		{
			Key: "thinking", Default: "",
			Choices: []OptionChoice{
				{Value: "", Label: "Default", FlagArgs: nil},
				{Value: "high", Label: "High", FlagArgs: []string{"--thinking", "high"}},
			},
		},
	}

	// Explicit empty string should be accepted (not rejected as "invalid").
	args, meta, err := ResolveOptions(schema, map[string]string{"thinking": ""})
	if err != nil {
		t.Fatalf("empty string choice should be valid: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("empty string choice should produce no args, got %v", args)
	}
	if meta["opt_thinking"] != "" {
		t.Errorf("explicit empty choice should be in metadata")
	}
	if _, ok := meta["opt_thinking"]; !ok {
		t.Error("explicit empty choice key should exist in metadata")
	}
}

func TestResolveOptions_NilSchema(t *testing.T) {
	args, meta, err := ResolveOptions(nil, map[string]string{"anything": "val"})
	if err == nil {
		t.Fatal("expected error for option against nil schema")
	}
	_ = args
	_ = meta
}
