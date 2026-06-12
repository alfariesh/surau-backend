# Deploy VPS dengan Docker Compose

Dokumen ini untuk deployment sederhana di satu server VPS dengan domain `api.surau.org`: aplikasi Go berjalan sebagai container, PostgreSQL berjalan sebagai container, dan port aplikasi hanya dibuka ke `127.0.0.1:8080` agar bisa diletakkan di belakang Nginx/Caddy/Cloudflare Tunnel.

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
