#!/usr/bin/env bash
# stale-claim-reaper — reset in_progress beads abandoned by dead worker
# processes back to the work pool.
#
# `bd update --claim` is atomic over (assignee, status=in_progress) but has
# no liveness signal. If a worker process dies between claim and the matching
# `bd close`, the claim sticks forever. orphan-sweep handles config-level
# orphans (assignee not in city.toml); this script handles process-level
# orphans (assignee names a configured agent slot whose specific process is
# dead).
#
# Selection criteria for a reap (ALL must hold):
#   - status == in_progress
#   - assignee is non-empty
#   - updated_at older than the rig policy's stale_threshold (default 24h)
#   - no commit in the rig's git log mentions the bead ID since the claim
#   - bead does not carry the policy's exclude_metadata flag
#
# Action: `bd update --status=open --assignee="" <bead>` (NOT close — close
# discards potentially valid work).
#
# Dormant by default: a rig is processed only if it has
# `<rig>/.beads/stale-claim-policy.yaml`.
#
# Dry-run by default: reaping is logged to the audit JSONL but no `bd update`
# is issued unless STALE_CLAIM_REAPER_APPLY=1 (or the deprecated synonym
# GC_STALE_CLAIM_REAPER_APPLY=1) is set in the environment.
#
# Audit log: one JSON line per scan/decision/reap, appended to
# `$GC_PACK_STATE_DIR/stale-claim-audit.jsonl` (defaults under
# `$GC_CITY_RUNTIME_DIR/packs/maintenance/`).
#
# Runs as an exec order (no LLM, no agent, no wisp).
set -euo pipefail

CITY="${GC_CITY:-.}"
PACK_STATE_DIR="${GC_PACK_STATE_DIR:-${GC_CITY_RUNTIME_DIR:-$CITY/.gc/runtime}/packs/maintenance}"
AUDIT_LOG="$PACK_STATE_DIR/stale-claim-audit.jsonl"

APPLY="${STALE_CLAIM_REAPER_APPLY:-${GC_STALE_CLAIM_REAPER_APPLY:-}}"
APPLY_MODE="dry-run"
if [ "$APPLY" = "1" ] || [ "$APPLY" = "true" ]; then
    APPLY_MODE="apply"
fi

mkdir -p "$(dirname "$AUDIT_LOG")"

# now_iso emits an ISO-8601 UTC timestamp suitable for both audit lines
# and `git log --since`.
now_iso() {
    date -u +"%Y-%m-%dT%H:%M:%SZ"
}

# now_epoch emits seconds since epoch for arithmetic.
now_epoch() {
    date -u +"%s"
}

# parse_epoch converts an ISO-8601 timestamp to seconds since epoch.
# Returns "0" if the timestamp is unparseable so the caller can decide
# how to treat it (we conservatively skip beads with unparseable times).
parse_epoch() {
    local ts="$1"
    if [ -z "$ts" ]; then
        echo "0"
        return 0
    fi
    # GNU date understands ISO-8601 with -d.
    date -u -d "$ts" +"%s" 2>/dev/null || echo "0"
}

# duration_to_seconds converts Go-style duration strings (e.g. "24h", "30m",
# "1h30m", "3600s") to seconds. Falls back to 86400 (24h) on parse error.
duration_to_seconds() {
    local dur="$1"
    if [ -z "$dur" ]; then
        echo 86400
        return 0
    fi
    local total=0
    local rest="$dur"
    while [ -n "$rest" ]; do
        # Pull leading [0-9]+ + unit (h|m|s).
        if [[ "$rest" =~ ^([0-9]+)([hms])(.*)$ ]]; then
            local n="${BASH_REMATCH[1]}"
            local unit="${BASH_REMATCH[2]}"
            rest="${BASH_REMATCH[3]}"
            case "$unit" in
                h) total=$((total + n * 3600)) ;;
                m) total=$((total + n * 60)) ;;
                s) total=$((total + n)) ;;
            esac
        else
            # Unparseable suffix — fail safe to default.
            echo 86400
            return 0
        fi
    done
    if [ "$total" -le 0 ]; then
        echo 86400
        return 0
    fi
    echo "$total"
}

# read_policy_value extracts a single scalar value from a tiny YAML file
# (`key: value` with optional quotes). Returns empty string when missing.
# This is a deliberately small parser: the policy schema is flat with three
# documented keys, and we don't want a hard dep on yq/python in the
# maintenance container.
read_policy_value() {
    local file="$1"
    local key="$2"
    [ -f "$file" ] || { echo ""; return 0; }
    awk -v k="$key" '
        /^[[:space:]]*#/ { next }
        {
            line = $0
            if (match(line, /^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]*:/)) {
                kk = line
                sub(/^[[:space:]]*/, "", kk)
                sub(/[[:space:]]*:.*/, "", kk)
                if (kk == k) {
                    val = line
                    sub(/^[^:]*:[[:space:]]*/, "", val)
                    # Strip trailing inline comment outside of quotes (best
                    # effort: strip after first " #").
                    sub(/[[:space:]]+#.*/, "", val)
                    # Strip surrounding quotes.
                    if (match(val, /^".*"$/) || match(val, /^'\''.*'\''$/)) {
                        val = substr(val, 2, length(val) - 2)
                    }
                    print val
                    exit
                }
            }
        }
    ' "$file"
}

# json_escape quotes a string for embedding in the audit JSONL.
json_escape() {
    local s="$1"
    # Use python3 if available — robust for any Unicode. Otherwise fall back
    # to a minimal escaper that handles the printable-ASCII cases the audit
    # records carry (bead IDs, agent names, ISO timestamps).
    if command -v python3 >/dev/null 2>&1; then
        # printf avoids the trailing newline that <<< would inject; otherwise
        # every JSON string would carry a literal "\n" at the end.
        printf '%s' "$s" | python3 -c 'import json,sys; sys.stdout.write(json.dumps(sys.stdin.read()))'
        return 0
    fi
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    s="${s//$'\n'/\\n}"
    s="${s//$'\r'/\\r}"
    s="${s//$'\t'/\\t}"
    printf '"%s"' "$s"
}

# audit appends a single JSONL record. Writes are well under PIPE_BUF so
# concurrent appends from overlapping invocations stay line-atomic on Linux.
audit() {
    local action="$1"
    local rig="$2"
    local bead_id="$3"
    local assignee="$4"
    local updated_at="$5"
    local age_seconds="$6"
    local reason="${7:-}"

    local now
    now=$(now_iso)
    {
        printf '{"ts":%s,"action":%s,"apply_mode":%s,"rig":%s,"bead_id":%s,"assignee":%s,"updated_at":%s,"age_seconds":%s,"reason":%s}\n' \
            "$(json_escape "$now")" \
            "$(json_escape "$action")" \
            "$(json_escape "$APPLY_MODE")" \
            "$(json_escape "$rig")" \
            "$(json_escape "$bead_id")" \
            "$(json_escape "$assignee")" \
            "$(json_escape "$updated_at")" \
            "$age_seconds" \
            "$(json_escape "$reason")"
    } >> "$AUDIT_LOG"
}

# Step 1: enumerate rigs.
RIGS_JSON=$(gc rig list --json 2>/dev/null) || RIGS_JSON="[]"
if [ -z "$RIGS_JSON" ] || [ "$RIGS_JSON" = "[]" ]; then
    exit 0
fi

# `gc rig list --json` emits objects with a `path` field per rig.
RIG_PATHS=$(echo "$RIGS_JSON" | jq -r '.[].path' 2>/dev/null) || exit 0
if [ -z "$RIG_PATHS" ]; then
    exit 0
fi

NOW_EPOCH=$(now_epoch)
TOTAL_REAPED=0
TOTAL_SCANNED=0
TOTAL_DRYRUN=0

while IFS= read -r RIG; do
    [ -z "$RIG" ] && continue
    POLICY_FILE="$RIG/.beads/stale-claim-policy.yaml"

    # Dormant by default: skip rigs without a policy file.
    if [ ! -f "$POLICY_FILE" ]; then
        continue
    fi

    THRESHOLD_RAW=$(read_policy_value "$POLICY_FILE" "stale_threshold")
    EXCLUDE_META=$(read_policy_value "$POLICY_FILE" "exclude_metadata")
    THRESHOLD_SECONDS=$(duration_to_seconds "$THRESHOLD_RAW")

    # Step 2: list in_progress beads with assignees. We list once per rig
    # because the bd store is global — `gc rig list` may return multiple
    # rig paths but they all see the same bead store. We still scope the
    # commit-search to each rig's git tree.
    BEADS_JSON=$(bd list --status=in_progress --json --limit=0 2>/dev/null) || BEADS_JSON="[]"
    if [ -z "$BEADS_JSON" ] || [ "$BEADS_JSON" = "[]" ]; then
        continue
    fi

    # Pre-compute the excluded set if the policy names an exclude metadata
    # key. Filtering at the bd layer avoids surfacing those beads at all.
    EXCLUDED_IDS=""
    if [ -n "$EXCLUDE_META" ]; then
        EXCLUDED_JSON=$(bd list --status=in_progress --json --limit=0 --metadata-field "$EXCLUDE_META=true" 2>/dev/null) || EXCLUDED_JSON="[]"
        EXCLUDED_IDS=$(echo "$EXCLUDED_JSON" | jq -r '.[].id' 2>/dev/null) || EXCLUDED_IDS=""
    fi

    is_excluded() {
        local id="$1"
        [ -z "$EXCLUDED_IDS" ] && return 1
        while IFS= read -r ex; do
            [ "$ex" = "$id" ] && return 0
        done <<< "$EXCLUDED_IDS"
        return 1
    }

    # Iterate candidate beads.
    while IFS=$'\t' read -r BEAD_ID ASSIGNEE UPDATED_AT; do
        [ -z "$BEAD_ID" ] && continue
        [ -z "$ASSIGNEE" ] && continue

        TOTAL_SCANNED=$((TOTAL_SCANNED + 1))

        # exclude_metadata filter.
        if is_excluded "$BEAD_ID"; then
            audit "reap-skipped-excluded" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" 0 "metadata flag set"
            continue
        fi

        # stale_threshold filter.
        UPDATED_EPOCH=$(parse_epoch "$UPDATED_AT")
        if [ "$UPDATED_EPOCH" = "0" ]; then
            audit "reap-skipped-not-stale" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" 0 "unparseable updated_at"
            continue
        fi
        AGE=$((NOW_EPOCH - UPDATED_EPOCH))
        if [ "$AGE" -lt "$THRESHOLD_SECONDS" ]; then
            audit "reap-skipped-not-stale" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "age below threshold"
            continue
        fi

        # commit-mention filter. Non-git rigs cannot shadow the reap with
        # commit evidence, so we proceed straight to the reap decision. For
        # git rigs, a failed `git log` (corrupt repo, permissions, etc.) is
        # ambiguous: we cannot tell whether commits exist, so we skip the
        # bead with a distinct audit action rather than risk a false reap.
        if [ -d "$RIG/.git" ]; then
            # Use literal --grep (no regex meta in canonical bead IDs) and
            # bound the search to the lifetime of the claim.
            if COMMIT_HITS=$(git -C "$RIG" log --grep "$BEAD_ID" --since "$UPDATED_AT" --oneline 2>/dev/null); then
                if [ -n "$COMMIT_HITS" ]; then
                    audit "reap-skipped-recent-commit" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "git log mentions bead"
                    continue
                fi
            else
                audit "reap-skipped-git-error" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "git log failed"
                continue
            fi
        fi

        # Selection passed — reap or dry-run.
        if [ "$APPLY_MODE" = "apply" ]; then
            if bd update "$BEAD_ID" --status=open --assignee="" >/dev/null 2>&1; then
                audit "reap-applied" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "process-orphan"
                TOTAL_REAPED=$((TOTAL_REAPED + 1))
            else
                audit "reap-failed" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "bd update returned non-zero"
            fi
        else
            audit "reap-dry-run" "$RIG" "$BEAD_ID" "$ASSIGNEE" "$UPDATED_AT" "$AGE" "process-orphan"
            TOTAL_DRYRUN=$((TOTAL_DRYRUN + 1))
        fi
    done < <(echo "$BEADS_JSON" | jq -r '.[] | select(.assignee != null and .assignee != "") | "\(.id)\t\(.assignee)\t\(.updated_at)"' 2>/dev/null)
done <<< "$RIG_PATHS"

if [ "$TOTAL_REAPED" -gt 0 ] || [ "$TOTAL_DRYRUN" -gt 0 ]; then
    echo "stale-claim-reaper: scanned=$TOTAL_SCANNED reaped=$TOTAL_REAPED dry-run-candidates=$TOTAL_DRYRUN mode=$APPLY_MODE"
fi
