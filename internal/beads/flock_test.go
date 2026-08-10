package beads

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestContextFileFlockHonorsDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.lock")
	held := NewFileFlock(path)
	if err := held.Lock(); err != nil {
		t.Fatal(err)
	}
	defer held.Unlock() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	waiter := NewContextFileFlock(ctx, path)
	err := waiter.Lock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock error = %v, want context deadline exceeded", err)
	}
}

func TestContextFileFlockRejectsCanceledContextWhenLockIsFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.lock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	flock := NewContextFileFlock(ctx, path)
	err := flock.Lock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock error = %v, want context canceled", err)
	}
	if flock.f != nil {
		t.Fatal("canceled Lock retained the file handle")
	}
}
