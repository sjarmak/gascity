package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestBeadsCloseExactCLIClosesOnlyExplicitFileStore(t *testing.T) {
	clearGCEnv(t)
	cityPath, rigPath := writeMetadataCASScopedFileCity(t)
	cityStore, err := openScopeLocalFileStore(cityPath)
	if err != nil {
		t.Fatalf("open city store: %v", err)
	}
	rigStore, err := openScopeLocalFileStore(rigPath)
	if err != nil {
		t.Fatalf("open rig store: %v", err)
	}
	fixture := beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	}
	cityBead, err := cityStore.Create(fixture)
	if err != nil {
		t.Fatalf("create city bead: %v", err)
	}
	rigBead, err := rigStore.Create(fixture)
	if err != nil {
		t.Fatalf("create rig bead: %v", err)
	}
	if cityBead.ID != rigBead.ID {
		t.Fatalf("fixture ids city=%q rig=%q, want duplicate exact id", cityBead.ID, rigBead.ID)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--city", cityPath,
		"beads", "close-exact", cityBead.ID,
		"--store-ref=city:demo",
		"--surface=file",
		"--expected-title=dr-wisp-1",
		"--expected-status=open",
		"--expected-metadata-key=gc.routed_to",
		"--expected-metadata-value=city/receiver",
		"--json",
	}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var result beadsCloseExactResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, stdout.String())
	}
	if result.Outcome != beadsCloseExactClosed || result.StoreRef != "city:demo" {
		t.Fatalf("result=%+v", result)
	}

	cityCheck, err := openScopeLocalFileStore(cityPath)
	if err != nil {
		t.Fatalf("reopen city store: %v", err)
	}
	rigCheck, err := openScopeLocalFileStore(rigPath)
	if err != nil {
		t.Fatalf("reopen rig store: %v", err)
	}
	closed, err := cityCheck.Get(cityBead.ID)
	if err != nil {
		t.Fatalf("get city bead: %v", err)
	}
	untouched, err := rigCheck.Get(rigBead.ID)
	if err != nil {
		t.Fatalf("get rig bead: %v", err)
	}
	if closed.Status != "closed" || untouched.Status != "open" {
		t.Fatalf("statuses city=%q rig=%q, want closed/open", closed.Status, untouched.Status)
	}
}

func TestBeadsCloseExactCLIRefusesRigWithoutExactFileStore(t *testing.T) {
	clearGCEnv(t)
	cityPath := writeMetadataCASTestCity(t)
	rigPath := filepath.Join(cityPath, "tributary")
	if err := ensurePersistedScopeLocalFileStore(cityPath); err != nil {
		t.Fatalf("ensure city file store: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatalf("make rig beads dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(rigPath, ".beads", "metadata.json"),
		[]byte(`{"backend":"dolt","dolt_database":"tributary"}`),
		0o644,
	); err != nil {
		t.Fatalf("mark rig bd-backed: %v", err)
	}
	cityStore, err := openScopeLocalFileStore(cityPath)
	if err != nil {
		t.Fatalf("open city store: %v", err)
	}
	target, err := cityStore.Create(beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	})
	if err != nil {
		t.Fatalf("create city bead: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--city", cityPath,
		"beads", "close-exact", target.ID,
		"--store-ref=rig:tributary",
		"--surface=file",
		"--expected-title=dr-wisp-1",
		"--expected-status=open",
		"--expected-metadata-key=gc.routed_to",
		"--expected-metadata-value=city/receiver",
		"--json",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("command unexpectedly succeeded: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	cityCheck, err := openScopeLocalFileStore(cityPath)
	if err != nil {
		t.Fatalf("reopen city store: %v", err)
	}
	untouched, err := cityCheck.Get(target.ID)
	if err != nil {
		t.Fatalf("get city bead: %v", err)
	}
	if untouched.Status != "open" {
		t.Fatalf("city bead status = %q, want open", untouched.Status)
	}
}

func TestValidateBeadsCloseExactRequestRequiresEveryIdentityFence(t *testing.T) {
	t.Parallel()
	valid := beadsCloseExactRequest{
		beadID:                   "gc-1",
		storeRef:                 "city:demo",
		surface:                  "file",
		expectedTitle:            "dr-wisp-1",
		expectedStatus:           "open",
		expectedMetadataKey:      "gc.routed_to",
		expectedMetadataValue:    "city/receiver",
		format:                   "json",
		storeRefSet:              true,
		surfaceSet:               true,
		expectedTitleSet:         true,
		expectedStatusSet:        true,
		expectedMetadataKeySet:   true,
		expectedMetadataValueSet: true,
	}
	if err := validateBeadsCloseExactRequest(valid); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	tests := []struct {
		name string
		edit func(*beadsCloseExactRequest)
		want string
	}{
		{"store ref", func(r *beadsCloseExactRequest) { r.storeRefSet = false }, "--store-ref is required"},
		{"surface", func(r *beadsCloseExactRequest) { r.surfaceSet = false }, "--surface is required"},
		{"title", func(r *beadsCloseExactRequest) { r.expectedTitleSet = false }, "--expected-title is required"},
		{"status", func(r *beadsCloseExactRequest) { r.expectedStatusSet = false }, "--expected-status is required"},
		{"metadata key", func(r *beadsCloseExactRequest) { r.expectedMetadataKeySet = false }, "--expected-metadata-key is required"},
		{"metadata value", func(r *beadsCloseExactRequest) { r.expectedMetadataValueSet = false }, "--expected-metadata-value is required"},
		{"unsafe id", func(r *beadsCloseExactRequest) { r.beadID = "../gc-1" }, "invalid bead id"},
		{"bad status", func(r *beadsCloseExactRequest) { r.expectedStatus = "closed" }, "must be open or in_progress"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := valid
			tc.edit(&request)
			err := validateBeadsCloseExactRequest(request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestApplyBeadsCloseExactClosesOnlyIdentityMatchedBead(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	other, err := store.Create(beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	result, err := applyBeadsCloseExact(store, beadsCloseExactRequest{
		beadID:                target.ID,
		storeRef:              "city:demo",
		surface:               "file",
		expectedTitle:         "dr-wisp-1",
		expectedStatus:        "open",
		expectedMetadataKey:   "gc.routed_to",
		expectedMetadataValue: "city/receiver",
	})
	if err != nil {
		t.Fatalf("apply exact close: %v", err)
	}
	if result.Outcome != beadsCloseExactClosed {
		t.Fatalf("outcome = %q, want %q", result.Outcome, beadsCloseExactClosed)
	}
	closed, err := store.Get(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	untouched, err := store.Get(other.ID)
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if closed.Status != "closed" || untouched.Status != "open" {
		t.Fatalf("statuses target=%q other=%q, want closed/open", closed.Status, untouched.Status)
	}
}

func TestApplyBeadsCloseExactRefusesIdentityMismatchWithoutMutation(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{
		Title:    "operator work",
		Metadata: beads.StringMap{"gc.routed_to": "city/worker"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	_, err = applyBeadsCloseExact(store, beadsCloseExactRequest{
		beadID:                target.ID,
		storeRef:              "city:demo",
		surface:               "file",
		expectedTitle:         "dr-wisp-1",
		expectedStatus:        "open",
		expectedMetadataKey:   "gc.routed_to",
		expectedMetadataValue: "city/receiver",
	})
	if err == nil || !strings.Contains(err.Error(), "title precondition") {
		t.Fatalf("error = %v, want title precondition failure", err)
	}
	after, err := store.Get(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if after.Status != "open" {
		t.Fatalf("mismatched bead status = %q, want open", after.Status)
	}
}

func TestApplyBeadsCloseExactRequiresMetadataKeyPresence(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{Title: "dr-wisp-1"})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	_, err = applyBeadsCloseExact(store, beadsCloseExactRequest{
		beadID:                target.ID,
		storeRef:              "city:demo",
		surface:               "file",
		expectedTitle:         "dr-wisp-1",
		expectedStatus:        "open",
		expectedMetadataKey:   "gc.routed_to",
		expectedMetadataValue: "",
	})
	if err == nil || !strings.Contains(err.Error(), "metadata precondition") {
		t.Fatalf("error = %v, want metadata precondition failure", err)
	}
	after, err := store.Get(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if after.Status != "open" {
		t.Fatalf("bead status = %q, want open", after.Status)
	}
}

type racingCloseStore struct {
	beads.Store
	backing *beads.MemStore
}

func (s *racingCloseStore) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	return s.backing.UpdateIfMatch(id, revision, opts)
}

func (s *racingCloseStore) CloseIfMatch(id string, revision int64) error {
	title := "claimed concurrently"
	if err := s.backing.Update(id, beads.UpdateOpts{Title: &title}); err != nil {
		return err
	}
	return s.backing.CloseIfMatch(id, revision)
}

func (s *racingCloseStore) DeleteIfMatch(id string, revision int64) error {
	return s.backing.DeleteIfMatch(id, revision)
}

func (s *racingCloseStore) CompareAndSetMetadataKey(id, key, expected, next string) (bool, error) {
	return s.backing.CompareAndSetMetadataKey(id, key, expected, next)
}

func TestApplyBeadsCloseExactRevisionFenceRefusesConcurrentChange(t *testing.T) {
	t.Parallel()
	backing := beads.NewMemStore()
	target, err := backing.Create(beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	store := &racingCloseStore{Store: backing, backing: backing}

	_, err = applyBeadsCloseExact(store, beadsCloseExactRequest{
		beadID:                target.ID,
		storeRef:              "city:demo",
		surface:               "file",
		expectedTitle:         "dr-wisp-1",
		expectedStatus:        "open",
		expectedMetadataKey:   "gc.routed_to",
		expectedMetadataValue: "city/receiver",
	})
	if !beads.IsPreconditionFailed(err) {
		t.Fatalf("error = %v, want revision precondition failure", err)
	}
	after, err := backing.Get(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if after.Status != "open" {
		t.Fatalf("raced bead status = %q, want open", after.Status)
	}
}

func TestApplyBeadsCloseExactIsIdempotentAfterVerifiedClose(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	target, err := store.Create(beads.Bead{
		Title:    "dr-wisp-1",
		Metadata: beads.StringMap{"gc.routed_to": "city/receiver"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := store.Close(target.ID); err != nil {
		t.Fatalf("close target: %v", err)
	}

	result, err := applyBeadsCloseExact(store, beadsCloseExactRequest{
		beadID:                target.ID,
		storeRef:              "city:demo",
		surface:               "file",
		expectedTitle:         "dr-wisp-1",
		expectedStatus:        "open",
		expectedMetadataKey:   "gc.routed_to",
		expectedMetadataValue: "city/receiver",
	})
	if err != nil {
		t.Fatalf("idempotent exact close: %v", err)
	}
	if result.Outcome != beadsCloseExactAlreadyClosed {
		t.Fatalf("outcome = %q, want %q", result.Outcome, beadsCloseExactAlreadyClosed)
	}
}
