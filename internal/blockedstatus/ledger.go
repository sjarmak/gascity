package blockedstatus

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	migrationLedgerSchema   = "goal3.blocked-status-migration.v1"
	maxMigrationLedgerBytes = int64(16 << 20)
	maxMigrationLedgerRows  = 100000
	maxMigrationLedgerToken = 512
)

// LegacyPreimage is the audited raw lifecycle tuple and restoration status for
// one legacy blocked row.
type LegacyPreimage struct {
	Status    string
	Revision  int64
	IsBlocked bool
	Preimage  string
}

type migrationLedger struct {
	SchemaVersion     string                 `json:"schema_version"`
	Checked           bool                   `json:"checked"`
	GeneratedAt       string                 `json:"generated_at"`
	Database          string                 `json:"database"`
	StoreRef          string                 `json:"store_ref"`
	ProjectID         string                 `json:"project_id"`
	BlockedRows       int                    `json:"blocked_rows"`
	ClassifiedCount   int                    `json:"classified_count"`
	UnclassifiedCount int                    `json:"unclassified_count"`
	Complete          bool                   `json:"complete"`
	UnclassifiedIDs   []string               `json:"unclassified_ids"`
	Entries           []migrationLedgerEntry `json:"entries"`
}

type migrationLedgerEntry struct {
	ID               string                  `json:"id"`
	Status           string                  `json:"status"`
	Revision         string                  `json:"revision"`
	CurrentIsBlocked bool                    `json:"current_is_blocked"`
	Classification   string                  `json:"classification"`
	Preimage         string                  `json:"preimage"`
	Evidence         migrationLedgerEvidence `json:"evidence"`
}

type migrationLedgerEvidence struct {
	EventID      string `json:"event_id"`
	Actor        string `json:"actor"`
	TransitionAt string `json:"transition_at"`
}

// LoadLegacyPreimages validates a complete, canonical migration ledger and
// returns only its audited lifecycle classifications. Any ambiguity refuses
// the acting pass; callers never infer a preimage from current state.
func LoadLegacyPreimages(reader io.Reader, expectedStoreRef, expectedProjectID string, maxEntries int) (map[string]LegacyPreimage, error) {
	if maxEntries <= 0 || maxEntries > maxMigrationLedgerRows {
		return nil, fmt.Errorf("blocked-status migration ledger entry limit must be between 1 and %d", maxMigrationLedgerRows)
	}
	limited := &io.LimitedReader{R: reader, N: maxMigrationLedgerBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var ledger migrationLedger
	if err := decoder.Decode(&ledger); err != nil {
		return nil, fmt.Errorf("decode blocked-status migration ledger: %w", err)
	}
	if err := requireLedgerEOF(decoder); err != nil {
		return nil, err
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("blocked-status migration ledger exceeds %d bytes", maxMigrationLedgerBytes)
	}
	if ledger.SchemaVersion != migrationLedgerSchema || !ledger.Checked || !ledger.Complete ||
		ledger.Database != "gc" || ledger.StoreRef != expectedStoreRef ||
		strings.TrimSpace(expectedProjectID) == "" || ledger.ProjectID != expectedProjectID {
		return nil, fmt.Errorf("blocked-status migration ledger is not a complete checked gc ledger")
	}
	generatedAt, err := time.Parse(time.RFC3339, ledger.GeneratedAt)
	if err != nil || generatedAt.Format(time.RFC3339) != ledger.GeneratedAt {
		return nil, fmt.Errorf("blocked-status migration ledger generated_at is not canonical RFC3339")
	}
	if ledger.UnclassifiedCount != 0 || len(ledger.UnclassifiedIDs) != 0 {
		return nil, fmt.Errorf("blocked-status migration ledger has unclassified rows")
	}
	if ledger.BlockedRows != ledger.ClassifiedCount || ledger.ClassifiedCount != len(ledger.Entries) {
		return nil, fmt.Errorf("blocked-status migration ledger counts do not match entries")
	}
	if len(ledger.Entries) > maxEntries {
		return nil, fmt.Errorf("blocked-status migration ledger exceeds %d entries", maxEntries)
	}

	preimages := make(map[string]LegacyPreimage, len(ledger.Entries))
	events := make(map[string]struct{}, len(ledger.Entries))
	for _, entry := range ledger.Entries {
		revision, revisionErr := strconv.ParseInt(entry.Revision, 10, 64)
		if !validLedgerToken(entry.ID) || entry.Status != "blocked" || revisionErr != nil || revision == 0 ||
			entry.Classification != "historical_status_transition" {
			return nil, fmt.Errorf("blocked-status migration ledger contains malformed classification")
		}
		if entry.Preimage != "open" && entry.Preimage != "in_progress" {
			return nil, fmt.Errorf("blocked-status migration ledger has invalid preimage for %q", entry.ID)
		}
		if _, exists := preimages[entry.ID]; exists {
			return nil, fmt.Errorf("blocked-status migration ledger repeats issue %q", entry.ID)
		}
		if err := validateLedgerEvidence(entry.Evidence); err != nil {
			return nil, fmt.Errorf("blocked-status migration ledger evidence for %q: %w", entry.ID, err)
		}
		if _, exists := events[entry.Evidence.EventID]; exists {
			return nil, fmt.Errorf("blocked-status migration ledger repeats event %q", entry.Evidence.EventID)
		}
		events[entry.Evidence.EventID] = struct{}{}
		preimages[entry.ID] = LegacyPreimage{
			Status: entry.Status, Revision: revision,
			IsBlocked: entry.CurrentIsBlocked, Preimage: entry.Preimage,
		}
	}
	return preimages, nil
}

// LoadLegacyPreimagesFile refuses special files before opening them and caps
// the input before decoding, so operator-supplied paths cannot block on a FIFO
// or allocate without bound.
func LoadLegacyPreimagesFile(path, expectedStoreRef, expectedProjectID string, maxEntries int) (map[string]LegacyPreimage, error) {
	file, size, err := openRegularMigrationLedger(path)
	if err != nil {
		return nil, err
	}
	if size > maxMigrationLedgerBytes {
		_ = file.Close()
		return nil, fmt.Errorf("migration ledger exceeds %d bytes", maxMigrationLedgerBytes)
	}
	preimages, loadErr := LoadLegacyPreimages(file, expectedStoreRef, expectedProjectID, maxEntries)
	closeErr := file.Close()
	if loadErr != nil {
		return nil, loadErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close migration ledger: %w", closeErr)
	}
	return preimages, nil
}

func validLedgerToken(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed == value && len(value) <= maxMigrationLedgerToken
}

func requireLedgerEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing blocked-status migration ledger data: %w", err)
	}
	return fmt.Errorf("blocked-status migration ledger contains multiple JSON values")
}

func validateLedgerEvidence(evidence migrationLedgerEvidence) error {
	if !validLedgerToken(evidence.EventID) || !validLedgerToken(evidence.Actor) {
		return fmt.Errorf("event id and actor must be nonempty")
	}
	const layout = "2006-01-02 15:04:05"
	parsed, err := time.ParseInLocation(layout, evidence.TransitionAt, time.UTC)
	if err != nil || parsed.Format(layout) != evidence.TransitionAt {
		return fmt.Errorf("transition_at must use canonical UTC layout %s", layout)
	}
	return nil
}
