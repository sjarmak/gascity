# Slack adapter as a slack-pack `[[service]] proxy_process`

**Status:** design — not implemented.
**Tracking:** `examples/oversight-rig/HANDOFF.md` open work item 2.
**Related:** open work item 5 (systemd user service) — subsumed if this lands.

## Goal

Move the Go Slack adapter from a hand-managed `nohup` process owned by
the operator's shell into a slack-pack-declared `[[service]]` of kind
`proxy_process`, so gc owns its lifecycle the same way the discord pack
manages discord-interactions. Side benefits: `gc service list`
integration, structured logs under `.gc/services/slack/logs/`,
restart-with-backoff out of the box, and the city-as-directory model
captures the slack adapter as part of its definition rather than as
external machine state.

## Today (concrete state on this host)

```
adapter:    /home/ds/gascity/examples/oversight-rig/adapter/gc-slack-adapter
            (PID 2582270; started by ./run.sh + nohup; logs in /tmp/...)
public:     TCP :8775           — /slack/events, /healthz
internal:   TCP 127.0.0.1:8776  — /publish
funnel:     tailscale funnel :443 → :8775
secrets:    ~/.config/gc-slack-adapter/env
            (SLACK_BOT_TOKEN, SLACK_SIGNING_SECRET, SLACK_WORKSPACE_ID,
             GC_API_BASE_URL=http://127.0.0.1:8372, GC_CITY_NAME=ds-research)
gc reach:   gc /v0/city/{city}/extmsg/outbound → adapter /publish (HTTP loopback)
self-reg:   adapter POSTs /v0/city/{city}/extmsg/adapters at startup,
            CallbackURL = http://127.0.0.1:8776 (= internalCallbackURL)
```

## What `proxy_process` actually requires

From `internal/workspacesvc/proxy_process.go` and
`internal/config/service.go`:

- The child process must bind a **Unix domain socket** at the path the
  controller passes via `$GC_SERVICE_SOCKET`. gc reverse-proxies HTTP
  through `/svc/{name}` to that socket via `httputil.NewSingleHostReverseProxy`.
- Controller-injected env: `GC_SERVICE_NAME`, `GC_SERVICE_STATE_ROOT`,
  `GC_SERVICE_RUN_ROOT`, `GC_SERVICE_SOCKET`, `GC_SERVICE_URL_PREFIX`,
  `GC_SERVICE_PUBLIC_URL`, `GC_SERVICE_VISIBILITY`,
  `GC_PUBLISHED_SERVICES_DIR`. Also inherits `os.Environ()`.
- Lifecycle: `Setpgid: true`; SIGTERM the process group → 2s wait → SIGKILL.
  Logs go to `$state_root/logs/service.log`. Restart backoff is 1s.
  Ready signal = UDS bind succeeds; if `health_path` set, gc HTTP-GETs it
  over the UDS before flipping state to `ready`.
- StateRoot must stay under `.gc/services/`.
- Publication: `private` (default), `public`, or `tenant`.

The proxy_process model accepts that the child can bind **additional
listeners** (TCP, file, whatever) — the controller only owns the UDS.

## Tension surface

Three frictions force the design.

### 1. Webhook ingress is public TCP, by Slack's definition

Slack's Event API requires a **public HTTPS URL** that accepts POST from
Slack's IP ranges, with the request authenticated by the signing secret
(HMAC-SHA256 over body). Today that URL terminates at the adapter's
`:8775` via Tailscale Funnel. proxy_process gives a UDS, which Slack
cannot reach.

Three resolution options:

(a) **Funnel routes to gc, gc routes to adapter UDS.** Move Funnel from
`:443 → :8775` to `:443 → gc-listener`. Slack POSTs land at
`https://<host>.<tailnet>.ts.net/svc/slack/slack/events`, gc proxies to
UDS. Forces gc to be public-facing on its tailnet domain. Tenant gating
exists (`publication.visibility = tenant`), but Slack's webhook carries
no tenant auth — only the signing secret — so the route must be `public`,
which exposes `/svc/slack/*` (and only that, under the `/svc/{name}` mount
isolation) to the internet. Trust boundary moves: gc is now a public
HTTP entry point, even if only `/svc/slack/*` is reachable.

(b) **Adapter binds both UDS and TCP, Funnel unchanged.** Adapter still
binds public TCP `:8775` for `/slack/events`, plus the controller-mandated
UDS for `/publish`. proxy_process model accepts this — the controller
only owns the UDS. The internal TCP `:8776` goes away entirely (replaced
by UDS). Funnel keeps pointing at `:8775`. Smallest change; hybrid
ownership (gc owns UDS lifecycle, adapter owns its public TCP binding +
Funnel registration is out-of-band).

(c) **All-UDS adapter, gc terminates TLS, gc registers Funnel.** Adapter
binds UDS only. gc terminates TLS for the public route, manages Funnel
reconciliation, and proxies `/svc/slack/slack/events` to UDS. Cleanest
but largest change: gc has no TLS termination today and no Funnel
controller. Out of scope for v1.

**Recommendation: (b).** Smallest blast radius, no gc surface-area
expansion, fully reversible (adapter falls back to TCP-only if
`$GC_SERVICE_SOCKET` is empty).

### 2. Webhook auth stays at the adapter regardless

HMAC verification against `SLACK_SIGNING_SECRET` happens inside the
adapter today (`/slack/events` handler). Whether Slack→adapter is direct
TCP (option b) or Slack→gc→UDS (a/c), the secret stays adapter-side and
gc never sees plaintext signing material. No migration cost on this axis.

### 3. Self-registration callback URL changes shape

Today the adapter registers itself at startup with
`CallbackURL = http://127.0.0.1:8776` so gc's `extmsg.AdapterRegistry`
knows where to POST `/publish` for outbound calls. Under proxy_process,
gc reaches the adapter via its own `/svc/slack/...` mount, so the
registered callback URL must become something like
`http://127.0.0.1:8372/svc/slack/publish` (where `:8372` is gc's API
listener). The adapter can compose this from `$GC_API_BASE_URL` and
`$GC_SERVICE_URL_PREFIX`. **All registration plumbing already lives in
the adapter — the only change is what URL it computes.**

## Design (option b)

### Phase A — adapter changes (smallest possible)

**Adapter behavior change (single conditional):** if
`$GC_SERVICE_SOCKET` is set, bind the UDS for `/publish` instead of the
internal TCP `:8776` listener. Public listener (`:8775`,
`/slack/events`) unchanged. Self-registration computes
`CallbackURL = $GC_API_BASE_URL + $GC_SERVICE_URL_PREFIX + /publish`
when the UDS path is set, otherwise keeps the legacy internal TCP URL.

```
                 Slack
                   │   POST /slack/events
                   ▼
         tailscale funnel :443
                   │
                   ▼
        ┌────────────────────────┐
        │ adapter (proxy_process)│
        │  TCP :8775  /events    │
        │  UDS $SOCK  /publish   │
        └────────────────────────┘
                   ▲
                   │ /svc/slack/publish (gc proxies via UDS)
                   │
                  gc API :8372
```

**Adapter file changes:**

- `loadConfig` reads new env: `serviceSocket = os.Getenv("GC_SERVICE_SOCKET")`.
- If `serviceSocket != ""`:
  - Skip the TCP `internalListen` listener entirely.
  - Bind a `net.Listen("unix", serviceSocket)` listener and serve
    `/publish` on it.
  - Compute self-registration `CallbackURL` from `$GC_API_BASE_URL` +
    `$GC_SERVICE_URL_PREFIX` + `/publish`.
  - Optionally serve `/healthz` on the UDS too so the controller's
    `health_path` probe can hit it without a separate listener.
- If `serviceSocket == ""`: legacy mode unchanged.

This keeps the script-managed and proxy_process-managed deployments on
one binary; cutover is reversible by restarting without the env var.

**Pack changes (`examples/slack-pack/pack.toml`):**

```toml
[[service]]
name = "slack"
kind = "proxy_process"
publication.visibility = "private"

[service.process]
command = ["./adapter/gc-slack-adapter"]
health_path = "/healthz"
```

Open question: where does the binary come from in a pack consumer's
deployment? Two options:
- (i) Pack ships a build step (Makefile target or pre-build hook) that
  invokes `go build` in `./adapter/`. Pack consumer runs it once at
  `gc city init`.
- (ii) Pack expects a prebuilt binary at `./adapter/gc-slack-adapter`
  and documents how to build it.

(ii) is consistent with proxy_process's "command is just argv" contract;
(i) overloads pack init with build-toolchain assumptions. Recommend
**(ii)** with a small `make build` doc note.

**Secrets handling:** keep the adapter reading
`~/.config/gc-slack-adapter/env` itself. proxy_process inherits
`os.Environ()`, which on the user's shell already has those vars (or
the file is sourced before `gc start`). No secrets in pack.toml or
city.toml. Document the env file location in pack README.

**Funnel:** unchanged. `tailscale funnel :443 → :8775` still works
because the adapter still binds `:8775` (just not as a controller-owned
listener). When the adapter is killed by `gc stop`, the Funnel rule
becomes a 502 until next `gc start` — same behavior as today modulo
who's running the adapter.

### Phase A — gc changes (none required)

Existing `proxy_process` infrastructure handles everything. The
`extmsg.AdapterRegistry` already accepts arbitrary `CallbackURL`s; it
just gets a different value at registration time.

### Phase A — cutover

```
1. gc supervisor running, adapter running on TCP-only mode (today's state)
2. Build new adapter binary with UDS support
3. Add [[service]] block to slack-pack pack.toml; gc loads it on next reload
4. Stop the manually-started adapter:  pkill -f gc-slack-adapter
5. gc supervisor reload                # picks up new service definition
6. Verify: gc service list             # shows slack: ready
   curl --unix-socket $sock http://x/healthz  # 200
7. Verify outbound: POST /v0/city/{city}/extmsg/outbound — should still
   land in Slack DM
8. Verify inbound: send Slack DM, check that adapter still receives
   on TCP :8775 and POSTs to gc /extmsg/inbound
```

Rollback: remove `[[service]]`, restart manual `./run.sh`. Adapter
ignores `$GC_SERVICE_SOCKET` when unset.

### Phase B — secrets in pack config (later, optional)

Today: env file. Possible future: pack.toml declares which env vars the
service needs (just names, not values), city.toml carries values via a
secrets table or `${SHELL_VAR}` references. Out of scope for v1.

### Phase C — public ingress through gc (later, optional)

If/when gc gains TLS termination + Funnel reconciliation, migrate from
option (b) to option (a): adapter binds UDS only, Funnel points at gc,
gc proxies `/svc/slack/slack/events` to UDS. Trust boundary review
required at that time. Out of scope for v1.

## Open questions

1. **Logs:** today the adapter logs to `/tmp/gc-slack-adapter/run.log`.
   Under proxy_process, logs go to
   `.gc/services/slack/logs/service.log` (controller-managed). Should
   we run anything to ingest the existing log into events on cutover,
   or just accept a fresh log? (Recommendation: fresh log; the existing
   one is a debugging artifact, not state.)
2. **Health endpoint:** today there's `/healthz` on TCP `:8775`. We need
   `/healthz` on the UDS for the controller's `health_path` probe to
   work without granting it TCP reach. Trivial — add the route to the
   UDS server too.
3. **`SourceDir` / pack provenance:** when the pack is loaded as
   `[imports.slack]` in city.toml, does `service.SourceDir` get
   populated correctly so `state_root` collision detection works?
   Should be tested at cutover.
4. **Concurrent adapter instances:** if the operator forgets to stop
   the manual adapter before `gc start` brings up the proxy_process
   one, both will try to register `(provider=slack, account_id=T...)`.
   The second registration overwrites the first; `extmsg/outbound`
   then targets only one of the `/publish` URLs (last-write-wins).
   Document this in the cutover doc — not a bug, just a foot-gun.
5. **Tailscale Funnel as machine state:** Funnel rules survive reboot
   on most distros, but they're not declared anywhere in the city.
   Should pack docs include a `tailscale funnel` recipe? Or punt to
   Phase C? (Recommendation: document in pack README, no automation
   yet.)

## Decision summary

- **Adopt option (b)** — UDS for `/publish`, keep public TCP for
  `/slack/events`, no gc TLS surface change.
- **Single feature flag** in the adapter:
  `$GC_SERVICE_SOCKET != ""` selects UDS mode; legacy TCP mode preserved.
- **Pack.toml gets a `[[service]]` declaration** with the proxy_process
  command and a UDS health probe.
- **Secrets stay in `~/.config/gc-slack-adapter/env`** for now.
- **Funnel config stays out of band** for now — possible future Phase C.

Estimated scope: ~80 lines of adapter Go (UDS listener + cond URL +
healthz on UDS), ~10 lines of pack.toml, plus pack README updates.
Reversible via env var, no gc-side changes, no schema migration.
