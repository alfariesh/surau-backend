# Citable Unit registry (Fase 1B / B-1, B-2, B-5, B-6, dan Q-2)

Last updated: 2026-07-12

Dokumen ini menjelaskan registry B-1 dan permukaan kurasi protected B-6. Kontrak Anchor yang
diratifikasi dan endpoint resolusi publik B-2 ada di [`docs/anchors.md`](anchors.md). Sumber
kebenaran desain:
`roadmap/phase-1b-content-backbone.md` §C1/C2 dan register keputusan B-D1..D11.

## Apa ini

Substrat tunggal ber-ID untuk unit ±paragraf lintas korpus (charter D3). Hari ini parser
`readerutil` memecah konten kitab jadi blok paragraf **hanya di memori**; registry memberi setiap
blok itu **alamat stabil** + provenance + lisensi + lifecycle + silsilah (lineage), sehingga
sitasi presisi (Fase 4), retrieval (Fase 7), dan halaman entitas (Fase 6) berdiri di atas
identitas yang sama. Tabel korpus (`book_pages`, `quran_ayahs`, …) tetap sumber kebenaran teks
tampilan; registry memegang identitas + teks ternormalisasi untuk pengindeksan.

Korpus aktif saat ini adalah **kitab** (pilot B-1) dan **Quran** (Q-2). Quran memakai registry,
resolver, serta lineage yang sama; tabel binding Quran hanya adapter natural-key ke sumber korpus,
bukan registry kedua. Hadith/wiki menyusul di fase domainnya.

## Skema

Fondasi: `migrations/20260709065724_add_citable_units.{up,down}.sql`. Adopsi Quran:
`migrations/20260712000001_add_quran_citable_units.{up,down}.sql` beserta tiga migrasi index
online sesudahnya.

### `citable_units`

| Kolom | Peran |
|---|---|
| `id` UUID PK | UUIDv5 deterministik atas natural key; PK ⇒ keunikan natural-key lintas SEMUA lifecycle gratis |
| `corpus` | `kitab`/`quran`/`hadith`/`wiki` (CHECK) |
| `book_id` | scope kitab; NULL untuk unit Quran |
| `heading_id` | soft-ref ke `book_headings` (tanpa FK); NULL = front-matter sebelum anchor pertama |
| `page_id` | soft-ref; locator fisik **sekunder** (B-D2), bukan identitas |
| `kind` | kind kitab lama ditambah `primary_text`/`translation`/`transliteration`; footnote tetap memakai `footnote` |
| `ordinal` | dicetak-sekali per scope, **tak pernah didaur ulang**; bagian dari anchor |
| `position` | urutan tampil kini (mutable) |
| `parent_unit_id` | footnote → unit induk; metadata mutable (di-repoint saat induk berganti) |
| `anchor` | grammar kanonik kitab atau `quran/{surah}:{ayah}/u/{ordinal}` (UNIQUE) |
| `marker` | marker footnote (mis. `(¬٢)`); bagian input hash untuk footnote |
| `text` / `html` | teks tampilan + html (bila ada) |
| `text_normalized` / `normalization_version` | hanya via `internal/searchtext` (profil ber-versi, C5/B-5) |
| `content_hash` | `sha256(kind‖0x00‖marker‖0x00‖text)` — perubahan formatting html TIDAK mengubah identitas |
| `occurrence` | pembeda kembar se-scope (naik melewati kembar pensiun ⇒ ID tak didaur) |
| `provenance_class` | `source`/`editorial`/`machine` — **immutable** (B-D11); review = dimensi terpisah |
| `provenance_detail` JSONB | `release` (source) / `edit_actor_id` (editorial) / `footnote_link` |
| `generation_run_id` | FK immutable ke `generation_runs`; wajib hanya ketika `provenance_class=machine` (B-6) |
| `license_status` | override per-unit; NULL = mewarisi Edition/Work sementara dari `books` (B-4) |
| `effective_license_status` | hasil akhir override unit, Edition kitab, atau sumber Quran |
| `license_source` | `unit_override`, `edition`, atau sumber script/terjemahan/transliterasi Quran |
| `interpretive_corpus_eligible` | generated DB: `corpus <> 'quran'`; aplikasi tidak dapat mengubahnya |
| `lifecycle` + `retired_at` | `active`/`superseded`/`tombstoned` (CHECK: active ⟺ retired_at NULL) |

`citable_unit_lineage(predecessor_id, successor_id, reason)` — edge supersede; `reason` =
`edit` (gap alignment se-scope) atau `content_move` (rescue pass antar-scope / kembar bertahan).

### Adapter Quran: `quran_citable_unit_bindings`

Satu ayah mempunyai Anchor logis permanen `quran/{surah}:{ayah}` dan unit anak berikut:

- ordinal 1: teks primer QPC Hafs, `kind=primary_text`, Provenance Class `source`;
- satu `translation` per `(source_id, ayah)`, lengkap dengan bahasa dan atribusi sumber;
- setiap footnote terstruktur menjadi unit `footnote` dengan `parent_unit_id` menunjuk unit
  terjemahannya serta `footnote_key` stabil;
- satu `transliteration` per `(source_id, ayah)`.

Ordinal yang pernah dicetak tidak digunakan ulang. Re-run atas sumber sama menghasilkan UUIDv5,
Anchor, dan content hash yang sama. Perubahan terjemahan/footnote/transliterasi mencetak penerus
dan mempertahankan Anchor lama lewat lineage. Perubahan teks primer setelah mint gagal tertutup
dengan `ErrQuranPrimaryTextDrift`; koreksi mushaf tidak boleh menyamar sebagai edit biasa.
Koreksi `page_number` hanya memperbarui `page_id` mutable pada unit yang sama—ID/ordinal tidak
berubah, dan locator halaman serta metadata unit tetap konsisten.

Lisensi dan atribusi tidak disalin ke ribuan unit. Binding menunjuk source table, lalu view
`citable_units_with_effective_license` menghitung status efektif saat baca. Karena itu takedown
satu sumber berlaku langsung tanpa re-mint.

### Gerbang License Status pada resolver publik

Work/Edition harus lolos proyeksi publik lebih dahulu. Setelah itu, Anchor dan Cross-Reference
kitab hanya menganggap unit ber-override `NULL` atau `permitted` sebagai target publik. Unit Quran
wajib tidak memiliki override dan selalu mengikuti status source. Filter berlaku
per-unit: sibling eligible pada heading/page atau ujung lineage yang sama tetap dikembalikan.
Buku yang `units_derived_at`-nya sudah terisi tidak pernah kembali ke fallback source-row ketika
semua unit locator terfilter; fallback kasar hanya menjaga kompatibilitas buku non-derived.

Reader HTML dan Book RAG belum menyusun respons dari registry ini. Komposisi per-unit keduanya
tetap pekerjaan K-1; B-4 tidak memotong lalu merangkai ulang halaman karena itu berisiko mengubah
teks sumber. Lihat batas operasional dan prosedur takedown sementara di
[`docs/license-governance.md`](license-governance.md).

### Invarian yang ditegakkan DB

- **UNIQUE anchor**; **UNIQUE `(corpus,book_id,heading_id,ordinal)`** semua lifecycle (ordinal tak
  didaur); **UNIQUE parsial `(…,kind,content_hash,occurrence) WHERE active`** (tripwire tulisan asing).
- **Trigger `citable_registry_guard()`** BEFORE INS/UPD/DEL di kedua tabel → tolak (SQLSTATE
  `42501`) kecuali transaksinya menjalankan `SET LOCAL surau.registry_writer = 'unit-service'`
  ATAU `pg_trigger_depth() > 1` (agar cascade FK internal lolos). Inilah penegakan "satu jalur
  tulis" di tingkat data (C2). Escape hatch & implikasi restore: `docs/data-change-playbook.md` §6.
- **Trigger identitas generation**: Citable Unit machine wajib membawa run terdaftar; unit
  source/editorial tidak boleh membawa run. Provenance Class dan run tidak dapat diubah setelah
  unit dicetak. Detail kontrak registry ada di [`docs/generation-runs.md`](generation-runs.md).
- **Gerbang anti-tafsir:** `interpretive_corpus_eligible` adalah kolom generated dan corpus unit
  immutable. Index retrieval interpretatif hanya berisi baris dengan nilai tersebut `TRUE`, jadi
  unit Quran tidak dapat masuk kandidat meskipun query/prompt berubah. Gerbang live yang wajib
  dipanggil U-6 adalah
  `internal/repo/persistent/quran_citable_unit_live_test.go::TestLiveQuranCitableUnitsNeverInterpretiveEligible`.
- **Teks primer immutable:** setelah QPC primer non-kosong tersimpan, trigger database menolak
  perubahan nilainya. Importer juga melakukan preflight sebelum tulisan pertama, sehingga command
  gagal tidak dapat meninggalkan typo Quran yang telanjur publik.

## Endpoint kurasi

Endpoint protected berikut membutuhkan JWT dengan capability `CapReviewEditorial`:

```http
GET /v1/editorial/citable-units/{id}
Authorization: Bearer <token>
```

Respons memuat unit yang diminta dan semua penerus aktif yang dicapai melalui lineage. Untuk
unit aktif, `successors` adalah array kosong. Untuk unit superseded, client kurasi dapat tetap
membaca identitas lama sekaligus berpindah ke satu atau beberapa penerus aktif.

```json
{
  "unit": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "corpus": "kitab",
    "book_id": 797,
    "heading_id": 11,
    "page_id": 12,
    "kind": "paragraph",
    "ordinal": 42,
    "position": 8,
    "anchor": "kitab/797/h/11/u/42",
    "text": "...",
    "text_normalized": "...",
    "normalization_version": 1,
    "occurrence": 1,
    "language": "id",
    "provenance_class": "machine",
    "generation": {
      "run_id": "f76535b0-5e15-4a9e-99a6-0da4ad1ef315",
      "model_id": "glm-5.1",
      "prompt_version": "reader-translation-v1"
    },
    "lifecycle": "active",
    "created_at": "2026-07-11T12:00:00Z",
    "updated_at": "2026-07-11T12:00:00Z"
  },
  "successors": []
}
```

`generation` hanya hadir untuk Provenance Class `machine`. Unit `source`/`editorial` tidak
mengirim field tersebut. `normalization_version` adalah versi profil pada
`text_normalized`, bukan versi teks tampilan. Bentuk lengkap juga memuat locator, parent,
marker/HTML, license override, dan retirement time bila tersedia.

Error contract: tanpa autentikasi `401`, tanpa capability `403`, UUID/unit yang tidak ditemukan
`404 {"error":"citable unit not found"}`. Endpoint resolver Anchor publik tetap locator-only;
detail provenance dan generation hanya tersedia di permukaan kurasi ini.

## Satu jalur tulis: service `unitregistry`

`internal/usecase/unitregistry` = SATU-SATUNYA penulis registry.

```
DeriveBook(src)            deriver.go  — murni: konten efektif → []DerivedUnit (identity-free)
PlanBook(...)              plan.go     — murni: derived + snapshot → rencana (mint/update/retire/lineage)
UseCase.ReconcileBook      unitregistry.go — load → derive → plan → apply (retry konflik ≤3)
CitableUnitRepo.ApplyReconcile  persistent — tx berjaga: SET LOCAL + advisory-lock + batch + assert invarian
UseCase.ReconcileQuranSurah quran.go — ayah+rendering → plan Quran pada registry yang sama
```

**Determinisme (AC-1):** derive murni deterministik (tanpa map-iteration/random/clock di jalur
output) + sumber tak berubah ⇒ himpunan derived identik ⇒ semua cocok ⇒ nol mint/retire/update ⇒
ID byte-identik. Identity = `UUIDv5(ns, "kitab|book|heading|kind|hex(hash)|occurrence")`.

**Ketahanan-suntingan (AC-2):** suntingan yang memecah/menggabung unit mencetak unit BARU +
menandai pendahulunya `superseded` + edge lineage; anchor lama selalu terselesaikan lewat
implementasi lineage bersama yang dipakai `ResolveUnit` internal dan
`GET /v1/anchors/resolve` publik (walk rekursif ke semua penerus aktif; lihat
[`docs/anchors.md`](anchors.md)). LIS atas pasangan match menjaga batas gap saat blok berpindah
urutan; rescue pass level-buku menutup pindah-scope & hapus-kembar.

## Titik masuk

1. **Backfill** (`internal/backfill`, playbook F1-H):
   - `citable-units-kitab-pilot` — derive/re-derive buku pilot yang butuh kerja (belum ter-derive
     ATAU sumber berubah sejak derivasi). 1 buku per chunk (reconcile atomik).
   - `citable-units-kitab-rederive` — re-run tanpa syarat atas buku ter-derive = **drill
     determinisme** (harus nol mutasi) + jalur pemulihan setelah perubahan parser/profil.
   - Pilot set: `CitablePilotBooks = [797, 7312, 12876, 22842]`.
   - `citable-units-quran` — initial/stale-only, atomik satu surah per checkpoint; cursor circular
     memastikan surah yang kembali stale di belakang cursor tetap diproses sebelum selesai.
   - `citable-units-quran-rederive` — drill determinisme seluruh surah yang pernah di-derive.
2. **Hook editorial** — `editorial.PublishPageDraft` memanggil `ReconcileBookIfDerived` setelah
   commit (buku non-pilot = no-op via gerbang `units_derived_at IS NULL`; error → log + counter
   `surau_citable_reconcile_failures_total`, publish tetap sukses).
3. **Importer kitab** — tidak ada hook; jalankan job kitab lagi setelah re-import.
4. **Importer Quran** — setelah seluruh tahap import dan bridge rujukan sukses, surah yang tersentuh
   langsung direconcile. Trigger source tetap menandai `units_stale_at`, sehingga kegagalan hook
   terlihat audit dan dapat dipulihkan dengan `citable-units-quran`.

Load Quran memakai satu snapshot repeatable-read. Apply mengunci row surah dan hanya boleh
menghapus `units_stale_at` yang tidak lebih baru dari snapshot; konflik otomatis replan. Selama
surah stale, reader boleh mempertahankan field display legacy berlisensi, tetapi tidak pernah
menempelkan ID/Anchor/footnote-unit lama seolah masih current.

## Audit terjadwal (AC-3)

Loop `citable_unit_audit` (F1-C, `CITABLE_AUDIT_ENABLED` default TRUE, `CITABLE_AUDIT_INTERVAL`
default 1h). `AuditPass` mengisi:

- **`surau_citable_audit_violations{check}`** (alert `sum > bool 0` → Telegram, rule
  `surau-citable-audit`): `book_gone`, `superseded_no_successor`, `active_with_successor`,
  `hash_mismatch` (recompute Go atas semua unit aktif — tripwire tulisan asing/pelanggaran GUC),
  `anchor_malformed`, `footnote_parent`, `lineage_cycle`, `quran_binding`,
  `quran_interpretive`.
- **`surau_citable_audit_info{check}`** (dashboard saja, TANPA alarm): `stale_books`,
  `stale_quran_surahs`, `legacy_dangling_*` (quran_book_references / knowledge_* menunjuk halaman
  `is_deleted` — milik B-3).
- Inventori `surau_citable_units{lifecycle}`.

## Cara menjalankan (ops)

```sh
# lihat job
/backfill -list

# derive/re-derive pilot (aman diulang; resume kalau di-pause)
/backfill -job=citable-units-kitab-pilot

# drill determinisme (harus nol mutasi; snapshot tabel sebelum/sesudah harus identik)
/backfill -job=citable-units-kitab-rederive

# derive Quran yang belum pernah diproses atau ditandai stale
/backfill -job=citable-units-quran

# drill determinisme/pemulihan seluruh surah derived
/backfill -job=citable-units-quran-rederive -restart

# impor buku pilot dulu bila belum ada (E4 staged importer, aman):
#   --entrypoint /import-books app -book-ids=797,7312,12876,22842 -source-dir=/shamela
```

Pantau: `surau_backfill_pending_rows{job="citable-units-kitab-pilot"}` → 0; `surau_citable_units`;
`surau_citable_audit_violations` = 0.

## Catatan deviasi (vs asumsi roadmap)

- **B-A1** ("parser readerutil memadai"): halaman Shamela bertag jatuh ke satu blok html kasar di
  `StructureSourceContent`. B-1 menambah `readerutil.StructureMixedContent` (sibling; kontrak
  reader lama beku) yang line-based & toleran-tag. Unit `html` fallback dihitung di laporan
  reconcile sebagai sinyal kualitas parser untuk Fase 4.
- **Anchor grammar B-2 diratifikasi tanpa backfill:** seluruh 16.205 Anchor pilot yang dicetak B-1
  sudah sesuai profil kanonik kitab, sehingga tidak ada alamat yang perlu ditulis ulang.
