package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"sync"
	"testing"
)

// countingBackingStore wraps a Store and counts SetMetadata /
// SetMetadataBatch / Update / Close invocations so tests can assert when
// CachingStore short-circuits a no-op write before the backing call.
type countingBackingStore struct {
	Store
	setMetadataCalls      int
	setMetadataBatchCalls int
	updateCalls           int
	closeCalls            int
	releaseIfCurrentCalls int
}

func (c *countingBackingStore) SetMetadata(id, key, value string) error {
	c.setMetadataCalls++
	return c.Store.SetMetadata(id, key, value)
}

func (c *countingBackingStore) SetMetadataBatch(id string, kvs map[string]string) error {
	c.setMetadataBatchCalls++
	return c.Store.SetMetadataBatch(id, kvs)
}

func (c *countingBackingStore) Update(id string, opts UpdateOpts) error {
	c.updateCalls++
	return c.Store.Update(id, opts)
}

func (c *countingBackingStore) Close(id string) error {
	c.closeCalls++
	return c.Store.Close(id)
}

func (c *countingBackingStore) ReleaseIfCurrent(id, expectedAssignee string) (bool, error) {
	c.releaseIfCurrentCalls++
	releaser, ok := c.Store.(ConditionalAssignmentReleaser)
	if !ok {
		return false, ErrConditionalReleaseUnsupported
	}
	return releaser.ReleaseIfCurrent(id, expectedAssignee)
}

type txPreservingBackingStore struct {
	Store
	txCalls     int
	updateCalls int
}

type cacheWriteNotification struct {
	eventType string
	beadID    string
	payload   json.RawMessage
}

type releaseRefreshFailOnceStore struct {
	Store
	failNextGet bool
}

type ambiguousMetadataBatchStore struct {
	Store
	err        error
	commitKeys []string
}

func (s *ambiguousMetadataBatchStore) SetMetadataBatch(id string, kvs map[string]string) error {
	committed := make(map[string]string, len(s.commitKeys))
	for _, key := range s.commitKeys {
		committed[key] = kvs[key]
	}
	if len(committed) > 0 {
		if err := s.Store.SetMetadataBatch(id, committed); err != nil {
			return err
		}
	}
	return s.err
}

func (s *releaseRefreshFailOnceStore) Get(id string) (Bead, error) {
	if s.failNextGet {
		s.failNextGet = false
		return Bead{}, errors.New("injected refresh failure")
	}
	return s.Store.Get(id)
}

func (s *releaseRefreshFailOnceStore) ReleaseIfCurrent(id, expectedAssignee string) (bool, error) {
	releaser, ok := s.Store.(ConditionalAssignmentReleaser)
	if !ok {
		return false, ErrConditionalReleaseUnsupported
	}
	released, err := releaser.ReleaseIfCurrent(id, expectedAssignee)
	if released && err == nil {
		s.failNextGet = true
	}
	return released, err
}

func (s *txPreservingBackingStore) Update(id string, opts UpdateOpts) error {
	s.updateCalls++
	if err := s.Store.Update(id, opts); err != nil {
		return err
	}
	if opts.Title == nil {
		clobbered := ""
		return s.Store.Update(id, UpdateOpts{Title: &clobbered})
	}
	return nil
}

func (s *txPreservingBackingStore) Tx(commitMsg string, fn func(Tx) error) error {
	s.txCalls++
	return s.Store.Tx(commitMsg, fn)
}

func TestCachingStoreTxDelegatesToBackingTxAndRefreshesCache(t *testing.T) {
	t.Parallel()

	backing := &txPreservingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{
		Title:       "preserve title",
		Description: "before",
		Labels:      []string{"keep-label", "drop-label"},
		Metadata:    map[string]string{"existing": "yes"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	description := "after"
	if err := cache.Tx("preserve backing semantics", func(tx Tx) error {
		if err := tx.Update(bead.ID, UpdateOpts{
			Description:  &description,
			Labels:       []string{"new-label"},
			RemoveLabels: []string{"drop-label"},
		}); err != nil {
			return err
		}
		if err := tx.SetMetadataBatch(bead.ID, map[string]string{"tx": "applied"}); err != nil {
			return err
		}
		return tx.Close(bead.ID)
	}); err != nil {
		t.Fatalf("Tx: %v", err)
	}

	if backing.txCalls != 1 {
		t.Fatalf("backing.Tx calls = %d, want 1", backing.txCalls)
	}
	if backing.updateCalls != 0 {
		t.Fatalf("backing.Update calls = %d, want 0 direct calls through CachingStore", backing.updateCalls)
	}

	got, err := backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("backing Get: %v", err)
	}
	assertTxPreservedBead(t, got)

	cached, err := cache.Get(bead.ID)
	if err != nil {
		t.Fatalf("cache Get: %v", err)
	}
	assertTxPreservedBead(t, cached)
}

func TestCachingStoreTxCloseClearsDependentProjectedIsBlocked(t *testing.T) {
	t.Parallel()

	blockedProjection := true
	backing := NewMemStore()
	blocker, err := backing.Create(Bead{
		Title:  "blocker",
		Status: "open",
		Type:   "task",
	})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	blocked, err := backing.Create(Bead{
		Title:     "blocked",
		Status:    "open",
		Type:      "task",
		Needs:     []string{blocker.ID},
		IsBlocked: &blockedProjection,
	})
	if err != nil {
		t.Fatalf("Create blocked: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	if err := cache.Tx("close blocker", func(tx Tx) error {
		return tx.Close(blocker.ID)
	}); err != nil {
		t.Fatalf("Tx close blocker: %v", err)
	}

	ready, ok := cache.CachedReady()
	if !ok {
		t.Fatal("CachedReady reported cache unavailable after tx close")
	}
	readyByID := make(map[string]bool, len(ready))
	for _, bead := range ready {
		readyByID[bead.ID] = true
	}
	if !readyByID[blocked.ID] {
		t.Fatalf("CachedReady after tx close ids = %v, want dependent unblocked by closed blocker", readyByID)
	}

	got, err := cache.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get blocked after tx close: %v", err)
	}
	if got.IsBlocked != nil {
		t.Fatalf("dependent IsBlocked after tx close = %v, want nil fallback to cached deps", got.IsBlocked)
	}
}

func assertTxPreservedBead(t *testing.T, got Bead) {
	t.Helper()
	if got.Title != "preserve title" {
		t.Fatalf("Title = %q, want preserved title", got.Title)
	}
	if got.Description != "after" {
		t.Fatalf("Description = %q, want after", got.Description)
	}
	if got.Status != "closed" {
		t.Fatalf("Status = %q, want closed", got.Status)
	}
	if got.Metadata["existing"] != "yes" || got.Metadata["tx"] != "applied" {
		t.Fatalf("Metadata = %#v, want existing=yes and tx=applied", got.Metadata)
	}
	if !stringSliceContains(got.Labels, "keep-label") || !stringSliceContains(got.Labels, "new-label") || stringSliceContains(got.Labels, "drop-label") {
		t.Fatalf("Labels = %#v, want keep-label and new-label without drop-label", got.Labels)
	}
}

func TestCachingStoreSetMetadataBatchNotifiesBeadUpdated(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	bead, err := backing.Create(Bead{Title: "metadata"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var notifications []cacheWriteNotification
	cache := NewCachingStoreForTest(backing, func(eventType, beadID string, payload json.RawMessage) {
		notifications = append(notifications, cacheWriteNotification{eventType: eventType, beadID: beadID, payload: payload})
	})
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	if err := cache.SetMetadataBatch(bead.ID, map[string]string{"review": "fixed"}); err != nil {
		t.Fatalf("SetMetadataBatch: %v", err)
	}

	if len(notifications) != 1 {
		t.Fatalf("notifications = %d, want 1: %#v", len(notifications), notifications)
	}
	if notifications[0].eventType != "bead.updated" || notifications[0].beadID != bead.ID {
		t.Fatalf("notification = %#v, want bead.updated for %s", notifications[0], bead.ID)
	}
	updated, _, err := decodeCacheEvent(notifications[0].payload)
	if err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if updated.Metadata["review"] != "fixed" {
		t.Fatalf("notification metadata = %#v, want review=fixed", updated.Metadata)
	}
}

func TestCachingStoreSetMetadataBatchErrorFencesStaleRow(t *testing.T) {
	patch := map[string]string{"state": "asleep", "reason": "healed"}
	for _, tc := range []struct {
		name       string
		commitKeys []string
		wantState  string
		wantReason string
	}{
		{name: "rejected", wantState: "active", wantReason: "old"},
		{name: "partially committed", commitKeys: []string{"state"}, wantState: "asleep", wantReason: "old"},
		{name: "fully committed", commitKeys: []string{"state", "reason"}, wantState: "asleep", wantReason: "healed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := NewMemStore()
			bead, err := mem.Create(Bead{
				Title:    "worker",
				Type:     "session",
				Metadata: map[string]string{"state": "active", "reason": "old"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			writeErr := errors.New("ambiguous metadata batch")
			backing := &ambiguousMetadataBatchStore{
				Store:      mem,
				err:        writeErr,
				commitKeys: tc.commitKeys,
			}
			notifications := 0
			cache := NewCachingStoreForTest(backing, func(string, string, json.RawMessage) {
				notifications++
			})
			if err := cache.Prime(context.Background()); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			cache.mu.RLock()
			startSeq := cache.mutationSeq
			cache.mu.RUnlock()

			if err := cache.SetMetadataBatch(bead.ID, patch); !errors.Is(err, writeErr) {
				t.Fatalf("SetMetadataBatch error = %v, want %v", err, writeErr)
			}

			cache.mu.RLock()
			fence := cache.beadSeq[bead.ID]
			_, dirty := cache.dirty[bead.ID]
			_, local := cache.localBeadAt[bead.ID]
			cache.mu.RUnlock()
			if fence <= startSeq || !dirty || local {
				t.Fatalf("ambiguity fence = seq:%d start:%d dirty:%v local:%v", fence, startSeq, dirty, local)
			}
			if notifications != 0 {
				t.Fatalf("notifications = %d, want 0 for an unconfirmed write", notifications)
			}
			query := ListQuery{Type: "session"}
			if _, ok := cache.CachedList(query); ok {
				t.Fatal("CachedList served a row whose backing write outcome is unknown")
			}

			rows, err := cache.List(query)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(rows) != 1 || rows[0].Metadata["state"] != tc.wantState || rows[0].Metadata["reason"] != tc.wantReason {
				t.Fatalf("List metadata = %#v, want state=%q reason=%q", rows, tc.wantState, tc.wantReason)
			}
			if rows, ok := cache.CachedList(query); !ok || len(rows) != 1 ||
				rows[0].Metadata["state"] != tc.wantState || rows[0].Metadata["reason"] != tc.wantReason {
				t.Fatalf("CachedList after reread = %#v, ok=%v", rows, ok)
			}
		})
	}
}

func TestCachingStoreReleaseIfCurrentDelegatesAndRefreshesCache(t *testing.T) {
	t.Parallel()

	status := "in_progress"
	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "task", Assignee: "worker-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := backing.Update(bead.ID, UpdateOpts{Status: &status}); err != nil {
		t.Fatalf("Update status: %v", err)
	}

	var events []string
	cache := NewCachingStoreForTest(backing, func(eventType, beadID string, _ json.RawMessage) {
		events = append(events, eventType+":"+beadID)
	})
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	events = nil

	released, err := cache.ReleaseIfCurrent(bead.ID, "worker-1")
	if err != nil {
		t.Fatalf("ReleaseIfCurrent: %v", err)
	}
	if !released {
		t.Fatal("ReleaseIfCurrent released = false, want true")
	}
	if backing.releaseIfCurrentCalls != 1 {
		t.Fatalf("backing ReleaseIfCurrent calls = %d, want 1", backing.releaseIfCurrentCalls)
	}
	got, err := cache.Get(bead.ID)
	if err != nil {
		t.Fatalf("cache Get: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("cached bead = %+v, want open and unassigned", got)
	}
	if !stringSliceContains(events, "bead.updated:"+bead.ID) {
		t.Fatalf("events = %v, want bead.updated for released bead", events)
	}
}

func TestCachingStoreReleaseIfCurrentKeepsDirtyWhenRefreshFails(t *testing.T) {
	t.Parallel()

	status := "in_progress"
	backing := &releaseRefreshFailOnceStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "task", Assignee: "worker-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := backing.Update(bead.ID, UpdateOpts{Status: &status}); err != nil {
		t.Fatalf("Update status: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	released, err := cache.ReleaseIfCurrent(bead.ID, "worker-1")
	if err != nil {
		t.Fatalf("ReleaseIfCurrent: %v", err)
	}
	if !released {
		t.Fatal("ReleaseIfCurrent released = false, want true")
	}
	cache.mu.Lock()
	_, dirty := cache.dirty[bead.ID]
	cache.mu.Unlock()
	if !dirty {
		t.Fatal("released bead was not kept dirty after refresh failure")
	}

	got, err := cache.Get(bead.ID)
	if err != nil {
		t.Fatalf("cache Get after dirty refresh: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("cached bead after dirty refresh = %+v, want open and unassigned", got)
	}
}

func TestCachingStoreDependencyWritesNotifyBeadUpdatedWithDeps(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	target, err := backing.Create(Bead{Title: "target"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	blocker, err := backing.Create(Bead{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	var notifications []cacheWriteNotification
	cache := NewCachingStoreForTest(backing, func(eventType, beadID string, payload json.RawMessage) {
		notifications = append(notifications, cacheWriteNotification{eventType: eventType, beadID: beadID, payload: payload})
	})
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	if err := cache.DepAdd(target.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	if err := cache.DepRemove(target.ID, blocker.ID); err != nil {
		t.Fatalf("DepRemove: %v", err)
	}

	if len(notifications) != 2 {
		t.Fatalf("notifications = %d, want 2: %#v", len(notifications), notifications)
	}
	added, _, err := decodeCacheEvent(notifications[0].payload)
	if err != nil {
		t.Fatalf("decode add notification: %v", err)
	}
	if notifications[0].eventType != "bead.updated" || len(added.Dependencies) != 1 || added.Dependencies[0].DependsOnID != blocker.ID {
		t.Fatalf("add notification = %#v bead=%+v, want dependency snapshot", notifications[0], added)
	}
	removed, _, err := decodeCacheEvent(notifications[1].payload)
	if err != nil {
		t.Fatalf("decode remove notification: %v", err)
	}
	if notifications[1].eventType != "bead.updated" || len(removed.Dependencies) != 0 {
		t.Fatalf("remove notification = %#v bead=%+v, want empty dependency snapshot", notifications[1], removed)
	}
}

func TestCachingStoreDeleteNotifiesBeadDeleted(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	bead, err := backing.Create(Bead{Title: "delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var notifications []cacheWriteNotification
	cache := NewCachingStoreForTest(backing, func(eventType, beadID string, payload json.RawMessage) {
		notifications = append(notifications, cacheWriteNotification{eventType: eventType, beadID: beadID, payload: payload})
	})
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	if err := cache.Delete(bead.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if len(notifications) != 1 {
		t.Fatalf("notifications = %d, want 1: %#v", len(notifications), notifications)
	}
	if notifications[0].eventType != "bead.deleted" || notifications[0].beadID != bead.ID {
		t.Fatalf("notification = %#v, want bead.deleted for %s", notifications[0], bead.ID)
	}
	deleted, _, err := decodeCacheEvent(notifications[0].payload)
	if err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if deleted.ID != bead.ID || deleted.Title != "delete" {
		t.Fatalf("deleted payload = %+v, want deleted bead snapshot", deleted)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestCachingStoreSetMetadataSkipsBackingWhenCachedValueMatches verifies that
// SetMetadata short-circuits before the backing call when the cached bead
// already has metadata[key]==value. Without this guard, no-op writes still
// fire bd's on_update hook and emit a bead.updated event.
func TestCachingStoreSetMetadataSkipsBackingWhenCachedValueMatches(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := backing.SetMetadata(bead.ID, "foo", "bar"); err != nil {
		t.Fatalf("seed SetMetadata: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.setMetadataCalls = 0

	if err := cache.SetMetadata(bead.ID, "foo", "bar"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if backing.setMetadataCalls != 0 {
		t.Errorf("backing.SetMetadata called %d times; want 0 (no-op write must short-circuit)",
			backing.setMetadataCalls)
	}
}

func TestCachingStoreSetMetadataFallsThroughWhenCacheStateCannotProveNoop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state cacheState
	}{
		{name: "uninitialized", state: cacheUninitialized},
		{name: "degraded", state: cacheDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/single", func(t *testing.T) {
			t.Parallel()

			backing := &countingBackingStore{Store: NewMemStore()}
			bead := createBeadWithMetadata(t, backing, map[string]string{"foo": "bar"})
			cache := staleMatchingMetadataCache(backing, bead, tt.state)
			backing.setMetadataCalls = 0

			if err := cache.SetMetadata(bead.ID, "foo", "bar"); err != nil {
				t.Fatalf("SetMetadata: %v", err)
			}
			if backing.setMetadataCalls != 1 {
				t.Fatalf("backing.SetMetadata called %d times; want 1", backing.setMetadataCalls)
			}
		})

		t.Run(tt.name+"/batch", func(t *testing.T) {
			t.Parallel()

			backing := &countingBackingStore{Store: NewMemStore()}
			bead := createBeadWithMetadata(t, backing, map[string]string{"foo": "bar", "baz": "qux"})
			cache := staleMatchingMetadataCache(backing, bead, tt.state)
			backing.setMetadataBatchCalls = 0

			if err := cache.SetMetadataBatch(bead.ID, map[string]string{"foo": "bar", "baz": "qux"}); err != nil {
				t.Fatalf("SetMetadataBatch: %v", err)
			}
			if backing.setMetadataBatchCalls != 1 {
				t.Fatalf("backing.SetMetadataBatch called %d times; want 1", backing.setMetadataBatchCalls)
			}
		})
	}
}

func createBeadWithMetadata(t *testing.T, backing Store, metadata map[string]string) Bead {
	t.Helper()

	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := backing.SetMetadataBatch(bead.ID, metadata); err != nil {
		t.Fatalf("seed SetMetadataBatch: %v", err)
	}
	bead, err = backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return bead
}

func staleMatchingMetadataCache(backing Store, bead Bead, state cacheState) *CachingStore {
	cache := NewCachingStoreForTest(backing, nil)
	cache.mu.Lock()
	cache.beads[bead.ID] = cloneBead(bead)
	cache.state = state
	cache.mu.Unlock()
	return cache
}

// TestCachingStoreSetMetadataFallsThroughOnValueMismatch verifies that a
// real value change still propagates to the backing store.
func TestCachingStoreSetMetadataFallsThroughOnValueMismatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := backing.SetMetadata(bead.ID, "foo", "old"); err != nil {
		t.Fatalf("seed SetMetadata: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.setMetadataCalls = 0

	if err := cache.SetMetadata(bead.ID, "foo", "new"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	// A cache hit now widens the write to the full merged metadata state
	// (mergedMetadataForWrite, F3) and sends it via SetMetadataBatch instead
	// of the single-key SetMetadata, so count both entry points.
	if got := backing.setMetadataCalls + backing.setMetadataBatchCalls; got != 1 {
		t.Errorf("backing SetMetadata/SetMetadataBatch called %d times; want 1 (real change must propagate)",
			got)
	}
}

// TestCachingStoreSetMetadataFallsThroughOnCacheMiss verifies that
// SetMetadata calls the backing store when the cache has no entry for the
// bead — without a primed copy we cannot prove the write is a no-op.
func TestCachingStoreSetMetadataFallsThroughOnCacheMiss(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	bead, err := backing.Create(Bead{Title: "post-prime"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.setMetadataCalls = 0

	if err := cache.SetMetadata(bead.ID, "foo", "bar"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if backing.setMetadataCalls != 1 {
		t.Errorf("backing.SetMetadata called %d times; want 1 (cache miss must fall through)",
			backing.setMetadataCalls)
	}
}

// TestCachingStoreSetMetadataBatchSkipsBackingWhenAllCachedValuesMatch
// verifies that SetMetadataBatch short-circuits when every kv pair already
// matches the cached metadata.
func TestCachingStoreSetMetadataBatchSkipsBackingWhenAllCachedValuesMatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for k, v := range map[string]string{"foo": "1", "bar": "2", "baz": "3"} {
		if err := backing.SetMetadata(bead.ID, k, v); err != nil {
			t.Fatalf("seed SetMetadata(%s): %v", k, err)
		}
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.setMetadataBatchCalls = 0

	if err := cache.SetMetadataBatch(bead.ID, map[string]string{"foo": "1", "bar": "2"}); err != nil {
		t.Fatalf("SetMetadataBatch: %v", err)
	}
	if backing.setMetadataBatchCalls != 0 {
		t.Errorf("backing.SetMetadataBatch called %d times; want 0 (all-match must short-circuit)",
			backing.setMetadataBatchCalls)
	}
}

// TestCachingStoreSetMetadataBatchFallsThroughOnAnyMismatch verifies that
// even one mismatching kv forces the backing call — partial matches do not
// suffice to skip the write.
func TestCachingStoreSetMetadataBatchFallsThroughOnAnyMismatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for k, v := range map[string]string{"foo": "1", "bar": "2"} {
		if err := backing.SetMetadata(bead.ID, k, v); err != nil {
			t.Fatalf("seed SetMetadata(%s): %v", k, err)
		}
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.setMetadataBatchCalls = 0

	// foo matches the cached value, bar does not. The mismatch must force
	// the full batch to the backing store.
	if err := cache.SetMetadataBatch(bead.ID, map[string]string{"foo": "1", "bar": "DIFFERENT"}); err != nil {
		t.Fatalf("SetMetadataBatch: %v", err)
	}
	if backing.setMetadataBatchCalls != 1 {
		t.Errorf("backing.SetMetadataBatch called %d times; want 1 (mismatch must propagate)",
			backing.setMetadataBatchCalls)
	}
}

// TestCachingStoreSetMetadataBatchEmptyKVsIsNoop verifies that an empty kvs
// map returns nil immediately without calling the backing store. This is
// the early-return branch before metadataAlreadyMatchesCached.
func TestCachingStoreSetMetadataBatchEmptyKVsIsNoop(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.setMetadataBatchCalls = 0

	if err := cache.SetMetadataBatch(bead.ID, map[string]string{}); err != nil {
		t.Fatalf("SetMetadataBatch(empty): %v", err)
	}
	if backing.setMetadataBatchCalls != 0 {
		t.Errorf("backing.SetMetadataBatch called %d times; want 0 (empty kvs must short-circuit)",
			backing.setMetadataBatchCalls)
	}
}

// staleWriteBackStore reproduces dr-1tvd's metadata-clobber defect class in a
// deterministic, Go-level fake: bd's dolt-server-mode metadata engine reads
// a bead's row at some point, then later writes the ENTIRE resulting
// metadata map back as one blob built from that (possibly stale) base plus
// only the keys the caller explicitly supplied — silently dropping any
// field written by another actor after the base was captured that the
// caller's own kvs do not restate. Real MemStore.Update/SetMetadataBatch
// merge per key from the CURRENT row and cannot reproduce a dropped field,
// so this fake tracks its own authoritative metadata row instead of
// delegating metadata storage to MemStore, and lets a test freeze a stale
// base explicitly via freezeStaleBase.
type staleWriteBackStore struct {
	*MemStore

	mu     sync.Mutex
	meta   map[string]map[string]string
	frozen map[string]map[string]string
}

func newStaleWriteBackStore() *staleWriteBackStore {
	return &staleWriteBackStore{
		MemStore: NewMemStore(),
		meta:     make(map[string]map[string]string),
		frozen:   make(map[string]map[string]string),
	}
}

func (s *staleWriteBackStore) Create(b Bead) (Bead, error) {
	created, err := s.MemStore.Create(b)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.meta[created.ID] = maps.Clone(b.Metadata)
	s.mu.Unlock()
	created.Metadata = maps.Clone(b.Metadata)
	return created, nil
}

func (s *staleWriteBackStore) Get(id string) (Bead, error) {
	b, err := s.MemStore.Get(id)
	if err != nil {
		return b, err
	}
	s.mu.Lock()
	if row, ok := s.meta[id]; ok {
		b.Metadata = maps.Clone(row)
	}
	s.mu.Unlock()
	return b, nil
}

// freezeStaleBase captures id's current metadata row as the base the next
// SetMetadata/SetMetadataBatch write-back will use instead of the row's true
// current state, simulating an internal read that happened before a later
// concurrent write lands (dr-1tvd Mechanism A: an 11ms race between an
// outcome-close write and a session-stamp write from a stale base).
func (s *staleWriteBackStore) freezeStaleBase(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen[id] = maps.Clone(s.meta[id])
}

// Update applies non-metadata fields via MemStore and merges metadata
// directly into the authoritative row — an immediate, non-stale write,
// simulating a close's own commit landing.
func (s *staleWriteBackStore) Update(id string, opts UpdateOpts) error {
	nonMeta := opts
	nonMeta.Metadata = nil
	if err := s.MemStore.Update(id, nonMeta); err != nil {
		return err
	}
	if len(opts.Metadata) == 0 {
		return nil
	}
	s.mu.Lock()
	row := maps.Clone(s.meta[id])
	if row == nil {
		row = make(map[string]string, len(opts.Metadata))
	}
	for k, v := range opts.Metadata {
		row[k] = v
	}
	s.meta[id] = row
	s.mu.Unlock()
	return nil
}

func (s *staleWriteBackStore) SetMetadata(id, key, value string) error {
	return s.SetMetadataBatch(id, map[string]string{key: value})
}

// SetMetadataBatch reproduces the stale whole-row write-back: if a base was
// frozen via freezeStaleBase, THAT base (not the row's true current state)
// is used, merged with only kvs — dropping any field written since the
// freeze that kvs does not restate.
func (s *staleWriteBackStore) SetMetadataBatch(id string, kvs map[string]string) error {
	s.mu.Lock()
	base, frozen := s.frozen[id]
	if !frozen {
		base = s.meta[id]
	}
	full := maps.Clone(base)
	if full == nil {
		full = make(map[string]string, len(kvs))
	}
	for k, v := range kvs {
		full[k] = v
	}
	s.meta[id] = full
	delete(s.frozen, id)
	s.mu.Unlock()
	return nil
}

// TestCachingStoreSetMetadataBatchPreservesConcurrentCloseDisposition is the
// dr-1tvd F2/F3 regression test: a supervisor stamp write (SetMetadataBatch)
// racing a close's disposition write must not lose the disposition, even
// against a backing store whose own metadata engine writes back from a
// stale base (dr-1tvd Mechanism A). F3 widens CachingStore's outgoing
// SetMetadata/SetMetadataBatch write to the bead's full cached metadata
// (mergedMetadataForWrite) instead of sending just the delta, so the
// disposition field the close just landed in the cache is resent explicitly
// on the very next stamp write, surviving the backing's stale base.
func TestCachingStoreSetMetadataBatchPreservesConcurrentCloseDisposition(t *testing.T) {
	t.Parallel()

	backing := newStaleWriteBackStore()
	bead, err := backing.Create(Bead{Title: "race", Metadata: map[string]string{"other": "x"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	// Simulate bd's internal metadata engine having read this row before the
	// close below commits.
	backing.freezeStaleBase(bead.ID)

	closedStatus := "closed"
	if err := cache.Update(bead.ID, UpdateOpts{
		Status:   &closedStatus,
		Metadata: map[string]string{"producer_disposition": "success"},
	}); err != nil {
		t.Fatalf("Update (close): %v", err)
	}

	// The stamp write only supplies a narrow delta. Without F3's widening
	// this backing call would carry only {"heartbeat": "t1"} and the stale
	// write-back would drop producer_disposition.
	if err := cache.SetMetadataBatch(bead.ID, map[string]string{"heartbeat": "t1"}); err != nil {
		t.Fatalf("SetMetadataBatch (stamp): %v", err)
	}

	got, err := backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata["producer_disposition"] != "success" {
		t.Errorf("producer_disposition clobbered by stale stamp write-back: got %q, want %q",
			got.Metadata["producer_disposition"], "success")
	}
	if got.Metadata["heartbeat"] != "t1" {
		t.Errorf("heartbeat = %q, want %q", got.Metadata["heartbeat"], "t1")
	}
	if got.Metadata["other"] != "x" {
		t.Errorf("other = %q, want %q (unrelated field must survive)", got.Metadata["other"], "x")
	}
}

// concurrentWholeRowStore reproduces dr-1tvd's whole-row write-back mechanism
// (see staleWriteBackStore) while also tracking how many goroutines are
// simultaneously inside SetMetadataBatch for the same id. A per-key backing
// merge (like MemStore's own SetMetadataBatch) cannot exercise the race
// described in gc-g7x3t Finding 2: it tolerates an unserialized CachingStore
// by construction, because each writer's key survives independently of what
// the others send. This fake instead REPLACES the whole row with exactly the
// kvs it receives, so a caller whose merge was built from a stale cache
// snapshot (because it ran concurrently with, instead of strictly after,
// another writer for the same id) drops that writer's key on the floor —
// the actual clobber mechanism, reproduced deterministically.
type concurrentWholeRowStore struct {
	*MemStore

	mu          sync.Mutex
	meta        map[string]map[string]string
	inflight    int
	maxInflight int
}

func newConcurrentWholeRowStore() *concurrentWholeRowStore {
	return &concurrentWholeRowStore{
		MemStore: NewMemStore(),
		meta:     make(map[string]map[string]string),
	}
}

func (s *concurrentWholeRowStore) Create(b Bead) (Bead, error) {
	created, err := s.MemStore.Create(b)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.meta[created.ID] = maps.Clone(b.Metadata)
	s.mu.Unlock()
	created.Metadata = maps.Clone(b.Metadata)
	return created, nil
}

func (s *concurrentWholeRowStore) Get(id string) (Bead, error) {
	b, err := s.MemStore.Get(id)
	if err != nil {
		return b, err
	}
	s.mu.Lock()
	if row, ok := s.meta[id]; ok {
		b.Metadata = maps.Clone(row)
	}
	s.mu.Unlock()
	return b, nil
}

func (s *concurrentWholeRowStore) SetMetadata(id, key, value string) error {
	return s.SetMetadataBatch(id, map[string]string{key: value})
}

// SetMetadataBatch replaces id's whole metadata row with exactly kvs — no
// merge against whatever is currently stored. Yielding the scheduler
// repeatedly between marking entry and committing widens the window for a
// concurrent, unserialized caller to interleave its own read-merge-write in
// a real (non-mocked) scheduler, rather than relying on luck to hit the
// race. This uses cooperative yields rather than a fixed sleep so the test
// doesn't block on wall-clock time to detect the race.
func (s *concurrentWholeRowStore) SetMetadataBatch(id string, kvs map[string]string) error {
	s.mu.Lock()
	s.inflight++
	if s.inflight > s.maxInflight {
		s.maxInflight = s.inflight
	}
	s.mu.Unlock()

	for i := 0; i < 1000; i++ {
		runtime.Gosched()
	}

	s.mu.Lock()
	s.meta[id] = maps.Clone(kvs)
	s.inflight--
	s.mu.Unlock()
	return nil
}

// TestCachingStoreConcurrentMetadataWritesAreSerializedPerID is gc-g7x3t
// Finding 2's regression test: many goroutines calling SetMetadataBatch on
// the SAME id, each with its own key, must not clobber one another even
// though the backing store performs a whole-row write-back from whatever
// CachingStore hands it (dr-1tvd's actual mechanism). Without idWriteLocks
// serializing the merge-write-refresh-absorb critical section per id, a
// writer's mergedMetadataForWrite call can read the cache before an
// in-flight sibling writer has absorbed its own result, producing a merge
// that omits the sibling's key — which the whole-row backing then commits
// over the sibling's write.
func TestCachingStoreConcurrentMetadataWritesAreSerializedPerID(t *testing.T) {
	t.Parallel()

	backing := newConcurrentWholeRowStore()
	bead, err := backing.Create(Bead{Title: "race", Metadata: map[string]string{"other": "x"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			value := fmt.Sprintf("v%d", i)
			if err := cache.SetMetadataBatch(bead.ID, map[string]string{key: value}); err != nil {
				t.Errorf("SetMetadataBatch(%q): %v", key, err)
			}
		}(i)
	}
	wg.Wait()

	backing.mu.Lock()
	maxInflight := backing.maxInflight
	backing.mu.Unlock()
	if maxInflight > 1 {
		t.Errorf("observed %d concurrent backing.SetMetadataBatch calls for the same id; "+
			"idWriteLocks should serialize the merge-write-absorb critical section to 1 at a time", maxInflight)
	}

	got, err := backing.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%d", i)
		want := fmt.Sprintf("v%d", i)
		if got.Metadata[key] != want {
			t.Errorf("metadata[%q] = %q, want %q (lost to a concurrent writer)", key, got.Metadata[key], want)
		}
	}
	if got.Metadata["other"] != "x" {
		t.Errorf("other = %q, want %q (unrelated field must survive concurrent writes)", got.Metadata["other"], "x")
	}
}

// TestCachingStoreUpdateSkipsBackingWhenAllFieldsMatch verifies that Update
// short-circuits before the backing call when every non-nil opts field
// already matches the cached bead. Without this guard the reconciler's
// per-tick Update calls fire bd subprocesses + post-Get refreshes even when
// the payload is identical. See gastownhall/gascity#1978 Phase 1.
func TestCachingStoreUpdateSkipsBackingWhenAllFieldsMatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test", Assignee: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.updateCalls = 0

	assignee := "alice"
	if err := cache.Update(bead.ID, UpdateOpts{Assignee: &assignee}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if backing.updateCalls != 0 {
		t.Errorf("backing.Update called %d times; want 0 (no-op update must short-circuit)",
			backing.updateCalls)
	}
}

// TestCachingStoreUpdateFallsThroughOnValueMismatch verifies that a real
// field change still propagates to the backing store.
func TestCachingStoreUpdateFallsThroughOnValueMismatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test", Assignee: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.updateCalls = 0

	assignee := "bob"
	if err := cache.Update(bead.ID, UpdateOpts{Assignee: &assignee}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if backing.updateCalls != 1 {
		t.Errorf("backing.Update called %d times; want 1 (real change must propagate)",
			backing.updateCalls)
	}
}

// TestCachingStoreUpdateFallsThroughOnCacheMiss verifies that Update calls
// the backing store when the cache has no entry for the bead — without a
// primed copy we cannot prove the write is a no-op.
func TestCachingStoreUpdateFallsThroughOnCacheMiss(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	bead, err := backing.Create(Bead{Title: "post-prime", Assignee: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.updateCalls = 0

	assignee := "alice"
	if err := cache.Update(bead.ID, UpdateOpts{Assignee: &assignee}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if backing.updateCalls != 1 {
		t.Errorf("backing.Update called %d times; want 1 (cache miss must fall through)",
			backing.updateCalls)
	}
}

// TestCachingStoreUpdateFallsThroughOnLabelMismatch verifies that a Labels
// opt requesting a label not yet on the bead still propagates to the backing
// store.
func TestCachingStoreUpdateFallsThroughOnLabelMismatch(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test", Labels: []string{"existing"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.updateCalls = 0

	if err := cache.Update(bead.ID, UpdateOpts{Labels: []string{"new-label"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if backing.updateCalls != 1 {
		t.Errorf("backing.Update called %d times; want 1 (new label must propagate)",
			backing.updateCalls)
	}
}

// TestCachingStoreCloseSkipsBackingWhenAlreadyClosed verifies that Close
// short-circuits before the backing call when the cached bead is already
// closed. The cache only holds active beads after Prime, so the close has
// to happen through CachingStore first to seed the closed status into the
// cache. See gastownhall/gascity#1978 Phase 1.
func TestCachingStoreCloseSkipsBackingWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	// First close: open → closed, must propagate.
	if err := cache.Close(bead.ID); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if backing.closeCalls != 1 {
		t.Fatalf("backing.Close after first close = %d, want 1", backing.closeCalls)
	}
	backing.closeCalls = 0

	// Second close on the already-closed bead must short-circuit. The
	// reconciler / cleanup paths sometimes re-close the same bead on
	// retry; that should not generate fresh bd subprocess traffic.
	if err := cache.Close(bead.ID); err != nil {
		t.Fatalf("repeat Close: %v", err)
	}
	if backing.closeCalls != 0 {
		t.Errorf("backing.Close called %d times on repeat close; want 0 (already-closed must short-circuit)",
			backing.closeCalls)
	}
}

// TestCachingStoreCloseFallsThroughWhenOpen verifies that a real close still
// propagates to the backing store.
func TestCachingStoreCloseFallsThroughWhenOpen(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	bead, err := backing.Create(Bead{Title: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	backing.closeCalls = 0

	if err := cache.Close(bead.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if backing.closeCalls != 1 {
		t.Errorf("backing.Close called %d times; want 1 (open->closed must propagate)",
			backing.closeCalls)
	}
}

// TestCachingStoreCloseFallsThroughOnCacheMiss verifies that Close calls the
// backing store when the cache has no entry for the bead.
func TestCachingStoreCloseFallsThroughOnCacheMiss(t *testing.T) {
	t.Parallel()

	backing := &countingBackingStore{Store: NewMemStore()}
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	bead, err := backing.Create(Bead{Title: "post-prime"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.closeCalls = 0

	if err := cache.Close(bead.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if backing.closeCalls != 1 {
		t.Errorf("backing.Close called %d times; want 1 (cache miss must fall through)",
			backing.closeCalls)
	}
}

// TestCachingStoreUpdateSkipsBackingPerFieldMatch is the per-field
// short-circuit coverage requested in gastownhall/gascity#2199. The original
// PR #2159 exercised Assignee + Labels-mismatch + cache-miss only; the
// remaining 6 field branches in updateMatchesCached were asserted by
// inspection. This table-driven test pins the short-circuit behavior for
// each field independently so a future refactor of any single check
// surfaces in CI.
func TestCachingStoreUpdateSkipsBackingPerFieldMatch(t *testing.T) {
	t.Parallel()

	type fieldCase struct {
		name string
		seed Bead
		opts UpdateOpts
	}
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }

	cases := []fieldCase{
		{
			name: "Title",
			seed: Bead{Title: "pinned"},
			opts: UpdateOpts{Title: strPtr("pinned")},
		},
		{
			name: "Status",
			seed: Bead{Title: "x", Status: "open"},
			opts: UpdateOpts{Status: strPtr("open")},
		},
		{
			name: "Type",
			seed: Bead{Title: "x", Type: "task"},
			opts: UpdateOpts{Type: strPtr("task")},
		},
		{
			name: "Priority",
			seed: Bead{Title: "x", Priority: intPtr(2)},
			opts: UpdateOpts{Priority: intPtr(2)},
		},
		{
			name: "Description",
			seed: Bead{Title: "x", Description: "body"},
			opts: UpdateOpts{Description: strPtr("body")},
		},
		{
			name: "ParentID",
			seed: Bead{Title: "x", ParentID: "gc-parent"},
			opts: UpdateOpts{ParentID: strPtr("gc-parent")},
		},
		{
			name: "Metadata",
			seed: Bead{Title: "x", Metadata: map[string]string{"k": "v"}},
			opts: UpdateOpts{Metadata: map[string]string{"k": "v"}},
		},
		{
			name: "Labels-present",
			seed: Bead{Title: "x", Labels: []string{"a", "b"}},
			opts: UpdateOpts{Labels: []string{"a"}},
		},
		{
			name: "RemoveLabels-absent",
			seed: Bead{Title: "x", Labels: []string{"a"}},
			opts: UpdateOpts{RemoveLabels: []string{"z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			backing := &countingBackingStore{Store: NewMemStore()}
			bead, err := backing.Create(tc.seed)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			cache := NewCachingStoreForTest(backing, nil)
			if err := cache.Prime(context.Background()); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			backing.updateCalls = 0

			if err := cache.Update(bead.ID, tc.opts); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if backing.updateCalls != 0 {
				t.Errorf("backing.Update called %d times; want 0 (%s value-match must short-circuit)",
					backing.updateCalls, tc.name)
			}
		})
	}
}

// TestCachingStoreUpdateFallsThroughPerFieldMismatch is the mismatch-side
// companion to TestCachingStoreUpdateSkipsBackingPerFieldMatch. Each
// subtest asserts that a real change in the named field forces the
// backing call — guarding the matcher against accidentally returning true
// when a single field actually differs.
func TestCachingStoreUpdateFallsThroughPerFieldMismatch(t *testing.T) {
	t.Parallel()

	type fieldCase struct {
		name string
		seed Bead
		opts UpdateOpts
	}
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }

	cases := []fieldCase{
		{
			name: "Title",
			seed: Bead{Title: "before"},
			opts: UpdateOpts{Title: strPtr("after")},
		},
		{
			name: "Status",
			seed: Bead{Title: "x", Status: "open"},
			opts: UpdateOpts{Status: strPtr("closed")},
		},
		{
			name: "Type",
			seed: Bead{Title: "x", Type: "task"},
			opts: UpdateOpts{Type: strPtr("epic")},
		},
		{
			name: "Priority",
			seed: Bead{Title: "x", Priority: intPtr(2)},
			opts: UpdateOpts{Priority: intPtr(3)},
		},
		{
			name: "Priority-nil-cached",
			seed: Bead{Title: "x"},
			opts: UpdateOpts{Priority: intPtr(2)},
		},
		{
			name: "Description",
			seed: Bead{Title: "x", Description: "before"},
			opts: UpdateOpts{Description: strPtr("after")},
		},
		{
			name: "ParentID",
			seed: Bead{Title: "x", ParentID: "gc-a"},
			opts: UpdateOpts{ParentID: strPtr("gc-b")},
		},
		{
			name: "Metadata-value",
			seed: Bead{Title: "x", Metadata: map[string]string{"k": "old"}},
			opts: UpdateOpts{Metadata: map[string]string{"k": "new"}},
		},
		{
			name: "Metadata-missing-key",
			seed: Bead{Title: "x"},
			opts: UpdateOpts{Metadata: map[string]string{"k": "v"}},
		},
		{
			name: "RemoveLabels-present",
			seed: Bead{Title: "x", Labels: []string{"a", "b"}},
			opts: UpdateOpts{RemoveLabels: []string{"a"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			backing := &countingBackingStore{Store: NewMemStore()}
			bead, err := backing.Create(tc.seed)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			cache := NewCachingStoreForTest(backing, nil)
			if err := cache.Prime(context.Background()); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			backing.updateCalls = 0

			if err := cache.Update(bead.ID, tc.opts); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if backing.updateCalls != 1 {
				t.Errorf("backing.Update called %d times; want 1 (%s real change must propagate)",
					backing.updateCalls, tc.name)
			}
		})
	}
}

func TestCachingStoreCloseAdoptsFreshBackingRead(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	b, err := cache.Create(Bead{Title: "close-adopt"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cache.Close(b.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fresh, err := backing.Get(b.ID)
	if err != nil {
		t.Fatalf("backing Get after close: %v", err)
	}
	got, err := cache.Get(b.ID)
	if err != nil {
		t.Fatalf("cache Get after close: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("cached status after Close = %q, want %q", got.Status, "closed")
	}
	if got.Revision != fresh.Revision {
		t.Fatalf("cached revision after Close = %d, backing = %d; the successful refresh read must be adopted, "+
			"or a Get→conditional-write consumer fences against a revision that no longer exists",
			got.Revision, fresh.Revision)
	}
}

func TestCachingStoreReopenAdoptsFreshBackingRead(t *testing.T) {
	t.Parallel()

	backing := NewMemStore()
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	b, err := cache.Create(Bead{Title: "reopen-adopt"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := cache.Close(b.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := cache.Reopen(b.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	fresh, err := backing.Get(b.ID)
	if err != nil {
		t.Fatalf("backing Get after reopen: %v", err)
	}
	got, err := cache.Get(b.ID)
	if err != nil {
		t.Fatalf("cache Get after reopen: %v", err)
	}
	if got.Status != "open" {
		t.Fatalf("cached status after Reopen = %q, want %q", got.Status, "open")
	}
	if got.Revision != fresh.Revision {
		t.Fatalf("cached revision after Reopen = %d, backing = %d; the successful refresh read must be adopted",
			got.Revision, fresh.Revision)
	}
}

func TestCachingStoreCloseKeepsCachedSynthesisWhenRefreshFails(t *testing.T) {
	t.Parallel()

	// When the post-close refresh Get fails, Close must still fall back to the
	// cached-status synthesis (today's behavior) rather than dropping the entry.
	backing := &releaseRefreshFailOnceStore{Store: NewMemStore()}
	cache := NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	b, err := cache.Create(Bead{Title: "close-refresh-fails"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	backing.failNextGet = true
	if err := cache.Close(b.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The synthesis keeps the entry cached (distinguishing it from an evict,
	// whose follow-up Get would fall through to the backing and also report
	// closed).
	cache.mu.RLock()
	_, inBeads := cache.beads[b.ID]
	cache.mu.RUnlock()
	if !inBeads {
		t.Fatal("entry missing from the cache after Close with failed refresh; want the cached-status synthesis, not an evict")
	}

	got, err := cache.Get(b.ID)
	if err != nil {
		t.Fatalf("cache Get after close with failed refresh: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("cached status after Close with failed refresh = %q, want %q (synthesis fallback)", got.Status, "closed")
	}
}
