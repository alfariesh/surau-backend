# Citable Unit registry (Fase 1B, Q-2, dan K-1)

Last updated: 2026-07-13

Dokumen ini menjelaskan registry B-1 dan permukaan kurasi protected B-6. Kontrak Anchor yang
diratifikasi dan endpoint resolusi publik B-2 ada di [`docs/anchors.md`](anchors.md). Sumber
kebenaran desain:
`roadmap/phase-1b-content-backbone.md` §C1/C2 dan register keputusan B-D1..D11.

## Apa ini

Substrat tunggal ber-ID untuk unit ±paragraf lintas korpus (charter D3). Registry memberi setiap
potongan **Anchor stabil** + Provenance Class + License Status + lifecycle + silsilah (lineage),
sehingga sitasi presisi, retrieval, dan halaman entitas berdiri di atas identitas yang sama.
Tabel korpus (`book_pages`, `quran_ayahs`, …) tetap sumber kebenaran teks tampilan; registry
memegang bentuk terstruktur dan koordinat exact untuk pencarian serta sitasi.

Korpus aktif saat ini adalah **kitab** (pilot B-1 yang diperluas ke katalog published oleh K-1)
dan **Quran** (Q-2). Quran memakai registry, resolver, serta lineage yang sama; tabel binding Quran
hanya adapter natural-key ke sumber korpus, bukan registry kedua. Hadith/wiki menyusul di fase
domainnya.

## Skema

Fondasi: `migrations/20260709065724_add_citable_units.{up,down}.sql`. Adopsi Quran:
`migrations/20260712000001_add_quran_citable_units.{up,down}.sql` beserta tiga migrasi index
online sesudahnya. Perluasan katalog kitab K-1 dimulai oleh
`migrations/20260713000001_add_k1_citable_catalog.{up,down}.sql`; index K-1 dibuat/diganti oleh
migrasi `20260713000002` sampai `20260713000013`, dengan setiap `CREATE/DROP INDEX CONCURRENTLY`
berdiri sendiri agar deploy tidak mengunci tulisan setabel.

### `citable_units`

| Kolom | Peran |
|---|---|
| `id` UUID PK | UUIDv5 deterministik atas natural key; PK ⇒ keunikan natural-key lintas SEMUA lifecycle gratis |
| `corpus` | `kitab`/`quran`/`hadith`/`wiki` (CHECK) |
| `book_id` | scope kitab; NULL untuk unit Quran |
| `heading_id` | soft-ref ke `book_headings` (tanpa FK); NULL = front-matter sebelum anchor pertama |
| `page_id` | soft-ref; locator fisik **sekunder** (B-D2), bukan identitas |
| `kind` | `paragraph`, `footnote`, `quran_quote`, `html`, `summary`, serta kind Quran; fallback `html` menyimpan isi yang belum dapat dipecah aman |
| `content_role` + `language` | slot publikasi kitab: `book_page/ar`, `section_translation/{lang}`, atau `heading_summary/{lang}`; keduanya immutable dan bagian natural key K-1 |
| `ordinal` | dicetak-sekali per scope, **tak pernah didaur ulang**; bagian dari anchor |
| `position` | urutan tampil kini (mutable) |
| `parent_unit_id` | footnote → unit induk; metadata mutable (di-repoint saat induk berganti) |
| `anchor` | grammar kanonik kitab atau `quran/{surah}:{ayah}/u/{ordinal}` (UNIQUE) |
| `marker` | marker footnote (mis. `(¬٢)`); bagian input hash untuk footnote |
| `text` / `html` | teks tampilan + html (bila ada) |
| `source_document_hash` + `source_char_start/end` | snapshot dokumen dan rentang Unicode code point/rune exact; angka adalah posisi karakter, bukan byte UTF-8 |
| `text_normalized` / `normalization_version` | hanya via `internal/searchtext` (profil ber-versi, C5/B-5) |
| `content_hash` | `sha256(kind‖0x00‖marker‖0x00‖text)` — perubahan formatting html TIDAK mengubah identitas |
| `occurrence` | pembeda kembar se-scope (naik melewati kembar pensiun ⇒ ID tak didaur) |
| `provenance_class` | `source`/`editorial`/`machine` — **immutable** (B-D11); review = dimensi terpisah |
| `review_status` | `pending`/`approved`/`rejected`/`ambiguous`/`needs_review`; tidak mengubah Provenance Class |
| `provenance_detail` JSONB | `release` (source) / `edit_actor_id` (editorial) / `footnote_link` |
| `generation_run_id` | FK immutable ke `generation_runs`; wajib hanya ketika `provenance_class=machine` (B-6) |
| `license_status` | override per-unit; NULL = mewarisi Edition/Work sementara dari `books` (B-4) |
| `effective_license_status` | hasil akhir override unit, Edition kitab, atau sumber Quran |
| `license_source` | `unit_override`, `edition`, atau sumber script/terjemahan/transliterasi Quran |
| `interpretive_corpus_eligible` | gerbang lama generated DB: `corpus <> 'quran'` |
| `interpretive_retrieval_eligible` | gerbang K-1 generated DB: menolak Quran/`quran_quote`; `source` boleh, sedangkan `editorial`/`machine` wajib `review_status=approved` |
| `lifecycle` + `retired_at` | `active`/`superseded`/`tombstoned` (CHECK: active ⟺ retired_at NULL) |

`citable_unit_lineage(predecessor_id, successor_id, reason)` — edge supersede; `reason` =
`edit` (perubahan/split/merge yang terbukti sejajar) atau `content_move` (rescue pass dengan
identitas isi yang sama). Resolver menelusuri seluruh penerus aktif, bukan menebak penerus terdekat.

### Identitas kitab v1 dan v2

- Unit `content_role=book_page, language=ar` selalu memakai natural key/UUID v1 dari B-1. Backfill
  K-1 tidak boleh mengganti UUID atau Anchor pilot yang sudah pernah diterbitkan.
- Unit translation/summary memakai UUID v2 yang menambahkan `(content_role, language)` ke natural
  key. Teks sama dalam dua bahasa atau dua peran tidak bertabrakan.
- HTML/whitespace yang hanya mengubah format memperbarui HTML tersanitasi pada unit yang sama.
  Perubahan teks, split, merge, atau perpindahan mencetak unit baru dan lineage sehingga Anchor
  lama tetap resolve.

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

Book-RAG K-1 membaca hanya dari `public_book_interpretive_citable_units`: unit harus aktif,
ber-role `book_page`, lolos publikasi/grandfather B-4, tidak ber-override `restricted`, dan
`interpretive_retrieval_eligible=true`. Kolom generated tersebut selalu menolak korpus Quran,
`quran_quote`, dan unit machine/editorial yang belum approved. Mode `dual` mempertahankan sumber
halaman lama lalu menambahkan `unit_id`/`unit_anchor` lewat quote exact; mode `unit` menyusun source
block langsung per-unit. Lihat prosedur takedown di
[`docs/license-governance.md`](license-governance.md) dan rollout di README §Book RAG.

License Status sengaja diputuskan pada saat query, bukan disalin saat backfill. Override
`citable_units.license_status='restricted'` selalu ditolak; `NULL` mewarisi keputusan buku melalui
`public_book_publications`, termasuk grandfather B-4. Karena itu perubahan lisensi atau unpublish
langsung menutup retrieval dalam transaksi sumber yang sama. Rekonsiliasi asinkron hanya
memperbarui bentuk unit dan tidak pernah menjadi jeda keamanan.

### Provenance Class dan generation identity

- Teks impor asli memakai Provenance Class `source`.
- Isi hasil publish edit halaman memakai `editorial` bila teks berubah. Edit format saja tidak
  mengubah identitas/provenance teks; aktor format tetap tercatat di detail.
- Translation dan summary menyalin Provenance Class sumber final apa adanya. Unit `machine` wajib
  membawa `generation_run_id` immutable yang menunjuk generation identity B-6 (model,
  prompt-version, run).
- Aset historis `legacy_unknown` tidak dipalsukan sebagai machine. Ia dilaporkan sebagai skipped
  dan tetap tidak eligible sampai provenance sah tersedia.

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
- **Gerbang anti-tafsir:** `interpretive_retrieval_eligible` adalah kolom generated dan corpus/kind
  unit immutable. Partial index dan view retrieval hanya berisi baris dengan nilai tersebut
  `TRUE`, jadi Quran, `quran_quote`, machine draft/pending/rejected, dan editorial belum-approved
  tidak dapat masuk kandidat meskipun query/prompt berubah. Gerbang live yang wajib
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
ID byte-identik. Unit halaman Arab memakai identitas v1; enrichment memakai identitas v2 yang
menambahkan content role dan language.

**Ketahanan-suntingan (AC-2):** suntingan yang memecah/menggabung unit mencetak unit BARU +
menandai pendahulunya `superseded` + edge lineage; anchor lama selalu terselesaikan lewat
implementasi lineage bersama yang dipakai `ResolveUnit` internal dan
`GET /v1/anchors/resolve` publik (walk rekursif ke semua penerus aktif; lihat
[`docs/anchors.md`](anchors.md)). LIS atas pasangan match menjaga batas gap saat blok berpindah
urutan; rescue pass level-buku menutup pindah-scope & hapus-kembar.

### Deriver K-1 dan character map

`readerutil.StructureMixedContent` sekarang DOM/block-aware. Parser mengenali paragraf, list,
tabel, `<pre>`, nested/malformed HTML, marker/footnote berulang atau tidak berpasangan, serta
kutipan Quran di teks biasa maupun HTML. Tokenisasi menghasilkan character map ke sumber sejak
awal; ia tidak mencari substring sesudah normalisasi. Jika struktur tidak dapat dipecah tanpa
kehilangan isi, satu unit `html` menyimpan fallback tersanitasi.

Profil derivasi kitab **v3** menutup temuan audit katalog nyata: halaman yang hanya berisi blok
judul/Anchor tetap mendapat unit `html` exact (tanpa menggandakan judul pada halaman yang memiliki
isi badan), dan footnote historis dengan parent yang sudah berpindah mengikuti satu successor
aktif atau menjadi `unlinked` secara fail-closed bila lineage ambigu. Perubahan profil menandai
semua buku lama stale sehingga runner F1-H wajib membuktikannya ulang; view retrieval baru membuka
buku kembali hanya setelah profil v3 selesai.

Coverage K-1 dibandingkan kembali terhadap dokumen kanonik. Setiap rentang memakai Unicode
code point/rune yang sama di Go dan Python, harus berada di dokumen dengan hash yang sama, dan
seluruh rune yang seharusnya tercakup wajib hadir tepat pada unit aktif.

### Re-anchor `knowledge_mentions`

K-1 menambah `unit_id`, `unit_char_start/end`, `unit_binding_status`, `unit_binding_version`, dan
`unit_source_hash` secara aditif; koordinat halaman lama tetap disimpan selama satu rilis.
Binding hanya exact, harus berada dalam satu Citable Unit, dan memakai teks/hash snapshot yang
sama dengan extractor Python. Hasil nol, lebih dari satu, atau lintas unit diberi status
`missing`, `ambiguous`, `stale`, atau `cross_unit`. Fuzzy match dan LLM dilarang.

Untuk mention lama yang dibuat sebelum edit editorial, transaksi katalog membangun snapshot raw,
mengikat span lama, lalu membentuk unit efektif beserta lineage. Mention approved wajib menunjuk
unit aktif atau Anchor historis yang masih resolve. Writer Python baru menyimpan mention, source
span, dan binding unit per halaman dalam satu transaksi.

## Titik masuk

1. **Backfill** (`internal/backfill`, playbook F1-H):
   - `citable-units-kitab-pilot` — derive/re-derive buku pilot yang butuh kerja (belum ter-derive
     ATAU sumber berubah sejak derivasi). 1 buku per chunk (reconcile atomik).
   - `citable-units-kitab-rederive` — re-run tanpa syarat atas buku ter-derive = **drill
     determinisme** (harus nol mutasi) + jalur pemulihan setelah perubahan parser/profil.
   - Pilot set: `CitablePilotBooks = [797, 7312, 12876, 22842]`.
   - `citable-units-kitab-catalog` — semua buku raw-published non-deleted, satu buku per commit
     tahan-restart; O-4-2 mendahulukan kategori tafsir/syarah, pembaca aktif, lalu `book_id`.
   - `citable-units-kitab-catalog-rederive` — pass checksum determinisme seluruh katalog; delta
     berikutnya hanya mengantre ulang buku yang hasil katalognya berubah.
   - `citable-units-quran` — initial/stale-only, atomik satu surah per checkpoint; cursor circular
     memastikan surah yang kembali stale di belakang cursor tetap diproses sebelum selesai.
   - `citable-units-quran-rederive` — drill determinisme seluruh surah yang pernah di-derive.
   - `quran-page-navigation-v1` — isi hanya `page_number IS NULL` dari snapshot QPC Hafs v1
     (6.236 ayat, 604 halaman, checksum dibekukan); tidak menimpa nilai existing. Job ini harus
     mendahului reconcile Quran agar locator halaman lama dan `page_id` unit kembali lengkap.
     Snapshot metadata faktual diambil 2026-07-12 dari Quran Foundation Content API v4
     `verses/by_juz` (field `page_number`), cocok dengan skema resource QUL 86, lalu dipin ke
     SHA-256 `6acff20b3a70942e7e3980f99a1fc03df53bf891165a6cd63b714e028dd75c14`.
     Generator hanya alat maintenance; aplikasi tidak memanggil sumber eksternal saat runtime.
2. **Invalidation sumber kitab** — perubahan halaman/heading, publish edit, translation/summary
   production, re-import, dan lifecycle menandai `books.units_stale_at` langsung. View retrieval
   fail-closed pada buku stale; job katalog/reconcile kemudian memperbarui bentuk unit.
3. **Importer kitab** — setelah re-import jalankan katalog dengan `-restart`; fingerprint drift
   membatalkan buku yang sumbernya berubah di tengah reconcile dan delta run mengantrekannya lagi.
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
  `anchor_malformed`, `footnote_parent`, `lineage_cycle`, `quran_binding`, `quran_interpretive`,
  `interpretive_safety`, `rag_projection_dangling`, `approved_mention_anchor`,
  `mention_unit_dangling`, `mention_binding_mismatch`, dan `cross_reference_anchor`.
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

# materialisasi seluruh katalog published dan pass determinisme K-1
/backfill -job=citable-units-kitab-catalog -catalog-priority-only -chunk-size=1 -sleep=200ms -restart
/backfill -job=citable-units-kitab-catalog -chunk-size=1 -sleep=200ms -restart
/backfill -job=citable-units-kitab-catalog-rederive -chunk-size=1 -sleep=200ms -restart

# laporan N/N, canonical-rune coverage, parity, audit nol, dan p95
/backfill -verify-citable-catalog

# derive Quran yang belum pernah diproses atau ditandai stale
/backfill -job=quran-page-navigation-v1 -restart
/backfill -job=citable-units-quran

# drill determinisme/pemulihan seluruh surah derived
/backfill -job=citable-units-quran-rederive -restart

# impor buku pilot dulu bila belum ada (E4 staged importer, aman):
#   --entrypoint /import-books app -book-ids=797,7312,12876,22842 -source-dir=/shamela
```

Pantau: `surau_citable_catalog_books{state="missing"}` dan `{state="stale"}` → 0;
jumlahkan `surau_citable_catalog_queue_items` untuk state `pending`, `running`, dan `failed` → 0;
`surau_citable_audit_violations` = 0, serta counter parity/fallback Book-RAG. Lihat tabel bukti
lengkap di [`docs/data-change-playbook.md`](data-change-playbook.md) §Membaca bukti K-1.

Pencarian unit memakai indeks full-text `simple` atas teks Arab tanpa harakat sebagai jalur umum
yang bounded; trigram lama tetap dipakai hanya bila hasil exact belum memenuhi jendela evidence.
Ini mempertahankan toleransi typo/substring tanpa membuat kata umum memindai ratusan ribu unit.

## Catatan deviasi (vs asumsi roadmap)

- **B-A1** ("parser readerutil memadai"): B-1 mula-mula menambah parser toleran-tag. K-1
  mengeraskannya menjadi DOM/block-aware dan mempertahankan unit `html` sebagai fallback tanpa
  kehilangan isi; kontrak reader lama tetap beku.
- **Anchor grammar B-2 diratifikasi tanpa backfill:** seluruh 16.205 Anchor pilot yang dicetak B-1
  sudah sesuai profil kanonik kitab, sehingga tidak ada alamat yang perlu ditulis ulang.
