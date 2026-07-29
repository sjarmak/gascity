package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bdDirtyTablesErrText is the refusal beads emits (verbatim, modulo the table
// list) when pending schema migrations would alter tables that already carry
// uncommitted working-set changes. Reproduced here so the classifier is tested
// against the real upstream text rather than a paraphrase of it.
const bdDirtyTablesErrText = "bd init: pending schema migrations alter pre-existing dirty tables: " +
	"config, dependencies; run 'bd dolt commit' to commit the working set at the current schema, " +
	"then re-run the migration (gastownhall/beads#4566)"

func TestIsBdInitDirtyTablesError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "verbatim beads refusal", err: errors.New(bdDirtyTablesErrText), want: true},
		{
			name: "ignored-migration variant",
			err: errors.New("pending ignored schema migrations alter pre-existing dirty tables: " +
				"issue_snapshots"),
			want: true,
		},
		{
			name: "wrapped by an exec provider",
			err:  fmt.Errorf("gc-beads-bd init: %w", errors.New(bdDirtyTablesErrText)),
			want: true,
		},
		{
			name: "unrelated migration failure",
			err:  errors.New("bd init: Unknown column 'agent_state' in 'issues'"),
			want: false,
		},
		{name: "already initialized", err: errors.New("bd init: already initialized"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBdInitDirtyTablesError(tt.err); got != tt.want {
				t.Fatalf("isBdInitDirtyTablesError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// stubCommitDirtyScopeTables swaps the managed-Dolt commit seam for the test's
// own and restores it afterwards.
func stubCommitDirtyScopeTables(t *testing.T, fn func(scope dirtyTablesRecoveryScope) (bool, error)) {
	t.Helper()
	prev := commitDirtyScopeTables
	commitDirtyScopeTables = fn
	t.Cleanup(func() { commitDirtyScopeTables = prev })
}

// stubManagedDoltDatabaseExistsBeforeInit swaps the pre-init catalog probe for
// the test's own and restores it afterwards, so a test can pin whether the
// database "already existed" without a live Dolt server.
func stubManagedDoltDatabaseExistsBeforeInit(t *testing.T, fn func(cityPath, database string) (bool, error)) {
	t.Helper()
	prev := managedDoltDatabaseExistsBeforeInit
	managedDoltDatabaseExistsBeforeInit = fn
	t.Cleanup(func() { managedDoltDatabaseExistsBeforeInit = prev })
}

// The dirty set observed in the field shrank 9 -> 1 -> 0: bd init makes partial
// progress each pass and re-dirties tables, so a single commit does not clear
// it. Recovery must keep committing until init stops refusing.
func TestRecoverBdInitFromDirtyTablesLoopsUntilClean(t *testing.T) {
	var commits int
	stubCommitDirtyScopeTables(t, func(scope dirtyTablesRecoveryScope) (bool, error) {
		commits++
		if scope.database != "gsp" {
			t.Fatalf("commit database = %q, want %q", scope.database, "gsp")
		}
		return true, nil
	})

	var reinits int
	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", scopeRoot: "/city/rig", database: "gsp", createdThisInvocation: true},
		errors.New(bdDirtyTablesErrText), func() error {
			reinits++
			if reinits < 2 {
				return errors.New(bdDirtyTablesErrText)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("recoverBdInitFromDirtyTables() = %v, want nil", err)
	}
	if commits != 2 {
		t.Fatalf("commit rounds = %d, want 2", commits)
	}
	if reinits != 2 {
		t.Fatalf("re-init attempts = %d, want 2", reinits)
	}
}

// A commit that reports nothing left to commit cannot make further progress:
// looping again would spin against an unchanged database.
func TestRecoverBdInitFromDirtyTablesStopsWhenNothingLeftToCommit(t *testing.T) {
	initErr := errors.New(bdDirtyTablesErrText)
	var commits, reinits int
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		commits++
		return false, nil
	})

	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", database: "gsp", createdThisInvocation: true},
		initErr, func() error {
			reinits++
			return nil
		})
	if !errors.Is(err, initErr) {
		t.Fatalf("error = %v, want the original init error", err)
	}
	if commits != 1 {
		t.Fatalf("commit rounds = %d, want 1", commits)
	}
	if reinits != 0 {
		t.Fatalf("re-init attempts = %d, want 0", reinits)
	}
}

// A non-dirty-table failure on the retry is the real outcome and must surface
// unchanged rather than being retried as if it were still the deadlock.
func TestRecoverBdInitFromDirtyTablesSurfacesUnrelatedRetryFailure(t *testing.T) {
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) { return true, nil })

	other := errors.New("bd init: connection refused")
	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", database: "gsp", createdThisInvocation: true},
		errors.New(bdDirtyTablesErrText), func() error {
			return other
		})
	if !errors.Is(err, other) {
		t.Fatalf("error = %v, want %v", err, other)
	}
}

// When committing itself fails, the operator needs both the refusal and the
// reason the documented remedy could not be applied.
func TestRecoverBdInitFromDirtyTablesSurfacesCommitFailure(t *testing.T) {
	initErr := errors.New(bdDirtyTablesErrText)
	commitErr := errors.New("connect to managed Dolt: dial tcp: connection refused")
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) { return false, commitErr })

	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", database: "gsp", createdThisInvocation: true},
		initErr, func() error {
			t.Fatal("re-init must not run after a failed commit")
			return nil
		})
	if !errors.Is(err, initErr) {
		t.Fatalf("error = %v, want it to wrap the init refusal", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("error = %v, want it to wrap the commit failure", err)
	}
}

// A database that never stops refusing must not spin forever.
func TestRecoverBdInitFromDirtyTablesBoundsRounds(t *testing.T) {
	var commits int
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		commits++
		return true, nil
	})

	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", database: "gsp", createdThisInvocation: true},
		errors.New(bdDirtyTablesErrText), func() error {
			return errors.New(bdDirtyTablesErrText)
		})
	if err == nil {
		t.Fatal("recoverBdInitFromDirtyTables() = nil, want an error")
	}
	if commits != maxBdInitDirtyTableRounds {
		t.Fatalf("commit rounds = %d, want %d", commits, maxBdInitDirtyTableRounds)
	}
}

// An empty database name leaves nothing to commit against, so recovery must
// decline rather than guess at a target.
func TestRecoverBdInitFromDirtyTablesWithoutDatabase(t *testing.T) {
	initErr := errors.New(bdDirtyTablesErrText)
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		t.Fatal("commit must not run without a database name")
		return false, nil
	})

	if err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", database: "  ", createdThisInvocation: true},
		initErr, func() error {
			t.Fatal("re-init must not run without a database name")
			return nil
		}); !errors.Is(err, initErr) {
		t.Fatalf("error = %v, want the original init error", err)
	}
}

// A database that pre-existed this init is not ours to bulk-commit: recovery
// must decline (surfacing the refusal) rather than run DOLT_COMMIT('-A') over a
// working set that may hold live agent/operator state (synthesis finding #2).
func TestRecoverBdInitFromDirtyTablesRefusesPreExistingDatabase(t *testing.T) {
	initErr := errors.New(bdDirtyTablesErrText)
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		t.Fatal("commit must not run for a pre-existing database")
		return false, nil
	})

	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", scopeRoot: "/city/rig", database: "gsp", createdThisInvocation: false},
		initErr, func() error {
			t.Fatal("re-init must not run for a pre-existing database")
			return nil
		})
	if !errors.Is(err, initErr) {
		t.Fatalf("error = %v, want it to wrap the init refusal", err)
	}
	if !strings.Contains(err.Error(), "pre-existed") {
		t.Fatalf("error = %v, want it to explain the database pre-existed this init", err)
	}
}

// When the pre-init catalog probe failed we never confirmed the database was
// ours to commit, so recovery still declines — but it must report the probe
// failure, not misattribute the decline to a pre-existing database (synthesis
// finding #2; the fail-safe branch must surface its real reason).
func TestRecoverBdInitFromDirtyTablesReportsProbeFailure(t *testing.T) {
	initErr := errors.New(bdDirtyTablesErrText)
	probeErr := errors.New("connect to managed Dolt: dial tcp: connection refused")
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		t.Fatal("commit must not run when the creation probe failed")
		return false, nil
	})

	err := recoverBdInitFromDirtyTables(dirtyTablesRecoveryScope{cityPath: "/city", scopeRoot: "/city/rig", database: "gsp", createdThisInvocation: false, creationProbeErr: probeErr},
		initErr, func() error {
			t.Fatal("re-init must not run when the creation probe failed")
			return nil
		})
	if !errors.Is(err, initErr) {
		t.Fatalf("error = %v, want it to wrap the init refusal", err)
	}
	if !strings.Contains(err.Error(), "catalog probe failed") {
		t.Fatalf("error = %v, want it to report the probe failure", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %v, want it to carry the underlying probe error", err)
	}
	if strings.Contains(err.Error(), "pre-existed") {
		t.Fatalf("error = %v, must not claim the database pre-existed when the probe failed", err)
	}
}

// End-to-end through the entry point `gc rig add` actually calls: a rig whose
// fresh database comes back dirty must be recovered in-process instead of
// handing the operator beads' circular "run 'bd dolt commit'" advice.
func TestInitBeadsForDirWithExecutorRecoversFromDirtyTables(t *testing.T) {
	cityDir := t.TempDir()
	cityConfig := `[workspace]
name = "demo"

[beads]
provider = "bd"
`
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(cityConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	rigDir := filepath.Join(cityDir, "rigs", "gascity-packs")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A database that does not yet exist is being created by this invocation, so
	// recovery is permitted to commit its working set.
	stubManagedDoltDatabaseExistsBeforeInit(t, func(string, string) (bool, error) { return false, nil })

	var commits int
	var gotScope dirtyTablesRecoveryScope
	stubCommitDirtyScopeTables(t, func(scope dirtyTablesRecoveryScope) (bool, error) {
		commits++
		gotScope = scope
		return true, nil
	})

	// Fail the first init the way beads does, then stop with a sentinel so the
	// assertion stays on the recovery and never reaches the real store
	// finalization.
	stopAfterRecovery := errors.New("stop after recovery")
	var calls int
	execute := func(_ string, _ []string, _ ...string) error {
		calls++
		if calls == 1 {
			return errors.New(bdDirtyTablesErrText)
		}
		return stopAfterRecovery
	}

	err := initBeadsForDirWithExecutor(cityDir, rigDir, "gsp", "gsp", execute)
	if !errors.Is(err, stopAfterRecovery) {
		t.Fatalf("initBeadsForDirWithExecutor error = %v, want %v", err, stopAfterRecovery)
	}
	if calls != 2 {
		t.Fatalf("bd init attempts = %d, want 2 (initial + post-commit retry)", calls)
	}
	if commits != 1 {
		t.Fatalf("commit rounds = %d, want 1", commits)
	}
	if gotScope.cityPath != cityDir {
		t.Fatalf("commit cityPath = %q, want %q", gotScope.cityPath, cityDir)
	}
	if gotScope.scopeRoot != rigDir {
		t.Fatalf("commit scopeRoot = %q, want %q", gotScope.scopeRoot, rigDir)
	}
	if gotScope.database != "gsp" {
		t.Fatalf("commit database = %q, want %q", gotScope.database, "gsp")
	}
	if !gotScope.createdThisInvocation {
		t.Fatal("commit scope createdThisInvocation = false, want true (fresh database)")
	}
}

// A pre-existing database routes through the same init path at every ordinary
// city/rig startup. When such an init hits the dirty-table deadlock, recovery
// must NOT bulk-commit its working set — it declines and surfaces the refusal
// (synthesis finding #2).
func TestInitBeadsForDirWithExecutorDeclinesPreExistingDatabase(t *testing.T) {
	cityDir := t.TempDir()
	cityConfig := `[workspace]
name = "demo"

[beads]
provider = "bd"
`
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(cityConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	rigDir := filepath.Join(cityDir, "rigs", "gascity-packs")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The database already exists in the catalog: this invocation is not creating
	// it, so its working set is not ours to bulk-commit.
	stubManagedDoltDatabaseExistsBeforeInit(t, func(string, string) (bool, error) { return true, nil })
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		t.Fatal("commit must not run for a pre-existing database")
		return false, nil
	})

	dirtyErr := errors.New(bdDirtyTablesErrText)
	var calls int
	execute := func(_ string, _ []string, _ ...string) error {
		calls++
		return dirtyErr
	}

	err := initBeadsForDirWithExecutor(cityDir, rigDir, "gsp", "gsp", execute)
	if !errors.Is(err, dirtyErr) {
		t.Fatalf("initBeadsForDirWithExecutor error = %v, want it to wrap the dirty-table refusal", err)
	}
	if !strings.Contains(err.Error(), "pre-existed") {
		t.Fatalf("error = %v, want it to explain the database pre-existed this init", err)
	}
	if calls != 1 {
		t.Fatalf("bd init attempts = %d, want 1 (no post-commit retry when recovery declines)", calls)
	}
}

// When the pre-init catalog probe itself fails, the same lifecycle path must
// decline (fail-safe) AND thread the probe error through to recovery, so the
// operator sees the real reason instead of a bogus "pre-existed" claim
// (synthesis finding #2; exercises the lifecycle probe -> scope wiring).
func TestInitBeadsForDirWithExecutorReportsProbeFailure(t *testing.T) {
	cityDir := t.TempDir()
	cityConfig := `[workspace]
name = "demo"

[beads]
provider = "bd"
`
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(cityConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	rigDir := filepath.Join(cityDir, "rigs", "gascity-packs")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	probeErr := errors.New("connect to managed Dolt: dial tcp: connection refused")
	stubManagedDoltDatabaseExistsBeforeInit(t, func(string, string) (bool, error) { return false, probeErr })
	stubCommitDirtyScopeTables(t, func(dirtyTablesRecoveryScope) (bool, error) {
		t.Fatal("commit must not run when the creation probe failed")
		return false, nil
	})

	dirtyErr := errors.New(bdDirtyTablesErrText)
	var calls int
	execute := func(_ string, _ []string, _ ...string) error {
		calls++
		return dirtyErr
	}

	err := initBeadsForDirWithExecutor(cityDir, rigDir, "gsp", "gsp", execute)
	if !errors.Is(err, dirtyErr) {
		t.Fatalf("initBeadsForDirWithExecutor error = %v, want it to wrap the dirty-table refusal", err)
	}
	if !strings.Contains(err.Error(), "catalog probe failed") {
		t.Fatalf("error = %v, want it to report the probe failure", err)
	}
	if strings.Contains(err.Error(), "pre-existed") {
		t.Fatalf("error = %v, must not claim the database pre-existed when the probe failed", err)
	}
	if calls != 1 {
		t.Fatalf("bd init attempts = %d, want 1 (no post-commit retry when recovery declines)", calls)
	}
}

// parseSmokeCount reads the value row of a `SELECT COUNT(*)` csv result. It
// ships inlined (the fork-side helper is not on upstream), so it carries its
// own coverage: header+value, zero, bare value, and the two error forms.
func TestParseSmokeCount(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "header and value", in: "cnt\n3", want: 3},
		{name: "zero value", in: "cnt\n0", want: 0},
		{name: "bare value", in: "5", want: 5},
		{name: "trailing blank lines", in: "cnt\n7\n\n", want: 7},
		{name: "empty output", in: "", wantErr: true},
		{name: "whitespace only", in: "   \n  ", wantErr: true},
		{name: "non-numeric value", in: "cnt\nfoo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSmokeCount(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSmokeCount(%q) = %d, nil; want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSmokeCount(%q) error = %v, want %d", tt.in, err, tt.want)
			}
			if got != tt.want {
				t.Fatalf("parseSmokeCount(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// A managed city can host an explicitly external rig. Recovery commits against
// the managed-city server, so it must resolve the FAILING scope's own endpoint
// and decline when that endpoint is external — committing a same-named database
// on the local server would mutate the wrong database (synthesis finding #1).
func TestEnsureScopeDoltIsManagedCityDeclinesExternalRig(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "frontend")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityToml := `[workspace]
name = "demo"

[beads]
provider = "bd"

[[rigs]]
name = "frontend"
path = "frontend"
prefix = "fe"
dolt_host = "rig-db.example.com"
dolt_port = "4407"
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityToml), 0o644); err != nil {
		t.Fatal(err)
	}
	rigCfg := `issue_prefix: fe
gc.endpoint_origin: explicit
gc.endpoint_status: verified
dolt.auto-start: false
dolt.host: rig-db.example.com
dolt.port: 4407
dolt.user: rig-user
`
	if err := os.WriteFile(filepath.Join(rigPath, ".beads", "config.yaml"), []byte(rigCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ensureScopeDoltIsManagedCity(cityPath, rigPath)
	if err == nil {
		t.Fatal("ensureScopeDoltIsManagedCity() = nil for an external rig, want a decline")
	}
	if !strings.Contains(err.Error(), "external") {
		t.Fatalf("ensureScopeDoltIsManagedCity() error = %v, want it to name the external endpoint", err)
	}
}

// A scope that inherits the managed city (no authoritative endpoint of its own)
// resolves to the managed-city server, so recovery is permitted (synthesis
// finding #1 — the fix must not over-decline the common managed case).
func TestEnsureScopeDoltIsManagedCityAllowsInheritedScope(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "frontend")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cityToml := `[workspace]
name = "demo"

[beads]
provider = "bd"
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityToml), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureScopeDoltIsManagedCity(cityPath, rigPath); err != nil {
		t.Fatalf("ensureScopeDoltIsManagedCity() = %v for an inherited managed scope, want nil", err)
	}
}
