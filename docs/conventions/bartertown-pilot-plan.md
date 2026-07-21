# Bartertown scoped-pilot plan (ds-research)

Decision: Stephanie approved a scoped pilot (option b) 2026-07-05, after a
sandboxed source review found the pack's code clean (no shell/eval/exec,
confined git, symlink defenses, real tests) but the DEFAULT wiring unsafe: it
routes the 15-minute sweep digest of unvetted third-party text straight into
`mayor`'s context, and its secret-lint only catches key-shaped strings.

Deploy key: `~/.ssh/bartertown-deploy-ds-research` (ed25519, generated
2026-07-05; private key local 0600, never leaves the box). City name on the
forum: `ds-research`.

## Mitigations to apply AT INSTALL (do not skip any)

1. **Dedicated low-privilege reader agent `bartertown-reader`.** New agent whose
   toolset is read/search + the `barter_*` tools ONLY — no `gc sling`, no git
   push, no shell/Bash, no dolt. It is the ONLY participant. Its output is
   treated as untrusted by mayor; mayor never auto-acts on it.

2. **`participants` = `["bartertown-reader"]`** in
   `.gc/services/bartertown/config.json` — NOT the default `"all"`, NOT
   `["mayor"]`. mayor carries zero Bartertown tool footprint.

3. **Skill NOT installed city-wide.** Copy `optional-skill/bartertown` only to
   `agents/bartertown-reader/skills/` if the reader needs it. Do NOT add the
   `bartertown-v0` fragment to mayor or `[agent_defaults]`.

4. **Sweep order left UNARMED.** Do NOT create `.gc/bartertown-sweep.enabled`.
   The shipped `orders/bartertown-sweep.toml` targets `--agent mayor`; if the
   sweep is ever wanted, re-point it to `bartertown-reader` first.

5. **`lint.banned_strings` populated** with our leak surface (exact-substring
   matches on outbound posts; the built-in secret-lint only catches key SHAPES):
   `/home/ds`, `account3`, `account4`, `account5`, `sjarmak`, `Stephanie`,
   `stephanie`, `29620` (dolt port), and the internal Slack channel IDs
   (`C0B25SS12CD`, `C0B0TQMQF2B`, `C0B1NSHTSKT`, plus any others from
   `.gc/services/slack/data/config.json` at apply time). NOTE: variable bead IDs
   (`gc-XXXXX`, `EnterpriseBench-XXXX`) can't be caught by exact-match; the
   reader must summarize and a human reviews before any post.

6. **No automation trusting post authorship.** `city_name` is self-asserted and
   any hub member with push can post as any city; never auto-accept playbooks by
   author.

## Enable command (scoped, NOT mayor)

    gc bartertown enable --reviewed-by-mayor bartertown-reader

## Sequence

1. Stephanie relays city name `ds-research` + the `.pub` to Wldc4rd (external).
2. Hub owner drops the key + provides the pack + hub URL.
3. Mayor: install pack, create `bartertown-reader`, write config.json with the 6
   mitigations above, `gc bartertown enable` scoped to the reader.
4. Verify: mayor's tool list carries NO `barter_*`; sweep disabled; a test
   `barter_search` works only from the reader; a dummy `barter_post` containing
   `/home/ds` is rejected by the lint.
