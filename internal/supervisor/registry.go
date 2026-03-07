// Package supervisor provides the machine-wide supervisor registry and
// configuration. The registry tracks which cities are managed by the
// supervisor; the config controls the supervisor's own behavior (API
// port, patrol interval, etc.).
package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/BurntSushi/toml"
)

// CityEntry is one registered city in the supervisor registry.
type CityEntry struct {
	Path string `toml:"path"` // absolute path to city root directory
}

// Name returns the city name derived from the directory basename.
// If a city.toml exists with workspace.name, the caller should use
// that instead; this is a fallback for when the config isn't loaded.
func (e CityEntry) Name() string {
	return filepath.Base(e.Path)
}

// registryFile is the TOML structure of ~/.gc/cities.toml.
type registryFile struct {
	Cities []CityEntry `toml:"cities"`
}

// Registry manages the set of registered cities. Thread-safe.
// Backed by a TOML file at the given path.
type Registry struct {
	mu   sync.RWMutex
	path string
}

// NewRegistry creates a Registry backed by the given file path.
// The file need not exist yet — it will be created on first write.
func NewRegistry(path string) *Registry {
	return &Registry{path: path}
}

// List returns all registered cities. Returns an empty slice (not nil)
// if the file doesn't exist or is empty.
func (r *Registry) List() ([]CityEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadLocked()
}

// Register adds a city to the registry. The path is resolved to an
// absolute path. Returns an error if the city is already registered
// (by path) or if a different city with the same directory basename
// is already registered. Uses file-level locking for cross-process safety.
func (r *Registry) Register(cityPath string) error {
	abs, err := filepath.Abs(cityPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	unlock, err := r.fileLock()
	if err != nil {
		return err
	}
	defer unlock()

	entries, err := r.loadLocked()
	if err != nil {
		return err
	}

	newName := filepath.Base(abs)
	for _, e := range entries {
		if e.Path == abs {
			return nil // already registered — idempotent
		}
		if filepath.Base(e.Path) == newName {
			return fmt.Errorf("city name %q already registered at %s (set a unique workspace.name)", newName, e.Path)
		}
	}

	entries = append(entries, CityEntry{Path: abs})
	return r.saveLocked(entries)
}

// Unregister removes a city from the registry by path. Returns an
// error if the city is not registered. The path is resolved to
// absolute before comparison. Uses file-level locking for cross-process safety.
func (r *Registry) Unregister(cityPath string) error {
	abs, err := filepath.Abs(cityPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	unlock, err := r.fileLock()
	if err != nil {
		return err
	}
	defer unlock()

	entries, err := r.loadLocked()
	if err != nil {
		return err
	}

	found := false
	filtered := entries[:0]
	for _, e := range entries {
		if e.Path == abs {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}
	if !found {
		return fmt.Errorf("city at %s is not registered", abs)
	}
	return r.saveLocked(filtered)
}

// loadLocked reads the registry file. Caller must hold at least r.mu.RLock.
func (r *Registry) loadLocked() ([]CityEntry, error) {
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	var rf registryFile
	if err := toml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parsing registry: %w", err)
	}
	return rf.Cities, nil
}

// fileLock acquires an exclusive flock on a sibling .lock file for
// cross-process safety during read-modify-write operations. Returns
// an unlock function. Caller must hold r.mu.Lock.
func (r *Registry) fileLock() (func(), error) {
	lockPath := r.path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening registry lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close() //nolint:errcheck
		return nil, fmt.Errorf("acquiring registry lock: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()                                   //nolint:errcheck
	}, nil
}

// saveLocked writes the registry file atomically. Caller must hold r.mu.Lock.
func (r *Registry) saveLocked(entries []CityEntry) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}
	rf := registryFile{Cities: entries}
	tmp := r.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating temp registry file: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(rf); err != nil {
		f.Close()      //nolint:errcheck // best-effort cleanup
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("encoding registry: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("closing temp registry file: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("renaming registry file: %w", err)
	}
	return nil
}
