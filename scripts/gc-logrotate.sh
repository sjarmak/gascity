#!/usr/bin/env bash
# gc-logrotate.sh -- size-based rotation for gascity city logs.
#
# For every configured log file over the size threshold:
#   1. copy the file to <file>.janitor-<timestamp>   (timestamps preserved)
#   2. truncate the live file to 0 bytes             (writer fds stay valid)
#   3. gzip the copy to <file>.janitor-<timestamp>.gz
#   4. prune that file's OWN .janitor-*.gz archives down to the newest N
#
# The live log is NEVER plain-deleted, moved, or renamed; writers holding the
# fd (systemd StandardOutput=append:, dolt, gc runtime tracers) keep working.
# The only deletion this script ever performs is keep-N pruning of the
# compressed archives it created itself (suffix .janitor-*.gz). It refuses to
# touch events.jsonl (gc has its own rotation) and beads.json (state, not a
# log), even if listed.
#
# DRY-RUN IS THE DEFAULT. Rotation requires --execute (or
# JANITOR_LOG_EXECUTE=1 from a gc order's [order.env]).
#
# Copy+truncate caveat: lines appended between the copy and the truncate are
# lost, and non-O_APPEND writers can leave a NUL-prefixed region after
# truncation (same trade-off as logrotate's copytruncate).
#
# Environment defaults: JANITOR_LOG_EXECUTE (0|1), JANITOR_LOG_MAX_MB,
# JANITOR_LOG_KEEP, JANITOR_LOG_LIST (file of globs, one per line, # comments).

set -euo pipefail

MAX_MB="${JANITOR_LOG_MAX_MB:-32}"
KEEP="${JANITOR_LOG_KEEP:-5}"
EXECUTE="${JANITOR_LOG_EXECUTE:-0}"
LIST_FILE="${JANITOR_LOG_LIST:-}"

usage() {
  sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'
  cat <<EOF

Usage: gc-logrotate.sh [--dry-run|--execute] [--max-mb N] [--keep N] [--list FILE]
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) EXECUTE=0 ;;
    --execute) EXECUTE=1 ;;
    --max-mb) MAX_MB="$2"; shift ;;
    --keep) KEEP="$2"; shift ;;
    --list) LIST_FILE="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "gc-logrotate: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

# Default coverage = the unrotated growth paths found by the 2026-07-03 leak
# census. events.jsonl and beads.json are deliberately absent (see header).
#
# 2026-07-21 (audit sec 4.3, gc-pif5): the census covered only the five paths
# below, so the ~35 append-forever logs that order scripts write into
# /home/ds/gas-city/.gc/*.log were covered by NOTHING. stale-worktree-reaper.log
# had reached 30MB accumulating since 2026-05-04 and codegraph-sync.log 20MB,
# with the root filesystem at 89%. Broadened to whole-directory globs so a NEW
# order script is covered the day it starts writing, rather than the day someone
# notices the file. The events.jsonl/beads.json refusals at the rotate_one case
# statement still protect those two by name regardless of what a glob matches.
#
# The removed glob was `runtime/*--control-dispatcher-trace.log`, which matched
# ZERO files: the dispatchers write `<rig>--core.control-dispatcher-trace.log`
# (note the `core.`). Dispatcher trace rotation had therefore never once run,
# which is why 500MB+ of live traces and 1.6GB of orphaned .log.1/.log.2
# remnants were sitting in runtime/. A glob that silently matches nothing is
# indistinguishable from a glob with nothing to do; `runtime/*.log` cannot fail
# that way. First real run: 8 files, ~500MB.
default_globs() {
  cat <<'EOF'
/home/ds/.gc/supervisor.log
/home/ds/.gc/slack-handle-alias-reaper.log
/home/ds/.gc/slack-adapter-autorestart.log
/home/ds/.gc/codegraph-sync.log
/home/ds/gas-city/.gc/*.log
/home/ds/gas-city/.gc/runtime/*.log
/home/ds/gas-city/.beads/dolt-server.log
EOF
}

globs() {
  if [ -n "$LIST_FILE" ]; then
    [ -r "$LIST_FILE" ] || { echo "gc-logrotate: cannot read list file: $LIST_FILE" >&2; exit 2; }
    grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$LIST_FILE"
  else
    default_globs
  fi
}

LOCK="${TMPDIR:-/tmp}/gc-logrotate.$(id -u).lock"
exec 9>"$LOCK"
if ! flock -n 9; then
  echo "gc-logrotate: another run holds $LOCK; exiting" >&2
  exit 3
fi

mode_word=DRY-RUN; [ "$EXECUTE" = 1 ] && mode_word=EXECUTE
threshold=$((MAX_MB * 1024 * 1024))
echo "gc-logrotate: mode=$mode_word threshold=${MAX_MB}MB keep=$KEEP"

rotated=0 skipped=0 pruned=0 refused=0

prune_archives() { # $1 = live log path; deletes this file's own archives beyond KEEP
  local f="$1" dir base old
  dir="$(dirname "$f")"; base="$(basename "$f")"
  while IFS= read -r old; do
    [ -n "$old" ] || continue
    if [ "$EXECUTE" = 1 ]; then
      rm -f -- "$old"
      echo "PRUNE-ARCHIVE	removed (beyond keep=$KEEP)	$old"
    else
      echo "PRUNE-ARCHIVE	would-remove (beyond keep=$KEEP)	$old"
    fi
    pruned=$((pruned+1))
  done < <(find "$dir" -maxdepth 1 -name "$base.janitor-*.gz" -printf '%T@\t%p\n' 2>/dev/null \
             | sort -rn | cut -f2- | tail -n +"$((KEEP+1))")
}

rotate_one() { # $1 = live log path
  local f="$1" sz ts tmp
  case "$(basename "$f")" in
    events.jsonl|beads.json|*.gz)
      echo "REFUSE	denylisted or already compressed	$f"
      refused=$((refused+1)); return ;;
  esac
  if [ ! -f "$f" ] || [ -L "$f" ]; then
    echo "SKIP	not a regular file	$f"; skipped=$((skipped+1)); return
  fi
  sz=$(stat -c %s -- "$f")
  if [ "$sz" -lt "$threshold" ]; then
    echo "SKIP	$((sz / 1024 / 1024))MB < ${MAX_MB}MB	$f"; skipped=$((skipped+1)); return
  fi
  ts=$(date +%Y%m%d-%H%M%S)
  tmp="$f.janitor-$ts"
  if [ "$EXECUTE" = 1 ]; then
    cp --preserve=timestamps -- "$f" "$tmp"
    truncate -s 0 -- "$f"
    gzip -- "$tmp"
    echo "ROTATE	$((sz / 1024 / 1024))MB -> $tmp.gz; live file truncated	$f"
  else
    echo "ROTATE	would rotate $((sz / 1024 / 1024))MB -> $tmp.gz (copy+truncate+gzip)	$f"
  fi
  rotated=$((rotated+1))
  prune_archives "$f"
}

while IFS= read -r g; do
  matched=0
  while IFS= read -r f; do
    if [ -z "$f" ] || [ ! -e "$f" ]; then continue; fi
    matched=1
    rotate_one "$f"
  done < <(compgen -G "$g" || true)
  if [ "$matched" -eq 0 ]; then
    echo "SKIP	no match for glob	$g"; skipped=$((skipped+1))
  fi
done < <(globs)

echo "gc-logrotate: summary mode=$mode_word rotated=$rotated skipped=$skipped archive_pruned=$pruned refused=$refused"
