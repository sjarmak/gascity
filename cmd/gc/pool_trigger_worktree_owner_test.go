package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worktree"
)

func TestBindPoolSessionTriggerBeadVerifiesManagedWorktreeBeforePublishing(t *testing.T) {
	repo, base := worktreeTestRepo(t)
	root := t.TempDir()
	path := filepath.Join(root, "gc-test")
	spec := worktree.Spec{
		RepoDir: repo, Root: root, Path: path, Branch: "work/gc-test", Base: base,
		BeadID: "gc-test", StoreRef: "rig:gascity", Creator: "gc-sling",
		Owner: "gc-sling", Generation: "1", Lifecycle: worktree.LifecycleActive,
	}
	report, err := worktree.Ensure(spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	spec.BaseSHA = report.Provenance.BaseSHA

	store := beads.NewMemStore()
	sessionBead, err := store.Create(beads.Bead{ID: "session-1", Type: "session"})
	if err != nil {
		t.Fatalf("Create session bead: %v", err)
	}
	info, err := sessionFrontDoor(store).Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get session info: %v", err)
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "city"},
		Agents: []config.Agent{{
			Name: "worker", WorkDir: filepath.Join(root, "slot"),
		}},
	}
	bp := newAgentBuildParams("city", t.TempDir(), cfg, runtime.NewFake(), time.Now().UTC(), store, &bytes.Buffer{})

	bound, err := bindPoolSessionTriggerBead(bp, &cfg.Agents[0], "worker", info, SessionRequest{
		WorkBeadID: "gc-test", WorkStoreRef: "rig:gascity", WorktreeSpec: &spec,
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if bound.WorkDirCanonical != path || bound.WorkDir != path {
		t.Fatalf("bound work dirs = canonical %q legacy %q, want verified %q", bound.WorkDirCanonical, bound.WorkDir, path)
	}
	persisted, err := store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get persisted session: %v", err)
	}
	if persisted.Metadata[beadmeta.WorkDirMetadataKey] != path ||
		persisted.Metadata[beadmeta.LegacyWorkDirMetadataKey] != path {
		t.Fatalf("persisted metadata = %+v, want verified work dir twins", persisted.Metadata)
	}
}

// TestBindPoolSessionTriggerBeadSeparatesExecutionFromWorkspaceOwner captures
// the drain contract: the session executes a materialized step while entering
// the current, strictly verified workspace owned by the drain member.
func TestBindPoolSessionTriggerBeadSeparatesExecutionFromWorkspaceOwner(t *testing.T) {
	repo, base := worktreeTestRepo(t)
	root := t.TempDir()
	path := filepath.Join(root, "member-1")
	memberSpec := worktree.Spec{
		RepoDir: repo, Root: root, Path: path, Branch: "work/member-1", Base: base,
		BeadID: "member-1", StoreRef: "rig:owner", Creator: "gc-sling",
		Owner: "gc-sling", Generation: "1", Lifecycle: worktree.LifecycleActive,
	}
	report, err := worktree.Ensure(memberSpec)
	if err != nil {
		t.Fatalf("Ensure member workspace: %v", err)
	}
	memberSpec.BaseSHA = report.Provenance.BaseSHA

	store := beads.NewMemStore()
	sessionBead, err := store.Create(beads.Bead{ID: "session-1", Type: "session", Metadata: map[string]string{
		"session_name": "worker-1",
		"template":     "worker",
		"pool_slot":    "1",
	}})
	if err != nil {
		t.Fatalf("Create session bead: %v", err)
	}
	info, err := sessionFrontDoor(store).Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get session info: %v", err)
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "city"},
		Agents:    []config.Agent{{Name: "worker", WorkDir: filepath.Join(root, "slot")}},
	}
	bp := newAgentBuildParams("city", t.TempDir(), cfg, runtime.NewFake(), time.Now().UTC(), store, &bytes.Buffer{})

	store.HonorExplicitIDs = true
	memberStore := beads.NewMemStore()
	memberStore.HonorExplicitIDs = true
	_, err = memberStore.Create(beads.Bead{ID: "member-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:       "rig:owner",
		beadmeta.WorkDirMetadataKey:            memberSpec.Path,
		beadmeta.WorktreeRepoMetadataKey:       memberSpec.RepoDir,
		beadmeta.WorktreeRootMetadataKey:       memberSpec.Root,
		beadmeta.WorkBranchMetadataKey:         memberSpec.Branch,
		beadmeta.WorktreeBaseRefMetadataKey:    memberSpec.Base,
		beadmeta.WorktreeBaseSHAMetadataKey:    memberSpec.BaseSHA,
		beadmeta.WorktreeCreatorMetadataKey:    memberSpec.Creator,
		beadmeta.WorktreeOwnerMetadataKey:      memberSpec.Owner,
		beadmeta.WorktreeGenerationMetadataKey: memberSpec.Generation,
		beadmeta.WorktreeLifecycleMetadataKey:  memberSpec.Lifecycle,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(beads.Bead{ID: "root-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:city", beadmeta.KindMetadataKey: beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
		beadmeta.DrainMemberIDMetadataKey:   "member-1", beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(beads.Bead{ID: "step-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:city", beadmeta.RootBeadIDMetadataKey: "root-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	bp.sessionCensusRigStores = map[string]beads.Store{"owner": memberStore}
	cfg.Rigs = []config.Rig{{Name: "owner", Path: filepath.Join(root, "owner-rig")}}
	bound, err := bindPoolSessionTriggerBead(bp, &cfg.Agents[0], "worker", info, SessionRequest{
		WorkBeadID: "step-1", WorkStoreRef: "city:city",
		WorktreeBinding: &poolWorktreeBinding{
			Execution: qualifiedBeadIdentity{StoreRef: "city:city", ID: "step-1"},
			Root:      qualifiedBeadIdentity{StoreRef: "city:city", ID: "root-1"},
			Owner:     qualifiedBeadIdentity{StoreRef: "rig:owner", ID: "member-1"},
			Spec:      memberSpec,
		},
	})
	if err != nil {
		t.Fatalf("bind drain step to member workspace: %v", err)
	}
	if bound.WorkDirCanonical != path || bound.WorkDir != path {
		t.Fatalf("bound work dirs = canonical %q legacy %q, want member workspace %q", bound.WorkDirCanonical, bound.WorkDir, path)
	}
	persisted, err := store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get persisted session: %v", err)
	}
	if got := persisted.Metadata[beadmeta.TriggerBeadIDMetadataKey]; got != "step-1" {
		t.Fatalf("trigger bead = %q, want execution step-1", got)
	}
	prepared, err := prepareStartCandidate(startCandidate{
		info: bound,
		tp:   TemplateParams{TemplateName: "worker", SessionName: "worker-1", WorkDir: filepath.Join(root, "slot")},
	}, cfg, store, &clock.Fake{Time: time.Now().UTC()})
	if err != nil {
		t.Fatalf("prepare bound drain start: %v", err)
	}
	if prepared.cfg.WorkDir != path {
		t.Fatalf("prepared start work dir = %q, want verified member path %q", prepared.cfg.WorkDir, path)
	}
	if got := prepared.cfg.Env["GC_TRIGGER_BEAD_ID"]; got != "step-1" {
		t.Fatalf("prepared execution id = %q, want step-1", got)
	}
	provider := runtime.NewFake()
	if err := provider.Start(context.Background(), "worker-1", prepared.cfg); err != nil {
		t.Fatalf("start bound drain step: %v", err)
	}
	starts := 0
	for _, call := range provider.Calls {
		if call.Method == "Start" && call.Name == "worker-1" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("start calls = %d, want exactly one", starts)
	}
	if started := provider.LastStartConfig("worker-1"); started == nil || started.WorkDir != path || started.Env["GC_TRIGGER_BEAD_ID"] != "step-1" {
		t.Fatalf("started config = %+v, want member work dir with step execution id", started)
	}
	if err := store.Close("step-1"); err != nil {
		t.Fatal(err)
	}
	successor, err := store.Create(beads.Bead{ID: "step-2", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "city:city", beadmeta.RootBeadIDMetadataKey: "root-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{{StoreRef: "city:city", Store: store}, {StoreRef: "rig:owner", Store: memberStore}})
	nextBinding, err := drainWorkspaceBindingForExecution(successor, "city:city", resolver)
	if err != nil {
		t.Fatal(err)
	}
	bound.State = "asleep"
	next, err := bindPoolSessionTriggerBead(bp, &cfg.Agents[0], "worker", bound, SessionRequest{
		SessionBeadID: bound.ID, Tier: "resume", WorkBeadID: successor.ID, WorkStoreRef: "city:city", WorktreeBinding: nextBinding,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != bound.ID || next.TriggerBeadID != successor.ID || next.WorkDirCanonical != path {
		t.Fatalf("successor did not rebind retained identity to member workspace: %+v", next)
	}
	persistedNext, err := store.Get(bound.ID)
	if err != nil || persistedNext.Metadata[beadmeta.TriggerBeadIDMetadataKey] != successor.ID {
		t.Fatalf("successor binding not persisted: %+v err=%v", persistedNext, err)
	}
}

func TestBindPoolSessionTriggerBeadRejectsCompetingOwnerBeforeMetadata(t *testing.T) {
	repo, base := worktreeTestRepo(t)
	root := t.TempDir()
	path := filepath.Join(root, "gc-test")
	spec := worktree.Spec{
		RepoDir: repo, Root: root, Path: path, Branch: "work/gc-test", Base: base,
		BeadID: "gc-test", StoreRef: "rig:gascity", Creator: "gc-sling",
		Owner: "gc-sling", Generation: "1", Lifecycle: worktree.LifecycleActive,
	}
	report, err := worktree.Ensure(spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	spec.BaseSHA = report.Provenance.BaseSHA
	spec.Owner = "formula"

	store := beads.NewMemStore()
	sessionBead, err := store.Create(beads.Bead{ID: "session-1", Type: "session"})
	if err != nil {
		t.Fatalf("Create session bead: %v", err)
	}
	info, err := sessionFrontDoor(store).Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get session info: %v", err)
	}
	cfg := &config.City{Workspace: config.Workspace{Name: "city"}, Agents: []config.Agent{{Name: "worker"}}}
	bp := newAgentBuildParams("city", t.TempDir(), cfg, runtime.NewFake(), time.Now().UTC(), store, &bytes.Buffer{})

	if _, err := bindPoolSessionTriggerBead(bp, &cfg.Agents[0], "worker", info, SessionRequest{
		WorkBeadID: "gc-test", WorkStoreRef: "rig:gascity", WorktreeSpec: &spec,
	}); err == nil {
		t.Fatal("bind accepted competing worktree owner")
	}
	persisted, err := store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get persisted session: %v", err)
	}
	if persisted.Metadata[beadmeta.WorkDirMetadataKey] != "" ||
		persisted.Metadata[beadmeta.LegacyWorkDirMetadataKey] != "" ||
		persisted.Metadata[beadmeta.TriggerBeadIDMetadataKey] != "" {
		t.Fatalf("failed verification published partial metadata: %+v", persisted.Metadata)
	}
}

func TestWorktreeSpecForBeadRequiresCompletePublishedEvidence(t *testing.T) {
	metadata := map[string]string{
		beadmeta.WorkDirMetadataKey:            "/worktrees/gc-test",
		beadmeta.WorkBranchMetadataKey:         "work/gc-test",
		beadmeta.WorktreeRootMetadataKey:       "/worktrees",
		beadmeta.WorktreeRepoMetadataKey:       "/repos/gascity",
		beadmeta.WorktreeBaseRefMetadataKey:    "main",
		beadmeta.WorktreeBaseSHAMetadataKey:    strings.Repeat("a", 40),
		beadmeta.WorktreeCreatorMetadataKey:    "gc-sling",
		beadmeta.WorktreeOwnerMetadataKey:      "gc-sling",
		beadmeta.WorktreeGenerationMetadataKey: "7",
		beadmeta.WorktreeLifecycleMetadataKey:  worktree.LifecycleActive,
	}
	bead := beads.Bead{ID: "gc-test", Metadata: metadata}

	spec, err := worktreeSpecForBead(bead, "rig:gascity")
	if err != nil {
		t.Fatalf("worktreeSpecForBead: %v", err)
	}
	if spec == nil || spec.BeadID != bead.ID || spec.StoreRef != "rig:gascity" ||
		spec.RepoDir != metadata[beadmeta.WorktreeRepoMetadataKey] ||
		spec.Root != metadata[beadmeta.WorktreeRootMetadataKey] ||
		spec.Path != metadata[beadmeta.WorkDirMetadataKey] ||
		spec.Branch != metadata[beadmeta.WorkBranchMetadataKey] ||
		spec.Base != metadata[beadmeta.WorktreeBaseRefMetadataKey] ||
		spec.BaseSHA != metadata[beadmeta.WorktreeBaseSHAMetadataKey] ||
		spec.Creator != metadata[beadmeta.WorktreeCreatorMetadataKey] ||
		spec.Owner != metadata[beadmeta.WorktreeOwnerMetadataKey] ||
		spec.Generation != metadata[beadmeta.WorktreeGenerationMetadataKey] ||
		spec.Lifecycle != metadata[beadmeta.WorktreeLifecycleMetadataKey] {
		t.Fatalf("spec = %+v, want exact published evidence", spec)
	}

	delete(metadata, beadmeta.WorktreeOwnerMetadataKey)
	if _, err := worktreeSpecForBead(bead, "rig:gascity"); err == nil ||
		!strings.Contains(err.Error(), beadmeta.WorktreeOwnerMetadataKey) {
		t.Fatalf("incomplete evidence error = %v, want missing owner key", err)
	}

	delete(metadata, beadmeta.WorkDirMetadataKey)
	if spec, err := worktreeSpecForBead(bead, "rig:gascity"); err != nil || spec != nil {
		t.Fatalf("bead without work_dir = spec %+v err %v, want no managed workspace", spec, err)
	}
}

// TestWorktreeSpecForBeadTreatsWorkDirOnlyAsLegacy covers a bead carrying
// work_dir without ownership evidence -- the shape stampDrainItemRecipe still
// mints for every drain recipe step. That is not "incomplete evidence" (the
// bead never claimed to publish any), so the pool must plan the seat unmanaged
// instead of erroring it into permanent starvation. Both work_dir spellings
// are covered: old beads carry the bare key, not the gc.-prefixed one.
func TestWorktreeSpecForBeadTreatsWorkDirOnlyAsLegacy(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "canonical", key: beadmeta.WorkDirMetadataKey},
		{name: "legacy", key: beadmeta.LegacyWorkDirMetadataKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bead := beads.Bead{ID: "gc-test", Metadata: map[string]string{
				tc.key: "/worktrees/gc-test",
			}}

			spec, err := worktreeSpecForBead(bead, "rig:gascity")
			if err != nil {
				t.Fatalf("worktreeSpecForBead: %v, want a work_dir-only bead to be treated as unmanaged, not errored", err)
			}
			if spec != nil {
				t.Fatalf("spec = %+v, want nil so the seat spawns exactly as it did before #5193", spec)
			}
		})
	}
}

// TestWorktreeSpecForBeadRejectsSingleOwnershipKey pins the boundary the
// unmanaged branch creates: nine missing ownership keys is "never published",
// but eight missing is partial evidence and must still fail closed. An
// off-by-one in the len(missing) == len(values) test would silently launch a
// seat into a directory whose ownership was only half-published.
func TestWorktreeSpecForBeadRejectsSingleOwnershipKey(t *testing.T) {
	bead := beads.Bead{ID: "gc-test", Metadata: map[string]string{
		beadmeta.WorkDirMetadataKey: "/worktrees/gc-test",
		// Exactly one of the nine, and deliberately the last one checked, so
		// the reported missing key is the first of the remaining eight.
		beadmeta.WorktreeLifecycleMetadataKey: worktree.LifecycleActive,
	}}

	spec, err := worktreeSpecForBead(bead, "rig:gascity")
	if err == nil {
		t.Fatalf("spec = %+v err = nil, want partial ownership evidence to fail closed", spec)
	}
	if spec != nil {
		t.Fatalf("spec = %+v, want nil alongside the error", spec)
	}
	if !strings.Contains(err.Error(), beadmeta.WorktreeRepoMetadataKey) {
		t.Fatalf("error = %v, want it to name the first missing key %q", err, beadmeta.WorktreeRepoMetadataKey)
	}
}

func TestWorktreeSpecForBeadPrefersCanonicalStoreRef(t *testing.T) {
	metadata := map[string]string{
		beadmeta.WorkDirMetadataKey:            "/worktrees/gc-test",
		beadmeta.WorkBranchMetadataKey:         "work/gc-test",
		beadmeta.WorktreeRootMetadataKey:       "/worktrees",
		beadmeta.WorktreeRepoMetadataKey:       "/repos/gascity",
		beadmeta.WorktreeBaseRefMetadataKey:    "main",
		beadmeta.WorktreeBaseSHAMetadataKey:    strings.Repeat("a", 40),
		beadmeta.WorktreeCreatorMetadataKey:    "gc-sling",
		beadmeta.WorktreeOwnerMetadataKey:      "gc-sling",
		beadmeta.WorktreeGenerationMetadataKey: "7",
		beadmeta.WorktreeLifecycleMetadataKey:  worktree.LifecycleActive,
		beadmeta.RootStoreRefMetadataKey:       "city:test-city",
	}
	bead := beads.Bead{ID: "gc-test", Metadata: metadata}

	// The probe shorthand "city" would not match the provenance the creating
	// side published under the canonical ref, so verification would refuse a
	// workspace that is ours.
	spec, err := worktreeSpecForBead(bead, "city")
	if err != nil {
		t.Fatalf("worktreeSpecForBead: %v", err)
	}
	if spec.StoreRef != "city:test-city" {
		t.Fatalf("StoreRef = %q, want the bead's canonical %q", spec.StoreRef, "city:test-city")
	}

	delete(metadata, beadmeta.RootStoreRefMetadataKey)
	fallback, err := worktreeSpecForBead(beads.Bead{ID: "gc-test", Metadata: metadata}, "city")
	if err != nil {
		t.Fatalf("worktreeSpecForBead without canonical ref: %v", err)
	}
	if fallback.StoreRef != "city" {
		t.Fatalf("fallback StoreRef = %q, want the caller's %q", fallback.StoreRef, "city")
	}
}
