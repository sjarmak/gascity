//go:build gascity_native_beads

package beads

import (
	"database/sql"
	"fmt"
	"testing"
)

// fakeBlockedRowScanner feeds scanBead a single issues row with a chosen raw
// status, matching scanBead's exact column order, so the status->marker fold can
// be exercised without a live DoltLite snapshot.
type fakeBlockedRowScanner struct {
	status string
}

func (r fakeBlockedRowScanner) Scan(dest ...any) error {
	vals := []any{
		"gc-doltlite-blocked",       // id
		"parked work",               // title
		r.status,                    // status (raw, pre-fold)
		"task",                      // type
		sql.NullInt64{},             // priority
		any("2026-07-31T00:00:00Z"), // createdRaw
		any("2026-07-31T00:00:00Z"), // updatedRaw
		"",                          // assignee
		"",                          // description
		"{}",                        // metadataRaw
		"",                          // parentID
		int64(0),                    // ephemeral
		int64(0),                    // noHistory
	}
	if len(dest) != len(vals) {
		return fmt.Errorf("scanBead requested %d columns, fixture supplies %d", len(dest), len(vals))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = vals[i].(string)
		case *sql.NullInt64:
			*p = vals[i].(sql.NullInt64)
		case *any:
			*p = vals[i]
		case *int64:
			*p = vals[i].(int64)
		default:
			return fmt.Errorf("unexpected scanBead dest type %T at column %d", d, i)
		}
	}
	return nil
}

func TestDoltliteScanBeadPreservesBlockedStatusMarker(t *testing.T) {
	// mapBdStatus folds a raw status="blocked" to "open". DoltLite's cache
	// full-scan List() does not filter status, so without preserving the marker
	// a parked bead is absorbed as claimable and demand loops against a bead
	// gc hook --claim rejects.
	blocked, err := scanBead(fakeBlockedRowScanner{status: "blocked"})
	if err != nil {
		t.Fatalf("scanBead blocked: %v", err)
	}
	if blocked.Status != "open" {
		t.Fatalf("Status = %q, want folded open", blocked.Status)
	}
	if blocked.IsBlocked == nil || !*blocked.IsBlocked {
		t.Fatalf("IsBlocked = %v, want true wire marker", blocked.IsBlocked)
	}
	if !IsSelfBlockedBead(blocked) {
		t.Fatal("IsSelfBlockedBead = false, want true for a status-blocked DoltLite row")
	}

	open, err := scanBead(fakeBlockedRowScanner{status: "open"})
	if err != nil {
		t.Fatalf("scanBead open: %v", err)
	}
	if open.IsBlocked != nil {
		t.Fatalf("IsBlocked = %v, want nil for an open row", *open.IsBlocked)
	}
	if IsSelfBlockedBead(open) {
		t.Fatal("IsSelfBlockedBead = true, want false for an open DoltLite row")
	}
}
