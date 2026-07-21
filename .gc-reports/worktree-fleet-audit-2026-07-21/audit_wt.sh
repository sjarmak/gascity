#!/usr/bin/env bash
# Classify every worktree: SAFE (merged + clean) / UNMERGED / DIRTY.
cd /home/ds/projects
OUT=/tmp/claude-1000/-home-ds-projects-persona-maker/8fec5849-6479-4525-b13a-981713e9c37e/scratchpad/worktrees.tsv
: > "$OUT"
for r in EnterpriseBench mem scix_experiments code-intelligence-digest aoa migration-evals codeprobe CodeScaleBench live_docs; do
  primary=$(git -C "$r" rev-parse --show-toplevel 2>/dev/null) || continue
  # integration ref: prefer main, else master
  for cand in main master; do
    git -C "$r" rev-parse --verify -q "$cand" >/dev/null && { base=$cand; break; }
  done
  [ -z "${base:-}" ] && continue
  git -C "$r" worktree list --porcelain | awk '/^worktree /{print $2}' | while read -r wt; do
    [ "$wt" = "$primary" ] && continue
    [ -d "$wt" ] || continue
    br=$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "?")
    sha=$(git -C "$wt" rev-parse HEAD 2>/dev/null || echo "?")
    dirty=$(git -C "$wt" status --porcelain 2>/dev/null | wc -l)
    ahead=$(git -C "$r" rev-list --count "$base..$sha" 2>/dev/null || echo "?")
    if [ "$dirty" -gt 0 ]; then cls=DIRTY
    elif [ "$ahead" = "0" ]; then cls=SAFE
    else cls=UNMERGED; fi
    printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$cls" "$r" "$br" "$ahead" "$dirty" "$wt" >> "$OUT"
  done
  unset base
done
echo "=== totals ==="
cut -f1 "$OUT" | sort | uniq -c | sort -rn
echo; echo "=== by repo ==="
awk -F'\t' '{k=$2"\t"$1; c[k]++} END{for(i in c) print c[i]"\t"i}' "$OUT" | sort -k2,2 -k1,1rn
