# Contributing to slack-pack

slack-pack is a Slack provider extension for Gas City. When this directory is
mirrored to `gastownhall/gascity-packs/slack/`, the root repo's contributing
guide is the source of truth — start there, then read the slack-pack-specific
notes below.

For Gas City's own contributor workflow (build, hooks, docs), see the in-tree
guide at [../../CONTRIBUTING.md](../../CONTRIBUTING.md).

## Build flow

Two pieces ship with this pack:

- **Pack scripts** live in `scripts/` and are pure Python. They run via the
  `gc slack <command>` shims under `commands/` and have no compile step.
- **Adapter** (the Slack-side HTTP/UDS bridge) currently lives at
  `examples/oversight-rig/adapter/` while it is in the gascity tree. Bead
  `gc-28a` tracks relocating it alongside the pack — once that lands the
  adapter source will move under `slack-pack/` (or a sibling). Treat the
  current path as load-bearing for this pack only; do not deeply hard-code
  it in scripts.

Build the adapter with:

```bash
cd examples/oversight-rig/adapter
go build ./...
```

## Test flow

Run pack tests (pytest, no external deps beyond `pytest` itself):

```bash
pytest examples/slack-pack/tests/
```

Run adapter tests:

```bash
cd examples/oversight-rig/adapter
go test -race ./...
```

CI runs both on every PR that touches `examples/slack-pack/**` or
`examples/oversight-rig/adapter/**` (see
`.github/workflows/slack-pack.yml`).

## Secret handling

slack-pack reads Slack credentials from environment variables only. Never
commit `.env` files or tokens. The README's "Adapter env contract" section
documents the full env-var contract; use a `.env` file outside the repo or a
secret manager and source it before running adapter / scripts.

## Pull requests

- Keep PRs scoped to slack-pack (or paired adapter changes when needed).
- Update `CHANGELOG.md` for any user-visible change — add bullets under a
  new `[Unreleased]` section, and the next release tag promotes them.
- Run `pytest` and `go test -race` locally before opening the PR.
