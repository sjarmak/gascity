# maintenance pack

Reusable infrastructure layer for multi-agent cities. Provides the dog
worker (shutdown dance) and a set of exec orders that handle mechanical
housekeeping with no LLM in the loop.

Include this pack alongside any domain-specific pack.

## Orders

Each order is registered as a TOML in `orders/` with its companion shell
script in `assets/scripts/`.

| Order | Script | Purpose |
| ----- | ------ | ------- |
| `gate-sweep` | `gate-sweep.sh` | Evaluate and close pending gates (timer / condition / GitHub). |
| `orphan-sweep` | `orphan-sweep.sh` | Reset beads assigned to dead config-level agents. |
| `stale-claim-reaper` | `stale-claim-reaper.sh` | Reset `in_progress` beads abandoned by dead worker processes. |
| `prune-branches` | `prune-branches.sh` | Delete merged / gone-tracked `gc/*` branches in each rig. |
| `wisp-compact` | `wisp-compact.sh` | Compact closed wisps in the bead store. |
| `cross-rig-deps` | `cross-rig-deps.sh` | Convert satisfied cross-rig blocks to related. |
| `spawn-storm-detect` | `spawn-storm-detect.sh` | Escalate beads stuck in a recovery loop. |
| `mol-dog-jsonl` | `jsonl-export.sh` | Export Dolt databases to JSONL archive. |
| `mol-dog-reaper` | `reaper.sh` | Reap stale wisps + auto-close stale issues. |

## stale-claim-reaper

`bd update --claim` atomically sets `assignee` and `status=in_progress`
but has no liveness signal: if a worker process dies between claim and
the matching `bd close`, the claim sticks forever. `orphan-sweep` does
not catch this — it handles config-level orphans (the assignee is not
in `city.toml`); the worker-process-death case has an assignee that
*is* a valid configured agent slot.

The reaper resets in-progress claims when ALL of the following hold:

- `status == in_progress`
- `assignee` is non-empty
- `updated_at` older than the rig's `stale_threshold` (default `24h`)
- no commit in the rig's git log mentions the bead ID since the claim
- the bead does not carry the policy's `exclude_metadata` flag

The action is `bd update --status=open --assignee="" <bead>` (NOT
`bd close` — close discards potentially-valid work).

### Dormant by default

A rig is processed only if it has `<rig>/.beads/stale-claim-policy.yaml`.
Rigs without a policy file are skipped silently.

### Dry-run by default

The reaper records its decisions in the audit log but does NOT call
`bd update` unless `STALE_CLAIM_REAPER_APPLY=1` is set in the environment
(or the deprecated synonym `GC_STALE_CLAIM_REAPER_APPLY=1`).

To opt into live reaping for a deployment, set the env var on the
controller process — for example, in `city.toml`:

```toml
[controller.env]
STALE_CLAIM_REAPER_APPLY = "1"
```

### Policy file

`<rig>/.beads/stale-claim-policy.yaml`:

```yaml
# How long a claim must be idle before it's eligible for reaping.
# Accepts Go-style durations (h / m / s, or combinations).
stale_threshold: "24h"

# Optional: skip beads carrying this metadata key set to "true".
# Useful for genuinely long-running work that wouldn't produce
# intermediate commits.
exclude_metadata: long_running

# Optional: customize the git-log search pattern. Default is the bead
# ID literal; change only if you embed bead IDs differently in commit
# messages.
match_commit_pattern: "{bead_id}"
```

The script uses a small built-in YAML scalar parser. The schema is
deliberately flat — no lists, no nested objects.

### Audit log

Every scan / decision / reap is appended as one JSON line to:

```
$GC_PACK_STATE_DIR/stale-claim-audit.jsonl
```

(default: `$GC_CITY_RUNTIME_DIR/packs/maintenance/stale-claim-audit.jsonl`)

Audit fields per line:

| Field | Meaning |
| ----- | ------- |
| `ts` | ISO-8601 UTC time of the audit entry. |
| `action` | One of `reap-applied`, `reap-dry-run`, `reap-failed`, `reap-skipped-excluded`, `reap-skipped-not-stale`, `reap-skipped-recent-commit`. |
| `apply_mode` | `dry-run` or `apply`. |
| `rig` | Absolute path to the rig the bead was inspected under. |
| `bead_id` | Bead ID. |
| `assignee` | Pre-reap assignee (cleared by `reap-applied`). |
| `updated_at` | Bead's `updated_at` at scan time. |
| `age_seconds` | Seconds since `updated_at`. |
| `reason` | Free-form explanation for the action. |

### Cadence

`stale-claim-reaper` runs on a 1h cooldown by default. Tune via the
`interval` field in `orders/stale-claim-reaper.toml` if you need a
different cadence; the reaper is the failsafe behind the worker
liveness gap, not a primary mechanism.
