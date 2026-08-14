package blockedstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validLedgerJSON = `{
	"schema_version":"goal3.blocked-status-migration.v1",
	"checked":true,
	"generated_at":"2026-08-14T05:26:11Z",
	"database":"gc",
	"store_ref":"city:ds-research",
	"project_id":"project-1",
	"blocked_rows":2,
	"classified_count":2,
	"unclassified_count":0,
	"complete":true,
	"unclassified_ids":[],
	"entries":[
		{"id":"dr-a","status":"blocked","revision":"11","current_is_blocked":false,"classification":"historical_status_transition","preimage":"open","evidence":{"event_id":"ev-a","actor":"mayor","transition_at":"2026-08-01 01:02:03"}},
		{"id":"dr-b","status":"blocked","revision":"12","current_is_blocked":true,"classification":"historical_status_transition","preimage":"in_progress","evidence":{"event_id":"ev-b","actor":"goal-3","transition_at":"2026-08-02 02:03:04"}}
	]
}`

func TestLoadLegacyPreimagesAcceptsCompleteAuditedLedger(t *testing.T) {
	t.Parallel()

	got, err := LoadLegacyPreimages(strings.NewReader(validLedgerJSON), "city:ds-research", "project-1", 100)
	if err != nil {
		t.Fatalf("LoadLegacyPreimages: %v", err)
	}
	if got["dr-a"].Preimage != "open" || got["dr-a"].Revision != 11 ||
		got["dr-b"].Preimage != "in_progress" || !got["dr-b"].IsBlocked || len(got) != 2 {
		t.Fatalf("preimages = %#v", got)
	}
}

func TestLoadLegacyPreimagesExercisesEveryFailClosedValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"noncanonical generated time": strings.Replace(validLedgerJSON, "2026-08-14T05:26:11Z", "2026-08-14T05:26:11+00:00", 1),
		"unclassified count":          strings.Replace(validLedgerJSON, `"unclassified_count":0`, `"unclassified_count":1`, 1),
		"count mismatch":              strings.Replace(validLedgerJSON, `"blocked_rows":2`, `"blocked_rows":3`, 1),
		"bad status":                  strings.Replace(validLedgerJSON, `"status":"blocked"`, `"status":"open"`, 1),
		"zero revision":               strings.Replace(validLedgerJSON, `"revision":"11"`, `"revision":"0"`, 1),
		"bad revision":                strings.Replace(validLedgerJSON, `"revision":"11"`, `"revision":"nope"`, 1),
		"bad classification":          strings.Replace(validLedgerJSON, `"classification":"historical_status_transition"`, `"classification":"guess"`, 1),
		"bad preimage":                strings.Replace(validLedgerJSON, `"preimage":"open"`, `"preimage":"closed"`, 1),
		"duplicate id":                strings.Replace(validLedgerJSON, `"id":"dr-b"`, `"id":"dr-a"`, 1),
		"blank event":                 strings.Replace(validLedgerJSON, `"event_id":"ev-a"`, `"event_id":" "`, 1),
		"blank actor":                 strings.Replace(validLedgerJSON, `"actor":"mayor"`, `"actor":" "`, 1),
		"untrimmed event":             strings.Replace(validLedgerJSON, `"event_id":"ev-a"`, `"event_id":" ev-a"`, 1),
		"untrimmed actor":             strings.Replace(validLedgerJSON, `"actor":"mayor"`, `"actor":"mayor "`, 1),
		"bad transition time":         strings.Replace(validLedgerJSON, "2026-08-01 01:02:03", "not-a-time", 1),
		"duplicate event":             strings.Replace(validLedgerJSON, `"event_id":"ev-b"`, `"event_id":"ev-a"`, 1),
		"unknown field":               strings.Replace(validLedgerJSON, `"checked":true`, `"checked":true,"surprise":1`, 1),
		"trailing value":              validLedgerJSON + ` {}`,
	}
	for name, input := range tests {
		input := input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadLegacyPreimages(strings.NewReader(input), "city:ds-research", "project-1", 100); err == nil {
				t.Fatal("LoadLegacyPreimages error = nil, want refusal")
			}
		})
	}
	if _, err := LoadLegacyPreimages(strings.NewReader(validLedgerJSON), "city:ds-research", "project-1", 1); err == nil {
		t.Fatal("entry limit error = nil, want refusal")
	}
	if _, err := LoadLegacyPreimages(strings.NewReader(validLedgerJSON), "city:ds-research", "project-1", 0); err == nil {
		t.Fatal("invalid caller limit error = nil, want refusal")
	}
}

func TestLoadLegacyPreimagesFailsClosedOnIncompleteOrMalformedLedger(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unchecked":      `{"schema_version":"goal3.blocked-status-migration.v1","checked":false,"complete":true}`,
		"incomplete":     `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"complete":false}`,
		"wrong database": `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"complete":true,"database":"not-gc"}`,
		"count mismatch": `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"complete":true,"database":"gc","blocked_rows":2,"classified_count":1,"unclassified_count":0,"entries":[{"id":"dr-a","classification":"historical_status_transition","preimage":"open","evidence":{"event_id":"ev-a","actor":"mayor","transition_at":"2026-08-01 01:02:03"}}]}`,
		"duplicate id":   `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"complete":true,"database":"gc","blocked_rows":2,"classified_count":2,"unclassified_count":0,"entries":[{"id":"dr-a","classification":"historical_status_transition","preimage":"open","evidence":{"event_id":"ev-a","actor":"mayor","transition_at":"2026-08-01 01:02:03"}},{"id":"dr-a","classification":"historical_status_transition","preimage":"open","evidence":{"event_id":"ev-b","actor":"mayor","transition_at":"2026-08-01 01:02:03"}}]}`,
		"bad preimage":   `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"complete":true,"database":"gc","blocked_rows":1,"classified_count":1,"unclassified_count":0,"entries":[{"id":"dr-a","classification":"historical_status_transition","preimage":"closed","evidence":{"event_id":"ev-a","actor":"mayor","transition_at":"2026-08-01 01:02:03"}}]}`,
	}
	for name, input := range tests {
		input := input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadLegacyPreimages(strings.NewReader(input), "city:ds-research", "project-1", 100); err == nil {
				t.Fatal("LoadLegacyPreimages error = nil, want fail-closed refusal")
			}
		})
	}
}

func TestLoadLegacyPreimagesRejectsWrongStore(t *testing.T) {
	t.Parallel()

	input := `{"schema_version":"goal3.blocked-status-migration.v1","checked":true,"generated_at":"2026-08-14T05:26:11Z","database":"gc","store_ref":"city:other","project_id":"project-1","blocked_rows":0,"classified_count":0,"unclassified_count":0,"complete":true,"unclassified_ids":[],"entries":[]}`
	if _, err := LoadLegacyPreimages(strings.NewReader(input), "city:ds-research", "project-1", 100); err == nil {
		t.Fatal("LoadLegacyPreimages error = nil, want wrong-store refusal")
	}
}

func TestLoadLegacyPreimagesFileRejectsNonRegularAndOversizedInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oversized := filepath.Join(dir, "oversized.json")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxMigrationLedgerBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyPreimagesFile(oversized, "city:ds-research", "project-1", 100); err == nil {
		t.Fatal("oversized error = nil, want refusal")
	}
}

func TestLoadLegacyPreimagesFileAcceptsBoundedRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(path, []byte(validLedgerJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLegacyPreimagesFile(path, "city:ds-research", "project-1", 100)
	if err != nil {
		t.Fatalf("LoadLegacyPreimagesFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("preimages = %#v", got)
	}

	symlink := filepath.Join(filepath.Dir(path), "ledger-link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyPreimagesFile(symlink, "city:ds-research", "project-1", 100); err == nil {
		t.Fatal("symlink error = nil, want no-follow refusal")
	}
}
