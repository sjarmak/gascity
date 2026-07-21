#!/usr/bin/env bash
set -euo pipefail

source_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

install -d -m 0755 "$unit_dir"
install -m 0644 "$source_dir/completion-reconciler.service" "$unit_dir/"
install -m 0644 "$source_dir/completion-reconciler.timer" "$unit_dir/"
systemctl --user daemon-reload
systemctl --user enable --now completion-reconciler.timer
