# Controlled cutover runbook (Phase 5)

Faithful replacement of the legacy `maintenance-cycle` order with the Temporal
Run provider. **Dispatch-only**: each cycle creates + slings the review and
author polecats and finishes — no CI/review wait, no gate, **no auto-merge**
(exactly what legacy did; the polecat does the work and halts, a human merges via
the normal pipeline).

Context: the legacy `maintenance-cycle` order has been de-registered/dormant
since 2026-07-06 (tracked separately, bead `gc-qo3`), so this cutover **restores**
the automation with Temporal as sole driver.

## Preconditions (already done)

- `temporal-server.service` + `temporal-maintenance-worker.service` deployed and
  healthy (`make install`), failure-domain independence verified.
- Prompts migrated to `prompts/{review,author}.md`.
- All code tested (`make test`), armed path component-tested.

## The flip (two reversible steps)

1. **Arm the worker** (bind RealAdapter + real selection):
   ```bash
   mkdir -p ~/.config/systemd/user/temporal-maintenance-worker.service.d
   install -m0644 deploy/worker-armed.conf \
     ~/.config/systemd/user/temporal-maintenance-worker.service.d/armed.conf
   systemctl --user daemon-reload
   systemctl --user restart temporal-maintenance-worker.service
   journalctl --user -u temporal-maintenance-worker.service -n5   # expect "ARMED (RealAdapter)"
   ```

2. **Arm the schedule** (start real 120m cycles):
   ```bash
   bash deploy/schedule-arm.sh
   ```

Then **watch the first cycle**: a review + author bead created (labels
`maintenance-cycle:{review,author}`) and slung to `/home/ds/gascity/polecat`.
The in-flight guard prevents a second same-half bead while one is open.

## Retire legacy (ONLY after the clean week — not after the first cycle)

**Do not retire legacy yet.** The plan's P5 gate is "legacy files deleted,
net-negative LOC, one clean week of Temporal-only operation, documented rollback
(**re-arm the paused order** + rebind DryRunAdapter)". The paused order *is* that
rollback, and `gas-city` has no git — deleting it during the soak destroys the
only way back. Legacy is already inert (`[[orders.overrides]] enabled = false` in
city.toml since 2026-07-06, no fire since), so there is no double-dispatch to
race. Temporal-only operation starts at the first armed cycle (2026-07-16).

After a clean week, retire it (net-negative LOC):
```bash
# Rename, do NOT `rm`: change control forbids deleting an order file outright
# (no git to recover from) — see .claude/skills/cityops-city-change-control.
mv orders/maintenance-cycle.toml orders/maintenance-cycle.toml.disabled
# ...with the retirement rationale in the file's own header, then delete
# bin/maintenance-cycle's lifecycle fns (create_and_sling, build_loop_close_metadata,
# open_half_inflight, log_event) — prompts already migrated to prompts/*.md
```
Removing the city.toml override is **city-infra-pl's** call (bead `gc-qo3`) and
carries Stephanie's provisional city.toml gate. Its current comment says to
re-enable once the worktree-provisioning fix lands — that undo condition is
**obsolete**: re-enabling now would double-dispatch against Temporal.
`bin/pr-state-poller` + its order stay: they are the separate OUR-PR copilot loop,
orthogonal to maintenance-cycle. Webhookizing signals / retiring pr-state-poller
is a later, independent step (dispatch-only uses no signals).

## If a sling dies "signal: killed" with the bead created but never routed

Root-caused 2026-07-16 — **it is the armed worker's memory ceiling, not the
sandbox.** `gc session list --json` alone peaks at ~455M RSS, above the base
unit's `MemoryMax=384M` (sized for the shadow worker, which forks nothing). The
armed worker forks the gc toolchain into its own cgroup, so the kernel thrashes
reclaim to hold it under the ceiling (12k+ `memory.events:high`, no OOM kill) and
`gc-sling`'s dead-target probe stalls past the activity's 5m deadline;
`exec.CommandContext` then SIGKILLs it mid-sling. `worker-armed.conf` raises the
ceiling to 1536M/2G; `bin/gc-sling`'s probe is now `timeout`-guarded.

Note for future sandbox debugging: `ProtectHome`/`ProtectSystem`/`PrivateTmp` are
**inert for user units on this box** (`kernel.apparmor_restrict_unprivileged_userns=1`
→ systemd skips the mount namespace silently; the worker shares the host mnt ns).
Verify with `readlink /proc/<pid>/ns/mnt` against your shell before blaming them.

## Rollback (any time)

```bash
bash deploy/schedule-disarm.sh                                   # stop Temporal driving
rm ~/.config/systemd/user/temporal-maintenance-worker.service.d/armed.conf
systemctl --user daemon-reload && systemctl --user restart temporal-maintenance-worker.service
# worker back to shadow (DryRunAdapter). To restore legacy instead: re-register
# orders/maintenance-cycle.toml.
```

## Gate (plan): one clean week of Temporal-only operation, net-negative LOC.
