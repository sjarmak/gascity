package beadmail

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// unreadMailSeed builds a seed Bead for NewMemStoreFrom representing an open
// unread message bead created at createdAt. opts mutate the bead (e.g. add the
// "read" label or mark it closed) so a single helper covers every candidate
// variant, mirroring readMailSeed for the unread-sweep tests.
func unreadMailSeed(id string, createdAt time.Time, opts ...func(*beads.Bead)) beads.Bead {
	b := beads.Bead{
		ID:        id,
		Type:      "message",
		Status:    "open",
		CreatedAt: createdAt,
	}
	for _, opt := range opts {
		opt(&b)
	}
	return b
}

func TestSweepUnreadMessagesBefore_ClosesAgedUnreadMailWithReason(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now
	old := now.Add(-time.Minute)
	fresh := now.Add(time.Minute)

	seed := []beads.Bead{
		unreadMailSeed("old-1", old),
		unreadMailSeed("old-2", old),
		unreadMailSeed("fresh", fresh),
		unreadMailSeed("read", old, func(b *beads.Bead) { b.Labels = []string{"read"} }),
		unreadMailSeed("already-closed", old, func(b *beads.Bead) { b.Status = "closed" }),
	}
	store := beads.NewMemStoreFrom(100, seed, nil)
	mailStore := beads.MailStore{Store: store}

	const reason = "mail gc-swept: test unread retention reason padded"
	closed, closeErrs, listErr := SweepUnreadMessagesBefore(mailStore, cutoff, 0, reason)
	if listErr != nil {
		t.Fatalf("unexpected list error: %v", listErr)
	}
	if len(closeErrs) != 0 {
		t.Fatalf("unexpected per-bead errors: %v", closeErrs)
	}
	if closed != 2 {
		t.Fatalf("closed = %d, want 2", closed)
	}

	for _, id := range []string{"old-1", "old-2"} {
		b, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if b.Status != "closed" {
			t.Errorf("%s status = %q, want closed", id, b.Status)
		}
		if got := b.Metadata["close_reason"]; got != reason {
			t.Errorf("%s close_reason = %q, want %q", id, got, reason)
		}
	}

	for _, id := range []string{"fresh", "read"} {
		b, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if b.Status != "open" {
			t.Errorf("%s status = %q, want open (must not be swept)", id, b.Status)
		}
		if _, ok := b.Metadata["close_reason"]; ok {
			t.Errorf("%s unexpectedly stamped close_reason", id)
		}
	}
}

func TestSweepUnreadMessagesBefore_LimitCapsCloses(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute)

	seed := []beads.Bead{
		unreadMailSeed("old-1", old),
		unreadMailSeed("old-2", old),
		unreadMailSeed("old-3", old),
	}
	store := beads.NewMemStoreFrom(100, seed, nil)
	mailStore := beads.MailStore{Store: store}

	closed, closeErrs, listErr := SweepUnreadMessagesBefore(mailStore, now, 2, "reason padded to twenty plus characters")
	if listErr != nil || len(closeErrs) != 0 {
		t.Fatalf("unexpected errors: list=%v perBead=%v", listErr, closeErrs)
	}
	if closed != 2 {
		t.Fatalf("closed = %d, want 2 (limit)", closed)
	}

	openCount := 0
	all, err := store.List(beads.ListQuery{Type: "message", TierMode: beads.TierBoth})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		if b.Status == "open" {
			openCount++
		}
	}
	if openCount != 1 {
		t.Fatalf("open unread beads = %d, want 1 (limit left one)", openCount)
	}
}

func TestSweepUnreadMessagesBefore_PerBeadCloseErrorIsCollected(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute)

	// good is older so the created_asc sweep visits it first; both are aged.
	seed := []beads.Bead{
		unreadMailSeed("good", old.Add(-time.Minute)),
		unreadMailSeed("bad", old),
	}
	base := beads.NewMemStoreFrom(100, seed, nil)
	store := closeErrStore{MemStore: base, failClose: map[string]error{"bad": errors.New("close boom")}}
	mailStore := beads.MailStore{Store: store}

	closed, closeErrs, listErr := SweepUnreadMessagesBefore(mailStore, now, 0, "reason padded to twenty plus characters")
	if listErr != nil {
		t.Fatalf("unexpected list error: %v", listErr)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1 (good only)", closed)
	}
	if len(closeErrs) != 1 {
		t.Fatalf("closeErrs = %v, want exactly one", closeErrs)
	}
	if got := closeErrs[0].Error(); !strings.Contains(got, "bad") || !strings.Contains(got, "close boom") {
		t.Fatalf("closeErrs[0] = %q, want it to name the bead and the close failure", got)
	}
}

func TestSweepUnreadMessagesBefore_ListErrorIsFatal(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := listErrStore{MemStore: beads.NewMemStore(), err: errors.New("store down")}
	mailStore := beads.MailStore{Store: store}

	closed, closeErrs, listErr := SweepUnreadMessagesBefore(mailStore, now, 0, "reason padded to twenty plus characters")
	if listErr == nil {
		t.Fatal("expected fatal list error")
	}
	if closed != 0 || len(closeErrs) != 0 {
		t.Fatalf("closed=%d closeErrs=%v, want zero on list failure", closed, closeErrs)
	}
}

func TestCountUnreadMessagesBefore_CountsWithoutMutating(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute)
	fresh := now.Add(time.Minute)

	seed := []beads.Bead{
		unreadMailSeed("old-1", old),
		unreadMailSeed("old-2", old),
		unreadMailSeed("fresh", fresh),
		unreadMailSeed("read", old, func(b *beads.Bead) { b.Labels = []string{"read"} }),
	}
	store := beads.NewMemStoreFrom(100, seed, nil)
	mailStore := beads.MailStore{Store: store}

	count, err := CountUnreadMessagesBefore(mailStore, now, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	// No mutation: every seeded bead is still open.
	for _, id := range []string{"old-1", "old-2", "fresh", "read"} {
		b, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if b.Status != "open" {
			t.Errorf("%s status = %q, count must not mutate", id, b.Status)
		}
	}
}

func TestCountUnreadMessagesBefore_LimitCapsCount(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute)
	seed := []beads.Bead{
		unreadMailSeed("old-1", old),
		unreadMailSeed("old-2", old),
		unreadMailSeed("old-3", old),
	}
	store := beads.NewMemStoreFrom(100, seed, nil)
	mailStore := beads.MailStore{Store: store}

	count, err := CountUnreadMessagesBefore(mailStore, now, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (limit)", count)
	}
}

func TestPurgeUnreadSweptMessageWisps_DeletesAgedUnreadSweptWisps(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)
	aged := now.Add(-2 * time.Hour)
	recent := now.Add(-30 * time.Minute)

	swept := func(id string, createdAt time.Time) beads.Bead {
		return beads.Bead{
			ID: id, Type: "message", Status: "closed", CreatedAt: createdAt, Ephemeral: true,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		}
	}
	seed := []beads.Bead{
		swept("unread-swept-old", aged),
		swept("unread-swept-recent", recent),
		// Aged but closed for the read-mail reason: excluded by close_reason match.
		{
			ID: "read-swept-old", Type: "message", Status: "closed", CreatedAt: aged, Ephemeral: true,
			Metadata: map[string]string{"close_reason": RetentionSweepCloseReason},
		},
		// Aged and unread-swept-reason but still open: excluded by the live re-verify.
		{
			ID: "still-open", Type: "message", Status: "open", CreatedAt: aged, Ephemeral: true,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		},
		// Main-tier: excluded by the TierWisps query.
		{
			ID: "main-tier", Type: "message", Status: "closed", CreatedAt: aged,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		},
	}
	store := &deleteTrackStore{MemStore: beads.NewMemStoreFrom(100, seed, nil), failDelete: map[string]error{}}
	mailStore := beads.MailStore{Store: store}

	purged, err := PurgeUnreadSweptMessageWisps(mailStore, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "unread-swept-old" {
		t.Fatalf("deleted = %v, want [unread-swept-old]", store.deleted)
	}
	for _, id := range []string{"unread-swept-recent", "read-swept-old", "still-open", "main-tier"} {
		if _, err := store.Get(id); err != nil {
			t.Errorf("%s should be preserved: %v", id, err)
		}
	}
}

// TestPurgeUnreadSweptMessageWisps_SkipsMessageReopenedAfterSnapshot mirrors
// the ra-nxppyo read-mail regression test for the unread-sweep arm: a message
// reopened (e.g. by a MarkUnread/un-sweep path) inside the cache window, after
// the closed snapshot was taken but before the purge sweep reaches it, must
// not be destructively deleted on the strength of that stale snapshot.
func TestPurgeUnreadSweptMessageWisps_SkipsMessageReopenedAfterSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)
	aged := now.Add(-2 * time.Hour)

	seed := []beads.Bead{
		{
			ID: "reopened-after-snapshot", Type: "message", Status: "closed", CreatedAt: aged, Ephemeral: true,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		},
	}
	underlying := &deleteTrackStore{MemStore: beads.NewMemStoreFrom(100, seed, nil), failDelete: map[string]error{}}
	store := &staleListStore{deleteTrackStore: underlying, snapshot: seed}

	openStatus := "open"
	if err := underlying.Update("reopened-after-snapshot", beads.UpdateOpts{Status: &openStatus}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	mailStore := beads.MailStore{Store: store}
	purged, err := PurgeUnreadSweptMessageWisps(mailStore, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0 (message was reopened after the snapshot)", purged)
	}
	if len(underlying.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", underlying.deleted)
	}
	if _, err := underlying.Get("reopened-after-snapshot"); err != nil {
		t.Fatalf("message should be preserved: %v", err)
	}
}

func TestPurgeUnreadSweptMessageWisps_SkipsMessageGoneAfterSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)
	aged := now.Add(-2 * time.Hour)

	seed := []beads.Bead{
		{
			ID: "gone-after-snapshot", Type: "message", Status: "closed", CreatedAt: aged, Ephemeral: true,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		},
	}
	underlying := &deleteTrackStore{MemStore: beads.NewMemStoreFrom(100, seed, nil), failDelete: map[string]error{}}
	store := &staleListStore{deleteTrackStore: underlying, snapshot: seed}

	if err := underlying.MemStore.Delete("gone-after-snapshot"); err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	mailStore := beads.MailStore{Store: store}
	purged, err := PurgeUnreadSweptMessageWisps(mailStore, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0 (message already gone)", purged)
	}
	if len(underlying.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", underlying.deleted)
	}
}

func TestPurgeUnreadSweptMessageWisps_DeleteErrorSurfacedAndContinues(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)
	aged := now.Add(-2 * time.Hour)

	swept := func(id string) beads.Bead {
		return beads.Bead{
			ID: id, Type: "message", Status: "closed", CreatedAt: aged, Ephemeral: true,
			Metadata: map[string]string{"close_reason": UnreadRetentionSweepCloseReason},
		}
	}
	store := &deleteTrackStore{
		MemStore:   beads.NewMemStoreFrom(100, []beads.Bead{swept("bad"), swept("good")}, nil),
		failDelete: map[string]error{"bad": errors.New("delete boom")},
	}
	mailStore := beads.MailStore{Store: store}

	purged, err := PurgeUnreadSweptMessageWisps(mailStore, cutoff)
	if err == nil {
		t.Fatal("expected delete error to be surfaced")
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1 (good deleted)", purged)
	}
	if !contains(store.deleted, "good") {
		t.Fatalf("deleted = %v, want to include good", store.deleted)
	}
}

func TestPurgeUnreadSweptMessageWisps_ListErrorSurfaced(t *testing.T) {
	store := listErrStore{MemStore: beads.NewMemStore(), err: errors.New("store down")}
	mailStore := beads.MailStore{Store: store}
	purged, err := PurgeUnreadSweptMessageWisps(mailStore, time.Now())
	if err == nil {
		t.Fatal("expected list error to be surfaced")
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0", purged)
	}
}

// TestUnreadAndReadRetentionCloseReasonsAreDistinct pins the bug's core
// requirement (gc-mtmlx): a message the retention path swept while unread
// must be distinguishable, by its close reason, from a message swept because
// it was read. Both also clear the validation.on-close=error 20-char floor.
func TestUnreadAndReadRetentionCloseReasonsAreDistinct(t *testing.T) {
	if UnreadRetentionSweepCloseReason == RetentionSweepCloseReason {
		t.Fatal("UnreadRetentionSweepCloseReason must differ from RetentionSweepCloseReason")
	}
	if len(UnreadRetentionSweepCloseReason) < 20 {
		t.Fatalf("UnreadRetentionSweepCloseReason len = %d, want >= 20", len(UnreadRetentionSweepCloseReason))
	}
}

// TestRetentionSweptUnreadMailStaysAddressableUntilPurge mirrors
// TestRetentionSweptReadMailStaysAddressableUntilPurge for the unread-mail
// arm: a message that was NEVER read still stays addressable by direct ID
// after the unread-retention sweep closes it, until
// PurgeUnreadSweptMessageWisps deletes it. This ties SweepUnreadMessagesBefore
// to Provider.Get/Read/Reply so a future edit to isRemovedMessageBead cannot
// silently diverge the unread-retention path from the read path — and asserts
// the swept bead's close_reason is verifiably distinct from a read-mail sweep,
// satisfying gc-mtmlx's execution-verification requirement.
func TestRetentionSweptUnreadMailStaysAddressableUntilPurge(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)

	sent, err := p.Send("alice", "bob", "never opened", "sat unread for a very long time")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Deliberately do NOT call p.Read/p.Check: this message was never read by
	// the recipient, unlike the read-mail sweep test's precondition.

	closed, closeErrs, listErr := SweepUnreadMessagesBefore(
		beads.MailStore{Store: store}, time.Now().Add(time.Hour), 0, UnreadRetentionSweepCloseReason)
	if listErr != nil {
		t.Fatalf("sweep list error: %v", listErr)
	}
	if len(closeErrs) != 0 {
		t.Fatalf("sweep per-bead errors: %v", closeErrs)
	}
	if closed != 1 {
		t.Fatalf("swept %d beads, want 1", closed)
	}

	// Precondition: the bead is closed and carries the unread-retention marker,
	// distinct from the read-mail sweep's reason.
	raw, err := store.Get(sent.ID)
	if err != nil {
		t.Fatalf("store.Get after sweep: %v", err)
	}
	if raw.Status != "closed" || raw.Metadata["close_reason"] != UnreadRetentionSweepCloseReason {
		t.Fatalf("swept bead status=%q close_reason=%q, want closed / %q",
			raw.Status, raw.Metadata["close_reason"], UnreadRetentionSweepCloseReason)
	}
	if raw.Metadata["close_reason"] == RetentionSweepCloseReason {
		t.Fatal("unread-swept bead must not carry the read-mail retention reason")
	}
	if hasLabel(raw.Labels, "read") {
		t.Fatal("unread-swept bead must not have been marked read by the sweep")
	}

	// Retention-swept mail (unread arm) stays addressable by direct ID until purge.
	if _, err := p.Get(sent.ID); err != nil {
		t.Errorf("Get(unread-retention-swept) = %v, want addressable", err)
	}
	if _, err := p.Read(sent.ID); err != nil {
		t.Errorf("Read(unread-retention-swept) = %v, want addressable", err)
	}
	reply, err := p.Reply(sent.ID, "bob", "RE: never opened", "still replying after unread retention")
	if err != nil {
		t.Fatalf("Reply(unread-retention-swept) = %v, want addressable", err)
	}
	if reply.ID == "" {
		t.Error("Reply(unread-retention-swept) returned an empty message")
	}

	// But it is retired from the active list views, same as the read-mail arm.
	inbox, err := p.Inbox("bob")
	if err != nil {
		t.Fatalf("Inbox after sweep: %v", err)
	}
	for _, m := range inbox {
		if m.ID == sent.ID {
			t.Errorf("Inbox surfaced unread-retention-swept message %q", sent.ID)
		}
	}

	// Purge deletes the swept-but-aged bead: unread-swept beads do not pile up
	// forever as closed-but-unpurgeable rows.
	purged, err := PurgeUnreadSweptMessageWisps(beads.MailStore{Store: store}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("purge error: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	if _, err := store.Get(sent.ID); !errors.Is(err, beads.ErrNotFound) {
		t.Errorf("Get after purge = %v, want ErrNotFound", err)
	}
}
