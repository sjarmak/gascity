package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/githubmonitor"
)

type fakeGitHubPRLister struct {
	prs []githubmonitor.PullRequest
	err error
}

func (f fakeGitHubPRLister) ListOpenPullRequests(context.Context, string, string) ([]githubmonitor.PullRequest, error) {
	return f.prs, f.err
}

// stubGitHubPRLister is a pointer-backed lister whose PR set can be mutated
// between backfill runs to simulate evolving GitHub state.
type stubGitHubPRLister struct {
	prs []githubmonitor.PullRequest
}

func (s *stubGitHubPRLister) ListOpenPullRequests(context.Context, string, string) ([]githubmonitor.PullRequest, error) {
	return s.prs, nil
}

// stubGitHubRepairWorkflowAttach replaces the workflow-attach seam with a
// recorder so repair-bead tests stay hermetic (no on-disk formulas). The
// returned slice pointer accumulates the workflow name attached per create.
func stubGitHubRepairWorkflowAttach(t *testing.T) *[]string {
	t.Helper()
	old := attachGitHubPRRepairWorkflow
	calls := []string{}
	attachGitHubPRRepairWorkflow = func(_ beads.Store, _ *config.City, _ config.Rig, monitor config.GitHubPRMonitor, _ beads.Bead, _ githubmonitor.Result) error {
		calls = append(calls, monitor.RepairWorkflowOrDefault())
		return nil
	}
	t.Cleanup(func() { attachGitHubPRRepairWorkflow = old })
	return &calls
}

func TestGitHubPRBackfillCommandReportsActionableResults(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(token string) githubPRLister {
		if token != "token" {
			t.Fatalf("token = %q, want test token", token)
		}
		return fakeGitHubPRLister{prs: []githubmonitor.PullRequest{
			{
				Number:           2560,
				Title:            "Deploy",
				URL:              "https://github.com/partcleda/partcl/pull/2560",
				BaseRefName:      "main",
				HeadRefName:      "fix",
				HeadSHA:          "abc123",
				MergeStateStatus: "BLOCKED",
				Checks:           []githubmonitor.Check{{Name: "deploy", Status: "COMPLETED", Conclusion: "FAILURE"}},
			},
		}}
	}
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"--city", cityPath, "github", "pr", "backfill", "partcl-main", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var payload struct {
		MonitorCount    int `json:"monitor_count"`
		ResultCount     int `json:"result_count"`
		ActionableCount int `json:"actionable_count"`
		Results         []githubmonitor.Result
		OK              bool `json:"ok"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if !payload.OK {
		t.Fatal("ok = false, want true")
	}
	if payload.MonitorCount != 1 || payload.ResultCount != 1 || payload.ActionableCount != 1 {
		t.Fatalf("counts = monitors %d results %d actionable %d, want 1/1/1", payload.MonitorCount, payload.ResultCount, payload.ActionableCount)
	}
	if got := payload.Results[0]; got.Number != 2560 || got.State != githubmonitor.StateFailed || got.RepairRoute != "partcl/polecat" {
		t.Fatalf("result = %#v, want failing PR routed to partcl/polecat", got)
	}
}

func TestGitHubPRBackfillCommandCreatesDedupedRepairBeads(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	store := beads.NewMemStore()
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	oldStore := openGitHubPRRepairStore
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister {
		return fakeGitHubPRLister{prs: []githubmonitor.PullRequest{
			{
				Number:           2560,
				Title:            "Deploy",
				URL:              "https://github.com/partcleda/partcl/pull/2560",
				BaseRefName:      "main",
				HeadRefName:      "fix",
				HeadSHA:          "abc123",
				MergeStateStatus: "BLOCKED",
				Checks:           []githubmonitor.Check{{Name: "deploy", Status: "COMPLETED", Conclusion: "FAILURE"}},
			},
		}}
	}
	openGitHubPRRepairStore = func(string, string) (beads.Store, error) {
		return store, nil
	}
	attachCalls := stubGitHubRepairWorkflowAttach(t)
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
		openGitHubPRRepairStore = oldStore
	})

	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--city", cityPath, "github", "pr", "backfill", "partcl-main", "--create-repair-beads", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run %d code = %d, stdout = %q, stderr = %q", i, code, stdout.String(), stderr.String())
		}
	}

	created, err := store.ListByMetadata(map[string]string{
		"source":          "github-pr-monitor",
		"github.owner":    "partcleda",
		"github.repo":     "partcl",
		"github.pr":       "2560",
		"github.head_sha": "abc123",
	}, 0)
	if err != nil {
		t.Fatalf("ListByMetadata: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("created repair beads = %#v, want one deduped bead", created)
	}
	if got := created[0].Metadata["gc.routed_to"]; got != "partcl/polecat" {
		t.Fatalf("gc.routed_to = %q, want partcl/polecat", got)
	}
	if !strings.Contains(created[0].Description, "deploy") {
		t.Fatalf("description = %q, want failed check detail", created[0].Description)
	}
	// The workflow attaches exactly once — on creation, not on the second
	// (update) pass over the same PR/head.
	if len(*attachCalls) != 1 {
		t.Fatalf("workflow attach calls = %v, want exactly one (create only)", *attachCalls)
	}
	if (*attachCalls)[0] != "mol-polecat-work" {
		t.Fatalf("attached workflow = %q, want mol-polecat-work default", (*attachCalls)[0])
	}
}

func TestGitHubPRBackfillCoalescesAcrossFailureKindTransition(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	store := beads.NewMemStore()
	lister := &stubGitHubPRLister{}
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	oldStore := openGitHubPRRepairStore
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister { return lister }
	openGitHubPRRepairStore = func(string, string) (beads.Store, error) { return store, nil }
	stubGitHubRepairWorkflowAttach(t)
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
		openGitHubPRRepairStore = oldStore
	})

	// Pass 1: GitHub reports the PR as BLOCKED (failure_kind=blocked).
	lister.prs = []githubmonitor.PullRequest{{
		Number: 2601, Title: "Feature", URL: "https://github.com/partcleda/partcl/pull/2601",
		BaseRefName: "main", HeadRefName: "feat", HeadSHA: "deadbeef", MergeStateStatus: "BLOCKED",
	}}
	runGitHubBackfillOrFatal(t, cityPath)

	// Pass 2: same head SHA, but now a required check has failed
	// (failure_kind=checks_failed). This must update the existing bead, not
	// create a second one.
	lister.prs = []githubmonitor.PullRequest{{
		Number: 2601, Title: "Feature", URL: "https://github.com/partcleda/partcl/pull/2601",
		BaseRefName: "main", HeadRefName: "feat", HeadSHA: "deadbeef", MergeStateStatus: "BLOCKED",
		Checks: []githubmonitor.Check{{Name: "build", Status: "COMPLETED", Conclusion: "FAILURE"}},
	}}
	runGitHubBackfillOrFatal(t, cityPath)

	beadsForHead, err := store.ListByMetadata(map[string]string{
		"source":          "github-pr-monitor",
		"github.owner":    "partcleda",
		"github.repo":     "partcl",
		"github.pr":       "2601",
		"github.head_sha": "deadbeef",
	}, 0)
	if err != nil {
		t.Fatalf("ListByMetadata: %v", err)
	}
	if len(beadsForHead) != 1 {
		t.Fatalf("repair beads for PR/head = %d, want one coalesced bead across failure-kind transition", len(beadsForHead))
	}
	if got := beadsForHead[0].Metadata["github.failure_kind"]; got != githubmonitor.FailureKindChecksFailed {
		t.Fatalf("failure_kind = %q, want refreshed to %q", got, githubmonitor.FailureKindChecksFailed)
	}
	if got := beadsForHead[0].Metadata["github.failed_checks"]; !strings.Contains(got, "build") {
		t.Fatalf("failed_checks = %q, want refreshed build failure", got)
	}
}

func TestGitHubPRBackfillCreatesSeparateBeadForNewHeadSHA(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	store := beads.NewMemStore()
	lister := &stubGitHubPRLister{}
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	oldStore := openGitHubPRRepairStore
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister { return lister }
	openGitHubPRRepairStore = func(string, string) (beads.Store, error) { return store, nil }
	stubGitHubRepairWorkflowAttach(t)
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
		openGitHubPRRepairStore = oldStore
	})

	base := githubmonitor.PullRequest{
		Number: 2601, Title: "Feature", URL: "https://github.com/partcleda/partcl/pull/2601",
		BaseRefName: "main", HeadRefName: "feat", MergeStateStatus: "DIRTY",
	}
	first := base
	first.HeadSHA = "sha-one"
	lister.prs = []githubmonitor.PullRequest{first}
	runGitHubBackfillOrFatal(t, cityPath)

	// A force-push changes the head SHA: stale-commit failures should not be
	// merged into a fresh bead, so a new bead is keyed on the new SHA.
	second := base
	second.HeadSHA = "sha-two"
	lister.prs = []githubmonitor.PullRequest{second}
	runGitHubBackfillOrFatal(t, cityPath)

	all, err := store.ListByMetadata(map[string]string{
		"source":       "github-pr-monitor",
		"github.owner": "partcleda",
		"github.repo":  "partcl",
		"github.pr":    "2601",
	}, 0)
	if err != nil {
		t.Fatalf("ListByMetadata: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("repair beads = %d, want one per distinct head SHA (2)", len(all))
	}
}

func TestGitHubPRBackfillDispatchesWorkflowOnCreateOnly(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	store := beads.NewMemStore()
	lister := &stubGitHubPRLister{prs: []githubmonitor.PullRequest{{
		Number: 2560, Title: "Deploy", URL: "https://github.com/partcleda/partcl/pull/2560",
		BaseRefName: "main", HeadRefName: "fix", HeadSHA: "abc123", MergeStateStatus: "DIRTY",
	}}}
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	oldStore := openGitHubPRRepairStore
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister { return lister }
	openGitHubPRRepairStore = func(string, string) (beads.Store, error) { return store, nil }
	attachCalls := stubGitHubRepairWorkflowAttach(t)
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
		openGitHubPRRepairStore = oldStore
	})

	// First pass creates and dispatches.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--city", cityPath, "github", "pr", "backfill", "partcl-main", "--create-repair-beads", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	var payload githubPRBackfillResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if payload.CreatedRepairs != 1 || payload.DispatchedRepairs != 1 {
		t.Fatalf("created=%d dispatched=%d, want 1/1", payload.CreatedRepairs, payload.DispatchedRepairs)
	}
	if len(payload.RepairBeads) != 1 || !payload.RepairBeads[0].Dispatched || payload.RepairBeads[0].Workflow != "mol-polecat-work" {
		t.Fatalf("repair bead = %#v, want dispatched mol-polecat-work", payload.RepairBeads)
	}

	// Second pass over the same PR/head updates, does not re-dispatch.
	var stdout2, stderr2 bytes.Buffer
	if code := run([]string{"--city", cityPath, "github", "pr", "backfill", "partcl-main", "--create-repair-beads", "--json"}, &stdout2, &stderr2); code != 0 {
		t.Fatalf("run 2 code = %d, stderr = %q", code, stderr2.String())
	}
	var payload2 githubPRBackfillResult
	if err := json.Unmarshal(stdout2.Bytes(), &payload2); err != nil {
		t.Fatalf("decode %q: %v", stdout2.String(), err)
	}
	if payload2.CreatedRepairs != 0 || payload2.UpdatedRepairs != 1 || payload2.DispatchedRepairs != 0 {
		t.Fatalf("second pass created=%d updated=%d dispatched=%d, want 0/1/0", payload2.CreatedRepairs, payload2.UpdatedRepairs, payload2.DispatchedRepairs)
	}
	if len(*attachCalls) != 1 {
		t.Fatalf("attach calls = %v, want exactly one across both passes", *attachCalls)
	}
}

func runGitHubBackfillOrFatal(t *testing.T, cityPath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--city", cityPath, "github", "pr", "backfill", "partcl-main", "--create-repair-beads", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestGitHubPRBackfillCommandFiltersCleanResultsByDefault(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister {
		return fakeGitHubPRLister{prs: []githubmonitor.PullRequest{
			{Number: 1, BaseRefName: "main", MergeStateStatus: "CLEAN"},
		}}
	}
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"--city", cityPath, "github", "pr", "backfill", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), `"number":1`) {
		t.Fatalf("stdout = %s, clean PR should be filtered by default", stdout.String())
	}
}

func writeGitHubMonitorTestCity(t *testing.T) string {
	t.Helper()
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatalf("mkdir .gc: %v", err)
	}
	body := `[workspace]
name = "test-city"

[[rigs]]
name = "partcl"
path = "partcl"
prefix = "pa"

[[github.pr_monitor]]
name = "partcl-main"
owner = "partcleda"
repo = "partcl"
base_branches = ["main"]
rig = "partcl"
repair_route = "partcl/polecat"
notify = ["gastown.mayor"]
poll_interval = "2m"
merge_queue = "repair"
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cityPath, "partcl"), 0o755); err != nil {
		t.Fatalf("mkdir partcl: %v", err)
	}
	return cityPath
}

func TestGitHubPRBackfillCommandPropagatesRepairStoreError(t *testing.T) {
	cityPath := writeGitHubMonitorTestCity(t)
	oldToken := resolveGitHubTokenForBackfill
	oldClient := newGitHubPRBackfillClient
	oldStore := openGitHubPRRepairStore
	resolveGitHubTokenForBackfill = func(context.Context) (string, error) { return "token", nil }
	newGitHubPRBackfillClient = func(string) githubPRLister {
		return fakeGitHubPRLister{prs: []githubmonitor.PullRequest{{Number: 1, BaseRefName: "main", HeadSHA: "abc", MergeStateStatus: "DIRTY"}}}
	}
	openGitHubPRRepairStore = func(string, string) (beads.Store, error) {
		return nil, fmt.Errorf("store unavailable")
	}
	t.Cleanup(func() {
		resolveGitHubTokenForBackfill = oldToken
		newGitHubPRBackfillClient = oldClient
		openGitHubPRRepairStore = oldStore
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"--city", cityPath, "github", "pr", "backfill", "--create-repair-beads"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "store unavailable") {
		t.Fatalf("stderr = %q, want store error", stderr.String())
	}
}

// TestDefaultNudgeGitHubPRRepairWorkerSurvivesToDurableRecordAndDedups is the
// github-pr-repair-nudge counterpart to
// TestDeliverSessionNudgeWithWorkerCallerIdentitySurvivesToDurableRecord
// (cmd_nudge_test.go), covering EFFECT-003 for defaultNudgeGitHubPRRepairWorker:
// it shells out to a real `gc session nudge ... --idempotency-key
// github-pr-repair-nudge:<bead.ID>:<FailureKind>` subprocess, and this is the
// only test that proves that key survives through the real CLI dispatch
// (cmdSessionNudge, deliberately NOT unit-tested on its own -- see the note
// in cmd_session_test.go) into the durable nudge record, and that a repeat
// tick with an unchanged FailureKind dedups against it per the EFFECT-003
// doc comment on defaultNudgeGitHubPRRepairWorker.
//
// The subprocess is the current test binary re-executed as "gc" (the same
// installProductMetricsDirectChildSpyCommand PATH-symlink trick
// TestProductMetricsDirectChildEnvGitHubNudge uses), but WITHOUT setting the
// env-spy path: with that var unset, TestMain's isTestscriptCommandInvocation
// check dispatches os.Args[0]=="gc" straight into the real CLI
// (mainExitCode), exactly as testscript.Main does for txtar fixtures.
func TestDefaultNudgeGitHubPRRepairWorkerSurvivesToDurableRecordAndDedups(t *testing.T) {
	installProductMetricsDirectChildSpyCommand(t, "gc")
	t.Setenv("GC_SESSION", "fake")
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_DOLT", "skip")
	t.Setenv("GC_BOOTSTRAP", "skip")

	cityPath := t.TempDir()
	writeCityToml(t, cityPath, "[workspace]\nname = \"demo\"\n\n[providers.claude]\nbase = \"builtin:claude\"\n\n[[agent]]\nname = \"worker\"\nprovider = \"claude\"\n\n[[named_session]]\ntemplate = \"worker\"\nmode = \"on_demand\"\n")

	bead := beads.Bead{ID: "gc-test-1"}
	result := githubmonitor.Result{Owner: "owner", Repo: "repo", Number: 1, FailureKind: "checks_failed"}

	defaultNudgeGitHubPRRepairWorker(cityPath, "worker", bead, result)

	wantKey := "github-pr-repair-nudge:" + bead.ID + ":" + result.FailureKind

	store, err := openNudgeBeadStoreErr(cityPath)
	if err != nil {
		t.Fatalf("openNudgeBeadStoreErr: %v", err)
	}
	if _, ok, err := nudgeFrontDoor(store).Find(wantKey); err != nil {
		t.Fatalf("Find(%q): %v", wantKey, err)
	} else if !ok {
		t.Fatalf("durable nudge record for github-pr-repair key %q not found; the subprocess's --idempotency-key did not survive to the durable record", wantKey)
	}

	pending, inFlight, dead, err := listQueuedNudges(cityPath, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 || len(inFlight) != 0 || len(dead) != 0 {
		t.Fatalf("pending/inFlight/dead = %d/%d/%d, want 1/0/0; stdout/stderr from the subprocess are not captured here, see cityPath %s for on-disk state", len(pending), len(inFlight), len(dead), cityPath)
	}
	if pending[0].ID != wantKey {
		t.Fatalf("pending[0].ID = %q, want %q", pending[0].ID, wantKey)
	}
	firstMessage := pending[0].Message

	// A repeat tick that finds the same repair bead with an UNCHANGED
	// FailureKind (the common case: the monitor polls every tick) must dedup
	// against the durable record instead of enqueueing a second, unidentified
	// nudge.
	defaultNudgeGitHubPRRepairWorker(cityPath, "worker", bead, result)
	pending, inFlight, dead, err = listQueuedNudges(cityPath, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges (after repeat tick, same FailureKind): %v", err)
	}
	if len(pending) != 1 || len(inFlight) != 0 || len(dead) != 0 {
		t.Fatalf("pending/inFlight/dead after repeat tick = %d/%d/%d, want 1/0/0 (dedup by github-pr-repair-nudge key)", len(pending), len(inFlight), len(dead))
	}
	if pending[0].Message != firstMessage {
		t.Fatalf("pending[0].Message after repeat tick = %q, want %q (dedup must not silently overwrite the durable record)", pending[0].Message, firstMessage)
	}

	// A tick where FailureKind CHANGES (per the EFFECT-003 doc comment on
	// defaultNudgeGitHubPRRepairWorker) must get its own durable nudge instead
	// of being dropped by an over-broad key.
	changedResult := result
	changedResult.FailureKind = "merge_conflict"
	defaultNudgeGitHubPRRepairWorker(cityPath, "worker", bead, changedResult)
	pending, inFlight, dead, err = listQueuedNudges(cityPath, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges (after FailureKind change): %v", err)
	}
	if len(pending) != 2 || len(inFlight) != 0 || len(dead) != 0 {
		t.Fatalf("pending/inFlight/dead after FailureKind change = %d/%d/%d, want 2/0/0 (a new FailureKind must not dedup against the old one)", len(pending), len(inFlight), len(dead))
	}
	changedKey := "github-pr-repair-nudge:" + bead.ID + ":" + changedResult.FailureKind
	if _, ok, err := nudgeFrontDoor(store).Find(changedKey); err != nil {
		t.Fatalf("Find(%q): %v", changedKey, err)
	} else if !ok {
		t.Fatalf("durable nudge record for changed-FailureKind key %q not found", changedKey)
	}
}
