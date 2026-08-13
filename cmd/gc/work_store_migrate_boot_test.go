package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestVerifyRetainedWorkMigrationAtBootBypassesNonBDAndGenesis(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider string
		rows     []beads.Bead
	}{
		{name: "file provider with retained Work", provider: "file", rows: []beads.Bead{workMigrationBootRow()}},
		{name: "bd genesis without retained file", provider: "bd"},
		{name: "bd retained file without Work", provider: "bd", rows: []beads.Bead{{ID: "gc-mail", Type: "message", CreatedAt: workMigrationBootTime()}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cityPath := t.TempDir()
			if test.rows != nil {
				writeWorkMigrationBootSource(t, cityPath, test.rows, nil)
			}
			opens := 0
			runtime := defaultWorkMigrationBootRuntime()
			runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
				opens++
				return nil, errors.New("destination must stay closed")
			}
			if err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, test.provider, "dr", runtime); err != nil {
				t.Fatalf("verifyRetainedWorkMigrationAtBoot: %v", err)
			}
			if opens != 0 {
				t.Fatalf("destination opens = %d, want 0", opens)
			}
		})
	}
}

func TestVerifyRetainedWorkMigrationAtBootRequiresCurrentProofBeforeDestinationOpen(t *testing.T) {
	cityPath := t.TempDir()
	writeWorkMigrationBootSource(t, cityPath, []beads.Bead{workMigrationBootRow()}, nil)
	opens := 0
	runtime := defaultWorkMigrationBootRuntime()
	runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
		opens++
		return nil, errors.New("destination must stay closed")
	}

	err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
	if !errors.Is(err, errWorkMigrationProofInvalid) || !strings.Contains(err.Error(), workMigrationProofPath(cityPath)) {
		t.Fatalf("error = %v, want proof-invalid error naming manifest", err)
	}
	if opens != 0 {
		t.Fatalf("destination opens = %d, want 0", opens)
	}
}

func TestVerifyRetainedWorkMigrationAtBootRejectsStaleManifestFields(t *testing.T) {
	for _, mutate := range []struct {
		name string
		fn   func(*workMigrationProof)
	}{
		{name: "version", fn: func(proof *workMigrationProof) { proof.Version++ }},
		{name: "source path", fn: func(proof *workMigrationProof) { proof.SourceFile += ".other" }},
		{name: "source hash shape", fn: func(proof *workMigrationProof) { proof.SourceFileSHA256 = "not-a-sha256" }},
		{name: "source witness", fn: func(proof *workMigrationProof) { proof.SourceWitness = "sha256:" + strings.Repeat("1", 64) }},
		{name: "work ids", fn: func(proof *workMigrationProof) { proof.WorkIDs = []string{"gc-other"} }},
		{name: "dependency witness", fn: func(proof *workMigrationProof) { proof.DependencyWitness = "sha256:" + strings.Repeat("2", 64) }},
		{name: "destination", fn: func(proof *workMigrationProof) { proof.DestinationWorkspace += ".other" }},
		{name: "native witness", fn: func(proof *workMigrationProof) { proof.NativeReadbackWitness = "sha256:" + strings.Repeat("3", 64) }},
		{name: "prefix", fn: func(proof *workMigrationProof) { proof.DestinationPrefix = "gc" }},
		{name: "disposition", fn: func(proof *workMigrationProof) { proof.Disposition = "preview" }},
		{name: "imported ids", fn: func(proof *workMigrationProof) {
			proof.Disposition = "imported"
			proof.ImportedIDs = nil
		}},
		{name: "command version", fn: func(proof *workMigrationProof) { proof.CommandVersion = "" }},
		{name: "verified at", fn: func(proof *workMigrationProof) { proof.VerifiedAt = "not-a-time" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
			mutate.fn(&proof)
			writeWorkMigrationBootProof(t, cityPath, proof)
			opens := 0
			runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
			err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
			if !errors.Is(err, errWorkMigrationProofInvalid) {
				t.Fatalf("error = %v, want errWorkMigrationProofInvalid", err)
			}
			if opens != 0 {
				t.Fatalf("destination opens = %d, want pre-open rejection", opens)
			}
		})
	}
}

func TestVerifyRetainedWorkMigrationAtBootRejectsMalformedProofBeforeDestinationOpen(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
	}{
		{name: "malformed", contents: "{"},
		{name: "unknown field", contents: `{"unknown":true}`},
		{name: "trailing value", contents: `{}` + "\n{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cityPath := t.TempDir()
			writeWorkMigrationBootSource(t, cityPath, []beads.Bead{workMigrationBootRow()}, nil)
			path := workMigrationProofPath(cityPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			opens := 0
			runtime := defaultWorkMigrationBootRuntime()
			runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
				opens++
				return nil, errors.New("destination must stay closed")
			}
			err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
			if !errors.Is(err, errWorkMigrationProofInvalid) || opens != 0 {
				t.Fatalf("error/opens = %v/%d, want proof invalid/0", err, opens)
			}
		})
	}
}

func TestVerifyRetainedWorkMigrationAtBootRechecksNativeCopyAndMovingSource(t *testing.T) {
	t.Run("valid proof", func(t *testing.T) {
		cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
		writeWorkMigrationBootProof(t, cityPath, proof)
		opens := 0
		runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
		if err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime); err != nil {
			t.Fatalf("verifyRetainedWorkMigrationAtBoot: %v", err)
		}
		if opens != 1 {
			t.Fatalf("destination opens = %d, want 1", opens)
		}
	})

	t.Run("native row missing", func(t *testing.T) {
		cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
		writeWorkMigrationBootProof(t, cityPath, proof)
		opens := 0
		runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
		runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
			opens++
			return newFakeWorkMigrationDestination(nil, nil), nil
		}
		err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
		if !errors.Is(err, errWorkCopyUnconfirmed) || opens != 1 {
			t.Fatalf("error/opens = %v/%d, want unconfirmed/1", err, opens)
		}
	})

	t.Run("source changes during check", func(t *testing.T) {
		cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
		writeWorkMigrationBootProof(t, cityPath, proof)
		opens := 0
		runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
		baseRead := runtime.readSource
		reads := 0
		runtime.readSource = func(path string) (beads.Store, []byte, error) {
			store, contents, err := baseRead(path)
			reads++
			if reads == 2 && err == nil {
				changed := workMigrationBootRow()
				changed.Title = "changed retained work"
				store = beads.NewMemStoreFrom(0, []beads.Bead{changed}, nil)
			}
			return store, contents, err
		}
		err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
		if !errors.Is(err, errWorkMigrationSourceChanged) {
			t.Fatalf("error = %v, want errWorkMigrationSourceChanged", err)
		}
	})

	t.Run("non-Work source churn is allowed", func(t *testing.T) {
		cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
		writeWorkMigrationBootProof(t, cityPath, proof)
		opens := 0
		runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
		baseRead := runtime.readSource
		reads := 0
		runtime.readSource = func(path string) (beads.Store, []byte, error) {
			store, contents, err := baseRead(path)
			reads++
			if reads != 2 || err != nil {
				return store, contents, err
			}
			return beads.NewMemStoreFrom(0, []beads.Bead{
				workMigrationBootRow(),
				{ID: "gc-mail", Type: "message", CreatedAt: workMigrationBootTime()},
			}, nil), append(slices.Clone(contents), '\n'), nil
		}
		if err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime); err != nil {
			t.Fatalf("verifyRetainedWorkMigrationAtBoot: %v", err)
		}
	})
}

func TestVerifyRetainedWorkMigrationAtBootExplainsInvalidRetainedSource(t *testing.T) {
	cityPath := t.TempDir()
	writeWorkMigrationBootSource(t, cityPath, []beads.Bead{
		workMigrationBootRow(),
		{ID: "gc-mail", Type: "message", CreatedAt: workMigrationBootTime()},
	}, []beads.Dep{{IssueID: "gc-work", DependsOnID: "gc-mail", Type: "blocks"}})
	runtime := defaultWorkMigrationBootRuntime()
	runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
		t.Fatal("invalid retained source opened destination")
		return nil, nil
	}
	err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
	if !errors.Is(err, errWorkMigrationSourceInvalid) ||
		!strings.Contains(err.Error(), "keep the file provider") ||
		!strings.Contains(err.Error(), "gc storage migrate-work") {
		t.Fatalf("error = %v, want actionable retained-source refusal", err)
	}
}

func TestVerifyRetainedWorkMigrationAtBootPropagatesDestinationCloseFailure(t *testing.T) {
	cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
	writeWorkMigrationBootProof(t, cityPath, proof)
	closeErr := errors.New("injected boot verifier close failure")
	runtime := workMigrationBootRuntimeForSnapshot(snapshot, new(int))
	runtime.openDestination = func(context.Context, string) (workMigrationDestination, error) {
		destination := newFakeWorkMigrationDestination(expectedWorkMigrationRows(snapshot.Exact), nil)
		destination.closeErr = closeErr
		return destination, nil
	}
	err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
	if !errors.Is(err, closeErr) || !errors.Is(err, errWorkVerificationUnavailable) {
		t.Fatalf("error = %v, want joined close/unavailable error", err)
	}
}

func TestVerifyRetainedWorkMigrationAtBootRejectsDestinationPrefixMismatch(t *testing.T) {
	cityPath, snapshot, proof := newWorkMigrationBootFixture(t)
	writeWorkMigrationBootProof(t, cityPath, proof)
	opens := 0
	runtime := workMigrationBootRuntimeForSnapshot(snapshot, &opens)
	baseOpen := runtime.openDestination
	runtime.openDestination = func(ctx context.Context, workspace string) (workMigrationDestination, error) {
		destination, err := baseOpen(ctx, workspace)
		if err != nil {
			return nil, err
		}
		return workMigrationBootPrefixDestination{workMigrationDestination: destination, prefix: "gc"}, nil
	}
	err := verifyRetainedWorkMigrationAtBoot(context.Background(), cityPath, "bd", "dr", runtime)
	if !errors.Is(err, errWorkMigrationProofInvalid) || opens != 1 {
		t.Fatalf("error/opens = %v/%d, want proof invalid/1", err, opens)
	}
}

func TestInitializeBeadsAfterProviderChecksRetainedWorkProofBeforeInit(t *testing.T) {
	originalVerify := startBeadsLifecycleVerifyRetainedWork
	originalInit := startBeadsLifecycleInitAndHookDir
	t.Cleanup(func() {
		startBeadsLifecycleVerifyRetainedWork = originalVerify
		startBeadsLifecycleInitAndHookDir = originalInit
	})
	sentinel := errors.New("injected invalid retained Work proof")
	startBeadsLifecycleVerifyRetainedWork = func(string, *config.City) error { return sentinel }
	inits := 0
	startBeadsLifecycleInitAndHookDir = func(string, string, string) error {
		inits++
		return nil
	}
	err := initializeBeadsAfterProvider(t.TempDir(), &config.City{}, io.Discard)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want injected proof failure", err)
	}
	if inits != 0 {
		t.Fatalf("init calls = %d, want 0", inits)
	}
}

func TestStartBeadsLifecycleOrdersProviderBeforeRetainedWorkProofAndInit(t *testing.T) {
	originalEnsure := startBeadsLifecycleEnsureProvider
	originalVerify := startBeadsLifecycleVerifyRetainedWork
	originalInit := startBeadsLifecycleInitAndHookDir
	t.Cleanup(func() {
		startBeadsLifecycleEnsureProvider = originalEnsure
		startBeadsLifecycleVerifyRetainedWork = originalVerify
		startBeadsLifecycleInitAndHookDir = originalInit
	})
	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[beads]\nprovider = \"file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var events []string
	startBeadsLifecycleEnsureProvider = func(string) error {
		events = append(events, "provider")
		return nil
	}
	startBeadsLifecycleVerifyRetainedWork = func(string, *config.City) error {
		events = append(events, "proof")
		return nil
	}
	startBeadsLifecycleInitAndHookDir = func(string, string, string) error {
		events = append(events, "init")
		return nil
	}
	if err := startBeadsLifecycle(cityPath, "test-city", &config.City{}, io.Discard); err != nil {
		t.Fatalf("startBeadsLifecycle: %v", err)
	}
	if want := []string{"provider", "proof", "init"}; !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestStartBeadsLifecycleBindsRetainedWorkVerifierInputsAndDeadline(t *testing.T) {
	originalVerify := startBeadsLifecycleVerifyRetainedWorkAtBoot
	t.Cleanup(func() { startBeadsLifecycleVerifyRetainedWorkAtBoot = originalVerify })
	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[beads]\nprovider = \"bd\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	type capturedCall struct {
		cityPath, provider, prefix string
		hasDeadline                bool
	}
	var got capturedCall
	startBeadsLifecycleVerifyRetainedWorkAtBoot = func(
		ctx context.Context, path, provider, prefix string, _ workMigrationBootRuntime,
	) error {
		_, got.hasDeadline = ctx.Deadline()
		got.cityPath, got.provider, got.prefix = path, provider, prefix
		return nil
	}
	if err := startBeadsLifecycleVerifyRetainedWork(cityPath, &config.City{Workspace: config.Workspace{Name: "ds-research"}}); err != nil {
		t.Fatalf("startBeadsLifecycleVerifyRetainedWork: %v", err)
	}
	if got != (capturedCall{cityPath: cityPath, provider: "bd", prefix: "dr", hasDeadline: true}) {
		t.Fatalf("verifier inputs = %+v, want bd/dr city with deadline", got)
	}
}

type workMigrationBootPrefixDestination struct {
	workMigrationDestination
	prefix string
}

func (destination workMigrationBootPrefixDestination) IDPrefix() string { return destination.prefix }

func newWorkMigrationBootFixture(t *testing.T) (string, workMigrationSnapshot, workMigrationProof) {
	t.Helper()
	cityPath := t.TempDir()
	row := workMigrationBootRow()
	writeWorkMigrationBootSource(t, cityPath, []beads.Bead{row}, nil)
	sourcePath := filepath.Join(cityPath, ".gc", "beads.json")
	source, contents, err := openWorkMigrationFileSource(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readWorkMigrationSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	proof := workMigrationProof{
		Version: workMigrationProofVersion, SourceFile: sourcePath,
		SourceFileSHA256: sha256String(contents), SourceWitness: snapshot.Exact.SourceWitness,
		WorkIDs: workMigrationIDs(snapshot.Exact.Rows), DependencyWitness: snapshot.DependencyWitness,
		DestinationWorkspace: cityPath, DestinationPrefix: "dr", Disposition: "already_proven",
		NativeReadbackWitness: snapshot.Exact.SourceWitness, CommandVersion: "test@commit",
		VerifiedAt: time.Date(2026, 8, 13, 19, 25, 4, 0, time.UTC).Format(time.RFC3339Nano),
	}
	return cityPath, snapshot, proof
}

func workMigrationBootRuntimeForSnapshot(snapshot workMigrationSnapshot, opens *int) workMigrationBootRuntime {
	return workMigrationBootRuntime{
		readSource: openWorkMigrationFileSource,
		readProof:  os.ReadFile,
		openDestination: func(context.Context, string) (workMigrationDestination, error) {
			*opens++
			rows := expectedWorkMigrationRows(snapshot.Exact)
			return newFakeWorkMigrationDestination(rows, rows[0].Dependencies), nil
		},
	}
}

func writeWorkMigrationBootSource(t *testing.T, cityPath string, rows []beads.Bead, deps []beads.Dep) {
	t.Helper()
	path := filepath.Join(cityPath, ".gc", "beads.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(struct {
		Seq   int          `json:"seq"`
		Beads []beads.Bead `json:"beads"`
		Deps  []beads.Dep  `json:"deps"`
	}{Seq: 1, Beads: rows, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeWorkMigrationBootProof(t *testing.T, cityPath string, proof workMigrationProof) {
	t.Helper()
	path := workMigrationProofPath(cityPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func workMigrationBootRow() beads.Bead {
	return beads.Bead{ID: "gc-work", Title: "retained work", Status: "open", Type: "task", CreatedAt: workMigrationBootTime()}
}

func workMigrationBootTime() time.Time {
	return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
}
