package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestValidateBeadsMetadataGuardedClearRequest(t *testing.T) {
	t.Parallel()

	valid := beadsMetadataGuardedClearRequest{
		beadID:           "ga-review_1.2",
		storeRef:         "rig:tributary",
		guardKey:         "gc.worktree_attempt_id",
		guardExpected:    "att-7",
		clearKeys:        []string{"gc.work_dir", "gc.work_branch"},
		setMetadata:      []string{"gc.worktree_lifecycle=removed"},
		format:           "json",
		storeRefSet:      true,
		guardKeySet:      true,
		guardExpectedSet: true,
	}
	if err := validateBeadsMetadataGuardedClearRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	explicitEmptyGuard := valid
	explicitEmptyGuard.guardExpected = ""
	if err := validateBeadsMetadataGuardedClearRequest(explicitEmptyGuard); err != nil {
		t.Fatalf("explicit empty guard-expected rejected: %v", err)
	}

	terminalOnly := valid
	terminalOnly.clearKeys = nil
	if err := validateBeadsMetadataGuardedClearRequest(terminalOnly); err != nil {
		t.Fatalf("terminal-only (no clear-key) request rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*beadsMetadataGuardedClearRequest)
		want   string
	}{
		{"missing store ref flag", func(r *beadsMetadataGuardedClearRequest) { r.storeRefSet = false }, "--store-ref is required"},
		{"missing guard key flag", func(r *beadsMetadataGuardedClearRequest) { r.guardKeySet = false }, "--guard-key is required"},
		{"missing guard expected flag", func(r *beadsMetadataGuardedClearRequest) { r.guardExpectedSet = false }, "--guard-expected is required"},
		{"empty id", func(r *beadsMetadataGuardedClearRequest) { r.beadID = "" }, "invalid bead id"},
		{"unsafe id", func(r *beadsMetadataGuardedClearRequest) { r.beadID = "../other" }, "invalid bead id"},
		{"unsafe guard key", func(r *beadsMetadataGuardedClearRequest) { r.guardKey = "att/id" }, "invalid --guard-key"},
		{"bad scope", func(r *beadsMetadataGuardedClearRequest) { r.storeRef = "all:*" }, "invalid --store-ref"},
		{"invalid utf8 guard expected", func(r *beadsMetadataGuardedClearRequest) { r.guardExpected = string([]byte{0xff}) }, "--guard-expected must be valid UTF-8"},
		{"no clear-key and no set-metadata", func(r *beadsMetadataGuardedClearRequest) {
			r.clearKeys = nil
			r.setMetadata = nil
		}, "at least one --clear-key or --set-metadata is required"},
		{"unsafe clear key", func(r *beadsMetadataGuardedClearRequest) { r.clearKeys = []string{"gc/work_dir"} }, "invalid --clear-key"},
		{"duplicate clear key", func(r *beadsMetadataGuardedClearRequest) {
			r.clearKeys = []string{"gc.work_dir", "gc.work_dir"}
		}, "repeated"},
		{"malformed set-metadata", func(r *beadsMetadataGuardedClearRequest) { r.setMetadata = []string{"no-equals-sign"} }, "invalid --set-metadata"},
		{"unsafe set-metadata key", func(r *beadsMetadataGuardedClearRequest) { r.setMetadata = []string{"bad/key=value"} }, "invalid --set-metadata"},
		{"duplicate set-metadata key", func(r *beadsMetadataGuardedClearRequest) {
			r.setMetadata = []string{"gc.worktree_lifecycle=removed", "gc.worktree_lifecycle=other"}
		}, "repeated"},
		{"bad format", func(r *beadsMetadataGuardedClearRequest) { r.format = "yaml" }, "invalid --format"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := valid
			tc.mutate(&request)
			err := validateBeadsMetadataGuardedClearRequest(request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestResolveBeadsMetadataGuardedClearOutputMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		request    beadsMetadataGuardedClearRequest
		wantFormat string
		wantErr    string
	}{
		{name: "default text", request: beadsMetadataGuardedClearRequest{format: "text"}, wantFormat: "text"},
		{name: "canonical json default", request: beadsMetadataGuardedClearRequest{format: "text", jsonOut: true}, wantFormat: "json"},
		{
			name:    "json conflicts with explicit text",
			request: beadsMetadataGuardedClearRequest{format: "text", formatSet: true, jsonOut: true},
			wantErr: "--json cannot be combined with --format=text",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := tc.request
			err := resolveBeadsMetadataGuardedClearOutputMode(&request)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if request.format != tc.wantFormat {
				t.Fatalf("format = %q, want %q", request.format, tc.wantFormat)
			}
		})
	}
}

func TestParseBeadsMetadataGuardedClearTerminal(t *testing.T) {
	t.Parallel()

	terminal, err := parseBeadsMetadataGuardedClearTerminal([]string{"gc.worktree_lifecycle=removed"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if terminal["gc.worktree_lifecycle"] != "removed" {
		t.Fatalf("terminal=%v", terminal)
	}

	if terminal, err := parseBeadsMetadataGuardedClearTerminal(nil); err != nil || terminal != nil {
		t.Fatalf("empty input: terminal=%v err=%v, want (nil, nil)", terminal, err)
	}
}

// TestBeadsMetadataGuardedClearAppliedClearsKeysAndSetsTerminal proves the
// applied path end to end against a real MemStore: every clear-key is gone,
// the terminal keys are set, and the JSON result reports "cleared".
func TestBeadsMetadataGuardedClearAppliedClearsKeysAndSetsTerminal(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title: "worktree pointer",
		Metadata: beads.StringMap{
			"gc.worktree_attempt_id": "att-7",
			"gc.work_dir":            "/tmp/wt",
			"gc.work_branch":         "fix/foo",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error {
		return nil
	})

	stdout, stderr, code := runGuardedClearTestCommand(
		cityPath, bead.ID,
		"--guard-key=gc.worktree_attempt_id",
		"--guard-expected=att-7",
		"--clear-key=gc.work_dir",
		"--clear-key=gc.work_branch",
		"--set-metadata=gc.worktree_lifecycle=removed",
		"--json",
	)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr=%q, want empty", stderr)
	}
	var result beadsMetadataGuardedClearResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, stdout)
	}
	if result.Outcome != beads.GuardedClearApplied || !result.OK {
		t.Fatalf("result=%+v, want outcome=cleared and ok", result)
	}

	got, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Metadata["gc.work_dir"]; ok {
		t.Fatalf("gc.work_dir still present: %v", got.Metadata)
	}
	if _, ok := got.Metadata["gc.work_branch"]; ok {
		t.Fatalf("gc.work_branch still present: %v", got.Metadata)
	}
	if got.Metadata["gc.worktree_lifecycle"] != "removed" {
		t.Fatalf("gc.worktree_lifecycle=%q, want removed", got.Metadata["gc.worktree_lifecycle"])
	}
	if got.Metadata["gc.worktree_attempt_id"] != "att-7" {
		t.Fatalf("guard key mutated: %v", got.Metadata)
	}
}

// TestBeadsMetadataGuardedClearStaleAttemptIsSkippedNotError proves A1's
// "stale-attempt precondition mismatch is a distinct SKIPPED non-error
// result": a fresh worktree that already overwrote the guard key must be
// left completely untouched, and the CLI must exit 0.
func TestBeadsMetadataGuardedClearStaleAttemptIsSkippedNotError(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title: "worktree pointer",
		Metadata: beads.StringMap{
			"gc.worktree_attempt_id": "att-8", // fresh attempt already overwrote the guard
			"gc.work_dir":            "/tmp/wt-fresh",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error {
		return nil
	})

	stdout, stderr, code := runGuardedClearTestCommand(
		cityPath, bead.ID,
		"--guard-key=gc.worktree_attempt_id",
		"--guard-expected=att-7", // stale: the store now holds att-8
		"--clear-key=gc.work_dir",
		"--json",
	)
	if code != 0 {
		t.Fatalf("code=%d, want 0 (skip is a non-error); stderr=%q stdout=%q", code, stderr, stdout)
	}
	var result beadsMetadataGuardedClearResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, stdout)
	}
	if result.Outcome != beads.GuardedClearSkipped || !result.OK {
		t.Fatalf("result=%+v, want outcome=skipped and ok", result)
	}

	got, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata["gc.work_dir"] != "/tmp/wt-fresh" {
		t.Fatalf("fresh workspace pointer was mutated: %v", got.Metadata)
	}
	if got.Metadata["gc.worktree_attempt_id"] != "att-8" {
		t.Fatalf("guard key was mutated on skip: %v", got.Metadata)
	}
}

// TestBeadsMetadataGuardedClearCapabilityAbsentFailsClosed proves A3: a store
// that cannot implement MetadataGuardedClearer (the BdStore/production shape)
// must fail closed — non-zero exit, no write attempted, nothing cleared.
func TestBeadsMetadataGuardedClearCapabilityAbsentFailsClosed(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	backing := beads.NewMemStore()
	bead, err := backing.Create(beads.Bead{
		Title: "worktree pointer",
		Metadata: beads.StringMap{
			"gc.worktree_attempt_id": "att-7",
			"gc.work_dir":            "/tmp/wt",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store := guardedClearUnsupportedStore{Store: backing}
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error {
		return nil
	})

	stdout, stderr, code := runGuardedClearTestCommand(
		cityPath, bead.ID,
		"--guard-key=gc.worktree_attempt_id",
		"--guard-expected=att-7",
		"--clear-key=gc.work_dir",
		"--json",
	)
	if code == 0 {
		t.Fatalf("code=0, want nonzero (capability-absent must fail closed); stdout=%q", stdout)
	}
	if strings.Contains(stdout, `"outcome"`) || strings.Contains(stdout, `"ok":true`) {
		t.Fatalf("success payload leaked on capability-absent failure: %q", stdout)
	}
	if stderr == "" {
		t.Fatalf("stderr empty, want a diagnostic for capability-absent failure")
	}

	got, err := backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata["gc.work_dir"] != "/tmp/wt" {
		t.Fatalf("workspace pointer was mutated despite capability-absent failure: %v", got.Metadata)
	}
}

func TestBeadsMetadataGuardedClearTextOutput(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title:    "worktree pointer",
		Metadata: beads.StringMap{"gc.worktree_attempt_id": "att-7", "gc.work_dir": "/tmp/wt"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error {
		return nil
	})

	stdout, stderr, code := runGuardedClearTestCommand(
		cityPath, bead.ID,
		"--guard-key=gc.worktree_attempt_id",
		"--guard-expected=att-7",
		"--clear-key=gc.work_dir",
	)
	// Text mode does not suppress advisory config-load warnings (matching
	// "gc beads metadata-cas"'s configWarnWriter behavior), so only the exit
	// code and stdout payload are asserted here.
	if code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "outcome=cleared") || !strings.Contains(stdout, "cleared_keys=gc.work_dir") {
		t.Fatalf("stdout=%q, want text outcome/cleared_keys fields", stdout)
	}
}

func installGuardedClearStoreSeams(
	t *testing.T,
	open func(storePath, cityPath string) (beads.Store, error),
	closeStore func(beads.Store) error,
) {
	t.Helper()
	previousOpen := openBeadsMetadataGuardedClearStore
	previousClose := closeBeadsMetadataGuardedClearStore
	openBeadsMetadataGuardedClearStore = open
	closeBeadsMetadataGuardedClearStore = closeStore
	t.Cleanup(func() {
		openBeadsMetadataGuardedClearStore = previousOpen
		closeBeadsMetadataGuardedClearStore = previousClose
	})
}

func runGuardedClearTestCommand(cityPath, beadID string, extraArgs ...string) (stdout, stderr string, code int) {
	args := []string{
		"--city", cityPath,
		"beads", "metadata-guarded-clear", beadID,
		"--store-ref", "city:demo",
	}
	args = append(args, extraArgs...)
	var stdoutBuffer, stderrBuffer bytes.Buffer
	code = run(args, &stdoutBuffer, &stderrBuffer)
	return stdoutBuffer.String(), stderrBuffer.String(), code
}

// guardedClearUnsupportedStore wraps a Store that has no MetadataGuardedClearer
// implementation, reproducing BdStore's capability-absent shape without
// needing a real bd binary.
type guardedClearUnsupportedStore struct {
	beads.Store
}
