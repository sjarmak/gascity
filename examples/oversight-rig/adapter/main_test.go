package main

import "testing"

func TestSlackKindFromChannelType(t *testing.T) {
	cases := []struct {
		name        string
		channelType string
		channelID   string
		want        string
	}{
		{"explicit im", "im", "D0B0TTS550F", "dm"},
		{"explicit public channel", "channel", "C0123ROOM01", "room"},
		{"explicit private channel", "group", "G0123ROOM01", "room"},
		{"explicit multi-party DM", "mpim", "G0123ROOM02", "room"},
		{"missing type, dm prefix", "", "D0B0TTS550F", "dm"},
		{"missing type, public prefix", "", "C0FALLBACK01", "room"},
		{"missing type, private prefix", "", "G0FALLBACK02", "room"},
		{"unknown both, default dm", "weird", "X0NEW", "dm"},
		{"empty both", "", "", "dm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slackKindFromChannelType(tc.channelType, tc.channelID)
			if got != tc.want {
				t.Errorf("slackKindFromChannelType(%q, %q) = %q, want %q",
					tc.channelType, tc.channelID, got, tc.want)
			}
		})
	}
}
