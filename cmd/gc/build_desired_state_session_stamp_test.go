package main

import (
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/molecule"
)

// countingStore wraps a Store and counts SetMetadataBatch / SetMetadata calls so
// a test can assert the stamp + affinity binding are idempotent (no writes once
// the bead already carries the resolved identity / slot binding).
type countingStore struct {
	beads.Store
	writes     int
	metaWrites int
}

func (c *countingStore) SetMetadataBatch(id string, kvs map[string]string) error {
	c.writes++
	return c.Store.SetMetadataBatch(id, kvs)
}

func (c *countingStore) SetMetadata(id, key, value string) error {
	c.metaWrites++
	return c.Store.SetMetadata(id, key, value)
}

func stampTestSession(name, workDir string) beads.Bead {
	return beads.Bead{
		ID:     "sess-" + name,
		Type:   "session",
		Status: "open",
		Metadata: map[string]string{
			"session_name": name,
			"work_dir":     workDir,
		},
	}
}

func TestStampRunSessionIdentityStampsInProgressAssignedBead(t *testing.T) {
	const sessionName = "codeprobe-worker-gc-1920"
	const workDir = "/home/ds/projects/codeprobe/codeprobe-worker-1"

	run := beads.Bead{ID: "co-run1", Type: "molecule", Status: "in_progress", Assignee: sessionName}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	store := &countingStore{Store: mem}
	sessions := newSessionBeadSnapshot([]beads.Bead{stampTestSession(sessionName, workDir)})

	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, sessions, io.Discard)

	got, err := mem.Get("co-run1")
	if err != nil {
		t.Fatalf("Get(co-run1): %v", err)
	}
	if got.Metadata["gc.session_name"] != sessionName {
		t.Errorf("gc.session_name = %q, want %q", got.Metadata["gc.session_name"], sessionName)
	}
	if got.Metadata["gc.work_dir"] != workDir {
		t.Errorf("gc.work_dir = %q, want %q", got.Metadata["gc.work_dir"], workDir)
	}

	// Idempotent: a second pass over the now-stamped bead writes nothing.
	stamped, _ := mem.Get("co-run1")
	store.writes = 0
	stampRunSessionIdentity([]beads.Bead{stamped}, []beads.Store{store}, sessions, io.Discard)
	if store.writes != 0 {
		t.Errorf("second pass wrote %d times, want 0 (stamp must be idempotent)", store.writes)
	}
}

func TestStampRunSessionIdentityPropagatesToRunRoot(t *testing.T) {
	// #2843: a worked in-progress STEP back-fills its workflow ROOT (which the
	// dashboard's root-only snapshot reads). The root is a control-lane bead,
	// never in_progress+assigned, so it is reached only via gc.root_bead_id.
	const sn = "gascity-packs-polecat-gc-1"
	const wd = "/home/ds/gascity-packs-worktrees/gascity-packs-polecat-1"
	root := beads.Bead{ID: "gpk-root", Type: "molecule", Status: "in_progress", Metadata: map[string]string{"gc.kind": "workflow"}}
	step := beads.Bead{ID: "gpk-step", Type: "step", Status: "in_progress", Assignee: sn, Metadata: map[string]string{"gc.step_ref": "wf.work", "gc.root_bead_id": "gpk-root"}}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{root, step}, nil)
	store := &countingStore{Store: mem}
	sessions := newSessionBeadSnapshot([]beads.Bead{stampTestSession(sn, wd)})

	stampRunSessionIdentity([]beads.Bead{step}, []beads.Store{store}, sessions, io.Discard)

	gotStep, _ := mem.Get("gpk-step")
	if gotStep.Metadata["gc.session_name"] != sn || gotStep.Metadata["gc.work_dir"] != wd {
		t.Errorf("step not stamped: session_name=%q work_dir=%q", gotStep.Metadata["gc.session_name"], gotStep.Metadata["gc.work_dir"])
	}
	gotRoot, _ := mem.Get("gpk-root")
	if gotRoot.Metadata["gc.session_name"] != sn {
		t.Errorf("root gc.session_name = %q, want %q (propagated from step)", gotRoot.Metadata["gc.session_name"], sn)
	}
	if gotRoot.Metadata["gc.work_dir"] != wd {
		t.Errorf("root gc.work_dir = %q, want %q (propagated from step)", gotRoot.Metadata["gc.work_dir"], wd)
	}

	// Idempotent: a second pass writes nothing (step + root already stamped).
	stamped, _ := mem.Get("gpk-step")
	store.writes = 0
	stampRunSessionIdentity([]beads.Bead{stamped}, []beads.Store{store}, sessions, io.Discard)
	if store.writes != 0 {
		t.Errorf("second pass wrote %d times, want 0 (step+root already stamped)", store.writes)
	}
}

func TestStampRunSessionIdentityBindsWorkflowSlotAffinity(t *testing.T) {
	// #2978: when a slot runs a graph.v2 workflow step, the workflow binds to
	// the slot's STABLE identity (alias), not its per-session session_name, and
	// that binding propagates to the workflow's open step beads so they
	// co-locate on the same slot rather than scattering across the pool.
	// slotID is slot-stable (survives rotation); sessionName is session-unique
	// and must NOT be used as the binding.
	const slotID = "rig/polecat-1"
	const sessionName = "rig-polecat-gc-555"
	root := beads.Bead{ID: "wf-root", Type: "molecule", Status: "in_progress", Metadata: map[string]string{"gc.kind": "workflow"}}
	step1 := beads.Bead{ID: "wf-s1", Type: "step", Status: "in_progress", Assignee: sessionName, Metadata: map[string]string{"gc.root_bead_id": "wf-root"}}
	step2 := beads.Bead{ID: "wf-s2", Type: "step", Status: "open", Metadata: map[string]string{"gc.root_bead_id": "wf-root"}}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{root, step1, step2}, nil)
	store := &countingStore{Store: mem}
	sess := beads.Bead{
		ID: "sess-1", Type: "session", Status: "open",
		Metadata: map[string]string{"session_name": sessionName, "alias": slotID, "work_dir": "/wt/1"},
	}
	sessions := newSessionBeadSnapshot([]beads.Bead{sess})

	stampRunSessionIdentity([]beads.Bead{step1}, []beads.Store{store}, sessions, io.Discard)

	for _, id := range []string{"wf-root", "wf-s1", "wf-s2"} {
		got, _ := mem.Get(id)
		if v := got.Metadata[molecule.SessionAffinitySlotMetadataKey]; v != slotID {
			t.Errorf("%s affinity = %q, want slot-stable %q (not session_name %q)", id, v, slotID, sessionName)
		}
	}

	// Idempotent: a second pass writes no metadata once everything is bound.
	stamped, _ := mem.Get("wf-s1")
	store.metaWrites = 0
	stampRunSessionIdentity([]beads.Bead{stamped}, []beads.Store{store}, sessions, io.Discard)
	if store.metaWrites != 0 {
		t.Errorf("second pass wrote %d affinity metadata, want 0 (binding must be idempotent)", store.metaWrites)
	}
}

func TestStampRunSessionIdentityAffinityFirstWriterWins(t *testing.T) {
	// A workflow already bound to polecat-2; a step of it briefly runs under
	// polecat-1 (startup race). The root's owner is authoritative and must NOT
	// be overwritten, and the open step converges to the recorded owner.
	root := beads.Bead{ID: "wf-root", Type: "molecule", Status: "in_progress", Metadata: map[string]string{"gc.kind": "workflow", molecule.SessionAffinitySlotMetadataKey: "rig/polecat-2"}}
	step := beads.Bead{ID: "wf-s1", Type: "step", Status: "in_progress", Assignee: "rig-polecat-gc-1", Metadata: map[string]string{"gc.root_bead_id": "wf-root"}}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{root, step}, nil)
	store := &countingStore{Store: mem}
	sess := beads.Bead{ID: "s1", Type: "session", Status: "open", Metadata: map[string]string{"session_name": "rig-polecat-gc-1", "alias": "rig/polecat-1", "work_dir": "/wt/1"}}
	sessions := newSessionBeadSnapshot([]beads.Bead{sess})

	stampRunSessionIdentity([]beads.Bead{step}, []beads.Store{store}, sessions, io.Discard)

	gotRoot, _ := mem.Get("wf-root")
	if v := gotRoot.Metadata[molecule.SessionAffinitySlotMetadataKey]; v != "rig/polecat-2" {
		t.Errorf("root affinity = %q, want unchanged rig/polecat-2 (first-writer-wins)", v)
	}
	gotStep, _ := mem.Get("wf-s1")
	if v := gotStep.Metadata[molecule.SessionAffinitySlotMetadataKey]; v != "rig/polecat-2" {
		t.Errorf("step affinity = %q, want propagated owner rig/polecat-2", v)
	}
}

func TestStampRunSessionIdentityNamedSessionUsesAlias(t *testing.T) {
	// Named sessions (e.g. mayor) carry an empty session_name; their
	// resolvable identifier lives in alias / configured_named_identity.
	mayor := beads.Bead{
		ID: "sess-mayor", Type: "session", Status: "open",
		Metadata: map[string]string{
			"session_name": "", "alias": "mayor",
			"configured_named_identity": "mayor", "work_dir": "/home/ds/gas-city",
		},
	}
	run := beads.Bead{ID: "dr-run", Type: "molecule", Status: "in_progress", Assignee: "mayor"}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	store := &countingStore{Store: mem}
	sessions := newSessionBeadSnapshot([]beads.Bead{mayor})

	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, sessions, io.Discard)

	got, _ := mem.Get("dr-run")
	if got.Metadata["gc.session_name"] != "mayor" {
		t.Errorf("gc.session_name = %q, want %q (alias fallback)", got.Metadata["gc.session_name"], "mayor")
	}
	if got.Metadata["gc.work_dir"] != "/home/ds/gas-city" {
		t.Errorf("gc.work_dir = %q, want /home/ds/gas-city", got.Metadata["gc.work_dir"])
	}
}

func TestStampRunSessionIdentityResolvesByIDAndAliasHistory(t *testing.T) {
	cases := map[string]struct {
		assignee string
		sess     beads.Bead
		wantName string
	}{
		"by bead ID": {
			assignee: "sess-pool",
			sess: beads.Bead{
				ID: "sess-pool", Type: "session", Status: "open",
				Metadata: map[string]string{"session_name": "pool-gc-9", "work_dir": "/wt/9"},
			},
			wantName: "pool-gc-9",
		},
		"by rotated alias_history": {
			// A pool worker whose alias rotated mid-run: the bead's Assignee is
			// a prior alias, still a live assignment identity.
			assignee: "old-alias",
			sess: beads.Bead{
				ID: "sess-rot", Type: "session", Status: "open",
				Metadata: map[string]string{
					"session_name": "pool-gc-10", "alias": "new-alias",
					"alias_history": "old-alias", "work_dir": "/wt/10",
				},
			},
			wantName: "pool-gc-10",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run := beads.Bead{ID: "r", Type: "molecule", Status: "in_progress", Assignee: tc.assignee}
			mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
			store := &countingStore{Store: mem}
			sessions := newSessionBeadSnapshot([]beads.Bead{tc.sess})

			stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, sessions, io.Discard)

			got, _ := mem.Get("r")
			if got.Metadata["gc.session_name"] != tc.wantName {
				t.Errorf("gc.session_name = %q, want %q", got.Metadata["gc.session_name"], tc.wantName)
			}
		})
	}
}

func TestStampRunSessionIdentitySkipsAmbiguousAssignee(t *testing.T) {
	// Two open sessions claim the same identity ("dupe") — a transient
	// duplicate-alias state. The stamp must skip rather than guess.
	a := beads.Bead{ID: "sa", Type: "session", Status: "open", Metadata: map[string]string{"alias": "dupe", "work_dir": "/a"}}
	b := beads.Bead{ID: "sb", Type: "session", Status: "open", Metadata: map[string]string{"alias": "dupe", "work_dir": "/b"}}
	run := beads.Bead{ID: "r", Type: "molecule", Status: "in_progress", Assignee: "dupe"}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	store := &countingStore{Store: mem}
	sessions := newSessionBeadSnapshot([]beads.Bead{a, b})

	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, sessions, io.Discard)

	if store.writes != 0 {
		t.Errorf("ambiguous assignee must not be stamped, got %d writes", store.writes)
	}
}

func TestStampRunSessionIdentityReassignmentRestamps(t *testing.T) {
	run := beads.Bead{
		ID: "co-run2", Type: "molecule", Status: "in_progress", Assignee: "worker-b",
		Metadata: map[string]string{"gc.session_name": "worker-a", "gc.work_dir": "/old"},
	}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	store := &countingStore{Store: mem}
	sessions := newSessionBeadSnapshot([]beads.Bead{stampTestSession("worker-b", "/new")})

	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, sessions, io.Discard)

	got, _ := mem.Get("co-run2")
	if got.Metadata["gc.session_name"] != "worker-b" || got.Metadata["gc.work_dir"] != "/new" {
		t.Errorf("reassignment not restamped: session_name=%q work_dir=%q",
			got.Metadata["gc.session_name"], got.Metadata["gc.work_dir"])
	}
}

func TestStampRunSessionIdentitySkipsNonExecuting(t *testing.T) {
	sessions := newSessionBeadSnapshot([]beads.Bead{stampTestSession("worker-x", "/wd")})

	cases := map[string]beads.Bead{
		"closed bead":        {ID: "b1", Status: "closed", Assignee: "worker-x"},
		"open (not claimed)": {ID: "b2", Status: "open", Assignee: "worker-x"},
		"no assignee":        {ID: "b3", Status: "in_progress", Assignee: ""},
		"unknown session":    {ID: "b4", Status: "in_progress", Assignee: "ghost"},
	}
	for name, wb := range cases {
		t.Run(name, func(t *testing.T) {
			mem := beads.NewMemStoreFrom(0, []beads.Bead{wb}, nil)
			store := &countingStore{Store: mem}
			stampRunSessionIdentity([]beads.Bead{wb}, []beads.Store{store}, sessions, io.Discard)
			if store.writes != 0 {
				t.Errorf("expected no stamp, got %d writes", store.writes)
			}
		})
	}
}

func TestStampRunSessionIdentityToleratesLengthMismatchAndNilSnapshot(t *testing.T) {
	run := beads.Bead{ID: "b", Status: "in_progress", Assignee: "w"}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	store := &countingStore{Store: mem}

	// Mismatched slice lengths must be a no-op, not a panic.
	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{}, newSessionBeadSnapshot(nil), io.Discard)
	// Nil snapshot must be a no-op, not a panic.
	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{store}, nil, io.Discard)
	if store.writes != 0 {
		t.Errorf("expected no writes on degenerate input, got %d", store.writes)
	}
}
