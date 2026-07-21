# RCA: stale GITHUB_TOKEN env-shadow blocked fleet gh auth

- **Bead:** dr-e6l (city-infra-pl) — "Fix stale GITHUB_TOKEN env-shadow blocking fleet gh auth"
- **Date:** 2026-07-13
- **Author:** city-infra-polecat (gc-480328)
- **Severity:** fleet-wide `gh` auth failure; sole blocker on the maintenance gh queue (#2723, #3958, #4003, #3420)

## Symptom

`gh` authenticated as the wrong (dead) identity for every process spawned under
the `ds-research` tmux server:

```
plain            gh api user  -> {"message":"Bad credentials"}
env -u GITHUB_TOKEN gh api user -> sjarmak        # valid stored PAT
```

A dead `ghp_` PAT (len 40, sha256 prefix `3a8c0a0024644f95`, masked `ghp_…04b`)
was exported into the running process tree and shadowed the valid PAT stored in
`~/.config/gh/hosts.yml`. `gh` prefers the `GITHUB_TOKEN` env var over stored
credentials, so the dead token won everywhere.

## Root cause

The dead token lived in the **tmux server global environment on the
`ds-research` socket**:

```
tmux -L ds-research show-environment -g GITHUB_TOKEN
  -> GITHUB_TOKEN=ghp_…04b        # matched the shadowed token exactly
```

Every pane/window the tmux server spawns inherits its global environment, so all
gc-managed agent sessions (which live as panes under this socket) picked up the
dead token. It was **not** in any re-injecting source — verified absent from:

- city launchers / hooks / orders / agents / `city.toml` / `pack.toml`
- `~/.config/systemd/user/` supervisor drop-ins
- `~/.bashrc`, `~/.bash_profile`, `~/.profile`, `~/.zshrc`, `/etc/environment`
- `systemctl --user show-environment` (manager env)

So it was a one-time manual `tmux set-environment -g` that persisted in the
long-lived tmux server. Nothing re-injects it → removing it from the tmux global
env is a permanent fix.

## Fix applied

```bash
tmux -L ds-research set-environment -gu GITHUB_TOKEN   # exit 0
```

- Removes the var from the tmux server global env. **Affects newly spawned
  panes/processes only** — zero disruption to running processes/orders.
- Reversible (`tmux -L ds-research set-environment -g GITHUB_TOKEN <val>`), but
  there is no reason to restore a revoked token.

## Verification (empirical)

Fresh process spawned under the `ds-research` socket after the fix:

```
FRESH_SPAWN_TOKEN=<unset>
gh api user -q .login -> sjarmak
```

Stored fallback credential confirmed present and valid:

```
gh auth status (GITHUB_TOKEN unset)
  -> ✓ Logged in to github.com account sjarmak (~/.config/gh/hosts.yml), full scopes
```

My own session shell was also cleared (`unset GITHUB_TOKEN`) and now
authenticates as `sjarmak`.

## Residual / follow-up

- **31 already-running tmux panes** still carry the dead token in their
  already-materialized environment. They are NOT auto-fixed by the global unset;
  each sheds it on its next natural session reset/cycle.
- **No blanket restart performed** (policy: targeted, not blanket — CLAUDE.md).
  Immediate per-command workaround for any running agent that needs `gh` before
  it cycles: `env -u GITHUB_TOKEN gh …`.
- The maintenance gh queue's PR actions are separately gated on review +
  Stephanie, so nothing auto-fires from a stale-env session in the meantime.
- **Optional accelerator (mayor decision):** targeted `gc session reset
  gascity-maintenance-pl` (gc-480211) would give that session clean auth
  immediately without a blanket fleet restart. Left to the mayor since it
  interrupts a running agent.
