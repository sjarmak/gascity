package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/session"
)

func TestEmitSessionSubmitResultFollowUpQueued(t *testing.T) {
	var stdout bytes.Buffer
	emitSessionSubmitResult(&stdout, io.Discard, "mayor", session.SubmitIntentFollowUp, true, false)
	if got := stdout.String(); !strings.Contains(got, "Queued follow-up for mayor") {
		t.Fatalf("stdout = %q, want queued confirmation", got)
	}
}

func TestEmitSessionSubmitResultFollowUpImmediate(t *testing.T) {
	var stdout bytes.Buffer
	emitSessionSubmitResult(&stdout, io.Discard, "mayor", session.SubmitIntentFollowUp, false, false)
	got := stdout.String()
	if strings.Contains(got, "Queued") {
		t.Fatalf("stdout = %q, should not say Queued when message was delivered immediately", got)
	}
	if !strings.Contains(got, "Submitted follow-up to mayor") {
		t.Fatalf("stdout = %q, want submitted follow-up confirmation", got)
	}
}

func TestEmitSessionSubmitResultJSON(t *testing.T) {
	var stdout bytes.Buffer
	emitSessionSubmitResult(&stdout, io.Discard, "mayor", session.SubmitIntentFollowUp, true, true)
	got := stdout.String()
	for _, want := range []string{`"schema_version":"1"`, `"ok":true`, `"target":"mayor"`, `"intent":"follow_up"`, `"queued":true`, `"outcome":"queued"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, missing %s", got, want)
		}
	}
}

func TestParseSessionSubmitIntentAcceptsLegacySpellings(t *testing.T) {
	cases := []struct {
		raw  string
		want session.SubmitIntent
	}{
		{raw: "follow_up", want: session.SubmitIntentFollowUp},
		{raw: "follow-up", want: session.SubmitIntentFollowUp},
		{raw: "interrupt_now", want: session.SubmitIntentInterruptNow},
		{raw: "interrupt-now", want: session.SubmitIntentInterruptNow},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseSessionSubmitIntent(tc.raw)
			if err != nil {
				t.Fatalf("parseSessionSubmitIntent(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseSessionSubmitIntent(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	for _, raw := range []string{"", "default"} {
		if _, err := parseSessionSubmitIntent(raw); err == nil {
			t.Fatalf("parseSessionSubmitIntent(%q) unexpectedly succeeded", raw)
		}
	}
	if _, err := parseSessionSubmitIntent("later"); err == nil {
		t.Fatal("parseSessionSubmitIntent(later) unexpectedly succeeded")
	}
}

func TestSessionSubmitRequiresExplicitSafeIntentBeforeTargetResolution(t *testing.T) {
	for _, args := range [][]string{
		{"not-a-real-session", "work direction"},
		{"not-a-real-session", "work direction", "--intent", "default"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := newSessionSubmitCmd(&stdout, &stderr)
			cmd.SetArgs(args)

			err := cmd.Execute()
			if !errors.Is(err, errExit) {
				t.Fatalf("Execute() error = %v, want errExit", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, "explicit --intent is required") {
				t.Fatalf("stderr = %q, want explicit intent error", got)
			}
		})
	}
}
