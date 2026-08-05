package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

var bdRecordMigrationNow = time.Now

func parseBdMigrateRecordArgs(args []string) (beads.RecordMigrationRequest, bool, error) {
	var req beads.RecordMigrationRequest
	if len(args) == 0 || args[0] != "migrate-record" {
		return req, false, nil
	}
	req.ExpectedMetadata = make(map[string]string)
	req.Metadata = make(map[string]string)
	clearMetadata := make(map[string]bool)
	expectedAbsentMetadata := make(map[string]bool)
	var idSet, actorSet, reasonSet, expectedAssigneeSet, assigneeSet bool

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			if idSet {
				return req, true, fmt.Errorf("migrate-record accepts exactly one record id")
			}
			req.ID = arg
			idSet = true
			continue
		}
		name, value, inline := strings.Cut(arg, "=")
		if !inline {
			if i+1 >= len(args) {
				return req, true, fmt.Errorf("%s requires a value", name)
			}
			i++
			value = args[i]
		}
		switch name {
		case "--actor":
			if actorSet {
				return req, true, fmt.Errorf("--actor may be specified only once")
			}
			req.Actor, actorSet = value, true
		case "--reason":
			if reasonSet {
				return req, true, fmt.Errorf("--reason may be specified only once")
			}
			req.Reason, reasonSet = value, true
		case "--expect-assignee":
			if expectedAssigneeSet {
				return req, true, fmt.Errorf("--expect-assignee may be specified only once")
			}
			req.ExpectedAssignee, expectedAssigneeSet = stringValuePointer(value), true
		case "--assignee":
			if assigneeSet {
				return req, true, fmt.Errorf("--assignee may be specified only once")
			}
			req.Assignee, assigneeSet = stringValuePointer(value), true
		case "--expect-metadata":
			key, metadataValue, err := parseBdMigrationMetadataPair(value)
			if err != nil {
				return req, true, fmt.Errorf("--expect-metadata: %w", err)
			}
			if _, duplicate := req.ExpectedMetadata[key]; duplicate || expectedAbsentMetadata[key] {
				return req, true, fmt.Errorf("--expect-metadata duplicates key %q", key)
			}
			req.ExpectedMetadata[key] = metadataValue
		case "--expect-metadata-absent":
			key := strings.TrimSpace(value)
			if key == "" || strings.Contains(key, "=") {
				return req, true, fmt.Errorf("--expect-metadata-absent requires a non-empty key")
			}
			if _, duplicate := req.ExpectedMetadata[key]; duplicate || expectedAbsentMetadata[key] {
				return req, true, fmt.Errorf("--expect-metadata-absent duplicates key %q", key)
			}
			expectedAbsentMetadata[key] = true
			req.ExpectedMetadataAbsent = append(req.ExpectedMetadataAbsent, key)
		case "--set-metadata":
			key, metadataValue, err := parseBdMigrationMetadataPair(value)
			if err != nil {
				return req, true, fmt.Errorf("--set-metadata: %w", err)
			}
			if _, duplicate := req.Metadata[key]; duplicate || clearMetadata[key] {
				return req, true, fmt.Errorf("metadata replacement duplicates key %q", key)
			}
			req.Metadata[key] = metadataValue
		case "--clear-metadata":
			key := strings.TrimSpace(value)
			if key == "" || strings.Contains(key, "=") {
				return req, true, fmt.Errorf("--clear-metadata requires a non-empty key")
			}
			if _, duplicate := req.Metadata[key]; duplicate || clearMetadata[key] {
				return req, true, fmt.Errorf("metadata replacement duplicates key %q", key)
			}
			clearMetadata[key] = true
			req.ClearMetadata = append(req.ClearMetadata, key)
		default:
			return req, true, fmt.Errorf("unknown migrate-record flag %s", name)
		}
	}

	if !idSet || strings.TrimSpace(req.ID) == "" {
		return req, true, fmt.Errorf("usage: gc bd migrate-record <id> --actor <actor> --reason <reason> [guarded changes]")
	}
	if !actorSet || strings.TrimSpace(req.Actor) == "" {
		return req, true, fmt.Errorf("--actor is required")
	}
	if !reasonSet || strings.TrimSpace(req.Reason) == "" {
		return req, true, fmt.Errorf("--reason is required")
	}
	if expectedAssigneeSet != assigneeSet {
		return req, true, fmt.Errorf("--expect-assignee and --assignee must be specified together")
	}
	for key := range req.Metadata {
		if _, ok := req.ExpectedMetadata[key]; !ok && !expectedAbsentMetadata[key] {
			return req, true, fmt.Errorf("metadata %q requires --expect-metadata %s=<old-value>", key, key)
		}
	}
	for key := range clearMetadata {
		if _, ok := req.ExpectedMetadata[key]; !ok {
			return req, true, fmt.Errorf("metadata %q requires --expect-metadata %s=<old-value>", key, key)
		}
	}
	for key := range req.ExpectedMetadata {
		_, set := req.Metadata[key]
		if !set && !clearMetadata[key] {
			return req, true, fmt.Errorf("expected metadata %q has no --set-metadata or --clear-metadata replacement", key)
		}
	}
	for key := range expectedAbsentMetadata {
		if _, set := req.Metadata[key]; !set {
			return req, true, fmt.Errorf("expected-absent metadata %q has no --set-metadata replacement", key)
		}
	}
	return req, true, nil
}

func stringValuePointer(value string) *string { return &value }

func parseBdMigrationMetadataPair(pair string) (string, string, error) {
	key, value, ok := strings.Cut(pair, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", fmt.Errorf("expected key=value, got %q", pair)
	}
	return key, value, nil
}

func doBdMigrateRecord(cityPath string, target execStoreTarget, req beads.RecordMigrationRequest, stdout, stderr io.Writer) int {
	provider := rawBeadsProviderForScope(target.ScopeRoot, cityPath)
	if provider != "file" {
		fmt.Fprintf(stderr, "gc bd migrate-record: requires a file-backed beads provider (resolved %q for %s)\n", provider, target.ScopeRoot) //nolint:errcheck
		return 1
	}
	store, err := openCompatibleFileStore(target.ScopeRoot, cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc bd migrate-record: opening store: %v\n", err) //nolint:errcheck
		return 1
	}
	if _, err := beads.MigrateFileRecord(store, req, bdRecordMigrationNow()); err != nil {
		fmt.Fprintf(stderr, "gc bd migrate-record: %v\n", err) //nolint:errcheck
		return 1
	}
	fmt.Fprintf(stdout, "migrated %s\n", req.ID) //nolint:errcheck
	return 0
}
