package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestParseBdMigrateRecordArgs(t *testing.T) {
	req, ok, err := parseBdMigrateRecordArgs([]string{
		"migrate-record", "gc-123",
		"--actor", "operator@example",
		"--reason=approved reconciliation",
		"--expect-assignee", "old-session",
		"--assignee", "new-session",
		"--expect-metadata", beadmeta.RoutedToMetadataKey + "=old-route",
		"--clear-metadata", beadmeta.RoutedToMetadataKey,
		"--expect-metadata-absent", "reviewed",
		"--set-metadata", "reviewed=true",
	})
	if err != nil {
		t.Fatalf("parseBdMigrateRecordArgs: %v", err)
	}
	if !ok || req.ID != "gc-123" || req.Actor != "operator@example" || req.Reason != "approved reconciliation" {
		t.Fatalf("request = %#v, ok=%v", req, ok)
	}
	if req.ExpectedAssignee == nil || *req.ExpectedAssignee != "old-session" || req.Assignee == nil || *req.Assignee != "new-session" {
		t.Fatalf("assignee guards = (%v, %v)", req.ExpectedAssignee, req.Assignee)
	}
	if req.ExpectedMetadata[beadmeta.RoutedToMetadataKey] != "old-route" || len(req.ClearMetadata) != 1 || req.ClearMetadata[0] != beadmeta.RoutedToMetadataKey {
		t.Fatalf("metadata guards = %#v, clear = %#v", req.ExpectedMetadata, req.ClearMetadata)
	}
	if len(req.ExpectedMetadataAbsent) != 1 || req.ExpectedMetadataAbsent[0] != "reviewed" || req.Metadata["reviewed"] != "true" {
		t.Fatalf("absent metadata guards = %#v, replacements = %#v", req.ExpectedMetadataAbsent, req.Metadata)
	}

	if _, ok, err := parseBdMigrateRecordArgs([]string{"list"}); ok || err != nil {
		t.Fatalf("list parsed as migrate-record: ok=%v err=%v", ok, err)
	}
}

func TestParseBdMigrateRecordArgsRejectsUnsafeShapes(t *testing.T) {
	cases := [][]string{
		{"migrate-record"},
		{"migrate-record", "gc-1", "--reason", "missing actor", "--expect-assignee", "old", "--assignee", "new"},
		{"migrate-record", "gc-1", "--actor", "operator", "--expect-assignee", "old", "--assignee", "new"},
		{"migrate-record", "gc-1", "gc-2", "--actor", "operator", "--reason", "no bulk"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "missing expectation", "--assignee", "new"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "missing replacement", "--expect-assignee", "old"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "missing expectation", "--set-metadata", "key=value"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "conflict", "--expect-metadata", "key=old", "--set-metadata", "key=new", "--clear-metadata", "key"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "conflict", "--expect-metadata", "key=old", "--expect-metadata-absent", "key", "--set-metadata", "key=new"},
		{"migrate-record", "gc-1", "--actor", "operator", "--reason", "unknown", "--bogus", "value"},
	}
	for _, args := range cases {
		if _, ok, err := parseBdMigrateRecordArgs(args); !ok || err == nil {
			t.Errorf("parseBdMigrateRecordArgs(%q) = ok=%v err=%v, want migration usage error", args, ok, err)
		}
	}
}

func TestGcBdMigrateRecordUpdatesOneFileProviderRecordWithAudit(t *testing.T) {
	clearInheritedBeadsEnv(t)
	origCityFlag := cityFlag
	origRigFlag := rigFlag
	origNow := bdRecordMigrationNow
	defer func() {
		cityFlag = origCityFlag
		rigFlag = origRigFlag
		bdRecordMigrationNow = origNow
	}()
	cityFlag = ""
	rigFlag = ""
	bdRecordMigrationNow = func() time.Time {
		return time.Date(2026, 8, 5, 18, 0, 0, 0, time.UTC)
	}

	cityDir := t.TempDir()
	if err := os.WriteFile(cityDir+"/city.toml", []byte(`[workspace]
name = "demo"

[beads]
provider = "file"
`), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	setCwd(t, cityDir)
	t.Setenv("GC_CITY_PATH", cityDir)
	store, err := openScopeLocalFileStore(cityDir)
	if err != nil {
		t.Fatalf("openScopeLocalFileStore: %v", err)
	}
	created, err := store.Create(beads.Bead{
		Title:    "historical mail",
		Type:     "message",
		Assignee: "old-session",
		Metadata: beads.StringMap{beadmeta.RoutedToMetadataKey: "old-route"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := doBd([]string{
		"migrate-record", created.ID,
		"--actor", "operator@example",
		"--reason", "approved reconciliation",
		"--expect-assignee", "old-session",
		"--assignee", "new-session",
		"--expect-metadata", beadmeta.RoutedToMetadataKey + "=old-route",
		"--clear-metadata", beadmeta.RoutedToMetadataKey,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doBd = %d, stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "migrated "+created.ID {
		t.Fatalf("stdout = %q", stdout.String())
	}

	reopened, err := openScopeLocalFileStore(cityDir)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	got, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Assignee != "new-session" {
		t.Fatalf("migrated bead = %#v", got)
	}
	if _, present := got.Metadata[beadmeta.RoutedToMetadataKey]; present {
		t.Fatalf("migrated bead retained cleared route: %#v", got.Metadata)
	}
	var history []map[string]any
	if err := json.Unmarshal([]byte(got.Metadata[beadmeta.MigrationHistoryMetadataKey]), &history); err != nil {
		t.Fatalf("decode audit history: %v", err)
	}
	if len(history) != 1 || history[0]["actor"] != "operator@example" || history[0]["reason"] != "approved reconciliation" {
		t.Fatalf("history = %#v", history)
	}
}

func TestGcBdMigrateRecordRefusesStaleEvidenceWithoutMutation(t *testing.T) {
	clearInheritedBeadsEnv(t)
	origCityFlag := cityFlag
	origRigFlag := rigFlag
	defer func() {
		cityFlag = origCityFlag
		rigFlag = origRigFlag
	}()
	cityFlag = ""
	rigFlag = ""

	cityDir := t.TempDir()
	if err := os.WriteFile(cityDir+"/city.toml", []byte("[workspace]\nname = \"demo\"\n\n[beads]\nprovider = \"file\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	setCwd(t, cityDir)
	t.Setenv("GC_CITY_PATH", cityDir)
	store, err := openScopeLocalFileStore(cityDir)
	if err != nil {
		t.Fatalf("openScopeLocalFileStore: %v", err)
	}
	created, err := store.Create(beads.Bead{Title: "work", Assignee: "actual"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := doBd([]string{
		"migrate-record", created.ID,
		"--actor", "operator@example",
		"--reason", "stale evidence",
		"--expect-assignee", "stale",
		"--assignee", "new",
	}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "assignee changed") {
		t.Fatalf("doBd = %d, stderr=%q, want stale-evidence refusal", code, stderr.String())
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Assignee != "actual" || got.Metadata[beadmeta.MigrationHistoryMetadataKey] != "" {
		t.Fatalf("refused migration mutated bead: %#v", got)
	}
}
