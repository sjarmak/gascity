# Adversarial security review — #2723 Host-header allowlist (dashboard)

Date: 2026-07-11 · Reviewer: verification agent (did not author) · READ-ONLY on repos, tests run freely.

## Verdict

**Merge-ready for the P1 security label.** The branch correctly and completely closes the
DNS-rebinding / Host-injection class *for the dashboard listener*, which is the half of #2723
that was still open. Empirical probing (real net/http server, raw TCP) found **no bypass**.
Two out-of-scope residuals remain in the broader "supervisor API" surface named by the issue
(a deliberate legacy carve-out and a pre-existing all-interfaces dashboard bind); neither is
introduced or worsened by this branch, and neither should block it. Recommend one follow-up
bead (below).

## What the branch is (and a correction to the task framing)

- Bead **gc-74rxa** is NOT the Host-header fix — it is the control-dispatcher `GC_DOLT_PORT`/Dolt-`:0`
  fix (PR #4108). The #2723 security fix is a separate branch that was gated behind gc-74rxa's merge
  (see gc-74rxa notes: "held gc-6uet8 re-sling for #2723 can fire" once #4108 lands).
- The fix lives on two **identical** branches in `/home/ds/gascity-main`:
  - `fix/dashboard-host-allowlist-2723` @ `abe825284`
  - `fix-2723-dashboard-host-allowlist` @ `a04693f1e`
  Same 7-file diff (+211/-15). Pick one; delete the other.
- Files: `cmd/gc/dashboard/hostcheck.go` (new), `cmd/gc/dashboard/serve.go` (wrap handler),
  `cmd/gc/cmd_dashboard.go` (new `--allowed-host` flag), plus tests and `docs/reference/cli.md`.
- The **supervisor API** half of #2723 already landed on `main`
  (`internal/api/middleware.go` `isAllowedSupervisorHost` + `withHostAllowing`,
  `internal/api/supervisor_security_test.go`). This branch is the dashboard complement and
  intentionally mirrors that implementation byte-for-byte.

## Placement — every in-scope listener enumerated

Full listener sweep (`grep ListenAndServe|.Serve(|http.Server|net.Listen tcp`, non-test):

| Listener | File:line | Host check | Notes |
|---|---|---|---|
| Machine-wide supervisor API (`gc supervisor`) | `cmd/gc/cmd_supervisor.go:1289` (`apiMux.Serve`) | **ENABLED, default-closed** | no `WithAnyHostAllowed`; extra hosts via `[supervisor] allowed_hosts` |
| Dashboard (`gc dashboard[/serve]`) | `cmd/gc/dashboard/serve.go:34` | **ENABLED, default-closed** (this branch) | `--allowed-host` for extra names |
| Standalone controller mode | `cmd/gc/controller.go:1357` | **DISABLED** (`WithAnyHostAllowed()` @ 1363) | legacy single-city; residual #1 |
| pprof debug | `internal/api/supervisor.go:261` | none | opt-in `GC_PPROF=1` only; residual #3 |
| Registry OAuth callback | `cmd/gc/cmd_registry_auth.go:308` | none | `127.0.0.1:0` ephemeral, `state` CSRF token; acceptable |

Both surfaces #2723 names for the *default* machine-wide deployment (supervisor API + dashboard)
are now default-closed.

## Default posture — default-CLOSED, confirmed

- Dashboard: `Serve(port, url, allowedHosts)`; `--allowed-host` defaults to `nil` → loopback-only.
  No `allowAny` escape hatch at all for the dashboard.
- Supervisor: `withHostAllowing(sm.allowAnyHost, ...)`, `allowAnyHost` zero-value `false`; only the
  legacy `WithAnyHostAllowed()` sets it true. Machine-wide `gc supervisor` never calls it.
- Legitimate-traffic regression check: the dashboard SPA is served to the browser at
  `localhost:8080` / `127.0.0.1:8080` → allowed. The SPA then calls the supervisor API at a separate
  origin (its own check). No adapter/dashboard caller sends a non-loopback Host. No `X-Forwarded-Host`
  is read anywhere in routing (`webhook_access.go` explicitly does NOT trust forwarded headers), so
  there is no XFH confusion path.

## Bypass matrix — empirically probed against the real handler + raw TCP

Probed `withHostAllowing(nil, ...)` via `httptest` handler and via raw sockets on a live
`httptest.NewServer`. Observed:

Rejected 421 (all attack forms):
- `evil.example`, `evil.example:8080`, `localhost.evil.example` (suffix attack — pinned by a test)
- `evil@127.0.0.1`, `evil@127.0.0.1:8080` (userinfo)
- `127.0.0.1 evil.example`, `127.0.0.1\tevil`, `localhost\x00.evil`, `127.0.0.1%00` (whitespace/null injection)
- `2130706433`, `127.1`, `0177.0.0.1` (decimal/octal/short IP the browser would normalize to loopback — rejected, safe direction)
- `localhost.`, `127.0.0.1.` (trailing dot), `0.0.0.0`, `0.0.0.0:8080`, `:8080`
- `[::1%eth0]` (zone id)

Allowed 200 (all genuine loopback identities — correct):
- `127.0.0.1`, `127.0.0.1:8080`, `127.0.0.2:8080` (127.0.0.0/8), `localhost`, `LOCALHOST`
- `::1`, `[::1]`, `[::1]:8080`, `[::ffff:127.0.0.1]` (IPv4-mapped loopback), `127.0.0.1:` (empty port)

Request-line / smuggling edge cases (raw TCP):
- Absolute-URI `GET http://evil.example/ HTTP/1.1` + `Host: 127.0.0.1` → **421** (Go binds `r.Host`
  to the URI authority; the Host header is correctly ignored, so loopback cannot be smuggled).
- Absolute-URI `GET http://127.0.0.1/` + `Host: evil.example` → 200 (effective authority is loopback; safe).
- Duplicate `Host:` headers (either order) → **400 Bad Request** rejected by net/http before the handler.

The security invariant that matters holds: a DNS-rebinding attack forces the browser to send the
*attacker's* hostname in `Host` (because it resolved attacker.com→127.0.0.1), and every non-loopback
hostname is rejected. The shorthand-loopback forms a browser might auto-normalize (`2130706433`, `127.1`)
are rejected rather than allowed — the safe direction. No CONFIRMED bypass on this surface.

## Tests

- `hostcheck_test.go` and `serve_test.go` pin the load-bearing cases: loopback ipv4/localhost/ipv6-`::1`,
  uppercase `LOCALHOST`, 127.0.0.0/8, private-IP reject, **`localhost.evil.example` suffix reject**,
  empty-host reject, configured-host allow, case-insensitive configured host, configured host with
  port. `go test ./cmd/gc/dashboard/... ./internal/api/...` and `./cmd/gc -run Dashboard` all green;
  `go build ./cmd/gc/...` and `go vet` clean.
- Coverage gaps (non-blocking, all resolve in the safe reject direction, so not security holes — just
  not regression-guarded): trailing-dot, userinfo, absolute-URI request line, decimal/short IP. Adding
  2-3 of these to `hostcheck_test.go` would lock in the current correct behavior cheaply.

## Residuals — out of scope for this branch, worth a follow-up

1. **Dashboard binds all interfaces, not loopback** (severity: low). `serve.go:34` binds
   `fmt.Sprintf(":%d", port)` = `0.0.0.0:8080` while the log line says "listening on
   http://localhost". This is **pre-existing** (the diff does not touch the `addr`/bind line). The
   Host allowlist stops browser DNS-rebinding, but a direct LAN attacker using curl can set
   `Host: localhost:8080` and reach the dashboard, because a non-browser client controls the Host
   header freely. Blast radius is limited: the dashboard only serves the static SPA bundle + an
   injected `supervisorURL` (information disclosure of the API endpoint), no control-plane mutations —
   those live on the supervisor API, which is separately Host-checked and, when loopback-bound,
   unreachable from the LAN. Real fix = default-bind `127.0.0.1` and correct the misleading log.
   Recommend a follow-up bead.

2. **Standalone controller mode disables the Host check** (severity: low-moderate, pre-existing,
   deliberate). `controller.go:1363` calls `WithAnyHostAllowed()` for legacy single-city mode. For a
   loopback bind with mutations enabled, DNS-rebinding against that API remains open. Documented as
   "preserves the legacy standalone city API behavior." Not the machine-wide default #2723 targets,
   but it is technically part of the issue's "supervisor API" ask; closing it fully would mean giving
   controller mode the same default-closed allowlist. Out of scope for the dashboard branch.

3. **pprof server has no Host check** (severity: negligible). `supervisor.go:261`, only alive under
   `GC_PPROF=1`. Dev/opt-in.

## Recommendation

Merge either branch as-is for #2723's dashboard half (they are identical; drop the duplicate). File
one follow-up bead to (a) bind the dashboard to `127.0.0.1` by default + fix the log line, and
optionally (b) extend the allowlist to standalone controller mode, to fully close the class across
all non-default run modes. Neither is a blocker for this P1.
