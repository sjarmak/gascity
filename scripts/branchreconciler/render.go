package branchreconciler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderJSON renders reports and their summary as a single JSON document,
// agent-consumable without a markdown parser.
func RenderJSON(reports []Report, summary Summary) ([]byte, error) {
	doc := struct {
		Summary Summary  `json:"summary"`
		Reports []Report `json:"reports"`
	}{Summary: summary, Reports: reports}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering report as JSON: %w", err)
	}
	return out, nil
}

// RenderMarkdown renders reports and their summary as a human-readable
// markdown table plus a one-line summary.
func RenderMarkdown(reports []Report, summary Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d branches, %d never appeared as a PR head ref, carrying %d commits not in main.\n\n",
		summary.TotalBranches, summary.NeverHadPR, summary.CommitsNeverPRd)
	b.WriteString("| Branch | Commits ahead of main | Ever had a PR |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, r := range reports {
		everHadPR := "no"
		if r.EverHadPR {
			everHadPR = "yes"
		}
		fmt.Fprintf(&b, "| %s | %d | %s |\n", r.Branch, r.CommitsAheadOfMain, everHadPR)
	}
	return b.String()
}
