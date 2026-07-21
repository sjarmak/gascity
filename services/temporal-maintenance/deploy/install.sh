#!/usr/bin/env bash
# install.sh — deploy the Phase-4 temporal-maintenance systemd --user units.
#
# Idempotent: copies the unit files into ~/.config/systemd/user, reloads the
# user manager, enables + starts the server then the worker. Enables linger so
# the units survive logout / reboot (failure-domain independence from the
# gascity-supervisor login session).
#
# Prereqs: `make build` has produced bin/maintenance-worker; the `temporal` CLI
# is on PATH. Run from services/temporal-maintenance (or anywhere — paths are
# resolved from this script's location).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
UNIT_DIR="${HOME}/.config/systemd/user"
UNITS="temporal-server.service temporal-maintenance-worker.service"

echo "install: staging units into ${UNIT_DIR}"
mkdir -p "$UNIT_DIR"
for u in $UNITS; do
  install -m0644 "${HERE}/${u}" "${UNIT_DIR}/${u}"
done

echo "install: enabling linger (survive logout/reboot)"
loginctl enable-linger "$USER" 2>/dev/null || echo "  (enable-linger needs elevated rights; skipped — units still run while logged in)"

systemctl --user daemon-reload
echo "install: starting temporal-server.service"
systemctl --user enable --now temporal-server.service
echo "install: starting temporal-maintenance-worker.service"
systemctl --user enable --now temporal-maintenance-worker.service

echo "install: done. status:"
systemctl --user --no-pager status temporal-server.service temporal-maintenance-worker.service | sed -n '1,6p' || true
