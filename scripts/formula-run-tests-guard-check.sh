#!/usr/bin/env bash
# Behavioural check for the mol-focus-review run-tests guard (aoa-k6nm).
#
# The guard has to keep three states apart, because each has a different remedy:
#   <unset>  the rig never resolved test_command -> re-sling / add rig config
#   ""       an operator deliberately configured no command -> fix config or SKIP:
#   SKIP:..  work that genuinely has no tests -> validated skip
# Collapsing the first into the second is what produced the aoa-k6nm report: the
# worker was sent to edit rig config that was already correct, and offered SKIP:
# as the way out — one rationalisation from a false green (dr-5lub).
#
# The case block is extracted from the shipped formula rather than duplicated, so
# this check goes stale loudly instead of silently.
#
# Usage: formula-run-tests-guard-check.sh [path/to/mol-focus-review.formula.toml]

set -u

FORMULA="${1:-/home/ds/gas-city/formulas/mol-focus-review.formula.toml}"
[ -f "$FORMULA" ] || { echo "FATAL: no formula at $FORMULA" >&2; exit 2; }

CASE=$(mktemp) || exit 2
trap 'rm -f "$CASE"' EXIT
awk '/^case "\$TC" in$/,/^esac$/' "$FORMULA" > "$CASE"
grep -q '^esac$' "$CASE" || {
  echo "FATAL: could not extract the 'case \$TC' guard from $FORMULA — the run-tests step was restructured; update this check." >&2
  exit 2
}

fail=0

# check <label> <want-rc> <must-match> <must-not-match|""> <TC> <CODE_BEARING>
check() {
  local label="$1" want_rc="$2" want="$3" reject="$4" tc="$5" cb="$6"
  local out rc ok=1
  out=$( TC="$tc" CODE_BEARING="$cb"; . "$CASE" 2>&1 ); rc=$?
  [ "$rc" = "$want_rc" ] || { ok=0; echo "  ! exit $rc, want $want_rc"; }
  printf '%s' "$out" | grep -q "$want" || { ok=0; echo "  ! missing: $want"; }
  if [ -n "$reject" ] && printf '%s' "$out" | grep -q "$reject"; then
    ok=0; echo "  ! must not say: $reject"
  fi
  if [ "$ok" = 1 ]; then
    echo "PASS $label"
  else
    echo "FAIL $label"; printf '%s\n' "$out" | sed 's/^/      /'; fail=1
  fi
}

# Unresolved var on code-bearing work: hard fail that names the real cause and
# refuses to dangle SKIP: as the escape.
check "unset + code -> names non-resolution" 1 \
  "did not resolve for this rig" "" "<unset>" 1
check "unset + code -> not reported as an EMPTY value" 1 \
  "rendered the '<unset>' default" "test_command is EMPTY but" "<unset>" 1
check "unset + code -> SKIP: not offered as the remedy" 1 \
  "Do NOT declare SKIP:" "declare test_command='SKIP:<reason>' if there genuinely" "<unset>" 1
check "unset + no code -> informational, no failure" 0 \
  "test_command unresolved and no code detected" "" "<unset>" 0

# Deliberate empty keeps its own wording: there, config-or-SKIP really is right.
check "empty + code -> distinct wording" 1 \
  "explicitly EMPTY" "did not resolve for this rig" "" 1
check "empty + code -> config/SKIP remedy retained" 1 \
  "declare test_command='SKIP:<reason>'" "" "" 1
check "empty + no code -> informational, no failure" 0 \
  "no test_command and no code detected" "" "" 0

# Explicit skip still honoured.
check "SKIP: declaration honoured" 0 \
  "explicit validated skip — no rust tests" "" "SKIP:no rust tests" 1

[ "$fail" = 0 ] && echo "guard check: all scenarios passed ($FORMULA)"
exit "$fail"
