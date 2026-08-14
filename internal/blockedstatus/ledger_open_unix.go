//go:build unix

package blockedstatus

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openRegularMigrationLedger(path string) (*os.File, int64, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open migration ledger: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, 0, fmt.Errorf("open migration ledger: invalid file descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat migration ledger: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, fmt.Errorf("migration ledger must be a regular file")
	}
	return file, info.Size(), nil
}
