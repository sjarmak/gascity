# Architecture diagram (LikeC4)

Architecture-as-code model of **Gas City**, rendered with
[LikeC4](https://likec4.dev). The model is the source of truth across
[`spec.c4`](spec.c4) (element kinds, tags, deployment node kinds),
[`model.c4`](model.c4) (the system), and [`views.c4`](views.c4) (structure,
walkthrough, and risk views), with the deployment model in
[`deployment.c4`](deployment.c4). The narrative companions are the
current-state architecture docs under
[`engdocs/architecture/`](../engdocs/architecture/index.md) and the
six-primitive overview in
[How Gas City Works](../docs/getting-started/how-gas-city-works.md).

Every element `link`s to its source (`cmd/gc/…`, `internal/…`) and, where one
exists, to the relevant current-state architecture doc — so any box in the
explorer is one click from the code and the doc behind it.

## Delivery state is tagged, not guessed

Every element carries a tag so **planned and research work renders distinctly
from what is already built** (legend in `spec.c4`). Tags are assigned from real
evidence in the working tree — wired-in runtime paths with tests are `#built`;
contracts defined but not yet connected are `#planned`.

| Tag | Meaning | Render |
|---|---|---|
| `#built` | code path exists, tested, and wired into a runtime path | solid |
| `#evolving` | built, but the contract/shape is still moving | solid |
| `#planned` | designed; defined but not yet wired into the runtime path | **dashed, dimmed** |
| `#research` | speculative / experimental track | **dashed, indigo** |

Notable non-`#built` items in the model:

- **`reviewQuorum` (`#planned`, `#risk`)** — `internal/reviewquorum` defines the
  durable two-lane review-synthesis contract, but the Go finalizer is **not yet
  invoked by formula synthesis** (the formula writes the summary state
  directly). Contract and consumer are defined but not connected.
- **`cloudflare` (`#research`)** — `internal/runtime/cloudflare` is an
  experimental Provider over a Cloudflare Worker runtime API.
- **`ralph`, `k8s`, `acp`, `cachingStore`, `readModel`, `packFetch`,
  `githubMonitor`, `sourceWorkflow` (`#evolving`)** — built and exercised, but
  the shape or coverage is still actively moving.

## Views

**Structure** — the static map:

| View | Scope |
|---|---|
| `index` | system landscape — Gas City in context of agent runtimes, the bd/Dolt backend, GitHub, and rig repos |
| `gascitySystem` | the Gas City system decomposed into its 11 containers |
| `controllerContainer` | controller runtime internals (loop, reconciler, pool, order dispatch, wisp GC, supervisor) |
| `dispatchContainer` | sling, control dispatcher, ralph, graph routing, telemetry |
| `formulaContainer` | formula compiler, molecule materialization, convergence, review-quorum |
| `beadsContainer` | the Store interface and its bd/Dolt, file, and mem backends |
| `runtimeContainer` | the Provider interface and the tmux / subprocess / k8s / acp / cloudflare providers |
| `configContainer` | config loader, pack composition, remote fetch, revision/watch |
| `ordersContainer` | order discovery/triggers, GitHub PR monitor, source workflow |
| `apiContainer` | HTTP + SSE handlers and the read-model cache |
| `planned` | planned + research work, with built dependencies dimmed |
| `deployment` | where each piece runs — supervisor/controller + sessions on the city host, bd/Dolt backend, remote runtimes, rigs, GitHub |

**Walkthrough flows** (dynamic / numbered-step views) — the narrative spine for
a design-review walkthrough:

| View | Flow |
|---|---|
| `controllerTick` | one reconciliation tick (reload → pool scale → reconcile sessions → wisp GC → order dispatch) |
| `slingFormula` | sling a formula → compile/materialize the graph → the control dispatcher drives it to completion |
| `workBead` | an agent claims and works a bead (the loop closes through shared bead-store state) |
| `orderFires` | an order's trigger is met and it slings a formula |

**Risk lens:**

| View | Scope |
|---|---|
| `risks` | the `#risk`-flagged elements with each open question stated in-box (currently the review-quorum finalizer-not-wired gap) |

### Running the walkthrough

For a design review, present in this order: `index` → `gascitySystem` (orient on
structure) → the four walkthrough flows in sequence (what actually happens) →
`deployment` (where it runs) → `risks` (what to probe) → `planned` (what's next).
In `npx likec4 start`, the dynamic views animate step-by-step.

## Viewing & regenerating

```bash
# Interactive, hot-reloading explorer (recommended)
npx likec4 start architecture

# Re-export static PNGs (needs a one-time browser download:
#   npx playwright install chromium-headless-shell)
npx likec4 export png architecture -o architecture/exports

# Validate the model (strict — the source of truth for correctness)
npx likec4 validate architecture
```

### Viewing the interactive explorer over SSH (headless remote)

`likec4 start` serves a Vite dev server on `localhost:5173`. From a headless
remote, forward that port to your laptop and open it locally — three options,
easiest first:

1. **VS Code / Cursor Remote-SSH** — run `npx likec4 start architecture` in the
   integrated terminal; the editor auto-forwards 5173 and offers "Open in
   Browser". Nothing else to configure.
2. **SSH local port-forward** — on your laptop:
   ```bash
   ssh -N -L 5173:localhost:5173 user@remote   # leave running
   ```
   then on the remote `npx likec4 start architecture` and open
   <http://localhost:5173> locally. (Already in an SSH session? Add the tunnel
   without reconnecting: press `~C` then type `-L 5173:localhost:5173`.)
3. **Bind + reach directly** — `npx likec4 start architecture --listen 0.0.0.0`
   and browse to `http://<remote-ip>:5173` (only if that port is reachable /
   firewall-open; the tunnel in option 2 is safer).

No browser at all? `npx likec4 export png architecture` needs a headless
Chromium (`npx playwright install chromium-headless-shell`) but no display —
`scp` the PNGs down, or view inline if your terminal supports images.
