package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestValidateBeadsMetadataGuardedClearRequest(t *testing.T) {
	t.Parallel()

	valid := beadsMetadataGuardedClearRequest{
		beadID:           "ga-worktree_1.2",
		storeRef:         "rig:tributary",
		guardKey:         "gc.worktree_attempt_id",
		guardExpected:    "att-9f2",
		clearKeys:        []string{"gc.work_dir", "gc.work_branch"},
		setMetadata:      []string{"gc.worktree_disposition=removed"},
		format:           "json",
		storeRefSet:      true,
		guardKeySet:      true,
		guardExpectedSet: true,
	}
	if err := validateBeadsMetadataGuardedClearRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	onlyClear := valid
	onlyClear.setMetadata = nil
	if err := validateBeadsMetadataGuardedClearRequest(onlyClear); err != nil {
		t.Fatalf("clear-key-only request rejected: %v", err)
	}
	onlySet := valid
	onlySet.clearKeys = nil
	if err := validateBeadsMetadataGuardedClearRequest(onlySet); err != nil {
		t.Fatalf("set-metadata-only request rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*beadsMetadataGuardedClearRequest)
		want   string
	}{
		{"missing store ref flag", func(r *beadsMetadataGuardedClearRequest) { r.storeRefSet = false }, "--store-ref is required"},
		{"missing guard key flag", func(r *beadsMetadataGuardedClearRequest) { r.guardKeySet = false }, "--guard-key is required"},
		{"missing guard expected flag", func(r *beadsMetadataGuardedClearRequest) { r.guardExpectedSet = false }, "--guard-expected is required"},
		{"no keys at all", func(r *beadsMetadataGuardedClearRequest) {
			r.clearKeys = nil
			r.setMetadata = nil
		}, "at least one --clear-key or --set-metadata is required"},
		{"empty id", func(r *beadsMetadataGuardedClearRequest) { r.beadID = "" }, "invalid bead id"},
		{"unsafe id", func(r *beadsMetadataGuardedClearRequest) { r.beadID = "../other" }, "invalid bead id"},
		{"unsafe guard key", func(r *beadsMetadataGuardedClearRequest) { r.guardKey = "att/id" }, "invalid guard key"},
		{"bad scope", func(r *beadsMetadataGuardedClearRequest) { r.storeRef = "all:*" }, "invalid --store-ref"},
		{"oversized guard expected", func(r *beadsMetadataGuardedClearRequest) {
			r.guardExpected = strings.Repeat("x", metadataCASMaxValueBytes+1)
		}, "--guard-expected exceeds"},
		{"invalid utf8 guard expected", func(r *beadsMetadataGuardedClearRequest) {
			r.guardExpected = string([]byte{0xff})
		}, "--guard-expected must be valid UTF-8"},
		{"unsafe clear key", func(r *beadsMetadataGuardedClearRequest) { r.clearKeys = []string{"a/b"} }, "invalid --clear-key"},
		{"duplicate clear key", func(r *beadsMetadataGuardedClearRequest) {
			r.clearKeys = []string{"gc.work_dir", "gc.work_dir"}
		}, "duplicate --clear-key"},
		{"set-metadata missing equals", func(r *beadsMetadataGuardedClearRequest) {
			r.setMetadata = []string{"gc.worktree_disposition"}
		}, "expected key=value"},
		{"set-metadata unsafe key", func(r *beadsMetadataGuardedClearRequest) {
			r.setMetadata = []string{"a/b=removed"}
		}, "invalid --set-metadata key"},
		{"set-metadata oversized value", func(r *beadsMetadataGuardedClearRequest) {
			r.setMetadata = []string{"gc.worktree_disposition=" + strings.Repeat("x", metadataCASMaxValueBytes+1)}
		}, "value exceeds"},
		{"set-metadata invalid utf8 value", func(r *beadsMetadataGuardedClearRequest) {
			r.setMetadata = []string{"gc.worktree_disposition=" + string([]byte{0xff})}
		}, "value must be valid UTF-8"},
		{"key in both clear and set", func(r *beadsMetadataGuardedClearRequest) {
			r.clearKeys = []string{"gc.work_dir"}
			r.setMetadata = []string{"gc.work_dir=removed"}
		}, "supplied by both"},
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
		{
			name:       "canonical json default",
			request:    beadsMetadataGuardedClearRequest{format: "text", jsonOut: true},
			wantFormat: "json",
		},
		{
			name:    "canonical conflicts with explicit text",
			request: beadsMetadataGuardedClearRequest{format: "text", formatSet: true, jsonOut: true},
			wantErr: "--json cannot be combined with --format=text",
		},
		{
			name:    "canonical does not hide invalid format",
			request: beadsMetadataGuardedClearRequest{format: "yaml", formatSet: true, jsonOut: true},
			wantErr: "invalid --format",
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
				t.Fatalf("resolve output mode: %v", err)
			}
			if request.format != tc.wantFormat {
				t.Fatalf("format = %q, want %q", request.format, tc.wantFormat)
			}
		})
	}
}

func TestBeadsMetadataGuardedClearCanonicalJSONOutcomes(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)

	t.Run("cleared", func(t *testing.T) {
		store, id := newGuardedClearTestMemStore(t)
		installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
			return store, nil
		}, func(beads.Store) error { return nil })

		stdout, stderr, code := runGuardedClearTestCommand(cityPath, id,
			"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1",
			"--clear-key=gc.work_dir", "--clear-key=gc.work_branch",
			"--set-metadata=gc.worktree_disposition=removed",
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
		if !result.OK || result.Outcome != beadsMetadataGuardedClearCleared {
			t.Fatalf("result=%+v, want ok+cleared", result)
		}
		want := []string{"gc.work_branch", "gc.work_dir", "gc.worktree_disposition"}
		sort.Strings(result.KeysWritten)
		if strings.Join(result.KeysWritten, ",") != strings.Join(want, ",") {
			t.Fatalf("keys_written=%v, want %v", result.KeysWritten, want)
		}

		got, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get after clear: %v", err)
		}
		if got.Metadata["gc.work_dir"] != "" || got.Metadata["gc.work_branch"] != "" {
			t.Fatalf("clear keys not cleared: metadata=%v", got.Metadata)
		}
		if got.Metadata["gc.worktree_disposition"] != "removed" {
			t.Fatalf("set-metadata not applied: metadata=%v", got.Metadata)
		}
		if got.Metadata["gc.worktree_attempt_id"] != "att-1" {
			t.Fatalf("guard key unexpectedly mutated: metadata=%v", got.Metadata)
		}
	})

	t.Run("skipped on initial guard mismatch", func(t *testing.T) {
		store, id := newGuardedClearTestMemStore(t)
		installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
			return store, nil
		}, func(beads.Store) error { return nil })

		stdout, stderr, code := runGuardedClearTestCommand(cityPath, id,
			"--guard-key=gc.worktree_attempt_id", "--guard-expected=stale-attempt",
			"--clear-key=gc.work_dir",
			"--json",
		)
		if code != 0 {
			t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
		}
		var result beadsMetadataGuardedClearResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("Unmarshal: %v\n%s", err, stdout)
		}
		if !result.OK || result.Outcome != beadsMetadataGuardedClearSkipped {
			t.Fatalf("result=%+v, want ok+skipped", result)
		}
		if len(result.KeysWritten) != 0 {
			t.Fatalf("keys_written=%v, want none", result.KeysWritten)
		}

		got, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get after skip: %v", err)
		}
		if got.Metadata["gc.work_dir"] != "/work/wt" {
			t.Fatalf("clear key mutated despite guard mismatch: metadata=%v", got.Metadata)
		}
	})
}

// TestBeadsMetadataGuardedClearGuardChangesMidSequence exercises the guard
// re-check between individual writes: a wrapper store rewrites the guard key
// out-of-band right before the second write's self-CAS check, so the
// sequence must stop with the first key already written and report
// "skipped", not partially fail.
func TestBeadsMetadataGuardedClearGuardChangesMidSequence(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	backing, id := newGuardedClearTestMemStore(t)
	store := &guardedClearRaceStore{MemStore: backing, id: id, guardKey: "gc.worktree_attempt_id"}
	store.reprovisionGuardOnNthGuardCheck = 2
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error { return nil })

	stdout, stderr, code := runGuardedClearTestCommand(cityPath, id,
		"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1",
		"--clear-key=gc.work_dir", "--clear-key=gc.work_branch",
		"--json",
	)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var result beadsMetadataGuardedClearResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, stdout)
	}
	if !result.OK || result.Outcome != beadsMetadataGuardedClearSkipped {
		t.Fatalf("result=%+v, want ok+skipped", result)
	}
	if strings.Join(result.KeysWritten, ",") != "gc.work_dir" {
		t.Fatalf("keys_written=%v, want only the key written before the guard changed", result.KeysWritten)
	}

	got, err := backing.Get(id)
	if err != nil {
		t.Fatalf("Get after mid-sequence skip: %v", err)
	}
	if got.Metadata["gc.work_dir"] != "" {
		t.Fatalf("first key not cleared: metadata=%v", got.Metadata)
	}
	if got.Metadata["gc.work_branch"] != "/work/branch" {
		t.Fatalf("second key cleared despite guard change: metadata=%v", got.Metadata)
	}
}

// TestBeadsMetadataGuardedClearKeyConflictIsHardFailure exercises a genuine
// write race on a clear-key itself (not the guard): a wrapper store rewrites
// the key's own value out-of-band right before the command's CAS on that key,
// so the CAS observes a live mismatch. This must surface as a non-zero-exit
// failure, never as "skipped".
func TestBeadsMetadataGuardedClearKeyConflictIsHardFailure(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	backing, id := newGuardedClearTestMemStore(t)
	store := &guardedClearRaceStore{MemStore: backing, id: id, tamperKey: "gc.work_dir", tamperValue: "raced-value"}
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error { return nil })

	stdout, stderr, code := runGuardedClearTestCommand(cityPath, id,
		"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1",
		"--clear-key=gc.work_dir",
		"--json",
	)
	if code == 0 {
		t.Fatalf("code=0, want nonzero; stdout=%q", stdout)
	}
	assertGuardedClearSharedFailureJSON(t, stdout)
	if !strings.Contains(stderr, "gc.work_dir") {
		t.Fatalf("stderr=%q, want it to name the raced key", stderr)
	}
}

func TestBeadsMetadataGuardedClearCanonicalJSONFailuresUseSharedContract(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	failureSchemaStdout, failureSchemaStderr := bytes.Buffer{}, bytes.Buffer{}
	if code := run([]string{"beads", "metadata-guarded-clear", "--json-schema=failure"}, &failureSchemaStdout, &failureSchemaStderr); code != 0 {
		t.Fatalf("failure schema code=%d stderr=%q stdout=%q", code, failureSchemaStderr.String(), failureSchemaStdout.String())
	}
	failureSchema := compileJSONSchema(t, "gc://schemas/failure.schema.json", failureSchemaStdout.Bytes())

	tests := []struct {
		name       string
		store      func(t *testing.T) (beads.Store, string)
		closeStore func(beads.Store) error
		extraArgs  []string
	}{
		{
			name: "validation",
			store: func(t *testing.T) (beads.Store, string) {
				t.Helper()
				return beads.NewMemStore(), "gc-1"
			},
			extraArgs: []string{"--json"},
		},
		{
			name: "unsupported capability",
			store: func(t *testing.T) (beads.Store, string) {
				t.Helper()
				backing, id := newGuardedClearTestMemStore(t)
				return guardedClearUnsupportedCommandStore{Store: backing}, id
			},
		},
		{
			name: "transport",
			store: func(t *testing.T) (beads.Store, string) {
				t.Helper()
				backing, id := newGuardedClearTestMemStore(t)
				return &guardedClearCommandFailureStore{Store: backing, casErr: errors.New("guarded clear transport failed")}, id
			},
		},
		{
			name: "readback",
			store: func(t *testing.T) (beads.Store, string) {
				t.Helper()
				backing, id := newGuardedClearTestMemStore(t)
				return &guardedClearCommandFailureStore{Store: backing, getErr: errors.New("guarded clear readback failed")}, id
			},
		},
		{
			name: "close",
			store: func(t *testing.T) (beads.Store, string) {
				t.Helper()
				return newGuardedClearTestMemStore(t)
			},
			closeStore: func(beads.Store) error { return errors.New("guarded clear close failed") },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, id := tc.store(t)
			closeStore := tc.closeStore
			if closeStore == nil {
				closeStore = func(beads.Store) error { return nil }
			}
			installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
				return store, nil
			}, closeStore)

			extraArgs := tc.extraArgs
			if extraArgs == nil {
				extraArgs = []string{"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1", "--clear-key=gc.work_dir", "--json"}
			}
			stdout, stderr, code := runGuardedClearTestCommand(cityPath, id, extraArgs...)
			if code == 0 {
				t.Fatalf("code=0, want nonzero; stderr=%q stdout=%q", stderr, stdout)
			}
			assertGuardedClearSharedFailureJSON(t, stdout)
			var payload any
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("failure stdout is not JSON: %v\n%s", err, stdout)
			}
			if err := failureSchema.Validate(payload); err != nil {
				t.Fatalf("failure payload does not match shared schema: %v\npayload=%s", err, stdout)
			}
		})
	}
}

func TestBeadsMetadataGuardedClearFormatCompatibility(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityPath := writeMetadataCASTestCity(t)
	store, id := newGuardedClearTestMemStore(t)
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error { return nil })

	stdout, stderr, code := runGuardedClearTestCommand(cityPath, id,
		"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1", "--clear-key=gc.work_dir", "--format=json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("--format=json code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var compatibility beadsMetadataGuardedClearResult
	if err := json.Unmarshal([]byte(stdout), &compatibility); err != nil || !compatibility.OK {
		t.Fatalf("--format=json payload=%q error=%v", stdout, err)
	}

	store, id = newGuardedClearTestMemStore(t)
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error { return nil })
	stdout, stderr, code = runGuardedClearTestCommand(cityPath, id,
		"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1", "--clear-key=gc.work_dir", "--json", "--format=text",
	)
	if code == 0 {
		t.Fatalf("--json --format=text code=0; stderr=%q stdout=%q", stderr, stdout)
	}
	assertGuardedClearSharedFailureJSON(t, stdout)
	if !strings.Contains(stderr, "--json cannot be combined with --format=text") {
		t.Fatalf("stderr=%q, want format conflict diagnostic", stderr)
	}
}

func TestBeadsMetadataGuardedClearManifestAndRuntimePayloadsAreCoherent(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	var manifestStdout, manifestStderr bytes.Buffer
	code := run([]string{"beads", "metadata-guarded-clear", "--json-schema"}, &manifestStdout, &manifestStderr)
	if code != 0 {
		t.Fatalf("manifest code=%d stderr=%q stdout=%q", code, manifestStderr.String(), manifestStdout.String())
	}
	var manifest jsonSchemaManifest
	if err := json.Unmarshal(manifestStdout.Bytes(), &manifest); err != nil {
		t.Fatalf("Unmarshal manifest: %v\n%s", err, manifestStdout.String())
	}
	if !manifest.JSONSupported {
		t.Fatalf("manifest does not declare JSON support: %+v", manifest)
	}
	if got := strings.Join(manifest.Command, " "); got != "beads metadata-guarded-clear" {
		t.Fatalf("manifest command=%q, want %q", got, "beads metadata-guarded-clear")
	}
	resultRaw := manifest.Schemas[jsonSchemaResultRole]
	failureRaw := manifest.Schemas[jsonSchemaFailureRole]
	if len(resultRaw) == 0 || len(failureRaw) == 0 {
		t.Fatalf("manifest schemas=%v, want result and failure", manifest.Schemas)
	}
	resultSchema := compileJSONSchema(t, "gc://schemas/beads/metadata-guarded-clear/result.schema.json", resultRaw)
	failureSchema := compileJSONSchema(t, "gc://schemas/failure.schema.json", failureRaw)

	cityPath := writeMetadataCASTestCity(t)
	store, id := newGuardedClearTestMemStore(t)
	installGuardedClearStoreSeams(t, func(_, _ string) (beads.Store, error) {
		return store, nil
	}, func(beads.Store) error { return nil })

	success, successStderr, successCode := runGuardedClearTestCommand(cityPath, id,
		"--guard-key=gc.worktree_attempt_id", "--guard-expected=att-1", "--clear-key=gc.work_dir", "--json",
	)
	if successCode != 0 || successStderr != "" {
		t.Fatalf("success code=%d stderr=%q stdout=%q", successCode, successStderr, success)
	}
	var successPayload any
	if err := json.Unmarshal([]byte(success), &successPayload); err != nil {
		t.Fatalf("Unmarshal success: %v\n%s", err, success)
	}
	if err := resultSchema.Validate(successPayload); err != nil {
		t.Fatalf("success payload does not match manifest result schema: %v\npayload=%s", err, success)
	}

	failure, _, failureCode := runGuardedClearTestCommand(cityPath, id, "--json")
	if failureCode == 0 {
		t.Fatalf("validation failure code=0; stdout=%q", failure)
	}
	var failurePayload any
	if err := json.Unmarshal([]byte(failure), &failurePayload); err != nil {
		t.Fatalf("Unmarshal failure: %v\n%s", err, failure)
	}
	if err := failureSchema.Validate(failurePayload); err != nil {
		t.Fatalf("failure payload does not match manifest failure schema: %v\npayload=%s", err, failure)
	}
}

func newGuardedClearTestMemStore(t *testing.T) (*beads.MemStore, string) {
	t.Helper()
	store := beads.NewMemStore()
	bead, err := store.Create(beads.Bead{
		Title: "worktree teardown fixture",
		Metadata: beads.StringMap{
			"gc.worktree_attempt_id": "att-1",
			"gc.work_dir":            "/work/wt",
			"gc.work_branch":         "/work/branch",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, bead.ID
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

func assertGuardedClearSharedFailureJSON(t *testing.T, stdout string) {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout is not exactly one JSON line: %q", stdout)
	}
	var payload jsonSchemaErrorPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal shared failure: %v\n%s", err, stdout)
	}
	if payload.OK || payload.SchemaVersion != "1" ||
		payload.Error.Code != "command_failed" || payload.Error.ExitCode == 0 {
		t.Fatalf("shared failure payload=%+v", payload)
	}
}

type guardedClearUnsupportedCommandStore struct {
	beads.Store
}

type guardedClearCommandFailureStore struct {
	beads.Store
	casErr error
	getErr error
}

func (s *guardedClearCommandFailureStore) CompareAndSetMetadataKey(_, _, _, _ string) (bool, error) {
	return false, s.casErr
}

func (s *guardedClearCommandFailureStore) Get(id string) (beads.Bead, error) {
	if s.getErr != nil {
		return beads.Bead{}, s.getErr
	}
	return s.Store.Get(id)
}

// guardedClearRaceStore simulates a concurrent write landing between the
// command's snapshot read and one of its per-key CAS calls. Exactly one of
// its two race modes is armed per test: reprovisionGuardOnNthGuardCheck
// rewrites the guard key before the Nth guard self-CAS; tamperKey/tamperValue
// rewrites a single named key before its own CAS call.
type guardedClearRaceStore struct {
	*beads.MemStore
	id       string
	guardKey string

	reprovisionGuardOnNthGuardCheck int
	guardCheckCount                 int

	tamperKey     string
	tamperValue   string
	tamperApplied bool
}

func (s *guardedClearRaceStore) CompareAndSetMetadataKey(id, key, expected, next string) (bool, error) {
	if s.reprovisionGuardOnNthGuardCheck > 0 && key == s.guardKey && expected == next {
		s.guardCheckCount++
		if s.guardCheckCount == s.reprovisionGuardOnNthGuardCheck {
			bead, err := s.Get(id)
			if err != nil {
				return false, err
			}
			if _, err := s.MemStore.CompareAndSetMetadataKey(id, s.guardKey, bead.Metadata[s.guardKey], "att-reprovisioned"); err != nil {
				return false, err
			}
		}
	}
	if s.tamperKey != "" && key == s.tamperKey && !s.tamperApplied {
		s.tamperApplied = true
		bead, err := s.Get(id)
		if err != nil {
			return false, err
		}
		if _, err := s.MemStore.CompareAndSetMetadataKey(id, s.tamperKey, bead.Metadata[s.tamperKey], s.tamperValue); err != nil {
			return false, err
		}
	}
	return s.MemStore.CompareAndSetMetadataKey(id, key, expected, next)
}
