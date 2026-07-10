# Citable Unit registry (Fase 1B / B-1 dan B-2)

Dokumen ini menjelaskan registry INTERNAL B-1. Kontrak Anchor yang diratifikasi dan endpoint
resolusi publik B-2 ada di [`docs/anchors.md`](anchors.md). Sumber kebenaran desain:
`roadmap/phase-1b-content-backbone.md` §C1/C2 dan register keputusan B-D1..D11.

## Apa ini

Substrat tunggal ber-ID untuk unit ±paragraf lintas korpus (charter D3). Hari ini parser
`readerutil` memecah konten kitab jadi blok paragraf **hanya di memori**; registry memberi setiap
blok itu **alamat stabil** + provenance + lisensi + lifecycle + silsilah (lineage), sehingga
sitasi presisi (Fase 4), retrieval (Fase 7), dan halaman entitas (Fase 6) berdiri di atas
identitas yang sama. Tabel korpus (`book_pages`, `quran_ayahs`, …) tetap sumber kebenaran teks
tampilan; registry memegang identitas + teks ternormalisasi untuk pengindeksan.

Korpus pilot B-1 = **kitab** saja. Quran/hadith/wiki menyusul di fase domainnya di atas kontrak
yang sama.

## Skema

`migrations/20260709065724_add_citable_units.{up,down}.sql`.

### `citable_units`

| Kolom | Peran |
|---|---|
| `id` UUID PK | UUIDv5 deterministik atas natural key; PK ⇒ keunikan natural-key lintas SEMUA lifecycle gratis |
| `corpus` | `kitab`/`quran`/`hadith`/`wiki` (CHECK) |
| `book_id` | scope kitab; satu-satunya FK keluar (`→books ON DELETE CASCADE`) |
| `heading_id` | soft-ref ke `book_headings` (tanpa FK); NULL = front-matter sebelum anchor pertama |
| `page_id` | soft-ref; locator fisik **sekunder** (B-D2), bukan identitas |
| `kind` | `paragraph`/`heading`/`quran_quote`/`footnote`/`html` |
| `ordinal` | dicetak-sekali per scope, **tak pernah didaur ulang**; bagian dari anchor |
| `position` | urutan tampil kini (mutable) |
| `parent_unit_id` | footnote → unit induk; metadata mutable (di-repoint saat induk berganti) |
| `anchor` | grammar kanonik `kitab/{book_id}/h/{heading_id\|0}/u/{ordinal}` (UNIQUE), diratifikasi B-2 |
| `marker` | marker footnote (mis. `(¬٢)`); bagian input hash untuk footnote |
| `text` / `html` | teks tampilan + html (bila ada) |
| `text_normalized` / `normalization_version` | hanya via `internal/searchtext` (profil ber-versi, C5/B-5) |
| `content_hash` | `sha256(kind‖0x00‖marker‖0x00‖text)` — perubahan formatting html TIDAK mengubah identitas |
| `occurrence` | pembeda kembar se-scope (naik melewati kembar pensiun ⇒ ID tak didaur) |
| `provenance_class` | `source`/`editorial`/`machine` — **immutable** (B-D11); review = dimensi terpisah |
| `provenance_detail` JSONB | `release` (source) / `edit_actor_id` (editorial) / `footnote_link` |
| `license_status` | override per-unit; NULL = mewarisi Work (kolom Work datang di B-4) |
| `lifecycle` + `retired_at` | `active`/`superseded`/`tombstoned` (CHECK: active ⟺ retired_at NULL) |

`citable_unit_lineage(predecessor_id, successor_id, reason)` — edge supersede; `reason` =
`edit` (gap alignment se-scope) atau `content_move` (rescue pass antar-scope / kembar bertahan).

### Invarian yang ditegakkan DB

- **UNIQUE anchor**; **UNIQUE `(corpus,book_id,heading_id,ordinal)`** semua lifecycle (ordinal tak
  didaur); **UNIQUE parsial `(…,kind,content_hash,occurrence) WHERE active`** (tripwire tulisan asing).
- **Trigger `citable_registry_guard()`** BEFORE INS/UPD/DEL di kedua tabel → tolak (SQLSTATE
  `42501`) kecuali transaksinya menjalankan `SET LOCAL surau.registry_writer = 'unit-service'`
  ATAU `pg_trigger_depth() > 1` (agar cascade FK internal lolos). Inilah penegakan "satu jalur
  tulis" di tingkat data (C2). Escape hatch & implikasi restore: `docs/data-change-playbook.md` §6.

## Satu jalur tulis: service `unitregistry`

`internal/usecase/unitregistry` = SATU-SATUNYA penulis registry.

```
DeriveBook(src)            deriver.go  — murni: konten efektif → []DerivedUnit (identity-free)
PlanBook(...)              plan.go     — murni: derived + snapshot → rencana (mint/update/retire/lineage)
UseCase.ReconcileBook      unitregistry.go — load → derive → plan → apply (retry konflik ≤3)
CitableUnitRepo.ApplyReconcile  persistent — tx berjaga: SET LOCAL + advisory-lock + batch + assert invarian
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
2. **Hook editorial** — `editorial.PublishPageDraft` memanggil `ReconcileBookIfDerived` setelah
   commit (buku non-pilot = no-op via gerbang `units_derived_at IS NULL`; error → log + counter
   `surau_citable_reconcile_failures_total`, publish tetap sukses).
3. **Importer** — TIDAK ada hook. Re-derive pasca-re-import = jalankan `citable-units-kitab-pilot`
   lagi (predikat staleness menangkap buku yang berubah). Industrialisasi katalog = Fase 4/K-1.

## Audit terjadwal (AC-3)

Loop `citable_unit_audit` (F1-C, `CITABLE_AUDIT_ENABLED` default TRUE, `CITABLE_AUDIT_INTERVAL`
default 1h). `AuditPass` mengisi:

- **`surau_citable_audit_violations{check}`** (alert `sum > bool 0` → Telegram, rule
  `surau-citable-audit`): `book_gone`, `superseded_no_successor`, `active_with_successor`,
  `hash_mismatch` (recompute Go atas semua unit aktif — tripwire tulisan asing/pelanggaran GUC),
  `anchor_malformed`, `footnote_parent`, `lineage_cycle`.
- **`surau_citable_audit_info{check}`** (dashboard saja, TANPA alarm): `stale_books`,
  `legacy_dangling_*` (quran_book_references / knowledge_* menunjuk halaman `is_deleted` — milik
  B-3).
- Inventori `surau_citable_units{lifecycle}`.

## Cara menjalankan (ops)

```sh
# lihat job
/backfill -list

# derive/re-derive pilot (aman diulang; resume kalau di-pause)
/backfill -job=citable-units-kitab-pilot

# drill determinisme (harus nol mutasi; snapshot tabel sebelum/sesudah harus identik)
/backfill -job=citable-units-kitab-rederive

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
