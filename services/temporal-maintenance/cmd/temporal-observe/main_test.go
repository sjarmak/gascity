package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tm "github.com/sjarmak/gas-city/services/temporal-maintenance"

	"github.com/stretchr/testify/require"
)

// readLines splits the metrics file into its newline-framed records.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(data), "\n"), "every append must leave a newline-terminated tail")
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestAppendRecord_SchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	require.NoError(t, appendRecord(path, tm.ObserveRecord{WindowHours: 72}, tm.DurableConfig{Mode: tm.DurableModeObserve}))

	lines := readLines(t, path)
	require.Len(t, lines, 1)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &decoded))
	require.Equal(t, float64(observeSchemaVersion), decoded["schema"],
		"gate-time consumers key on the schema field to tell layouts apart")
	require.Equal(t, "observe", decoded["durable_mode"])
}

// A torn previous append (ENOSPC, interrupt) leaves the file ending mid-line.
// The next append must isolate that fragment as one invalid line instead of
// fusing it with the new record — one record lost, not two.
func TestAppendRecord_TornTailSelfHeals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"timestamp":"2026-07-`), 0o644))

	require.NoError(t, appendRecord(path, tm.ObserveRecord{WindowHours: 72}, tm.DurableConfig{Mode: tm.DurableModeObserve}))

	lines := readLines(t, path)
	require.Len(t, lines, 2)
	require.Equal(t, `{"timestamp":"2026-07-`, lines[0], "the fragment stays alone on its own (invalid) line")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &decoded), "the fresh record must be intact JSON")
	require.Equal(t, float64(72), decoded["window_hours"])
}

// A cleanly-terminated file must NOT get an extra blank line prepended — the
// heal only fires on a torn tail.
func TestAppendRecord_CleanTailNoBlankLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	require.NoError(t, appendRecord(path, tm.ObserveRecord{}, tm.DurableConfig{Mode: tm.DurableModeObserve}))
	require.NoError(t, appendRecord(path, tm.ObserveRecord{}, tm.DurableConfig{Mode: tm.DurableModeObserve}))

	lines := readLines(t, path)
	require.Len(t, lines, 2)
	for i, line := range lines {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &decoded), "line %d must be valid JSON, not blank", i)
	}
}
