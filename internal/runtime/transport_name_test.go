package runtime

import "testing"

func TestTransportForRuntimeName(t *testing.T) {
	tests := map[string]string{
		"":                        "tmux",
		"tmux":                    "tmux",
		"k8s":                     "tmux",
		"exec:/opt/runtime":       "tmux",
		"acp":                     "acp",
		"t3bridge":                "t3",
		"exec:/opt/gc-session-t3": "t3",
		"exec:gc-session-t3":      "t3",
	}
	for name, want := range tests {
		if got := TransportForRuntimeName(name); got != want {
			t.Errorf("TransportForRuntimeName(%q) = %q, want %q", name, got, want)
		}
	}
	if got := TransportForRuntimeName(" exec:/x/gc-session-t3  "); got != "t3" {
		t.Errorf("TransportForRuntimeName() did not trim surrounding whitespace: got %q, want t3", got)
	}
}

func TestCanonicalTransport(t *testing.T) {
	tests := map[string]string{
		// Already a transport carrier — returned unchanged.
		"acp":  "acp",
		"tmux": "tmux",
		"t3":   "t3",
		// A runtime-selection name that leaked into a transport slot — mapped.
		"t3bridge":                "t3",
		"exec:/opt/gc-session-t3": "t3",
		// Anything else falls to the tmux carrier.
		"k8s": "tmux",
	}
	for value, want := range tests {
		if got := CanonicalTransport(value); got != want {
			t.Errorf("CanonicalTransport(%q) = %q, want %q", value, got, want)
		}
	}
	if got := CanonicalTransport("  t3bridge "); got != "t3" {
		t.Errorf("CanonicalTransport() did not trim surrounding whitespace: got %q, want t3", got)
	}
}
