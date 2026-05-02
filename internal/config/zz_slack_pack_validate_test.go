package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSlackPackTOMLValidates is a regression check that
// examples/slack-pack/pack.toml stays well-formed against the live
// service schema. It catches drift between the pack scaffold and
// internal/config when the slack-pack adds or changes service blocks
// — relevant while the pack lives in-tree at examples/slack-pack/.
//
// The test resolves the pack path from the test file's own location
// so it works in any checkout. If the path doesn't exist (e.g. the
// pack has been promoted out of examples/), the test skips rather
// than failing — the file can be deleted at that point.
func TestSlackPackTOMLValidates(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable; cannot locate slack-pack")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	packDir := filepath.Join(repoRoot, "examples", "slack-pack")
	packPath := filepath.Join(packDir, "pack.toml")
	if _, err := os.Stat(packPath); os.IsNotExist(err) {
		t.Skipf("slack-pack not present at %s; pack likely promoted out of examples/", packPath)
	}

	type packTOML struct {
		Pack    map[string]any `toml:"pack"`
		Service []Service      `toml:"service"`
	}
	var pt packTOML
	if _, err := toml.DecodeFile(packPath, &pt); err != nil {
		t.Fatalf("decode %s: %v", packPath, err)
	}
	if len(pt.Service) == 0 {
		t.Skip("slack-pack has no [[service]] blocks yet")
	}
	for i := range pt.Service {
		pt.Service[i].SourceDir = packDir
	}
	if err := ValidateServices(pt.Service); err != nil {
		t.Fatalf("ValidateServices: %v", err)
	}

	// The slack adapter service contract: proxy_process with a
	// /healthz probe over the controller-managed UDS.
	var slack *Service
	for i := range pt.Service {
		if pt.Service[i].Name == "slack" {
			slack = &pt.Service[i]
			break
		}
	}
	if slack == nil {
		t.Fatal("expected a service named \"slack\" in pack.toml")
	}
	if slack.KindOrDefault() != "proxy_process" {
		t.Errorf("slack.kind = %q, want proxy_process", slack.KindOrDefault())
	}
	if slack.PublicationVisibilityOrDefault() != "private" {
		t.Errorf("slack publication.visibility = %q, want private", slack.PublicationVisibilityOrDefault())
	}
	if slack.Process.HealthPath != "/healthz" {
		t.Errorf("slack process.health_path = %q, want /healthz", slack.Process.HealthPath)
	}
	if len(slack.Process.Command) == 0 {
		t.Errorf("slack process.command is empty")
	}
}
