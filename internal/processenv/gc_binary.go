package processenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveGCBinary returns the executable that should be used when gc launches
// another gc process. An explicit GC_BIN is authoritative, but it must name a
// real absolute executable; a malformed override is never replaced with PATH
// lookup or os.Executable. With no override, os.Executable is used and is
// subject to the same ordinary-file check so sealed/ephemeral identities do
// not leak into persistent child launchers.
func ResolveGCBinary() (string, error) {
	if explicit, configured := os.LookupEnv("GC_BIN"); configured {
		// Empty is the established unset sentinel used by test and provider
		// environment overlays. Any non-empty value, including whitespace-only,
		// is an explicit override and must fail closed rather than falling back.
		if explicit != "" {
			return validateGCBinary("GC_BIN", explicit)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve os.Executable: %w", err)
	}
	return validateGCBinary("os.Executable", executable)
}

func validateGCBinary(source, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s is empty", source)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", source, path)
	}
	if ephemeralGCBinaryPath(path) {
		return "", fmt.Errorf("%s %q is an ephemeral process identity", source, path)
	}
	if pathHasEphemeralSymlink(path) {
		return "", fmt.Errorf("%s %q resolves through an ephemeral process identity", source, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", source, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %q is not a regular file", source, path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s %q is not executable", source, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", source, path, err)
	}
	if ephemeralGCBinaryPath(resolved) {
		return "", fmt.Errorf("%s %q resolves to ephemeral process identity %q", source, path, resolved)
	}
	return path, nil
}

func ephemeralGCBinaryPath(path string) bool {
	path = filepath.Clean(path)
	return path == "/proc" || strings.HasPrefix(path, "/proc/") ||
		path == "/dev/fd" || strings.HasPrefix(path, "/dev/fd/") ||
		path == "/dev/stdin" || path == "/dev/stdout" || path == "/dev/stderr" ||
		strings.HasPrefix(path, "/memfd:")
}

func pathHasEphemeralSymlink(path string) bool {
	for current := filepath.Clean(path); current != filepath.Dir(current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(current)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		if ephemeralGCBinaryPath(target) {
			return true
		}
		if symlinkChainHasEphemeralTarget(current, target) {
			return true
		}
	}
	return false
}

func symlinkChainHasEphemeralTarget(link, target string) bool {
	seen := map[string]bool{filepath.Clean(link): true}
	for depth := 0; depth < 32; depth++ {
		current := filepath.Clean(target)
		if ephemeralGCBinaryPath(current) {
			return true
		}
		if seen[current] {
			return false
		}
		seen[current] = true
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return false
		}
		next, err := os.Readlink(current)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(current), next)
		}
		target = next
	}
	return true
}
