# Evidence OneSignal erasure

Tanggal verifikasi: 2026-07-23
Branch: `feat/onesignal-identity`
Scope: provider erasure yang dipicu `POST /v1/auth/delete-account`

Dokumen ini sengaja tidak memuat raw UUID, JWT, ciphertext, App API Key, identity private key,
erasure secret, atau APNs key.

## Bukti otomatis

| Pemeriksaan | Hasil | Bukti tersanitasi |
| --- | --- | --- |
| Migration penuh ke PostgreSQL 18.4 | PASS | Seluruh migration sampai `20260723000001` berhasil. |
| Migration erasure `up → down → up` | PASS | Tabel, status constraints, lease constraint, due index, dan retention index kembali valid. |
| Transaksi delete account + outbox | PASS | Skenario commit menyimpan keduanya; App ID invalid me-rollback anonimisasi, session deletion, dan outbox. |
| Invalidasi session | PASS | Setelah commit, jumlah session aktif test account `0` dan `token_version` bertambah. |
| Claim dua worker | PASS | Dua claimant concurrent menghasilkan total tepat satu claim. |
| Reclaim setelah restart/lease expiry | PASS | Row dapat diambil kembali setelah lease melewati expiry. |
| Verifikasi `404` | PASS | State menjadi `verified`, ciphertext menjadi `NULL`, attempt evidence tetap tersanitasi. |
| Retensi | PASS | Cleanup verified menghapus parent dan attempt melalui cascade. |
| Provider contract simulation | PASS | DELETE `202/404`, GET `200/404`, `400`, `401`, `403`, `429 Retry-After`, `5xx`, timeout, dan redaksi response tercakup test. |
| Retry lintas instance/restart | PASS | State `verifying` dari pass pertama diteruskan instance baru sampai GET `404`. |
| Fail-fast konfigurasi | PASS | Identity ditolak saat erasure/App ID/App API Key/secret belum siap. |
| Alert contract | PASS | Stale 24 jam dan provider auth failure diarahkan ke default contact point Telegram. |
| `make integration-test` | PASS | Integration suite repo hijau. |
| `make pre-commit` | PASS | Module verify, formatter, lint, race/unit tests, dan normalization contract hijau. |

Live database drill dijalankan pada container PostgreSQL temporer lokal dan container dihapus setelah
test. Tidak ada data pengguna riil yang dipakai.

## Acceptance provider staging

Status: **PENDING — belum merupakan bukti production/staging provider**.

Workspace saat verifikasi tidak memiliki `ONESIGNAL_REST_API_KEY`, `ONESIGNAL_ERASURE_SECRET`,
`SURAU_LIVE_PG`, `.env`, `.env.production`, atau dua controlled test account. Karena itu panggilan
riil Delete User/View User tidak dijalankan dan tidak boleh ditandai lulus hanya dari mock server.

Saat secret dan test account tersedia, jalankan matriks pada
[runbook OneSignal erasure](onesignal-erasure.md#bukti-staging-tersanitasi) dan ganti bagian ini
dengan evidence:

```text
tested_at_utc: <timestamp>
environment: staging
onesignal_app_id: 7a650cae-1c1e-4b19-a7fe-393c14b894f0
audit_ref: <64-char HMAC>
delete_result: 202|404
view_sequence: 200,...,404
local_sessions_after_delete: 0
final_status: verified
ciphertext_present_after_verified: false
raw_uuid_or_jwt_present: false
```

UUID/JWT/key tidak boleh ditambahkan ketika melengkapi evidence tersebut.
