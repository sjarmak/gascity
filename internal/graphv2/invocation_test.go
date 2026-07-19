package graphv2

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/formulatest"
)

func TestPrepareInvocationCreatesInputConvoyForBeadTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy == "" {
		t.Fatalf("invocation = %+v, want input convoy", inv)
	}
	if got := inv.Vars[ConvoyIDVar]; got != inv.InputConvoy {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, inv.InputConvoy)
	}
	members, err := convoycore.Members(store, inv.InputConvoy, true)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != target.ID {
		t.Fatalf("members = %+v, want target %s", members, target.ID)
	}
	created, err := store.Get(inv.InputConvoy)
	if err != nil {
		t.Fatalf("Get(input convoy): %v", err)
	}
	if created.Type != "convoy" {
		t.Fatalf("input convoy type = %q, want convoy", created.Type)
	}
	wantMetadata := map[string]string{
		"gc.synthetic":              "true",
		"gc.graphv2_invocation_key": "graphv2-input-convoy:" + target.ID + ":work",
	}
	if !maps.Equal(created.Metadata, wantMetadata) {
		t.Fatalf("input convoy metadata = %+v, want %+v", created.Metadata, wantMetadata)
	}

	again, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation again: %v", err)
	}
	if again.InputConvoy != inv.InputConvoy {
		t.Fatalf("input convoy = %s, want the live one already tracking %s (%s)", again.InputConvoy, target.ID, inv.InputConvoy)
	}
}

// TestNormalizeInputConvoyReusesLiveSyntheticConvoy pins the stable-identity
// half of RootKey. RootKey is documented as stable and varsFingerprint already
// excludes convoy_id to keep it so, but a freshly minted convoy per invocation
// put a different ID in the key's leading segment, so two dispatches of the
// same formula at the same target produced two different keys and therefore two
// live roots (gc-28jm).
func TestNormalizeInputConvoyReusesLiveSyntheticConvoy(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	first, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy: %v", err)
	}
	second, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy again: %v", err)
	}
	if first != second {
		t.Fatalf("input convoy = %s then %s, want one stable convoy per target", first, second)
	}

	convoys, err := convoycore.TrackingConvoysForItem(store, target.ID)
	if err != nil {
		t.Fatalf("TrackingConvoysForItem: %v", err)
	}
	if len(convoys) != 1 {
		t.Fatalf("tracking convoys = %d, want exactly one (no orphan litter); convoys=%+v", len(convoys), convoys)
	}
	members, err := convoycore.Members(store, second, true)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != target.ID {
		t.Fatalf("members = %+v, want the reused convoy to still track %s exactly once", members, target.ID)
	}
}

func TestInputConvoyLockKeyMatchesPersistedIdentity(t *testing.T) {
	base := InputConvoyLockKey(" target ", " work ")
	if base != InputConvoyLockKey("target", "work") {
		t.Fatal("lock key does not canonicalize the persisted target+formula identity")
	}
	if base == InputConvoyLockKey("target:work", "") {
		t.Fatal("lock key aliases distinct target+formula tuples")
	}
	if base == InputConvoyLockKey("target", "other") {
		t.Fatal("lock key does not distinguish formulas")
	}
}

// TestNormalizeInputConvoyRepairsMissingTrackOnReuse covers the convoy that
// exists but tracks nothing, which reuse newly makes reachable: creating the
// convoy and tracking its target are separate writes, so a failure between them
// leaves one behind, and handing it back untouched would run a formula against
// an empty input.
func TestNormalizeInputConvoyRepairsMissingTrackOnReuse(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	// A convoy stamped with the invocation key but never tracked — the shape a
	// crash between Create and TrackItem leaves behind.
	stranded, err := store.Create(beads.Bead{
		Title: "input convoy for " + target.ID,
		Type:  "convoy",
		Metadata: map[string]string{
			syntheticMetadataKey: "true",
			graphV2InvocationKey: InputConvoyInvocationKey(target.ID, "work"),
		},
	})
	if err != nil {
		t.Fatalf("Create stranded convoy: %v", err)
	}

	got, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy: %v", err)
	}
	if got != stranded.ID {
		t.Fatalf("input convoy = %q, want the stranded convoy %q reused", got, stranded.ID)
	}
	members, err := convoycore.Members(store, got, true)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != target.ID {
		t.Fatalf("members = %+v, want the track to %s repaired", members, target.ID)
	}
}

// TestNormalizeInputConvoySeparatesFormulasOnSameTarget keeps reuse scoped to
// one formula. The formula-cook path rejects any second live graph.v2 root
// sharing an input convoy, so a convoy shared across formulas would turn a
// legitimate second formula at the same target into a spurious conflict.
func TestNormalizeInputConvoySeparatesFormulasOnSameTarget(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	work, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy(work): %v", err)
	}
	review, err := NormalizeInputConvoy(store, target.ID, "review")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy(review): %v", err)
	}
	if work == review {
		t.Fatalf("both formulas share input convoy %s, want one per (target, formula)", work)
	}
}

// TestNormalizeInputConvoyDoesNotReuseTerminalConvoy keeps a retry legitimate:
// a closed convoy is spent, so the next invocation must mint a fresh one rather
// than resurrect it.
func TestNormalizeInputConvoyDoesNotReuseTerminalConvoy(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	first, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy: %v", err)
	}
	if err := store.Close(first); err != nil {
		t.Fatalf("Close(%s): %v", first, err)
	}

	second, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy after close: %v", err)
	}
	if second == first {
		t.Fatalf("input convoy = %s, want a fresh convoy after the previous one closed", second)
	}
}

// TestNormalizeInputConvoyRejectsMultipleLiveCandidates proves normalization
// no longer performs an unstable CreatedAt/ID election. Cross-process callers
// are serialized before this function; if legacy corruption presents two live
// candidates, their clock and ID ordering must not select a winner.
func TestNormalizeInputConvoyRejectsMultipleLiveCandidates(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	key := InputConvoyInvocationKey(target.ID, "work")
	for _, id := range []string{"zz-later-lexically", "aa-earlier-lexically"} {
		if _, err := store.Create(beads.Bead{ID: id, Title: "candidate", Type: "convoy", Metadata: map[string]string{
			graphV2InvocationKey: key,
		}}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}
	if _, err := NormalizeInputConvoy(store, target.ID, "work"); err == nil || !strings.Contains(err.Error(), "multiple reusable convoys") {
		t.Fatalf("NormalizeInputConvoy error = %v, want deterministic corruption error instead of clock/ID election", err)
	}
}

func TestNormalizeInputConvoyReusesClosedConvoyWhileRootLive(t *testing.T) {
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	convoyID, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{Title: "live root", Type: "task", Metadata: map[string]string{
		beadmeta.InputConvoyIDMetadataKey:   convoyID,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(convoyID); err != nil {
		t.Fatal(err)
	}
	got, err := NormalizeInputConvoy(store, target.ID, "work")
	if err != nil {
		t.Fatal(err)
	}
	if got != convoyID {
		t.Fatalf("input convoy = %q, want closed convoy %q retained while its root is live", got, convoyID)
	}
}

// TestNormalizeInputConvoyPassesThroughConvoyTarget keeps an explicit convoy
// target on its existing path: it is already a stable identity and must not be
// wrapped in a synthetic convoy.
func TestNormalizeInputConvoyPassesThroughConvoyTarget(t *testing.T) {
	store := beads.NewMemStore()
	convoy, err := store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatalf("Create convoy: %v", err)
	}
	got, err := NormalizeInputConvoy(store, convoy.ID, "work")
	if err != nil {
		t.Fatalf("NormalizeInputConvoy: %v", err)
	}
	if got != convoy.ID {
		t.Fatalf("input convoy = %q, want the target convoy %q", got, convoy.ID)
	}
}

func TestNormalizeInputConvoyRejectsNilStore(t *testing.T) {
	_, err := NormalizeInputConvoy(nil, "target", "work")
	if err == nil {
		t.Fatal("NormalizeInputConvoy succeeded, want nil-store error")
	}
	if !strings.Contains(err.Error(), "requires a bead store") {
		t.Fatalf("error = %q, want nil-store message", err)
	}
}

func TestCreateSingleItemInputConvoyRejectsNilStore(t *testing.T) {
	_, err := CreateSingleItemInputConvoy(nil, beads.Bead{ID: "target", Status: "open"}, InputConvoyInvocationKey("target", "work"))
	if err == nil {
		t.Fatal("CreateSingleItemInputConvoy succeeded, want nil-store error")
	}
	if !strings.Contains(err.Error(), "requires a bead store") {
		t.Fatalf("error = %q, want nil-store message", err)
	}
}

func TestRootKeyIgnoresConvoyIDRuntimeVar(t *testing.T) {
	base := RootKey("convoy-1", "graph-work", map[string]string{
		ConvoyIDVar: "convoy-1",
		"mode":      "review",
	}, "", "")
	same := RootKey("convoy-1", "graph-work", map[string]string{
		ConvoyIDVar: "different-preview-value",
		"mode":      "review",
	}, "", "")
	if same != base {
		t.Fatalf("RootKey changed when only %s changed: %q vs %q", ConvoyIDVar, same, base)
	}
	changed := RootKey("convoy-1", "graph-work", map[string]string{
		ConvoyIDVar: "convoy-1",
		"mode":      "implement",
	}, "", "")
	if changed == base {
		t.Fatalf("RootKey did not change when non-reserved vars changed: %q", changed)
	}
}

func TestLockKeySerializesSameKey(t *testing.T) {
	var active int32
	var overlapped int32
	var wg sync.WaitGroup

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := LockKey("same-root-key")
			defer unlock()
			if n := atomic.AddInt32(&active, 1); n > 1 {
				atomic.StoreInt32(&overlapped, 1)
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}

	wg.Wait()
	if atomic.LoadInt32(&overlapped) != 0 {
		t.Fatal("LockKey allowed concurrent critical sections for the same key")
	}
}

func TestPrepareInvocationUsesFormulaCompilerRequirement(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
type = "workflow"

[requires]
formula_compiler = ">=2.0.0"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	isGraph, _, err := IsGraphV2Formula("work", []string{dir})
	if err != nil {
		t.Fatalf("IsGraphV2Formula: %v", err)
	}
	if !isGraph {
		t.Fatal("IsGraphV2Formula = false, want formula_compiler requirement to opt into graph semantics")
	}
	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy == "" {
		t.Fatalf("InputConvoy empty for formula_compiler graph invocation: %+v", inv)
	}
}

func TestPrepareInvocationHonorsFormulaRef(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	invocationGitOK(t)
	root := initInvocationRepo(t)
	formulaDir := filepath.Join(root, "formulas")
	if err := os.MkdirAll(formulaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFormula(t, formulaDir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	runInvocationGit(t, root, "add", "formulas/work.formula.toml")
	runInvocationGit(t, root, "commit", "-m", "graph formula")

	writeFormula(t, formulaDir, "work.formula.toml", `
formula = "work"
version = 1
type = "workflow"

[[steps]]
id = "legacy"
title = "Legacy working tree edit"
`)
	t.Setenv("GC_FORMULA_REF", "main")

	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	inv, err := PrepareInvocation(context.Background(), store, "work", []string{formulaDir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy == "" {
		t.Fatalf("InputConvoy empty; graph.v2 loader read working-tree formula instead of GC_FORMULA_REF")
	}
	if got := inv.Vars[ConvoyIDVar]; got != inv.InputConvoy {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, inv.InputConvoy)
	}
}

func TestPrepareInvocationRejectsClosedTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	if err := store.Close(target.ID); err != nil {
		t.Fatalf("Close target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want closed target error")
	}
	if !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("error = %q, want closed target", err)
	}
}

func TestPrepareInvocationDoesNotReusePreviousInputConvoy(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	existing, err := store.Create(beads.Bead{
		Title:    "existing input",
		Type:     "convoy",
		Metadata: map[string]string{"gc.synthetic": "true"},
	})
	if err != nil {
		t.Fatalf("Create existing input convoy: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy == existing.ID {
		t.Fatalf("InputConvoy = %q, want a fresh input convoy", inv.InputConvoy)
	}
	members, err := convoycore.Members(store, inv.InputConvoy, true)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != target.ID {
		t.Fatalf("members = %+v, want target %s", members, target.ID)
	}
}

func TestPrepareInvocationUsesExistingConvoyTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	convoy, err := store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatalf("Create convoy: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, convoy.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if inv.InputConvoy != convoy.ID {
		t.Fatalf("invocation = %+v, want existing convoy", inv)
	}
}

func TestPrepareInvocationRejectsCallerReservedVars(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, map[string]string{"issue": target.ID})
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want reserved var error")
	}
	if !strings.Contains(err.Error(), "reserved variable") {
		t.Fatalf("error = %q, want reserved variable", err)
	}
}

func TestPrepareInvocationRejectsMissingParentRuntimeVarsBeforeNormalizingTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}} with {{missing}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want missing runtime var error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %q, want missing runtime var", err)
	}
	inputConvoys, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List input convoys: %v", err)
	}
	if len(inputConvoys) != 0 {
		t.Fatalf("input convoys = %+v, want none before validation succeeds", inputConvoys)
	}
}

func TestPrepareInvocationTargetlessRejectsConvoyReferenceFromExpansion(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 2
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "work"
title = "Work"
expand = "needs-convoy"
`)
	writeFormula(t, dir, "needs-convoy.formula.toml", `
formula = "needs-convoy"
version = 2
contract = "graph.v2"
type = "expansion"

[[template]]
id = "{target}.expanded"
title = "Expanded {{convoy_id}}"
`)

	_, err := PrepareInvocation(context.Background(), beads.NewMemStore(), "parent", []string{dir}, "", nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want targetless expanded convoy_id error")
	}
	if !strings.Contains(err.Error(), "convoy_id requires a targeted formulas v2 invocation") {
		t.Fatalf("error = %q, want expanded convoy_id target error", err)
	}
}

func TestPrepareInvocationTargetlessRejectsConditionedConvoyReferenceFromExpansionBeforeFiltering(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 2
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "work"
title = "Work"
expand = "conditioned-convoy"
`)
	writeFormula(t, dir, "conditioned-convoy.formula.toml", `
formula = "conditioned-convoy"
version = 2
type = "expansion"

[[template]]
id = "{target}.targetless-only"
title = "Targetless-only work"
condition = "!{{convoy_id}}"
`)

	_, err := PrepareInvocation(context.Background(), beads.NewMemStore(), "parent", []string{dir}, "", nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want targetless expanded condition convoy_id error")
	}
	if !strings.Contains(err.Error(), "convoy_id requires a targeted formulas v2 invocation") {
		t.Fatalf("error = %q, want expanded condition convoy_id target error", err)
	}
}

func TestPrepareInvocationTargetlessRejectsConditionedConvoyReferenceBeforeFiltering(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 2
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "skip-when-targeted"
title = "Only targetless"
condition = "!{{convoy_id}}"
`)

	_, err := PrepareInvocation(context.Background(), beads.NewMemStore(), "work", []string{dir}, "", nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want targetless conditioned convoy_id error")
	}
	if !strings.Contains(err.Error(), "convoy_id requires a targeted formulas v2 invocation") {
		t.Fatalf("error = %q, want conditioned convoy_id target error", err)
	}
}

func TestPreparePreviewInvocationUsesPreviewInputConvoyForBeadTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PreparePreviewInvocation(context.Background(), store, "work", []string{dir}, target.ID, false, nil)
	if err != nil {
		t.Fatalf("PreparePreviewInvocation: %v", err)
	}
	want := previewInputConvoyPrefix + target.ID
	if inv.InputConvoy != want {
		t.Fatalf("preview invocation = %+v, want preview input convoy %q", inv, want)
	}
	if got := inv.Vars[ConvoyIDVar]; got != want {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, want)
	}
	matches, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List convoys: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("preview created input convoys = %+v, want none", matches)
	}
}

func TestPreparePreviewInvocationRoutingIdentityTargetSkipsBeadLookup(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()

	// A routing identity (e.g. a workflow root's gc.routed_to value) has no
	// bead-store entry; the preview must not fail the bead lookup.
	inv, err := PreparePreviewInvocation(context.Background(), store, "work", []string{dir}, "myrig/worker", true, nil)
	if err != nil {
		t.Fatalf("PreparePreviewInvocation: %v", err)
	}
	want := previewInputConvoyPrefix + "myrig/worker"
	if inv.InputConvoy != want {
		t.Fatalf("preview invocation = %+v, want routing-identity preview input convoy %q", inv, want)
	}
	if got := inv.Vars[ConvoyIDVar]; got != want {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, want)
	}
	matches, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List convoys: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("preview created input convoys = %+v, want none", matches)
	}
}

func TestPreviewInputConvoyIDForRoutingIdentity(t *testing.T) {
	got := PreviewInputConvoyIDForRoutingIdentity("  myrig/worker  ")
	want := previewInputConvoyPrefix + "myrig/worker"
	if got != want {
		t.Fatalf("PreviewInputConvoyIDForRoutingIdentity = %q, want %q", got, want)
	}
}

func TestPrepareInvocationRejectsUnsupportedDrainFromExpansionBeforeNormalizingTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 2
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "work"
title = "Work"
expand = "bad-drain"
`)
	writeFormula(t, dir, "bad-drain.formula.toml", `
formula = "bad-drain"
version = 2
contract = "graph.v2"
type = "expansion"

[[template]]
id = "{target}.drain"
title = "Drain"

[template.drain]
context = "separate"
formula = "item"
max_units = 101
`)
	writeFormula(t, dir, "item.formula.toml", `
formula = "item"
version = 2
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "work"
title = "Item {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "parent", []string{dir}, target.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want expanded drain validation error")
	}
	if !strings.Contains(err.Error(), `max_units must be <= 100`) {
		t.Fatalf("error = %q, want expanded drain max_units error", err)
	}
	matches, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List input convoys: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("input convoys = %+v, want none before validation succeeds", matches)
	}
}

func TestPrepareInvocationRejectsNonGraphDrainItemFormulaBeforeNormalizingTarget(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "drain"
title = "Drain"

[steps.drain]
context = "separate"
formula = "item"
`)
	writeFormula(t, dir, "item.formula.toml", `
formula = "item"
version = 1
type = "workflow"

[[steps]]
id = "work"
title = "Legacy item"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "parent", []string{dir}, target.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want drain item graph.v2 error")
	}
	if !strings.Contains(err.Error(), "must declare the formulas v2 contract ([requires] formula_compiler = \">=2.0.0\")") {
		t.Fatalf("error = %q, want formulas v2 item formula message", err)
	}
	matches, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List input convoys: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("input convoys = %+v, want none before validation succeeds", matches)
	}
}

func TestPrepareInvocationRejectsDrainItemFormulaMissingRuntimeVars(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "drain"
title = "Drain"

[steps.drain]
context = "separate"
formula = "item"
`)
	writeFormula(t, dir, "item.formula.toml", `
formula = "item"
version = 1
contract = "graph.v2"
type = "workflow"

[vars]
[vars.extra]
required = true

[[steps]]
id = "work"
title = "Item {{convoy_id}} {{extra}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "parent", []string{dir}, target.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want drain item runtime var error")
	}
	if !strings.Contains(err.Error(), "runtime vars") || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("error = %q, want missing item runtime var", err)
	}
}

func TestPrepareInvocationPassesParentRuntimeVarsToDrainItemValidation(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "drain"
title = "Drain"

[steps.drain]
context = "separate"
formula = "item"
`)
	writeFormula(t, dir, "item.formula.toml", `
formula = "item"
version = 1
contract = "graph.v2"
type = "workflow"

[vars]
[vars.extra]
required = true

[[steps]]
id = "work"
title = "Item {{convoy_id}} {{extra}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "parent", []string{dir}, target.ID, map[string]string{"extra": "provided"})
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if got := inv.Vars["extra"]; got != "provided" {
		t.Fatalf("vars[extra] = %q, want provided", got)
	}
}

func TestPrepareInvocationPassesParentDefaultVarsToDrainItemValidation(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "parent.formula.toml", `
formula = "parent"
version = 1
contract = "graph.v2"
type = "workflow"

[vars]
[vars.extra]
default = "from-default"

[[steps]]
id = "drain"
title = "Drain"

[steps.drain]
context = "separate"
formula = "item"
`)
	writeFormula(t, dir, "item.formula.toml", `
formula = "item"
version = 1
contract = "graph.v2"
type = "workflow"

[vars]
[vars.extra]
required = true

[[steps]]
id = "work"
title = "Item {{convoy_id}} {{extra}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "parent", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if got := inv.Vars["extra"]; got != "from-default" {
		t.Fatalf("vars[extra] = %q, want formula default", got)
	}
}

func TestRuntimeVarsMetadataExcludesReservedVars(t *testing.T) {
	raw := RuntimeVarsMetadata(map[string]string{
		ConvoyIDVar: "CONVOY-1",
		"issue":     "BD-1",
		"extra":     "provided",
	})
	if strings.Contains(raw, ConvoyIDVar) || strings.Contains(raw, "issue") {
		t.Fatalf("RuntimeVarsMetadata = %q, want reserved vars excluded", raw)
	}
	parsed, err := ParseRuntimeVarsMetadata(raw)
	if err != nil {
		t.Fatalf("ParseRuntimeVarsMetadata: %v", err)
	}
	if len(parsed) != 1 || parsed["extra"] != "provided" {
		t.Fatalf("parsed = %#v, want only extra", parsed)
	}
}

func writeFormula(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.TrimSpace(contents)+"\n"), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}
}

func invocationGitOK(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
}

func initInvocationRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runInvocationGit(t, root, "init", "-b", "main")
	runInvocationGit(t, root, "config", "user.email", "test@example.com")
	runInvocationGit(t, root, "config", "user.name", "test")
	runInvocationGit(t, root, "config", "commit.gpgsign", "false")
	return root
}

func runInvocationGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestPrepareInvocationResolvesDeprecatedIssueAlias(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "legacy.formula.toml", `
formula = "legacy"
version = 1
contract = "graph.v2"
type = "workflow"

[vars]
[vars.issue]
description = "legacy work bead"
required = true

[[steps]]
id = "inspect"
title = "Inspect {{issue}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "legacy", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if got := inv.Vars[LegacyIssueVar]; got != target.ID {
		t.Fatalf("vars[%s] = %q, want single tracked member %q", LegacyIssueVar, got, target.ID)
	}
	if got := inv.Vars[ConvoyIDVar]; got != inv.InputConvoy {
		t.Fatalf("vars[%s] = %q, want %q", ConvoyIDVar, got, inv.InputConvoy)
	}
	if len(inv.Deprecations) == 0 {
		t.Fatal("invocation has no deprecations, want issue alias warnings")
	}
	for _, d := range inv.Deprecations {
		if !strings.Contains(d, "2941") || !strings.Contains(d, "deprecated") {
			t.Fatalf("deprecation %q missing migration pointer", d)
		}
	}
}

func TestPrepareInvocationIssueAliasRequiresSingleMemberConvoy(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "legacy.formula.toml", `
formula = "legacy"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{issue}}"
`)
	store := beads.NewMemStore()
	convoy, err := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("Create convoy: %v", err)
	}
	for _, title := range []string{"one", "two"} {
		member, err := store.Create(beads.Bead{Title: title, Type: "task"})
		if err != nil {
			t.Fatalf("Create member: %v", err)
		}
		if err := convoycore.TrackItem(store, convoy.ID, member.ID); err != nil {
			t.Fatalf("TrackItem: %v", err)
		}
	}

	_, err = PrepareInvocation(context.Background(), store, "legacy", []string{dir}, convoy.ID, nil)
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want single-member alias error")
	}
	if !strings.Contains(err.Error(), "exactly one tracked member") {
		t.Fatalf("error = %q, want single-member alias message", err)
	}
}

func TestPrepareInvocationWithoutLegacyRefsDoesNotInjectIssue(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "work.formula.toml", `
formula = "work"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{convoy_id}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	inv, err := PrepareInvocation(context.Background(), store, "work", []string{dir}, target.ID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation: %v", err)
	}
	if _, ok := inv.Vars[LegacyIssueVar]; ok {
		t.Fatalf("vars = %+v, want no injected issue alias", inv.Vars)
	}
	if len(inv.Deprecations) != 0 {
		t.Fatalf("deprecations = %v, want none", inv.Deprecations)
	}
}

func TestPrepareInvocationStillRejectsCallerSuppliedIssue(t *testing.T) {
	formulatest.EnableV2ForTest(t)
	dir := t.TempDir()
	writeFormula(t, dir, "legacy.formula.toml", `
formula = "legacy"
version = 1
contract = "graph.v2"
type = "workflow"

[[steps]]
id = "inspect"
title = "Inspect {{issue}}"
`)
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "target", Type: "task"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	_, err = PrepareInvocation(context.Background(), store, "legacy", []string{dir}, target.ID, map[string]string{"issue": "user-bead"})
	if err == nil {
		t.Fatal("PrepareInvocation succeeded, want reserved-var rejection")
	}
	if !strings.Contains(err.Error(), "cannot be supplied by the caller") {
		t.Fatalf("error = %q, want caller-reserved message", err)
	}
}

func TestRootKeyIgnoresDeprecatedIssueRuntimeVar(t *testing.T) {
	base := RootKey("convoy-1", "graph-work", map[string]string{
		"mode": "review",
	}, "", "")
	withAlias := RootKey("convoy-1", "graph-work", map[string]string{
		"mode":          "review",
		LegacyIssueVar:  "gc-member",
		legacyBeadIDVar: "gc-member",
	}, "", "")
	if base != withAlias {
		t.Fatalf("RootKey with alias vars = %q, want %q (issue/bead_id must not affect idempotence keys)", withAlias, base)
	}
}
