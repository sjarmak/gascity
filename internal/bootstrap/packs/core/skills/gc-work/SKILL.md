---
name: gc-work
description: Finding, creating, claiming, and closing work items (beads)
---

# Work Items (Beads)

Everything in Gas City is a bead — tasks, messages, molecules, convoys.
The `gc bd` CLI is the primary interface for bead CRUD **in a bd-backed
scope** (a rig whose `.beads/` resolves to the `bd`/Dolt provider). A
file-backed scope — for example a city root whose resolved provider is
`file` — refuses every `gc bd` write verb outright:

```
gc bd: only supported for bd-backed beads providers (resolved "file" for <scope root>)
  hint: check city.toml [beads].provider and any per-rig provider overrides.
```

(The hint text varies by scope — `bdProviderMismatchHint`
(`cmd/gc/providers.go`) has three branches. If the scope carries a
`.gc/beads.json` marker you'll see a hint about that file being stale or the
scope being genuinely file-backed; if `GC_BEADS` is set in the environment
you'll see `GC_BEADS env var overrides the provider. Unset it, or set
GC_BEADS=bd for this scope.`; otherwise you'll see the generic
`check city.toml [beads].provider ...` hint. Whichever hint you see, the
first line — `only supported for bd-backed beads providers (resolved "file"
for ...)` — is the one to match.)

If you hit this, the working directory's resolved provider is `file`, not
`bd`, and there is no bead write path there. `GC_BEADS=bd` and calling `bd`
directly do NOT fix this: they satisfy the provider check but route the
write into whatever store `bd` resolves to on its own, which in a
file-backed city root is a different live store from the `.gc/beads.json`
file the city actually schedules from — the write silently lands somewhere
nothing reads. `gc beads` (note: `beads`, not `bd`) is the read path at a
file-backed scope, but it has no write verbs (`city`, `health`, `list`,
`metadata-cas`, `metadata-guarded-clear`, `show` — see `gc beads --help`),
so there is no `gc`-level bead write in a file-backed root at all.

Create, claim, and close work in a bd-backed **rig** scope instead. If you
need to work at the city root, read with `gc beads list` / `gc beads show`
and do the actual create/claim/close in the rig the work belongs to.

Everything below this section describes the bd-backed case (rigs), where
`gc bd` works as documented.

## Rig-scoped beads

Each rig has its own `.beads/` database with its own ID prefix (e.g.
`fe-` for frontend, `be-` for beads). **A bead must live in the
same database as the agent that will work on it.** When you sling a bead
to a rig-scoped agent, sling operates on the agent's rig database — so
the bead must already exist there. The bead ID prefix tells you which
rig it belongs to.

Use `gc rig list` to see rig names, paths, and prefixes.

## Creating work

**Use `--rig` to create beads in the right database.** If the work will
be dispatched to a rig-scoped agent, create the bead in that agent's rig:

```
gc bd create "title" --rig frontend         # Create in frontend's db (fe- prefix)
gc bd create "title" --rig beads            # Create in beads db (be- prefix)
gc bd create "title"                        # Create in current directory's .beads/
gc bd create "title" -t bug                 # Create with type
gc bd create "title" --label priority=high  # Create with labels
```

## Finding work

```
gc bd list                                # List beads in current .beads/
gc bd list --rig <rigname>                # List beads in a specific rig
gc bd ready                               # List beads available for claiming
gc bd ready --label role:worker           # Filter by label
gc bd show <id>                           # Show bead details
gc ready                                  # Same frontier, federated over every store the city uses
```

On a city that serves a coordination class from its own `[storage]` binding,
`gc bd ready` (and `gc bd list --ready`) is refused with exit 1: it reads one
ledger and the city's ready set spans more than one. Use `gc ready` there. It
takes `--assignee`, `--unassigned`, `--metadata-field`, `--exclude-type`,
`--exclude-label`, `--sort`, `--limit`, `--include-ephemeral`, `--status` and
`--json` — not the label, parent, type or priority selectors `gc bd ready`
forwards.

## Claiming and updating

```
gc bd update <id> --claim                 # Claim a bead (sets assignee + in_progress) — races in a multi-agent city; prefer `gc hook --claim` there
gc bd update <id> --status in_progress    # Update status
gc bd update <id> --add-label <key>=<value>  # Add/update labels
gc bd update <id> --append-notes "progress..."  # Append a note (does not replace existing notes)
```

## Closing work

```
gc bd close <id>                          # Close a completed bead
gc bd close <id> --reason "done"          # Close with reason
```

## Hooks

```
gc hook [agent]                        # Show routed work for an agent (defaults to $GC_AGENT)
gc hook --claim                        # Atomically claim one routed work item onto this agent's hook
```
