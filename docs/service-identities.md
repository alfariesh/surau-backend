# Identitas layanan A-2

Dokumen ini adalah kontrak untuk mesin yang memanggil Surau. Satu principal
mewakili satu layanan bernama; token hanyalah kredensial yang dapat diganti.
Nama principal tidak dapat diubah dan pencabutan principal bersifat permanen.

## Registry scope

| Principal | Scope | Permukaan yang diidentifikasi |
|---|---|---|
| `collab-server` | `collab:draft:write` | seluruh `/internal/collab/*`, termasuk `whoami` |
| `rag-eval` | `rag-eval:read` | Book RAG dan resolver Anchor publik saat header hadir |
| `http-enrichment` | `enrichment:read` | reader/catalog publik saat header hadir |
| `u0-inference` | `prompt-registry:manage`, `inference-budget:manage` | kontrak U-0; belum ada principal aktif atau secret |

Daftar ini beku di kode dan `CHECK` PostgreSQL. Scope baru harus aditif dan
memperbarui keduanya beserta contract test. Header tetap
`X-Internal-Token`; endpoint publik tetap dapat dipakai tanpa header. Bila
header dikirim, token wajib valid dan memiliki scope yang tepat. Token eval
atau enrichment tidak mendapat data tambahan dan tidak melewati rate limit.

## Bentuk dan penyimpanan token

Token baru berbentuk:

```text
surau_st_<token-uuid>.<secret-base64url-256-bit>
```

Plaintext hanya muncul pada respons penerbitan pertama. Respons memakai
`Cache-Control: no-store`; endpoint list/get hanya mengembalikan metadata token.
Database menyimpan SHA-256 dari seluruh token, bukan token atau secret mentah.
TTL default 30 hari, harus positif, dan maksimum 90 hari. Batas maksimum juga
diterapkan oleh constraint database sehingga tidak dapat dilewati lewat SQL.
Satu principal boleh mempunyai T1 dan T2 aktif bersamaan.

Autentikasi membaca database pada setiap request tanpa positive cache. Revoke
token mematikan satu credential; revoke principal mematikan semua credential
sekarang dan mendatang. Token yang expired/revoked tetap dapat diatribusikan
ke principal setelah digest cocok. Token palsu atau malformed selalu dicatat
sebagai `unattributed`, bukan nama dari UUID yang ditebak.

## API admin

Semua endpoint memerlukan Bearer JWT dengan kapabilitas
`manage-service-tokens`. Semua mutasi juga memerlukan MFA yang masih segar.

| Method | Path | Catatan |
|---|---|---|
| `GET` / `POST` | `/v1/admin/service-identities` | list `{items,total}` / buat principal |
| `GET` / `PATCH` | `/v1/admin/service-identities/{id}` | metadata aman / ganti deskripsi+scope |
| `POST` | `/v1/admin/service-identities/{id}/tokens` | terbitkan token, plaintext sekali |
| `POST` | `/v1/admin/service-identities/{id}/tokens/{token_id}/revoke` | cabut satu token |
| `POST` | `/v1/admin/service-identities/{id}/revoke` | cabut principal permanen |

Mutasi resource existing memerlukan ETag dari GET melalui `If-Match`.
Header hilang menghasilkan `428`, ETag stale/malformed `412`, dan `*` adalah
escape hatch eksplisit. Nama principal tidak dapat diubah lewat PATCH.

Error autentikasi mesin stabil untuk konsumen:

| HTTP | `code` | Arti |
|---|---|---|
| 401 | `invalid_service_token` | missing, malformed, palsu, expired, atau revoked; sengaja disamarkan |
| 403 | `insufficient_service_scope` | credential valid tetapi scope salah |
| 503 | `service_identity_unavailable` | registry/audit tidak dapat menjamin pencatatan |

## Audit request internal

Semua route `/internal/*` harus didaftarkan lewat manifest route berscope.
Contract test membandingkan manifest dengan route Fiber agar route baru tidak
dapat lolos tanpa auth dan audit. Row audit dibuat sebelum handler bisnis;
bila write audit gagal, handler tidak dijalankan dan request mendapat 503.
Setelah handler selesai, status HTTP akhir ditulis ke row yang sama.

Audit menyimpan snapshot nama principal, token ID, scope yang diminta, method,
route template (bukan ID/query mentah), request/trace ID, IP, hasil auth, dan
status HTTP. Cleanup terawasi menghapus row yang berumur lebih dari tepat 90
hari; `SERVICE_IDENTITY_AUDIT_RETENTION` dikunci ke `2160h`.

## Jaringan

`/internal/*` tetap hanya untuk jaringan privat. Reverse proxy publik harus
hard-404 path tersebut. Scope dan audit adalah lapisan tambahan, bukan pengganti
segmentasi jaringan. Prosedur rotasi dan pembuktian tanpa downtime ada di
[`service-identity-rotation.md`](service-identity-rotation.md).
