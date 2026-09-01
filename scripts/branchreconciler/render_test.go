package branchreconciler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderJSONRoundTrips(t *testing.T) {
	reports := []Report{
		{Branch: "fix/lost", CommitsAheadOfMain: 12, EverHadPR: false},
		{Branch: "fix/shipped", CommitsAheadOfMain: 5, EverHadPR: true},
	}
	summary := Summarize(reports)

	out, err := RenderJSON(reports, summary)
	if err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}

	var decoded struct {
		Summary Summary  `json:"summary"`
		Reports []Report `json:"reports"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("RenderJSON() output did not parse: %v\noutput: %s", err, out)
	}
	if decoded.Summary != summary {
		t.Fatalf("decoded summary = %+v, want %+v", decoded.Summary, summary)
	}
	if len(decoded.Reports) != 2 || decoded.Reports[0].Branch != "fix/lost" {
		t.Fatalf("decoded reports = %+v", decoded.Reports)
	}
}

func TestRenderMarkdownIncludesEveryBranchAndSummaryLine(t *testing.T) {
	reports := []Report{
		{Branch: "fix/lost", CommitsAheadOfMain: 12, EverHadPR: false},
		{Branch: "fix/shipped", CommitsAheadOfMain: 5, EverHadPR: true},
	}
	summary := Summarize(reports)

	out := RenderMarkdown(reports, summary)

	for _, want := range []string{"fix/lost", "fix/shipped", "12", "5", "2 branches", "1 never appeared"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderMarkdown() missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderMarkdownEmptyReports(t *testing.T) {
	out := RenderMarkdown(nil, Summary{})
	if !strings.Contains(out, "0 branches") {
		t.Fatalf("RenderMarkdown(nil) = %q, want it to report 0 branches", out)
	}
}
