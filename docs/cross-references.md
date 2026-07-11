# Kontrak Cross-Reference (Fase 1B / B-3)

Last updated: 2026-07-11

Dokumen ini adalah kontrak normatif untuk registry **Cross-Reference**: klaim ter-atribusi yang
menghubungkan dua **Anchor** konten. Grammar Anchor tetap bersumber dari `docs/anchors.md`.
Registry ini hanya untuk hubungan konten↔konten; hubungan span mention→entity tetap berada di
domain Wiki dan bukan bagian endpoint ini.

B-3 bersifat aditif. Endpoint lama
`GET /v1/books/{book_id}/quran-references` dan embed reader tetap memakai bentuk respons dan
perilaku lama, sementara data Quran dipindahkan secara paralel ke registry umum.

## Model klaim

Setiap Cross-Reference disimpan satu kali dengan arah `source_anchor` → `target_anchor`.
Backlink bukan salinan baris baru: query `incoming` membaca indeks sisi target, sedangkan
`outgoing` membaca indeks sisi sumber.

```ts
type CrossReferenceKind = "cites" | "quotes" | "explains" | "parallel";
type CrossReferenceMethod = "resolver" | "machine" | "human";
type CrossReferenceReviewStatus =
  | "pending"
  | "approved"
  | "rejected"
  | "ambiguous"
  | "needs_review";

type GenerationIdentity = {
  run_id: string;
  model_id: string;
  prompt_version: string;
};

type CrossReference = {
  id: string;
  source_anchor: string;
  target_anchor: string;
  source_corpus: string;
  target_corpus: string;
  source_work_id?: number;
  target_work_id?: number;
  target_quran_surah_id?: number;
  target_quran_from_ayah?: number;
  target_quran_to_ayah?: number;
  kind: CrossReferenceKind;
  method: CrossReferenceMethod;
  method_detail: {
    strategy?: string;
    model_id?: string;
    prompt_version?: string;
    run_id?: string;
    actor_id?: string;
  };
  generation?: GenerationIdentity;
  confidence?: number;
  review_status: CrossReferenceReviewStatus;
  evidence_text: string;
  evidence_normalized: string;
  normalization_version: number;
  origin: string;
  origin_key: string;
  created_by?: string;
  reviewed_by?: string;
  reviewed_at?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};
```

### Kind terkurasi

| `kind` | Arti |
|---|---|
| `cites` | Sumber menyebut atau merujuk target. |
| `quotes` | Sumber mengutip target secara verbatim. |
| `explains` | Sumber menjelaskan target, misalnya tafsir atas ayat. |
| `parallel` | Kedua teks/riwayat merupakan paralel yang ter-atribusi. |

Kosakata ini tertutup. Granularitas ditentukan oleh Anchor, bukan kind baru: `quran/73`
berarti surah secara umum, `quran/73:4` berarti satu ayah, dan
`quran/73:4..quran/73:10` berarti rentang.

### Atribusi metode dan confidence

- `resolver` wajib membawa `method_detail.strategy`.
- `machine` wajib membawa `model_id`, `prompt_version`, dan `run_id`. Ketiganya adalah identitas
  pekerjaan mesin dan tidak boleh direka atau dihapus setelah review. Run wajib terdaftar di
  registry immutable `generation_runs`.
- `human` wajib membawa `actor_id` yang diambil server dari sesi terautentikasi; payload tidak
  boleh menentukan atau menyamar sebagai aktor lain.
- Tulisan baru wajib memiliki `confidence` dalam rentang `0..1`. `null` hanya dapat muncul pada
  row legacy yang memang tidak memiliki nilai; migrasi tidak mengarang confidence lama.

`evidence_text` menyimpan kutipan bukti asli. `evidence_normalized` menyimpan bentuk hasil profil
normalisasi kanonik dan `normalization_version` menyatakan versi profil itu. Metadata adalah
pelengkap, bukan pengganti bukti maupun atribusi metode.

`generation` adalah representasi typed B-6 dari tuple machine yang sama. `method_detail` tetap
dipertahankan agar client lama tidak rusak; client baru sebaiknya membaca `generation` untuk
atribusi model+prompt+run. Untuk `resolver` dan `human`, `generation` tidak dikirim. Review hanya
mengubah `review_status`, reviewer, dan waktu review; identitas machine tetap utuh.

Kolom corpus, Work, dan rentang Quran adalah projection untuk query/visibility. Identitas tetap
dua Anchor; projection tidak boleh dipakai sebagai identitas kedua.

## Endpoint publik dua arah

```http
GET /v1/cross-references?anchor={canonical_anchor}&direction=incoming|outgoing&kind={kind}&limit=50&offset=0
```

Endpoint ini public dan tidak membutuhkan bearer token.

| Query | Wajib | Default/batas | Semantik |
|---|---:|---|---|
| `anchor` | ya | Anchor kanonik | Point/range yang ingin dicari. Selalu URL-encode. |
| `direction` | ya | — | `incoming` untuk backlink; `outgoing` untuk tautan dari Anchor. |
| `kind` | tidak | seluruh empat kind | Filter satu kind terkurasi. |
| `limit` | tidak | `50`, maksimum `200` | Ukuran halaman. |
| `offset` | tidak | `0`, maksimum `10000` | Offset halaman. |

Tidak ada query status publik. Server selalu mengunci query ke `review_status=approved`; nilai
status yang dikirim client tidak dapat membuka `pending`, `rejected`, `ambiguous`, atau
`needs_review`.

Contoh backlink ayah:

```http
GET /v1/cross-references?anchor=quran%2F73%3A4&direction=incoming&kind=quotes
```

```json
{
  "items": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "source_anchor": "kitab/797/h/11/u/42",
      "target_anchor": "quran/73:4..quran/73:10",
      "source_corpus": "kitab",
      "target_corpus": "quran",
      "source_work_id": 797,
      "target_quran_surah_id": 73,
      "target_quran_from_ayah": 4,
      "target_quran_to_ayah": 10,
      "kind": "quotes",
      "method": "resolver",
      "method_detail": { "strategy": "exact_quote" },
      "confidence": 1,
      "review_status": "approved",
      "evidence_text": "...",
      "evidence_normalized": "...",
      "normalization_version": 1,
      "origin": "legacy_quran_reference",
      "origin_key": "550e8400-e29b-41d4-a716-446655440000",
      "metadata": {},
      "created_at": "2026-07-10T12:00:00Z",
      "updated_at": "2026-07-10T12:00:00Z"
    }
  ],
  "total": 3,
  "work_total": 2
}
```

`total` menghitung edge, sedangkan `work_total` menghitung Work berbeda pada sisi lawan. Karena
itu dua kutipan dari kitab yang sama tetap menambah `total` dua kali tetapi hanya menambah
`work_total` satu kali. Untuk UI ayah, gunakan `work_total` bagi label seperti
"dikutip oleh 2 kitab" dan gunakan `total` untuk paginasi daftar klaim.

Urutan stabil adalah `created_at`, lalu `id`. `total` dan `work_total` adalah hitungan penuh
sebelum `limit`/`offset`; halaman kosong karena offset tinggi tetap membawa hitungan yang benar.

### Range, lineage, dan visibility

- Query incoming satu ayah juga menemukan target range Quran yang mencakup ayah itu.
- Anchor surah-only seperti `quran/73` tidak dianggap merujuk setiap ayah dalam surah. Ia hanya
  cocok ketika Anchor surah itu sendiri diminta.
- Backlink ke Anchor Citable Unit lama tetap dapat ditemukan melalui seluruh penerus aktifnya
  setelah split/merge. Hasil akhir di-deduplicate.
- Kedua ujung wajib lolos gerbang visibility publik yang sama dengan resolver/reader. Buku
  unpublished/deleted dan Anchor yang tidak terlihat tidak memengaruhi `items`, `total`, atau
  `work_total`.
- Semua status selain `approved` tidak pernah memengaruhi tiga field tersebut.

Respons sukses memakai weak body-hash `ETag` dan cache browser lima menit:

```http
Cache-Control: public, max-age=300, stale-while-revalidate=86400
ETag: W/"..."
```

Client boleh mengirim `If-None-Match`; kecocokan menghasilkan `304 Not Modified`. Endpoint ini
sengaja tidak masuk cache L1/KV worker agar perubahan review dan lineage tidak tertahan oleh
salinan edge lama.

## Endpoint editorial

Semua route berikut membutuhkan autentikasi dan capability `CapReviewEditorial`:

```text
GET   /v1/editorial/cross-references
POST  /v1/editorial/cross-references
GET   /v1/editorial/cross-references/{id}
PATCH /v1/editorial/cross-references/{id}/review
```

List editorial memakai envelope `{items,total,work_total}` dan mendukung filter `anchor`,
`direction`, `kind`, `method`, `review_status`, `limit`, dan `offset`. Berbeda dari endpoint
publik, queue editorial boleh membaca kelima status review.

List dan get editorial mengirim `generation` untuk setiap row `method=machine`, misalnya:

```json
{
  "method": "machine",
  "method_detail": {
    "model_id": "glm-5.1",
    "prompt_version": "quran-reference-v1",
    "run_id": "550e8400-e29b-41d4-a716-446655440000"
  },
  "generation": {
    "run_id": "550e8400-e29b-41d4-a716-446655440000",
    "model_id": "glm-5.1",
    "prompt_version": "quran-reference-v1"
  }
}
```

Server dan trigger database memastikan kedua bentuk itu sama persis. UUID run tidak dapat diganti
setelah row dibuat. Descriptor lengkap registry dan aturan provenance ada di
[`docs/generation-runs.md`](generation-runs.md).

### Membuat tautan manusia

```http
POST /v1/editorial/cross-references
Authorization: Bearer <token>
Content-Type: application/json

{
  "source_anchor": "kitab/797/h/11/u/42",
  "target_anchor": "kitab/798/h/7/u/3",
  "kind": "quotes",
  "confidence": 0.95,
  "evidence_text": "kutipan asli yang menjadi bukti",
  "metadata": { "note": "opsional" }
}
```

Server menetapkan `method=human`, mengambil actor dari sesi, menormalisasi bukti memakai profil
kanonik, mengisi origin, dan selalu membuat klaim sebagai `pending`. Field atribusi, status,
normalisasi, projection, UUID, dan timestamp dari payload tidak dipercaya. Klaim pending belum
boleh muncul pada query publik mana pun.

### Membaca dan mereview

`GET /v1/editorial/cross-references/{id}` mengirim `ETag`. Client wajib mengirim nilai itu secara
verbatim ketika mereview:

```http
PATCH /v1/editorial/cross-references/550e8400-e29b-41d4-a716-446655440000/review
Authorization: Bearer <token>
If-Match: "opaque-etag-from-get"
Content-Type: application/json

{ "review_status": "approved" }
```

Kelima status sah, termasuk membuka kembali klaim approved menjadi `pending` atau
`needs_review`. `If-Match` yang hilang menghasilkan `428 Precondition Required`; validator yang
sudah stale menghasilkan `412 Precondition Failed`. `If-Match: *` sah untuk aksi sadar yang
memang ingin memakai versi terbaru tanpa membandingkan ETag spesifik.

Tidak ada endpoint edit-in-place atau delete. Klaim yang salah direview menjadi `rejected`, lalu
klaim pengganti dibuat sebagai row baru. Dengan begitu bukti, aktor, dan sejarah keputusan tidak
hilang.

## Bridge Quran dan kontrak endpoint lama

`quran_cross_reference_bridge` menyimpan projection locator fisik lama: `book_id`, `page_id`,
`heading_id`, mention, source/normalized text beserta `normalization_version`, kind lama, rentang
ayah, `match_strategy`, metadata, dan timestamp. UUID row lama dipakai juga sebagai UUID
Cross-Reference. Ini membuat parity, idempotensi, dan rollback dapat dibuktikan tanpa pencocokan
heuristik.

Pemetaan legacy:

| `quran_book_references` | Registry | Target Anchor |
|---|---|---|
| `surah_ayah`, satu ayah | `cites` | `quran/73:4` |
| `surah_ayah`, rentang | `cites` | `quran/73:4..quran/73:10` |
| `surah` | `cites` | `quran/73` |
| `quote` | `quotes` | point/range Quran |
| `ambiguous` dengan target jelas | `cites` | point/range/surah, status wajib `ambiguous` |

Source memakai Anchor heading bila `heading_id` tersedia dan heading masih aktif; jika tidak,
source memakai Anchor Work.
`page_id` tetap locator fisik di bridge dan bukan identitas baru. Row `approved` dengan
`reference_kind=ambiguous`, atau row approved yang tidak dapat membentuk dua Anchor sah, adalah
kesalahan preflight dan menghentikan backfill secara jelas.

Resolver Quran melakukan dual-write legacy + registry + bridge dalam satu transaksi. Kegagalan
salah satu lengan menggulung semuanya; retry aman dan tidak menimpa status yang sudah direview
manusia. Registry dan bridge dilindungi guard database dan hanya boleh ditulis lewat service
bersama.

Selama backfill belum selesai, endpoint lama membaca projection registry lalu melakukan fallback
ke row legacy yang belum mempunyai bridge. Anti-join mencegah UUID yang sudah di-bridge muncul dua
kali. Kontrak berikut tidak berubah:

- envelope `{items,total}` dan shape `BookQuranReference` lama, dengan tambahan aditif
  `normalization_version` (`1` bila terverifikasi; `null` untuk legacy yang belum terbukti);
- approved-only dan gerbang buku published;
- filter `heading_id`, bahasa ayah terlampir, paginasi, total, serta urutan lama;
- `ayahs` terlampir dan embed
  `GET /v1/books/{book_id}/toc/{heading_id}/read?include_quran_references=true`;
- header cache/validator publik yang sudah ada.

Parameter `status` pada endpoint lama dipertahankan hanya untuk kompatibilitas request client
lama. Server mengabaikannya dan selalu mengembalikan `approved`; `status=pending` atau
`status=all` tidak pernah membuka data editorial.

## Operasional backfill dan parity

Nama job adalah `cross-references-quran-bridge`. Jalankan di dev terlebih dahulu:

```sh
# lokal; PG_URL tidak dicetak ke log
go run ./cmd/backfill -job=cross-references-quran-bridge

# container dev/VPS; command yang sama diulang untuk resume
/backfill -job=cross-references-quran-bridge
```

SIGINT/SIGTERM menyelesaikan chunk berjalan, menyimpan checkpoint per buku, lalu memberi status
`paused`. Jalankan command yang sama untuk resume. Setelah `completed`, rerun biasa tidak mengubah
data; `-restart` hanya untuk drill idempotensi terencana. Jangan menjalankan dua instance karena
runner memakai advisory lock. Job bridge **tidak** membekukan writer legacy secara otomatis.

Preflight minimum sebelum backfill:

```sql
SELECT count(*) AS approved_ambiguous
FROM quran_book_references
WHERE review_status = 'approved' AND reference_kind = 'ambiguous';
```

Nilai wajib `0`. Sesudah backfill, approved legacy yang belum lengkap di registry juga wajib `0`:

```sql
SELECT count(*) AS approved_missing_registry
FROM quran_book_references AS legacy
LEFT JOIN quran_cross_reference_bridge AS bridge
  ON bridge.cross_reference_id = legacy.id
LEFT JOIN cross_references AS registry
  ON registry.id = legacy.id
WHERE legacy.review_status = 'approved'
  AND (bridge.cross_reference_id IS NULL OR registry.id IS NULL);
```

Periksa checkpoint dan status review:

```sql
SELECT job_name, status, last_cursor, rows_done, rows_total, error
FROM backfill_jobs
WHERE job_name IN (
  'cross-references-quran-bridge',
  'cross-references-quran-freeze',
  'cross-references-quran-unfreeze'
);

SELECT review_status, count(*)
FROM quran_book_references AS legacy
LEFT JOIN quran_cross_reference_bridge AS bridge
  ON bridge.cross_reference_id = legacy.id
WHERE bridge.cross_reference_id IS NULL
GROUP BY review_status
ORDER BY review_status;
```

Gerbang switch/freeze adalah:

1. drill pause → resume → rerun selesai;
2. approved missing registry tepat nol;
3. `EXCEPT` dua arah pada UUID, locator, kind, strategy, confidence, status, bukti, metadata, dan
   timestamp tepat nol;
4. parity HTTP endpoint lama dan embed reader lulus untuk urutan, pagination, total, dan ayah;
5. smoke outgoing/incoming serta `work_total` lulus;
6. barulah direct-write legacy dibekukan.

Setelah keenam gerbang lulus, jalankan switch eksplisit berikut. Job akan menolak bila checkpoint
bridge belum `completed` atau parity database belum nol:

```sh
# lokal
go run ./cmd/backfill -job=cross-references-quran-freeze

# container dev/VPS
/backfill -job=cross-references-quran-freeze
```

Tabel legacy tetap dipertahankan sebagai rollback projection selama masa soak. Mengubahnya menjadi
view atau menghapusnya bukan bagian deploy B-3. Bila operator memutuskan rollback ke binary lama,
buka kembali writer legacy **sebelum** deploy binary tersebut:

```sh
# lokal
go run ./cmd/backfill -job=cross-references-quran-unfreeze

# container dev/VPS
/backfill -job=cross-references-quran-unfreeze
```

Unfreeze hanya mengubah switch writer secara guarded; registry dan bridge tidak dihapus. Down
migration baru dipertimbangkan setelah memastikan tidak ada writer baru yang membutuhkan registry.
Untuk siklus freeze/unfreeze berikutnya, tambahkan `-restart` karena checkpoint job transisi yang
sudah `completed` sengaja tidak dieksekusi ulang tanpa persetujuan eksplisit operator.
