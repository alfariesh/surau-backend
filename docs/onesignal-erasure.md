# OneSignal provider erasure untuk delete account

Status: diimplementasikan sebagai pekerjaan independen. Flow ini hanya dipicu oleh
`POST /v1/auth/delete-account`; tidak ada endpoint DSAR atau admin baru.

Referensi provider:

- [OneSignal Delete User](https://documentation.onesignal.com/reference/delete-user)
- [OneSignal View User](https://documentation.onesignal.com/reference/view-user)

## Kontrak privasi

Delete account lokal dan pembuatan pekerjaan provider berada dalam satu transaksi PostgreSQL.
Karena itu hanya ada dua hasil:

1. akun dianonimkan, seluruh session dihapus, `token_version` dinaikkan, dan outbox OneSignal
   tersimpan bersama-sama; atau
2. semua perubahan di-rollback dan endpoint memakai error `500` yang sudah ada.

Response publik tidak berubah:

```json
{"account_deleted":true}
```

Setelah commit, akun dan session lama langsung tidak dapat menerbitkan OneSignal identity JWT baru.
Penghapusan provider bersifat asynchronous dan tidak menahan response HTTP delete account.

## Data model dan enkripsi

Migration `20260723000001_onesignal_user_erasures` membuat dua tabel:

| Tabel | Fungsi |
| --- | --- |
| `onesignal_user_erasures` | State durable `pending` → `verifying` → `verified`, lease, jadwal retry, dan HMAC audit. |
| `onesignal_user_erasure_attempts` | Evidence append-only yang tersanitasi: waktu, operasi, outcome, HTTP status, dan reason code/detail terbatas. |

Exact `external_id` hanya disimpan sementara sebagai ciphertext AES-256-GCM. Kunci AES dan kunci
HMAC audit diturunkan terpisah dari `ONESIGNAL_ERASURE_SECRET` melalui HKDF-SHA256 dengan domain
yang berbeda. HMAC SHA-256 tidak dapat dibalik dan menjadi satu-satunya binding yang dipertahankan
setelah verifikasi. Worker membandingkan HMAC ciphertext yang baru didekripsi dengan binding row
secara constant-time sebelum memanggil provider; ciphertext yang tertukar/korup gagal tertutup.

Ciphertext langsung dibuat `NULL` ketika DELETE atau GET menghasilkan `404`. Attempt evidence tidak
memiliki kolom UUID. UUID, JWT, identity private key, erasure secret, dan App API Key dilarang masuk
log, metrik, screenshot, tiket, atau evidence.

## Lifecycle worker

Worker `onesignal_user_erasure` dijalankan supervisor background-loop F1-C. Setiap pass:

1. mengambil batch terbatas dengan `FOR UPDATE SKIP LOCKED`;
2. memasang lease lintas instance;
3. mendekripsi identifier hanya di memori;
4. memanggil provider;
5. mencatat attempt dan state berikutnya dalam satu transaksi;
6. membersihkan evidence `verified` yang melewati retensi.

Pekerjaan dengan lease kedaluwarsa dapat direbut oleh instance lain atau proses setelah restart.
Tidak ada status terminal gagal; pekerjaan hanya selesai setelah provider memberi bukti `404`.

| Provider result | State/tindakan |
| --- | --- |
| `DELETE 202` | `verifying`; tunggu lalu panggil View User. |
| `DELETE 404` | `verified`; hapus ciphertext. |
| `GET 200` | Tetap `verifying`; jadwalkan pemeriksaan berikutnya. |
| `GET 404` | `verified`; hapus ciphertext. |
| `400` | Catat `invalid_request`, tetap terlihat dan retry dengan backoff. |
| `401/403` | Catat `unauthorized`, gagalkan pass agar supervisor backoff dan alarm provider menyala. |
| `429` | Hormati `Retry-After` dalam batas retry; bila tidak ada, pakai backoff. |
| `5xx`/network/timeout | Retry exponential 30 detik sampai maksimum 15 menit. |

## Retensi dan status DSAR

- Data user/subscription OneSignal hanya boleh ada selama akun Surau aktif.
- Pekerjaan belum `verified` dipertahankan selama diperlukan untuk memperoleh bukti `404`.
- Ciphertext identifier dihapus segera setelah `404`.
- HMAC, status akhir, dan attempt evidence tersanitasi dipertahankan 90 hari setelah `verified`,
  lalu parent dan attempt dihapus bersama.
- Provider message/API data mengikuti default provider 30 hari; payload push tetap tidak boleh
  berisi data sensitif.
- DSAR terpisah tanpa delete account Surau berada di luar scope ini.

## Metrik dan alarm Telegram

Metrik:

- `surau_onesignal_erasure_queue{status="pending|verifying|verified"}`;
- `surau_onesignal_erasure_attempts_total{operation,outcome,reason_code}`;
- `surau_onesignal_erasure_stale`.

Grafana mengirim ke contact point Telegram:

- `surau-onesignal-erasure-stale` bila ada row yang belum `verified` setelah 24 jam;
- `surau-onesignal-erasure-provider-auth` bila provider menjawab `401/403`.

## Deployment

Secret wajib berasal dari secret manager/deployment secret store, bukan git:

```text
ONESIGNAL_APP_ID=7a650cae-1c1e-4b19-a7fe-393c14b894f0
ONESIGNAL_REST_API_KEY=<server-only App API Key>
ONESIGNAL_ERASURE_ENABLED=true
ONESIGNAL_ERASURE_SECRET=<dedicated random secret, minimum 32 bytes>
ONESIGNAL_ERASURE_INTERVAL=1m
ONESIGNAL_ERASURE_BATCH_SIZE=50
ONESIGNAL_ERASURE_LEASE_DURATION=2m
ONESIGNAL_ERASURE_VERIFICATION_DELAY=30s
ONESIGNAL_ERASURE_STALE_AFTER=24h
ONESIGNAL_ERASURE_RETENTION=2160h
```

`ONESIGNAL_IDENTITY_ENABLED=true` membuat aplikasi fail-fast kecuali erasure enabled, App ID, App
API Key, dan erasure secret semuanya valid. Setelah identity pernah diaktifkan di production,
`ONESIGNAL_ERASURE_ENABLED` wajib tetap `true` meskipun identity/personal push sedang dipause.

Identity Verification private key adalah key ES256 OneSignal, berbeda dari APNs `.p8`. File tersebut
dipasang read-only melalui `ONESIGNAL_SECRETS_DIR` dan tidak boleh dibaca ke tiket atau shell output.

## Runbook tanpa UUID mentah

Jangan menyalin `external_id_ciphertext` atau mencoba mendekripsinya manual. Untuk melihat antrean:

```sql
SELECT
    external_id_hash AS audit_ref,
    status,
    attempt_count,
    last_http_status,
    last_reason_code,
    accepted_at,
    verified_at,
    created_at,
    next_attempt_at
FROM onesignal_user_erasures
ORDER BY created_at DESC
LIMIT 50;
```

Untuk alarm stale:

```sql
SELECT
    external_id_hash AS audit_ref,
    status,
    attempt_count,
    last_http_status,
    last_reason_code,
    age(clock_timestamp(), created_at) AS age,
    next_attempt_at,
    lease_expires_at
FROM onesignal_user_erasures
WHERE status <> 'verified'
  AND created_at < clock_timestamp() - INTERVAL '24 hours'
ORDER BY created_at;
```

Evidence final untuk satu `audit_ref`:

```sql
SELECT
    e.external_id_hash AS audit_ref,
    e.status,
    e.accepted_at,
    e.verified_at,
    a.operation,
    a.outcome,
    a.http_status,
    a.reason_code,
    a.occurred_at
FROM onesignal_user_erasures e
JOIN onesignal_user_erasure_attempts a ON a.erasure_id = e.id
WHERE e.external_id_hash = '<64-char-audit-ref>'
ORDER BY a.occurred_at;
```

Status final yang sah adalah parent `verified`, attempt terakhir `not_found`, HTTP `404`,
`verified_at` terisi, dan `external_id_ciphertext IS NULL`. Jika `unauthorized`, rotasi/perbaiki App
API Key melalui secret manager lalu restart/redeploy; jangan menaruh key pada command history.

## Bukti staging tersanitasi

Gunakan test account yang disetujui dan rekam hanya:

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

Jangan isi template dengan UUID, JWT, App API Key, private key, atau ciphertext.

Hasil drill otomatis dan status gate provider tersimpan di
[Evidence OneSignal erasure](onesignal-erasure-evidence.md).
