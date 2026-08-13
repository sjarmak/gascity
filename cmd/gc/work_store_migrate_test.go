package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestReadWorkMigrationSnapshotSelectsExactWorkClosure(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	priority := 2
	source := beads.NewMemStoreFrom(0, []beads.Bead{
		{ID: "gc-b", Title: "b", Status: "open", Type: "task", CreatedAt: createdAt, Priority: &priority, Metadata: beads.StringMap{"gc.execution_routed_to": "worker"}},
		{ID: "gc-a", Title: "a", Status: "open", Type: "task", CreatedAt: createdAt, ParentID: "gc-b", Metadata: beads.StringMap{"gc.routed_to": "worker"}},
		{ID: "gc-step", Title: "step", Status: "open", Type: "step", CreatedAt: createdAt, Metadata: beads.StringMap{"gc.step_id": "build", "gc.routed_to": "worker", "gc.root_bead_id": "gc-root"}},
	}, []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "parent-child"}})

	snapshot, err := readWorkMigrationSnapshot(source)
	if err != nil {
		t.Fatalf("readWorkMigrationSnapshot: %v", err)
	}
	if got := snapshot.Exact.SourceWitness; !strings.HasPrefix(got, "sha256:") || len(got) != 71 {
		t.Fatalf("source witness = %q", got)
	}
	if len(snapshot.Exact.Rows) != 2 || snapshot.Exact.Rows[0].ID != "gc-a" || snapshot.Exact.Rows[1].ID != "gc-b" {
		t.Fatalf("work rows = %+v", snapshot.Exact.Rows)
	}
	if deps := snapshot.Exact.Rows[0].Dependencies; len(deps) != 1 || deps[0].DependsOnID != "gc-b" || deps[0].Type != "parent-child" {
		t.Fatalf("work dependencies = %+v", deps)
	}
	if snapshot.Exact.Rows[0].Metadata["gc.routed_to"] != "worker" || snapshot.Exact.Rows[1].Metadata["gc.execution_routed_to"] != "worker" {
		t.Fatalf("routing metadata changed: %+v", snapshot.Exact.Rows)
	}
}

func TestWorkMigrationRowsWitnessCanonicalizesDependencyOrder(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	first := beads.Dep{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}
	second := beads.Dep{IssueID: "gc-a", DependsOnID: "gc-c", Type: "tracks"}
	rows := []beads.Bead{{
		ID: "gc-a", Title: "a", Status: "open", Type: "task", CreatedAt: createdAt,
		Labels: []string{"z", "a", "z"}, Dependencies: []beads.Dep{first, second},
	}}
	reordered := append([]beads.Bead(nil), rows...)
	reordered[0].Dependencies = []beads.Dep{second, first}
	reordered[0].Labels = []string{"a", "z"}

	want, err := workMigrationRowsWitness(rows)
	if err != nil {
		t.Fatalf("first witness: %v", err)
	}
	got, err := workMigrationRowsWitness(reordered)
	if err != nil {
		t.Fatalf("reordered witness: %v", err)
	}
	if got != want {
		t.Fatalf("reordered witness = %s, want %s", got, want)
	}
}

func TestReadWorkMigrationSnapshotRefusesRoutingAndClosureViolations(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	tests := []struct {
		name string
		rows []beads.Bead
		deps []beads.Dep
	}{
		{
			name: "work carries both routing authorities",
			rows: []beads.Bead{{ID: "gc-a", Status: "open", Type: "task", CreatedAt: createdAt, Metadata: beads.StringMap{"gc.routed_to": "worker", "gc.execution_routed_to": "worker"}}},
		},
		{
			name: "graph dispatch step lacks claim route",
			rows: []beads.Bead{{ID: "gc-step", Status: "open", Type: "step", CreatedAt: createdAt, Metadata: beads.StringMap{"gc.step_id": "build", "gc.root_bead_id": "gc-root"}}},
		},
		{
			name: "work dependency leaves work closure",
			rows: []beads.Bead{
				{ID: "gc-a", Status: "open", Type: "task", CreatedAt: createdAt},
				{ID: "gc-step", Status: "open", Type: "step", CreatedAt: createdAt, Metadata: beads.StringMap{"gc.step_id": "build", "gc.routed_to": "worker", "gc.root_bead_id": "gc-root"}},
			},
			deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-step", Type: "blocks"}},
		},
		{
			name: "work dependency endpoint is missing",
			rows: []beads.Bead{{ID: "gc-a", Status: "open", Type: "task", CreatedAt: createdAt}},
			deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-missing", Type: "blocks"}},
		},
		{
			name: "work dependency type is empty",
			rows: []beads.Bead{
				{ID: "gc-a", Status: "open", Type: "task", CreatedAt: createdAt},
				{ID: "gc-b", Status: "open", Type: "task", CreatedAt: createdAt},
			},
			deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b"}},
		},
		{
			name: "work dependency is self-referential",
			rows: []beads.Bead{{ID: "gc-a", Status: "open", Type: "task", CreatedAt: createdAt}},
			deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-a", Type: "blocks"}},
		},
		{
			name: "source has no work rows",
			rows: []beads.Bead{{ID: "gc-step", Status: "open", Type: "step", CreatedAt: createdAt, Metadata: beads.StringMap{"gc.step_id": "build", "gc.routed_to": "worker", "gc.root_bead_id": "gc-root"}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := beads.NewMemStoreFrom(0, test.rows, test.deps)
			if _, err := readWorkMigrationSnapshot(source); !errors.Is(err, errWorkMigrationSourceInvalid) {
				t.Fatalf("readWorkMigrationSnapshot error = %v, want errWorkMigrationSourceInvalid", err)
			}
		})
	}
}

func TestVerifyWorkMigrationSnapshotRejectsLossMutations(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	priority := 1
	source := beads.NewMemStoreFrom(0, []beads.Bead{
		{ID: "gc-a", Title: "a", Status: "in_progress", Type: "bug", Priority: &priority, CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Hour), Assignee: "worker", Metadata: beads.StringMap{"gc.routed_to": "worker"}},
		{ID: "gc-b", Title: "b", Status: "open", Type: "task", CreatedAt: createdAt},
	}, []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}})
	snapshot, err := readWorkMigrationSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	expected := expectedWorkMigrationRows(snapshot.Exact)
	tests := []struct {
		name string
		rows []beads.Bead
		deps []beads.Dep
	}{
		{name: "dropped row", rows: expected[:1]},
		{name: "dropped dependency", rows: expected},
		{name: "changed field", rows: mutateWorkMigrationTitle(expected), deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}}},
		{
			name: "invented incoming dependency",
			rows: append(append([]beads.Bead(nil), expected...), beads.Bead{
				ID: "dr-extra", Title: "extra", Status: "open", Type: "task", CreatedAt: createdAt,
			}),
			deps: []beads.Dep{
				{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"},
				{IssueID: "dr-extra", DependsOnID: "gc-a", Type: "tracks"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := beads.NewMemStoreFrom(0, test.rows, test.deps)
			if _, err := verifyWorkMigrationSnapshot(destination, snapshot.Exact); !errors.Is(err, errWorkCopyUnconfirmed) {
				t.Fatalf("verifyWorkMigrationSnapshot error = %v, want errWorkCopyUnconfirmed", err)
			}
		})
	}
	destination := beads.NewMemStoreFrom(0, expected, []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}})
	witness, err := verifyWorkMigrationSnapshot(destination, snapshot.Exact)
	if err != nil {
		t.Fatalf("faithful snapshot refused: %v", err)
	}
	if witness != snapshot.Exact.SourceWitness {
		t.Fatalf("native readback witness = %q, want %q", witness, snapshot.Exact.SourceWitness)
	}
	if _, err := verifyWorkMigrationSnapshot(unavailableStore{err: errors.New("injected query outage")}, snapshot.Exact); !errors.Is(err, errWorkVerificationUnavailable) {
		t.Fatalf("query outage error = %v, want errWorkVerificationUnavailable", err)
	}
}

func mutateWorkMigrationTitle(rows []beads.Bead) []beads.Bead {
	mutated := append([]beads.Bead(nil), rows...)
	mutated[0].Title = "changed"
	return mutated
}

func TestRunWorkMigrationDryRunNeverOpensOrWritesDestination(t *testing.T) {
	runtime, _ := newWorkMigrationRuntimeForTest(t)
	runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
		t.Fatal("dry-run opened destination")
		return nil, nil
	}
	runtime.writeProof = func(string, workMigrationProof) error {
		t.Fatal("dry-run wrote proof")
		return nil
	}

	result, err := runWorkMigration(context.Background(), workMigrationRequest{
		FromFile: "/source/beads.json", DestinationWorkspace: "/destination", DryRun: true,
	}, runtime)
	if err != nil {
		t.Fatalf("runWorkMigration dry-run: %v", err)
	}
	if !result.DryRun || result.Rows != 2 || result.Dependencies != 1 || result.Disposition != "preview" {
		t.Fatalf("dry-run result = %+v", result)
	}
}

func TestStorageMigrateWorkCommandRequiresExplicitAbsoluteEndpoints(t *testing.T) {
	runtime, _ := newWorkMigrationRuntimeForTest(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing both"},
		{name: "relative source", args: []string{"--from-file", "beads.json", "--destination-workspace", "/destination", "--dry-run"}},
		{name: "relative destination", args: []string{"--from-file", "/source/beads.json", "--destination-workspace", "destination", "--dry-run"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := newStorageMigrateWorkCmdWithRuntime(runtime, &bytes.Buffer{}, &bytes.Buffer{})
			cmd.SetArgs(test.args)
			if err := cmd.Execute(); !errors.Is(err, errExit) {
				t.Fatalf("Execute error = %v, want errExit", err)
			}
		})
	}
}

func TestStorageMigrateWorkCommandReportsStructuredDryRun(t *testing.T) {
	runtime, _ := newWorkMigrationRuntimeForTest(t)
	var stdout, stderr bytes.Buffer
	cmd := newStorageMigrateWorkCmdWithRuntime(runtime, &stdout, &stderr)
	cmd.SetArgs([]string{"--from-file", "/source/beads.json", "--destination-workspace", "/destination", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v; stderr=%s", err, stderr.String())
	}
	var got workMigrationResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode result: %v; stdout=%s", err, stdout.String())
	}
	if !got.DryRun || got.Disposition != "preview" || got.Rows != 2 || got.Dependencies != 1 {
		t.Fatalf("result = %+v", got)
	}
}

func TestStorageRootCarriesMigrateWorkVerb(t *testing.T) {
	root := newStorageCmd(&bytes.Buffer{}, &bytes.Buffer{})
	command, _, err := root.Find([]string{storageWorkMigrationVerb})
	if err != nil || command == root || command.Name() != storageWorkMigrationVerb {
		t.Fatalf("storage migrate-work command = %v, %v", command, err)
	}
}

func TestRunWorkMigrationProofRequiresIndependentReadback(t *testing.T) {
	tests := []struct {
		name       string
		reader     func(*fakeWorkMigrationDestination) (workMigrationDestination, error)
		wantErr    error
		wantProofs int
	}{
		{
			name: "healthy",
			reader: func(destination *fakeWorkMigrationDestination) (workMigrationDestination, error) {
				return destination, nil
			},
			wantProofs: 1,
		},
		{
			name: "dropped row",
			reader: func(destination *fakeWorkMigrationDestination) (workMigrationDestination, error) {
				rows, _ := destination.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
				return newFakeWorkMigrationDestination(rows[:1], nil), nil
			},
			wantErr: errWorkCopyUnconfirmed,
		},
		{
			name: "dropped dependency",
			reader: func(destination *fakeWorkMigrationDestination) (workMigrationDestination, error) {
				rows, _ := destination.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
				return newFakeWorkMigrationDestination(rows, nil), nil
			},
			wantErr: errWorkCopyUnconfirmed,
		},
		{
			name: "changed field",
			reader: func(destination *fakeWorkMigrationDestination) (workMigrationDestination, error) {
				rows, _ := destination.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
				rows[0].Title = "changed"
				return newFakeWorkMigrationDestination(rows, []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}}), nil
			},
			wantErr: errWorkCopyUnconfirmed,
		},
		{
			name: "native readback outage",
			reader: func(*fakeWorkMigrationDestination) (workMigrationDestination, error) {
				return nil, errors.New("injected native outage")
			},
			wantErr: errWorkVerificationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, destination := newWorkMigrationRuntimeForTest(t)
			proofs := 0
			runtime.reopenDestination = func(_ context.Context, _ string) (workMigrationDestination, error) {
				return test.reader(destination)
			}
			runtime.writeProof = func(string, workMigrationProof) error { proofs++; return nil }

			_, err := runWorkMigration(context.Background(), workMigrationRequest{
				FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true,
			}, runtime)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("runWorkMigration error = %v, want %v", err, test.wantErr)
			}
			if proofs != test.wantProofs {
				t.Fatalf("proof writes = %d, want %d", proofs, test.wantProofs)
			}
		})
	}
}

func TestRunWorkMigrationRerunIsVerifiedNoOp(t *testing.T) {
	runtime, destination := newWorkMigrationRuntimeForTest(t)
	proofs := 0
	runtime.writeProof = func(string, workMigrationProof) error { proofs++; return nil }
	request := workMigrationRequest{FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true}

	first, err := runWorkMigration(context.Background(), request, runtime)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := runWorkMigration(context.Background(), request, runtime)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.Disposition != "imported" || second.Disposition != "already_proven" || destination.imports != 1 {
		t.Fatalf("runs = %+v / %+v, imports=%d", first, second, destination.imports)
	}
	rows, _ := destination.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
	deps, _ := destination.DepList("gc-a", "down")
	if len(rows) != 2 || len(deps) != 1 || proofs != 2 {
		t.Fatalf("rerun state rows=%d deps=%d proofs=%d", len(rows), len(deps), proofs)
	}
}

func TestRunWorkMigrationRefusesMovingSourceAndWriterHazards(t *testing.T) {
	t.Run("source changed before proof", func(t *testing.T) {
		runtime, _ := newWorkMigrationRuntimeForTest(t)
		reads := 0
		runtime.readSource = func(string) (beads.Store, []byte, error) {
			reads++
			store := workMigrationSourceForTest(t)
			return store, []byte(strings.Repeat("x", reads)), nil
		}
		proofs := 0
		runtime.writeProof = func(string, workMigrationProof) error { proofs++; return nil }
		_, err := runWorkMigration(context.Background(), workMigrationRequest{
			FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true,
		}, runtime)
		if !errors.Is(err, errWorkMigrationSourceChanged) || proofs != 0 {
			t.Fatalf("run error/proofs = %v/%d, want source-changed and no proof", err, proofs)
		}
	})

	t.Run("fleet attestation missing", func(t *testing.T) {
		runtime, _ := newWorkMigrationRuntimeForTest(t)
		_, err := runWorkMigration(context.Background(), workMigrationRequest{
			FromFile: "/source/beads.json", DestinationWorkspace: "/destination",
		}, runtime)
		if !errors.Is(err, errWorkMigrationWritersUnfenced) {
			t.Fatalf("run error = %v, want errWorkMigrationWritersUnfenced", err)
		}
	})

	t.Run("foreign controller", func(t *testing.T) {
		runtime, _ := newWorkMigrationRuntimeForTest(t)
		runtime.foreignControllerPID = func(string) int { return 42 }
		_, err := runWorkMigration(context.Background(), workMigrationRequest{
			FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true,
		}, runtime)
		if !errors.Is(err, errWorkMigrationWritersUnfenced) {
			t.Fatalf("run error = %v, want errWorkMigrationWritersUnfenced", err)
		}
	})

	t.Run("guard release failure is loud", func(t *testing.T) {
		runtime, _ := newWorkMigrationRuntimeForTest(t)
		runtime.acquireGuard = func(context.Context, string) (workMigrationGuard, error) {
			return failingWorkMigrationGuard{}, nil
		}
		_, err := runWorkMigration(context.Background(), workMigrationRequest{
			FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true,
		}, runtime)
		if err == nil || !strings.Contains(err.Error(), "releasing work migration guard") {
			t.Fatalf("run error = %v, want loud guard release failure", err)
		}
	})

	t.Run("writer cleanup failure is joined", func(t *testing.T) {
		runtime, destination := newWorkMigrationRuntimeForTest(t)
		destination.importErr = errors.New("injected import failure")
		destination.closeErr = errors.New("injected writer close failure")
		_, err := runWorkMigration(context.Background(), workMigrationRequest{
			FromFile: "/source/beads.json", DestinationWorkspace: "/destination", FleetStopped: true,
		}, runtime)
		if err == nil || !strings.Contains(err.Error(), "injected import failure") || !strings.Contains(err.Error(), "injected writer close failure") {
			t.Fatalf("run error = %v, want joined import and writer-close failures", err)
		}
	})
}

type fakeWorkMigrationDestination struct {
	*beads.MemStore
	imports   int
	importErr error
	closeErr  error
}

func newFakeWorkMigrationDestination(rows []beads.Bead, deps []beads.Dep) *fakeWorkMigrationDestination {
	return &fakeWorkMigrationDestination{MemStore: beads.NewMemStoreFrom(0, rows, deps)}
}

func (f *fakeWorkMigrationDestination) ImportExactWorkSnapshot(snapshot beads.ExactWorkSnapshot) (beads.ExactWorkImportResult, error) {
	if f.importErr != nil {
		return beads.ExactWorkImportResult{}, f.importErr
	}
	for _, row := range snapshot.Rows {
		if _, err := f.Get(row.ID); err == nil {
			return beads.ExactWorkImportResult{}, beads.ErrWorkMigrationCollision
		}
	}
	rows := expectedWorkMigrationRows(snapshot)
	var deps []beads.Dep
	for _, row := range rows {
		deps = append(deps, row.Dependencies...)
		row.Dependencies = nil
	}
	f.MemStore = beads.NewMemStoreFrom(0, rows, deps)
	f.imports++
	ids := make([]string, len(rows))
	for index := range rows {
		ids[index] = rows[index].ID
	}
	return beads.ExactWorkImportResult{IDs: ids}, nil
}

func (f *fakeWorkMigrationDestination) CloseStore() error { return f.closeErr }

func (f *fakeWorkMigrationDestination) IDPrefix() string { return "dr" }

type fakeWorkMigrationGuard struct{}

func (fakeWorkMigrationGuard) Release() error { return nil }

type failingWorkMigrationGuard struct{}

func (failingWorkMigrationGuard) Release() error { return errors.New("injected release failure") }

func newWorkMigrationRuntimeForTest(t *testing.T) (workMigrationRuntime, *fakeWorkMigrationDestination) {
	t.Helper()
	destination := newFakeWorkMigrationDestination(nil, nil)
	return workMigrationRuntime{
		readSource: func(string) (beads.Store, []byte, error) {
			return workMigrationSourceForTest(t), []byte("stable source"), nil
		},
		openDestination:      func(context.Context, string) (workMigrationDestination, error) { return destination, nil },
		reopenDestination:    func(context.Context, string) (workMigrationDestination, error) { return destination, nil },
		foreignControllerPID: func(string) int { return 0 },
		acquireGuard:         func(context.Context, string) (workMigrationGuard, error) { return fakeWorkMigrationGuard{}, nil },
		writeProof:           func(string, workMigrationProof) error { return nil },
		now:                  func() time.Time { return time.Date(2026, 8, 13, 17, 0, 0, 0, time.UTC) },
	}, destination
}

func workMigrationSourceForTest(t *testing.T) beads.Store {
	t.Helper()
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	return beads.NewMemStoreFrom(0, []beads.Bead{
		{ID: "gc-a", Title: "a", Status: "open", Type: "task", CreatedAt: createdAt},
		{ID: "gc-b", Title: "b", Status: "open", Type: "task", CreatedAt: createdAt},
	}, []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}})
}
