//go:build unix

package blockedstatus

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLoadLegacyPreimagesFileRejectsFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()

	fifo := filepath.Join(t.TempDir(), "ledger.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	if _, err := LoadLegacyPreimagesFile(fifo, "city:ds-research", "project-1", 100); err == nil {
		t.Fatal("FIFO error = nil, want refusal before reading")
	}
}
