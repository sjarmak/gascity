//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestRunWorkMigrationRealNativeDestination(t *testing.T) {
	root := t.TempDir()
	destinationPath := filepath.Join(root, "destination")
	sourcePath := filepath.Join(destinationPath, ".gc", "beads.json")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destinationPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	native, err := beads.OpenNativeStorage(context.Background(), destinationPath, nil)
	if err != nil {
		t.Fatalf("initialize native destination: %v", err)
	}
	if err := native.SetConfig(context.Background(), "issue_prefix", "dr"); err != nil {
		_ = native.Close()
		t.Fatalf("set native destination prefix: %v", err)
	}
	if err := native.Close(); err != nil {
		t.Fatalf("close native initializer: %v", err)
	}
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 856235045, time.UTC)
	longTitle := strings.Repeat("legacy work direction ", 30)
	fixture := struct {
		Seq   int          `json:"seq"`
		Beads []beads.Bead `json:"beads"`
		Deps  []beads.Dep  `json:"deps"`
	}{
		Seq: 2,
		Beads: []beads.Bead{
			{ID: "gc-a", Title: longTitle, Status: "open", Type: "task", CreatedAt: createdAt, ParentID: "gc-missing"},
			{ID: "gc-b", Title: "b", Status: "closed", Type: "bug", CreatedAt: createdAt},
		},
		Deps: []beads.Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	request := workMigrationRequest{FromFile: sourcePath, DestinationWorkspace: destinationPath, FleetStopped: true}
	first, err := runWorkMigration(context.Background(), request, defaultWorkMigrationRuntime())
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	second, err := runWorkMigration(context.Background(), request, defaultWorkMigrationRuntime())
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if first.Disposition != "imported" || second.Disposition != "already_proven" {
		t.Fatalf("dispositions = %q/%q", first.Disposition, second.Disposition)
	}
	proofData, err := os.ReadFile(workMigrationProofPath(destinationPath))
	if err != nil {
		t.Fatalf("read proof: %v", err)
	}
	var proof workMigrationProof
	if err := json.Unmarshal(proofData, &proof); err != nil {
		t.Fatalf("decode proof: %v", err)
	}
	if proof.Version != workMigrationProofVersion ||
		len(proof.WorkIDs) != 2 ||
		proof.SourceWitness != first.SourceWitness ||
		proof.NativeReadbackWitness != first.SourceWitness ||
		proof.DependencyWitness != first.DependencyWitness ||
		proof.DestinationPrefix != "dr" ||
		proof.CommandVersion == "" ||
		proof.Disposition != "already_proven" ||
		len(proof.ImportedIDs) != 0 {
		t.Fatalf("proof = %+v", proof)
	}
	if info, err := os.Stat(workMigrationProofPath(destinationPath)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("proof mode = %v, %v", info, err)
	}
	reader, err := beads.OpenNativeDoltStoreAtWithoutAmbientEnv(context.Background(), destinationPath)
	if err != nil {
		t.Fatalf("reopen native destination: %v", err)
	}
	migrated, err := reader.Get("gc-a")
	closeErr := reader.CloseStore()
	if err != nil || closeErr != nil {
		t.Fatalf("read migrated long title: %v; close: %v", err, closeErr)
	}
	if migrated.Title != longTitle {
		t.Fatalf("migrated title length = %d, want exact %d", len(migrated.Title), len(longTitle))
	}
	if _, leaked := migrated.Metadata["gc.work_migration_legacy_title"]; leaked {
		t.Fatalf("legacy title representation leaked through metadata: %+v", migrated.Metadata)
	}
	if err := verifyRetainedWorkMigrationAtBoot(
		context.Background(), destinationPath, "bd", "dr", defaultWorkMigrationBootRuntime(),
	); err != nil {
		t.Fatalf("boot verification of proven copy: %v", err)
	}
	mutator, err := beads.OpenNativeDoltStoreAtWithoutAmbientEnv(context.Background(), destinationPath)
	if err != nil {
		t.Fatalf("open native mutator: %v", err)
	}
	metadataErr := mutator.SetMetadata("gc-a", "runtime.after_cutover", "changed")
	newWork, createErr := mutator.Create(beads.Bead{Title: "new native work", Type: "task"})
	closeErr = mutator.CloseStore()
	if metadataErr != nil || createErr != nil || closeErr != nil {
		t.Fatalf("evolve native Work authority: metadata: %v; create: %v; close: %v", metadataErr, createErr, closeErr)
	}
	if !strings.HasPrefix(newWork.ID, "dr-") {
		t.Fatalf("new native Work id = %q, want destination prefix dr-", newWork.ID)
	}
	if err := verifyRetainedWorkMigrationAtBoot(
		context.Background(), destinationPath, "bd", "dr", defaultWorkMigrationBootRuntime(),
	); err != nil {
		t.Fatalf("boot verification after legitimate native Work mutation: %v", err)
	}
	if err := os.WriteFile(sourcePath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyRetainedWorkMigrationAtBoot(
		context.Background(), destinationPath, "bd", "dr", defaultWorkMigrationBootRuntime(),
	); err != nil {
		t.Fatalf("boot verification after non-semantic source churn: %v", err)
	}
	fixture.Beads[0].Title = "changed retained work"
	changedData, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, changedData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyRetainedWorkMigrationAtBoot(
		context.Background(), destinationPath, "bd", "dr", defaultWorkMigrationBootRuntime(),
	); !errors.Is(err, errWorkMigrationProofInvalid) {
		t.Fatalf("boot verification after Work mutation = %v, want proof invalid", err)
	}
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	mutator, err = beads.OpenNativeDoltStoreAtWithoutAmbientEnv(context.Background(), destinationPath)
	if err != nil {
		t.Fatalf("open native mutator: %v", err)
	}
	deleteErr := mutator.Delete("gc-b")
	closeErr = mutator.CloseStore()
	if deleteErr != nil || closeErr != nil {
		t.Fatalf("delete native row: %v; close: %v", deleteErr, closeErr)
	}
	if err := verifyRetainedWorkMigrationAtBoot(
		context.Background(), destinationPath, "bd", "dr", defaultWorkMigrationBootRuntime(),
	); !errors.Is(err, errWorkCopyUnconfirmed) {
		t.Fatalf("boot verification after native deletion = %v, want copy unconfirmed", err)
	}
}
