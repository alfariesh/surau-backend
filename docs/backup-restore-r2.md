# PostgreSQL Backups to Cloudflare R2

Production backups are created on the VPS with `pg_dump`, compressed with `zstd`,
uploaded to Cloudflare R2, and verified by restoring into a temporary PostgreSQL
container.

## R2

- Bucket: `surau-backend-backups`
- Prefix: `postgres/`
- Lifecycle: expire `postgres/` objects after 30 days.

Create an R2 API token in Cloudflare with Object Read & Write permission scoped
to the `surau-backend-backups` bucket. Store the Access Key ID and Secret Access
Key in `/etc/surau-backup/env`.

For local dump/restore smoke tests before R2 credentials are available, run the
backup command with `R2_UPLOAD_REQUIRED=0`. The systemd service should keep
`R2_UPLOAD_REQUIRED=1` so scheduled backups fail loudly if R2 upload is not
configured.

## VPS Files

- `/usr/local/sbin/surau-pg-backup`
- `/usr/local/sbin/surau-pg-restore-check`
- `/etc/systemd/system/surau-pg-backup.service`
- `/etc/systemd/system/surau-pg-backup.timer`
- `/etc/surau-backup/env`
- `/var/backups/surau/postgres/`

## Commands

Run a backup now:

```sh
sudo systemctl start surau-pg-backup.service
```

Watch logs:

```sh
sudo journalctl -u surau-pg-backup.service -n 100 --no-pager
```

Restore-check the latest R2 backup:

```sh
sudo /usr/local/sbin/surau-pg-restore-check r2-latest
```

Restore-check the latest local backup:

```sh
sudo /usr/local/sbin/surau-pg-restore-check local-latest
```
