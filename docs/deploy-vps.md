# Deploy VPS dengan Docker Compose

Dokumen ini untuk deployment di VPS: aplikasi Go berjalan sebagai container, PostgreSQL berjalan sebagai container, dan port aplikasi hanya dibuka ke `127.0.0.1:8080` agar bisa diletakkan di belakang Caddy/Cloudflare.

## Environments & release flow (dev + prod)

Ada DUA VPS terpisah, di-deploy oleh dua GitHub Actions workflow:

| Env | Trigger | Workflow | Domain | Secrets |
|-----|---------|----------|--------|---------|
| **dev** | push ke `main` (auto) | `.github/workflows/deploy-dev.yml` | `dev-api.surau.org` | `DEV_VPS_*` |
| **prod** | push tag `api-vX.Y.Z` (auto) | `.github/workflows/deploy-prod.yml` | `api.surau.org` | `PROD_VPS_*` |

- **`main` = trunk.** Feature branch → PR (CI gate) → merge `main` → auto-deploy ke DEV VPS. `APP_VERSION=dev-<short-sha>`, `APP_ENV=dev`.
- **Rilis prod = tag.** Prod HANYA berubah saat kamu cut tag dari `main`:
  ```sh
  git tag -a api-v0.8.0 -m "API v0.8.0"
  git push origin api-v0.8.0
  ```
  Workflow prod checkout commit di tag itu (detached HEAD), deploy ke PROD VPS (`APP_VERSION=0.8.0`, `APP_ENV=prod`), lalu buat GitHub Release otomatis. **Rollback** = deploy ulang tag sebelumnya (`git push origin api-v0.7.x` ulang, atau `workflow_dispatch`) atau restore snapshot pra-deploy terenkripsi dari `/var/backups/surau/predeploy/` / R2 `predeploy/prod/` (lihat §Pemulihan schema DIRTY).
- **Verifikasi env:** `curl https://api.surau.org/version` → `{"name","version","env":"prod"}`; `curl https://dev-api.surau.org/version` → `env:"dev"`.
- Kedua VPS pakai `docker-compose.prod.yml` + `.env.production` masing-masing (nilai beda: dev pakai `LOG_LEVEL=debug`, `SWAGGER_ENABLED=true`, `EMAIL_DELIVERY_MODE=log`, `ONESIGNAL_ENABLED=false`; prod pakai nilai produksi). Reverse proxy: `deploy/Caddyfile.tmpl` (ganti `{$DOMAIN}` per host).
- **Secrets GitHub** (Settings → Secrets → Actions): `DEV_VPS_HOST/USER/DEPLOY_PATH/SSH_PRIVATE_KEY` + `PROD_VPS_HOST/USER/DEPLOY_PATH/SSH_PRIVATE_KEY`.

Bagian di bawah = langkah setup satu VPS (berlaku untuk dev maupun prod).

## 1. Siapkan server

Install Docker Engine dan Docker Compose plugin di server, lalu clone repo ini.

```sh
git clone <repo-url> surau-backend
cd surau-backend
```

## 2. Buat environment production

```sh
cp .env.production.example .env.production
openssl rand -hex 32
```

Edit `.env.production`:

- Ganti `POSTGRES_PASSWORD`.
- Ganti password yang sama di bagian `PG_URL`.
- Ganti `JWT_SECRET` dengan output `openssl rand -hex 32`.
- Biarkan `JWT_ISSUER=surau-backend` dan `JWT_AUDIENCE=surau-api`, kecuali ada kebutuhan integrasi token khusus.
- Biarkan `AUTH_RATE_LIMIT_ENABLED=true` untuk limiter DB-backed lintas instance; override nilai `AUTH_RATE_LIMIT_*` hanya jika traffic/UX membutuhkan.
- Isi `CF_EMAIL_ACCOUNT_ID`, `CF_EMAIL_API_TOKEN`, `EMAIL_FROM_ADDRESS`, `EMAIL_VERIFY_FRONTEND_URL`, `PASSWORD_RESET_FRONTEND_URL`, `EMAIL_CHANGE_FRONTEND_URL`, dan `EMAIL_UNSUBSCRIBE_PUBLIC_URL=https://api.surau.org/v1/email/unsubscribe`.
- Jika mengaktifkan `EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED=true`, isi `EMAIL_CLOUDFLARE_ZONE_ID` dan `EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN`; token ini harus punya permission GraphQL Analytics Read untuk zone `surau.org`.
- Pastikan domain `EMAIL_FROM_ADDRESS` sudah onboard di Cloudflare Email Service untuk Email Sending.
- Biarkan `APP_BIND_ADDR=127.0.0.1` jika reverse proxy ada di server yang sama.
- Biarkan `APP_PUBLISHED_PORT=8080`, kecuali port 8080 sudah dipakai service lain.
- Isi `CORS_ALLOWED_ORIGINS` dengan origin web frontend (mis. `https://surau.org`); kosongkan jika belum ada client browser. Aplikasi mobile native tidak butuh CORS.
- Konfigurasi reverse proxy (Nginx/Caddy) agar membalas 404 untuk `/internal` dan `/metrics` — keduanya hanya untuk jaringan privat (nginx dev sudah melakukannya di `nginx/nginx.conf`).

Jika memakai database cloud, ganti `PG_URL` ke URL provider. Untuk database yang wajib SSL, pakai `?sslmode=require`.
Jika password database berisi karakter khusus seperti `@`, `#`, `/`, atau `:`, encode password tersebut di `PG_URL`.

## 3. Build dan jalankan

```sh
docker compose --env-file .env.production -f docker-compose.prod.yml up -d --build
```

Aplikasi otomatis menjalankan migration saat container `app` start karena Dockerfile membangun binary dengan tag `migrate`.
Migration auth terbaru menambahkan `users.token_version`, `auth_rate_limits`, `auth_audit_logs`, email verification/reset/change token tables, dan soft-delete account fields. Setelah password reset, change password, change email, atau delete account, JWT lama otomatis ditolak dan user harus login ulang.

## 4. Cek health

```sh
docker compose --env-file .env.production -f docker-compose.prod.yml ps
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
curl -i https://api.surau.org/healthz
```

`/healthz` mengecek proses HTTP. `/readyz` mengecek koneksi PostgreSQL.

## 5. Cloudflare DNS

Di Cloudflare DNS untuk zona `surau.org`, buat record:

- Type: `A`
- Name: `api`
- Content: IP publik VPS
- Proxy status: Proxied

Jika reverse proxy di VPS sudah memakai sertifikat HTTPS valid, gunakan mode SSL/TLS Cloudflare `Full (strict)`.

Untuk email verification dan password reset, buka Cloudflare Dashboard > Compute > Email Service > Email Sending, onboard domain pengirim, lalu pastikan records SPF, DKIM, DMARC, dan `cf-bounce` sudah `Locked`/valid sebelum membuka registrasi publik.

## 6. Reverse proxy contoh

Contoh Nginx host config:

```nginx
server {
    listen 80;
    server_name api.surau.org;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Untuk HTTPS, pasang Certbot atau gunakan Caddy/Cloudflare Tunnel.

## 7. Update aplikasi

```sh
git pull
docker compose --env-file .env.production -f docker-compose.prod.yml up -d --build
docker compose --env-file .env.production -f docker-compose.prod.yml logs -f app
```

## Catatan data

Database disimpan di Docker volume `surau-backend-prod_db_data`, dimount ke `/var/lib/postgresql` sesuai layout image PostgreSQL 18. Jangan jalankan `docker compose down -v` di production kecuali memang ingin menghapus data.

## Tuning Postgres per RAM VPS (F1-G)

Semua setelan server Postgres hidup sebagai flag `-c` di `command:` service `db`
(`docker-compose.prod.yml`) dengan nilai dari `.env.production` — TIDAK ada
postgresql.conf yang di-mount (menghindari tabrakan dengan layout PGDATA
pgBackRest). Flag archive pgBackRest di atasnya JANGAN pernah diubah/dihapus.

Nilai per host (default compose = aman untuk 2GB):

| Knob (env) | dev VPS (2GB) | prod VPS (4GB) | Alasan |
|---|---|---|---|
| `PG_SHARED_BUFFERS` | 256MB (default) | 512MB | ±25% RAM yang dialokasikan utk DB di kotak bersama |
| `PG_EFFECTIVE_CACHE_SIZE` | 1GB (default) | 2GB | Estimasi jujur cache OS (default Postgres 4GB itu bohong di kotak 2GB) |
| `PG_WORK_MEM` | 6MB (default) | 6MB | Kecil × banyak sort paralel; naikkan hanya dengan bukti |
| `PG_MAINTENANCE_WORK_MEM` | 128MB (default) | 128MB | Vacuum/index build lebih cepat |
| `PG_MAX_CONNECTIONS` | 50 (default) | 50 | Pool app 10 + collab 5 + burst CLI/backup + exporter ≪ 50 |
| `PG_LOG_MIN_DURATION` | 200ms (default) | 200ms | = budget p95 baca (charter §2.3); query lambat tampil di `docker logs db` |

`shared_preload_libraries=pg_stat_statements` ikut di command (panel "slow
statements" di Grafana). Ekstensinya dibuat migrasi `20260708000002`.

**Rollout (perubahan tuning perlu restart db ±10–30 dtk — di luar jalur deploy;
db tidak pernah disentuh workflow deploy):**

```sh
cd /srv/surau/backend
# 1) set nilai per-host di .env.production (lihat tabel)
# 2) recreate db dengan flag baru (downtime singkat!)
sudo docker compose --env-file .env.production -f docker-compose.prod.yml up -d db
# 3) WAJIB: pastikan arsip WAL pgBackRest tetap jalan
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec db pgbackrest --stanza=main check
# 4) hidupkan/refresh exporter metrik DB
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  --profile observability up -d postgres-exporter
# 5) verifikasi: SHOW shared_buffers; SHOW log_min_duration_statement;
```

## Keamanan migration (auto-migrate saat boot)

Aplikasi auto-migrate saat boot (build `-tags migrate`). Pipeline deploy sudah: (1) snapshot pra-deploy via `surau-predeploy-snapshot` — pg_dump terenkripsi (age) ke `/var/backups/surau/predeploy/` DAN diunggah ke R2 `predeploy/<env>/` dengan retensi 7 hari (dump gagal = deploy batal; upload R2 gagal = deploy lanjut + alarm Telegram), dan (2) `docker image prune` HANYA setelah `/readyz` hijau (biar image lama masih ada untuk rollback bila deploy gagal). `migrate.go` menolak auto-migrate bila schema DIRTY dan mencetak langkah pemulihan.

Deploy dev juga menjalankan recovery Q-2 yang idempotent setelah aplikasi baru sehat:
`quran-page-navigation-v1 -restart` lalu `citable-units-quran -restart`. Workflow baru dianggap
berhasil bila locator halaman 1 dan resolver B-2 sama-sama mengembalikan Citable Unit aktif.

### Preflight WAJIB sebelum deploy migration constraint baru

Migration yang membuat UNIQUE index (mis. `chronological_order` di `20260628000001`) memvalidasi baris existing saat dibuat; kalau ada duplikat, migration abort → boot gagal → schema DIRTY. Jalankan preflight ini di prod dulu (semua harus 0 baris):

```sh
docker compose --env-file .env.production -f docker-compose.prod.yml exec -T db \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
  "SELECT chronological_order, count(*) FROM quran_surahs WHERE chronological_order IS NOT NULL GROUP BY 1 HAVING count(*)>1;
   SELECT count(*) FROM quran_surahs WHERE slug = '';"
```

### Pemulihan schema DIRTY

Kalau boot gagal dengan pesan `schema is DIRTY at version N`:

```sh
# 1. Inspeksi & perbaiki data/schema penyebab migration gagal.
# 2. Restore snapshot pra-deploy bila perlu (artefak terenkripsi, lihat
#    docs/backup-restore-r2.md untuk detail kunci):
age -d -i /etc/surau-backup/age.key \
  /var/backups/surau/predeploy/surau-predeploy-<stamp>-<sha>.dump.zst.age | zstd -dc | \
  docker compose --env-file .env.production -f docker-compose.prod.yml exec -T db \
  pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --no-privileges
# 3. Set ulang versi ke migration terakhir yang sukses (JANGAN force sembarangan):
migrate -path migrations -database "$PG_URL" force <last-good-version>
# 4. Deploy ulang.
```
