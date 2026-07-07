# PostgreSQL Backups to Cloudflare R2

Backups are created on each VPS with `pg_dump`, compressed with `zstd`,
**encrypted client-side with [age](https://age-encryption.org)**, uploaded to
Cloudflare R2, and verified weekly by restoring into a temporary PostgreSQL
container. Failures alert the operator's Telegram bot (decision O-F1-1), and a
dead-man check alarms when no fresh backup lands in R2 for >26 hours.

## Layout

- Bucket: `surau-backend-backups`
- Prefixes: `postgres/prod/` (prod VPS) and `postgres/dev/` (dev VPS).
  Legacy plaintext objects under `postgres/` age out via the 30-day lifecycle.
- Lifecycle: expire `postgres/` objects after 30 days.
- Artifact name: `surau-postgres-<UTCstamp>-<gitsha>.dump.zst.age` (+ `.sha256`
  over the ciphertext).

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

## VPS Files

- `/usr/local/sbin/surau-pg-backup` · `surau-pg-restore-check` ·
  `surau-backup-watchdog` · `surau-notify` · `surau-alert`
- `/etc/systemd/system/surau-pg-backup.{service,timer}` (daily 04:00),
  `surau-pg-restore-check.{service,timer}` (weekly Mon 06:00),
  `surau-backup-watchdog.{service,timer}` (every 2h), `surau-alert@.service`
- `/etc/surau-backup/env` (0600) and `/etc/surau-backup/age.key` (0600)
- `/var/backups/surau/postgres/`

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
