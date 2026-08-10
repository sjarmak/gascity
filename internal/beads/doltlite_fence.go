package beads

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, CGO_ENABLED=0 safe
)

type doltliteMetadata struct {
	Backend      string `json:"backend"`
	Database     string `json:"database"`
	DoltDatabase string `json:"dolt_database"`
}

// fenceMetadataKeyViaDoltliteSQLite performs the lifecycle value-CAS directly
// against DoltLite's physical SQLite store. bd sql intentionally refuses
// embedded mode, so this narrow writer is available in every build even when
// the optional native read-store acceleration is disabled.
func fenceMetadataKeyViaDoltliteSQLite(ctx context.Context, dir, id, key, expected, next string) (won bool, err error) {
	dbPath, err := doltliteFenceDBPath(dir)
	if err != nil {
		return false, fmt.Errorf("resolving doltlite store: %w", err)
	}
	busyTimeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < busyTimeout {
			busyTimeout = max(remaining, time.Millisecond)
		}
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=rw&_busy_timeout="+strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	if err != nil {
		return false, fmt.Errorf("opening doltlite store %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if closeErr := db.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing doltlite store %q after metadata fence: %w", dbPath, closeErr)
		}
	}()

	path := metadataJSONPath(key)
	rowLock := freshMetadataFenceRowLock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning doltlite metadata fence: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Commit or the returned operation error is authoritative.
	table := ""
	for _, candidate := range []string{"issues", "wisps"} {
		var exists int
		if queryErr := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, candidate,
		).Scan(&exists); queryErr != nil {
			return false, fmt.Errorf("checking doltlite %s table %q: %w", candidate, dbPath, queryErr)
		}
		if exists == 0 {
			continue
		}
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+candidate+` WHERE id = ?)`, id).Scan(&exists); queryErr != nil {
			return false, fmt.Errorf("locating doltlite %s row %q: %w", candidate, id, queryErr)
		}
		if exists != 0 {
			table = candidate
			break
		}
	}
	if table == "" {
		return false, fmt.Errorf("fencing metadata key on %q: %w", id, ErrNotFound)
	}
	result, execErr := tx.ExecContext(ctx,
		`UPDATE `+table+`
			 SET metadata = json_set(COALESCE(NULLIF(metadata, ''), '{}'), ?, ?),
			     row_lock = CASE WHEN row_lock = ? THEN ? ELSE ? END,
			     updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?
			   AND COALESCE(json_extract(COALESCE(NULLIF(metadata, ''), '{}'), ?), '') = ?`,
		path, next, rowLock, rowLock+1, rowLock, id, path, expected,
	)
	if execErr != nil {
		return false, fmt.Errorf("fencing metadata key in doltlite %s table %q: %w", table, dbPath, execErr)
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return false, fmt.Errorf("reading doltlite %s metadata fence result: %w", table, rowsErr)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing doltlite metadata fence: %w", err)
	}
	return rows > 0, nil
}

func doltliteFenceDBPath(dir string) (string, error) {
	meta, err := readDoltliteMetadata(dir)
	if err != nil {
		return "", err
	}
	dbName := strings.TrimSpace(meta.DoltDatabase)
	if dbName == "" || dbName == "doltlite" {
		dbName = strings.TrimSpace(meta.Database)
	}
	if dbName == "" || dbName == "doltlite" {
		dbName = "hq"
	}
	if dbName != filepath.Base(dbName) {
		return "", fmt.Errorf("metadata.json dolt_database %q must be a database name, not a path", dbName)
	}
	return filepath.Join(dir, ".beads", "doltlite", dbName+".db"), nil
}

func readDoltliteMetadata(dir string) (doltliteMetadata, error) {
	var meta doltliteMetadata
	data, err := os.ReadFile(filepath.Join(dir, ".beads", "metadata.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	if !isDoltliteMetadata(meta.Backend, meta.Database) {
		return meta, fmt.Errorf("not a doltlite beads store")
	}
	return meta, nil
}
