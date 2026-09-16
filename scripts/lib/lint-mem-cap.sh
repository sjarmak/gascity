#!/usr/bin/env bash
# lint-mem-cap.sh — hard memory ceiling for every golangci-lint invocation.
#
# gc-vrva1q: golangci-lint over ./cmd/gc peaks around 6.35 GiB RSS even at
# concurrency=1 with GOGC halved, and the whole-repo run has been measured at
# 8.08 GiB and, separately, 9.85 GiB. On a host with exhausted swap that
# overshoot has nowhere to spill and reaches the OOM killer directly,
# killing unrelated processes host-wide (dr-krxbg). Soft levers (GOMEMLIMIT,
# --concurrency) lower the peak but do not bound it against a loaded host,
# so every invocation also runs inside a systemd-run --user --scope cgroup
# with a hard MemoryMax: an overshoot becomes a contained cgroup kill of the
# lint process instead of a global OOM.
#
# Usage: gc_lint_mem_cap_exec <golangci-lint-binary> <args...>
# Falls back to plain, uncapped execution when systemd-run or a responsive
# user systemd manager is unavailable (macOS, containers, minimal CI) —
# unbounded-but-running beats a hard dependency on systemd for the gate to
# execute at all. GC_LINT_NO_MEM_CAP=1 opts out explicitly (e.g. nested
# invocation already inside a caller-provided cap).

: "${GC_LINT_MEMORY_MAX:=12G}"

gc_lint_mem_cap_available() {
  [[ "${GC_LINT_NO_MEM_CAP:-0}" != "1" ]] || return 1
  command -v systemd-run >/dev/null 2>&1 || return 1
  systemd-run --user --scope --collect --quiet \
    -p MemoryMax="$GC_LINT_MEMORY_MAX" -- true >/dev/null 2>&1 || return 1
}

gc_lint_mem_cap_exec() {
  if gc_lint_mem_cap_available; then
    exec systemd-run --user --scope --collect --quiet \
      -p MemoryMax="$GC_LINT_MEMORY_MAX" -- "$@"
  fi
  exec "$@"
}
