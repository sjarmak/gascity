#!/usr/bin/env bash
# gc-janitor.sh -- worktree GC patrol for the gascity rig.
#
# Classifies every registered worktree of $JANITOR_REPO plus every top-level
# directory under $JANITOR_WORKTREE_DIR, using the same rules as the
# 2026-07-03 census:
#
#   MISSING_DIR           registered worktree whose directory is gone
#   DIRTY                 uncommitted changes (or git status failed)
#   CLEAN_MERGED          clean tree, HEAD an ancestor of the base ref
#   CLEAN_PUSHED_UNMERGED clean, ahead of base ref, HEAD on some remote ref
#   UNPUSHED              clean, commits reachable from no remote ref
#   ORPHANED              directory in the worktree dir that is not registered
#
# SAFETY MODEL (non-negotiable, no override flags exist):
#   * DRY-RUN IS THE DEFAULT. Deletion requires an explicit --execute.
#   * DIRTY, UNPUSHED, CLEAN_PUSHED_UNMERGED and ORPHANED entries are NEVER
#     removed, under every flag combination. Only CLEAN_MERGED registered
#     worktrees inside the worktree dir are removal candidates.
#   * Removal is `git worktree remove` WITHOUT --force, so git re-verifies
#     cleanliness at removal time (second gate against races). There is
#     deliberately no way to pass --force through this script.
#   * Candidates that match the protect regex (default: the polecat-1..9
#     pool worktrees and everything under the live polecat/ rig-infra dir)
#     are kept even when CLEAN_MERGED.
#   * Candidates younger than --min-age-days (mtime) are kept.
#   * A per-run cap (--max-remove, default 25) bounds each patrol tick;
#     oldest candidates go first.
#   * --execute refuses to run when the base ref is stale (last fetch >24h
#     ago) unless --fetch is given: merged-ness must be judged fresh.
#   * `git worktree prune` (for MISSING_DIR registrations) only runs with
#     --execute --prune; otherwise the command is printed as a proposal.
#
# Every classification and every keep/remove decision is logged to stdout as
# tab-separated lines:  CLASSIFY <class> <detail> <path>
#                       DECIDE   <action> <reason> <path>
#
# Environment defaults (overridden by flags): JANITOR_REPO,
# JANITOR_WORKTREE_DIR, JANITOR_BASE_REF, JANITOR_MODE (--dry-run|--execute),
# JANITOR_MAX_REMOVE, JANITOR_MIN_AGE_DAYS, JANITOR_PROTECT_RE.
# Env-first design lets a gc exec order run this with a bare `exec` path and
# tune behavior via [order.env].

set -euo pipefail

REPO="${JANITOR_REPO:-/home/ds/gascity}"
WTDIR="${JANITOR_WORKTREE_DIR:-/home/ds/gascity-worktrees}"
BASE_REF="${JANITOR_BASE_REF:-origin/main}"
MAX_REMOVE="${JANITOR_MAX_REMOVE:-25}"
MIN_AGE_DAYS="${JANITOR_MIN_AGE_DAYS:-3}"
PROTECT_RE="${JANITOR_PROTECT_RE:-^polecat-[0-9]$|^polecat(/.*)?$}"
EXECUTE=0
DO_FETCH=0
DO_PRUNE=0
DO_DU=0
[ "${JANITOR_MODE:---dry-run}" = "--execute" ] && EXECUTE=1

usage() {
  sed -n '2,44p' "$0" | sed 's/^# \{0,1\}//'
  cat <<EOF

Usage: gc-janitor.sh [--dry-run|--execute] [--repo DIR] [--worktree-dir DIR]
                     [--base-ref REF] [--fetch] [--prune] [--du]
                     [--max-remove N] [--min-age-days N] [--protect REGEX]
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) EXECUTE=0 ;;
    --execute) EXECUTE=1 ;;
    --repo) REPO="$2"; shift ;;
    --worktree-dir) WTDIR="$2"; shift ;;
    --base-ref) BASE_REF="$2"; shift ;;
    --fetch) DO_FETCH=1 ;;
    --prune) DO_PRUNE=1 ;;
    --du) DO_DU=1 ;;
    --max-remove) MAX_REMOVE="$2"; shift ;;
    --min-age-days) MIN_AGE_DAYS="$2"; shift ;;
    --protect) PROTECT_RE="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "gc-janitor: unknown argument: $1 (no force/override flags exist, by design)" >&2; exit 2 ;;
  esac
  shift
done

[ -d "$REPO" ]  || { echo "gc-janitor: repo not found: $REPO" >&2; exit 2; }
[ -d "$WTDIR" ] || { echo "gc-janitor: worktree dir not found: $WTDIR" >&2; exit 2; }
case "$WTDIR" in /|/home|/home/*/..*|"") echo "gc-janitor: refusing worktree dir: $WTDIR" >&2; exit 2 ;; esac

# Single-instance lock (auto-released on exit; no stale state file).
LOCK="${TMPDIR:-/tmp}/gc-janitor.$(id -u).lock"
exec 9>"$LOCK"
if ! flock -n 9; then
  echo "gc-janitor: another run holds $LOCK; exiting" >&2
  exit 3
fi

mode_word=DRY-RUN; [ "$EXECUTE" -eq 1 ] && mode_word=EXECUTE
echo "gc-janitor: mode=$mode_word repo=$REPO worktree_dir=$WTDIR base_ref=$BASE_REF max_remove=$MAX_REMOVE min_age_days=$MIN_AGE_DAYS"
echo "gc-janitor: protect_re=$PROTECT_RE"

if [ "$DO_FETCH" -eq 1 ]; then
  echo "gc-janitor: fetching ${BASE_REF%%/*} ..."
  git -C "$REPO" fetch --quiet "${BASE_REF%%/*}"
fi

git -C "$REPO" rev-parse --verify --quiet "$BASE_REF" >/dev/null \
  || { echo "gc-janitor: base ref $BASE_REF does not resolve in $REPO" >&2; exit 2; }

# Freshness gate: merged-ness is only meaningful against a recent fetch.
gitdir="$(git -C "$REPO" rev-parse --git-common-dir)"
case "$gitdir" in /*) ;; *) gitdir="$REPO/$gitdir" ;; esac
fetch_age_h=999999
if [ -f "$gitdir/FETCH_HEAD" ]; then
  fetch_age_h=$(( ( $(date +%s) - $(stat -c %Y "$gitdir/FETCH_HEAD") ) / 3600 ))
fi
if [ "$fetch_age_h" -gt 24 ]; then
  if [ "$EXECUTE" -eq 1 ]; then
    echo "gc-janitor: REFUSING --execute: last fetch was ${fetch_age_h}h ago (>24h). Re-run with --fetch." >&2
    exit 4
  fi
  echo "gc-janitor: WARNING: last fetch was ${fetch_age_h}h ago; merged-ness may be stale"
fi

# ---- pass 0: registered worktree map ---------------------------------------
declare -A REG_SHA REG_BRANCH
while IFS=$'\t' read -r p sha br; do
  REG_SHA["$p"]="$sha"
  REG_BRANCH["$p"]="$br"
done < <(git -C "$REPO" worktree list --porcelain | awk '
  /^worktree /{p=substr($0,10)}
  /^HEAD /{h=$2}
  /^branch /{b=$2; sub("refs/heads/","",b)}
  /^detached$/{b="(detached)"}
  /^$/{if(p!=""){print p "\t" h "\t" b}; p="";h="";b=""}
  END{if(p!=""){print p "\t" h "\t" b}}')

mapfile -t REG_PATHS < <(printf '%s\n' "${!REG_SHA[@]}" | sort)

declare -A COUNT=([MISSING_DIR]=0 [DIRTY]=0 [CLEAN_MERGED]=0 [CLEAN_PUSHED_UNMERGED]=0 [UNPUSHED]=0 [ORPHANED]=0)
CANDIDATES=()   # "mtime<TAB>path"
MISSING=0
now=$(date +%s)

classify_line() { printf 'CLASSIFY\t%s\t%s\t%s\n' "$1" "$2" "$3"; }
decide_line()   { printf 'DECIDE\t%s\t%s\t%s\n' "$1" "$2" "$3"; }

has_nested_registered() { # $1 = path; true if another registered worktree lives under it
  local rp
  for rp in "${REG_PATHS[@]}"; do
    case "$rp" in "$1"/*) return 0 ;; esac
  done
  return 1
}

# ---- pass 1: classify registered worktrees ---------------------------------
for p in "${REG_PATHS[@]}"; do
  sha="${REG_SHA[$p]}"
  br="${REG_BRANCH[$p]}"
  loc=outside; case "$p" in "$WTDIR"/*) loc=inside ;; esac
  rel="${p#"$WTDIR"/}"

  if [ ! -d "$p" ]; then
    COUNT[MISSING_DIR]=$((COUNT[MISSING_DIR]+1)); MISSING=$((MISSING+1))
    classify_line MISSING_DIR "branch=$br;registered;dir_gone;$loc" "$p"
    decide_line prune-candidate "handled by 'git worktree prune' (gated behind --execute --prune)" "$p"
    continue
  fi

  if ! st="$(git -C "$p" status --porcelain 2>/dev/null)"; then
    COUNT[DIRTY]=$((COUNT[DIRTY]+1))
    classify_line DIRTY "branch=$br;git_status_error;$loc" "$p"
    decide_line keep "REFUSED: status unreadable is treated as DIRTY; never removed" "$p"
    continue
  fi
  ndirty=$(printf '%s' "$st" | grep -c . || true)

  merged=no
  git -C "$REPO" merge-base --is-ancestor "$sha" "$BASE_REF" 2>/dev/null && merged=yes
  ahead=$(git -C "$REPO" rev-list --count "$BASE_REF..$sha" 2>/dev/null || echo "?")
  onremote=no
  if [ "$merged" = yes ]; then
    onremote=yes
  elif [ -z "$(git -C "$REPO" rev-list -1 "$sha" --not --remotes 2>/dev/null)" ]; then
    onremote=yes
  fi
  detail="branch=$br;dirty=$ndirty;merged=$merged;ahead=$ahead;on_remote=$onremote;$loc"

  if [ "$ndirty" -gt 0 ]; then
    COUNT[DIRTY]=$((COUNT[DIRTY]+1))
    classify_line DIRTY "$detail" "$p"
    decide_line keep "REFUSED: dirty worktree is never removed" "$p"
  elif [ "$merged" = yes ]; then
    COUNT[CLEAN_MERGED]=$((COUNT[CLEAN_MERGED]+1))
    classify_line CLEAN_MERGED "$detail" "$p"
    if [ "$loc" = outside ]; then
      decide_line keep "outside worktree dir; out of GC scope" "$p"
    elif printf '%s' "$rel" | grep -Eq "$PROTECT_RE"; then
      decide_line keep "protected: '$rel' matches protect regex" "$p"
    elif has_nested_registered "$p"; then
      decide_line keep "REFUSED: contains nested registered worktrees" "$p"
    else
      mtime=$(stat -c %Y "$p")
      age_d=$(( (now - mtime) / 86400 ))
      if [ "$age_d" -lt "$MIN_AGE_DAYS" ]; then
        decide_line keep "age ${age_d}d < min-age ${MIN_AGE_DAYS}d" "$p"
      else
        CANDIDATES+=("$mtime	$p")
      fi
    fi
  elif [ "$onremote" = yes ]; then
    COUNT[CLEAN_PUSHED_UNMERGED]=$((COUNT[CLEAN_PUSHED_UNMERGED]+1))
    classify_line CLEAN_PUSHED_UNMERGED "$detail" "$p"
    decide_line keep "REFUSED: pushed but not merged to $BASE_REF (open-PR state unknown)" "$p"
  else
    COUNT[UNPUSHED]=$((COUNT[UNPUSHED]+1))
    classify_line UNPUSHED "$detail" "$p"
    decide_line keep "REFUSED: commits exist on no remote; removal would lose work" "$p"
  fi
done

# ---- pass 1b: unregistered (orphaned) top-level dirs in the worktree dir ---
for d in "$WTDIR"/*/; do
  d="${d%/}"
  [ -d "$d" ] || continue
  [ -n "${REG_SHA[$d]+x}" ] && continue
  nreg=0
  for rp in "${REG_PATHS[@]}"; do
    case "$rp" in "$d"/*) nreg=$((nreg+1)) ;; esac
  done
  COUNT[ORPHANED]=$((COUNT[ORPHANED]+1))
  classify_line ORPHANED "unregistered;nested_registered=$nreg" "$d"
  decide_line keep "REFUSED: unregistered dirs are never touched (may be live rig infra)" "$d"
done

# ---- pass 2: act on candidates (oldest first, capped) -----------------------
removed=0 deferred=0 refused_by_git=0 would=0
if [ "${#CANDIDATES[@]}" -gt 0 ]; then
  mapfile -t ORDERED < <(printf '%s\n' "${CANDIDATES[@]}" | sort -n)
  i=0
  for line in "${ORDERED[@]}"; do
    p="${line#*	}"
    i=$((i+1))
    if [ "$i" -gt "$MAX_REMOVE" ]; then
      deferred=$((deferred+1))
      decide_line deferred "beyond --max-remove $MAX_REMOVE this run" "$p"
      continue
    fi
    if [ "$DO_DU" -eq 1 ]; then
      sz_kb=$(du -sk "$p" 2>/dev/null | cut -f1)
      szinfo=" (${sz_kb}KB)"
    else
      szinfo=""
    fi
    if [ "$EXECUTE" -eq 1 ]; then
      if git -C "$REPO" worktree remove "$p"; then
        removed=$((removed+1))
        decide_line removed "git worktree remove (no --force)$szinfo" "$p"
      else
        refused_by_git=$((refused_by_git+1))
        decide_line refused-by-git "git worktree remove declined (second gate); left in place" "$p"
      fi
    else
      would=$((would+1))
      decide_line would-remove "CLEAN_MERGED; dry-run$szinfo -- rerun with --execute to remove" "$p"
    fi
  done
fi

# ---- prune stale registrations ----------------------------------------------
if [ "$MISSING" -gt 0 ] || [ "$DO_PRUNE" -eq 1 ]; then
  if [ "$EXECUTE" -eq 1 ] && [ "$DO_PRUNE" -eq 1 ]; then
    echo "gc-janitor: running: git -C $REPO worktree prune --verbose"
    git -C "$REPO" worktree prune --verbose
  else
    echo "gc-janitor: PROPOSED (needs --execute --prune): git -C $REPO worktree prune --verbose  # $MISSING stale registration(s)"
  fi
fi

# ---- summary -----------------------------------------------------------------
echo "gc-janitor: summary mode=$mode_word"
for k in CLEAN_MERGED CLEAN_PUSHED_UNMERGED DIRTY UNPUSHED ORPHANED MISSING_DIR; do
  printf 'gc-janitor:   %-22s %d\n' "$k" "${COUNT[$k]}"
done
printf 'gc-janitor:   candidates=%d would_remove=%d removed=%d deferred=%d refused_by_git=%d\n' \
  "${#CANDIDATES[@]}" "$would" "$removed" "$deferred" "$refused_by_git"
