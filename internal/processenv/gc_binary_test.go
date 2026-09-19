package processenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveGCBinaryUsesExplicitAbsoluteExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc")
	if err := os.WriteFile(path, []byte("gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_BIN", path)

	got, err := ResolveGCBinary()
	if err != nil {
		t.Fatalf("ResolveGCBinary() error = %v", err)
	}
	if got != path {
		t.Fatalf("ResolveGCBinary() = %q, want %q", got, path)
	}
}

func TestResolveGCBinaryRejectsInvalidExplicitOverride(t *testing.T) {
	for name, value := range map[string]string{
		"relative":   "gc",
		"missing":    filepath.Join(t.TempDir(), "missing-gc"),
		"directory":  t.TempDir(),
		"whitespace": "   ",
		"proc":       "/proc/self/exe",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("GC_BIN", value)
			if got, err := ResolveGCBinary(); err == nil || got != "" {
				t.Fatalf("ResolveGCBinary() = (%q, %v), want an error and empty path", got, err)
			}
		})
	}
}

func TestResolveGCBinaryRejectsSymlinkToEphemeralIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc")
	if err := os.Symlink("/proc/self/exe", path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("GC_BIN", path)
	if got, err := ResolveGCBinary(); err == nil || got != "" {
		t.Fatalf("ResolveGCBinary() = (%q, %v), want an error and empty path", got, err)
	}
}

func TestResolveGCBinaryRejectsPathThroughEphemeralDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/proc/self", filepath.Join(root, "proc")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("GC_BIN", filepath.Join(root, "proc", "exe"))
	if got, err := ResolveGCBinary(); err == nil || got != "" {
		t.Fatalf("ResolveGCBinary() = (%q, %v), want an error and empty path", got, err)
	}
}

func TestResolveGCBinaryRejectsNestedEphemeralDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/proc/self", filepath.Join(root, "alias1")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink("alias1", filepath.Join(root, "alias2")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("GC_BIN", filepath.Join(root, "alias2", "exe"))
	if got, err := ResolveGCBinary(); err == nil || got != "" {
		t.Fatalf("ResolveGCBinary() = (%q, %v), want an error and empty path", got, err)
	}
}

func TestResolveGCBinaryFallsBackWhenGCBinEmpty(t *testing.T) {
	t.Setenv("GC_BIN", "")
	got, err := ResolveGCBinary()
	if err != nil {
		t.Fatalf("ResolveGCBinary() error = %v", err)
	}
	if got == "" || !filepath.IsAbs(got) || strings.Contains(got, ":") && strings.HasPrefix(got, "/memfd:") {
		t.Fatalf("ResolveGCBinary() = %q, want an ordinary absolute executable", got)
	}
}
