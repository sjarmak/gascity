package temporalmaintenance

import (
	"os"
	"strings"
	"testing"
)

func TestScheduledPromptsAreProposalOnly(t *testing.T) {
	tests := []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path: "prompts/review.md",
			required: []string{
				"EVERY OUTCOME IS PROPOSAL-ONLY",
				"Do NOT submit a GitHub review of any kind",
				"Do NOT post issue/PR comments",
				"exact head SHA",
			},
			forbidden: []string{
				"COMMENTS on an incoming\n     contributor PR is FINE — post them",
			},
		},
		{
			path: "prompts/author.md",
			required: []string{
				"auto_push=false",
				"BRANCH-READY ONLY",
				"Do NOT push, open or",
			},
			forbidden: []string{"auto_push=true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			body, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, want := range tt.required {
				if !strings.Contains(text, want) {
					t.Errorf("missing proposal-only policy %q", want)
				}
			}
			for _, bad := range tt.forbidden {
				if strings.Contains(text, bad) {
					t.Errorf("contains forbidden scheduled-write policy %q", bad)
				}
			}
		})
	}
}
