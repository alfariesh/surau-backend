# Local Email Testing

Panduan ini untuk mengetes email verifikasi dan reset password dari backend lokal.

## Inti Masalah

Jika log backend menampilkan baris seperti ini:

```text
DEV_EMAIL to="user@example.com" subject="Reset your Surau password" link="http://localhost:3005/reset-password?token=..."
```

berarti backend sedang berada di mode log/local. Email tidak dikirim ke inbox. Link hanya dicetak ke log agar flow auth bisa dites tanpa provider email.
Body plaintext penuh tidak dicetak ke log.

Untuk mengetes email sungguhan, backend harus memakai Cloudflare Email Service.

## Env Untuk Email Real

Isi file `.env` backend:

```env
EMAIL_DELIVERY_MODE=cloudflare
CF_EMAIL_ACCOUNT_ID=isi_account_id_cloudflare
CF_EMAIL_API_TOKEN=isi_api_token_cloudflare
EMAIL_FROM_ADDRESS=noreply@mail.surau.org
EMAIL_FROM_NAME=Surau
EMAIL_REPLY_TO=
EMAIL_VERIFY_FRONTEND_URL=http://localhost:3005/verify-email
PASSWORD_RESET_FRONTEND_URL=http://localhost:3005/reset-password
EMAIL_CHANGE_FRONTEND_URL=http://localhost:3005/change-email
EMAIL_UNSUBSCRIBE_FRONTEND_URL=http://localhost:3005/unsubscribe
EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID=default
EMAIL_UNSUBSCRIBE_TOKEN_SECRET=
EMAIL_UNSUBSCRIBE_TOKEN_SECRETS=
EMAIL_CLOUDFLARE_WEBHOOK_SECRET=
EMAIL_HTTP_TIMEOUT=10s
```

Catatan:

- `EMAIL_DELIVERY_MODE=cloudflare` wajib jika branch lokal punya mode log.
- Jika backend tidak punya `EMAIL_DELIVERY_MODE`, abaikan variable itu; backend akan langsung pakai Cloudflare.
- `EMAIL_FROM_ADDRESS` harus memakai domain yang sudah aktif di Cloudflare Email Service.
- `EMAIL_VERIFY_FRONTEND_URL`, `PASSWORD_RESET_FRONTEND_URL`, dan `EMAIL_CHANGE_FRONTEND_URL` harus mengarah ke port frontend yang sedang dipakai.
- `EMAIL_UNSUBSCRIBE_FRONTEND_URL` dipakai untuk link unsubscribe campaign marketing. Jika kosong, backend menurunkan URL dari `EMAIL_VERIFY_FRONTEND_URL` dengan path `/unsubscribe`.
- `EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID` masuk ke token unsubscribe baru. Default `default`.
- `EMAIL_UNSUBSCRIBE_TOKEN_SECRET` opsional. Jika kosong, backend memakai secret dari `EMAIL_UNSUBSCRIBE_TOKEN_SECRETS` untuk key id aktif, atau `JWT_SECRET` sebagai fallback.
- `EMAIL_UNSUBSCRIBE_TOKEN_SECRETS` opsional untuk rotation, format JSON object seperti `{"2026-06":"secret-baru","2026-05":"secret-lama"}`.
- `EMAIL_CLOUDFLARE_WEBHOOK_SECRET` mengaktifkan `POST /v1/email/webhooks/cloudflare/bounces`; kosong berarti endpoint webhook disabled.

## Restart Backend

Setelah `.env` berubah:

```sh
cd /Users/macmini/Downloads/surau-backend
docker compose up -d app
docker compose logs -f app
```

Tidak perlu rebuild image kalau hanya mengubah env.

## Test Reset Password

Flow reset password paling mudah untuk mengetes email real.

```sh
curl -i -X POST http://127.0.0.1:8080/v1/auth/forgot-password \
  -H 'Content-Type: application/json' \
  -d '{"email":"alfarieshsalman@gmail.com"}'
```

Expected:

- Response `202 Accepted`.
- Log backend tidak menampilkan `DEV_EMAIL`.
- Tidak ada `email delivery failed`.
- Email masuk ke inbox atau spam.

## Test Email Verification

Register akun baru dari frontend, atau kirim ulang verifikasi:

```sh
curl -i -X POST http://127.0.0.1:8080/v1/auth/resend-verification \
  -H 'Content-Type: application/json' \
  -d '{"email":"alfarieshsalman@gmail.com"}'
```

Expected sama:

- Response `202 Accepted`.
- Tidak ada `DEV_EMAIL`.
- Email verifikasi masuk ke inbox atau spam.

## Test Notifikasi Keamanan

Dengan `AUTH_EMAIL_NOTIFICATIONS_ENABLED=true`, backend juga mengirim email keamanan best-effort untuk:

- password berhasil diubah lewat reset password atau change password;
- email berhasil diverifikasi;
- email login berhasil diubah;
- akun berhasil dihapus;
- role user berubah;
- login dari kombinasi IP dan user agent baru;
- percobaan login dibatasi karena rate limit email.

Notifikasi ini tidak membawa password, JWT, verification token, reset token, atau email-change token. Jika pengiriman notifikasi gagal, operasi utama tetap sukses dan error hanya muncul di log.

## Jika Kena Rate Limit

Untuk local dev saja, reset rate limit:

```sh
docker compose exec -T db psql -U user -d db -c "truncate table auth_rate_limits;"
```

Lalu coba request lagi.

## Jika Email Gagal

Cek log:

```sh
docker compose logs --tail=120 app
```

Makna umum:

- `DEV_EMAIL`: masih mode log, belum Cloudflare.
- `CloudflareEmailClient status 401/403`: token salah, expired, atau permission kurang.
- `CloudflareEmailClient status 404`: account id salah, endpoint tidak cocok, atau Email Service belum aktif untuk akun itu.
- `permanent bounce`: alamat tujuan ditolak provider.
- `recipient was not delivered or queued`: Cloudflare tidak memasukkan email ke delivered/queued.

## Checklist Cloudflare

Sebelum test email real, pastikan:

- Cloudflare Account ID benar.
- API token punya akses Email Service Sending.
- Domain `mail.surau.org` atau domain pengirim sudah onboard di Cloudflare Email Service.
- DNS/SPF/DKIM/DMARC/bounce records sudah valid.
- `EMAIL_FROM_ADDRESS` sesuai domain yang sudah diotorisasi.
