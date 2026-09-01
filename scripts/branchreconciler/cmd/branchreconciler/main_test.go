package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	err := run([]string{"--format", "yaml"}, &buf)
	if err == nil {
		t.Fatal("run() error = nil, want error for unknown --format")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Fatalf("run() error = %v, want it to mention the bad format", err)
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var buf bytes.Buffer
	err := run([]string{"--nonexistent-flag"}, &buf)
	if err == nil {
		t.Fatal("run() error = nil, want error for unknown flag")
	}
}
