package beadmeta

// EpicType is the bead Type ("issue_type") value bd assigns to a step
// promoted to epic status because it has children (see
// internal/formula/compile.go's flattenSteps, and coordclass.ClassWork,
// which places epics in the work class alongside tasks and convoys).
//
// It lives in beadmeta rather than internal/beads because internal/beads
// transitively imports internal/rollout, which imports internal/config
// (beads -> rollout -> config): a package that both internal/beads and
// internal/config need to import (to build the generated shell/jq query
// strings and the Go-side exclusion predicate from a single source) must sit
// below both, and beadmeta already does since it has zero internal imports.
const EpicType = "epic"
