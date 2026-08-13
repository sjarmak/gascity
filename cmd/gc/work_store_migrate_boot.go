package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/coordclass"
)

var errWorkMigrationProofInvalid = errors.New("retained Work migration proof invalid")

type workMigrationBootRuntime struct {
	readSource      func(string) (beads.Store, []byte, error)
	readProof       func(string) ([]byte, error)
	openDestination func(context.Context, string) (workMigrationDestination, error)
}

func defaultWorkMigrationBootRuntime() workMigrationBootRuntime {
	return workMigrationBootRuntime{
		readSource: openWorkMigrationFileSource,
		readProof:  os.ReadFile,
		openDestination: func(ctx context.Context, workspace string) (workMigrationDestination, error) {
			return beads.OpenNativeDoltStoreAtWithoutAmbientEnv(ctx, workspace)
		},
	}
}

func verifyRetainedWorkMigrationAtBoot(
	ctx context.Context,
	cityPath, provider, expectedPrefix string,
	runtime workMigrationBootRuntime,
) error {
	if !providerUsesBdStoreContract(provider) {
		return nil
	}
	sourcePath := filepath.Join(cityPath, ".gc", "beads.json")
	source, _, err := runtime.readSource(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking retained Work source: %w", err)
	}
	hasWork, err := retainedSourceHasWork(source)
	if err != nil {
		return err
	}
	if !hasWork {
		return nil
	}
	snapshot, err := readWorkMigrationSnapshot(source)
	if err != nil {
		return invalidRetainedWorkSource(err)
	}
	proof, err := readAndValidateWorkMigrationProof(
		cityPath, sourcePath, expectedPrefix, snapshot, runtime,
	)
	if err != nil {
		return err
	}
	if err := verifyBootWorkMigrationDestination(ctx, cityPath, proof, runtime); err != nil {
		return err
	}
	currentSource, _, err := runtime.readSource(sourcePath)
	if err != nil {
		return fmt.Errorf("%w: rereading retained source: %w", errWorkMigrationSourceChanged, err)
	}
	currentSnapshot, err := readWorkMigrationSnapshot(currentSource)
	if err != nil {
		return fmt.Errorf("%w: retained Work source no longer satisfies the migration invariant: %w", errWorkMigrationSourceChanged, err)
	}
	if workMigrationSnapshotChanged(snapshot, currentSnapshot) {
		return errWorkMigrationSourceChanged
	}
	return nil
}

func retainedSourceHasWork(source beads.Store) (bool, error) {
	rows, err := source.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
	if err != nil {
		return false, fmt.Errorf("listing retained Work source: %w", err)
	}
	for _, row := range rows {
		if coordclass.Classify(row) == coordclass.ClassWork {
			return true, nil
		}
	}
	return false, nil
}

func readAndValidateWorkMigrationProof(
	cityPath, sourcePath, expectedPrefix string,
	snapshot workMigrationSnapshot,
	runtime workMigrationBootRuntime,
) (workMigrationProof, error) {
	proofPath := workMigrationProofPath(cityPath)
	data, err := runtime.readProof(proofPath)
	if err != nil {
		return workMigrationProof{}, invalidWorkMigrationProof(proofPath, "reading manifest: %v", err)
	}
	proof, err := decodeWorkMigrationProof(data)
	if err != nil {
		return workMigrationProof{}, invalidWorkMigrationProof(proofPath, "decoding manifest: %v", err)
	}
	if err := validateWorkMigrationProof(proof, cityPath, sourcePath, expectedPrefix, snapshot); err != nil {
		return workMigrationProof{}, invalidWorkMigrationProof(proofPath, "%v", err)
	}
	return proof, nil
}

func decodeWorkMigrationProof(data []byte) (workMigrationProof, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var proof workMigrationProof
	if err := decoder.Decode(&proof); err != nil {
		return workMigrationProof{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return workMigrationProof{}, errors.New("trailing JSON value")
		}
		return workMigrationProof{}, err
	}
	return proof, nil
}

func validateWorkMigrationProof(
	proof workMigrationProof,
	cityPath, sourcePath, expectedPrefix string,
	snapshot workMigrationSnapshot,
) error {
	expectedIDs := workMigrationIDs(snapshot.Exact.Rows)
	switch {
	case proof.Version != workMigrationProofVersion:
		return fmt.Errorf("version %d, want %d", proof.Version, workMigrationProofVersion)
	case !strictSameWorkMigrationPath(proof.SourceFile, sourcePath):
		return fmt.Errorf("source_file %q does not name %q", proof.SourceFile, sourcePath)
	case !validSHA256String(proof.SourceFileSHA256):
		return errors.New("source_file_sha256 is not a canonical SHA-256 digest")
	case proof.SourceWitness != snapshot.Exact.SourceWitness:
		return errors.New("source_witness does not match the retained Work rows")
	case !slices.Equal(proof.WorkIDs, expectedIDs):
		return errors.New("work_ids do not match the retained Work rows")
	case proof.DependencyWitness != snapshot.DependencyWitness:
		return errors.New("dependency_witness does not match the retained Work graph")
	case !strictSameWorkMigrationPath(proof.DestinationWorkspace, cityPath):
		return fmt.Errorf("destination_workspace %q does not name %q", proof.DestinationWorkspace, cityPath)
	case proof.DestinationPrefix != expectedPrefix:
		return fmt.Errorf("destination_prefix %q, want %q", proof.DestinationPrefix, expectedPrefix)
	case proof.NativeReadbackWitness != snapshot.Exact.SourceWitness:
		return errors.New("native_readback_witness does not match the retained Work rows")
	case !validWorkMigrationDisposition(proof, expectedIDs):
		return errors.New("disposition and imported_ids are inconsistent")
	case strings.TrimSpace(proof.CommandVersion) == "":
		return errors.New("command_version is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, proof.VerifiedAt); err != nil {
		return fmt.Errorf("verified_at is invalid: %w", err)
	}
	return nil
}

func workMigrationSnapshotChanged(before, after workMigrationSnapshot) bool {
	return before.Exact.SourceWitness != after.Exact.SourceWitness ||
		before.DependencyWitness != after.DependencyWitness ||
		!slices.Equal(workMigrationIDs(before.Exact.Rows), workMigrationIDs(after.Exact.Rows))
}

func invalidRetainedWorkSource(cause error) error {
	return fmt.Errorf(
		"retained Work source cannot support proven bd startup: %w; keep the file provider, restore the Work-only migration invariant, then rerun gc storage migrate-work",
		cause,
	)
}

func strictSameWorkMigrationPath(actual, expected string) bool {
	return filepath.IsAbs(actual) && filepath.Clean(actual) == actual && samePath(actual, expected)
}

func validWorkMigrationDisposition(proof workMigrationProof, expectedIDs []string) bool {
	switch proof.Disposition {
	case "already_proven":
		return len(proof.ImportedIDs) == 0
	case "imported":
		imported := slices.Clone(proof.ImportedIDs)
		sort.Strings(imported)
		return slices.Equal(imported, expectedIDs)
	default:
		return false
	}
}

func verifyBootWorkMigrationDestination(
	ctx context.Context,
	cityPath string,
	proof workMigrationProof,
	runtime workMigrationBootRuntime,
) error {
	destination, err := runtime.openDestination(ctx, cityPath)
	if err != nil {
		return fmt.Errorf("opening proven native Work destination: %w", err)
	}
	if destination.IDPrefix() != proof.DestinationPrefix {
		err = invalidWorkMigrationProof(
			workMigrationProofPath(cityPath), "native destination prefix %q, want %q",
			destination.IDPrefix(), proof.DestinationPrefix,
		)
	} else {
		err = verifyBootWorkMigrationContinuity(destination, proof)
	}
	closeErr := destination.CloseStore()
	if closeErr != nil {
		closeErr = fmt.Errorf("%w: closing boot verifier: %w", errWorkVerificationUnavailable, closeErr)
	}
	return errors.Join(err, closeErr)
}

func verifyBootWorkMigrationContinuity(destination beads.Store, proof workMigrationProof) error {
	stamped, err := destination.List(beads.ListQuery{
		IncludeClosed: true,
		TierMode:      beads.TierBoth,
		AllowScan:     true,
		Metadata: map[string]string{
			beadmeta.WorkMigrationSourceWitnessMetadataKey: proof.SourceWitness,
		},
	})
	if err != nil {
		return fmt.Errorf("%w: listing native Work continuity: %w", errWorkVerificationUnavailable, err)
	}
	ids := make([]string, 0, len(stamped))
	for _, row := range stamped {
		if row.Metadata[beadmeta.WorkMigrationSourceWitnessMetadataKey] != proof.SourceWitness {
			return fmt.Errorf("%w: destination row %q does not carry the proven source witness", errWorkCopyUnconfirmed, row.ID)
		}
		ids = append(ids, row.ID)
	}
	sort.Strings(ids)
	if !slices.Equal(ids, proof.WorkIDs) {
		return fmt.Errorf("%w: destination witness ids %v, want %v", errWorkCopyUnconfirmed, ids, proof.WorkIDs)
	}
	return nil
}

func invalidWorkMigrationProof(path, format string, args ...any) error {
	detail := fmt.Sprintf(format, args...)
	return fmt.Errorf(
		"%w at %s: %s; refresh it with gc storage migrate-work before selecting the bd provider",
		errWorkMigrationProofInvalid, path, detail,
	)
}
