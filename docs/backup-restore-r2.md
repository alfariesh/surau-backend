# PostgreSQL Backups to Cloudflare R2

Three complementary layers protect the data, all failing loudly to the
operator's Telegram bot (decision O-F1-1):

1. **Daily encrypted dumps** (`pg_dump | zstd | age` → R2), verified weekly by
   restoring into a temporary PostgreSQL container; a dead-man check alarms
   when no fresh dump lands in R2 for >26 hours.
2. **Continuous WAL archiving / PITR** (pgBackRest → R2, E3): every database
   change is shipped within ≤5 minutes (`archive_timeout=300`), so recovery to
   an arbitrary point in time is possible — RPO ≤5 min instead of 24 h.
3. **Pre-deploy snapshots** (E6): every deploy uploads an encrypted dump to R2
   (`predeploy/<env>/`, 7-day retention) before migrations run.

## Layout

- Bucket: `surau-backend-backups`
- Prefixes:
  - `postgres/prod/`, `postgres/dev/` — daily encrypted dumps
    (`surau-postgres-<UTCstamp>-<gitsha>.dump.zst.age` + `.sha256` over the
    ciphertext). Legacy plaintext objects under `postgres/` age out via the
    30-day lifecycle.
  - `pitr/prod/`, `pitr/dev/` — pgBackRest repos (base backups + WAL,
    AES-256 encrypted, retention `repo1-retention-full=2` ≈ 2 weeks of PITR).
  - `predeploy/prod/`, `predeploy/dev/` — pre-deploy snapshots
    (`surau-predeploy-<UTCstamp>-<gitsha>.dump.zst.age`, pruned after 7 days
    by the snapshot script itself).
- Lifecycle: expire `postgres/` objects after 30 days.

Create an R2 API token in Cloudflare with Object Read & Write permission scoped
to the `surau-backend-backups` bucket. Store the Access Key ID and Secret Access
Key in `/etc/surau-backup/env`.

For local dump/restore smoke tests before R2 credentials are available, run the
backup command with `R2_UPLOAD_REQUIRED=0`. The systemd service should keep
`R2_UPLOAD_REQUIRED=1` so scheduled backups fail loudly if R2 upload is not
configured.

## Encryption

Dumps contain user PII (emails, password hashes) and are never uploaded in
plaintext. The `age` key pair is deliberately **separate from the R2
credentials**: compromising the bucket token yields ciphertext only.

- `AGE_RECIPIENT` (public key, in `/etc/surau-backup/env`) encrypts. The backup
  script **fails hard** when it is unset — no silent plaintext fallback.
- `/etc/surau-backup/age.key` (private key, root 0600, `AGE_KEY_FILE`) decrypts
  during restore-check. It must never be uploaded to the bucket.
- **Escrow:** keep an offline copy of `age.key` (operator password manager).
  If the VPS is lost *and* no escrow copy exists, backups in R2 are unreadable.

First-time key setup on a host:

```sh
sudo age-keygen -o /etc/surau-backup/age.key
sudo chmod 600 /etc/surau-backup/age.key
# put the printed "public key: age1..." into AGE_RECIPIENT in /etc/surau-backup/env
```

Manual decrypt of an artifact:

```sh
age -d -i /etc/surau-backup/age.key surau-postgres-...dump.zst.age | zstd -dc > backup.dump
```

## Alerts & reports (Telegram)

`surau-notify` posts to the operator's Telegram bot using `TELEGRAM_BOT_TOKEN`
and `TELEGRAM_CHAT_ID` from `/etc/surau-backup/env`, prefixed with
`[${ENV_LABEL}]`. (Email fallback is planned with F1-B.)

- `surau-alert@.service` is wired as `OnFailure=` on the backup and
  restore-check services: any failed run alerts immediately with journal tail.
- `surau-backup-watchdog` (every 2h) checks the newest object in this host's R2
  prefix and alarms when it is older than `MAX_BACKUP_AGE_HOURS` (26) — the
  dead-man switch for *silent* failures.
- The weekly restore-check sends a short success report (duration + invariant
  counts) with `NOTIFY_ON_SUCCESS=1`.

## PITR (pgBackRest, E3)

The db container is `surau-db:latest` ([ops/pitr/Dockerfile.db](../ops/pitr/Dockerfile.db)
= `postgres:18.4-alpine` + pgBackRest). Compose starts postgres with
`archive_mode=on`, `archive_command='pgbackrest --stanza=main archive-push %p'`,
`archive_timeout=300`. Config lives on the host at
`/etc/surau-backup/pgbackrest.conf` (template:
[pgbackrest.conf.example](../ops/backup/pgbackrest.conf.example)), mounted
read-only into the container; it holds the R2 keys and the **repo cipher
passphrase** (AES-256) — keep an offline escrow copy of that passphrase
together with the age key. Repo per host: `pitr/prod` vs `pitr/dev`.

- `surau-pitr-backup` (daily 03:30): base backup — full on Sunday, diff
  otherwise; retention expires old sets automatically. Failure → Telegram.
- `surau-pitr-check` (every 6h): `pgbackrest check` forces a WAL switch and
  proves the segment reaches R2. Failure → Telegram (silent archiving death
  surfaces within 6h).
- `surau-pg-pitr-restore '<YYYY-MM-DD HH:MM:SS+TZ>'` — restores the repo into
  a TEMPORARY container up to the given time (`--type=time`,
  `--target-action=promote`), validates corpus invariants, runs any extra SQL
  you pass, then tears down (set `KEEP_CONTAINER=1` to keep it running, e.g.
  to extract rows during a real incident). The temporary instance starts with
  `archive_mode` off, so it can never pollute the WAL archive.

First-time setup on a host (after `install.sh`):

```sh
# 1. fill /etc/surau-backup/pgbackrest.conf; chown it to the CONTAINER postgres
#    uid (check: docker exec <db> id -u postgres), chmod 400
# 2. build + restart the db with archiving enabled (brief API blip):
sudo docker compose --env-file .env.production -f docker-compose.prod.yml build db
sudo docker compose --env-file .env.production -f docker-compose.prod.yml up -d db
# 3. create the stanza and take the first full backup:
sudo docker compose --env-file .env.production -f docker-compose.prod.yml exec -T -u postgres db \
  pgbackrest --stanza=main stanza-create
sudo /usr/local/sbin/surau-pitr-backup
sudo /usr/local/sbin/surau-pitr-check
```

## Pre-deploy snapshots (E6)

Both deploy workflows call `sudo /usr/local/sbin/surau-predeploy-snapshot`
before the app migrates: encrypted custom-format dump to
`/var/backups/surau/predeploy/` + upload to `r2://…/predeploy/<env>/`, both
pruned after 7 days. Dump failure **aborts the deploy**; R2-upload failure
alarms Telegram but lets the deploy continue (the local artifact still covers
rollback). Restore one with the same recipe as any encrypted dump (see
docs/deploy-vps.md §Pemulihan schema DIRTY).

## VPS Files

- `/usr/local/sbin/surau-pg-backup` · `surau-pg-restore-check` ·
  `surau-backup-watchdog` · `surau-notify` · `surau-alert` ·
  `surau-pitr-backup` · `surau-pitr-check` · `surau-pg-pitr-restore` ·
  `surau-predeploy-snapshot`
- `/etc/systemd/system/surau-pg-backup.{service,timer}` (daily 04:00),
  `surau-pg-restore-check.{service,timer}` (weekly Mon 06:00),
  `surau-backup-watchdog.{service,timer}` (every 2h),
  `surau-pitr-backup.{service,timer}` (daily 03:30),
  `surau-pitr-check.{service,timer}` (every 6h), `surau-alert@.service`
- `/etc/surau-backup/env` (0600), `/etc/surau-backup/age.key` (0600),
  `/etc/surau-backup/pgbackrest.conf` (0400, owned by container postgres uid)
- `/var/backups/surau/postgres/` and `/var/backups/surau/predeploy/`

Install/update everything from a checkout of this repo:

```sh
sudo ops/backup/install.sh
```

## Commands

Run a backup now:

```sh
sudo systemctl start surau-pg-backup.service
```

Watch logs:

```sh
sudo journalctl -u surau-pg-backup.service -n 100 --no-pager
```

Restore-check the latest R2 backup (add `NOTIFY_ON_SUCCESS=1` to also report
to Telegram):

```sh
sudo /usr/local/sbin/surau-pg-restore-check r2-latest
```

Restore-check the latest local backup:

```sh
sudo /usr/local/sbin/surau-pg-restore-check local-latest
```

Send a test Telegram message / run the dead-man check now:

```sh
sudo /usr/local/sbin/surau-notify "test"
sudo systemctl start surau-backup-watchdog.service
```

## Tests

`ops/backup/test-backup-scripts.sh` (run in CI job `backup-scripts` together
with shellcheck) covers the encrypt→checksum→decrypt round-trip, wrong-key
rejection, and latest-archive selection across mixed `.zst`/`.zst.age` names.

## Drill log

| # | Date (UTC) | Scenario | Source | Duration | Result |
|---|---|---|---|---|---|
| 1 | 2026-07-07 | Full restore of encrypted dump from R2 into empty instance (invariants: books ≥1, pages ≥1, ayahs = 6236) | prod, `postgres/prod/` r2-latest (`surau-postgres-20260707T082107Z-28e91ba.dump.zst.age`, 242 MB) | **241 s** end-to-end (download + decrypt + restore + checks) — RTO target ≤4 h | **PASS** — books=161, pages=295604, ayahs=6236; success report delivered to Telegram. Also verified same day: deliberate backup failure → OnFailure Telegram alarm (dev), forced-stale dead-man → Telegram alarm (dev) |
| 2 | 2026-07-07 | **PITR** (E3): marker row committed 2 s before target time T, "incident" row committed 3 s after T (its WAL segment archived to R2), then `surau-pg-pitr-restore T` | dev, pgBackRest repo `pitr/dev` (encrypted, full base backup + WAL) | **82 s** end-to-end (fetch base backup + WAL replay + promote + checks) | **PASS** — before-row present, incident-row absent, invariants intact (books=161, pages=295604, ayahs=6236). Recovery point demonstrably seconds-granular ⇒ RPO ≤5 min honored. Prod archiving proven by `pgbackrest check` same day |
