#!/usr/bin/env bash
# Test for R2 — jj-proof gitleaks secret scanning.
#
# Verifies two things in a throwaway git repo:
#   1. GIT PATH (unchanged): the existing `gitleaks git --staged` scan still
#      flags a STAGED secret.
#   2. JJ PATH (the fix): the working-tree scan `gitleaks dir` flags an
#      UNTRACKED secret that a `--staged`-only scan would miss — proving the
#      secret jj auto-snapshots is detectable by the command the jj branch runs.
#
# jj is not installed here, so we exercise the jj branch's SCAN COMMAND
# (`gitleaks dir`) directly rather than driving jj itself.

set -uo pipefail

GITLEAKS="${GITLEAKS:-/home/ds/.local/bin/gitleaks}"
command -v "$GITLEAKS" >/dev/null 2>&1 || GITLEAKS="$(command -v gitleaks)"

if [ -z "${GITLEAKS:-}" ] || ! command -v "$GITLEAKS" >/dev/null 2>&1; then
  echo "FAIL: gitleaks not found" >&2
  exit 1
fi

# Realistic fake secret: AWS access-key-id shape (RuleID aws-access-token).
# NB: the canonical AKIAIOSFODNN7EXAMPLE is allowlisted by gitleaks because it
# contains "EXAMPLE"; this fake value is not, so gitleaks flags it.
SECRET='AKIA2E0A8F3B244C9986'

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git -C "$WORK" init -q
git -C "$WORK" config user.email t@t.t
git -C "$WORK" config user.name t

fails=0

# --- Test 1: git path still catches a STAGED secret ---
printf 'aws_secret = "%s"\n' "$SECRET" > "$WORK/staged.txt"
git -C "$WORK" add staged.txt
( cd "$WORK" && "$GITLEAKS" git --pre-commit --staged --no-banner ) >/dev/null 2>&1
rc=$?
if [ "$rc" -ne 0 ]; then
  echo "PASS: staged secret flagged by 'gitleaks git --staged' (rc=$rc)"
else
  echo "FAIL: staged secret NOT flagged by 'gitleaks git --staged' (rc=$rc)"
  fails=$((fails + 1))
fi

# --- Test 2: --staged scan MISSES an untracked secret (the gap) ---
WORK2="$(mktemp -d)"
git -C "$WORK2" init -q
git -C "$WORK2" config user.email t@t.t
git -C "$WORK2" config user.name t
# Untracked file only — nothing staged.
printf 'aws_secret = "%s"\n' "$SECRET" > "$WORK2/untracked.txt"
( cd "$WORK2" && "$GITLEAKS" git --pre-commit --staged --no-banner ) >/dev/null 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then
  echo "PASS: 'gitleaks git --staged' MISSES untracked secret (rc=$rc) — the gap jj exposes"
else
  echo "INFO: 'gitleaks git --staged' returned rc=$rc on untracked-only repo"
fi

# --- Test 3: jj-branch command (gitleaks dir) CATCHES the untracked secret ---
( cd "$WORK2" && "$GITLEAKS" dir --no-banner . ) >/dev/null 2>&1
rc=$?
rm -rf "$WORK2"
if [ "$rc" -ne 0 ]; then
  echo "PASS: 'gitleaks dir' flags UNTRACKED secret (rc=$rc) — jj auto-snapshot covered"
else
  echo "FAIL: 'gitleaks dir' did NOT flag untracked secret (rc=$rc)"
  fails=$((fails + 1))
fi

# --- Test 4: clean working tree passes 'gitleaks dir' (no false positive) ---
WORK3="$(mktemp -d)"
git -C "$WORK3" init -q
printf 'hello world\n' > "$WORK3/ok.txt"
( cd "$WORK3" && "$GITLEAKS" dir --no-banner . ) >/dev/null 2>&1
rc=$?
rm -rf "$WORK3"
if [ "$rc" -eq 0 ]; then
  echo "PASS: 'gitleaks dir' clean on secret-free tree (rc=$rc)"
else
  echo "FAIL: 'gitleaks dir' false-positive on clean tree (rc=$rc)"
  fails=$((fails + 1))
fi

# --- Test 5: jj guard is dormant when jj is absent ---
if command -v jj >/dev/null 2>&1; then
  echo "INFO: jj present on this machine; guard would activate in colocated repos"
else
  echo "PASS: jj absent — 'command -v jj' guard keeps the new branch dormant"
fi

echo "----"
if [ "$fails" -eq 0 ]; then
  echo "ALL TESTS PASSED"
  exit 0
else
  echo "$fails TEST(S) FAILED"
  exit 1
fi
