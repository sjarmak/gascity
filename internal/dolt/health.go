// Package dolt — health.go provides a lightweight data-plane health report.
//
// gc dolt health runs this on every deacon patrol cycle. It checks server
// status, per-database stats, backup freshness, orphan databases, and zombie
// Dolt processes. Output is structured JSON for agent consumption.
package dolt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HealthReport is the machine-readable output of gc dolt health --json.
type HealthReport struct {
	Timestamp string           `json:"timestamp"`
	Server    ServerHealth     `json:"server"`
	Databases []DatabaseHealth `json:"databases,omitempty"`
	Backups   BackupHealth     `json:"backups"`
	Orphans   []OrphanDB       `json:"orphans,omitempty"`
	Processes ProcessHealth    `json:"processes"`
}

// ServerHealth reports Dolt server status.
type ServerHealth struct {
	Running   bool  `json:"running"`
	PID       int   `json:"pid,omitempty"`
	Port      int   `json:"port,omitempty"`
	LatencyMs int64 `json:"latency_ms,omitempty"`
}

// DatabaseHealth reports per-database stats.
type DatabaseHealth struct {
	Name      string `json:"name"`
	Commits   int    `json:"commits"`
	OpenBeads int    `json:"open_beads"`
}

// BackupHealth reports backup freshness.
type BackupHealth struct {
	DoltFreshness string `json:"dolt_freshness,omitempty"`
	DoltAgeSec    int    `json:"dolt_age_seconds,omitempty"`
	DoltStale     bool   `json:"dolt_stale"`
}

// OrphanDB is a database not referenced by any rig.
type OrphanDB struct {
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

// ProcessHealth reports zombie Dolt server processes.
type ProcessHealth struct {
	ZombieCount int   `json:"zombie_count"`
	ZombiePIDs  []int `json:"zombie_pids,omitempty"`
}

// RunHealthCheck collects a lightweight data-plane health report.
func RunHealthCheck(cityPath string) *HealthReport {
	report := &HealthReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	report.Server = checkServer(cityPath)
	if report.Server.Running {
		report.Databases = checkDatabases(cityPath)
	}
	report.Backups = checkBackups(cityPath)
	report.Orphans = checkOrphans(cityPath)
	report.Processes = checkZombieProcesses(cityPath)

	return report
}

// checkServer checks Dolt server status and latency.
func checkServer(cityPath string) ServerHealth {
	sh := ServerHealth{}

	running, pid, err := IsRunningCity(cityPath)
	if err != nil || !running {
		return sh
	}

	sh.Running = true
	sh.PID = pid

	config := GasCityConfig(cityPath)
	sh.Port = config.Port

	// Measure query latency.
	start := time.Now()
	if err := HealthCheckQuery(cityPath); err == nil {
		sh.LatencyMs = time.Since(start).Milliseconds()
	}

	return sh
}

// checkDatabases queries per-database stats: commit count and open bead count.
func checkDatabases(cityPath string) []DatabaseHealth {
	databases, err := ListDatabasesCity(cityPath)
	if err != nil {
		return nil
	}

	config := GasCityConfig(cityPath)
	var results []DatabaseHealth

	for _, dbName := range databases {
		dh := DatabaseHealth{Name: dbName}

		// Commit count — triggers compactor if too high.
		ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := buildDoltSQLCmd(ctx1, config, "-q",
			fmt.Sprintf("SELECT COUNT(*) FROM `%s`.dolt_log", dbName),
			"-r", "csv")
		if out, err := cmd.Output(); err == nil {
			dh.Commits = parseCSVInt(out)
		}
		cancel1()

		// Open bead count — separate context so it gets its own timeout budget.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		cmd = buildDoltSQLCmd(ctx2, config, "-q",
			fmt.Sprintf("SELECT COUNT(*) FROM `%s`.issues WHERE status IN ('open','in_progress')", dbName),
			"-r", "csv")
		if out, err := cmd.Output(); err == nil {
			dh.OpenBeads = parseCSVInt(out)
		}
		cancel2()

		results = append(results, dh)
	}

	return results
}

// checkBackups checks Dolt filesystem backup freshness.
func checkBackups(cityPath string) BackupHealth {
	bh := BackupHealth{}

	backupDir := filepath.Join(cityPath, ".gc", "dolt-backup")
	info, err := os.Stat(backupDir)
	if err != nil {
		// Try migration-backup dirs as fallback.
		backups, err := FindBackups(cityPath)
		if err != nil || len(backups) == 0 {
			return bh
		}
		// Use newest backup timestamp.
		info, err = os.Stat(backups[0].Path)
		if err != nil {
			return bh
		}
	}

	age := time.Since(info.ModTime())
	bh.DoltAgeSec = int(age.Seconds())
	bh.DoltFreshness = age.Round(time.Second).String()
	bh.DoltStale = age > 30*time.Minute

	return bh
}

// checkOrphans finds databases not referenced by any rig.
func checkOrphans(cityPath string) []OrphanDB {
	orphans, err := FindOrphanedDatabasesCity(cityPath)
	if err != nil {
		return nil
	}

	var results []OrphanDB
	for _, o := range orphans {
		results = append(results, OrphanDB{
			Name: o.Name,
			Size: formatBytes(o.SizeBytes),
		})
	}
	return results
}

// checkZombieProcesses finds Dolt server processes not on the expected port.
func checkZombieProcesses(cityPath string) ProcessHealth {
	ph := ProcessHealth{}

	config := GasCityConfig(cityPath)
	expectedPort := config.Port

	// Find all dolt sql-server processes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pgrep", "-f", "dolt sql-server")
	out, err := cmd.Output()
	if err != nil {
		return ph // No dolt processes found.
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid == 0 {
			continue
		}

		// Check if this PID is our expected server.
		dataDir := doltProcessDataDir(pid)
		if dataDir != "" && dataDir == filepath.Join(cityPath, ".gc", "dolt-data") {
			continue // This is our server.
		}

		// Check if it's listening on our expected port.
		portCmd := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN",
			"-p", strconv.Itoa(pid))
		portOut, err := portCmd.Output()
		if err == nil && strings.Contains(string(portOut), fmt.Sprintf(":%d", expectedPort)) {
			continue // On expected port.
		}

		ph.ZombiePIDs = append(ph.ZombiePIDs, pid)
	}

	ph.ZombieCount = len(ph.ZombiePIDs)
	return ph
}

// parseCSVInt extracts the first integer from dolt csv output.
// Expected format: "header\nvalue\n"
func parseCSVInt(out []byte) int {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(lines[len(lines)-1]))
	return n
}

// formatBytes is defined in doltserver.go.
