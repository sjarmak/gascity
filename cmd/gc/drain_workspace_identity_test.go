package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/worktree"
)

func TestQualifiedBeadResolverResolvesExactStoreRefIndependentOfOrder(t *testing.T) {
	city := beads.NewMemStore()
	rigA := beads.NewMemStore()
	rigB := beads.NewMemStore()
	collisionID := ""
	for _, item := range []struct {
		store beads.Store
		ref   string
		title string
	}{
		{city, "city:test", "city"},
		{rigA, "rig:a", "rig-a"},
		{rigB, "rig:b", "rig-b"},
	} {
		created, err := item.store.Create(beads.Bead{Title: item.title, Metadata: map[string]string{
			beadmeta.RootStoreRefMetadataKey: item.ref,
		}})
		if err != nil {
			t.Fatalf("create %s collision: %v", item.ref, err)
		}
		if collisionID == "" {
			collisionID = created.ID
		} else if created.ID != collisionID {
			t.Fatalf("test stores generated ids %q and %q, want collision", collisionID, created.ID)
		}
	}
	orders := [][]qualifiedStoreBinding{
		{{StoreRef: "city", Store: city}, {StoreRef: "a", Store: rigA}, {StoreRef: "b", Store: rigB}},
		{{StoreRef: "b", Store: rigB}, {StoreRef: "city", Store: city}, {StoreRef: "a", Store: rigA}},
	}
	for i, bindings := range orders {
		got, err := newQualifiedBeadResolver(bindings).Resolve(qualifiedBeadIdentity{StoreRef: "rig:b", ID: collisionID})
		if err != nil {
			t.Fatalf("order %d Resolve: %v", i, err)
		}
		if got.Title != "rig-b" {
			t.Fatalf("order %d resolved %q, want rig-b", i, got.Title)
		}
	}
}

func TestQualifiedBeadResolverTreatsClassResidencyAsPhysicalOnly(t *testing.T) {
	classStore := beads.NewMemStore()
	member, err := classStore.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "rig:owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{{
		StoreRef: "class:graph", Store: classStore, ClassBinding: true,
	}})
	if _, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: "rig:other", ID: member.ID}); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("wrong logical ref error = %v, want ErrNotFound", err)
	}
	got, err := resolver.Resolve(qualifiedBeadIdentity{StoreRef: "rig:owner", ID: member.ID})
	if err != nil || got.ID != member.ID {
		t.Fatalf("qualified class resolve = %+v, %v", got, err)
	}
}

func TestDrainWorkspaceBindingUsesCurrentQualifiedMemberEvidence(t *testing.T) {
	repo, base := worktreeTestRepo(t)
	worktreeRoot := t.TempDir()
	path := filepath.Join(worktreeRoot, "member-1")
	spec := worktree.Spec{
		RepoDir: repo, Root: worktreeRoot, Path: path, Branch: "work/member-1", Base: base,
		BeadID: "member-1", StoreRef: "rig:owner", Creator: "gc-sling", Owner: "gc-sling",
		Generation: "4", Lifecycle: worktree.LifecycleActive,
	}
	report, err := worktree.Ensure(spec)
	if err != nil {
		t.Fatalf("Ensure member workspace: %v", err)
	}
	spec.BaseSHA = report.Provenance.BaseSHA

	executionStore := beads.NewMemStore()
	memberStore := beads.NewMemStore()
	memberStore.HonorExplicitIDs = true
	memberMetadata := map[string]string{
		beadmeta.RootStoreRefMetadataKey:       "rig:owner",
		beadmeta.WorkDirMetadataKey:            spec.Path,
		beadmeta.WorktreeRepoMetadataKey:       spec.RepoDir,
		beadmeta.WorktreeRootMetadataKey:       spec.Root,
		beadmeta.WorkBranchMetadataKey:         spec.Branch,
		beadmeta.WorktreeBaseRefMetadataKey:    spec.Base,
		beadmeta.WorktreeBaseSHAMetadataKey:    spec.BaseSHA,
		beadmeta.WorktreeCreatorMetadataKey:    spec.Creator,
		beadmeta.WorktreeOwnerMetadataKey:      spec.Owner,
		beadmeta.WorktreeGenerationMetadataKey: spec.Generation,
		beadmeta.WorktreeLifecycleMetadataKey:  spec.Lifecycle,
	}
	member, err := memberStore.Create(beads.Bead{ID: "member-1", Type: "task", Metadata: memberMetadata})
	if err != nil {
		t.Fatal(err)
	}
	root, err := executionStore.Create(beads.Bead{Type: "task", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:        "city:test",
		beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
		beadmeta.DrainMemberIDMetadataKey:       member.ID,
		beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	step, err := executionStore.Create(beads.Bead{Type: "task", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:test",
		beadmeta.RootBeadIDMetadataKey:   root.ID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{
		{StoreRef: "city", Store: executionStore},
		{StoreRef: "owner", Store: memberStore},
	})
	binding, err := drainWorkspaceBindingForExecution(step, "city:test", resolver)
	if err != nil {
		t.Fatalf("drainWorkspaceBindingForExecution: %v", err)
	}
	if binding == nil || binding.Execution.ID != step.ID || binding.Execution.StoreRef != "city:test" ||
		binding.Owner.ID != member.ID || binding.Owner.StoreRef != "rig:owner" || binding.Spec.Path != path {
		t.Fatalf("binding = %+v, want qualified execution and current member workspace", binding)
	}
	if _, err := worktree.Verify(binding.Spec); err != nil {
		t.Fatalf("strict member provenance verification: %v", err)
	}

	memberMetadata[beadmeta.WorktreeGenerationMetadataKey] = ""
	if err := memberStore.Update(member.ID, beads.UpdateOpts{Metadata: memberMetadata}); err != nil {
		t.Fatal(err)
	}
	if _, err := drainWorkspaceBindingForExecution(step, "city:test", resolver); err == nil ||
		!strings.Contains(err.Error(), beadmeta.WorktreeGenerationMetadataKey) {
		t.Fatalf("partial current member evidence error = %v", err)
	}
}

func TestDrainWorkspaceBindingRejectsWrongQualifiedRelation(t *testing.T) {
	executionStore := beads.NewMemStore()
	memberStore := beads.NewMemStore()
	root, _ := executionStore.Create(beads.Bead{ID: "root-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:        "city:test",
		beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
		beadmeta.DrainMemberIDMetadataKey:       "member-1",
		beadmeta.DrainMemberStoreRefMetadataKey: "rig:wrong",
	}})
	step, _ := executionStore.Create(beads.Bead{ID: "step-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:test",
		beadmeta.RootBeadIDMetadataKey:   root.ID,
	}})
	_, _ = memberStore.Create(beads.Bead{ID: "member-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "rig:owner",
	}})
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{
		{StoreRef: "city", Store: executionStore},
		{StoreRef: "owner", Store: memberStore},
	})
	if _, err := drainWorkspaceBindingForExecution(step, "city:test", resolver); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("wrong member store error = %v, want ErrNotFound", err)
	}
	if _, err := drainWorkspaceBindingForExecution(step, "city:other", resolver); err == nil {
		t.Fatal("wrong execution store was accepted")
	}
}

func TestDefaultScaleCheckDemandCarriesQualifiedDrainWorkspaceBinding(t *testing.T) {
	const template = "workflows/worker"
	executionStore := beads.NewMemStore()
	executionStore.HonorExplicitIDs = true
	memberStore := beads.NewMemStore()
	memberStore.HonorExplicitIDs = true
	if _, err := memberStore.Create(beads.Bead{ID: "member-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:       "rig:owner",
		beadmeta.WorkDirMetadataKey:            "/worktrees/member-1",
		beadmeta.WorktreeRepoMetadataKey:       "/repos/gascity",
		beadmeta.WorktreeRootMetadataKey:       "/worktrees",
		beadmeta.WorkBranchMetadataKey:         "work/member-1",
		beadmeta.WorktreeBaseRefMetadataKey:    "main",
		beadmeta.WorktreeBaseSHAMetadataKey:    strings.Repeat("a", 40),
		beadmeta.WorktreeCreatorMetadataKey:    "gc-sling",
		beadmeta.WorktreeOwnerMetadataKey:      "gc-sling",
		beadmeta.WorktreeGenerationMetadataKey: "9",
		beadmeta.WorktreeLifecycleMetadataKey:  worktree.LifecycleActive,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := executionStore.Create(beads.Bead{ID: "root-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:        "city:test",
		beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
		beadmeta.DrainMemberIDMetadataKey:       "member-1",
		beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := executionStore.Create(beads.Bead{ID: "step-1", Type: "task", Status: "open", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:test",
		beadmeta.RootBeadIDMetadataKey:   "root-1",
		beadmeta.RoutedToMetadataKey:     template,
	}}); err != nil {
		t.Fatal(err)
	}
	workspaceStores := []qualifiedStoreBinding{
		{StoreRef: "city", Store: executionStore},
		{StoreRef: "owner", Store: memberStore},
	}
	counts, demand, partial, errs := defaultScaleCheckCountsAndDemand(nil, []defaultScaleCheckTarget{{
		template: template, storeKey: "city", store: executionStore, workspaceStores: workspaceStores,
	}})
	if len(errs) > 0 || len(partial) > 0 || counts[template] != 1 {
		t.Fatalf("scale demand count=%v partial=%v errs=%v", counts, partial, errs)
	}
	binding := demand[template].WorktreeBindings["step-1"]
	if binding == nil || binding.Execution != (qualifiedBeadIdentity{StoreRef: "city:test", ID: "step-1"}) ||
		binding.Owner != (qualifiedBeadIdentity{StoreRef: "rig:owner", ID: "member-1"}) {
		t.Fatalf("qualified drain binding = %+v", binding)
	}
	if got := demand[template].StoreRefs["step-1"]; got != "city:test" {
		t.Fatalf("execution store ref = %q, want city:test", got)
	}
	request := requestWithScaleDemandProvenance(SessionRequest{}, demand[template], "step-1")
	if request.WorktreeBinding != binding || request.WorkBeadID != "step-1" || request.WorkStoreRef != "city:test" {
		t.Fatalf("session request lost qualified binding: %+v", request)
	}
}
