package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/worktree"
)

func TestReviewRejectsStaleDrainRelation(t *testing.T) {
	repo, base := worktreeTestRepo(t)
	rootDir := t.TempDir()
	spec := worktree.Spec{RepoDir: repo, Root: rootDir, Path: filepath.Join(rootDir, "member"), Branch: "work/member", Base: base, BeadID: "member", StoreRef: "city:test", Creator: "gc-sling", Owner: "gc-sling", Generation: "1", Lifecycle: worktree.LifecycleActive}
	report, err := worktree.Ensure(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.BaseSHA = report.Provenance.BaseSHA
	store := beads.NewMemStore()
	store.HonorExplicitIDs = true
	_, err = store.Create(beads.Bead{ID: "member", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:test", beadmeta.WorkDirMetadataKey: spec.Path, beadmeta.WorktreeRepoMetadataKey: spec.RepoDir, beadmeta.WorktreeRootMetadataKey: spec.Root, beadmeta.WorkBranchMetadataKey: spec.Branch, beadmeta.WorktreeBaseRefMetadataKey: spec.Base, beadmeta.WorktreeBaseSHAMetadataKey: spec.BaseSHA, beadmeta.WorktreeCreatorMetadataKey: spec.Creator, beadmeta.WorktreeOwnerMetadataKey: spec.Owner, beadmeta.WorktreeGenerationMetadataKey: spec.Generation, beadmeta.WorktreeLifecycleMetadataKey: spec.Lifecycle,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(beads.Bead{ID: "root", Metadata: map[string]string{beadmeta.RootStoreRefMetadataKey: "city:test", beadmeta.KindMetadataKey: beadmeta.KindWorkflow, beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2, beadmeta.DrainMemberIDMetadataKey: "member", beadmeta.DrainMemberStoreRefMetadataKey: "city:test"}})
	if err != nil {
		t.Fatal(err)
	}
	step, err := store.Create(beads.Bead{ID: "step", Metadata: map[string]string{beadmeta.RootStoreRefMetadataKey: "city:test", beadmeta.RootBeadIDMetadataKey: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{{StoreRef: "city", Store: store}})
	binding, err := drainWorkspaceBindingForExecution(step, "city:test", resolver)
	if err != nil || binding == nil {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	bp := &agentBuildParams{beadStore: store, city: &config.City{Workspace: config.Workspace{Name: "test"}}}
	request := SessionRequest{WorkBeadID: "step", WorkStoreRef: "city:test", WorktreeBinding: binding}
	if path, err := verifiedPoolTriggerWorkDir(bp, nil, "worker", request); err != nil || path != spec.Path {
		t.Fatalf("current relation rejected: path=%q err=%v", path, err)
	}
	if err := store.Update("root", beads.UpdateOpts{Metadata: map[string]string{beadmeta.DrainMemberIDMetadataKey: "different-member"}}); err != nil {
		t.Fatal(err)
	}
	path, err := verifiedPoolTriggerWorkDir(bp, nil, "worker", request)
	if err == nil {
		t.Fatalf("stale root-to-member relation accepted at destination: path=%q", path)
	}
	if err := store.Update("root", beads.UpdateOpts{Metadata: map[string]string{beadmeta.DrainMemberIDMetadataKey: "member"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, id, key, value string }{
		{"step-root", "step", beadmeta.RootBeadIDMetadataKey, "different-root"},
		{"owner-generation", "member", beadmeta.WorktreeGenerationMetadataKey, "2"},
		{"owner-path", "member", beadmeta.WorkDirMetadataKey, filepath.Join(rootDir, "other")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := store.Get(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Update(tc.id, beads.UpdateOpts{Metadata: map[string]string{tc.key: tc.value}}); err != nil {
				t.Fatal(err)
			}
			if _, err := verifiedPoolTriggerWorkDir(bp, nil, "worker", request); err == nil {
				t.Fatal("changed authority accepted")
			}
			if err := store.Update(tc.id, beads.UpdateOpts{Metadata: before.Metadata}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, id := range []string{"step", "root", "member"} {
		t.Run("unknown-"+id, func(t *testing.T) {
			readErr := errors.New("destination unavailable")
			unreadableBP := *bp
			unreadableBP.beadStore = &failingGetStore{Store: store, failID: id, err: readErr}
			if _, err := verifiedPoolTriggerWorkDir(&unreadableBP, nil, "worker", request); !errors.Is(err, readErr) {
				t.Fatalf("UNKNOWN error=%v", err)
			}
		})
	}
}
