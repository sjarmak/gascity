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
  # "infinity" is a value systemd genuinely accepts for MemoryMax=/
  # MemorySwapMax= (it means unlimited), so the probe below would report
  # "ok" -- but an unlimited ceiling never binds, which defeats the whole
  # point of this fence. Reject it explicitly rather than letting a
  # syntactically-valid-but-useless value pass as a working cap.
  if [[ "${GC_LINT_MEMORY_MAX,,}" == "infinity" || "${GC_LINT_MEMORY_SWAP_MAX,,}" == "infinity" ]]; then
    echo bad-value
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
  # Probe both ceilings so a bad GC_LINT_MEMORY_SWAP_MAX fails through this
  # same diagnostic instead of surfacing later as a raw systemd error from
  # the real (capped) invocation.
  if ! systemd-run --user --scope --collect --quiet \
    -p MemoryMax="$GC_LINT_MEMORY_MAX" \
    -p MemorySwapMax="$GC_LINT_MEMORY_SWAP_MAX" -- true >/dev/null 2>&1; then
    echo bad-value
    return 1
  fi
  echo ok
  return 0
}

# gc_lint_mem_cap_report_kill prints a legible diagnostic to stderr when the
# capped run died from something that looks like a cgroup memory kill. It is
# only called for a non-zero exit that looks like a kill signal (128+N); a
# normal golangci-lint failure (e.g. exit 1, lint findings) never reaches it.
#
# Confidence is worded to match what was actually checked: `Result=oom-kill`
# on the transient scope unit is systemd's own record that THIS cgroup's
# memory.events oom_kill counter fired (authoritative). Exit 137 (SIGKILL)
# without that confirmation is consistent with a cap hit but not proof --
# anything can send SIGKILL -- and the message says so rather than asserting
# it. A death from any other signal is not the cap's signature at all and is
# reported as unrelated.
gc_lint_mem_cap_report_kill() {
  local status="$1" unit="$2"
  local sig=$((status - 128))
  local result=""
  if command -v systemctl >/dev/null 2>&1; then
    result="$(systemctl --user show "${unit}.scope" -p Result --value 2>/dev/null || true)"
  fi

  local confidence
  if [[ "$result" == "oom-kill" ]]; then
    confidence="CONFIRMED: systemd recorded this run's cgroup as Result=oom-kill -- the kernel OOM-killed a process inside the memory ceiling below."
  elif [[ "$sig" -eq 9 ]]; then
    confidence="Exit $status is SIGKILL, which is consistent with hitting the memory ceiling below, but that is not proof by itself -- SIGKILL can come from other sources too, and systemd did not confirm Result=oom-kill for this run (${result:-no confirmation available})."
  else
    # Not the cap's signature (SIGKILL, or a confirmed oom-kill Result) --
    # a different signal killed this, unrelated to the memory fence. Say
    # nothing further; the caller's own exit status already reports it.
    return 0
  fi

  cat >&2 <<EOF
gc_lint_mem_cap: golangci-lint died under a memory cap (exit $status, signal $sig).
  $confidence
  A memory cap was applied on purpose, not incidental: GC_LINT_MEMORY_MAX=$GC_LINT_MEMORY_MAX (set by the GC_LINT_MEMORY_MAX env var), GC_LINT_MEMORY_SWAP_MAX=$GC_LINT_MEMORY_SWAP_MAX (GC_LINT_MEMORY_SWAP_MAX).
  Where 7G came from: measured golangci-lint peaks on this repo range 5.40-9.85 GiB depending on scope and GOMEMLIMIT; 7G sits above the capped peak (so a normal run passes) and below the uncapped peak (so it actually binds). Full measurements are in this file's header comment.
  Your options: raise the ceiling for this run only, e.g. GC_LINT_MEMORY_MAX=10G <command>; or reduce the lint scope (fewer packages, or set GC_LINT_GOMEMLIMIT lower) so it fits under the current cap.
EOF
}

gc_lint_mem_cap_exec() {
  local status
  # gc_lint_mem_cap_available returns non-zero for every non-"ok" outcome,
  # and this file is sourced into callers running under `set -euo
  # pipefail` (lint-run.sh) -- without `|| true` here, errexit kills this
  # function on this line before the case block below ever runs, so
  # GC_LINT_NO_MEM_CAP=1 and every bad-value diagnostic are unreachable.
  status="$(gc_lint_mem_cap_available)" || true
  case "$status" in
    ok)
      # Do NOT exec here. Execing replaces this process, so if the capped
      # command gets OOM-killed there is nothing left running to explain
      # why -- the caller just sees a bare exit 137. Run it as a child
      # instead, so this function is still alive to report on the way out.
      # A named unit (no --collect) lets us read the scope's own
      # Result=oom-kill afterward; it is reset-failed below either way so
      # transient units don't accumulate.
      local unit="gc-lint-mem-cap-$$-$RANDOM"
      local run_status
      set +e
      systemd-run --user --scope --quiet --unit="$unit" \
        -p MemoryMax="$GC_LINT_MEMORY_MAX" \
        -p MemorySwapMax="$GC_LINT_MEMORY_SWAP_MAX" -- "$@"
      run_status=$?
      set -e
      if [[ "$run_status" -gt 128 ]]; then
        gc_lint_mem_cap_report_kill "$run_status" "$unit"
      fi
      if command -v systemctl >/dev/null 2>&1; then
        systemctl --user reset-failed "${unit}.scope" >/dev/null 2>&1 || true
      fi
      return "$run_status"
      ;;
    bad-value)
      echo "gc_lint_mem_cap: GC_LINT_MEMORY_MAX=$GC_LINT_MEMORY_MAX / GC_LINT_MEMORY_SWAP_MAX=$GC_LINT_MEMORY_SWAP_MAX was rejected; refusing to run golangci-lint without a working memory cap. systemd's MemoryMax=/MemorySwapMax= want a byte count, a K/M/G/T suffix with no 'i' and no trailing 'B' (e.g. 7G, not 7GiB or 7GB), or are rejected outright if set to 'infinity' (unlimited never binds)." >&2
      return 1
      ;;
    no-manager)
      if [[ "${GC_LINT_NO_MEM_CAP:-0}" != "1" ]]; then
        echo "gc_lint_mem_cap: WARNING - no responsive user systemd manager; running '$*' WITHOUT a memory cap. An overshoot here can OOM the whole host, not just this process." >&2
      fi
      exec "$@"
      ;;
    *)
      # Must never fall through silently: a status this function does not
      # recognize would otherwise return 0 from gc_lint_mem_cap_exec without
      # running the wrapped command at all, so `make lint` would exit 0
      # having linted nothing -- the worst possible failure mode for a
      # safety fence. Fail loud instead.
      echo "gc_lint_mem_cap: internal error - gc_lint_mem_cap_available returned unrecognized status '$status'; refusing to run golangci-lint uncapped or silently." >&2
      return 1
      ;;
  esac
}
