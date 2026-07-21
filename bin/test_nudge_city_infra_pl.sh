#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

calls_file="$tmp_dir/calls"

cat >"$tmp_dir/gc" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "$*" in
  "session list --json")
    printf '%s\n' '{"ok":true,"sessions":[{"id":"gc-active","template":"city-infra-pl","state":"active"},{"id":"gc-asleep","template":"city-infra-pl","state":"asleep"},{"id":"gc-other","template":"mayor","state":"active"}]}'
    ;;
  session\ nudge\ *)
    if [[ " $* " == *" --message "* ]]; then
      printf '%s\n' 'removed --message flag used' >&2
      exit 2
    fi
    printf '%s\n' "$*" >>"$GC_CALLS_FILE"
    ;;
  *)
    printf 'unexpected gc invocation: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$tmp_dir/gc"

output=$(PATH="$tmp_dir:$PATH" GC_CALLS_FILE="$calls_file" "$script_dir/nudge-city-infra-pl.sh")

[[ "$output" == "nudged 1 city-infra-pl session(s)" ]]
[[ $(wc -l <"$calls_file") -eq 1 ]]
grep -q '^session nudge gc-active City-infra tick: ' "$calls_file"

echo "nudge-city-infra-pl tests passed"
