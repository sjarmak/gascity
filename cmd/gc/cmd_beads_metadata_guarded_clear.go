package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/spf13/cobra"
)

// beadsMetadataGuardedClearOutcome is the durable classification of one
// guarded-clear attempt. It intentionally mirrors the two outcomes
// gc-worktree-finalize parses ("cleared" / "skipped"); every other result is
// a non-zero exit, never a third outcome string.
type beadsMetadataGuardedClearOutcome string

const (
	beadsMetadataGuardedClearCleared beadsMetadataGuardedClearOutcome = "cleared"
	beadsMetadataGuardedClearSkipped beadsMetadataGuardedClearOutcome = "skipped"
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
	SchemaVersion string                           `json:"schema_version"`
	OK            bool                             `json:"ok"`
	BeadID        string                           `json:"bead_id"`
	StoreRef      string                           `json:"store_ref"`
	GuardKey      string                           `json:"guard_key"`
	Outcome       beadsMetadataGuardedClearOutcome `json:"outcome"`
	KeysWritten   []string                         `json:"keys_written,omitempty"`
}

func newBeadsMetadataGuardedClearCmd(stdout, stderr io.Writer) *cobra.Command {
	var request beadsMetadataGuardedClearRequest
	cmd := &cobra.Command{
		Use:   "metadata-guarded-clear <bead-id>",
		Short: "Clear a set of metadata keys gated on a guard key, in an exact local store",
		Long: `Clear a set of metadata keys in one exact local bead store, gated on a
guard key matching an expected value.

The store must be selected explicitly with --store-ref=city:<name> or
--store-ref=rig:<name>. This command never scans other stores, follows a
cross-store fallback, or operates on a remote city.

Before every individual key write, the guard key is re-checked with a
single-key compare-and-set against --guard-expected. If the guard no longer
matches — because a fresh attempt re-provisioned the bead — the command stops
and reports outcome=skipped without partially applying remaining writes; keys
already cleared before the mismatch was observed stay cleared, which is safe
because the operation is idempotent. This narrows, but does not eliminate,
the race window: true multi-key atomicity needs a revision fence the beads
v1.1.0 schema does not have (upstream beads#4697). A conflict on a --clear-key
or --set-metadata key itself (as opposed to the guard) is a genuine write
race and is reported as a hard, non-zero-exit failure.

Use --json for the canonical machine-output contract. --format=json remains
accepted for compatibility. Combining --json with an explicit --format=text is
a usage error.`,
		Example: `  gc beads metadata-guarded-clear wt-123 \
    --store-ref=rig:tributary \
    --guard-key=gc.worktree_attempt_id \
    --guard-expected=att-9f2 \
    --clear-key=gc.work_dir \
    --clear-key=gc.work_branch \
    --set-metadata="gc.worktree_disposition=removed" \
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
	cmd.Flags().StringVar(&request.guardKey, "guard-key", "", "metadata key that must match --guard-expected before any write")
	cmd.Flags().StringVar(&request.guardExpected, "guard-expected", "", "expected current value of --guard-key (explicit empty is allowed)")
	cmd.Flags().StringArrayVar(&request.clearKeys, "clear-key", nil, "metadata key to clear (repeatable)")
	cmd.Flags().StringArrayVar(&request.setMetadata, "set-metadata", nil, "key=value metadata pair to set after clearing (repeatable)")
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
	case len(request.clearKeys) == 0 && len(request.setMetadata) == 0:
		return fmt.Errorf("at least one --clear-key or --set-metadata is required")
	}
	if !validMetadataCASToken(request.beadID, metadataCASMaxBeadIDBytes) {
		return fmt.Errorf("invalid bead id %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
			request.beadID, metadataCASMaxBeadIDBytes)
	}
	if !validMetadataCASToken(request.guardKey, metadataCASMaxKeyBytes) {
		return fmt.Errorf("invalid guard key %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
			request.guardKey, metadataCASMaxKeyBytes)
	}
	if _, _, err := parseBeadsMetadataCASStoreRef(request.storeRef); err != nil {
		return err
	}
	if !utf8.ValidString(request.guardExpected) {
		return fmt.Errorf("--guard-expected must be valid UTF-8")
	}
	if len(request.guardExpected) > metadataCASMaxValueBytes {
		return fmt.Errorf("--guard-expected exceeds %d bytes", metadataCASMaxValueBytes)
	}
	seen := make(map[string]bool, len(request.clearKeys))
	for _, key := range request.clearKeys {
		if !validMetadataCASToken(key, metadataCASMaxKeyBytes) {
			return fmt.Errorf("invalid --clear-key %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
				key, metadataCASMaxKeyBytes)
		}
		if key == request.guardKey {
			return fmt.Errorf("--clear-key %q must not equal --guard-key: the guard is never a write target", key)
		}
		if seen[key] {
			return fmt.Errorf("duplicate --clear-key %q", key)
		}
		seen[key] = true
	}
	for _, pair := range request.setMetadata {
		key, value, err := parseBeadsMetadataGuardedClearSetPair(pair)
		if err != nil {
			return err
		}
		if key == request.guardKey {
			return fmt.Errorf("--set-metadata key %q must not equal --guard-key: the guard is never a write target", key)
		}
		if seen[key] {
			return fmt.Errorf("key %q supplied by both --clear-key and --set-metadata", key)
		}
		seen[key] = true
		if !utf8.ValidString(value) {
			return fmt.Errorf("--set-metadata %q: value must be valid UTF-8", key)
		}
		if len(value) > metadataCASMaxValueBytes {
			return fmt.Errorf("--set-metadata %q: value exceeds %d bytes", key, metadataCASMaxValueBytes)
		}
	}
	if request.format != "text" && request.format != "json" {
		return fmt.Errorf("invalid --format %q: expected text or json", request.format)
	}
	return nil
}

func parseBeadsMetadataGuardedClearSetPair(pair string) (key, value string, err error) {
	key, value, ok := strings.Cut(pair, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid --set-metadata %q: expected key=value", pair)
	}
	if !validMetadataCASToken(key, metadataCASMaxKeyBytes) {
		return "", "", fmt.Errorf("invalid --set-metadata key %q: must be 1-%d ASCII letters, digits, dot, underscore, or hyphen and start with a letter or digit",
			key, metadataCASMaxKeyBytes)
	}
	return key, value, nil
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
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: closing %s after clear: %v\n", canonicalRef, closeErr) //nolint:errcheck // best-effort stderr
		return 1
	}
	return renderBeadsMetadataGuardedClear(result, request.format, stdout, stderr)
}

// applyBeadsMetadataGuardedClear performs the guarded clear as a sequence of
// single-key compare-and-set operations. Each key write is preceded by a
// fresh self-CAS check on the guard key: expected and next are both
// guard-expected, so the call only ever confirms or refuses, never mutates
// the guard. A guard mismatch at any point stops the sequence and reports
// "skipped" (ok, not an error — the bead was re-provisioned by a fresh
// attempt); keys already written stay written, which is safe because every
// individual write is idempotent. A conflict on a clear/set key itself (not
// the guard) is a genuine write race on that key and is returned as an error.
func applyBeadsMetadataGuardedClear(store beads.Store, request beadsMetadataGuardedClearRequest) (beadsMetadataGuardedClearResult, error) {
	bead, err := store.Get(request.beadID)
	if err != nil {
		return beadsMetadataGuardedClearResult{}, fmt.Errorf("reading %q before guarded clear: %w", request.beadID, err)
	}
	if bead.Metadata[request.guardKey] != request.guardExpected {
		return beadsMetadataGuardedClearResult{
			SchemaVersion: "1",
			OK:            true,
			BeadID:        request.beadID,
			StoreRef:      request.storeRef,
			GuardKey:      request.guardKey,
			Outcome:       beadsMetadataGuardedClearSkipped,
		}, nil
	}

	type write struct {
		key      string
		expected string
		next     string
	}
	writes := make([]write, 0, len(request.clearKeys)+len(request.setMetadata))
	for _, key := range request.clearKeys {
		writes = append(writes, write{key: key, expected: bead.Metadata[key], next: ""})
	}
	for _, pair := range request.setMetadata {
		key, value, _ := parseBeadsMetadataGuardedClearSetPair(pair)
		writes = append(writes, write{key: key, expected: bead.Metadata[key], next: value})
	}

	written := make([]string, 0, len(writes))
	for _, w := range writes {
		guardOutcome, err := beads.ApplyMetadataCAS(store, request.beadID, request.guardKey, request.guardExpected, request.guardExpected)
		if err != nil {
			return beadsMetadataGuardedClearResult{}, fmt.Errorf("re-checking guard %q before writing %q: %w", request.guardKey, w.key, err)
		}
		if guardOutcome != beads.MetadataCASSwapped {
			// Swapped is the only outcome that proves the guard held for this
			// write's entire compare window. AlreadyNext means the self-CAS
			// observed a mismatch and only the readback found the guard back
			// at guard-expected (an ABA race); conflict means it still
			// mismatches. Both stop the sequence exactly like a persistent
			// mismatch.
			return beadsMetadataGuardedClearResult{
				SchemaVersion: "1",
				OK:            true,
				BeadID:        request.beadID,
				StoreRef:      request.storeRef,
				GuardKey:      request.guardKey,
				Outcome:       beadsMetadataGuardedClearSkipped,
				KeysWritten:   written,
			}, nil
		}

		keyOutcome, err := beads.ApplyMetadataCAS(store, request.beadID, w.key, w.expected, w.next)
		if err != nil {
			return beadsMetadataGuardedClearResult{}, fmt.Errorf("writing %q: %w", w.key, err)
		}
		if keyOutcome == beads.MetadataCASConflict {
			return beadsMetadataGuardedClearResult{}, fmt.Errorf("writing %q: %w", w.key, errBeadsMetadataGuardedClearKeyConflict)
		}
		written = append(written, w.key)
	}

	sort.Strings(written)
	return beadsMetadataGuardedClearResult{
		SchemaVersion: "1",
		OK:            true,
		BeadID:        request.beadID,
		StoreRef:      request.storeRef,
		GuardKey:      request.guardKey,
		Outcome:       beadsMetadataGuardedClearCleared,
		KeysWritten:   written,
	}, nil
}

var errBeadsMetadataGuardedClearKeyConflict = fmt.Errorf("concurrent write raced this key outside the guard")

func renderBeadsMetadataGuardedClear(result beadsMetadataGuardedClearResult, format string, stdout, stderr io.Writer) int {
	if format == "json" {
		return writeCLIJSONLineOrExit(stdout, stderr, "gc beads metadata-guarded-clear", result)
	}
	if _, err := fmt.Fprintf(stdout, "bead=%s store=%s guard_key=%s outcome=%s keys_written=%s\n",
		result.BeadID, result.StoreRef, result.GuardKey, result.Outcome, strings.Join(result.KeysWritten, ",")); err != nil {
		fmt.Fprintf(stderr, "gc beads metadata-guarded-clear: writing result: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	return 0
}
