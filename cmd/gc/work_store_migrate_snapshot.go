package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/coordclass"
)

type workMigrationSnapshot struct {
	Exact             beads.ExactWorkSnapshot
	DependencyWitness string
}

func readWorkMigrationSnapshot(source beads.Store) (workMigrationSnapshot, error) {
	all, err := source.List(beads.ListQuery{IncludeClosed: true, TierMode: beads.TierBoth, AllowScan: true})
	if err != nil {
		return workMigrationSnapshot{}, fmt.Errorf("listing file source: %w", err)
	}
	classes := make(map[string]coordclass.Class, len(all))
	rows := make([]beads.Bead, 0, len(all))
	for _, row := range all {
		class := coordclass.Classify(row)
		classes[row.ID] = class
		if class == coordclass.ClassGraph && row.Metadata[beadmeta.StepIDMetadataKey] != "" && row.Metadata[beadmeta.RoutedToMetadataKey] == "" {
			return workMigrationSnapshot{}, fmt.Errorf("%w: graph dispatch unit %q lacks %s", errWorkMigrationSourceInvalid, row.ID, beadmeta.RoutedToMetadataKey)
		}
		if class != coordclass.ClassWork {
			continue
		}
		if row.Metadata[beadmeta.RoutedToMetadataKey] != "" && row.Metadata[beadmeta.ExecutionRoutedToMetadataKey] != "" {
			return workMigrationSnapshot{}, fmt.Errorf("%w: work row %q carries both routing authorities", errWorkMigrationSourceInvalid, row.ID)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) == 0 {
		return workMigrationSnapshot{}, fmt.Errorf("%w: source has no Work rows", errWorkMigrationSourceInvalid)
	}
	for index := range rows {
		deps, err := source.DepList(rows[index].ID, "down")
		if err != nil {
			return workMigrationSnapshot{}, fmt.Errorf("listing dependencies of %q: %w", rows[index].ID, err)
		}
		for _, dep := range deps {
			if dep.IssueID != rows[index].ID || dep.DependsOnID == "" || dep.Type == "" || dep.IssueID == dep.DependsOnID {
				return workMigrationSnapshot{}, fmt.Errorf("%w: malformed dependency %q -> %q (%q)", errWorkMigrationSourceInvalid, dep.IssueID, dep.DependsOnID, dep.Type)
			}
			class, found := classes[dep.DependsOnID]
			if !found || class != coordclass.ClassWork {
				return workMigrationSnapshot{}, fmt.Errorf("%w: dependency %q -> %q leaves the Work closure", errWorkMigrationSourceInvalid, dep.IssueID, dep.DependsOnID)
			}
		}
		sortWorkMigrationDependencies(deps)
		rows[index].Dependencies = deps
	}
	witness, err := workMigrationRowsWitness(rows)
	if err != nil {
		return workMigrationSnapshot{}, err
	}
	depWitness, err := workMigrationDependencyWitness(rows)
	if err != nil {
		return workMigrationSnapshot{}, err
	}
	return workMigrationSnapshot{
		Exact:             beads.ExactWorkSnapshot{SourceWitness: witness, Rows: rows},
		DependencyWitness: depWitness,
	}, nil
}

func workMigrationRowsWitness(rows []beads.Bead) (string, error) {
	canonical := expectedWorkMigrationRows(beads.ExactWorkSnapshot{Rows: rows})
	for index := range canonical {
		delete(canonical[index].Metadata, beadmeta.WorkMigrationSourceWitnessMetadataKey)
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encoding canonical Work rows: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func workMigrationDependencyWitness(rows []beads.Bead) (string, error) {
	var deps []beads.Dep
	for _, row := range rows {
		deps = append(deps, row.Dependencies...)
	}
	sortWorkMigrationDependencies(deps)
	data, err := json.Marshal(deps)
	if err != nil {
		return "", fmt.Errorf("encoding canonical Work dependencies: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func expectedWorkMigrationRows(snapshot beads.ExactWorkSnapshot) []beads.Bead {
	rows := make([]beads.Bead, len(snapshot.Rows))
	for index, source := range snapshot.Rows {
		row := source
		row.Priority = cloneWorkMigrationPriority(source.Priority)
		if row.Priority != nil && *row.Priority == 2 {
			row.Priority = nil
		}
		if row.UpdatedAt.IsZero() {
			row.UpdatedAt = row.CreatedAt
		}
		row.CreatedAt = beads.CanonicalWorkMigrationTime(row.CreatedAt)
		row.UpdatedAt = beads.CanonicalWorkMigrationTime(row.UpdatedAt)
		if row.DeferUntil != nil {
			canonical := beads.CanonicalWorkMigrationTime(*row.DeferUntil)
			row.DeferUntil = &canonical
		}
		if row.Status == "" {
			row.Status = "open"
		}
		if row.Type == "" {
			row.Type = "task"
		}
		row.Metadata = maps.Clone(source.Metadata)
		if row.Metadata == nil {
			row.Metadata = make(beads.StringMap)
		}
		if snapshot.SourceWitness != "" {
			row.Metadata[beadmeta.WorkMigrationSourceWitnessMetadataKey] = snapshot.SourceWitness
		}
		row.Labels = slices.Clone(source.Labels)
		sort.Strings(row.Labels)
		row.Labels = slices.Compact(row.Labels)
		row.Needs = slices.Clone(source.Needs)
		row.Dependencies = slices.Clone(source.Dependencies)
		sortWorkMigrationDependencies(row.Dependencies)
		row.IsBlocked = nil
		row.Revision = 0
		row.ClaimFence = 0
		rows[index] = row
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func verifyWorkMigrationSnapshot(destination beads.Store, snapshot beads.ExactWorkSnapshot) (string, error) {
	expected := expectedWorkMigrationRows(snapshot)
	expectedIncoming := make(map[string][]beads.Dep, len(expected))
	for _, row := range expected {
		for _, dep := range row.Dependencies {
			expectedIncoming[dep.DependsOnID] = append(expectedIncoming[dep.DependsOnID], dep)
		}
	}
	stamped, err := destination.List(beads.ListQuery{
		IncludeClosed: true,
		TierMode:      beads.TierBoth,
		AllowScan:     true,
		Metadata:      map[string]string{beadmeta.WorkMigrationSourceWitnessMetadataKey: snapshot.SourceWitness},
	})
	if err != nil {
		return "", fmt.Errorf("%w: listing destination witness: %w", errWorkVerificationUnavailable, err)
	}
	if len(stamped) != len(expected) {
		return "", fmt.Errorf("%w: destination witness covers %d rows, want %d", errWorkCopyUnconfirmed, len(stamped), len(expected))
	}
	readback := make([]beads.Bead, 0, len(expected))
	for _, want := range expected {
		got, err := destination.Get(want.ID)
		if errors.Is(err, beads.ErrNotFound) {
			return "", fmt.Errorf("%w: destination row %q is missing", errWorkCopyUnconfirmed, want.ID)
		}
		if err != nil {
			return "", fmt.Errorf("%w: reading destination row %q: %w", errWorkVerificationUnavailable, want.ID, err)
		}
		if diff := beadCopyDifference(want, got); diff != "" {
			return "", fmt.Errorf("%w: row %q differs: %s", errWorkCopyUnconfirmed, want.ID, diff)
		}
		gotDeps, err := destination.DepList(want.ID, "down")
		if err != nil {
			return "", fmt.Errorf("%w: listing destination dependencies for %q: %w", errWorkVerificationUnavailable, want.ID, err)
		}
		if diff := exactWorkDependencyDifference(want.Dependencies, gotDeps); diff != "" {
			return "", fmt.Errorf("%w: row %q dependencies differ: %s", errWorkCopyUnconfirmed, want.ID, diff)
		}
		gotIncoming, err := destination.DepList(want.ID, "up")
		if err != nil {
			return "", fmt.Errorf("%w: listing incoming destination dependencies for %q: %w", errWorkVerificationUnavailable, want.ID, err)
		}
		if diff := exactWorkDependencyDifference(expectedIncoming[want.ID], gotIncoming); diff != "" {
			return "", fmt.Errorf("%w: row %q incoming dependencies differ: %s", errWorkCopyUnconfirmed, want.ID, diff)
		}
		got.Dependencies = gotDeps
		readback = append(readback, got)
	}
	witness, err := workMigrationRowsWitness(readback)
	if err != nil {
		return "", fmt.Errorf("%w: witnessing native destination: %w", errWorkVerificationUnavailable, err)
	}
	if witness != snapshot.SourceWitness {
		return "", fmt.Errorf("%w: native readback witness %s, want %s", errWorkCopyUnconfirmed, witness, snapshot.SourceWitness)
	}
	return witness, nil
}

func exactWorkDependencyDifference(want, got []beads.Dep) string {
	wantKeys := workMigrationDependencyKeys(want)
	gotKeys := workMigrationDependencyKeys(got)
	if slices.Equal(wantKeys, gotKeys) {
		return ""
	}
	return fmt.Sprintf("got %s, want %s", strings.Join(gotKeys, ", "), strings.Join(wantKeys, ", "))
}

func workMigrationDependencyKeys(deps []beads.Dep) []string {
	keys := make([]string, len(deps))
	for index, dep := range deps {
		keys[index] = dep.IssueID + "\x00" + dep.DependsOnID + "\x00" + dep.Type
	}
	sort.Strings(keys)
	return keys
}

func sortWorkMigrationDependencies(deps []beads.Dep) {
	sort.Slice(deps, func(i, j int) bool {
		left, right := deps[i], deps[j]
		if left.IssueID != right.IssueID {
			return left.IssueID < right.IssueID
		}
		if left.DependsOnID != right.DependsOnID {
			return left.DependsOnID < right.DependsOnID
		}
		return left.Type < right.Type
	})
}

func cloneWorkMigrationPriority(priority *int) *int {
	if priority == nil {
		return nil
	}
	value := *priority
	return &value
}
