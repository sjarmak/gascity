//go:build integration

package beads

import (
	"context"
	"fmt"

	beadslib "github.com/steveyegge/beads"
)

// OpenNativeDoltStoreForConditionalConformance initializes a real, isolated
// upstream store for the external ConditionalWriter conformance suite.
func OpenNativeDoltStoreForConditionalConformance(ctx context.Context, beadsDir, actor string) (*NativeDoltStore, error) {
	storage, err := beadslib.OpenBestAvailable(ctx, beadsDir)
	if err != nil {
		return nil, err
	}
	if err := storage.SetConfig(ctx, "issue_prefix", "gc"); err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("set issue prefix: %w", err)
	}
	metadata, ok := storage.(interface {
		SetMetadata(context.Context, string, string) error
	})
	if !ok {
		_ = storage.Close()
		return nil, fmt.Errorf("native test storage does not implement SetMetadata")
	}
	if err := metadata.SetMetadata(ctx, "_project_id", "conditional-writer-conformance-project"); err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("set project identity: %w", err)
	}
	return newNativeDoltStoreWithStorageAndPrefix(storage, actor, "gc"), nil
}

// CommitNativeDoltStoreForConditionalConformance settles pending graph writes
// before tests exercise the production full-recompute cleanliness guard.
func CommitNativeDoltStoreForConditionalConformance(ctx context.Context, store *NativeDoltStore, actor string) error {
	storage, release, err := store.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	committer, ok := storage.(interface {
		CommitPending(context.Context, string) (bool, error)
	})
	if !ok {
		return fmt.Errorf("native test storage does not implement CommitPending")
	}
	_, err = committer.CommitPending(ctx, actor)
	return err
}
