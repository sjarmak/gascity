# `gc-5cgt` is branch-ready locally; the original held-set acceptance drifted

_2026-07-20, local implementation and read-only live census. No push, merge,
PR, review submission, Slack post, service restart, dispatcher action, worker
action, or external publication occurred._

## Local implementation

The Gas City worktree is clean and pinned to:

```text
worktree: /home/ds/gascity-worktrees/polecat-1/worktrees/gc-5cgt
branch: work/gc-5cgt
base: 92119a561f34f5a60d5cea71c6d9997cd33415ea
commit: e192a2465769bc0e103773abcadc3e9d5cee2efd
subject: fix(dispatch): honor dispatch-blocked at claim time
```

The commit changes 11 repository files (551 insertions, 36 deletions). Exact
`dispatch-blocked` labels are excluded from ready advertisement and guarded at
the atomic claim mutation used by hook claims, agent-script `bd_claim`, and
continuation preassignment. Ordinary work, near-match labels, and same-owner
idempotency remain supported; same-owner work carrying the exact hold label is
still refused. The mutation re-read handles zero or ambiguous affected-row
results, and ordinary issues versus no-history wisps use their correct label
tables.

The separate city-local workflow precheck remains in:

```text
/home/ds/gas-city/formulas/mol-focus-review.formula.toml
sha256 e0ecaa5eae3877988dc308615d6c0335db8fb1ca15228b88c26bae073eb29d01

/home/ds/gas-city/bin/mol-focus-review-precheck.test
sha256 27a8ec5241cd422405e06f7db43d6f62bdef082d53826ad235b0aaa9275cf438
```

It rejects exact hold labels, blocked/closed status, failed reads, malformed
JSON, non-singleton results, malformed status, explicit-null or non-string
labels, and non-string label elements before `/focus`. It accepts an omitted
`labels` field because real unlabeled `bd show` output omits that field.

## Verification and review

Focused repository tests passed:

```bash
go test ./cmd/gc -run 'Hook.*Claim|AgentScript|DispatchBlocked' -count=1
go test ./internal/beads -run 'Ready|Dispatchable|DispatchBlocked' -count=1
```

The following also passed:

```bash
go vet ./internal/beads ./cmd/gc
git diff --check
bash -n /home/ds/gas-city/bin/mol-focus-review-precheck.test
shellcheck /home/ds/gas-city/bin/mol-focus-review-precheck.test
bash /home/ds/gas-city/bin/mol-focus-review-precheck.test
# Python tomllib parse of mol-focus-review.formula.toml
```

The formula harness reported:

```text
PASS: mol-focus-review rejects dispatch-blocked and unknown state without changing ordinary open behavior
PASS: formula TOML parses
```

The commit hook ran changed-package lint, generated-reference checks, and
`go vet ./...` successfully. The final independent Oracle review returned
`PASS` after two earlier findings were corrected: scripted same-owner
idempotency was restored, and legitimately omitted labels were accepted while
malformed label state remains fail-closed.

The broad suite is not represented as green. Existing failures were reproduced
unchanged at the same base in `/home/ds/gascity-main`:

```text
TestProductMetricsCommandCensusMatchesProductionBuiltins
TestClassifyProductMetricsCommandRejectsAnnotationDrift
TestWriteRunMapMatchesProxyReaderContract
TestRequiresDedicatedTestenvImportFile
TestRepositoryLedgerMatchesCensusAndDocumentation
```

The earlier full log is `/var/tmp/gascity-test.jsonl.PkfLD0`.

## Current held-set census is safe, but the literal 14-bead history is not clean

A read-only AOA census found 11 nonterminal beads carrying exact
`dispatch-blocked`. Every one is unassigned and has no `gc.routed_to`. Ten also
have no session metadata. `aoa-w0o` has stale historical
`gc.session_name=claude-2-gc-518004`, but is unassigned/unrouted and that session
is not the current AOA pool worker. Incident bead `aoa-wbn7` remains open,
unassigned, unrouted, and labeled `dispatch-blocked`.

Three label-bearing beads closed after `gc-5cgt` was filed, so the acceptance
statement that all original 14 remained unclaimed cannot be asserted:

| Bead | Closed (UTC) | Durable attribution |
|---|---|---|
| `aoa-d6t.41` | 2026-07-20 15:05:47 | no assignee/session metadata |
| `aoa-ctyo` | 2026-07-20 14:57:25 | no assignee/session metadata |
| `aoa-g2g5` | 2026-07-20 15:23:55 | started 14:52:47 as `claude-5-gc-524077`; landed `fcf2c68` |

These transitions predate the local branch-ready commit and are preserved as
incident evidence. No AOA state was reverted or rewritten. Maintenance
dispositioned them as a historical exception that cannot be retroactively made
true, rather than erasing or waiving the evidence. The current safe census,
atomic claim/readiness/load-context fixtures, and final independent PASS satisfy
the branch-ready/no-land milestone. `gc-5cgt` closed on that basis at 15:03 EDT.
The local branch is not published or active in the installed `gc` binary.

## Required next gate

After the commit is adopted, a live runtime census is required before
`gc-xgo4`, `gc-3cp6`, F01/F02/F14, or any held publication path may advance.
Those items remain blocked and `dispatch-blocked`; publication and landing are
separately authorization-gated. Durable closure mail: `gc-525242`.
