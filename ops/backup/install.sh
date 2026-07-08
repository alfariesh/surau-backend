#!/usr/bin/env bash
# Idempotent installer for the Surau backup stack. Run as root on the VPS from
# this directory (or point SRC_DIR at it). Does NOT create /etc/surau-backup/env
# or the age key — see docs/backup-restore-r2.md for first-time setup.
set -Eeuo pipefail

SRC_DIR="${SRC_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "run as root (sudo $0)" >&2
  exit 2
fi

scripts=(surau-pg-backup surau-pg-restore-check surau-backup-watchdog surau-notify surau-alert
  surau-pitr-backup surau-pitr-check surau-pg-pitr-restore surau-predeploy-snapshot)
units=(surau-pg-backup.service surau-pg-backup.timer
  surau-pg-restore-check.service surau-pg-restore-check.timer
  surau-backup-watchdog.service surau-backup-watchdog.timer
  surau-pitr-backup.service surau-pitr-backup.timer
  surau-pitr-check.service surau-pitr-check.timer
  surau-alert@.service)

for s in "${scripts[@]}"; do
  install -m 0755 "${SRC_DIR}/${s}" "/usr/local/sbin/${s}"
done

for u in "${units[@]}"; do
  install -m 0644 "${SRC_DIR}/${u}" "/etc/systemd/system/${u}"
done

mkdir -p /etc/surau-backup /var/backups/surau/postgres /var/lib/node_exporter/textfile
chmod 700 /etc/surau-backup

systemctl daemon-reload
systemctl enable --now surau-pg-backup.timer surau-pg-restore-check.timer surau-backup-watchdog.timer \
  surau-pitr-backup.timer surau-pitr-check.timer

if [[ ! -f /etc/surau-backup/env ]]; then
  echo "WARNING: /etc/surau-backup/env is missing — copy env.example and fill it in" >&2
fi
if [[ ! -f /etc/surau-backup/pgbackrest.conf ]]; then
  echo "WARNING: /etc/surau-backup/pgbackrest.conf is missing — copy pgbackrest.conf.example and fill it in (PITR timers will alarm until it exists)" >&2
fi

echo "installed. timers:"
systemctl list-timers --no-pager | grep surau || true
