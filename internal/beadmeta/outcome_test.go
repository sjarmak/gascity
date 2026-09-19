package beadmeta

import "testing"

func TestIsOutcomeFailed(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]string
		want     bool
	}{
		{"explicit fail", map[string]string{OutcomeMetadataKey: OutcomeFail}, true},
		{"explicit pass, no on_fail", map[string]string{OutcomeMetadataKey: OutcomePass}, false},
		{"no on_fail, no outcome", map[string]string{}, false},
		{"abort_scope pass", map[string]string{OnFailMetadataKey: "abort_scope", OutcomeMetadataKey: OutcomePass}, false},
		{"abort_scope skipped", map[string]string{OnFailMetadataKey: "abort_scope", OutcomeMetadataKey: OutcomeSkipped}, false},
		{"abort_scope canceled", map[string]string{OnFailMetadataKey: "abort_scope", OutcomeMetadataKey: OutcomeCanceled}, false},
		{"abort_scope missing outcome defaults failed", map[string]string{OnFailMetadataKey: "abort_scope"}, true},
		{"abort_scope unrecognized outcome defaults failed", map[string]string{OnFailMetadataKey: "abort_scope", OutcomeMetadataKey: "bogus"}, true},
		{"abort_scope retry attempt exempt", map[string]string{
			OnFailMetadataKey:        "abort_scope",
			LogicalBeadIDMetadataKey: "gc-abc123",
			KindMetadataKey:          "retry-run",
		}, false},
		{"abort_scope attempt-tagged exempt", map[string]string{
			OnFailMetadataKey:        "abort_scope",
			LogicalBeadIDMetadataKey: "gc-abc123",
			AttemptMetadataKey:       "2",
		}, false},
		{"on_fail not abort_scope ignored", map[string]string{OnFailMetadataKey: "retry"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOutcomeFailed(tc.metadata); got != tc.want {
				t.Fatalf("IsOutcomeFailed(%v) = %t, want %t", tc.metadata, got, tc.want)
			}
		})
	}
}
