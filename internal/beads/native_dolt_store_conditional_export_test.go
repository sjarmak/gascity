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
	return newNativeDoltStoreWithStorageAndPrefix(storage, actor, "gc"), nil
}
