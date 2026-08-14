package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestStableNudgeWireCommitBeforeResponseLossRetriesSameEffect(t *testing.T) {
	dir := t.TempDir()
	effectFile := filepath.Join(dir, "effects")
	receiptFile := filepath.Join(dir, "receipt")
	lookupFile := filepath.Join(dir, "lookup")
	requestFile := filepath.Join(dir, "request")
	effectID := "mail-nudge-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	content := runtime.TextContent("1 actionable mail delivery; run gc mail inbox")
	want, err := runtime.NewStableNudgeReceipt(effectID, "session-a", content, "exec-ledger:effect-1", time.Date(2026, 8, 14, 1, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewStableNudgeReceipt: %v", err)
	}
	wire, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	lookupWire, err := json.Marshal(runtime.StableNudgeLookup{
		Version: 1, State: runtime.StableNudgeLookupCommitted,
		Receipt: want, ObservedAt: time.Date(2026, 8, 14, 1, 5, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Marshal lookup: %v", err)
	}
	script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
  protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
  nudge-stable)
    cat > %q
    if test ! -f %q; then
      printf '%%s\n' "$3" >> %q
      printf '%%s' %q > %q
      printf '%%s' %q > %q
      exit 1
    fi
    cat %q
    ;;
  nudge-stable-status) cat %q ;;
  *) exit 2 ;;
esac
`, requestFile, receiptFile, effectFile, string(wire), receiptFile, string(lookupWire), lookupFile, receiptFile, lookupFile))
	p := NewSeamBacked(script)
	stable, ok := p.(runtime.StableNudgeProvider)
	if !ok {
		t.Fatalf("NewSeamBacked result %T does not implement StableNudgeProvider", p)
	}

	if _, err := stable.NudgeStable(context.Background(), "session-a", effectID, content); !errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
		t.Fatalf("first NudgeStable error = %v, want ErrStableNudgeRetrySafe", err)
	}
	lookup, err := stable.LookupStableNudge(context.Background(), "session-a", effectID, content)
	if err != nil || lookup.State != runtime.StableNudgeLookupCommitted || lookup.Receipt != want {
		t.Fatalf("LookupStableNudge = %#v, %v", lookup, err)
	}
	got, err := stable.NudgeStable(context.Background(), "session-a", effectID, content)
	if err != nil {
		t.Fatalf("retry NudgeStable: %v", err)
	}
	if got != want {
		t.Fatalf("receipt = %#v, want %#v", got, want)
	}
	effects, err := os.ReadFile(effectFile)
	if err != nil {
		t.Fatalf("ReadFile(effects): %v", err)
	}
	if got := strings.Count(strings.TrimSpace(string(effects)), effectID); got != 1 {
		t.Fatalf("effect count = %d, effects=%q", got, effects)
	}
	request, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatalf("ReadFile(request): %v", err)
	}
	if !strings.Contains(string(request), effectID) || strings.Contains(string(request), "subject") || strings.Contains(string(request), "body") {
		t.Fatalf("request = %s", request)
	}
}

func TestStableNudgeWireLookupReturnsExplicitUnknown(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, `
case "$1" in
 protocol) printf '%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable-status) printf '%s' '{"version":1,"state":"unknown_external_state","observed_at":"2026-08-14T01:07:00Z"}' ;;
 *) exit 2 ;;
esac
`)
	p := NewProvider(script)
	lookup, err := p.LookupStableNudge(context.Background(), "session-a",
		"mail-nudge-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", runtime.TextContent("notice"))
	if err != nil || lookup.State != runtime.StableNudgeLookupUnknownExternalState || lookup.Receipt != (runtime.StableNudgeReceipt{}) {
		t.Fatalf("LookupStableNudge = %#v, %v", lookup, err)
	}
}

func TestStableNudgeWireNaiveNegativeControlDuplicatesAfterResponseLoss(t *testing.T) {
	dir := t.TempDir()
	effectFile := filepath.Join(dir, "effects")
	countFile := filepath.Join(dir, "count")
	effectID := "mail-nudge-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	content := runtime.TextContent("1 actionable mail delivery; run gc mail inbox")
	want, err := runtime.NewStableNudgeReceipt(effectID, "session-a", content, "naive:effect", time.Date(2026, 8, 14, 1, 6, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(want)
	script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
 protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable)
   printf '%%s\n' "$3" >> %q
   if test ! -f %q; then : > %q; exit 1; fi
   printf '%%s' '%s'
   ;;
 *) exit 2 ;;
esac
`, effectFile, countFile, countFile, string(wire)))
	p := NewProvider(script)
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, content); !errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
		t.Fatalf("first = %v", err)
	}
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, content); err != nil {
		t.Fatalf("retry = %v", err)
	}
	effects, _ := os.ReadFile(effectFile)
	if got := strings.Count(strings.TrimSpace(string(effects)), effectID); got != 2 {
		t.Fatalf("naive physical effects = %d, want 2", got)
	}
}

func TestStableNudgeWireRefusesUndeclaredCapabilityBeforeOperation(t *testing.T) {
	dir := t.TempDir()
	called := filepath.Join(dir, "called")
	script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
  protocol) printf '%%s' '{"version":0}' ;;
  nudge-stable) : > %q ;;
  *) exit 2 ;;
esac
`, called))
	p := NewProvider(script)
	_, err := p.NudgeStable(context.Background(), "session-a",
		"mail-nudge-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", runtime.TextContent("notice"))
	if !errors.Is(err, runtime.ErrStableNudgeUnsupported) {
		t.Fatalf("NudgeStable error = %v, want ErrStableNudgeUnsupported", err)
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nudge-stable operation ran without capability: %v", err)
	}
}

func TestStableNudgeWireClassifiesDeclaredButUnsupportedOperation(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, `
case "$1" in
 protocol) printf '%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 *) exit 2 ;;
esac
`)
	p := NewProvider(script)
	effectID := "mail-nudge-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, runtime.TextContent("notice")); !errors.Is(err, runtime.ErrStableNudgeUnsupported) || errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
		t.Fatalf("NudgeStable error = %v, want unsupported only", err)
	}
	if _, err := p.LookupStableNudge(context.Background(), "session-a", effectID, runtime.TextContent("notice")); !errors.Is(err, runtime.ErrStableNudgeUnsupported) {
		t.Fatalf("LookupStableNudge error = %v, want unsupported", err)
	}
}

func TestStableNudgeWireMapsTypedDestinationConflictWithoutRetry(t *testing.T) {
	dir := t.TempDir()
	effectID := "mail-nudge-9999999999999999999999999999999999999999999999999999999999999999"
	content := runtime.TextContent("current content")
	contentHash, err := runtime.StableNudgeContentSHA256(content)
	if err != nil {
		t.Fatal(err)
	}
	conflictWire, err := json.Marshal(runtime.StableNudgeConflict{
		Version: 1, State: runtime.StableNudgeConflictState,
		EffectID: effectID, RequestedContentSHA256: contentHash,
		ExistingContentSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
 protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable) cat >/dev/null; printf '%%s' '%s' ;;
 *) exit 2 ;;
esac
`, string(conflictWire)))
	p := NewProvider(script)
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, content); !errors.Is(err, runtime.ErrStableNudgeConflict) || errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
		t.Fatalf("NudgeStable error = %v, want conflict only", err)
	}
}

func TestStableNudgeWireRejectsMalformedTypedConflictFailClosed(t *testing.T) {
	dir := t.TempDir()
	effectID := "mail-nudge-8888888888888888888888888888888888888888888888888888888888888888"
	script := writeScript(t, dir, `
case "$1" in
 protocol) printf '%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable) cat >/dev/null; printf '%s' '{"version":1,"state":"conflict","effect_id":"wrong","requested_content_sha256":"bad","existing_content_sha256":"bad"}' ;;
 *) exit 2 ;;
esac
`)
	p := NewProvider(script)
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, runtime.TextContent("notice")); err == nil || errors.Is(err, runtime.ErrStableNudgeRetrySafe) || errors.Is(err, runtime.ErrStableNudgeConflict) {
		t.Fatalf("NudgeStable error = %v, want fail-closed malformed output", err)
	}
}

func TestStableNudgeWireClassifiesMalformedReceiptApartFromIdentityConflict(t *testing.T) {
	dir := t.TempDir()
	effectID := "mail-nudge-" + strings.Repeat("7", 64)
	script := writeScript(t, dir, `
case "$1" in
 protocol) printf '%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable) cat >/dev/null; printf '%s' '{"version":1,"effect_id":"bad","target_runtime_name":"Session-A"}' ;;
 *) exit 2 ;;
esac
`)
	p := NewProvider(script)
	if _, err := p.NudgeStable(context.Background(), "session-a", effectID, runtime.TextContent("notice")); err == nil || errors.Is(err, runtime.ErrStableNudgeConflict) || errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
		t.Fatalf("malformed receipt error = %v, want fail-closed non-conflict", err)
	}
}

func TestStableNudgeLookupClassifiesMalformedShapeApartFromValidConflict(t *testing.T) {
	effectID := "mail-nudge-" + strings.Repeat("6", 64)
	content := runtime.TextContent("notice")
	acceptedAt := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	receipt, err := runtime.NewStableNudgeReceipt(effectID, "session-b", content, "receipt:one", acceptedAt)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		lookup       runtime.StableNudgeLookup
		wantConflict bool
	}{
		{
			name: "valid identity mismatch", wantConflict: true,
			lookup: runtime.StableNudgeLookup{Version: 1, State: runtime.StableNudgeLookupCommitted, Receipt: receipt, ObservedAt: acceptedAt.Add(time.Minute)},
		},
		{
			name: "malformed receipt",
			lookup: func() runtime.StableNudgeLookup {
				bad := receipt
				bad.TargetRuntimeName = "Session-B"
				return runtime.StableNudgeLookup{Version: 1, State: runtime.StableNudgeLookupCommitted, Receipt: bad, ObservedAt: acceptedAt.Add(time.Minute)}
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			wire, err := json.Marshal(tc.lookup)
			if err != nil {
				t.Fatal(err)
			}
			script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
 protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable-status) cat >/dev/null; printf '%%s' '%s' ;;
 *) exit 2 ;;
esac
`, wire))
			_, gotErr := NewProvider(script).LookupStableNudge(context.Background(), "session-a", effectID, content)
			if gotErr == nil || errors.Is(gotErr, runtime.ErrStableNudgeConflict) != tc.wantConflict || errors.Is(gotErr, runtime.ErrStableNudgeRetrySafe) {
				t.Fatalf("lookup error = %v, want conflict=%v and never retry-safe", gotErr, tc.wantConflict)
			}
		})
	}
}

func TestStableNudgeLookupRejectsMalformedJSONAndTrailingData(t *testing.T) {
	effectID := "mail-nudge-" + strings.Repeat("5", 64)
	for name, wire := range map[string]string{
		"malformed": `{"version":`,
		"trailing":  `{"version":1,"state":"unknown_external_state","observed_at":"2026-08-14T01:00:00Z"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
 protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable-status) cat >/dev/null; printf '%%s' '%s' ;;
 *) exit 2 ;;
esac
`, wire))
			if _, err := NewProvider(script).LookupStableNudge(context.Background(), "session-a", effectID, runtime.TextContent("notice")); err == nil || errors.Is(err, runtime.ErrStableNudgeConflict) || errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
				t.Fatalf("malformed lookup error = %v", err)
			}
		})
	}
}

func TestStableNudgeWireRejectsMalformedEffectIDBeforeOperation(t *testing.T) {
	dir := t.TempDir()
	called := filepath.Join(dir, "called")
	script := writeScript(t, dir, fmt.Sprintf(`
case "$1" in
 protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
 nudge-stable) : > %q ;;
 *) exit 2 ;;
esac
`, called))
	p := NewProvider(script)
	if _, err := p.NudgeStable(context.Background(), "session-a", "bad", runtime.TextContent("notice")); err == nil {
		t.Fatal("malformed stable effect ID accepted")
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination invoked for malformed ID: %v", err)
	}
}
