package session

import (
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TransportMetadataKey is the canonical session bead key for transport identity.
const TransportMetadataKey = "transport"

// ResolveEffectiveTransport combines the already-resolved per-session
// transport with the runtime selection that hosts its default path. An
// explicit transport wins; otherwise the runtime's bundled transport is the
// authoritative answer.
func ResolveEffectiveTransport(runtimeName, resolvedTransport string) string {
	if transport := strings.TrimSpace(resolvedTransport); transport != "" {
		// A resolved transport may actually be a runtime-selection name — a
		// per-agent `session = "t3bridge"` reaches here verbatim through
		// config.ResolveSessionCreateTransport's default branch. Canonicalize it
		// to its transport carrier so classifiers matching "t3"/"acp"/"tmux" are
		// not defeated by "t3bridge" or an exec:.../gc-session-t3 alias.
		return runtime.CanonicalTransport(transport)
	}
	return runtime.TransportForRuntimeName(runtimeName)
}

// persistedTransport returns the explicit value to record in a session bead.
//
// normalizeTransport answers what the supplied metadata proves and correctly
// returns empty for unknown legacy input. Persistence has a different contract:
// new beads must be self-describing, so an unresolved default records tmux.
func persistedTransport(provider, transport string) string {
	if normalized := normalizeTransport(provider, transport); normalized != "" {
		return normalized
	}
	return config.SessionTransportTmux
}

// persistedTransportForCreate extends persistedTransport with the ACP evidence
// available on a fresh-create path. MCP servers are materialized only for ACP
// transport, so their presence proves the session is ACP even when neither the
// explicit transport nor the template/provider resolved one. Without this, an
// ACP session created without a config-resolvable template/provider (ad-hoc API
// callers, createBeadOnly) would be stamped tmux by the unresolved default,
// outranking the ACP-artifact fallback with no reconcile recovery — reconcile
// never revisits an unconfigured session.
func persistedTransportForCreate(provider, transport string, acpEvidence bool) string {
	if normalized := normalizeTransport(provider, transport); normalized != "" {
		return normalized
	}
	if acpEvidence {
		return config.SessionTransportACP
	}
	return config.SessionTransportTmux
}

// ReconcileTransportMetadataPatch returns the session-owned metadata mutation
// needed to project an authoritative transport onto an existing bead. ACP-only
// artifacts are cleared whenever the resolved transport is not ACP so legacy
// fallback classifiers cannot later re-arm stale routing evidence.
func ReconcileTransportMetadataPatch(existing map[string]string, transport string) MetadataPatch {
	transport = persistedTransport("", transport)
	patch := MetadataPatch{}
	if strings.TrimSpace(existing[TransportMetadataKey]) != transport {
		patch[TransportMetadataKey] = transport
	}
	if transport == config.SessionTransportACP {
		return patch
	}
	if strings.TrimSpace(existing[MCPIdentityMetadataKey]) != "" {
		patch[MCPIdentityMetadataKey] = ""
	}
	if strings.TrimSpace(existing[MCPServersSnapshotMetadataKey]) != "" {
		patch[MCPServersSnapshotMetadataKey] = ""
	}
	return patch
}
