package runtime

import (
	"path/filepath"
	"strings"
)

// TransportForRuntimeName reports the transport bundled with a runtime
// selection. Runtime and transport are not fully composable yet, so the
// selection fixes the transport: ACP uses "acp", the native and legacy T3
// selections use "t3", and the remaining runtimes use the tmux carrier.
func TransportForRuntimeName(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "acp":
		return "acp"
	case name == "t3bridge":
		return "t3"
	case strings.HasPrefix(name, "exec:") &&
		filepath.Base(strings.TrimSpace(strings.TrimPrefix(name, "exec:"))) == "gc-session-t3":
		return "t3"
	default:
		return "tmux"
	}
}

// CanonicalTransport maps a resolved transport-or-runtime-selection value onto
// its transport carrier. A value that is already a carrier ("acp"/"tmux"/"t3")
// is returned unchanged; any other non-empty value is treated as a
// runtime-selection name and mapped through TransportForRuntimeName. This keeps
// a per-agent selection that reaches a transport slot verbatim (e.g.
// "t3bridge", or an "exec:.../gc-session-t3" alias) from persisting as a
// transport no classifier matches.
func CanonicalTransport(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "acp", "tmux", "t3":
		return value
	default:
		return TransportForRuntimeName(value)
	}
}
