# dr-dwrn independent review: `mol-focus-review` sentinel

Date: 2026-07-21

Verdict: **PASS**

## Independence and scope

City Infra routed the current formula and its named pre-change backup to an
independent `code-reviewer` lane. The reviewer was instructed not to invoke
`mol-focus-review` or trust its own pass result, not to modify files, and not to
review the separate worktree-corruption or finalize-gap defects tracked by
`dr-6dc` and `dr-t1m`.

## Artifact integrity

At review start, both artifacts existed and matched the hashes recorded on the
bead:

| Artifact | SHA-256 | Result |
| --- | --- | --- |
| `formulas/mol-focus-review.formula.toml` | `fe47e38cfee570ad22b5a0a2cc5f715fa7de5547ac7710583ea5533664f73487` | Match |
| `formulas/mol-focus-review.formula.toml.bak-aoa-k6nm-2026-07-21` | `e0ecaa5eae3877988dc308615d6c0335db8fb1ca15228b88c26bae073eb29d01` | Match |

The reviewer captured a complete `diff -u` before continuing. The backup was
removed concurrently after that initial hash and diff. City Infra did not
restore or modify it. A post-review check found the current formula still at
`fe47e38cfee570ad22b5a0a2cc5f715fa7de5547ac7710583ea5533664f73487`
and the backup absent.

## Behavior reviewed

The pre-change formula defaulted `test_command` to an empty string, collapsing
an absent value and an explicit empty override into one state. The current
formula defaults it to `<unset>` and handles `SKIP:*`, `<unset>`, explicit
empty, and a resolved command separately. The reviewer traced the real
defaults-then-overrides path through sling, graph-v2 invocation, molecule
instantiation, and formula substitution. An explicit empty value is preserved
as present and overrides `<unset>`.

## Eight-case matrix

The reviewer parsed the live TOML, extracted the exact command-dispatch shell
body, substituted only the rendered command value, and executed that body
in-memory for both values of `CODE_BEARING`. It did not invoke the formula as a
verification gate.

| Command state | Non-code work | Code-bearing work | Result |
| --- | --- | --- | --- |
| Unset (`<unset>`) | Exit 0; reports unresolved but no code detected | Exit 1; fatal unresolved command | Pass: code fails closed |
| Explicit empty (`""`) | Exit 0; reports no command and no code detected | Exit 1; fatal explicitly empty command | Pass: code fails closed |
| `SKIP:` | Exit 0; explicit skip | Exit 0; explicit skip | Pass: deliberate escape remains available |
| Resolved successful command | Command runs and exits 0 | Command runs and exits 0 | Pass |

The resolved-command path continued to reject a non-zero exit and output
indicating no packages, no test files, or no tests to run.

## Independent verification

Commands reported by the independent lane:

```text
sha256sum formulas/mol-focus-review.formula.toml \
  formulas/mol-focus-review.formula.toml.bak-aoa-k6nm-2026-07-21
diff -u formulas/mol-focus-review.formula.toml.bak-aoa-k6nm-2026-07-21 \
  formulas/mol-focus-review.formula.toml

PYTHONDONTWRITEBYTECODE=1 python3 <bounded in-memory TOML/shell matrix harness>

go test ./internal/formula ./internal/molecule ./internal/graphv2 \
  -run '^(TestSubstitute|TestInstantiateVarDefaults|TestPrepareInvocationPassesParentDefaultVarsToDrainItemValidation)$' \
  -count=1

go test ./cmd/gc -run '^TestBuildSlingFormulaVarsRigDefaults$' -count=1
```

All targeted Go tests passed. The reviewer made no file, runtime, config,
process, or external changes.

## Findings

No critical, high, or medium findings were identified. One pre-existing low
severity observation remains: bare `SKIP:` is accepted without a reason because
the branch matches `SKIP:*`. Requiring `SKIP:<reason>` would be optional future
hardening, not a defect in the absent-versus-empty change reviewed here.

## Decision

Retain the current formula unchanged at hash
`fe47e38cfee570ad22b5a0a2cc5f715fa7de5547ac7710583ea5533664f73487`.
The `<unset>` sentinel correctly distinguishes unresolved input from a
deliberate explicit-empty override, and both states fail closed for
code-bearing work.
