package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/spf13/cobra"
)

type beadsMetadataGuardedClearRequest struct {
	beadID        string
	storeRef      string
	guardKey      string
	guardExpected string
	clearKeys     []string
	setMetadata   []string
	format        string
	jsonOut       bool

	storeRefSet      bool
	guardKeySet      bool
	guardExpectedSet bool
	formatSet        bool
}

type beadsMetadataGuardedClearResult struct {
	SchemaVersion string                    `json:"schema_version"`
	OK            bool                      `json:"ok"`
	BeadID        string                    `json:"bead_id"`
	StoreRef      string                    `json:"store_ref"`
	GuardKey      string                    `json:"guard_key"`
	Outcome       beads.GuardedClearOutcome `json:"outcome"`
	ClearedKeys   []string                  `json:"cleared_keys"`
	Terminal      map[string]string         `json:"terminal,omitempty"`
}

func newBeadsMetadataGuardedClearCmd(stdout, stderr io.Writer) *cobra.Command {
	var request beadsMetadataGuardedClearRequest
	cmd := &cobra.Command{
		Use:   "metadata-guarded-clear <bead-id>",
		Short: "Atomically clear several metadata keys in one local store, gated on a guard key",
		Long: `Atomically clear several metadata keys, and set a terminal set of
key/values, as one indivisible write in one exact local bead store — iff a
single guard key still holds an expected value at the instant of the write.

The store must be selected explicitly with --store-ref=city:<name> or
--store-ref=rig:<name>, exactly like "gc beads metadata-cas". This command
never scans other stores or falls back to a cross-store search.

A guard mismatch (the guard key's current value no longer equals
--guard-expected) is an ordinary zero-exit "skipped" outcome, not an error:
nothing was written. A store that cannot implement this capability (no real
mutual exclusion across the guard check and the clear) fails closed with a
non-zero exit and writes nothing — it never falls back to an unconditional
clear.

Use --json for the canonical machine-output contract. --format=json remains
accepted for compatibility. Combining --json with an explicit --format=text is
a usage error.`,
		Example: `  gc beads metadata-guarded-clear gc-123 \
    --store-ref=rig:tributary \
    --guard-key=gc.worktree_attempt_id \
    --guard-expected=att-7 \
    --clear-key=gc.work_dir \
    --clear-key=gc.work_branch \
    --set-metadata=gc.worktree_lifecycle=removed \
    --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request.beadID = args[0]
			request.storeRefSet = cmd.Flags().Changed("store-ref")
			request.guardKeySet = cmd.Flags().Changed("guard-key")
			request.guardExpectedSet = cmd.Flags().Changed("guard-expected")
			request.formatSet = cmd.Flags().Changed("format")
			if err := resolveBeadsMetadataGuardedClearOutputMode(&request); err != nil {
				fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			if err := validateBeadsMetadataGuardedClearRequest(request); err != nil {
				fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			if cmdBeadsMetadataGuardedClear(request, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&request.storeRef, "store-ref", "", "exact local store: city:<name> or rig:<name>")
	cmd.Flags().StringVar(&request.guardKey, "guard-key", "", "metadata key whose current value gates the clear")
	cmd.Flags().StringVar(&request.guardExpected, "guard-expected", "", "expected current value of --guard-key (explicit empty is allowed)")
	cmd.Flags().StringArrayVar(&request.clearKeys, "clear-key", nil, "metadata key to clear (repeatable)")
	cmd.Flags().StringArrayVar(&request.setMetadata, "set-metadata", nil, "terminal metadata to set on a successful clear (key=value, repeatable)")
	cmd.Flags().StringVar(&request.format, "format", "text", "output format: text or json")
	cmd.Flags().BoolVar(&request.jsonOut, "json", false, "emit the canonical JSON result")
	return cmd
}

func resolveBeadsMetadataGuardedClearOutputMode(request *beadsMetadataGuardedClearRequest) error {
	if request == nil || !request.jsonOut {
		return nil
	}
	if request.formatSet {
		switch request.format {
		case "json":
		case "text":
			return fmt.Errorf("--json cannot be combined with --format=text")
		default:
			return fmt.Errorf("invalid --format %q: expected text or json", request.format)
		}
	}
	request.format = "json"
	return nil
}

func validateBeadsMetadataGuardedClearRequest(request beadsMetadataGuardedClearRequest) error {
	switch {
	case !request.storeRefSet:
		return fmt.Errorf("--store-ref is required")
	case !request.guardKeySet:
		return fmt.Errorf("--guard-key is required")
	case !request.guardExpectedSet:
		return fmt.Errorf("--guard-expected is required (use --guard-expected= for an empty value)")
	}
	if !validMetadataCASToken(request.beadID, metadataCASMaxBeadIDBytes) {
		return fmt.Errorf("invalid bead id %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
			request.beadID, metadataCASMaxBeadIDBytes)
	}
	if !validMetadataCASToken(request.guardKey, metadataCASMaxKeyBytes) {
		return fmt.Errorf("invalid --guard-key %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
			request.guardKey, metadataCASMaxKeyBytes)
	}
	if _, _, err := parseBeadsMetadataCASStoreRef(request.storeRef); err != nil {
		return err
	}
	if !utf8ValidAndBounded(request.guardExpected, metadataCASMaxValueBytes) {
		return fmt.Errorf("--guard-expected must be valid UTF-8 and at most %d bytes", metadataCASMaxValueBytes)
	}
	if len(request.clearKeys) == 0 && len(request.setMetadata) == 0 {
		return fmt.Errorf("at least one --clear-key or --set-metadata is required")
	}
	seen := make(map[string]bool, len(request.clearKeys))
	for _, key := range request.clearKeys {
		if !validMetadataCASToken(key, metadataCASMaxKeyBytes) {
			return fmt.Errorf("invalid --clear-key %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
				key, metadataCASMaxKeyBytes)
		}
		if seen[key] {
			return fmt.Errorf("--clear-key %q repeated", key)
		}
		seen[key] = true
	}
	if _, err := parseBeadsMetadataGuardedClearTerminal(request.setMetadata); err != nil {
		return err
	}
	if request.format != "text" && request.format != "json" {
		return fmt.Errorf("invalid --format %q: expected text or json", request.format)
	}
	return nil
}

func utf8ValidAndBounded(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value)
}

// parseBeadsMetadataGuardedClearTerminal parses repeated --set-metadata
// key=value flags into the terminal map ApplyGuardedMetadataClear sets on a
// successful clear. A key may appear at most once.
func parseBeadsMetadataGuardedClearTerminal(setMetadata []string) (map[string]string, error) {
	if len(setMetadata) == 0 {
		return nil, nil
	}
	terminal := make(map[string]string, len(setMetadata))
	for _, entry := range setMetadata {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !validMetadataCASToken(key, metadataCASMaxKeyBytes) {
			return nil, fmt.Errorf("invalid --set-metadata %q: expected key=value with a valid metadata key", entry)
		}
		if !utf8ValidAndBounded(value, metadataCASMaxValueBytes) {
			return nil, fmt.Errorf("--set-metadata %q: value must be valid UTF-8 and at most %d bytes", entry, metadataCASMaxValueBytes)
		}
		if _, dup := terminal[key]; dup {
			return nil, fmt.Errorf("--set-metadata key %q repeated", key)
		}
		terminal[key] = value
	}
	return terminal, nil
}

var (
	openBeadsMetadataGuardedClearStore  = openAuthoritativeStoreAtForCity
	closeBeadsMetadataGuardedClearStore = closeBeadStoreHandle
)

func cmdBeadsMetadataGuardedClear(request beadsMetadataGuardedClearRequest, stdout, stderr io.Writer) int {
	ctx, err := resolveContext()
	if err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	cfg, err := loadCityConfig(ctx.CityPath, configWarnWriter(request.format == "json", stderr))
	if err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: loading city config: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	scopeRoot, canonicalRef, err := resolveBeadsMetadataCASStore(cfg, ctx.CityPath, request.storeRef)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	request.storeRef = canonicalRef

	store, err := openBeadsMetadataGuardedClearStore(scopeRoot, ctx.CityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: opening %s: %v\n", canonicalRef, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	result, err := applyBeadsMetadataGuardedClear(store, request)
	closeErr := closeBeadsMetadataGuardedClearStore(store)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: closing %s after guarded clear: %v\n", canonicalRef, closeErr) //nolint:errcheck // best-effort stderr
		return 1
	}
	return renderBeadsMetadataGuardedClear(result, request.format, stdout, stderr)
}

func applyBeadsMetadataGuardedClear(store beads.Store, request beadsMetadataGuardedClearRequest) (beadsMetadataGuardedClearResult, error) {
	terminal, err := parseBeadsMetadataGuardedClearTerminal(request.setMetadata)
	if err != nil {
		return beadsMetadataGuardedClearResult{}, err
	}
	outcome, err := beads.ApplyGuardedMetadataClear(store, request.beadID, request.guardKey, request.guardExpected, request.clearKeys, terminal)
	if err != nil {
		return beadsMetadataGuardedClearResult{}, err
	}
	return beadsMetadataGuardedClearResult{
		SchemaVersion: "1",
		OK:            true,
		BeadID:        request.beadID,
		StoreRef:      request.storeRef,
		GuardKey:      request.guardKey,
		Outcome:       outcome,
		ClearedKeys:   request.clearKeys,
		Terminal:      terminal,
	}, nil
}

func renderBeadsMetadataGuardedClear(result beadsMetadataGuardedClearResult, format string, stdout, stderr io.Writer) int {
	if format == "json" {
		return writeCLIJSONLineOrExit(stdout, stderr, "gc beads metadata-guarded-clear", result)
	}
	if _, err := fmt.Fprintf(stdout, "bead=%s store=%s guard_key=%s outcome=%s cleared_keys=%s\n",
		result.BeadID, result.StoreRef, result.GuardKey, result.Outcome, strings.Join(result.ClearedKeys, ",")); err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: writing result: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	return 0
}
