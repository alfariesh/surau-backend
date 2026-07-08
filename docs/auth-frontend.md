# Frontend Auth Integration Guide

Dokumen ini adalah kontrak integrasi auth untuk frontend Surau. Satu-satunya transport yang didukung adalah REST API — pakai endpoint HTTP di bawah `/v1`. (Dukungan gRPC/NATS/AMQP dari template lama sudah dihapus.)

## Ringkasan

- Auth memakai JWT Bearer token.
- Login sukses selalu mengembalikan shape yang sama: `{ "token": "..." }`.
- Tidak ada cookie auth. **MFA (TOTP) tersedia sejak A-3**: akun ber-MFA mendapat langkah kode 6 digit setelah password (lihat `## Flow MFA`); akun tanpa MFA memakai alur login lama TANPA perubahan. Endpoint session/refresh yang lebih baru (`POST /v1/auth/refresh`, `POST /v1/auth/logout`, `GET/DELETE /v1/auth/sessions`) belum tercakup penuh di dokumen ini — cek `/swagger/index.html` untuk skemanya.
- Email verification, reset password, dan change email di-queue secara durable oleh backend, lalu dikirim asynchronous oleh dispatcher background memakai Cloudflare Email Service (default tick 15 detik; email biasanya tiba dalam ~15-30 detik).
- User baru belum bisa login sampai email verified.
- Reset password, change password, change email, dan delete account akan membuat semua JWT lama invalid.
- Profile response berisi user auth + `profile`, `preferences`, dan `onboarding_required`.
- Onboarding dan preferensi reader didokumentasikan lengkap di `docs/user-onboarding-api.md`.
- Backend juga mengirim email keamanan best-effort untuk password changed, email verified, email changed, account deleted, role changed, new login/device, dan suspicious failed login.
- Error REST memakai shape umum: `{ "error": "message", "code": "...", "request_id": "..." }`.

## Base URL

Gunakan environment frontend seperti:

```env
VITE_API_BASE_URL=http://localhost:8080
# atau
NEXT_PUBLIC_API_BASE_URL=http://localhost:8080
```

Semua path di dokumen ini memakai prefix `/v1`, contoh:

```text
POST {API_BASE_URL}/v1/auth/login
```

Header umum:

```http
Content-Type: application/json
Accept: application/json
```

Karena backend memakai Bearer token, frontend tidak perlu mengirim cookie credentials. Pada `fetch`, default `credentials: "same-origin"` aman untuk same-origin. Untuk cross-origin API, gunakan default atau `credentials: "omit"`. CORS backend berjalan dengan `AllowCredentials: false` — auth murni Bearer token, bukan cookie — jadi jangan memakai `credentials: "include"`.

Protected endpoint wajib memakai:

```http
Authorization: Bearer <token>
```

Backend menerima scheme `Bearer` secara case-insensitive, tetapi frontend tetap disarankan selalu mengirim format standar `Bearer <token>`.

## Model Data

### User

Response register memakai object user:

```json
{
  "id": 1,
  "username": "ahmad",
  "email": "ahmad@example.com",
  "role": "user",
  "email_verified": true,
  "created_at": "2026-05-27T10:00:00Z",
  "updated_at": "2026-05-27T10:00:00Z"
}
```

Catatan:

- `password_hash` tidak pernah dikirim ke frontend.
- `token_version` tidak dikirim ke frontend.
- Jangan jadikan decoded JWT sebagai source of truth untuk UI user. Ambil profile dari `/v1/user/profile`.
- `GET /v1/user/profile` mengembalikan `UserAccount`, yaitu field user di atas ditambah `profile`, `preferences`, dan `onboarding_required`.

### UserAccount

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "username": "ahmad",
  "email": "ahmad@example.com",
  "role": "user",
  "email_verified": true,
  "created_at": "2026-05-29T08:00:00Z",
  "updated_at": "2026-05-29T08:00:00Z",
  "profile": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "onboarding_version": 1,
    "personalization_enabled": true,
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "preferences": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "preferred_ui_lang": "id",
    "preferred_content_lang": "id",
    "fallback_langs": ["id"],
    "arabic_level": "none",
    "reader_mode": "arabic_translation",
    "interests": [],
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "onboarding_required": true
}
```

Frontend memakai `preferences.preferred_content_lang` sebagai default bahasa Quran/kitab setelah login.

### Token

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs..."
}
```

Token dipakai apa adanya di header `Authorization`.

### Error

```json
{
  "error": "invalid credentials",
  "code": "AUTH_INVALID_CREDENTIALS",
  "message": "invalid credentials",
  "request_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

Frontend sebaiknya branch berdasarkan HTTP status terlebih dahulu, lalu pakai `code` untuk logic yang perlu stabil. `error` dan `message` tetap bisa dipakai untuk copy/fallback, dan `request_id` sebaiknya disimpan saat laporan bug.

## Validasi Frontend

Backend tetap melakukan validasi final. Frontend sebaiknya melakukan validasi awal untuk UX.

| Field | Rule | Catatan |
| --- | --- | --- |
| `display_name` / `name` | trim, 3 sampai 255 karakter | Untuk register dan profile. FE baru sebaiknya kirim `name`; backend tetap menerima `username` legacy. |
| `username` | trim, 3 sampai 255 karakter | Legacy register field; bukan public handle unik lagi. |
| `email` | trim, format email valid | Kirim email yang sudah di-trim. |
| `new_email` | trim, format email valid | Untuk secure change email. |
| `password` | 8 sampai 72 bytes | Jangan trim password. Spasi adalah bagian password. |
| `current_password` | 8 sampai 72 bytes | Untuk change password. Jangan trim. |
| `new_password` | 8 sampai 72 bytes | Untuk change/reset password. Jangan trim. |
| `token` | string non-empty | Token verify/reset berasal dari query param FE. |

Penting: batas password adalah bytes, bukan jumlah karakter. Untuk JavaScript:

```ts
export function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function isValidPassword(value: string): boolean {
  const length = byteLength(value);
  return length >= 8 && length <= 72;
}
```

Jangan lakukan `.trim()` pada password sebelum dikirim.

## Endpoint Ringkas

| Method | Path | Auth | Success |
| --- | --- | --- | --- |
| `POST` | `/v1/auth/register` | Public | `201 User` |
| `POST` | `/v1/auth/login` | Public | `200 { "token": "..." }` |
| `POST` | `/v1/auth/verify-email` | Public | `200 { "email_verified": true }` |
| `POST` | `/v1/auth/resend-verification` | Public | `202 { "accepted": true }` |
| `POST` | `/v1/auth/forgot-password` | Public | `202 { "accepted": true }` |
| `POST` | `/v1/auth/reset-password` | Public | `200 { "password_reset": true }` |
| `POST` | `/v1/auth/change-password` | Bearer | `200 { "password_changed": true }` |
| `POST` | `/v1/auth/change-email/request` | Bearer | `202 { "accepted": true }` |
| `POST` | `/v1/auth/change-email/verify` | Bearer | `200 { "email_changed": true }` |
| `POST` | `/v1/auth/delete-account` | Bearer | `200 { "account_deleted": true }` |
| `GET` | `/v1/user/profile` | Bearer | `200 UserAccount` |
| `PATCH` | `/v1/user/profile` | Bearer | `200 UserAccount` |
| `PATCH` | `/v1/user/onboarding` | Bearer | `200 UserAccount` |
| `PATCH` | `/v1/user/preferences` | Bearer | `200 UserAccount` |
| `GET` | `/v1/admin/users?q=&role=&email_verified=&limit=&offset=` | Admin | `200 { users: UserAccount[], total: number }` |
| `GET` | `/v1/admin/users/{id}` | Admin | `200 UserAccount` |
| `GET` | `/v1/admin/users/{id}/activity` | Admin | `200 { activity: UserActivity[], total: number }` |
| `PATCH` | `/v1/admin/users/role` | Admin + step-up | `200 User` |
| `POST` | `/v1/auth/mfa/verify` | mfa_token | `200 Token` |
| `POST` | `/v1/auth/mfa/enroll` | Bearer | `200 { secret, otpauth_url }` |
| `POST` | `/v1/auth/mfa/enroll/confirm` | Bearer | `200 { recovery_codes: string[] }` |
| `POST` | `/v1/auth/mfa/step-up` | Bearer | `200 { stepped_up: true, expires_at }` |
| `POST` | `/v1/auth/mfa/disable` | Bearer + step-up | `200 Token` |
| `POST` | `/v1/auth/mfa/recovery-codes` | Bearer + step-up | `200 { recovery_codes: string[] }` |
| `GET` | `/v1/auth/mfa/status` | Bearer | `200 MFAStatus` |
| `POST` | `/v1/auth/mfa/reset/request` | mfa_token | `202 { reset_token, expires_in }` |
| `POST` | `/v1/auth/mfa/reset/confirm` | reset_token | `200 { mfa_reset: true }` |

## Admin User Management

Admin user management can use:

```http
GET /v1/admin/users?q=&role=&email_verified=&limit=&offset=
GET /v1/admin/users/{id}
GET /v1/admin/users/{id}/activity?limit=&offset=
PATCH /v1/admin/users/role
```

Use `GET /v1/admin/users?role=editor` as the editor lookup for assigning production project `owner_id`. Role changes are recorded in activity with `actor_id`, `actor_email`, `old_role`, `new_role`, `capability` (the capability that authorized the change), and `created_at`.

## Peran & kapabilitas (A-1)

Peran valid: `user`, `editor`, `curator`, `scholar_reviewer`, `admin` (`admin` = superset semua kapabilitas). `PATCH /v1/admin/users/role` menerima kelimanya (aditif — peran baru tak mengubah kontrak lama). Otorisasi backend memakai **kapabilitas**, bukan string peran; FE cukup tahu peran mana yang boleh melihat menu apa. Matriks beku saat ini:

| Kapabilitas | user | editor | curator | scholar_reviewer | admin |
| --- | :--: | :--: | :--: | :--: | :--: |
| `review-editorial` (dashboard editorial) | | ✓ | | | ✓ |
| `publish-production` (publish/unpublish, hapus aset final) | | | | | ✓ |
| `manage-users` (kelola akun, peran, email admin) | | | | | ✓ |
| `curate-entities` (kurasi entitas — belum ada rute) | | | ✓ | ✓ | ✓ |
| `approve-neutral-claim` (belum ada rute) | | | ✓ | ✓ | ✓ |
| `approve-sensitive-claim` (belum ada rute) | | | | ✓ | ✓ |
| `manage-service-tokens` (belum ada rute) | | | | | ✓ |

Rute ber-kapabilitas yang ditolak membalas `403 { "error": "forbidden", "code": "forbidden", ... }`. Empat kapabilitas terakhir sudah dibekukan di kebijakan tetapi belum menggerbangi rute apa pun (menyusul di fase wiki/token layanan). MFA wajib untuk `admin` + `scholar_reviewer` (lihat Flow MFA).

## Flow MFA (TOTP) — A-3

MFA WAJIB untuk peran `admin` & `scholar_reviewer` (A-1 mengaktifkan mandat scholar_reviewer); `editor`/`curator`/`user` opsional. Aturan penting untuk FE:

1. **Login akun ber-MFA** — `POST /v1/auth/login` sukses password mengembalikan `200` dengan body BARU (bukan token):
   ```json
   { "mfa_required": true, "mfa_token": "...", "expires_in": 300 }
   ```
   Tampilkan input kode 6 digit, lalu `POST /v1/auth/mfa/verify` `{ "mfa_token": "...", "code": "123456" }` → `200 Token` (shape sama persis dengan login biasa). Field `code` juga menerima recovery code (`XXXX-XXXX-XXXX-XXXX`). Akun TANPA MFA: response login tidak berubah sama sekali (tanpa field `mfa_required`).
2. **Enrollment** — `POST /v1/auth/mfa/enroll` → `{ secret, otpauth_url }`; render `otpauth_url` sebagai QR untuk di-scan aplikasi authenticator (Google Authenticator, Aegis, 1Password, dll.), tampilkan `secret` untuk entri manual. Konfirmasi dengan kode pertama: `POST /v1/auth/mfa/enroll/confirm` `{ "code": "123456" }` → `{ "recovery_codes": [10 kode] }` — **tampilkan SEKALI dan minta pengguna menyimpannya offline; tidak bisa dilihat lagi** (hanya bisa di-regenerate).
3. **Step-up (aksi destruktif)** — endpoint publish/unpublish produksi, hapus final asset, dan `PATCH /v1/admin/users/role` menuntut bukti kode yang SEGAR (default 10 menit). Bila kadaluarsa, response `403` dengan `code`:
   - `mfa_step_up_required` → tampilkan prompt kode → `POST /v1/auth/mfa/step-up` `{ "code": "..." }` → ulangi aksi.
   - `mfa_enrollment_required` → admin belum enroll dan masa tenggangnya (default 7 hari sejak diberi peran) habis → arahkan ke halaman enrollment. Cek `GET /v1/auth/mfa/status` (`grace_ends_at`) untuk menampilkan banner peringatan sebelum terkunci.
4. **Kehilangan HP** — dua jalur: (a) login pakai recovery code di langkah verify; (b) reset penuh: dari state `mfa_token`, `POST /v1/auth/mfa/reset/request` → OTP 6 digit dikirim ke email → `POST /v1/auth/mfa/reset/confirm` `{ reset_token, otp, recovery_code }` → MFA lepas + SEMUA sesi keluar → login password → enroll ulang. Kehabisan recovery code juga → hubungi admin (CLI darurat).
5. **Nonaktif/regenerate** — `POST /v1/auth/mfa/disable` dan `POST /v1/auth/mfa/recovery-codes` menuntut step-up segar (403 `mfa_step_up_required` bila tidak). Disable mengeluarkan SEMUA sesi dan mengembalikan pasangan token baru.

Error MFA (semua ber-envelope `{error, code, message, request_id}`): `invalid_mfa_code` 401, `invalid_mfa_challenge` 401 (kadaluarsa/terpakai → ulang login), `invalid_mfa_reset` 401, `mfa_already_enabled` 409, `mfa_not_enabled` 400, `mfa_enrollment_not_started` 400, `mfa_step_up_required` 403, `mfa_enrollment_required` 403. Percobaan kode dibatasi rate limit (429 + `retry_after`).

## Flow Register dan Verify Email

1. User submit register form.
2. FE panggil `POST /v1/auth/register`.
3. Backend membuat user dengan `email_verified=false`.
4. Backend meng-queue email verification; dispatcher background mengirimkannya via Cloudflare, biasanya dalam ~15-30 detik.
5. FE tampilkan screen "cek email" dan tombol resend, dengan copy bahwa email bisa butuh sampai ±30 detik untuk tiba.
6. Link email membuka halaman FE: `${EMAIL_VERIFY_FRONTEND_URL}?token=<token>`.
7. Halaman FE membaca query param `token`.
8. FE panggil `POST /v1/auth/verify-email`.
9. Setelah sukses, arahkan user ke login.

### Register

Request:

```http
POST /v1/auth/register
Content-Type: application/json
```

```json
{
  "name": "Ahmad",
  "email": "ahmad@example.com",
  "password": "correct horse battery"
}
```

`display_name` juga diterima. `username` masih diterima untuk client lama, tetapi FE baru sebaiknya tidak memakai `username`.

Success `201`:

```json
{
  "id": 1,
  "username": "ahmad",
  "email": "ahmad@example.com",
  "role": "user",
  "email_verified": false,
  "created_at": "2026-05-27T10:00:00Z",
  "updated_at": "2026-05-27T10:00:00Z"
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Tampilkan validasi field. |
| `409` | `user already exists` | Arahkan ke login atau forgot password. |
| `429` | `too many auth attempts` | Tampilkan cooldown. |
| `503` | `email delivery failed` | Backend gagal menyimpan email ke antrian. User bisa coba resend verification. |
| `500` | `internal server error` | Tampilkan pesan umum. |

Catatan `503`: email verification di-queue, bukan dikirim langsung di dalam request. `503 email delivery failed` sekarang hanya terjadi bila backend gagal menyimpan email ke antrian durable (kegagalan database) — provider email yang down/lambat tidak lagi menggagalkan register atau menambah latensi. Pada kasus `503`, user mungkin sudah tersimpan sebagai unverified, jadi FE sebaiknya tetap menyediakan form resend verification.

### Verify Email

Request:

```http
POST /v1/auth/verify-email
Content-Type: application/json
```

```json
{
  "token": "token-dari-query-param"
}
```

Atau OTP dari email:

```json
{
  "email": "ahmad@example.com",
  "otp": "123456"
}
```

Success `200`:

```json
{
  "email_verified": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Token kosong atau body salah. |
| `400` | `invalid verification token` | Tampilkan link/kode expired atau invalid dan beri opsi resend. |
| `429` | `too many auth attempts` | Tampilkan cooldown untuk percobaan OTP. |
| `500` | `internal server error` | Tampilkan pesan umum. |

Token dan OTP verification bersifat single-use untuk request yang sama. OTP 6 digit berlaku singkat, default `10m`; link mengikuti TTL token email. Setelah submit token dari link, hapus token dari URL:

```ts
window.history.replaceState({}, document.title, window.location.pathname);
```

Pada router modern, gunakan `router.replace("/verify-email")` atau route sukses sesuai framework.

### Resend Verification

Request:

```http
POST /v1/auth/resend-verification
Content-Type: application/json
```

```json
{
  "email": "ahmad@example.com"
}
```

Success `202`:

```json
{
  "accepted": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Email invalid. |
| `429` | `verification email recently sent` | Tampilkan cooldown resend. |
| `429` | `too many auth attempts` | Tampilkan cooldown umum. |
| `503` | `email delivery failed` | Backend gagal menyimpan email ke antrian (kegagalan database). Tampilkan pesan coba lagi. |
| `500` | `internal server error` | Tampilkan pesan umum. |

`202 accepted` berarti email sudah masuk antrian kirim; email biasanya tiba dalam ~15-30 detik. Untuk mengurangi account probing, backend juga mengembalikan `202 accepted` bila email tidak ditemukan atau sudah verified. FE jangan menampilkan pesan seperti "email tidak ditemukan" untuk flow ini.

## Flow Login

1. User submit email dan password.
2. FE panggil `POST /v1/auth/login`.
3. Jika sukses, simpan token dan ambil profile.
4. Jika `403 email not verified`, arahkan ke screen verifikasi email.
5. Jika `401 invalid credentials`, tampilkan email/password salah.

Request:

```http
POST /v1/auth/login
Content-Type: application/json
```

```json
{
  "email": "ahmad@example.com",
  "password": "correct horse battery"
}
```

Success `200`:

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs..."
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Tampilkan validasi field. |
| `401` | `invalid credentials` | Email atau password salah. |
| `403` | `email not verified` | Tampilkan screen verify/resend. |
| `429` | `too many auth attempts` | Tampilkan cooldown login. |
| `500` | `internal server error` | Tampilkan pesan umum. |

## Flow Profile dan Session Bootstrap

Saat aplikasi dibuka:

1. Ambil token dari storage.
2. Jika tidak ada token, user dianggap guest.
3. Jika ada token, panggil `GET /v1/user/profile`.
4. Jika `200`, simpan user di state.
5. Jika `onboarding_required=true`, tampilkan onboarding.
6. Gunakan `preferences.preferred_content_lang` sebagai default `?lang=` Quran/kitab.
7. Jika `401`, hapus token dan arahkan ke login.

Request:

```http
GET /v1/user/profile
Authorization: Bearer <token>
```

Success `200`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "username": "ahmad",
  "email": "ahmad@example.com",
  "role": "user",
  "email_verified": true,
  "created_at": "2026-05-29T08:00:00Z",
  "updated_at": "2026-05-29T08:00:00Z",
  "profile": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "onboarding_version": 1,
    "personalization_enabled": true,
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "preferences": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "preferred_ui_lang": "id",
    "preferred_content_lang": "id",
    "fallback_langs": ["id"],
    "arabic_level": "none",
    "reader_mode": "arabic_translation",
    "interests": [],
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "onboarding_required": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `401` | `missing authorization header` | Clear token, redirect login. |
| `401` | `invalid authorization header format` | Clear token, redirect login. |
| `401` | `invalid or expired token` | Clear token, redirect login. |
| `401` | `unauthorized` | Clear token, redirect login. |
| `404` | `user not found` | Clear token, redirect login. |
| `500` | `internal server error` | Tampilkan pesan umum. |

## Flow Onboarding dan Preferences

Onboarding adalah langkah setelah login pertama, bukan bagian dari register.
Frontend boleh melewati onboarding screen hanya jika `onboarding_required=false`.

Request:

```http
PATCH /v1/user/onboarding
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "preferred_content_lang": "id",
  "preferred_ui_lang": "id",
  "arabic_level": "basic",
  "reader_mode": "arabic_translation",
  "interests": ["quran_daily", "tafsir", "hadith"],
  "daily_goal_minutes": 15
}
```

Success `200` mengembalikan `UserAccount` dengan `onboarding_required=false`.

Settings screen memakai:

```http
PATCH /v1/user/preferences
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "preferred_content_lang": "en",
  "reader_mode": "translation_only"
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `unsupported language` | Reset pilihan bahasa ke nilai valid sebelumnya atau `id`. |
| `400` | `invalid user preference` | Tampilkan error pada field preference. |
| `401` | `invalid or expired token` | Clear token, redirect login. |

Detail field, enum, dan QA checklist ada di `docs/user-onboarding-api.md`.

## Flow Update Profile

Endpoint ini untuk perubahan data non-auth-sensitive seperti nama tampilan, timezone, negara, dan pilihan personalization.

```http
PATCH /v1/user/profile
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "display_name": "Ahmad",
  "timezone": "Asia/Jakarta",
  "country_code": "ID",
  "personalization_enabled": true
}
```

Field yang tidak dikirim tidak diubah. Kirim `""` untuk clear `timezone` atau `country_code`. Jangan kirim `display_name=""`; backend akan mengembalikan `400`.

Success `200` mengembalikan `UserAccount`.

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid user preference` | Tampilkan error pada field profile. |
| `401` | `invalid or expired token` | Clear token, redirect login. |
| `404` | `user not found` | Clear token, redirect login. |

## Flow Forgot dan Reset Password

### Forgot Password

1. User submit email di halaman forgot password.
2. FE panggil `POST /v1/auth/forgot-password`.
3. Backend membuat token reset dan meng-queue email reset; dispatcher background mengirimkannya, biasanya dalam ~15-30 detik.
4. FE selalu tampilkan pesan netral: "Jika email terdaftar, link reset akan dikirim."
5. Link email membuka halaman FE: `${PASSWORD_RESET_FRONTEND_URL}?token=<token>`.

Request:

```http
POST /v1/auth/forgot-password
Content-Type: application/json
```

```json
{
  "email": "ahmad@example.com"
}
```

Success `202`:

```json
{
  "accepted": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Email invalid. |
| `429` | `password reset email recently sent` | Tampilkan cooldown. |
| `429` | `too many auth attempts` | Tampilkan cooldown umum. |
| `503` | `email delivery failed` | Backend gagal menyimpan email ke antrian (kegagalan database). Tampilkan pesan coba lagi. |
| `500` | `internal server error` | Tampilkan pesan umum. |

Backend mengembalikan `202 accepted` untuk email yang tidak ditemukan supaya tidak ada account probing. FE jangan membedakan email terdaftar dan tidak terdaftar.

### Reset Password

1. Halaman reset membaca query param `token`.
2. User input password baru.
3. FE panggil `POST /v1/auth/reset-password`.
4. Jika sukses, hapus token auth lokal jika ada.
5. Arahkan user ke login.

Request:

```http
POST /v1/auth/reset-password
Content-Type: application/json
```

```json
{
  "token": "token-dari-query-param",
  "password": "new correct horse battery"
}
```

Success `200`:

```json
{
  "password_reset": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Token/password invalid, expired, used, atau body salah. |
| `429` | `too many auth attempts` | Tampilkan cooldown. |
| `500` | `internal server error` | Tampilkan pesan umum. |

Reset password juga menandai `email_verified=true`. Ini berarti user yang belum verified tetap boleh reset password, dan setelah reset sukses email dianggap verified.

Reset password akan increment `token_version`, sehingga JWT lama milik user tersebut otomatis invalid. FE harus clear token lokal setelah reset sukses.

## Flow Change Password

Endpoint ini untuk user yang masih login.

1. User buka settings/security.
2. User input current password dan new password.
3. FE panggil `POST /v1/auth/change-password` dengan Bearer token.
4. Jika sukses, backend membuat semua JWT lama invalid, termasuk token yang sedang dipakai.
5. FE harus clear token dan arahkan user ke login.

Request:

```http
POST /v1/auth/change-password
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "current_password": "old password",
  "new_password": "new correct horse battery"
}
```

Success `200`:

```json
{
  "password_changed": true
}
```

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Current/new password invalid. |
| `401` | `invalid credentials` | Current password salah. |
| `401` | `invalid or expired token` | Clear token, redirect login. |
| `401` | `unauthorized` | Clear token, redirect login. |
| `429` | `too many auth attempts` | Tampilkan cooldown. |
| `500` | `internal server error` | Tampilkan pesan umum. |

## Flow Change Email

Change email adalah flow protected dua langkah. User harus login dan memasukkan current password. Link verifikasi di-queue dan dikirim ke email baru (biasanya tiba dalam ~15-30 detik), lalu FE memanggil endpoint verify dengan Bearer token user yang sama. Jika user membuka link tanpa session valid, arahkan ke login lalu lanjutkan verify setelah login.

### Request Change Email

```http
POST /v1/auth/change-email/request
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "current_password": "current password",
  "new_email": "new@example.com"
}
```

Success `202`:

```json
{
  "accepted": true
}
```

Link email membuka halaman FE: `${EMAIL_CHANGE_FRONTEND_URL}?token=<token>`.

### Verify Change Email

```http
POST /v1/auth/change-email/verify
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "token": "token-dari-query-param"
}
```

Atau OTP dari email baru:

```json
{
  "otp": "123456"
}
```

Success `200`:

```json
{
  "email_changed": true
}
```

Setelah verify sukses, backend increment `token_version`. FE harus clear token lokal dan arahkan user ke login ulang.

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Password/email/token/OTP invalid, expired, used, atau bukan milik user login. |
| `401` | `invalid credentials` | Current password salah. |
| `401` | `invalid or expired token` | Clear token, redirect login. |
| `409` | `user already exists` | Email baru sudah dipakai akun lain. |
| `429` | `too many auth attempts` | Tampilkan cooldown. |
| `503` | `email delivery failed` | Backend gagal menyimpan email ke antrian (kegagalan database). Tampilkan pesan coba lagi. |

## Flow Delete Account

Delete account adalah soft delete yang tidak bisa dipulihkan lewat public API. Backend menganonimkan akun, menghapus data personal user, dan membuat semua JWT lama invalid.

```http
POST /v1/auth/delete-account
Authorization: Bearer <token>
Content-Type: application/json
```

```json
{
  "current_password": "current password"
}
```

Success `200`:

```json
{
  "account_deleted": true
}
```

FE harus clear token lokal, hapus state user, lalu arahkan ke goodbye/login screen.

Error penting:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `invalid request body` | Current password invalid format. |
| `401` | `invalid credentials` | Current password salah. |
| `401` | `invalid or expired token` | Clear token, redirect login. |
| `429` | `too many auth attempts` | Tampilkan cooldown. |
| `500` | `internal server error` | Tampilkan pesan umum. |

## Token Revocation Behavior

JWT membawa claim `token_version`. Backend membandingkan versi token dengan `users.token_version` di database pada setiap protected request.

Token menjadi invalid ketika:

- Expired.
- Signature/issuer/audience invalid.
- User tidak ditemukan.
- User `token_version` sudah berubah setelah reset password, change password, change email, atau delete account.

Frontend behavior:

- Pada protected request yang mendapat `401`, clear token lokal.
- Redirect ke login atau tampilkan session expired.
- Jangan retry otomatis dengan token yang sama.
- Dokumen ini tidak mencakup flow refresh token; tanpa refresh, user harus login ulang untuk memperoleh token baru (lihat `/swagger/index.html` untuk `POST /v1/auth/refresh`).

## Rate Limit Behavior

Backend memakai DB-backed rate limit supaya berlaku lintas instance dan lintas transport.

Default yang perlu diketahui FE:

| Action | Limit |
| --- | --- |
| Login | `5/email/5m`, `30/ip/5m` |
| Register | `3/email/1h`, `10/ip/1h` |
| Forgot password | `3/email/1h`, `20/ip/1h` |
| Resend verification | `3/email/1h`, `20/ip/1h` |
| Reset password | `5/token/15m`, `30/ip/15m` |
| Change password | `5/user/5m`, `30/ip/5m` |
| Change email request | `3/user/1h`, `10/ip/1h` |
| Change email verify | `5/token/15m`, `10/ip/1h` |
| Delete account | `3/user/1h`, `10/ip/1h` |

Jika kena limit, REST mengembalikan `429`. Response `429` menyertakan header `Retry-After` (detik) dan field `retry_after` di body bila limiter bisa menghitung sisa jendela; header ini di-expose ke browser lewat CORS, jadi FE bisa memakainya untuk countdown. Fallback copy umum:

- "Terlalu banyak percobaan. Coba lagi beberapa saat."
- Untuk resend/forgot, disable tombol selama minimal 60 detik setelah request sukses atau rate-limited.

Endpoint session management (`GET /v1/auth/sessions` dan `DELETE /v1/auth/sessions/{id}`) dibatasi terpisah: 30 request/menit per user, dengan `429` + header `Retry-After` saat melebihi batas. Sejak F1-D body 429 ini memakai envelope error standar (`{"error":"too many requests","code":"too_many_requests","retry_after":N,"request_id":"..."}` — sebelumnya teks polos). List sessions memakai envelope `{ "items": [...], "total": number }`.

## Recommended Frontend Routes

| Route FE | Fungsi |
| --- | --- |
| `/register` | Register form. |
| `/login` | Login form. |
| `/verify-email?token=...` | Consume token verification dari email. |
| `/forgot-password` | Request reset password email. |
| `/reset-password?token=...` | Consume token reset dari email. |
| `/change-email?token=...` | Consume token change email dari email baru. |
| `/onboarding` | First-run profile/preference setup setelah login. |
| `/settings/profile` | Update display name/timezone/country. |
| `/settings/security` | Change password, change email, dan delete account untuk user login. |

Backend environment harus menunjuk ke route FE yang benar:

```env
EMAIL_VERIFY_FRONTEND_URL=https://app.example.com/verify-email
PASSWORD_RESET_FRONTEND_URL=https://app.example.com/reset-password
EMAIL_CHANGE_FRONTEND_URL=https://app.example.com/change-email
AUTH_EMAIL_NOTIFICATIONS_ENABLED=true
AUTH_NEW_LOGIN_EMAIL_ENABLED=true
AUTH_FAILED_LOGIN_EMAIL_ENABLED=true
AUTH_PASSWORD_CHANGED_EMAIL_ENABLED=true
AUTH_EMAIL_VERIFIED_EMAIL_ENABLED=true
AUTH_ROLE_CHANGED_EMAIL_ENABLED=true
AUTH_EMAIL_CHANGED_EMAIL_ENABLED=true
AUTH_ACCOUNT_DELETED_EMAIL_ENABLED=true
```

Email keamanan tidak mengubah response API. Jika email notifikasi gagal dikirim, flow utama tetap sukses selama operasi utama sukses.

Checklist staging/production sebelum buka auth publik:

- `EMAIL_VERIFY_FRONTEND_URL` mengarah ke route verify email frontend production.
- `PASSWORD_RESET_FRONTEND_URL` mengarah ke route reset password frontend production.
- `EMAIL_CHANGE_FRONTEND_URL` mengarah ke route change email frontend production.
- Origin frontend production terdaftar di env backend `CORS_ALLOWED_ORIGINS` — CORS sekarang dilayani backend sendiri. Default dev mengizinkan `http://localhost:3000` dan `http://localhost:3005`; origin web lain harus ditambahkan ke env tersebut.
- Request header yang diizinkan CORS backend: `Authorization`, `Content-Type`, `X-Request-ID`. Response header yang di-expose: `ETag`, `Retry-After`, `X-Request-ID`.
- `AllowCredentials` selalu `false`; backend juga mengirim security headers standar (helmet) — tidak ada aksi FE yang dibutuhkan.

## Recommended Auth State

Minimal state frontend:

```ts
type AuthStatus = "loading" | "guest" | "authenticated";

type AuthState = {
  status: AuthStatus;
  token: string | null;
  user: UserAccount | null;
};
```

Route guard:

| Kondisi | Behavior |
| --- | --- |
| `status=loading` | Tampilkan loading ringan. |
| Protected page tanpa token | Redirect login. |
| Protected page token invalid | Clear token, redirect login. |
| Login/register saat sudah authenticated | Redirect ke app utama. |
| Login mendapat `403 email not verified` | Tampilkan screen resend verification. |

## TypeScript Example

Contoh ini framework-agnostic. Adaptasikan storage/router sesuai React, Next.js, Vue, Svelte, atau mobile client.

```ts
const API_BASE_URL =
  import.meta.env.VITE_API_BASE_URL ?? "http://localhost:8080";

export type User = {
  id: string;
  username: string;
  email: string;
  role: string;
  email_verified: boolean;
  created_at: string;
  updated_at: string;
};

export type ContentLang = "ar" | "id" | "en";
export type ArabicLevel = "none" | "basic" | "intermediate" | "advanced" | "native";
export type ReaderMode = "arabic_translation" | "translation_only" | "arabic_only";

export type UserProfile = {
  user_id: string;
  display_name?: string | null;
  timezone?: string | null;
  country_code?: string | null;
  onboarding_version: number;
  onboarding_completed_at?: string | null;
  personalization_enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type UserPreferences = {
  user_id: string;
  preferred_ui_lang: ContentLang;
  preferred_content_lang: ContentLang;
  fallback_langs: ContentLang[];
  arabic_level: ArabicLevel;
  reader_mode: ReaderMode;
  interests: string[];
  daily_goal_minutes?: number | null;
  quran_translation_source_id?: string | null;
  quran_recitation_id?: string | null;
  created_at: string;
  updated_at: string;
};

export type UserAccount = User & {
  profile: UserProfile;
  preferences: UserPreferences;
  onboarding_required: boolean;
};

export type LoginResponse = {
  token: string;
};

export type ApiErrorBody = {
  error?: string;
};

export class ApiError extends Error {
  status: number;
  body: ApiErrorBody;

  constructor(status: number, body: ApiErrorBody) {
    super(body.error ?? `HTTP ${status}`);
    this.status = status;
    this.body = body;
  }
}

function getToken(): string | null {
  return window.localStorage.getItem("surau_token");
}

function setToken(token: string): void {
  window.localStorage.setItem("surau_token", token);
}

function clearToken(): void {
  window.localStorage.removeItem("surau_token");
}

async function apiFetch<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    auth?: boolean;
    signal?: AbortSignal;
  } = {},
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
  };

  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }

  if (options.auth) {
    const token = getToken();
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: options.method ?? "GET",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  });

  const text = await response.text();
  const data = text ? JSON.parse(text) : {};

  if (!response.ok) {
    if (response.status === 401 && options.auth) {
      clearToken();
    }
    throw new ApiError(response.status, data);
  }

  return data as T;
}

export async function register(input: {
  username: string;
  email: string;
  password: string;
}): Promise<User> {
  return apiFetch<User>("/v1/auth/register", {
    method: "POST",
    body: {
      username: input.username.trim(),
      email: input.email.trim(),
      password: input.password,
    },
  });
}

export async function login(input: {
  email: string;
  password: string;
}): Promise<UserAccount> {
  const result = await apiFetch<LoginResponse>("/v1/auth/login", {
    method: "POST",
    body: {
      email: input.email.trim(),
      password: input.password,
    },
  });

  setToken(result.token);
  return getProfile();
}

export async function getProfile(): Promise<UserAccount> {
  return apiFetch<UserAccount>("/v1/user/profile", {
    auth: true,
  });
}

export async function completeOnboarding(input: {
  display_name?: string;
  timezone?: string;
  country_code?: string;
  personalization_enabled?: boolean;
  preferred_ui_lang?: ContentLang;
  preferred_content_lang?: ContentLang;
  fallback_langs?: ContentLang[];
  arabic_level?: ArabicLevel;
  reader_mode?: ReaderMode;
  interests?: string[];
  daily_goal_minutes?: number;
  quran_translation_source_id?: string;
  quran_recitation_id?: string;
}): Promise<UserAccount> {
  return apiFetch<UserAccount>("/v1/user/onboarding", {
    method: "PATCH",
    auth: true,
    body: input,
  });
}

export async function updatePreferences(input: Partial<UserPreferences>): Promise<UserAccount> {
  return apiFetch<UserAccount>("/v1/user/preferences", {
    method: "PATCH",
    auth: true,
    body: input,
  });
}

export async function verifyEmail(token: string): Promise<{ email_verified: true }> {
  return apiFetch<{ email_verified: true }>("/v1/auth/verify-email", {
    method: "POST",
    body: { token },
  });
}

export async function resendVerification(email: string): Promise<{ accepted: true }> {
  return apiFetch<{ accepted: true }>("/v1/auth/resend-verification", {
    method: "POST",
    body: { email: email.trim() },
  });
}

export async function forgotPassword(email: string): Promise<{ accepted: true }> {
  return apiFetch<{ accepted: true }>("/v1/auth/forgot-password", {
    method: "POST",
    body: { email: email.trim() },
  });
}

export async function resetPassword(input: {
  token: string;
  password: string;
}): Promise<{ password_reset: true }> {
  const result = await apiFetch<{ password_reset: true }>("/v1/auth/reset-password", {
    method: "POST",
    body: {
      token: input.token,
      password: input.password,
    },
  });

  clearToken();
  return result;
}

export async function changePassword(input: {
  currentPassword: string;
  newPassword: string;
}): Promise<{ password_changed: true }> {
  const result = await apiFetch<{ password_changed: true }>(
    "/v1/auth/change-password",
    {
      method: "POST",
      auth: true,
      body: {
        current_password: input.currentPassword,
        new_password: input.newPassword,
      },
    },
  );

  clearToken();
  return result;
}
```

Catatan storage:

- `localStorage` mudah dipakai tetapi rentan jika ada XSS.
- In-memory storage lebih aman terhadap persistence, tetapi user akan logout saat reload.
- Backend saat ini hanya mendukung Bearer token, bukan HttpOnly cookie.
- Apa pun storage yang dipilih, prioritaskan proteksi XSS di frontend.

## Error Handling Copy

Mapping copy yang disarankan:

| Status/Code | Legacy error | Copy UI |
| --- | --- | --- |
| `400 invalid_request_body` | `invalid request body` | "Periksa kembali data yang kamu isi." |
| `401 AUTH_INVALID_CREDENTIALS` | `invalid credentials` | "Email atau password salah." |
| `401 AUTH_TOKEN_INVALID` | `invalid or expired token` | "Sesi kamu sudah berakhir. Silakan login lagi." |
| `403 AUTH_EMAIL_NOT_VERIFIED` | `email not verified` | "Email belum diverifikasi. Cek inbox atau kirim ulang email verifikasi." |
| `409 user_already_exists` | `user already exists` | "Email ini sudah terdaftar. Silakan login atau reset password." |
| `429 AUTH_RATE_LIMITED` | `too many auth attempts` | "Terlalu banyak percobaan. Coba lagi beberapa saat." |
| `429 verification_email_recently_sent` | `verification email recently sent` | "Email verifikasi baru saja dikirim. Tunggu sebentar sebelum mengirim ulang." |
| `429 password_reset_email_recently_sent` | `password reset email recently sent` | "Email reset password baru saja dikirim. Tunggu sebentar sebelum mengirim ulang." |
| `503 email_delivery_failed` | `email delivery failed` | "Email belum bisa dikirim. Coba lagi nanti." |
| `500 internal_server_error` | `internal server error` | "Terjadi gangguan. Coba lagi nanti." |

## Security Checklist FE

- Selalu kirim `Authorization: Bearer <token>` untuk protected endpoint.
- Clear token pada semua response `401` dari protected endpoint.
- Clear token setelah reset password sukses.
- Clear token setelah change password sukses.
- Jangan trim password.
- Trim username dan email.
- Jangan log password, JWT, verification token, atau reset token di console/analytics.
- Jangan menampilkan token verify/reset di UI.
- Hapus token verify/reset dari URL setelah diproses.
- Jangan ungkap apakah email terdaftar pada forgot password dan resend verification.
- Jangan mengandalkan decoded JWT untuk role/profile final.
- Ambil profile dari backend setelah login dan saat app bootstrap.
- Tampilkan state loading saat bootstrap agar halaman protected tidak flicker.
- Pakai HTTPS di production.
- Jika browser memblokir request karena CORS, tambahkan origin frontend ke env backend `CORS_ALLOWED_ORIGINS` (default dev: `http://localhost:3000`, `http://localhost:3005`). Header `Authorization`, `Content-Type`, dan `X-Request-ID` sudah diizinkan backend.

## QA Checklist FE

### Register

- Register valid menampilkan screen cek email.
- Register password 7 bytes ditolak FE.
- Register password lebih dari 72 bytes ditolak FE.
- Password dengan spasi awal/akhir tidak di-trim.
- Duplicate email menampilkan copy login/forgot password.
- Jika register mendapat `503 email delivery failed`, user tetap bisa memakai resend verification.

### Email Verification

- `/verify-email?token=valid` memanggil backend dan sukses.
- Token invalid/expired menampilkan opsi resend.
- Token dipakai dua kali menampilkan invalid/expired.
- Token hilang dari URL setelah submit.

### Login

- Login user verified sukses, token tersimpan, profile ter-load.
- Login password salah menampilkan `invalid credentials`.
- Login user belum verified menampilkan screen verify/resend.
- Login kena `429` menampilkan cooldown.

### Forgot/Reset Password

- Forgot password selalu menampilkan pesan netral untuk email terdaftar atau tidak.
- Reset password valid sukses dan redirect login.
- Reset password clear token lama jika user sebelumnya login.
- Reset token invalid/expired/used menampilkan pesan token invalid.
- Setelah reset, login dengan password lama gagal dan password baru sukses.

### Change Password

- User login bisa change password dengan current password benar.
- Current password salah menampilkan error.
- Setelah sukses, token lama invalid dan user diarahkan login.
- Login ulang dengan password baru sukses.

### Session

- App bootstrap dengan token valid berhasil load profile.
- App bootstrap dengan token expired/revoked clear token dan redirect login.
- Protected API mendapat `401` clear token.

## Swagger

Jika backend dijalankan dengan `SWAGGER_ENABLED=true`, dokumentasi OpenAPI tersedia di `/swagger/index.html`. Gunakan Swagger untuk mengecek field terbaru, tetapi dokumen ini tetap menjadi panduan flow frontend auth.
