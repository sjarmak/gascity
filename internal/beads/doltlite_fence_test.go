package beads

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFenceMetadataKeyUsesDoltliteSQLite(t *testing.T) {
	dir := t.TempDir()
	dbDir := filepath.Join(dir, ".beads", "doltlite")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "metadata.json"), []byte(`{"backend":"doltlite","dolt_database":"hq"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dbDir, "hq.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE issues (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE wisps (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO wisps (id, metadata) VALUES ('gc-session', '{"gc.drain_ack_token":"old"}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewBdStore(dir, func(_, name string, args ...string) ([]byte, error) {
		t.Fatalf("DoltLite fence invoked external command %s %v", name, args)
		return nil, nil
	})
	won, err := store.FenceMetadataKey("gc-session", "gc.drain_ack_token", "old", "new")
	if err != nil {
		t.Fatalf("FenceMetadataKey: %v", err)
	}
	if !won {
		t.Fatal("FenceMetadataKey did not win matching DoltLite value")
	}
	lost, err := store.FenceMetadataKey("gc-session", "gc.drain_ack_token", "old", "stale")
	if err != nil {
		t.Fatalf("stale FenceMetadataKey: %v", err)
	}
	if lost {
		t.Fatal("FenceMetadataKey won stale DoltLite value")
	}

	verify, err := sql.Open("sqlite", "file:"+filepath.Join(dbDir, "hq.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := verify.Close(); err != nil {
			t.Errorf("Close verification database: %v", err)
		}
	})
	var got string
	if err := verify.QueryRow(`SELECT json_extract(metadata, '$."gc.drain_ack_token"') FROM wisps WHERE id = 'gc-session'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "new" {
		t.Fatalf("metadata token = %q, want new", got)
	}
	var rowLock int64
	if err := verify.QueryRow(`SELECT row_lock FROM wisps WHERE id = 'gc-session'`).Scan(&rowLock); err != nil {
		t.Fatal(err)
	}
	if rowLock == 0 {
		t.Fatal("row_lock was not reminted by the direct-SQL fence")
	}
}

func TestFenceMetadataKeyDoltliteUsesIssuesTierWhenIDsCollide(t *testing.T) {
	dir := t.TempDir()
	dbDir := filepath.Join(dir, ".beads", "doltlite")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "metadata.json"), []byte(`{"backend":"doltlite","dolt_database":"hq"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dbDir, "hq.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE issues (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE wisps (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO issues (id, metadata) VALUES ('gc-collision', '{"fence":"old"}')`,
		`INSERT INTO wisps (id, metadata) VALUES ('gc-collision', '{"fence":"old"}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store := NewBdStore(dir, func(_, name string, args ...string) ([]byte, error) {
		t.Fatalf("DoltLite fence invoked external command %s %v", name, args)
		return nil, nil
	})
	if won, err := store.FenceMetadataKey("gc-collision", "fence", "old", "new"); err != nil || !won {
		t.Fatalf("FenceMetadataKey = (%v, %v), want (true, nil)", won, err)
	}
	verify, err := sql.Open("sqlite", "file:"+filepath.Join(dbDir, "hq.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close() //nolint:errcheck // test cleanup
	for table, want := range map[string]string{"issues": "new", "wisps": "old"} {
		var got string
		if err := verify.QueryRow(`SELECT json_extract(metadata, '$.fence') FROM ` + table + ` WHERE id = 'gc-collision'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s fence = %q, want %q", table, got, want)
		}
	}
}

func TestFenceMetadataKeyDoltliteAllowsMissingOptionalWispsTable(t *testing.T) {
	dir := t.TempDir()
	dbDir := filepath.Join(dir, ".beads", "doltlite")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "metadata.json"), []byte(`{"backend":"doltlite","dolt_database":"hq"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dbDir, "hq.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE issues (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO issues (id, metadata) VALUES ('gc-session', '{"fence":"old"}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store := NewBdStore(dir, func(_, name string, args ...string) ([]byte, error) {
		t.Fatalf("DoltLite fence invoked external command %s %v", name, args)
		return nil, nil
	})
	won, err := store.FenceMetadataKey("gc-session", "fence", "old", "new")
	if err != nil || !won {
		t.Fatalf("FenceMetadataKey = (%v, %v), want (true, nil)", won, err)
	}
}

func TestFenceMetadataKeyDoltliteHonorsStoreContext(t *testing.T) {
	dir := t.TempDir()
	dbDir := filepath.Join(dir, ".beads", "doltlite")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "metadata.json"), []byte(`{"backend":"doltlite","dolt_database":"hq"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "hq.db")
	locker, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	for _, stmt := range []string{
		`CREATE TABLE issues (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE wisps (id TEXT PRIMARY KEY, metadata TEXT, updated_at TEXT, row_lock INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO wisps (id, metadata) VALUES ('gc-session', '{"fence":"old"}')`,
		`BEGIN EXCLUSIVE`,
	} {
		if _, err := locker.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = locker.Exec(`ROLLBACK`) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	store := NewBdStore(dir, func(_, name string, args ...string) ([]byte, error) {
		t.Fatalf("DoltLite fence invoked external command %s %v", name, args)
		return nil, nil
	}, WithBdStoreContext(ctx))
	started := time.Now()
	_, err = store.FenceMetadataKey("gc-session", "fence", "old", "new")
	if err == nil {
		t.Fatal("FenceMetadataKey succeeded while DoltLite was exclusively locked")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FenceMetadataKey ignored context for %s", elapsed)
	}
}
