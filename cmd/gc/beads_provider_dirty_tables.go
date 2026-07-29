package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Recovery from beads' dirty-table schema-migration refusal.
//
// Every beads store open runs pending schema migrations, and the migration
// runner refuses to alter a table that already carries uncommitted working-set
// changes — it will not entangle a migration's DDL with unrelated dirty rows
// (github.com/steveyegge/beads internal/storage/schema.DirtyTablesError). The
// refusal prescribes `bd dolt commit` as the remedy, but that command also
// opens the store and hits the same refusal before its commit can clear the
// dirty state: the prescribed remedy is circular.
//
// Upstream broke that deadlock in beads#4567, but only for the embedded
// backend — cmd/bd's newDoltStore returns on cfg.ServerMode before it ever
// reaches the cfg.LenientOpen branch that opens past the guard, so a
// server-mode store (what `bd init --server` and therefore every `gc rig add`
// uses) still has no in-tool escape. The dirty working set is not exotic
// either: a healthy managed database normally sits with `config` modified, so
// the next schema bump meets this guard by default.
//
// This path runs both from `gc rig add` (which creates the database) and from
// ordinary lifecycle startup (which re-inits the city and every configured rig
// against databases that already exist). Committing the working set is only ours
// to do in the first case: a database this invocation created holds nothing but
// init's own writes, while a pre-existing one may carry live agent/operator
// state that a bulk `DOLT_COMMIT('-A')` must not silently record. Recovery
// therefore commits directly through Dolt — bypassing bd, whose own open would
// hit the refusal first — only when (a) the failing scope's own endpoint is the
// managed-city server, and (b) this invocation created the database.

// maxBdInitDirtyTableRounds bounds the commit -> re-init recovery loop. One
// pass is not always enough: bd init makes partial progress and re-dirties
// tables as it goes, and the dirty set observed in the field shrank 9 -> 1 -> 0
// across two rounds. The bound keeps a database that never converges from
// spinning forever.
const maxBdInitDirtyTableRounds = 5

// bdInitDirtyTablesMarker is the stable part of beads' refusal text. It matches
// both the plain ("pending schema migrations alter pre-existing dirty tables")
// and ignored-migration ("pending ignored schema migrations alter ...") forms,
// and survives the wrapping every exec provider adds around bd's output.
const bdInitDirtyTablesMarker = "alter pre-existing dirty tables"

// dirtyScopeTablesCommitMessage is the Dolt commit message recorded for a
// working set committed by this recovery. It is a fixed literal with no quote
// characters so it needs no SQL escaping.
const dirtyScopeTablesCommitMessage = "gc: commit working set before beads schema migration"

// dirtyTablesRecoveryScope identifies the scope whose bd init hit the dirty-table
// deadlock and carries what recovery needs to act safely on the shared managed
// Dolt server:
//
//   - scopeRoot lets recovery resolve the scope's OWN Dolt endpoint rather than
//     assuming the city's — a managed city can host an explicitly external rig,
//     whose same-named database on the local server is a different database we
//     must not touch.
//   - createdThisInvocation records whether this init is creating the database.
//     Only then is its whole working set init's own writes and safe to
//     bulk-commit; a pre-existing database may hold live working-set state.
//   - creationProbeErr is non-nil when the pre-init catalog probe could not
//     determine whether the database already existed. Recovery still declines
//     (the safe default) but reports the probe failure rather than
//     misattributing the decline to a pre-existing database.
type dirtyTablesRecoveryScope struct {
	cityPath              string
	scopeRoot             string
	database              string
	createdThisInvocation bool
	creationProbeErr      error
}

// isBdInitDirtyTablesError reports whether err is beads' refusal to migrate
// tables that already have uncommitted working-set changes.
func isBdInitDirtyTablesError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), bdInitDirtyTablesMarker)
}

// commitDirtyScopeTables commits a scope database's uncommitted working set on
// the managed Dolt server, reporting whether there was anything to commit. It
// is a variable so tests can exercise the recovery loop without a live server.
var commitDirtyScopeTables = commitDirtyScopeTablesViaManagedDolt

// commitDirtyScopeTablesViaManagedDolt applies the remedy that actually clears
// the guard: commit the dirty tables straight through Dolt rather than through
// bd, whose own open would hit the refusal first. It reports false when the
// database has no dirty tables, which tells the caller that committing cannot
// make further progress.
//
// It only acts on the managed-city Dolt server, and only when the failing
// scope's OWN endpoint resolves to that server: an external or gateway endpoint
// is not ours to commit against, and its working set is not what bd is refusing
// over here.
func commitDirtyScopeTablesViaManagedDolt(scope dirtyTablesRecoveryScope) (bool, error) {
	database := strings.TrimSpace(scope.database)
	if database == "" {
		return false, fmt.Errorf("no Dolt database resolved for scope")
	}
	if !cityUsesBdStoreContract(scope.cityPath) {
		return false, fmt.Errorf("city %q does not use the bd store contract", scope.cityPath)
	}
	if err := ensureScopeDoltIsManagedCity(scope.cityPath, scope.scopeRoot); err != nil {
		return false, err
	}
	port := currentResolvableManagedDoltPort(scope.cityPath)
	if strings.TrimSpace(port) == "" {
		return false, fmt.Errorf("no managed Dolt port resolvable for city %q", scope.cityPath)
	}

	dirty, err := managedDoltDirtyTableCount("", port, "root", database)
	if err != nil {
		return false, err
	}
	if dirty == 0 {
		return false, nil
	}

	commitSQL := "USE " + managedDoltQuoteIdent(database) + "; " +
		"CALL DOLT_COMMIT('-A', '-m', '" + dirtyScopeTablesCommitMessage + "');"
	if _, err := runManagedDoltSQL("", port, "root", "-q", commitSQL); err != nil {
		return false, fmt.Errorf("committing dirty tables in Dolt database %q: %w", database, err)
	}
	return true, nil
}

// ensureScopeDoltIsManagedCity confirms the failing scope's canonical Dolt
// endpoint is the managed-city server that recovery would commit against.
//
// The old check gated only on the CITY's externality, so a managed city hosting
// an explicitly external rig passed it and then committed a same-named database
// on the local server — the wrong database. Resolving the scope's own target
// closes that gap: an authoritative external scope is declined here, and a scope
// that inherits the city (non-authoritative) falls back to the city-level gate.
func ensureScopeDoltIsManagedCity(cityPath, scopeRoot string) error {
	if strings.TrimSpace(scopeRoot) != "" {
		target, ok, err := canonicalScopeDoltTarget(cityPath, scopeRoot)
		if err != nil {
			return fmt.Errorf("resolving Dolt endpoint for scope %s: %w", scopeRoot, err)
		}
		if ok && target.External {
			return fmt.Errorf("dolt endpoint for scope %s is external; commit its working set at that endpoint, not the managed-city server", scopeRoot)
		}
	}
	if isExternalDolt(cityPath) {
		return fmt.Errorf("dolt endpoint for city %q is external; commit its working set at the endpoint", cityPath)
	}
	return nil
}

// managedDoltDirtyTableCount returns how many tables in database have
// uncommitted working-set changes.
func managedDoltDirtyTableCount(host, port, user, database string) (int, error) {
	out, err := runManagedDoltSQL(host, port, user, "-r", "csv", "-q",
		"SELECT COUNT(*) AS cnt FROM "+managedDoltQuoteIdent(database)+".dolt_status")
	if err != nil {
		return 0, fmt.Errorf("reading dolt_status for Dolt database %q: %w", database, err)
	}
	n, err := parseSmokeCount(out)
	if err != nil {
		return 0, fmt.Errorf("reading dolt_status for Dolt database %q: %w", database, err)
	}
	return n, nil
}

// parseSmokeCount extracts the integer from `SELECT COUNT(*)` csv output by
// taking the last numeric line (the value row, after the "cnt" header).
// Inlined here so this PR is self-contained on upstream (the fork-only
// maintenance_dolt_ops.go helper is not available upstream).
func parseSmokeCount(out string) (int, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return 0, fmt.Errorf("parse smoke count %q: %w", line, err)
		}
		return n, nil
	}
	return 0, fmt.Errorf("empty smoke count output")
}

// managedDoltDatabaseExistsBeforeInit reports whether database already exists in
// the managed-city Dolt catalog. It must be captured BEFORE bd init runs: init
// creates the database, so a probe taken afterwards can no longer distinguish a
// database this invocation created (its whole working set is init's own writes,
// safe to bulk-commit) from a pre-existing one (whose working set may hold live
// agent/operator state). A var so tests can stub it without a live server.
var managedDoltDatabaseExistsBeforeInit = func(cityPath, database string) (bool, error) {
	database = strings.TrimSpace(database)
	if database == "" {
		return false, nil
	}
	if !cityUsesBdStoreContract(cityPath) {
		return false, nil
	}
	port := currentResolvableManagedDoltPort(cityPath)
	if strings.TrimSpace(port) == "" {
		return false, nil
	}
	dbs, err := managedDoltListUserDatabasesAfterInit(port)
	if err != nil {
		return false, err
	}
	for _, d := range dbs {
		if strings.EqualFold(d, database) {
			return true, nil
		}
	}
	return false, nil
}

// recoverBdInitFromDirtyTables clears the dirty-table deadlock that initErr
// reports and re-runs init, repeating until init stops refusing. reinit re-runs
// the same bd init the caller just attempted.
//
// It returns nil once init succeeds. Otherwise it returns an error that wraps
// the refusal, so a caller that cannot be helped still sees why.
func recoverBdInitFromDirtyTables(scope dirtyTablesRecoveryScope, initErr error, reinit func() error) error {
	database := strings.TrimSpace(scope.database)
	if database == "" {
		return initErr
	}
	if !scope.createdThisInvocation {
		// The recovery below commits the whole working set with DOLT_COMMIT('-A'),
		// which is only ours to do for a database this invocation created. On a
		// pre-existing database — every ordinary city/rig startup re-inits — that
		// working set may hold live agent/operator changes, so surface actionable
		// guidance instead of silently recording them.
		if scope.creationProbeErr != nil {
			// The pre-init probe failed, so we never confirmed the database was
			// this invocation's to commit. Declining is still the safe default,
			// but report the probe failure rather than claiming the database
			// pre-existed — which we do not actually know.
			return fmt.Errorf("%w; could not determine whether the Dolt database %q was created by this init (catalog probe failed: %w), so its working set is not ours to bulk-commit — resolve the probe failure, then re-run",
				initErr, database, scope.creationProbeErr)
		}
		return fmt.Errorf("%w; the Dolt database %q pre-existed this init, so its working set is not ours to bulk-commit — commit or discard it at the managed endpoint, then re-run",
			initErr, database)
	}

	err := initErr
	for round := 0; round < maxBdInitDirtyTableRounds; round++ {
		committed, commitErr := commitDirtyScopeTables(scope)
		if commitErr != nil {
			return fmt.Errorf("%w; committing the working set to clear it failed: %w", err, commitErr)
		}
		if !committed {
			// Nothing left to commit yet init still refuses — another commit
			// would change nothing, so stop rather than spin.
			return err
		}
		retryErr := reinit()
		if retryErr == nil {
			return nil
		}
		if !isBdInitDirtyTablesError(retryErr) {
			return retryErr
		}
		err = retryErr
	}
	return fmt.Errorf("bd init still reports dirty tables in Dolt database %q after %d commit rounds: %w",
		database, maxBdInitDirtyTableRounds, err)
}
