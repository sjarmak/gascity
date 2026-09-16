#!/usr/bin/env bash
# lint-mem-cap.sh — hard memory ceiling for every golangci-lint invocation.
#
# gc-vrva1q: golangci-lint over ./cmd/gc peaks around 6.35 GiB RSS even at
# concurrency=1 with GOGC halved, and the whole-repo run has been measured at
# 8.08 GiB and, separately, 9.85 GiB. On a host with exhausted swap that
# overshoot has nowhere to spill and reaches the OOM killer directly,
# killing unrelated processes host-wide (dr-krxbg). Soft levers (GOMEMLIMIT,
# --concurrency, see lint-run.sh) cut the peak when GOMEMLIMIT is actually
# set below the natural peak (measured: ./cmd/gc/... unfixed 8.18-8.61 GiB
# over 3 contained samples; GOMEMLIMIT=3GiB + --concurrency=1 brings that
# down to 5.40-5.47 GiB over 3 contained samples). Soft levers are not a
# safety guarantee by themselves — a caller can override or drop them — so
# every invocation also runs inside a systemd-run --user --scope cgroup with
# a hard MemoryMax: an overshoot becomes a contained cgroup kill of the lint
# process instead of a global OOM. THE CEILING ONLY PROTECTS THE HOST IF IT
# SITS BELOW THE NATURAL (UNCAPPED) PEAK — a ceiling above every measured
# peak never engages and is decoration (round-1 mistake: GC_LINT_MEMORY_MAX
# defaulted to 12G, GC_LINT_GOMEMLIMIT to 10GiB, both above the 8.08-9.85 GiB
# range they existed to bound).
#
# UNIT GRAMMARS DIFFER — do not copy one override's spelling to the other:
#   GC_LINT_GOMEMLIMIT (lint-run.sh) is Go's GOMEMLIMIT: takes a binary
#     suffix WITH the "i" (B, KiB, MiB, GiB, TiB), e.g. "3GiB".
#   GC_LINT_MEMORY_MAX (here) is systemd's MemoryMax=: takes a binary
#     suffix WITHOUT the "i" and without a trailing "B" (K, M, G, T), e.g.
#     "7G". "7GiB" or "7GB" are REJECTED by systemd, and a rejected value is
#     now a hard failure (see below), not a silent uncapped run.
#
# Usage: gc_lint_mem_cap_exec <golangci-lint-binary> <args...>
#
# Three distinct outcomes, on purpose:
#   1. Cap applies                                -> run capped, no message.
#   2. No responsive user systemd manager          -> WARN on stderr, run
#      (systemd-run missing, or present but the      uncapped. Documented,
#      user manager is unreachable: macOS,           deliberate fallback so
#      containers, minimal CI, GC_LINT_NO_MEM_CAP=1)  the gate still runs.
#   3. systemd-run reachable but rejects the value  -> FAIL, do not run the
#      in GC_LINT_MEMORY_MAX (operator typo/typo-     lint at all. A bad
#      class error, e.g. "12GiB", "12GB", "bogus")    value must never look
#                                                      like "no cap needed".
# (1) and (2) were the only two outcomes before this fix, and systemd
# rejecting a malformed value was indistinguishable from systemd being
# absent, so a typo silently ran the lint fully uncapped at exit 0.

: "${GC_LINT_MEMORY_MAX:=7G}"
: "${GC_LINT_MEMORY_SWAP_MAX:=2G}"

# gc_lint_mem_cap_available echoes one of: ok | no-manager | bad-value
gc_lint_mem_cap_available() {
  if [[ "${GC_LINT_NO_MEM_CAP:-0}" == "1" ]]; then
    echo no-manager
    return 1
  fi
  if ! command -v systemd-run >/dev/null 2>&1; then
    echo no-manager
    return 1
  fi
  # Probe the manager itself with no cap, so an unreachable user manager
  # (the documented fallback case) can never be confused with a value
  # systemd understood and rejected (an operator error).
  if ! systemd-run --user --scope --collect --quiet -- true >/dev/null 2>&1; then
    echo no-manager
    return 1
  fi
  if ! systemd-run --user --scope --collect --quiet \
    -p MemoryMax="$GC_LINT_MEMORY_MAX" -- true >/dev/null 2>&1; then
    echo bad-value
    return 1
  fi
  echo ok
  return 0
}

gc_lint_mem_cap_exec() {
  local status
  status="$(gc_lint_mem_cap_available)"
  case "$status" in
    ok)
      exec systemd-run --user --scope --collect --quiet \
        -p MemoryMax="$GC_LINT_MEMORY_MAX" \
        -p MemorySwapMax="$GC_LINT_MEMORY_SWAP_MAX" -- "$@"
      ;;
    bad-value)
      echo "gc_lint_mem_cap: GC_LINT_MEMORY_MAX=$GC_LINT_MEMORY_MAX was rejected by systemd; refusing to run golangci-lint without a working memory cap. systemd's MemoryMax= wants a byte count or a K/M/G/T suffix with no 'i' and no trailing 'B' (e.g. 7G, not 7GiB or 7GB)." >&2
      return 1
      ;;
    no-manager)
      if [[ "${GC_LINT_NO_MEM_CAP:-0}" != "1" ]]; then
        echo "gc_lint_mem_cap: WARNING - no responsive user systemd manager; running '$*' WITHOUT a memory cap. An overshoot here can OOM the whole host, not just this process." >&2
      fi
      exec "$@"
      ;;
  esac
}
