package beadmeta

import "strings"

// IsOutcomeFailed reports whether a closed bead's metadata counts as failed,
// applying the same fail-closed default the workflow finalizer uses:
// gc.outcome=fail is always a failure, and a bead with gc.on_fail=abort_scope
// that closed without a recognized terminal gc.outcome (pass, skipped,
// canceled) defaults to failed rather than success. Retry-managed attempt
// beads are exempt — their contract violations are classified by retry-eval
// as transient retries, not scope aborts.
//
// Any renderer that shows a bead's completion status (CLI, dashboard) must
// call this instead of treating status=="closed" as success, or a failed
// step will display as passed while the finalizer treats it as failed.
func IsOutcomeFailed(metadata map[string]string) bool {
	outcome := strings.TrimSpace(metadata[OutcomeMetadataKey])
	if outcome == OutcomeFail {
		return true
	}
	if strings.TrimSpace(metadata[OnFailMetadataKey]) != "abort_scope" || isRetryAttemptMetadata(metadata) {
		return false
	}
	switch outcome {
	case OutcomePass, OutcomeSkipped, OutcomeCanceled:
		return false
	default:
		return true
	}
}

func isRetryAttemptMetadata(metadata map[string]string) bool {
	if metadata[LogicalBeadIDMetadataKey] == "" {
		return false
	}
	switch metadata[KindMetadataKey] {
	case "retry-run", "retry-eval":
		return true
	}
	return metadata[AttemptMetadataKey] != ""
}
