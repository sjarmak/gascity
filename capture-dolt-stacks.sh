#!/usr/bin/env bash
set -euo pipefail
umask 077

instance_info="/home/ds/gas-city/.beads/dolt/.dolt/sql-server.info"
pid="${1:-}"
if [[ -z "$pid" ]]; then
  if [[ ! -r "$instance_info" ]]; then
    echo "Refusing: managed Dolt instance info is not readable: $instance_info" >&2
    exit 1
  fi
  IFS=: read -r pid _ < "$instance_info"
fi
expected_config="/home/ds/gas-city/.gc/runtime/packs/dolt/dolt-config.yaml"
expected_executable="/home/ds/.local/bin/dolt"
artifact_dir="/home/ds/gas-city/.gc/investigations"

verify_target() {
  local expected_start_time="${1:-}"
  local stat suffix start_time arg executable owner
  local -a argv stat_fields

  if [[ ! "$pid" =~ ^[0-9]+$ ]] || [[ ! -r "/proc/$pid/cmdline" ]] || [[ ! -r "/proc/$pid/stat" ]]; then
    echo "Refusing: PID $pid is not a readable live process." >&2
    return 1
  fi

  executable="$(sudo readlink -f "/proc/$pid/exe")"
  owner="$(stat -c '%u' "/proc/$pid")"
  if [[ "$executable" != "$expected_executable" ]] || [[ "$owner" != "$(id -u)" ]]; then
    echo "Refusing: PID $pid does not have the expected executable and owner." >&2
    return 1
  fi

  mapfile -d '' -t argv < "/proc/$pid/cmdline"
  if (( ${#argv[@]} < 3 )) || [[ "${argv[0]##*/}" != dolt ]] || [[ "${argv[1]}" != sql-server ]]; then
    echo "Refusing: PID $pid is not a dolt sql-server process." >&2
    return 1
  fi
  local has_config=false
  for ((arg = 2; arg < ${#argv[@]}; arg++)); do
    if [[ "${argv[arg]}" == "--config=$expected_config" ]] ||
       [[ "${argv[arg]}" == --config && "${argv[arg + 1]:-}" == "$expected_config" ]]; then
      has_config=true
      break
    fi
  done
  if [[ "$has_config" != true ]]; then
    echo "Refusing: PID $pid does not use the expected managed Dolt config." >&2
    return 1
  fi

  stat="$(<"/proc/$pid/stat")"
  suffix="${stat##*) }"
  read -r -a stat_fields <<< "$suffix"
  start_time="${stat_fields[19]:-}"
  if [[ ! "$start_time" =~ ^[1-9][0-9]*$ ]]; then
    echo "Refusing: PID $pid has an invalid process start identity." >&2
    return 1
  fi
  if [[ -n "$expected_start_time" && "$start_time" != "$expected_start_time" ]]; then
    echo "Refusing: PID $pid was replaced while waiting for sudo." >&2
    return 1
  fi
  printf '%s\n' "$start_time"
}

sudo -v
initial_start_time="$(verify_target)"

mkdir -p "$artifact_dir"
artifact="$artifact_dir/dolt-$pid-stacks-$(date +%Y%m%dT%H%M%S%z).txt"

echo "Capturing privileged thread stacks from PID $pid."
echo "GDB attachment briefly pauses Dolt; run this only with separate operator approval."
echo "Output: $artifact"

verify_target "$initial_start_time" >/dev/null

set +e
# The user shell intentionally opens this private, user-owned artifact before sudo.
# shellcheck disable=SC2024
sudo gdb -q -nx -batch \
  -ex 'set pagination off' \
  -ex 'set debuginfod enabled off' \
  -ex "python stat = open('/proc/$pid/stat').read(); start = stat.rsplit(') ', 1)[1].split()[19]; mismatch = start != '$initial_start_time'; gdb.execute('detach') if mismatch else None; gdb.execute('quit 74') if mismatch else None" \
  -ex 'thread apply all bt' \
  -p "$pid" >"$artifact" 2>&1
gdb_rc=$?
set -e

if (( gdb_rc != 0 )); then
  echo "GDB failed with exit code $gdb_rc. Partial output remains at: $artifact" >&2
  exit "$gdb_rc"
fi

echo "Capture complete: $artifact"
