package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

// ── resolveBinding: two-tier name→pane resolution + running verdict ──────────
//
// herdr ≥0.7.4 clears an agent's *name* when its pane occupant changes, so
// name-keyed lookups can go dark on a live agent. resolveBinding keeps the
// name lookup as the fast path and falls back to the pane binding Start
// persisted in the sidecar, probed live before it is trusted (pane ids
// recycle). The running verdict is mode-aware: a registered agent
// (bindModeAgent) whose pane sits at a bare shell prompt past the launch
// grace has *exited* and is REAPED (pane closed, binding cleared) — under
// tmux the pane would have died with the process; a raw shell session
// (bindModeShell) is running as long as its pane exists, because
// `exec /bin/sh -c …` panes die with the command.

// resolveOpsRec records the side effects resolveBinding performed.
type resolveOpsRec struct {
	cleared bool
	reaped  string
}

func opsForRec(t *testing.T, agentHit bool, agentErr error, bound, mode string, probe paneProbe, probeErr error, rec *resolveOpsRec) paneLookupOps {
	t.Helper()
	return paneLookupOps{
		getAgent: func() (agentInfo, bool, error) {
			if agentErr != nil {
				return agentInfo{}, false, agentErr
			}
			if agentHit {
				return agentInfo{Name: "mayor", PaneID: "%5"}, true, nil
			}
			return agentInfo{}, false, nil
		},
		boundPane:    func() string { return bound },
		boundMode:    func() string { return mode },
		boundAge:     func() time.Duration { return time.Hour }, // long past any launch window
		probePane:    func(string) (paneProbe, error) { return probe, probeErr },
		reapPane:     func(paneID string) { rec.reaped = paneID },
		clearBinding: func() { rec.cleared = true },
	}
}

func opsFor(t *testing.T, agentHit bool, agentErr error, bound, mode string, probe paneProbe, probeErr error, cleared *bool) paneLookupOps {
	t.Helper()
	rec := &resolveOpsRec{}
	ops := opsForRec(t, agentHit, agentErr, bound, mode, probe, probeErr, rec)
	if cleared != nil {
		ops.clearBinding = func() { *cleared = true }
	}
	return ops
}

func TestResolveBindingNameHitWinsAndRuns(t *testing.T) {
	cleared := false
	ops := opsFor(t, true, nil, "", "", paneProbe{}, nil, &cleared)
	ops.boundPane = func() string { t.Fatal("bound pane must not be consulted on a name hit"); return "" }
	ops.probePane = func(string) (paneProbe, error) { t.Fatal("no probe on a name hit"); return paneProbe{}, nil }
	pane, running, err := resolveBinding(ops)
	if err != nil || pane != "%5" || !running {
		t.Fatalf("resolveBinding = %q, %v, %v; want %%5, true, nil", pane, running, err)
	}
	if cleared {
		t.Error("binding cleared on a name hit")
	}
}

// The 0.7.4 storm case: name cleared, bound pane busy running the agent.
func TestResolveBindingBusyPaneRunsRegardlessOfMode(t *testing.T) {
	for _, mode := range []string{bindModeAgent, bindModeShell, ""} {
		pane, running, err := resolveBinding(opsFor(t, false, nil, "%5", mode, paneProbe{Exists: true, Busy: true}, nil, nil))
		if err != nil || pane != "%5" || !running {
			t.Fatalf("mode %q: resolveBinding = %q, %v, %v; want %%5, true, nil", mode, pane, running, err)
		}
	}
}

// A registered agent's pane back at its bare shell prompt past the launch
// grace means the agent EXITED: under tmux the pane would have died with the
// process, so reap it — close the pane, clear the binding, resolve absent.
// Without this, every completed ephemeral wisp (unique tab label, no future
// Start to recycle it, no Stop because the session reads not-running) leaks
// one shell pane forever — the herdr echo of the witness sleep leak.
func TestResolveBindingReapsExitedAgentPane(t *testing.T) {
	rec := &resolveOpsRec{}
	pane, running, err := resolveBinding(opsForRec(t, false, nil, "%5", bindModeAgent, paneProbe{Exists: true, Busy: false}, nil, rec))
	if err != nil || pane != "" || running {
		t.Fatalf("resolveBinding = %q, %v, %v; want absent (exited agent reaped)", pane, running, err)
	}
	if rec.reaped != "%5" {
		t.Errorf("exited agent pane not reaped (reaped=%q)", rec.reaped)
	}
	if !rec.cleared {
		t.Error("exited agent binding not cleared")
	}
}

// Inside the launch grace window the same pane state means "shell ready,
// agent still being launched": the pane must resolve untouched — a reap here
// would close the pane out from under the in-flight Start that provisionally
// bound it.
func TestResolveBindingSparesFreshBindingAtPrompt(t *testing.T) {
	rec := &resolveOpsRec{}
	ops := opsForRec(t, false, nil, "%5", bindModeAgent, paneProbe{Exists: true, Busy: false}, nil, rec)
	ops.boundAge = func() time.Duration { return 5 * time.Second }
	pane, running, err := resolveBinding(ops)
	if err != nil || pane != "%5" || running {
		t.Fatalf("resolveBinding = %q, %v, %v; want %%5, false, nil (mid-launch pane spared)", pane, running, err)
	}
	if rec.reaped != "" || rec.cleared {
		t.Error("mid-launch pane was reaped/cleared")
	}
}

// A bare-shell session (empty command) is its own shell: running while the
// pane exists even with nothing in the foreground.
func TestResolveBindingShellModeExistsIsRunning(t *testing.T) {
	pane, running, err := resolveBinding(opsFor(t, false, nil, "%5", bindModeShell, paneProbe{Exists: true, Busy: false}, nil, nil))
	if err != nil || pane != "%5" || !running {
		t.Fatalf("resolveBinding = %q, %v, %v; want %%5, true, nil", pane, running, err)
	}
}

// A pane herdr confirms gone is a stale binding: absent, not running, cleared.
func TestResolveBindingClearsConfirmedGonePane(t *testing.T) {
	cleared := false
	pane, running, err := resolveBinding(opsFor(t, false, nil, "%5", bindModeAgent, paneProbe{}, nil, &cleared))
	if err != nil || pane != "" || running {
		t.Fatalf("resolveBinding = %q, %v, %v; want absent", pane, running, err)
	}
	if !cleared {
		t.Error("confirmed-gone binding was not cleared")
	}
}

// A transport failure probing the pane proves nothing: surface the error,
// keep the binding — a socket blip must not erase the handle to a live agent.
func TestResolveBindingProbeTransportErrorKeepsBinding(t *testing.T) {
	cleared := false
	blip := errors.New("dial unix: connection refused")
	pane, running, err := resolveBinding(opsFor(t, false, nil, "%5", bindModeShell, paneProbe{}, blip, &cleared))
	if !errors.Is(err, blip) || pane != "" || running {
		t.Fatalf("resolveBinding = %q, %v, %v; want the probe error", pane, running, err)
	}
	if cleared {
		t.Error("binding cleared on a transport error")
	}
}

// No binding and no live name: genuinely absent.
func TestResolveBindingAbsentWithoutBinding(t *testing.T) {
	ops := opsFor(t, false, nil, "", "", paneProbe{}, nil, nil)
	ops.probePane = func(string) (paneProbe, error) { t.Fatal("no binding, no probe"); return paneProbe{}, nil }
	pane, running, err := resolveBinding(ops)
	if err != nil || pane != "" || running {
		t.Fatalf("resolveBinding = %q, %v, %v; want absent", pane, running, err)
	}
}

// ── paneProbeFrom: the busy verdict ──────────────────────────────────────────

func TestPaneProbeFrom(t *testing.T) {
	tests := []struct {
		name     string
		shellPID int
		fg       []proc
		want     paneProbe
	}{
		{"gone", 0, nil, paneProbe{}},
		{"bare prompt (root shell only)", 100, []proc{{PID: 100, Name: "zsh"}}, paneProbe{Exists: true, ShellPID: 100}},
		{"bare prompt, login-shell name", 100, []proc{{PID: 100, Name: "-zsh"}}, paneProbe{Exists: true, ShellPID: 100}},
		{"empty foreground", 100, nil, paneProbe{Exists: true, ShellPID: 100}},
		{"foreground child (launched agent)", 100, []proc{{PID: 101, Name: "claude"}}, paneProbe{Exists: true, Busy: true, ShellPID: 100}},
		{"exec'd command replaced the shell", 100, []proc{{PID: 100, Name: "sleep"}}, paneProbe{Exists: true, Busy: true, ShellPID: 100}},
		{"sh -c wrapper with child", 100, []proc{{PID: 101, Name: "sleep"}, {PID: 100, Name: "bash"}}, paneProbe{Exists: true, Busy: true, ShellPID: 100}},
	}
	for _, tt := range tests {
		if got := paneProbeFrom(tt.shellPID, tt.fg); got != tt.want {
			t.Errorf("%s: paneProbeFrom = %+v; want %+v", tt.name, got, tt.want)
		}
	}
}

// paneRunsCommand recognizes the launched `/bin/sh -c <raw>` in a pane's
// foreground — the signal that the typed launch actually executed (a fresh
// pane's shell-init children read as Busy, so Busy alone cannot tell "our
// command is running" from "zsh is still sourcing rc files").
func TestPaneRunsCommand(t *testing.T) {
	raw := `for i in $(seq 1 60); do echo "tick $i"; sleep 1; done`
	wrapper := proc{PID: 100, Name: "bash", Argv: []string{"/bin/sh", "-c", raw}}
	if !paneRunsCommand([]proc{{PID: 101, Name: "sleep"}, wrapper}, raw) {
		t.Error("wrapper present: want true")
	}
	init := []proc{{PID: 100, Name: "zsh", Argv: []string{"-zsh"}}, {PID: 102, Name: "sw_vers", Argv: []string{"/usr/bin/sw_vers"}}}
	if paneRunsCommand(init, raw) {
		t.Error("shell-init foreground must not read as launched")
	}
	if paneRunsCommand(nil, raw) {
		t.Error("empty foreground must not read as launched")
	}
}

// paneRootReplaced spots a launch whose command exec'd straight through the
// wrapper (e.g. `exec sleep 120`): the pane's root pid is no longer a shell.
// Shell-init children (root still a shell) must not read as replaced.
func TestPaneRootReplaced(t *testing.T) {
	if !paneRootReplaced(100, []proc{{PID: 100, Name: "sleep"}}) {
		t.Error("exec'd root: want replaced")
	}
	if paneRootReplaced(100, []proc{{PID: 100, Name: "-zsh"}, {PID: 102, Name: "sw_vers"}}) {
		t.Error("shell init: want not replaced")
	}
	if paneRootReplaced(100, nil) {
		t.Error("no root visible: want not replaced")
	}
}

// A name-lookup transport failure surfaces without touching the binding.
func TestResolveBindingNameLookupErrorSurfaces(t *testing.T) {
	cleared := false
	boom := errors.New("herdr transport down")
	ops := opsFor(t, false, boom, "%5", bindModeAgent, paneProbe{Exists: true, Busy: true}, nil, &cleared)
	ops.boundPane = func() string { t.Fatal("no fallback on a name-lookup transport error"); return "" }
	_, running, err := resolveBinding(ops)
	if !errors.Is(err, boom) || running {
		t.Fatalf("resolveBinding = _, %v, %v; want the lookup error", running, err)
	}
	if cleared {
		t.Error("binding cleared on a name-lookup transport error")
	}
}

// ── clearPaneBindingIfPane: compare-and-delete against a rebind race ────────

// clearPaneBindingIfPane must not erase a binding that has since moved to a
// different pane — the shape of a concurrent Start rebinding name between a
// caller's stale pane→name snapshot and its clear.
func TestClearPaneBindingIfPaneSparesRebind(t *testing.T) {
	p := New("s", t.TempDir(), "", 0, 0)
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	// Simulate a concurrent Start rebinding alice to a new pane between the
	// caller's snapshot (which still names w1:p1) and its clear attempt.
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w2:p1"}, bindModeShell); err != nil {
		t.Fatalf("rebind bindPlacement: %v", err)
	}

	if ok := p.clearPaneBindingIfPane("alice", "w1:p1"); !ok {
		t.Error("clearPaneBindingIfPane on a stale pane reported failure")
	}
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "w2:p1" {
		t.Fatalf("current binding disturbed by a stale clear: pane=%q err=%v, want w2:p1", pane, err)
	}

	if ok := p.clearPaneBindingIfPane("alice", "w2:p1"); !ok {
		t.Error("clearPaneBindingIfPane on the current pane reported failure")
	}
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "" {
		t.Fatalf("current binding not cleared: pane=%q err=%v", pane, err)
	}
}

// A read failure on the current binding (not just a stale-pane mismatch)
// must report false, not silently succeed — a caller (pruneStalePane) that
// cannot tell the two apart would disable backoff against a metadata store
// that cannot actually be written.
func TestClearPaneBindingIfPaneReportsReadFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC permission checks, so chmod(0) would not make the file unreadable")
	}
	p := New("s", t.TempDir(), "", 0, 0)
	if err := p.bindPlacement(context.Background(), "victim", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	metaPath := filepath.Join(p.metaDir, sanitize("victim"), sanitize(metaBoundPane))
	if err := os.Chmod(metaPath, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(metaPath, 0o600) })

	if ok := p.clearPaneBindingIfPane("victim", "w1:p1"); ok {
		t.Error("clearPaneBindingIfPane reported success despite an unreadable binding")
	}
}

// ── clearPaneBindingIfPaneGen: generation-checked clear against a same-pane
// rebind race ─────────────────────────────────────────────────────────────

// The adversarial scenario the round-6 review found: a caller's "stale"
// verdict (probeAffirmsRegistryOverSidecar, sidecarBindingLikelyCurrent) is
// formed under a lock that is released before the caller clears. If a
// concurrent bindPlacement rebinds name to a pane id that recycles to the
// SAME value between that verdict and the clear, clearPaneBindingIfPane's
// pane-only comparison cannot tell the fresh rebind apart from the stale
// binding it replaced, and would erase the just-completed rebind.
// clearPaneBindingIfPaneGen must refuse to clear once metaBoundAt no longer
// matches the generation the verdict was formed against.
func TestClearPaneBindingIfPaneGenSparesSamePaneRebind(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	wantBoundAt, err := p.GetMeta("alice", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt: %v", err)
	}

	// Simulate a concurrent rebind landing between the verdict and the
	// clear: alice is unbound and rebound to the SAME recycled pane id,
	// which stamps a fresh metaBoundAt without changing metaBoundPane.
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("rebind bindPlacement: %v", err)
	}
	gotBoundAt, err := p.GetMeta("alice", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt after rebind: %v", err)
	}
	if gotBoundAt == wantBoundAt {
		t.Fatalf("rebind did not advance metaBoundAt (test fixture is not exercising the race)")
	}

	if ok := p.clearPaneBindingIfPaneGen("alice", "w1:p1", wantBoundAt); !ok {
		t.Error("clearPaneBindingIfPaneGen against a stale generation reported failure, want a no-op success")
	}
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "w1:p1" {
		t.Fatalf("fresh rebind erased by a generation-mismatched clear: pane=%q err=%v, want w1:p1", pane, err)
	}
	if boundAt, err := p.GetMeta("alice", metaBoundAt); err != nil || boundAt != gotBoundAt {
		t.Fatalf("fresh rebind's boundAt disturbed: boundAt=%q err=%v, want %q", boundAt, err, gotBoundAt)
	}

	// The matching generation must still clear normally.
	if ok := p.clearPaneBindingIfPaneGen("alice", "w1:p1", gotBoundAt); !ok {
		t.Error("clearPaneBindingIfPaneGen on a matching generation reported failure")
	}
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "" {
		t.Fatalf("matching-generation binding not cleared: pane=%q err=%v", pane, err)
	}
}

// wantBoundAt == "" means the caller's own locked read could not confirm a
// generation token (a transient I/O error) — clearPaneBindingIfPaneGen must
// fail open (skip the clear, report success) rather than treat an unproven
// read failure as license to destroy the binding.
func TestClearPaneBindingIfPaneGenSkipsClearOnEmptyGenToken(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}

	if ok := p.clearPaneBindingIfPaneGen("alice", "w1:p1", ""); !ok {
		t.Error("clearPaneBindingIfPaneGen with an empty gen token reported failure, want a no-op success")
	}
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "w1:p1" {
		t.Fatalf("binding erased despite an empty gen token: pane=%q err=%v, want w1:p1", pane, err)
	}
}

// TestProbeAffirmsRegistryOverSidecarProbeToSnapshotRace reproduces the race
// probeAffirmsRegistryOverSidecar's pre/post-probe stability check exists to
// close: a rebind of name to the SAME pane id landing while its unlocked
// probePane call is in flight. The fake CLI reports shell_pid 1111 for the
// in-flight (stale-generation) probe and shell_pid 4242 (its normal default)
// for every other call, so the rebind that lands mid-probe leaves
// metaBoundShellPID at 4242 — genuinely different from the stale probe's
// 1111. Without the pre-probe snapshot, that mismatch alone would satisfy
// the "different pid, registry wins" branch and return affirms=true carrying
// the FRESH rebind's own boundAt, which a caller then hands straight back to
// clearPaneBindingIfPaneGen — matching itself and destroying the binding
// that just replaced the stale one. The fix must instead detect that
// metaBoundAt moved between the pre- and post-probe reads and fail open.
func TestProbeAffirmsRegistryOverSidecarProbeToSnapshotRace(t *testing.T) {
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "herdr")
	syncDir := t.TempDir()
	arm := filepath.Join(syncDir, "arm")
	started := filepath.Join(syncDir, "started")
	release := filepath.Join(syncDir, "release")
	fake := `#!/bin/sh
ARM='` + arm + `'
STARTED='` + started + `'
RELEASE='` + release + `'
shift 2
case "$1_$2" in
pane_process-info)
  if [ -e "$ARM" ] && [ ! -e "$STARTED" ]; then
    PID=1111
    : > "$STARTED"
    rm -f "$ARM"
    while [ ! -e "$RELEASE" ]; do sleep 0.01; done
  else
    PID=4242
  fi
  printf '{"result":{"process_info":{"shell_pid":%s,"foreground_processes":[{"pid":%s,"name":"zsh"}]}}}' "$PID" "$PID"
  ;;
*)
  printf '%s' '{"error":{"code":"unsupported","message":"unsupported verb"}}'
  ;;
esac
`
	if err := os.WriteFile(script, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New("gctest-race", t.TempDir(), "", 0, 0)
	p.c.bin = script

	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	staleBoundAt, err := p.GetMeta("alice", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt: %v", err)
	}

	if err := os.WriteFile(arm, nil, 0o600); err != nil {
		t.Fatalf("arm race: %v", err)
	}

	type verdict struct {
		affirms bool
		boundAt string
	}
	got := make(chan verdict, 1)
	go func() {
		affirms, boundAt := p.probeAffirmsRegistryOverSidecar(context.Background(), "alice", "w1:p1")
		got <- verdict{affirms, boundAt}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The rebind lands here, in the window between the verdict function's
	// pre-probe snapshot and its (still pending) post-probe read.
	if err := p.bindPlacement(context.Background(), "alice", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("rebind bindPlacement: %v", err)
	}
	freshBoundAt, err := p.GetMeta("alice", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt after rebind: %v", err)
	}
	if freshBoundAt == staleBoundAt {
		t.Fatal("rebind did not advance metaBoundAt (test fixture is not exercising the race)")
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release probe: %v", err)
	}

	select {
	case v := <-got:
		if v.affirms {
			t.Errorf("probeAffirmsRegistryOverSidecar affirms=true from stale probe evidence paired with a generation that moved mid-probe; want false (fail open)")
		}
		if v.boundAt != "" {
			t.Errorf("probeAffirmsRegistryOverSidecar boundAt = %q on a mid-probe rebind, want empty", v.boundAt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probeAffirmsRegistryOverSidecar did not return")
	}

	// The fresh rebind must survive regardless: a caller gating a clear on
	// the returned (empty) boundAt can never match it against a real binding.
	if pane, err := p.GetMeta("alice", metaBoundPane); err != nil || pane != "w1:p1" {
		t.Fatalf("fresh rebind disturbed: pane=%q err=%v, want w1:p1", pane, err)
	}
	if boundAt, err := p.GetMeta("alice", metaBoundAt); err != nil || boundAt != freshBoundAt {
		t.Fatalf("fresh rebind's boundAt disturbed: boundAt=%q err=%v, want %q", boundAt, err, freshBoundAt)
	}
}

// ── forEachPaneBinding: real read failures must not read as "no bindings" ───

// A metaDir that has simply never been created is legitimately "no
// bindings"; a directory read failure of any other kind must propagate so a
// transient hiccup during the initial walk does not silently leave panes
// undeliverable.
func TestForEachPaneBindingMissingDirIsNoBindings(t *testing.T) {
	p := New("s", filepath.Join(t.TempDir(), "never-created"), "", 0, 0)
	names, err := p.boundSessionNames()
	if err != nil {
		t.Fatalf("boundSessionNames on a missing metaDir: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("boundSessionNames = %v, want none", names)
	}
}

func TestForEachPaneBindingPropagatesRealReadErrors(t *testing.T) {
	dir := t.TempDir()
	metaDir := filepath.Join(dir, "meta")
	if err := os.WriteFile(metaDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed non-dir metaDir: %v", err)
	}
	p := New("s", metaDir, "", 0, 0)
	if _, err := p.boundSessionNames(); err == nil {
		t.Error("boundSessionNames swallowed a non-ENOENT directory read error")
	}
}

// ── boundPaneBindings: collision resolution by freshness ────────────────────

// Two sidecar bindings sharing one pane id (a stale binding not yet cleared
// after a rebind) must collapse to whichever is more recently bound, not
// whichever directory-iteration order happens to surface.
func TestBoundPaneBindingsCollisionKeepsFreshest(t *testing.T) {
	p := New("s", t.TempDir(), "", 0, 0)
	for _, m := range []struct {
		name    string
		boundAt string
	}{
		{"stale-occupant", "100"},
		{"fresh-occupant", "200"},
	} {
		if err := p.SetMeta(m.name, metaBoundName, m.name); err != nil {
			t.Fatalf("SetMeta name: %v", err)
		}
		if err := p.SetMeta(m.name, metaBoundPane, "w1:p1"); err != nil {
			t.Fatalf("SetMeta pane: %v", err)
		}
		if err := p.SetMeta(m.name, metaBoundAt, m.boundAt); err != nil {
			t.Fatalf("SetMeta boundAt: %v", err)
		}
	}

	bindings, _, err := p.boundPaneBindings()
	if err != nil {
		t.Fatalf("boundPaneBindings: %v", err)
	}
	if got := bindings["w1:p1"]; got != "fresh-occupant" {
		t.Errorf("boundPaneBindings[w1:p1] = %q, want fresh-occupant (the newer boundAt)", got)
	}
}

// ── round-4 review coverage: registry-mismatch corroboration, PID
// arbitration, failed-probe rebinding, typed-error propagation, timestamp
// boundaries, and PartialListError wrapping ──────────────────────────────────

// A probe transport failure must not let the registry override a sidecar
// binding: neither side is more trustworthy this cycle, and destroying
// attribution on unproven grounds is the more dangerous failure mode.
func TestProbeAffirmsRegistryOverSidecarTransportFailure(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	p.c.bin = filepath.Join(t.TempDir(), "missing-herdr-binary")
	if err := p.SetMeta("sess", metaBoundShellPID, "4242"); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.probeAffirmsRegistryOverSidecar(context.Background(), "sess", "%5"); got {
		t.Errorf("probeAffirmsRegistryOverSidecar = true on a probe transport failure, want false (must not let the registry override on unconfirmed evidence)")
	}
}

// A probe reporting the SAME shell pid the sidecar bound at placement time
// is direct proof the occupant hasn't changed, so the registry's differing
// claim is the stale side — the sidecar must still win.
func TestProbeAffirmsRegistryOverSidecarPIDMatch(t *testing.T) {
	p, _ := newFakeHerdrProvider(t) // fake process-info always reports shell_pid 4242
	if err := p.SetMeta("sess", metaBoundShellPID, "4242"); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.probeAffirmsRegistryOverSidecar(context.Background(), "sess", "%5"); got {
		t.Errorf("probeAffirmsRegistryOverSidecar = true despite a matching shell pid (same occupant), want false")
	}
}

// A probe reporting a DIFFERENT shell pid than the one bound at placement
// time is direct proof of a new occupant — the registry's claim must win and
// the stale sidecar binding must be clearable.
func TestProbeAffirmsRegistryOverSidecarPIDMismatch(t *testing.T) {
	p, _ := newFakeHerdrProvider(t) // fake process-info always reports shell_pid 4242
	if err := p.SetMeta("sess", metaBoundShellPID, "9999"); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.probeAffirmsRegistryOverSidecar(context.Background(), "sess", "%5"); !got {
		t.Errorf("probeAffirmsRegistryOverSidecar = false despite a genuinely different shell pid, want true (registry should win over a recycled pane)")
	}
}

// probePane must propagate a raw transport failure (here, a missing herdr
// binary) as a real error rather than silently reading it as a typed
// pane_not_found confirmed-gone result.
func TestProbePaneTransportErrorPropagates(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	p.c.bin = filepath.Join(t.TempDir(), "missing-herdr-binary")
	if _, err := p.probePane(context.Background(), "%5"); err == nil {
		t.Fatal("probePane returned nil error for a missing binary; want the transport failure propagated, not swallowed as confirmed-gone")
	}
}

// probePane must respect its caller's context bound rather than letting a
// hung or already-expired context silently succeed.
func TestProbePaneRespectsContextCancellation(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.probePane(ctx, "%5"); err == nil {
		t.Fatal("probePane returned nil error for an already-canceled context; want the bound propagated as an error")
	}
}

// probePane must be bounded by its own internal sessionEventOpTimeout even
// when the caller's context carries no deadline of its own — otherwise a
// caller passing a long-lived parent context (derivedFilterSet uses the live
// event stream's ctx, which has no deadline of its own) could hang forever
// on one wedged CLI invocation. A pre-canceled parent context alone
// (TestProbePaneRespectsContextCancellation, above) would pass even if
// probePane's own context.WithTimeout wrapping were deleted, since the
// parent's own cancellation propagates regardless — this test instead gives
// probePane an undeadlined parent and a fake CLI that hangs well past
// sessionEventOpTimeout, so only the internal wrapping can account for it
// returning early.
func TestProbePaneBoundedByInternalTimeout(t *testing.T) {
	p, state := newFakeHerdrProvider(t)
	setState(t, state, "hang")

	start := time.Now()
	_, err := p.probePane(context.Background(), "%5")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("probePane returned nil error against a hung CLI, want the internal timeout propagated as an error")
	}
	if elapsed < sessionEventOpTimeout {
		t.Fatalf("probePane returned after %s, before its own %s internal timeout — an undeadlined parent context should not have let it return this early", elapsed, sessionEventOpTimeout)
	}
	if elapsed > sessionEventOpTimeout+5*time.Second {
		t.Fatalf("probePane returned after %s, want close to its %s internal timeout, not the fake CLI's full 30s hang (internal context.WithTimeout not actually bounding it)", elapsed, sessionEventOpTimeout)
	}
}

// lockName must serialize concurrent holders for the same name (no two
// callers observe overlapping ownership) and must reclaim its map entry once
// every holder has released — a leak here is exactly round-4's original
// "unbounded per-name lock storage" finding reappearing under concurrency
// instead of sequential churn.
func TestLockNameExclusionAndReclamation(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	const goroutines = 50

	var mu sync.Mutex
	holders, maxSeen := 0, 0
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			unlock := p.lockName("sess")
			mu.Lock()
			holders++
			if holders > maxSeen {
				maxSeen = holders
			}
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			holders--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()

	if maxSeen != 1 {
		t.Errorf("lockName allowed %d concurrent holders for one name, want 1 (exclusion violated)", maxSeen)
	}

	p.nameLocksMu.Lock()
	n := len(p.nameLocks)
	p.nameLocksMu.Unlock()
	if n != 0 {
		t.Errorf("nameLocks has %d entries after every holder released, want 0 (entries not reclaimed)", n)
	}
}

// parseBoundAt must distinguish legacy Unix-SECONDS values from current
// Unix-NANOSECOND values at the documented ceiling without overflowing int64
// nanoseconds for a legacy value just under that ceiling.
func TestParseBoundAtBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantOK       bool
		checkNano    bool
		wantUnix     int64
		wantUnixNano int64
	}{
		{name: "empty", raw: "", wantOK: false},
		{name: "zero", raw: "0", wantOK: false},
		{name: "negative", raw: "-100", wantOK: false},
		{name: "not a number", raw: "abc", wantOK: false},
		{
			// The exact overflow case round-4 flagged: ts*int64(time.Second)
			// wraps for a legacy value this close to the ceiling.
			name:     "legacy seconds just under the overflow-prone ceiling",
			raw:      "999999999999",
			wantOK:   true,
			wantUnix: 999999999999,
		},
		{
			name:         "exact ceiling reads as nanoseconds",
			raw:          "1000000000000",
			wantOK:       true,
			checkNano:    true,
			wantUnixNano: 1000000000000,
		},
		{
			name:         "realistic current nanosecond timestamp",
			raw:          "1700000000000000000",
			wantOK:       true,
			checkNano:    true,
			wantUnixNano: 1700000000000000000,
		},
		{
			name:     "realistic legacy seconds timestamp",
			raw:      "1700000000",
			wantOK:   true,
			wantUnix: 1700000000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBoundAt(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseBoundAt(%q) ok = %v, want %v", tt.raw, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if tt.checkNano {
				if got.UnixNano() != tt.wantUnixNano {
					t.Errorf("parseBoundAt(%q).UnixNano() = %d, want %d", tt.raw, got.UnixNano(), tt.wantUnixNano)
				}
				return
			}
			if got.Unix() != tt.wantUnix {
				t.Errorf("parseBoundAt(%q).Unix() = %d, want %d", tt.raw, got.Unix(), tt.wantUnix)
			}
		})
	}
}

// A rebind whose bind-time probe fails must clear any stale shell pid left
// over from the PREVIOUS pane this name was bound to — otherwise a later
// probe reporting the new pane's real pid looks like a recycle and clears
// the valid new binding (round-4's "Failed rebind retains the previous
// occupant PID").
func TestBindPlacementClearsStaleShellPIDOnFailedRebind(t *testing.T) {
	p, _ := newFakeHerdrProvider(t)
	ctx := context.Background()
	if err := p.bindPlacement(ctx, "sess", agentInfo{PaneID: "p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement (p1): %v", err)
	}
	got, err := p.boundShellPID("sess")
	if err != nil {
		t.Fatalf("boundShellPID after successful bind: %v", err)
	}
	if got != 4242 {
		t.Fatalf("boundShellPID after successful bind = %d, want 4242", got)
	}

	// Rebind the same name to a different pane while its bind-time probe
	// fails (a missing binary stands in for any transport failure).
	p.c.bin = filepath.Join(t.TempDir(), "missing-herdr-binary")
	if err := p.bindPlacement(ctx, "sess", agentInfo{PaneID: "p2"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement (p2): %v", err)
	}
	got, err = p.boundShellPID("sess")
	if err != nil {
		t.Fatalf("boundShellPID after failed-probe rebind: %v", err)
	}
	if got != 0 {
		t.Errorf("boundShellPID after failed-probe rebind = %d, want 0 (stale p1 pid must be cleared, not left in place)", got)
	}
}

// ListRunning must merge partial sidecar-enumeration results (one binding's
// metadata is unreadable) with the readable ones, reporting a
// PartialListError rather than either discarding the good bindings or
// masking the read failure as full success.
func TestListRunningPartialListError(t *testing.T) {
	p, state := newFakeHerdrProvider(t)
	setState(t, state, "busy")
	bindTestPane(t, p, "gastown__good", bindModeShell)
	if err := p.SetMeta("gastown__good", metaBoundName, "gastown__good"); err != nil {
		t.Fatal(err)
	}
	bindTestPane(t, p, "gastown__bad", bindModeShell)
	if err := p.SetMeta("gastown__bad", metaBoundName, "gastown__bad"); err != nil {
		t.Fatal(err)
	}

	// Corrupt the bad session's name file into a directory so
	// forEachPaneBinding hits a genuine read error (not the ErrNotExist "no
	// binding" case), forcing a partial walk.
	nameFile := filepath.Join(p.metaDir, sanitize("gastown__bad"), sanitize(metaBoundName))
	if err := os.Remove(nameFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nameFile, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := p.ListRunning("gastown__")
	if !runtime.IsPartialListError(err) {
		t.Fatalf("ListRunning err = %v, want a PartialListError", err)
	}
	if len(got) != 1 || got[0] != "gastown__good" {
		t.Fatalf("ListRunning = %v, want exactly [gastown__good]", got)
	}
}
