#!/usr/bin/env bash
# lint-run.sh — the single choke point every Makefile lint target calls
# through, so the gc-vrva1q memory fence applies uniformly instead of being
# reconstructed per target.
#
# Applies, in order:
#   1. GOMEMLIMIT (soft): makes the Go GC work harder before growing the
#      heap. Only lowers the peak if set BELOW the natural peak; measured on
#      ./cmd/gc/..., 3 contained samples each, cache cleared between samples:
#      unfixed (no levers) 8.18-8.61 GiB; GOMEMLIMIT=3GiB + --concurrency=1
#      5.40-5.47 GiB. GOMEMLIMIT takes Go's OWN suffix grammar: B/KiB/MiB/
#      GiB/TiB, WITH the "i". Do not copy this spelling to GC_LINT_MEMORY_MAX
#      below — systemd's grammar is different and silently rejects it.
#   2. --concurrency (soft): golangci-lint defaults to NumCPU; per-package
#      analyzer state is what scales, so serializing bounds the number of
#      packages resident at once. Cost: 5-8x wall time on the package
#      measured above (1:01-1:17 unfixed vs 6:30-6:43 at concurrency=1).
#      Applies everywhere this wrapper is used, including CI runners that
#      do not share this host's memory constraint.
#   3. A hard systemd-run --user --scope MemoryMax cgroup ceiling (see
#      scripts/lib/lint-mem-cap.sh): the soft levers above are not a safety
#      guarantee (a caller can override GC_LINT_GOMEMLIMIT or GOMEMLIMIT
#      itself), so every invocation also runs capped. GC_LINT_MEMORY_MAX
#      uses systemd's grammar: K/M/G/T, WITHOUT the "i", no trailing "B".
#      A value systemd rejects (e.g. "7GiB", "7GB") is a hard failure, not
#      a silent uncapped run — see lint-mem-cap.sh.
#
# Usage: lint-run.sh <golangci-lint-binary> <subcommand> <args...>
# --concurrency is only added for the "run" subcommand ("fmt" rejects it).
# Override any lever per-invocation: GC_LINT_GOMEMLIMIT, GC_LINT_CONCURRENCY,
# GC_LINT_MEMORY_MAX (see lint-mem-cap.sh), GC_LINT_NO_MEM_CAP=1 to skip the
# cgroup entirely (e.g. a caller that already wraps this script in its own
# cap).

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/lint-mem-cap.sh
source "$script_dir/lib/lint-mem-cap.sh"

: "${GC_LINT_GOMEMLIMIT:=3GiB}"
: "${GC_LINT_CONCURRENCY:=1}"

bin="$1"
subcommand="$2"
shift 2

concurrency_args=()
if [[ "$subcommand" == "run" ]]; then
  case " $* " in
    *" --concurrency"*|*" -j"*)
      # Caller already specified concurrency explicitly; do not override it.
      ;;
    *)
      concurrency_args=(--concurrency "$GC_LINT_CONCURRENCY")
      ;;
  esac
fi

GOMEMLIMIT="$GC_LINT_GOMEMLIMIT" gc_lint_mem_cap_exec "$bin" "$subcommand" "${concurrency_args[@]}" "$@"
