#!/usr/bin/env bash
# lint-run.sh — the single choke point every Makefile lint target calls
# through, so the gc-vrva1q memory fence applies uniformly instead of being
# reconstructed per target.
#
# Applies, in order:
#   1. GOMEMLIMIT (soft): makes the Go GC work harder before growing the
#      heap. Default matches the measured single-package peak with margin.
#   2. --concurrency (soft): golangci-lint defaults to NumCPU; per-package
#      analyzer state is what scales, so serializing bounds the number of
#      packages resident at once.
#   3. A hard systemd-run --user --scope MemoryMax cgroup ceiling (see
#      scripts/lib/lint-mem-cap.sh): converts any overshoot the soft levers
#      miss into a contained cgroup kill of the lint run, never a host-wide
#      OOM.
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

: "${GC_LINT_GOMEMLIMIT:=10GiB}"
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
