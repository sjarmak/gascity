package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── pane binding: the stable agent handle under herdr ≥0.7.4 ─────────────────
//
// herdr ≥0.7.4 clears an agent's *name* from its registry when the pane
// occupant exits, is released, or is replaced. On 0.7.5 that is by design —
// `agent start` detects the launched TUI and a cleared name means the agent
// exited — but it also means every name-keyed lookup can go dark while the
// session's pane lives on (raw shell sessions are never registered at all).
// Reading a live session as absent is the spawn storm: IsRunning goes false,
// the reconciler re-Starts every tick, and each wrongful Start leaks a pane.
// The *pane id* is the stable handle, so Start persists it (plus the launch
// mode) in the metadata sidecar and every name→pane resolution falls back to
// it, probed live before it is trusted (pane ids recycle).

// Sidecar keys for the placement herdr assigned at Start. Namespaced away from
// the GC_* env keys seedMetaFromEnv mirrors into the same store.
const (
	metaBoundPane      = "GC_HERDR_PANE_ID"
	metaBoundTab       = "GC_HERDR_TAB_ID"
	metaBoundWorkspace = "GC_HERDR_WORKSPACE_ID"
	metaBoundMode      = "GC_HERDR_LAUNCH_MODE"
	// metaBoundName holds the exact session name (sidecar directories use the
	// sanitized form, which is lossy), so ListRunning can enumerate bound
	// sessions that herdr's registry does not know about.
	metaBoundName = "GC_HERDR_SESSION_NAME"
	// metaBoundAt holds the unix-NANOSECOND timestamp of the binding, so the
	// exited-agent reap can distinguish a pane whose agent is still being
	// launched (fresh binding) from one whose agent exited (old binding).
	// Nanosecond, not second, precision: boundPaneBindings' collision
	// resolution picks the fresher of two bindings sharing one pane id by
	// this timestamp, and two Starts landing in the same wall-clock second
	// (plausible under concurrent pool-wisp churn) would otherwise tie and
	// fall back to directory-iteration order — the exact bug the freshness
	// check exists to remove.
	metaBoundAt = "GC_HERDR_BOUND_AT"
	// metaBoundShellPID holds the shell pid herdr reported for the pane at
	// bind time (0/absent when the probe failed or predates this field). A
	// pane's pty keeps one pid for its whole life — recycling only happens by
	// destroying the pane and creating a new one, which mints a new pid — so a
	// later probe reporting a DIFFERENT shell pid for the same pane id is
	// direct proof of a new occupant, not just "something is running here"
	// (which busy/shell-mode alone cannot distinguish from the original
	// session). See sidecarBindingLikelyCurrent.
	metaBoundShellPID = "GC_HERDR_SHELL_PID"
)

// bindingLaunchGrace is how long after binding a pane may sit at a bare
// shell prompt in bindModeAgent before it reads as "agent exited" and is
// reaped. Sized past the whole launch window (shell readiness wait +
// herdr's agent-start timeout + busy retries), so an in-flight Start's
// provisionally bound pane is never closed from under it.
const bindingLaunchGrace = 3 * time.Minute

// Launch modes persisted at metaBoundMode. They pick the liveness rule for
// the binding fallback: a registered agent (bindModeAgent) whose pane is back
// at a bare shell prompt has exited — its pane still resolves so Stop can
// close it, but the session is not running; a raw/bare shell session
// (bindModeShell) runs as long as its pane exists, because `exec /bin/sh -c`
// panes die with their command.
const (
	bindModeAgent = "agent"
	bindModeShell = "shell"
)

// paneProbe is what probing a bound pane learned: whether the pane still
// exists, and whether something beyond the pane's own shell is in the
// foreground (a foreground process with a pid other than the shell's).
type paneProbe struct {
	Exists   bool
	Busy     bool
	ShellPID int
}

// paneLookupOps are the operations resolveBinding needs, injected as closures
// so the resolution decision is unit-testable without a live herdr server
// (mirrors agentStartOps).
type paneLookupOps struct {
	// getAgent is the name-keyed registry lookup (fast path while the name lives).
	getAgent func() (agentInfo, bool, error)
	// boundPane reads the sidecar pane binding ("" when absent).
	boundPane func() string
	// boundMode reads the persisted launch mode ("" on pre-upgrade bindings).
	boundMode func() string
	// boundAge reports how long ago the binding was persisted (a very large
	// value when unknown, so pre-upgrade bindings are still reapable).
	boundAge func() time.Duration
	// reapPane closes an exited agent's leftover pane (best-effort).
	reapPane func(paneID string)
	// probePane inspects the bound pane. A zero probe with nil error means
	// herdr confirmed the pane gone; a non-nil error means the probe itself
	// failed (transport), which proves nothing either way.
	probePane func(paneID string) (paneProbe, error)
	// clearBinding drops a binding whose pane herdr confirmed gone, so a
	// recycled pane id can never resurrect a dead session.
	clearBinding func()
}

// resolveBinding resolves a session name to its herdr pane id and a running
// verdict: registry name lookup first (a live name is a running agent), then
// the sidecar pane binding, trusted only after a live probe. Running is
// mode-aware: a busy pane always runs; a bare shell prompt runs only for
// bindModeShell. A bindModeAgent pane at a bare prompt past the launch grace
// means the agent EXITED — under tmux the pane would have died with the
// process, so it is reaped here (pane closed, binding cleared): nothing else
// ever reaps it for an ephemeral wisp, whose unique tab label sees no future
// Start and whose not-running verdict means no Stop — one leaked shell pane
// per completed wisp otherwise. Within the grace the pane resolves untouched
// (an in-flight Start provisionally bound it). A binding whose pane is
// confirmed gone is cleared and resolves absent; a transport failure on
// either tier surfaces as an error and clears nothing.
func resolveBinding(ops paneLookupOps) (paneID string, running bool, err error) {
	a, ok, err := ops.getAgent()
	if err != nil {
		return "", false, err
	}
	if ok && a.PaneID != "" {
		return a.PaneID, true, nil
	}
	pane := strings.TrimSpace(ops.boundPane())
	if pane == "" {
		return "", false, nil
	}
	probe, err := ops.probePane(pane)
	if err != nil {
		return "", false, err
	}
	if !probe.Exists {
		ops.clearBinding()
		return "", false, nil
	}
	if probe.Busy || ops.boundMode() == bindModeShell {
		return pane, true, nil
	}
	if ops.boundAge() > bindingLaunchGrace {
		ops.reapPane(pane)
		ops.clearBinding()
		return "", false, nil
	}
	return pane, false, nil
}

// bindPlacement persists the placement herdr assigned this agent plus its
// launch mode, so every later name-keyed op survives the name clear. Called
// by Start after the agent (fresh or adopted) is up; Stop's clearMeta
// removes it. Also probes the pane for its current shell pid and persists
// that, so a later sidecarBindingLikelyCurrent check has real
// occupant-identity evidence instead of trusting "the pane is busy" alone.
// The shell-pid key is always explicitly set-or-removed rather than only
// conditionally set: name may already hold a binding for a DIFFERENT prior
// pane (a rebind — Start calls this twice, and adoption can rebind an
// existing name), and a probe failure on the new pane must not leave that
// prior pane's shell pid file in place under the new binding — a later
// identity check would then compare the new pane's real pid against the OLD
// pane's stale one, read it as a mismatch, and destroy the just-written,
// perfectly valid binding.
func (p *Provider) bindPlacement(ctx context.Context, name string, info agentInfo, mode string) error {
	defer p.lockName(name)()
	values := map[string]string{
		metaBoundPane:      info.PaneID,
		metaBoundTab:       info.TabID,
		metaBoundWorkspace: info.WorkspaceID,
		metaBoundMode:      mode,
		metaBoundName:      name,
		metaBoundAt:        strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	shellPID := 0
	if probe, err := p.probePane(ctx, info.PaneID); err == nil {
		shellPID = probe.ShellPID
	}
	for key, val := range values {
		if val == "" {
			continue
		}
		if err := p.SetMeta(name, key, val); err != nil {
			return err
		}
	}
	if shellPID != 0 {
		return p.SetMeta(name, metaBoundShellPID, strconv.Itoa(shellPID))
	}
	return p.RemoveMeta(name, metaBoundShellPID)
}

// nameLock is one session name's exclusion entry: the mutex lockName's
// caller actually holds, plus a count of goroutines currently between
// lockName and its returned unlock func (i.e. either waiting on mu or
// holding it) so the entry can be reclaimed the instant nothing references
// it anymore.
type nameLock struct {
	mu   sync.Mutex
	refs int
}

// lockName serializes sidecar binding mutations and multi-key reads for one
// session name so a concurrent Start (bindPlacement), a destructive clear
// (clearPaneBinding / clearPaneBindingIfPane / clearMeta), and a coherent
// read of several related keys (sidecarBindingLikelyCurrent) can never
// interleave into a corrupted mix or a torn snapshot — the sidecar has no
// single-file atomic representation to compare-and-swap against (several
// independent files per binding), so mutual exclusion is enforced in-process
// instead. Keyed by name rather than one global lock, so unrelated sessions
// never serialize through each other on every Start. Returns the unlock
// func; callers `defer` it. Not reentrant: no locked function may call
// another locked function for the same name.
//
// Entries are reference-counted rather than left in the map forever (which
// would grow it by one entry per distinct name for the provider's whole
// lifetime under a session-naming scheme that churns names, e.g. a pool's
// per-wisp naming) and rather than deleted unconditionally on unlock (a
// delete racing a fresh lookup for the same name could hand out two
// different mutexes for what should be one name's exclusion, i.e. no
// exclusion at all). Both the map access and each entry's refs field are
// guarded by the single nameLocksMu, so increment-then-use and
// release-then-maybe-delete can never interleave across goroutines: any
// concurrent lockName(name) either observes refs already bumped above zero
// (reuses the live entry; the delete below cannot have fired yet) or
// observes the entry already deleted (creates a fresh one) — never a window
// where two entries both claim to be "the lock" for the same name at once.
func (p *Provider) lockName(name string) func() {
	p.nameLocksMu.Lock()
	nl, ok := p.nameLocks[name]
	if !ok {
		nl = &nameLock{}
		if p.nameLocks == nil {
			p.nameLocks = make(map[string]*nameLock)
		}
		p.nameLocks[name] = nl
	}
	nl.refs++
	p.nameLocksMu.Unlock()

	nl.mu.Lock()

	return func() {
		nl.mu.Unlock()
		p.nameLocksMu.Lock()
		nl.refs--
		if nl.refs == 0 {
			delete(p.nameLocks, name)
		}
		p.nameLocksMu.Unlock()
	}
}

// clearPaneBinding drops the persisted placement (not the whole sidecar — the
// session identity keys stay for the reconciler). Idempotent. Returns the
// first RemoveMeta error, if any: a caller whose correctness depends on the
// clear actually landing (pruneStalePane's stale-pane recovery) needs to
// distinguish a real deletion from a filesystem failure masquerading as
// one — silently swallowing every error here let a wedged stale pane retry
// at full speed forever, since the caller believed the prune had succeeded
// and kept disabling backoff every cycle.
func (p *Provider) clearPaneBinding(name string) error {
	defer p.lockName(name)()
	return p.removeBindingKeys(name)
}

// removeBindingKeys is clearPaneBinding's non-locking core, shared with
// clearPaneBindingIfPane so its check-then-delete can hold the name lock
// across both the GetMeta read and the removals — re-entering lockName here
// would deadlock against that already-held lock.
func (p *Provider) removeBindingKeys(name string) error {
	var firstErr error
	for _, key := range []string{metaBoundPane, metaBoundTab, metaBoundWorkspace, metaBoundMode, metaBoundName, metaBoundAt, metaBoundShellPID} {
		if err := p.RemoveMeta(name, key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// clearPaneBindingIfPane drops name's persisted placement only if its
// currently stored pane id still equals pane — a check-then-delete guard
// against clearing a binding that has since moved to a different, live pane.
// Without this, a caller working from a stale pane↔name snapshot (a registry
// entry that outlived the pane's original occupant, or a concurrent Start
// that rebound name to a new pane between the snapshot and the clear) erases
// the session's *current* binding instead of the stale one it meant to drop.
// Returns false only when the current pane could not be determined or the
// clear itself failed; true covers both "cleared" and "nothing to clear
// because the binding had already moved on".
func (p *Provider) clearPaneBindingIfPane(name, pane string) bool {
	defer p.lockName(name)()
	current, err := p.GetMeta(name, metaBoundPane)
	if err != nil {
		return false
	}
	if strings.TrimSpace(current) != pane {
		return true
	}
	return p.removeBindingKeys(name) == nil
}

// clearPaneBindingIfPaneGen is clearPaneBindingIfPane's generation-checked
// sibling, for callers whose "stale" verdict was formed by a separate
// locked read (sidecarBindingLikelyCurrent, probeAffirmsRegistryOverSidecar)
// that released the lock before returning. Comparing the pane id alone is
// not enough there: nothing stops a concurrent bindPlacement from rebinding
// name to a NEW pane that happens to recycle to the very same pane id in
// the gap between that verdict and this clear, and clearPaneBindingIfPane
// cannot distinguish that fresh rebind from the stale binding it replaced —
// both have the same (name, pane) pair. metaBoundAt is written fresh, at
// nanosecond precision, on every bindPlacement, so it serves as a
// generation token: wantBoundAt is the value the caller observed at verdict
// time, and a mismatch here proves the binding has already moved on, so the
// clear is skipped (reporting success, since there is nothing stale left to
// clear) rather than erasing a binding newer than the one the verdict was
// formed against. wantBoundAt == "" means the caller's own locked read
// could not confirm a generation token (a transient I/O error) — the clear
// is skipped in that case too, on the same fail-open reasoning used
// throughout this file: an unproven read failure is not grounds to destroy
// a binding, and the pane is simply re-evaluated next cycle.
func (p *Provider) clearPaneBindingIfPaneGen(name, pane, wantBoundAt string) bool {
	if wantBoundAt == "" {
		return true
	}
	defer p.lockName(name)()
	current, err := p.GetMeta(name, metaBoundPane)
	if err != nil {
		return false
	}
	if strings.TrimSpace(current) != pane {
		return true
	}
	boundAt, err := p.GetMeta(name, metaBoundAt)
	if err != nil {
		return false
	}
	if strings.TrimSpace(boundAt) != wantBoundAt {
		return true
	}
	return p.removeBindingKeys(name) == nil
}

// lockedMetaBoundAt reads name's persisted metaBoundAt under its lock,
// trimmed. err is non-nil only for a genuine I/O failure (GetMeta already
// normalizes os.ErrNotExist to an absent "" value with a nil error) — the
// shared building block every verdict function in this file and events.go
// uses to bracket an unlocked probe with a same-key locked read on each
// side, so a rebind landing during the probe is detectable as a change in
// this one value rather than silently producing a torn verdict.
func (p *Provider) lockedMetaBoundAt(name string) (string, error) {
	defer p.lockName(name)()
	raw, err := p.GetMeta(name, metaBoundAt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(raw), nil
}

// forEachPaneBinding walks the sidecar directory once and calls fn with each
// live-looking binding (a stored name, pane id, and bind timestamp), for
// boundSessionNames and boundPaneBindings to project into the shape their
// caller needs. err is non-nil only when the root directory itself cannot be
// enumerated — distinct from "no bindings yet", which a metaDir that has
// never been created is, not a failure; on a root failure complete is
// meaningless and callers should discard any partial fn calls made before
// the error (there are none — the failure is always found before the walk
// starts). A single entry's own files failing to read (a transient
// permission or filesystem hiccup on that one session) is logged and
// skipped rather than aborting the whole walk — propagating it as a hard
// err here takes down status-event delivery for every OTHER healthy session
// sharing this cycle, which is worse than the one entry going briefly
// unlisted — but it does make the walk incomplete, reported via complete so
// a caller that needs to know (boundSessionNames, for ListRunning's
// PartialListError) can distinguish "every binding accounted for" from
// "some entries were unreadable, results may undercount". A caller
// indifferent to that distinction (boundPaneBindings, whose own consumer
// already treats "no entry" as fine) can ignore complete.
func (p *Provider) forEachPaneBinding(fn func(pane, name, boundAtRaw string, boundAt time.Time)) (complete bool, err error) {
	entries, err := os.ReadDir(p.metaDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("herdr: read pane-binding sidecar %s: %w", p.metaDir, err)
	}
	complete = true
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(p.metaDir, e.Name())
		name, err := readMetaFile(filepath.Join(dir, sanitize(metaBoundName)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "herdr: skipping unreadable pane-binding entry %s: %v\n", dir, err) //nolint:errcheck // best-effort diagnostic
			complete = false
			continue
		}
		if name == "" {
			continue
		}
		pane, err := readMetaFile(filepath.Join(dir, sanitize(metaBoundPane)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "herdr: skipping unreadable pane-binding entry %s: %v\n", dir, err) //nolint:errcheck // best-effort diagnostic
			complete = false
			continue
		}
		if pane == "" {
			continue
		}
		boundAtRaw, err := readMetaFile(filepath.Join(dir, sanitize(metaBoundAt)))
		if err != nil {
			boundAtRaw = ""
		}
		boundAt, _ := parseBoundAt(boundAtRaw)
		fn(pane, name, boundAtRaw, boundAt)
	}
	return complete, nil
}

// legacyBoundAtSecondsCeiling disambiguates a persisted metaBoundAt value
// between the pre-nanosecond-precision format (Unix SECONDS, ~1.7e9 today)
// and the current format (Unix NANOSECONDS, ~1.7e18 today): the two are
// separated by nine orders of magnitude, so any value under this ceiling
// cannot be a real nanosecond timestamp (that reading would place it within
// the first ~11 days after the epoch) and any value at or above it cannot be
// a real seconds timestamp (that reading would place it past the year
// 33658). Without this, upgrading the binary while sessions are running
// leaves their bindings' still-in-seconds values parsed as nanoseconds —
// time.Unix(0, ~1.7e9) lands under two seconds after the 1970 epoch — so
// paneBoundAge reads every pre-upgrade binding as ~55 years old, past any
// grace window, and the exited-agent reap or the recycled-pane clear can
// destroy a perfectly live session's binding the moment the new binary
// first evaluates it.
const legacyBoundAtSecondsCeiling = 1_000_000_000_000 // 1e12

// parseBoundAt interprets a persisted metaBoundAt value, auto-upconverting a
// legacy seconds-precision value (see legacyBoundAtSecondsCeiling) so every
// caller sees one uniform nanosecond-precision time.Time regardless of which
// binary version wrote the binding. ok is false when raw is empty or not a
// valid positive integer.
func parseBoundAt(raw string) (t time.Time, ok bool) {
	ts, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || ts <= 0 {
		return time.Time{}, false
	}
	if ts < legacyBoundAtSecondsCeiling {
		// time.Unix(seconds, 0) directly, not ts*int64(time.Second) folded
		// into a single nanosecond count: a legacy value just under the
		// ceiling (e.g. 999999999999) overflows int64 nanoseconds when
		// multiplied by 1e9, wrapping to a bogus date instead of the
		// correct ~31,688 AD (a real seconds value this large is never a
		// live binding's timestamp either way, but a wrapped result must
		// not silently masquerade as one near the 1970 epoch).
		return time.Unix(ts, 0), true
	}
	return time.Unix(0, ts), true
}

// paneBoundAge reports how long ago name's binding was persisted, or an
// effectively-infinite duration when the timestamp is absent or unparseable
// (a pre-upgrade binding) — so callers gating on the launch grace treat an
// unknown age as "long past the grace" rather than "just bound".
func (p *Provider) paneBoundAge(name string) time.Duration {
	raw, _ := p.GetMeta(name, metaBoundAt)
	t, ok := parseBoundAt(raw)
	if !ok {
		return time.Duration(1<<62) * time.Nanosecond
	}
	return time.Since(t)
}

// boundSessionNames enumerates the session names with a live-looking sidecar
// binding (a stored name and pane id), for ListRunning to merge with herdr's
// registry — which never sees raw shell sessions. A non-nil error alongside a
// non-empty names means the walk was only PARTIAL (some entries were
// individually unreadable — see forEachPaneBinding): the names collected so
// far are still real bindings, not a discarded, half-built result, so
// ListRunning reports them together with a PartialListError rather than
// treating the incompleteness as either silent full success or a hard
// failure that throws the good names away too.
func (p *Provider) boundSessionNames() ([]string, error) {
	var names []string
	complete, err := p.forEachPaneBinding(func(_, name, _ string, _ time.Time) { names = append(names, name) })
	if err != nil {
		return nil, err
	}
	if !complete {
		return names, fmt.Errorf("herdr: some pane-binding entries were unreadable")
	}
	return names, nil
}

// boundPaneBindings enumerates every live-looking sidecar binding as a
// pane id → exact gc session name map. This is the pane↔name mapping the
// herdr agent registry cannot supply on its own: herdr ≥0.8.0 only lists an
// agent once it has DETECTED a supported interactive process in the pane
// (`herdr agent explain`), so a pane whose occupant herdr has not yet
// classified — or never will, such as a raw shell — is absent from
// agent.list even though its session is live and bound. Keying by the
// sidecar's persisted metaBoundName also sidesteps herdrAgentName's lossy
// mapping (lowercased, length-capped): the registry's a.Name is the herdr
// label, not the gc session name, and the two only coincide by chance.
//
// Keyed by pane id, so two sessions sharing one pane (a stale binding not
// yet cleared after a rebind) collapse to one entry rather than both
// appearing. The survivor is whichever binding's metaBoundAt is more
// recent — directory-iteration order carries no information about which
// binding is current, so picking by it (the prior behavior) could just as
// easily surface the stale side of the collision as the live one. Callers
// that need every bound name regardless of pane collisions want
// boundSessionNames instead.
//
// boundAt returns the same survivor's raw metaBoundAt string alongside its
// name, keyed the same way — derivedFilterSet's only use for it is handing
// a generation token forward to sessionEventStream.paneBoundAt, for
// pruneStalePane to gate a later clear against, since a herdr
// pane_not_found rejection carries no generation information of its own and
// the gap between "this cycle observed pane as name's binding" and "the
// prune actually runs" is exactly the kind of window a same-ID rebind can
// land in.
func (p *Provider) boundPaneBindings() (names map[string]string, boundAt map[string]string, err error) {
	type binding struct {
		name       string
		boundAtRaw string
		boundAt    time.Time
	}
	latest := make(map[string]binding)
	// complete is unused here: derivedFilterSet (this method's only caller)
	// already tolerates individually-missing bindings by construction — a
	// pane this cycle can't account for just gets no paneNames entry, same
	// as if it were never bound — so there is no separate "partial" signal
	// worth surfacing on top of that. boundSessionNames (ListRunning) is
	// where the distinction earns its keep.
	_, err = p.forEachPaneBinding(func(pane, name, boundAtRaw string, boundAt time.Time) {
		if cur, ok := latest[pane]; !ok || boundAt.After(cur.boundAt) {
			latest[pane] = binding{name: name, boundAtRaw: boundAtRaw, boundAt: boundAt}
		}
	})
	if err != nil {
		return nil, nil, err
	}
	names = make(map[string]string, len(latest))
	boundAt = make(map[string]string, len(latest))
	for pane, b := range latest {
		names[pane] = b.name
		boundAt[pane] = b.boundAtRaw
	}
	return names, boundAt, nil
}

// readMetaFile reads one sidecar value ("" when absent).
func readMetaFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// probePane inspects a bound pane via `pane process-info`, bounded by
// sessionEventOpTimeout so one hung CLI invocation cannot stall a caller
// indefinitely — derivedFilterSet calls this once per undetected/mismatched
// binding on the event stream's long-lived ctx, which otherwise has no bound
// of its own and would let a single wedged `herdr` subprocess stop event
// delivery for every session sharing the stream. herdr answering a typed
// pane_not_found is a confirmed-gone pane (zero probe, nil error); any other
// failure — including a raw exec failure such as the herdr binary itself
// being missing from PATH — is a transport error that proves nothing about
// the pane and must not be read as "confirmed gone". Matching on the typed
// herdr error code rather than a substring in err.Error() is what keeps
// those two apart: an *exec.Error's own text ("executable file not found in
// $PATH") also contains "not found" and would otherwise be misread as
// herdr's answer instead of this call never having reached herdr at all.
func (p *Provider) probePane(ctx context.Context, paneID string) (paneProbe, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionEventOpTimeout)
	defer cancel()
	shellPID, fg, err := p.c.processInfo(ctx, paneID)
	if err != nil {
		if herdrErrorCode(err) == "pane_not_found" {
			return paneProbe{}, nil
		}
		return paneProbe{}, err
	}
	return paneProbeFrom(shellPID, fg), nil
}

// sidecarBindingLikelyCurrent decides whether a sidecar binding with no
// corresponding registry entry still describes the pane's actual occupant, as
// opposed to a stale binding left behind on a pane herdr's detector has since
// recycled to something else (or not yet classified). Absence from the
// registry alone proves nothing under herdr ≥0.8.0 — undetected occupants
// (raw shells, agents herdr hasn't classified yet) are legitimately invisible
// there — so this applies the same live-probe-before-clear discipline
// resolveBinding uses: a confirmed-gone pane is stale, a busy pane is
// current, and an idle pane is current only while within the launch grace
// (bindModeShell panes are treated as always current once they exist, since a
// raw shell pane dies with its command rather than idling at a stale prompt).
//
// "Busy" and "shell mode" alone only prove SOMETHING is running in the pane,
// not that it is still THIS binding's occupant — a pane herdr recycled to an
// unrelated undetected process would read exactly the same way. Where a
// shell pid was captured at bind time (bindPlacement; absent on a binding
// written before that field existed, or one whose bind-time probe itself
// failed), a probe reporting the SAME shell pid for the same pane id is
// direct proof of the same occupant (a pane's pty keeps one pid for its
// life, so recycling is the only way this pid changes) and is trusted
// regardless of busy/idle/mode; a DIFFERENT shell pid is equally direct
// proof of a new occupant. Without a bound pid to compare against, busy and
// shell-mode carry no occupant-identity evidence at all — a recycled pane's
// unrelated new occupant would be busy or in shell mode too — so that case
// falls back to the same freshness standard already used for an unconfirmed
// idle pane: trusted only within the launch grace window.
//
// The multi-key read (bound shell pid, bind timestamp) is taken under
// name's lock as one coherent snapshot, not as separate unlocked GetMeta
// calls: bindPlacement's writes for a rebind land under the same lock but
// are not atomic across files (several independent SetMeta/RemoveMeta calls
// in sequence), so an unlocked reader could otherwise observe, say, a
// just-written new pane id alongside the OLD pane's now-stale shell pid
// (not yet overwritten/removed) — a torn snapshot straddling two different
// bindings — and misjudge a binding that in fact just finished being
// written correctly as stale.
//
// A probe transport failure, or a metadata read failure on either
// metaBoundShellPID or metaBoundAt (a transient I/O error, not the value
// being legitimately absent), returns true: neither proves anything either
// way, and destroying a binding on unproven grounds is the more dangerous
// failure mode (the caller loses correct pane attribution for a live
// session).
//
// The multi-key read now always runs under name's lock, even when the pane
// has already been confirmed gone, so boundAt reflects a snapshot taken no
// earlier than the verdict itself. boundAt is metaBoundAt as observed by
// that locked read (empty when unreadable) — the caller passes it to
// clearPaneBindingIfPaneGen so a clear triggered by a "false" verdict here
// can be gated against the binding having moved on again since. See
// clearPaneBindingIfPaneGen for why the verdict and the clear cannot simply
// share one lock hold.
//
// probePane itself still runs unlocked, before either metadata read — a
// probe-to-snapshot race: a rebind of name landing in that gap would make
// the probe evidence describe the OLD occupant while the (single, later)
// locked read it used to be paired with described the NEW generation,
// letting a "stale" verdict escape carrying the fresh generation's own
// boundAt — which then matches itself in clearPaneBindingIfPaneGen and
// erases the very binding that just replaced the stale one. A first locked
// read taken BEFORE the probe, compared against the post-probe read, closes
// this: if metaBoundAt moved between them, the probe's evidence cannot be
// trusted to describe whatever generation the post-probe read found, so the
// verdict fails open (current, no boundAt) rather than risk pairing stale
// evidence with a fresh token.
func (p *Provider) sidecarBindingLikelyCurrent(ctx context.Context, name, pane string) (current bool, boundAt string) {
	preAt, preErr := p.lockedMetaBoundAt(name)

	probe, err := p.probePane(ctx, pane)
	if err != nil {
		return true, ""
	}

	unlock := p.lockName(name)
	boundPID, pidErr := p.boundShellPID(name)
	raw, rawErr := p.GetMeta(name, metaBoundAt)
	unlock()
	if rawErr == nil {
		boundAt = strings.TrimSpace(raw)
	}
	if preErr != nil || rawErr != nil || preAt != boundAt {
		return true, ""
	}

	if !probe.Exists {
		return false, boundAt
	}
	if pidErr != nil {
		return true, boundAt
	}
	if boundPID != 0 && probe.ShellPID != 0 {
		return probe.ShellPID == boundPID, boundAt
	}
	if rawErr != nil {
		return true, ""
	}
	t, ok := parseBoundAt(raw)
	if !ok {
		return false, boundAt
	}
	return time.Since(t) <= bindingLaunchGrace, boundAt
}

// boundShellPID reads name's persisted bind-time shell pid. pid is 0 and err
// is nil when the value is legitimately absent (a pre-upgrade binding, or
// bindPlacement's own probe failed) or unparseable. err is non-nil only for a
// genuine I/O failure on the underlying read (GetMeta already normalizes
// os.ErrNotExist to an absent value with a nil error) — callers must not
// treat that read failure the same as legitimate absence, since a real
// stored pid that transiently fails to read is not evidence the binding was
// ever unbound.
func (p *Provider) boundShellPID(name string) (pid int, err error) {
	raw, err := p.GetMeta(name, metaBoundShellPID)
	if err != nil {
		return 0, err
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		return 0, nil
	}
	return pid, nil
}

// interactiveShells are the interactive shells a fresh pane idles in; a pane
// whose root foreground process is one of these (and nothing else runs) is at
// a bare prompt.
var interactiveShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "tcsh": true, "csh": true,
}

// paneProbeFrom folds process-info into the probe verdict. Busy means the
// pane is running something beyond an interactive shell prompt: a foreground
// process other than the root (a launched agent or a shell job), or a root
// that is no longer a shell at all (`exec`'d commands replace it, keeping its
// pid). This is the version-robust "is the session still in there" signal —
// matching configured process names is not (claude ≥2.1.x reports comm as
// its bare version string).
func paneProbeFrom(shellPID int, fg []proc) paneProbe {
	probe := paneProbe{Exists: shellPID != 0, ShellPID: shellPID}
	for _, pr := range fg {
		if pr.PID == 0 {
			continue
		}
		if pr.PID != shellPID || !interactiveShells[strings.TrimPrefix(pr.Name, "-")] {
			probe.Busy = true
			break
		}
	}
	return probe
}

// paneRunsCommand reports whether a pane's foreground holds the launched
// `/bin/sh -c <raw>` wrapper (exec preserves argv) — the positive signal that
// a typed raw launch actually executed, immune to the shell-init children a
// fresh pane runs first.
func paneRunsCommand(fg []proc, raw string) bool {
	for _, pr := range fg {
		if len(pr.Argv) >= 3 && strings.HasSuffix(pr.Argv[0], "sh") && pr.Argv[1] == "-c" && pr.Argv[2] == raw {
			return true
		}
	}
	return false
}

// paneRootReplaced reports whether the pane's root process (pid == shellPID)
// is visible in the foreground and is no longer an interactive shell — a raw
// launch that exec'd straight through the `/bin/sh -c` wrapper (e.g.
// `exec sleep 120`). Shell-init children keep the root a shell, so they never
// read as replaced.
func paneRootReplaced(shellPID int, fg []proc) bool {
	for _, pr := range fg {
		if pr.PID == shellPID {
			return !interactiveShells[strings.TrimPrefix(pr.Name, "-")]
		}
	}
	return false
}

// lookupOps wires paneLookupOps for a session name.
func (p *Provider) lookupOps(ctx context.Context, name string) paneLookupOps {
	meta := func(key string) string {
		v, err := p.GetMeta(name, key)
		if err != nil {
			return ""
		}
		return v
	}
	return paneLookupOps{
		getAgent:     func() (agentInfo, bool, error) { return p.c.getAgent(ctx, herdrAgentName(name)) },
		boundPane:    func() string { return meta(metaBoundPane) },
		boundMode:    func() string { return strings.TrimSpace(meta(metaBoundMode)) },
		boundAge:     func() time.Duration { return p.paneBoundAge(name) },
		probePane:    func(paneID string) (paneProbe, error) { return p.probePane(ctx, paneID) },
		reapPane:     func(paneID string) { _ = p.c.closePane(ctx, paneID) },
		clearBinding: func() { _ = p.clearPaneBinding(name) },
	}
}
