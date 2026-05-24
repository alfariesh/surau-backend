# Deploy VPS dengan Docker Compose

Dokumen ini untuk deployment sederhana di satu server VPS: aplikasi Go berjalan sebagai container, PostgreSQL berjalan sebagai container, dan port aplikasi hanya dibuka ke `127.0.0.1:8080` agar bisa diletakkan di belakang Nginx/Caddy/Cloudflare Tunnel.

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
- Biarkan `APP_BIND_ADDR=127.0.0.1` jika reverse proxy ada di server yang sama.

Jika memakai database cloud, ganti `PG_URL` ke URL provider. Untuk database yang wajib SSL, pakai `?sslmode=require`.

## 3. Build dan jalankan

```sh
docker compose --env-file .env.production -f docker-compose.prod.yml up -d --build
```

Aplikasi otomatis menjalankan migration saat container `app` start karena Dockerfile membangun binary dengan tag `migrate`.

## 4. Cek health

```sh
docker compose --env-file .env.production -f docker-compose.prod.yml ps
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

`/healthz` mengecek proses HTTP. `/readyz` mengecek koneksi PostgreSQL.

## 5. Reverse proxy contoh

Contoh Nginx host config:

```nginx
server {
    listen 80;
    server_name api.example.com;

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

## 6. Update aplikasi

```sh
git pull
docker compose --env-file .env.production -f docker-compose.prod.yml up -d --build
docker compose --env-file .env.production -f docker-compose.prod.yml logs -f app
```

## Catatan data

Database disimpan di Docker volume `surau-backend_db_data`. Jangan jalankan `docker compose down -v` di production kecuali memang ingin menghapus data.
