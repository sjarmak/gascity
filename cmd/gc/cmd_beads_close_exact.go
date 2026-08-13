package main

import (
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/spf13/cobra"
)

type beadsCloseExactOutcome string

const (
	beadsCloseExactClosed        beadsCloseExactOutcome = "closed"
	beadsCloseExactAlreadyClosed beadsCloseExactOutcome = "already_closed"
)

type beadsCloseExactRequest struct {
	beadID                string
	storeRef              string
	surface               string
	expectedTitle         string
	expectedStatus        string
	expectedMetadataKey   string
	expectedMetadataValue string
	format                string
	jsonOut               bool

	storeRefSet              bool
	surfaceSet               bool
	expectedTitleSet         bool
	expectedStatusSet        bool
	expectedMetadataKeySet   bool
	expectedMetadataValueSet bool
	formatSet                bool
}

type beadsCloseExactResult struct {
	SchemaVersion string                 `json:"schema_version"`
	OK            bool                   `json:"ok"`
	BeadID        string                 `json:"bead_id"`
	StoreRef      string                 `json:"store_ref"`
	Surface       string                 `json:"surface"`
	Outcome       beadsCloseExactOutcome `json:"outcome"`
}

func newBeadsCloseExactCmd(stdout, stderr io.Writer) *cobra.Command {
	var request beadsCloseExactRequest
	cmd := &cobra.Command{
		Use:   "close-exact <bead-id>",
		Short: "Revision-fence an identity-checked close in one exact local store",
		Long: `Close one bead in one explicitly selected local file store after checking
its title, status, and one metadata value. Both the scope and the file-provider
surface are explicit. The close is revision-fenced, so a
concurrent mutation is a hard failure rather than closing changed work. The
result is read back from the same store before success is reported.

The command never scans other stores, follows a cross-store fallback, or
operates on a remote city. A retry against an already-closed bead succeeds only
when the title and metadata identity still match.

Use --json for the canonical machine-output contract. --format=json remains
accepted for compatibility. Combining --json with an explicit --format=text is
a usage error.`,
		Example: `  gc beads close-exact gc-123 \
    --store-ref=city:demo \
    --surface=file \
    --expected-title=dr-wisp-abc \
    --expected-status=open \
    --expected-metadata-key=gc.routed_to \
    --expected-metadata-value=demo/receiver \
    --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request.beadID = args[0]
			request.storeRefSet = cmd.Flags().Changed("store-ref")
			request.surfaceSet = cmd.Flags().Changed("surface")
			request.expectedTitleSet = cmd.Flags().Changed("expected-title")
			request.expectedStatusSet = cmd.Flags().Changed("expected-status")
			request.expectedMetadataKeySet = cmd.Flags().Changed("expected-metadata-key")
			request.expectedMetadataValueSet = cmd.Flags().Changed("expected-metadata-value")
			request.formatSet = cmd.Flags().Changed("format")
			if err := resolveBeadsCloseExactOutputMode(&request); err != nil {
				fmt.Fprintf(stderr, "gc beads close-exact: %v\n", err) //nolint:errcheck
				return errExit
			}
			if err := validateBeadsCloseExactRequest(request); err != nil {
				fmt.Fprintf(stderr, "gc beads close-exact: %v\n", err) //nolint:errcheck
				return errExit
			}
			if cmdBeadsCloseExact(request, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&request.storeRef, "store-ref", "", "exact local store: city:<name> or rig:<name>")
	cmd.Flags().StringVar(&request.surface, "surface", "", "exact provider surface (file)")
	cmd.Flags().StringVar(&request.expectedTitle, "expected-title", "", "required exact title precondition")
	cmd.Flags().StringVar(&request.expectedStatus, "expected-status", "", "required pre-close status: open or in_progress")
	cmd.Flags().StringVar(&request.expectedMetadataKey, "expected-metadata-key", "", "required metadata identity key")
	cmd.Flags().StringVar(&request.expectedMetadataValue, "expected-metadata-value", "", "required metadata identity value (explicit empty is allowed)")
	cmd.Flags().StringVar(&request.format, "format", "text", "output format: text or json")
	cmd.Flags().BoolVar(&request.jsonOut, "json", false, "emit the canonical JSON result")
	return cmd
}

func resolveBeadsCloseExactOutputMode(request *beadsCloseExactRequest) error {
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

func validateBeadsCloseExactRequest(request beadsCloseExactRequest) error {
	switch {
	case !request.storeRefSet:
		return fmt.Errorf("--store-ref is required")
	case !request.surfaceSet:
		return fmt.Errorf("--surface is required")
	case !request.expectedTitleSet:
		return fmt.Errorf("--expected-title is required")
	case !request.expectedStatusSet:
		return fmt.Errorf("--expected-status is required")
	case !request.expectedMetadataKeySet:
		return fmt.Errorf("--expected-metadata-key is required")
	case !request.expectedMetadataValueSet:
		return fmt.Errorf("--expected-metadata-value is required (use --expected-metadata-value= for an empty value)")
	}
	if !validMetadataCASToken(request.beadID, metadataCASMaxBeadIDBytes) {
		return fmt.Errorf("invalid bead id %q", request.beadID)
	}
	if _, _, err := parseBeadsMetadataCASStoreRef(request.storeRef); err != nil {
		return err
	}
	if request.surface != "file" {
		return fmt.Errorf("--surface must be file")
	}
	if !utf8.ValidString(request.expectedTitle) || len(request.expectedTitle) > metadataCASMaxValueBytes {
		return fmt.Errorf("--expected-title must be valid UTF-8 and at most %d bytes", metadataCASMaxValueBytes)
	}
	if request.expectedStatus != "open" && request.expectedStatus != "in_progress" {
		return fmt.Errorf("--expected-status must be open or in_progress")
	}
	if !validMetadataCASToken(request.expectedMetadataKey, metadataCASMaxKeyBytes) {
		return fmt.Errorf("invalid metadata key %q", request.expectedMetadataKey)
	}
	if !utf8.ValidString(request.expectedMetadataValue) || len(request.expectedMetadataValue) > metadataCASMaxValueBytes {
		return fmt.Errorf("--expected-metadata-value must be valid UTF-8 and at most %d bytes", metadataCASMaxValueBytes)
	}
	if request.format != "text" && request.format != "json" {
		return fmt.Errorf("invalid --format %q: expected text or json", request.format)
	}
	return nil
}

var (
	openBeadsCloseExactStore = func(scopeRoot, _ string) (beads.Store, error) {
		return openExistingScopeLocalFileStore(scopeRoot)
	}
	closeBeadsCloseExactStore = closeBeadStoreHandle
)

func cmdBeadsCloseExact(request beadsCloseExactRequest, stdout, stderr io.Writer) int {
	ctx, err := resolveContext()
	if err != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: %v\n", err) //nolint:errcheck
		return 1
	}
	cfg, err := loadCityConfig(ctx.CityPath, configWarnWriter(request.format == "json", stderr))
	if err != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: loading city config: %v\n", err) //nolint:errcheck
		return 1
	}
	scopeRoot, canonicalRef, err := resolveBeadsMetadataCASStore(cfg, ctx.CityPath, request.storeRef)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: %v\n", err) //nolint:errcheck
		return 1
	}
	request.storeRef = canonicalRef
	store, err := openBeadsCloseExactStore(scopeRoot, ctx.CityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: opening %s: %v\n", canonicalRef, err) //nolint:errcheck
		return 1
	}
	result, applyErr := applyBeadsCloseExact(store, request)
	closeErr := closeBeadsCloseExactStore(store)
	if applyErr != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: %v\n", applyErr) //nolint:errcheck
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: closing %s after write: %v\n", canonicalRef, closeErr) //nolint:errcheck
		return 1
	}
	return renderBeadsCloseExact(result, request.format, stdout, stderr)
}

func applyBeadsCloseExact(store beads.Store, request beadsCloseExactRequest) (beadsCloseExactResult, error) {
	before, err := store.Get(request.beadID)
	if err != nil {
		return beadsCloseExactResult{}, fmt.Errorf("read %s before close: %w", request.beadID, err)
	}
	if before.Title != request.expectedTitle {
		return beadsCloseExactResult{}, fmt.Errorf("title precondition failed for %s", request.beadID)
	}
	metadataValue, metadataPresent := before.Metadata[request.expectedMetadataKey]
	if !metadataPresent || metadataValue != request.expectedMetadataValue {
		return beadsCloseExactResult{}, fmt.Errorf("metadata precondition failed for %s key %s", request.beadID, request.expectedMetadataKey)
	}
	result := beadsCloseExactResult{
		SchemaVersion: "1",
		OK:            true,
		BeadID:        request.beadID,
		StoreRef:      request.storeRef,
		Surface:       request.surface,
	}
	if before.Status == "closed" {
		result.Outcome = beadsCloseExactAlreadyClosed
		return result, nil
	}
	if before.Status != request.expectedStatus {
		return beadsCloseExactResult{}, fmt.Errorf("status precondition failed for %s: got %q, want %q", request.beadID, before.Status, request.expectedStatus)
	}
	writer, ok := beads.ConditionalWriterFor(store)
	if !ok {
		return beadsCloseExactResult{}, fmt.Errorf("exact store %T does not support revision-fenced close", store)
	}
	if err := writer.CloseIfMatch(before.ID, before.Revision); err != nil {
		return beadsCloseExactResult{}, fmt.Errorf("revision-fenced close %s: %w", request.beadID, err)
	}
	after, err := store.Get(request.beadID)
	if err != nil {
		return beadsCloseExactResult{}, fmt.Errorf("read %s after close: %w", request.beadID, err)
	}
	if after.Status != "closed" || after.Title != before.Title ||
		after.Metadata[request.expectedMetadataKey] != request.expectedMetadataValue {
		return beadsCloseExactResult{}, fmt.Errorf("authoritative readback did not preserve identity and closed status for %s", request.beadID)
	}
	result.Outcome = beadsCloseExactClosed
	return result, nil
}

func renderBeadsCloseExact(result beadsCloseExactResult, format string, stdout, stderr io.Writer) int {
	if format == "json" {
		return writeCLIJSONLineOrExit(stdout, stderr, "gc beads close-exact", result)
	}
	if _, err := fmt.Fprintf(stdout, "bead=%s store=%s surface=%s outcome=%s\n", result.BeadID, result.StoreRef, result.Surface, result.Outcome); err != nil {
		fmt.Fprintf(stderr, "gc beads close-exact: writing result: %v\n", err) //nolint:errcheck
		return 1
	}
	return 0
}
