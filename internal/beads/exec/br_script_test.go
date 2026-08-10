package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestGcBeadsBrReadyIncludeEphemeralFailsLoudlyUntilSupported(t *testing.T) {
	s := NewStore(findGcBeadsBrScript(t))

	_, err := s.Ready(beads.ReadyQuery{TierMode: beads.TierBoth})
	if err == nil {
		t.Fatal("Ready(TierBoth) succeeded, want gc-beads-br to reject --include-ephemeral until br can support it")
	}
	if !strings.Contains(err.Error(), "ready: --include-ephemeral is not supported by gc-beads-br") {
		t.Fatalf("Ready(TierBoth) error = %q, want clear --include-ephemeral unsupported message", err)
	}
}

func TestGcBeadsBrFenceAndRemovalUseRevisionGuardedMetadataLabels(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "br.log")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const oldLabel = "metahex:647261696e5f61636b5f746f6b656e:6f6c64"
	fake := filepath.Join(binDir, "br")
	script := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$BR_TEST_LOG"
case "$1" in
  show) printf '[{"id":"gc-session","status":"open","labels":["keep","` + oldLabel + `"]}]\n' ;;
  update) printf '{"ok":true}\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BR_TEST_LOG", logPath)
	t.Setenv("BR_DIR", tmp)

	store := NewStore(findGcBeadsBrScript(t))
	if won, err := store.FenceMetadataKey("gc-session", "drain_ack_token", "old", "new"); err != nil || !won {
		t.Fatalf("FenceMetadataKey = (%v, %v), want (true, nil)", won, err)
	}
	if err := store.Update("gc-session", beads.UpdateOpts{RemoveMetadata: []string{"drain_ack_token"}}); err != nil {
		t.Fatalf("Update RemoveMetadata: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{
		"update --json gc-session --set-labels keep,metahex:647261696e5f61636b5f746f6b656e:6e6577",
		"update --json gc-session --set-labels keep",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("br calls missing %q:\n%s", want, log)
		}
	}
}

func findGcBeadsBrScript(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			scriptPath := filepath.Join(dir, "contrib", "beads-scripts", "gc-beads-br")
			if _, err := os.Stat(scriptPath); err != nil {
				t.Fatalf("gc-beads-br not found at %s: %v", scriptPath, err)
			}
			return scriptPath
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (no go.mod)")
		}
		dir = parent
	}
}
