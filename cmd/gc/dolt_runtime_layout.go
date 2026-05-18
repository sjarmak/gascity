package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/citylayout"
)

type managedDoltRuntimeLayout struct {
	PackStateDir string
	DataDir      string
	LogFile      string
	StateFile    string
	PIDFile      string
	// LockFile is the shell-side serialization lock owned by
	// `gc-beads-bd.sh`'s op_start (`exec 9>"$LOCK_FILE"`; `flock -n 9`).
	// It is broadcast to the script via the GC_DOLT_LOCK_FILE env var. The
	// gc binary itself MUST NOT flock this path — see LifecycleLockFile.
	LockFile string
	// LifecycleLockFile is the in-process gc lifecycle lock added in
	// gastownhall/gascity#2130 to serialize concurrent `gc dolt start`
	// invocations regardless of how they were launched. It must be a
	// distinct path from LockFile because the script's op_start holds an
	// exclusive flock on LockFile when it shells out to
	// `gc dolt-state start-managed`; if gc also locked LockFile, the
	// subprocess would deadlock-wait against its own parent script.
	LifecycleLockFile string
	ConfigFile        string
}

func resolveManagedDoltRuntimeLayout(cityPath string) (managedDoltRuntimeLayout, error) {
	cityPath = filepath.Clean(strings.TrimSpace(cityPath))
	if cityPath == "" || cityPath == "." {
		return managedDoltRuntimeLayout{}, fmt.Errorf("missing --city")
	}
	cityPath = normalizePathForCompare(cityPath)

	packStateDir := strings.TrimSpace(os.Getenv("GC_PACK_STATE_DIR"))
	if packStateDir == "" {
		if runtimeDir := strings.TrimSpace(os.Getenv("GC_CITY_RUNTIME_DIR")); runtimeDir != "" {
			packStateDir = filepath.Join(runtimeDir, "packs", "dolt")
		} else {
			packStateDir = citylayout.PackStateDir(cityPath, "dolt")
		}
	}
	dataDir := defaultEnvPath("GC_DOLT_DATA_DIR", filepath.Join(cityPath, ".beads", "dolt"))
	logFile := defaultEnvPath("GC_DOLT_LOG_FILE", filepath.Join(packStateDir, "dolt.log"))
	stateFile := defaultEnvPath("GC_DOLT_STATE_FILE", filepath.Join(packStateDir, "dolt-provider-state.json"))
	pidFile := defaultEnvPath("GC_DOLT_PID_FILE", filepath.Join(packStateDir, "dolt.pid"))
	lockFile := defaultEnvPath("GC_DOLT_LOCK_FILE", filepath.Join(packStateDir, "dolt.lock"))
	lifecycleLockFile := defaultEnvPath("GC_DOLT_LIFECYCLE_LOCK_FILE", filepath.Join(packStateDir, "dolt-gc-lifecycle.lock"))
	configFile := defaultEnvPath("GC_DOLT_CONFIG_FILE", filepath.Join(packStateDir, "dolt-config.yaml"))

	return managedDoltRuntimeLayout{
		PackStateDir:      packStateDir,
		DataDir:           dataDir,
		LogFile:           logFile,
		StateFile:         stateFile,
		PIDFile:           pidFile,
		LockFile:          lockFile,
		LifecycleLockFile: lifecycleLockFile,
		ConfigFile:        configFile,
	}, nil
}

func defaultEnvPath(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return normalizePathForCompare(value)
	}
	return normalizePathForCompare(fallback)
}

func doltRuntimeLayoutFields(layout managedDoltRuntimeLayout) []string {
	return []string{
		"GC_PACK_STATE_DIR\t" + layout.PackStateDir,
		"GC_DOLT_DATA_DIR\t" + layout.DataDir,
		"GC_DOLT_LOG_FILE\t" + layout.LogFile,
		"GC_DOLT_STATE_FILE\t" + layout.StateFile,
		"GC_DOLT_PID_FILE\t" + layout.PIDFile,
		"GC_DOLT_LOCK_FILE\t" + layout.LockFile,
		"GC_DOLT_CONFIG_FILE\t" + layout.ConfigFile,
	}
}
