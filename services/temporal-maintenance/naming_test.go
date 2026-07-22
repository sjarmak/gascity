package temporalmaintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fireEDT is a nominal fire time carrying a non-UTC zone and sub-second
// precision, so exact-match assertions prove both normalization rules
// (UTC conversion, second truncation) at once. 15:30:00.123-04:00 EDT ==
// 19:30:00 UTC.
var fireEDT = time.Date(2026, 7, 21, 15, 30, 0, 123456789, time.FixedZone("EDT", -4*3600))

// TestFormatScheduledTime pins the single time-formatting rule: UTC, second
// granularity, ISO 8601 basic with a trailing Z, and deterministic across
// zone representations of the same instant.
func TestFormatScheduledTime(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"utc already", time.Date(2026, 7, 21, 19, 30, 0, 0, time.UTC), "20260721T193000Z"},
		{"non-utc converts", fireEDT, "20260721T193000Z"},
		{"sub-second drops", time.Date(2026, 1, 2, 3, 4, 5, 999999999, time.UTC), "20260102T030405Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, FormatScheduledTime(tt.in))
		})
	}
}

// TestOrderRunKey covers the order/{scoped}/run/{time}/{op} constructor: exact
// output including the time rule, and every validation rejection.
func TestOrderRunKey(t *testing.T) {
	tests := []struct {
		name    string
		scoped  string
		at      time.Time
		op      string
		want    string
		wantErr string
	}{
		{
			name: "city-level order", scoped: "dolt-health", at: fireEDT, op: "sling-selection",
			want: "order/dolt-health/run/20260721T193000Z/sling-selection",
		},
		{
			name: "rig-scoped order name is one segment", scoped: "dolt-health:rig:demo-repo", at: fireEDT, op: "sling-selection",
			want: "order/dolt-health:rig:demo-repo/run/20260721T193000Z/sling-selection",
		},
		{name: "empty scoped order", scoped: "", at: fireEDT, op: "op", wantErr: "scoped order name must not be empty"},
		{name: "slash in scoped order", scoped: "a/b", at: fireEDT, op: "op", wantErr: "must not contain '/'"},
		{name: "zero time", scoped: "dolt-health", at: time.Time{}, op: "op", wantErr: "zero time"},
		{name: "empty operation", scoped: "dolt-health", at: fireEDT, op: "", wantErr: "operation must not be empty"},
		{name: "slash in operation", scoped: "dolt-health", at: fireEDT, op: "gh/merge", wantErr: "must not contain '/'"},
		{name: "whitespace in operation", scoped: "dolt-health", at: fireEDT, op: "gh merge", wantErr: "whitespace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OrderRunKey(tt.scoped, tt.at, tt.op)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestOrderRunKey_DistinctFiresDistinctKeys proves two fires of the same
// order never dedup into each other — the failure the legacy cycleKey
// derivation exists to prevent.
func TestOrderRunKey_DistinctFiresDistinctKeys(t *testing.T) {
	k1, err := OrderRunKey("dolt-health", fireEDT, "sling-selection")
	require.NoError(t, err)
	k2, err := OrderRunKey("dolt-health", fireEDT.Add(2*time.Hour), "sling-selection")
	require.NoError(t, err)
	require.NotEqual(t, k1, k2)
}

// TestMoleculeKey covers molecule/{rootBead}/{op}: exact output and
// validation rejections.
func TestMoleculeKey(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		op      string
		want    string
		wantErr string
	}{
		{name: "typical", root: "gc-4zf.3", op: "dispatch", want: "molecule/gc-4zf.3/dispatch"},
		{name: "empty root", root: "", op: "dispatch", wantErr: "root bead id must not be empty"},
		{name: "slash in root", root: "gc/4zf", op: "dispatch", wantErr: "must not contain '/'"},
		{name: "empty operation", root: "gc-4zf.3", op: "", wantErr: "operation must not be empty"},
		{name: "control char in root", root: "gc-\x004zf", op: "dispatch", wantErr: "control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MoleculeKey(tt.root, tt.op)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestWorkflowIDFor covers the gc/{cityID}/{kind}/... constructor: exact
// output per kind, arity enforcement, unknown kinds, and segment validation.
func TestWorkflowIDFor(t *testing.T) {
	tests := []struct {
		name    string
		city    string
		kind    WorkflowKind
		ids     []string
		want    string
		wantErr string
	}{
		{name: "bead", city: "ds-research", kind: KindBead, ids: []string{"gc-4zf.6"}, want: "gc/ds-research/bead/gc-4zf.6"},
		{name: "molecule", city: "ds-research", kind: KindMolecule, ids: []string{"gc-4zf.3"}, want: "gc/ds-research/molecule/gc-4zf.3"},
		{
			name: "order run", city: "ds-research", kind: KindOrder, ids: []string{"dolt-health:rig:demo-repo", "20260721T193000Z"},
			want: "gc/ds-research/order/dolt-health:rig:demo-repo/20260721T193000Z",
		},
		{name: "empty city", city: "", kind: KindBead, ids: []string{"gc-1"}, wantErr: "city id must not be empty"},
		{name: "slash in city", city: "a/b", kind: KindBead, ids: []string{"gc-1"}, wantErr: "must not contain '/'"},
		{name: "unknown kind", city: "ds-research", kind: WorkflowKind("cron"), ids: []string{"x"}, wantErr: `unknown workflow kind "cron"`},
		{name: "bead arity high", city: "ds-research", kind: KindBead, ids: []string{"gc-1", "extra"}, wantErr: "takes 1 id segment(s), got 2"},
		{name: "bead arity zero", city: "ds-research", kind: KindBead, ids: nil, wantErr: "takes 1 id segment(s), got 0"},
		{name: "order arity low", city: "ds-research", kind: KindOrder, ids: []string{"dolt-health"}, wantErr: "takes 2 id segment(s), got 1"},
		{name: "empty id segment", city: "ds-research", kind: KindBead, ids: []string{""}, wantErr: "id segment must not be empty"},
		{name: "slash in id segment", city: "ds-research", kind: KindOrder, ids: []string{"dolt-health", "a/b"}, wantErr: "must not contain '/'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WorkflowIDFor(tt.city, tt.kind, tt.ids...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestWorkflowIDWrappers proves each fixed-arity wrapper is exactly its
// WorkflowIDFor spelling.
func TestWorkflowIDWrappers(t *testing.T) {
	viaBead, err := BeadWorkflowID("ds-research", "gc-4zf.6")
	require.NoError(t, err)
	require.Equal(t, "gc/ds-research/bead/gc-4zf.6", viaBead)

	viaMol, err := MoleculeWorkflowID("ds-research", "gc-4zf.3")
	require.NoError(t, err)
	require.Equal(t, "gc/ds-research/molecule/gc-4zf.3", viaMol)

	viaOrder, err := OrderRunWorkflowID("ds-research", "dolt-health", FormatScheduledTime(fireEDT))
	require.NoError(t, err)
	require.Equal(t, "gc/ds-research/order/dolt-health/20260721T193000Z", viaOrder)

	_, err = OrderRunWorkflowID("ds-research", "dolt-health", "")
	require.ErrorContains(t, err, "id segment must not be empty")
}

// TestLegacyFormatsUnchanged pins the LIVE mid-soak formats. The running
// Schedule and the persisted KeyStore records are keyed on these exact
// strings; per the gc-4zf.6 migration rule they must not drift until the
// post-soak migration change. If this test fails, someone touched a legacy
// key producer — stop and re-read docs/design/durable-naming-convention.md.
func TestLegacyFormatsUnchanged(t *testing.T) {
	require.Equal(t, "gascity-maintenance/gastownhall-gascity/c1",
		WorkflowID("gastownhall-gascity", "c1"))
	require.Equal(t, "temporal-shadow/gastownhall-gascity/c1/review/sling-selection/gc-x",
		idempotencyKey("gastownhall-gascity", "c1", "review", "sling-selection", "gc-x"))
}
