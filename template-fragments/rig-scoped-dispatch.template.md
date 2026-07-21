{{ define "rig-scoped-dispatch" -}}

To dispatch:

```bash
# Atomic in-rig work (single bead → single worker):
gc-sling <your-worker-pool> <bead-id>

# Convoy-creating formulas (epic → multi-bead graph; in-rig only):
gc-sling <your-worker-pool> --on mol-decompose --var issue=<epic> --var rig=<your-rig> --stdin
gc-sling <your-worker-pool> --on mol-pr-from-issue --var issue_number=<N> --stdin
```

Use the `gc-sling` wrapper — it auto-injects `--nudge`. Then **verify the
worker actually picked it up** — a bead can be routed but sit unclaimed if
no worker session is awake:

```bash
gc bd --rig <your-rig> show <bead-id>   # expect IN_PROGRESS within a few minutes
```

If it stays `open` with `gc.routed_to` already set, the pool is asleep.
`gc sling` treats an already-routed bead as an idempotent skip and will NOT
re-nudge — re-slinging a stuck bead is a silent no-op. Unstick it by waking
a worker and nudging it onto the bead:

```bash
gc session wake <your-worker-pool>-1
gc session nudge <your-worker-pool>-1 "Claim and work routed bead <bead-id>." --delivery immediate
```
{{- end }}
