# dr-imrf — Obsidian write authorization trace and fail-closed boundary

Date: 2026-07-20  
Mode: read-only investigation; vault evidence preserved unchanged

## Finding

The `gascity-packs-pl` write was caused by a real conflict between a mandatory
standing write directive and a narrower current-task prohibition:

1. The shared `VAULT_NOTES` prompt fragment says `YOU write this one`, `Write
   on every status change`, `Log every issue`, and `Fix any drift found`.
2. `gascity-packs-pl` directly renders that fragment into its standing prompt.
3. The loaded global `obsidian-vault` skill says to work on the vault as plain
   Markdown and explicitly warns that every write propagates to Stephanie's
   devices within seconds, with no Git history or undo.
4. The active bead, mayor mail `gc-525222`, and the user's current-task message
   all prohibited external action. The bead retained
   `external_action_authorized=false`.
5. No user message authorized a vault mutation before the write.

The PL treated the standing mandatory verbs as authorization and did not
classify live-synced personal-vault mutation as an external action. Its later
acknowledgement states that this interpretation was too broad and that it should
have recorded progress only on the bead unless the user explicitly authorized
a vault update.

This was an authorization-precedence defect, not an absent instruction or an
undisclosed sync mechanism. The standing prompt commanded the write; the skill
made its external effect explicit; the current task denied it. The deny should
have controlled.

## Exact instruction chain

### 1. Source-of-truth standing prompt

`template-fragments/pl-periodic-directives.template.md:255-279`:

```text
### DIRECTIVE: VAULT_NOTES (standing, Stephanie 2026-07-05)

Your project has two notes in Stephanie's Obsidian vault under
`/home/ds/brain/Projects/` (writable plain markdown; edits sync to her devices
within seconds — treat as production, never bulk-delete):
...
- `<Project> Issues Log.md` ... YOU write this one.
...
1. Write on every status change ... update the ELI5/open-work sections in
   place and append a dated daily-log line.
2. Log every issue ... goes in the Issues Log ...
3. Morning check ... Fix any drift found ...
```

The prompt pointer `prompts/pl-periodic-directives.md:1-24` identifies that
fragment as the single source of truth and says it is included verbatim in all
eight rig-PL templates.

### 2. `gascity-packs-pl` inheritance

`agents/gascity-packs-pl/prompt.template.md:331-334` identifies the role and
renders:

```text
Agent: {{ .AgentName }}
Rig:   gascity-packs (gastownhall/gascity-packs)

{{ template "pl-periodic-directives" . }}
```

`city.toml:222-225` binds the always-on `gascity-packs-pl` named session to
that template. `agents/gascity-packs-pl/agent.toml:1-12` supplies its work
directory and Amp provider; neither adds an authorization gate.

### 3. Loaded skill

`/home/ds/.claude/skills/obsidian-vault/SKILL.md:8-18` says:

```text
Stephanie's personal Obsidian vault, mirrored to `/home/ds/brain` by Syncthing.
Work on it directly as plain markdown files ...
Every write here propagates to all her devices within seconds. There is no git
history and no undo. Treat the vault as production data ...
```

The PL loaded this skill at 2026-07-20T19:25:04.800Z. It is operational safety
guidance, not an authorization grant. Its propagation warning proves the write
crosses the external-action boundary.

### 4. Project memory

The only matching gascity-packs project memory is
`/home/ds/.claude/projects/-home-ds-gascity-packs/memory/slack-status-is-not-a-posting-record.md:23-31`.
It tells the role to **read** the vault daily log as a Slack dedup signal and
describes the `VAULT_NOTES` morning check. It does not independently authorize
writes.

The project brief contains no vault-write instruction. The active durable bead
memory pointed the other way:

```text
external_action_authorized=false
publication_execution_authorized=false
no external action
```

Mayor mail `gc-525222` and the user's pre-write task message also said `no
external action`. A later user message naming `no personal brain/vault edit`
arrived after the write and therefore tightened future handling but was not the
pre-existing prohibition breached here.

## Inherited roles

Eight rig project-lead templates directly render the shared fragment:

| Role | Include site |
|---|---|
| `codeprobe-pl` | `agents/codeprobe-pl/prompt.template.md:510` |
| `enterprisebench-pl` | `agents/enterprisebench-pl/prompt.template.md:442` |
| `gascity-dashboard-pl` | `agents/gascity-dashboard-pl/prompt.template.md:496` |
| `gascity-maintenance-pl` | `agents/gascity-maintenance-pl/prompt.template.md:615` |
| `gascity-packs-pl` | `agents/gascity-packs-pl/prompt.template.md:334` |
| `mem-pl` | `agents/mem-pl/prompt.template.md:571` |
| `migration-evals-pl` | `agents/migration-evals-pl/prompt.template.md:526` |
| `scix-experiments-pl` | `agents/scix-experiments-pl/prompt.template.md:495` |

`city-infra-pl` is a ninth indirect consumer: its prompt at
`agents/city-infra-pl/prompt.md:77-86` instructs it to read the canonical
`prompts/pl-periodic-directives.md` contract. Unlike the eight templates, its
prompt does not render the fragment directly. This role is still exposed to the
ambiguous standing rule by reference.

The mayor does not inherit this project-note directive. Mayor has separate
working-memory and executive-status vault instructions; those need the same
authorization classification but are not an inheritance path for the
`VAULT_NOTES` text. Orders `vault-status-mirror` and `vault-issues-log-sync`
refer to PL `VAULT_NOTES` output but do not inject the directive into additional
agent roles.

## Exact mutation and preserved evidence

The authoritative Amp thread is
`T-019f7ffb-ddad-7640-8a4b-ea5b5d8fe858`.

At 2026-07-20T19:25:37.898Z, one `shell_command` ran a Python heredoc from
`/home/ds/gascity-packs-main`. It used `Path.write_text` after anchored
`str.replace(..., 1)` operations and wrote both:

1. `/home/ds/brain/Projects/Gas City Packs.md`
2. `/home/ds/brain/Projects/Gas City Packs Issues Log.md`

No other tool call mutated those files. No revert or normalization followed.

### The two project-note bullets that must remain preserved

Current file metadata:

```text
path: /home/ds/brain/Projects/Gas City Packs.md
size: 2786 bytes
mtime/ctime: 2026-07-20 15:25:49.366757951 -0400
sha256: 55d3a005ae1be5bd083911c070a38c65d85ba8af6f10ac892f62850d07531c8e
```

`Gas City Packs.md:10`, under `## Open work`:

```text
- Finish the human-gated installation choice for the new inert external-action verifier; its exact-repository/action/subject/head, replay, expiry, and bypass fixtures now pass locally.
```

`Gas City Packs.md:22`, under `## Daily log` → `### 2026-07-20`:

```text
- The account-independent authorization verifier now exists as an inert, unintegrated local commit with 31 targeted checks passing and two independent review findings fixed. Settings, hooks, formula wiring, and publication remain untouched pending the separate installation decision.
```

Both bullets remain byte-for-byte present at those locations. This
investigation did not edit, revert, normalize, rename, or move the note.

### Contradiction: one other vault file was changed

The incident ledger's statement that no other vault file changed is false. The
same Python tool call inserted this bullet at
`Gas City Packs Issues Log.md:5`, under `### 2026-07-20`:

```text
- The first inert authorization-verifier draft trusted omitted or truthy-string verifier and record attestations, and malformed top-level records could raise instead of denying. Learning: security-boundary defaults and runtime shapes must fail closed even when type hints claim valid inputs. Fix: changed both attestations to false-by-default exact-`True` checks, reject non-mapping records, added regression fixtures, and reran 31 targeted tests plus Ruff and format checks.
```

Current file metadata:

```text
path: /home/ds/brain/Projects/Gas City Packs Issues Log.md
size: 2973 bytes
mtime/ctime: 2026-07-20 15:25:49.367150303 -0400
sha256: 8630dcb50aa1ca57cd4ab0c4466dd1cac5c2fc3210751983fba4d97b1ac63d59
```

The two files differ in mtime by approximately 0.392 milliseconds, consistent
with the one sequential Python write call. A read-only vault scan for
2026-07-20 15:25:40 through 15:26:00 found exactly these two files and no
others. A read-only filename scan found no `*sync-conflict*`, `*conflict*`, or
`*conflicted copy*` artifact anywhere under `/home/ds/brain`.

The Issues Log bullet is also preserved unchanged because no vault mutation is
authorized. Human disposition is required for all three inserted bullets.

## Exact authorization boundary

`/home/ds/brain/**` is live-synced personal/external state.

- Reading vault files is a local read-only action.
- Creating, writing, appending, replacing, renaming, moving, or deleting any
  vault path is an external mutation, even when performed through a local file
  tool or shell command.
- A standing prompt, order, memory, skill, prior permission, routine status
  cadence, or instruction to keep durable notes schedules work; it does not
  authorize a current vault mutation.
- Current explicit user authorization must identify the vault target and the
  permitted mutation. Authorization for one path/action does not cover another,
  and authorization for Slack, GitHub, mail, or a different external surface
  does not transfer.
- `external_action_authorized=false`, `vault_mutation_authorized=false`, or any
  current `no external action` instruction is an explicit deny. Absence of an
  action-specific user authorization also denies the mutation.
- Bead metadata may durably record a user authorization and its source, but it
  cannot originate or broaden authorization. Mayor/agent instructions cannot
  override a user's deny or grant human-only permission.
- The most specific current-task constraint overrides older standing
  directives. On conflict or uncertainty, do not touch the vault; write the
  proposed text to the bead or a local report and ask for a decision.

Durable local bead/report writes remain autonomous when they do not themselves
sync or publish externally. The boundary is destination and effect, not the
API used: local `Path.write_text` is external when its destination is the
Syncthing-backed vault.

## Smallest fail-closed wording

Prepend this paragraph to the `VAULT_NOTES` directive before any mandatory
write verb:

```text
AUTHORIZATION GATE: `/home/ds/brain/**` is live-synced external state. Reading
is local/read-only; every create, write, append, replace, rename, move, or
delete is an external action. This standing directive, any memory or order, and
the Obsidian skill schedule work but DO NOT authorize mutation. Before each
vault mutation, require current explicit user authorization naming the target
path and permitted action. `external_action_authorized=false`,
`vault_mutation_authorized=false`, any current “no external action” instruction,
or missing action-specific authorization means STOP: preserve the proposed text
in the bead or a local report and surface the decision. The most specific
current-task deny overrides this standing directive.
```

The same classification sentence should appear in the Obsidian skill, but the
prompt source of truth is the smallest first fix because all eight direct PL
roles inherit it in one edit. No prompt or skill was changed during this
investigation.

## Smallest regression test

Add a hermetic prompt-policy test; never point it at the real vault.

1. Render every direct PL template and assert the authorization gate occurs
   before `Write on every status change`, `Log every issue`, and `Fix any drift
   found`.
2. Run a fake-filesystem decision matrix whose sink records attempted paths:

| Inputs | Expected |
|---|---|
| standing `VAULT_NOTES` only | deny write; emit local proposed-text artifact |
| standing directive + loaded Obsidian skill | deny write |
| memory says to log + `external_action_authorized=false` | deny write |
| mayor/order requests status update, no user authorization | deny write |
| bead says `external_action_authorized=true`, no cited user grant | deny write |
| stale/prior user authorization | deny write |
| user authorizes only `<Project>.md` append | allow only that append; deny Issues Log |
| user authorizes both named paths and actions | allow only those named mutations |
| user authorizes Slack/GitHub publication | deny vault mutation |
| current user says `no external action` after an older grant | deny write |

3. Assert no test can resolve `/home/ds/brain`; use an in-memory or temporary
   fake root and fail the test if a real-vault prefix appears.
4. Assert the deny path writes the exact proposed bullet to a local bead/report
   fixture and emits one authorization decision, without silently dropping the
   requested status update.
5. Include the `city-infra-pl` pointer path in a static test so indirect
   consumers cannot bypass the gate by reading the canonical contract.

The falsifiable property is: no standing instruction or metadata boolean can
produce a vault write without a current, action-specific user grant, and a
grant for one target cannot mutate another.

## Verification performed

Read-only checks established:

- the exact source fragment and all direct include sites;
- the city named-session binding and indirect city-infra pointer;
- the skill's live-sync warning;
- the only matching project-memory rule;
- the authoritative Amp thread's write call, timestamps, inserted content, and
  later acknowledgement;
- exact current bullet locations and metadata for both modified files;
- exactly two vault files in the write-time window; and
- no sync-conflict-named artifact under the vault.

No vault file, global prompt/skill/config, Syncthing state, runtime, or rig
source was mutated. No external communication was sent.
