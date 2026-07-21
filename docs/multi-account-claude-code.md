# Running multiple Claude Code accounts on one machine

A field guide for anyone who maintains more than one paid Claude subscription
and wants isolated, headless-friendly sessions on each — whether for parallel
agent fleets, separating client work, or just keeping different research
contexts apart.

## The core idea

Claude Code reads its credentials and config from a directory it calls
`CLAUDE_CONFIG_DIR` (defaults to `~/.claude/`). If two sessions share that
directory and both try to refresh OAuth tokens, you get a race that corrupts
`.credentials.json`. The fix is per-account isolation:

- Each account gets its own `CLAUDE_CONFIG_DIR`.
- Shared configuration (skills, hooks, rules, settings) is **symlinked** in
  from a single source of truth.
- A small launcher script flips `CLAUDE_CONFIG_DIR` at exec time, so
  `claude-1`, `claude-2`, etc. all run the same `claude` binary with
  isolated credentials but identical config.

```
~/.claude/                              # shared config (one source of truth)
├── skills/                              ├── commands/                            ├── hooks/                               ├── settings.json                        └── ...

~/.claude-homes/
├── account1/.claude/                   # per-account
│   ├── .credentials.json   ← isolated  │   ├── skills/    ← symlink to ~/.claude/skills    │   ├── hooks/     ← symlink   │   └── settings.json ← symlink
├── account2/.claude/
│   ├── .credentials.json   ← isolated  │   └── ... (same symlinks)
```

## A launcher that doesn't break

There are four concurrency / headless gotchas that bite if you skip them:

1. **`flock` the bootstrap.** First-launch work (creating symlinks, writing
   onboarding state) must be serialized per account. Two parallel launches
   on the same account will otherwise race on the JSON writes and corrupt
   `.claude.json` or `settings.json`.
2. **Atomic JSON writes.** Use `tmp file + os.replace()` for every config
   write. A partial write is just as bad as a race.
3. **Pre-accept the dialogs.** Headless / agent sessions hang forever on
   the theme picker, the dangerous-mode prompt, and the "trust this folder?"
   dialog. Pre-write the relevant flags before exec'ing `claude`.
4. **Close the lock fd before exec.** `flock` fd 9 should be released
   before handing control to `claude` so the agent process doesn't inherit
   it.

A reference launcher in pseudocode (real implementation lives in
`bin/claude-account` in this repo):

```bash
#!/usr/bin/env bash
set -euo pipefail

ACCOUNT="${1:?Usage: claude-account <N> [args...]}"
shift

export CLAUDE_CONFIG_DIR="$HOME/.claude-homes/account${ACCOUNT}/.claude"

if [ ! -f "$CLAUDE_CONFIG_DIR/.credentials.json" ]; then
  echo "ERROR: no credentials for account$ACCOUNT" >&2
  exit 1
fi

# Serialize the bootstrap section per account.
ACCOUNT_HOME="$(dirname "$CLAUDE_CONFIG_DIR")"
mkdir -p "$ACCOUNT_HOME"
exec 9>"$ACCOUNT_HOME/.claude-account.lock"
flock 9

# Symlink shared config from ~/.claude/ on first launch.
for item in commands hooks skills mcp-configs settings.json rules agents; do
  src="$HOME/.claude/$item"
  dest="$CLAUDE_CONFIG_DIR/$item"
  if [ -e "$src" ] && [ ! -e "$dest" ]; then
    ln -s "$src" "$dest"
  fi
done

# Pre-accept onboarding + dialogs (.claude.json lives one level up).
# Use atomic JSON writes — see real implementation for details.
ensure_onboarding_state "$ACCOUNT_HOME/.claude.json"
ensure_trust_dialog    "$ACCOUNT_HOME/.claude.json" "$(pwd)"
ensure_skip_dangerous  "$CLAUDE_CONFIG_DIR/settings.json"

# Release the lock before exec.
exec 9>&-

exec claude "$@"
```

The fields the helpers must set (use atomic writes for all of these):

- `.claude.json` → `hasCompletedOnboarding: true`,
  `projects[<cwd>].hasTrustDialogAccepted: true`
- `settings.json` → `skipDangerousModePermissionPrompt: true` (only relevant
  if you're going to launch with `--dangerously-skip-permissions`)

The full reference implementation in `bin/claude-account` adds version /
install-method fields and uses Python for the JSON writes; the shape above
is the minimum viable version.

## Per-account convenience wrappers

Once the launcher exists, each account gets a one-liner on `$PATH`:

```bash
# ~/.local/bin/claude-1
#!/usr/bin/env bash
exec "$HOME/path/to/claude-account" 1 "$@"
```

Now `claude-1`, `claude-2`, etc. each open isolated sessions on their
respective accounts. Behavior is identical to running plain `claude`,
except the credential is account-scoped.

## Orchestrating multiple sessions

The orchestration pattern this repo uses: each agent is a real `claude`
subprocess launched by the orchestrator, with `CLAUDE_CONFIG_DIR` set to
the appropriate account home. The orchestrator never touches OAuth
tokens, never makes API calls itself — it just sets the env var and execs
the official Claude Code CLI.

This is important from a policy standpoint (see "Anthropic policy notes"
below). A wrapper that launches Claude Code is fundamentally different
from a third-party client that uses Claude Code's OAuth token to make
its own API calls.

## Capacity-aware routing across accounts

If you have multiple subscriptions and want to dispatch work to whichever
account currently has the most headroom, the routing decision needs a
quota signal per account. Three approaches in rough order of complexity:

### 1. Reactive (simplest)

Dispatch optimistically. When a session reports "Claude usage limit
reached," mark that account on cooldown until the documented reset time
and route the next request elsewhere.

- **Pros:** no proactive probing, no extra plumbing, just error handling.
- **Cons:** at least one request per cooldown window has to fail before
  you learn the account is capped. Fine for most workloads.

### 2. Session-derived

Parse Claude Code session output for the limit-reached marker (and any
other capacity-related messages the client emits). Maintain a per-account
"last seen capped at" timestamp; treat anything within the documented
5-hour or 7-day window as still-capped.

- **Pros:** uses signals Claude Code itself emits, no extra API surface.
- **Cons:** needs structured access to session transcripts; signal is
  binary (capped / not capped) rather than continuous utilization.

### 3. Header-based probing

The Anthropic API returns rate-limit utilization headers
(`anthropic-ratelimit-unified-5h-utilization` and similar) on every
response. Some users probe these directly with a minimal request.

- **Pros:** continuous utilization signal, lowest latency to detect a
  cool account.
- **Cons:** uses OAuth tokens outside the official client — check the
  current Acceptable Use Policy before relying on this.

For most users approach (1) is enough; (2) is a nice upgrade if you
already have session-output plumbing.

## A note on Anthropic's terms

Holding multiple paid subscriptions is allowed; sharing one account
across multiple humans is not. Routing your own work across your own
accounts is normal capacity management. Consumer OAuth tokens are
sanctioned for use in the official Claude Code and Claude.ai clients,
not in third-party clients.

Terms change — read the primary sources before relying on this summary:

- [Consumer Terms of Service](https://www.anthropic.com/legal/consumer-terms)
- [Acceptable Use Policy](https://www.anthropic.com/legal/aup)

## Reference implementation

The launcher (`bin/claude-account`) and the per-account wrappers in this
repo are the working version of the patterns above. Read them as a
reference, not a blueprint — your project layout, account count, and
orchestration shape will differ.
