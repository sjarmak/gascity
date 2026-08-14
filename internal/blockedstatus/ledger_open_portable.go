//go:build !unix

package blockedstatus

import (
	"fmt"
	"os"
)

func openRegularMigrationLedger(path string) (*os.File, int64, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat migration ledger: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("migration ledger must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open migration ledger: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat migration ledger: %w", err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, 0, fmt.Errorf("migration ledger changed while opening")
	}
	return file, after.Size(), nil
}
