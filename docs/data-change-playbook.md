# Playbook Perubahan Data Besar (F1-H) — expand-contract + backfill resumable

Panduan WAJIB untuk setiap perubahan skema/data yang menyentuh banyak baris atau tabel besar
(book_pages 295rb+ baris, book_headings 182rb+, dst.) — supaya endpoint publik TIDAK PERNAH
down dan pekerjaan besar bisa dihentikan/dilanjutkan tanpa kehilangan progres.

Konsumen pertama: backfill `authors-name-search` (sesi F1-C/F1-H). Registry Citable Unit lalu
memakai pola yang sama untuk pilot B-1, Quran Q-2, dan materialisasi seluruh katalog kitab K-1.

> **Nota supervisi:** job backfill dijalankan MANUAL sebagai CLI (bukan loop app F1-C) —
> aspek "supervisi" yang relevan untuk job one-shot dipenuhi oleh: checkpoint resumable,
> advisory lock anti-dobel, panic-safe (proses terpisah dari API), dan metrik progres yang
> menumpang collector F1-B. Job TERJADWAL in-app (mis. audit B-1) tetap wajib memakai
> `loopSpec` supervisor (`internal/app/loop.go`, lihat docs/module-conventions.md).

---

## 1. Kapan playbook ini wajib

- Menambah kolom turunan/denormalisasi yang harus diisi untuk jutaan/ratusan-ribu baris lama.
- Mengubah bentuk data (pemecahan paragraf, re-normalisasi teks, migrasi tipe kolom).
- Menambah constraint pada tabel yang sudah berisi data.
- Menghapus kolom/jalur baca lama setelah penggantinya hidup.

Perubahan kecil (tabel baru kosong, kolom nullable tanpa backfill, seed statis ratusan baris)
cukup migrasi biasa — tetap pasangan up/down.

## 2. Aturan dasar migrasi repo ini (golang-migrate)

- File timestamped berpasangan `YYYYMMDDHHMMSS_desc.up.sql` / `.down.sql` (`make migrate-create`).
- **Setiap statement berjalan autocommit sendiri-sendiri — TIDAK ada transaksi pembungkus.**
  Urutan statement adalah bagian dari desain: letakkan yang aman-diulang lebih dulu, dan
  tulis semua DDL idempotent (`IF NOT EXISTS` / `IF EXISTS`) supaya migrasi yang gagal di
  tengah bisa diulang tanpa manual-fix.
- App otomatis migrate saat boot (build `-tags migrate`); schema DIRTY = boot ditolak dengan
  instruksi pemulihan (runbook: docs/deploy-vps.md §Keamanan migration).
- Down migration yang tidak bisa membalikkan (mis. VALIDATE) = no-op berkomentar + `SELECT 1;`.

## 3. Pola inti: EXPAND → BACKFILL → SWITCH → CONTRACT

Empat langkah TERPISAH (deploy terpisah bila menyentuh jalur baca):

1. **EXPAND (aditif, deploy kapan saja):** tambah kolom nullable / tabel / jalur tulis baru.
   Tanpa `NOT NULL`, tanpa `DEFAULT` yang menulis ulang tabel, tanpa index berat inline —
   `ADD COLUMN` nullable = metadata-only, instan.
   Mulai isi jalur baru untuk TULISAN BARU di deploy yang sama (mis. importer ikut menulis
   `name_search` di INSERT **dan** di `ON CONFLICT ... DO UPDATE SET` — kalau tidak, re-import
   meninggalkan nilai basi yang lebih buruk daripada NULL).
2. **BACKFILL (job resumable, di luar migrasi):** isi baris lama pakai runner §4. JANGAN
   `UPDATE` masal di dalam file migrasi untuk tabel besar — memblokir boot deploy dan tak
   bisa di-pause.
3. **SWITCH READER:** pembaca mulai memakai jalur baru, DENGAN fallback aditif (lengan lama
   tetap) sampai backfill terbukti tuntas (metrik pending = 0).
4. **CONTRACT (paling akhir, setelah soak):** kunci invariant (`NOT VALID` CHECK → VALIDATE,
   atau `SET NOT NULL`), pasang index final, hapus lengan/kolom lama. Langkah ini sering
   TIDAK dikerjakan di sesi yang sama — catat di §8.

### Pola constraint: NOT VALID → preflight → VALIDATE (self-guarding)

Sudah dipraktikkan di `migrations/20260627000002`, `20260628000001`, `20260628000002` — jadikan
templat:

```sql
-- (a) EXPAND: constraint hanya untuk baris BARU; baris lama tidak discan,
--     migrasi deploy tidak mungkin abort karena data legacy kotor.
ALTER TABLE t ADD CONSTRAINT t_col_check CHECK (col <> '') NOT VALID;

-- (b) PREFLIGHT (manual, sebelum VALIDATE — masing-masing HARUS 0 baris):
--     SELECT count(*) FROM t WHERE NOT (col <> '');
--     remediasi data dulu bila > 0 (via backfill §4, bukan UPDATE buta).

-- (c) VALIDATE self-guarding di migrasi berikutnya: hanya tervalidasi
--     saat data sudah bersih; tidak pernah menggagalkan deploy.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM t WHERE NOT (col <> '')) THEN
        ALTER TABLE t VALIDATE CONSTRAINT t_col_check;
    ELSE
        RAISE NOTICE 't_col_check belum divalidasi: masih ada baris kotor';
    END IF;
END $$;
```

### Index pada tabel besar

`CREATE INDEX` biasa mengunci tulisan setabel penuh. Untuk tabel besar WAJIB
`CREATE INDEX CONCURRENTLY IF NOT EXISTS ...` — dan karena golang-migrate autocommit
per-statement, CONCURRENTLY legal di file migrasi ASAL berdiri sendiri (tanpa statement lain
yang bergantung padanya di file yang sama). Gagal di tengah = index INVALID → `DROP INDEX` +
ulangi. Preflight ruang disk dulu untuk index >1GB.

## 4. Pola backfill resumable (runner `internal/backfill`)

Sifat yang dijamin runner (dipakai `cmd/backfill`):

| Sifat | Mekanisme |
|---|---|
| Resume tanpa kehilangan progres | checkpoint `backfill_jobs` (cursor + rows_done) ditulis SETIAP chunk |
| Pause anggun | SIGINT/SIGTERM → chunk berjalan diselesaikan → status `paused` |
| Anti dobel-instans | `pg_try_advisory_lock` per job di KONEKSI dedicated (lock advisory = session-scoped) |
| Tanpa downtime | chunk kecil (default 100) + throttle antar chunk (default 200ms) ⇒ lock baris singkat |
| Idempotent | job hanya menyentuh baris yang masih butuh kerja (`WHERE kolom IS NULL`), urut PK |
| Ter-metrik | collector F1-B membaca `backfill_jobs` → `surau_backfill_rows_done/rows_total/pending_rows{job}` di Prometheus/Grafana — TANPA scrape target baru |
| Gagal jelas | error → status `failed` + kolom `error`; rerun = resume dari cursor |

Menulis job baru = implement interface `backfill.Job` (`internal/backfill/jobs.go`) + daftar di
`Jobs()`. Aturan wajib:
- Chunk = `SELECT ... WHERE pk > $cursor AND <butuh-kerja> ORDER BY pk LIMIT $n` lalu SATU
  `UPDATE ... FROM (VALUES ...)` (UPDATE Postgres tidak punya ORDER BY/LIMIT).
- JANGAN bump `updated_at` untuk kolom turunan — churn timestamp = bohong ke konsumen
  (sitemap lastmod, cache, dsb.).
- Transformasi teks Arab HANYA via `internal/searchtext` (profil kanonik ber-versi, charter
  D9); versi profil tercatat di checkpoint (`profile_version`).
- Live test `TestLive*` (SURAU_LIVE_PG) untuk job baru = bukti pause/resume-nya, contoh:
  `internal/backfill/backfill_live_test.go`.

### Varian K-1: antrean tahan-restart per buku

K-1 menambahkan `citable_unit_catalog_queue` di bawah checkpoint F1-H. Checkpoint
`backfill_jobs` tetap mengendalikan satu proses, pause, dan cursor. Antrean tambahan menyimpan
hasil setiap buku agar proses yang mati tidak mengulang seluruh katalog.

| Status item | Arti dan tindakan operator |
|---|---|
| `pending` | Menunggu giliran. Rerun melanjutkan dari item ini. |
| `running` | Sedang diproses. Saat proses lama mati, runner tunggal berikutnya mengembalikannya ke `pending`. |
| `completed` | Satu buku sudah commit atomik dan memiliki `source_fingerprint`, `result_checksum`, waktu, serta jumlah percobaan. |
| `failed` | Buku gagal dan alasan disimpan. Periksa alasan, perbaiki sumber/bug, lalu jalankan kembali job; item gagal dicoba lagi sekali pada awal resume. |
| `cancelled` | Buku tidak lagi published atau sudah dihapus. Bukti antreannya disimpan, tetapi buku tidak diproses. Bila dipublish lagi, delta run akan mengantrekannya kembali. |

Satu buku adalah satu unit transaksi `REPEATABLE READ` dengan advisory lock per buku. Deriver
membaca fingerprint sumber sebelum dan sesudah pekerjaan; perubahan halaman/aset di tengah jalan
membatalkan commit. Karena itu restart tidak dapat meninggalkan separuh halaman atau separuh
binding `knowledge_mentions`.

Urutan antrean K-1 mengikuti O-4-2 dan tidak boleh diubah oleh kebetulan urutan SQL:

1. kategori 3 harus bernama `التفسير` dan kategori 7 harus bernama `شروح الحديث`;
2. jumlah pembaca aktif terbanyak, lalu aktivitas `reading_progress` terbaru;
3. `book_id` terkecil sebagai pemutus seri deterministik.

Runner menolak mulai jika nama kategori 3/7 drift. Targetnya selalu dihitung ulang dari
`book_publications.status='published' AND books.is_deleted=false`; License Status tidak mengubah
denominator internal ini. Buku restricted tetap dimaterialisasi, lalu proyeksi publik menolaknya.

## 5. Menjalankan di VPS (dev dulu, lalu prod)

Binary `backfill` ikut di image app (Dockerfile). Jalankan dari direktori deploy:

```sh
cd /srv/surau/backend

# lihat job terdaftar
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -list

# jalankan (PG_URL sudah ada di env container app)
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -job=authors-name-search

# pause: Ctrl-C (atau kirim SIGTERM) → "paused ... rerun the same command to resume"
# resume: jalankan perintah yang sama lagi
# ulang dari nol (mis. profil normalisasi naik versi): tambah -restart
# tabel sangat besar: naikkan -sleep (mis. 500ms) atau kecilkan -chunk-size
```

Materialisasi dan pembuktian K-1 dijalankan berurutan. `-restart` pada job katalog berarti
"hitung ulang target dan proses delta", bukan menghapus Citable Unit yang sudah selesai:

```sh
# Gelombang 1 O-4-2: hanya kategori 3 التفسير dan 7 شروح الحديث.
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -job=citable-units-kitab-catalog \
    -catalog-priority-only -chunk-size=1 -sleep=200ms -restart

# Gelombang 2: buka queue yang sama untuk sisa katalog; unit wave 1 tidak diulang.
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -job=citable-units-kitab-catalog \
    -chunk-size=1 -sleep=200ms -restart

# Pass kedua seluruh katalog. Setiap buku wajib nol mutasi dan checksum identik.
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -job=citable-units-kitab-catalog-rederive \
    -chunk-size=1 -sleep=200ms -restart

# Bukti machine-readable. Exit 0 hanya jika seluruh gerbang K-1 lulus.
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  exec app /backfill -verify-citable-catalog
```

- **Pause/resume:** kirim SIGINT/SIGTERM atau tekan Ctrl-C, lalu jalankan perintah yang sama.
  Buku yang sudah commit tidak diulang.
- **Retry gagal:** baca `error` pada `citable_unit_catalog_queue`, perbaiki penyebabnya, lalu rerun.
  Jangan mengubah status queue dengan SQL manual.
- **Crash setelah commit buku:** item sudah `completed`; rerun mengambil item `pending` berikutnya.
- **Delta setelah job pernah completed:** jalankan job katalog dengan `-restart`. Hanya buku baru,
  stale, profil lama, atau fingerprint hasil yang berubah yang masuk antrean lagi.
- **Buku ditarik dari katalog:** runner memberi status `cancelled`; itu bukan kegagalan coverage
  karena buku tersebut juga keluar dari denominator published saat verifier berikutnya.

Pantau: Grafana → Prometheus query `surau_backfill_rows_done{job="..."}` vs `rows_total`;
`surau_backfill_pending_rows` harus turun ke 0 dan TETAP 0 (drift = jalur tulis baru bocor).

Untuk K-1, pantau juga metrik tanpa label ID buku/unit:

- `surau_citable_catalog_books{state="target|materialized|missing|stale"}`;
- `surau_citable_catalog_queue_items{job,state}`;
- `surau_citable_catalog_queue_attempts{job}`;
- `surau_citable_catalog_completed_duration_seconds{job}`.

## 6. Verifikasi & rollback

- Selesai backfill: `pending_rows = 0`, status `completed`, spot-check nilai
  (`SELECT ... LIMIT 5`), lalu smoke endpoint publik yang terdampak.
- Endpoint publik harus tetap 200 SELAMA backfill jalan (buktikan dengan curl saat drill).
- Rollback EXPAND = down migration (drop kolom turunan — aman, re-derivable).
- Rollback SWITCH = deploy sebelumnya (lengan lama masih ada — itu alasan fallback aditif).
- CONTRACT hanya setelah ≥1 siklus soak tanpa alarm; sesudah contract, rollback = restore
  (mahal) — karena itu paling akhir.

### Membaca bukti K-1

`/backfill -verify-citable-catalog` mencetak satu objek JSON bertimestamp oleh sistem pencatat
operasi. Simpan output mentah bersama SHA deploy. Arti field penting:

| Bukti | Syarat lulus |
|---|---|
| `target_books`, `materialized_books`, `missing_books`, `stale_books` | `target_books=N`, `materialized_books=N`, `missing_books=0`, `stale_books=0`; N dihitung saat run, bukan angka yang ditulis tangan. |
| `uncovered_pages`, `target_assets`, `materialized_assets`, `uncovered_assets` | Semua halaman efektif nonkosong dan aset translation/summary published terwakili; uncovered wajib 0. |
| `canonical_documents`, `canonical_covered_runes`, `uncovered_canonical_runes`, `unexpected_canonical_spans` | Rentang karakter memakai Unicode code point/rune yang sama dengan extractor; uncovered dan unexpected wajib 0. |
| `determinism_verified_books` | Sama dengan N setelah pass rederive; setiap buku harus `minted=updated=superseded=tombstoned=0` dan checksum registry sama. |
| `parity_target_books`, `parity_verified_books`, `parity_mismatches`, `unit_anchors_unresolved` | Stub LLM deterministik menguji satu sitasi per buku public-retrievable; verified=target, mismatch=0, Anchor unit unresolved=0. |
| `mention_bindings` | Ringkasan `bound|pending|stale|ambiguous|cross_unit|missing`; mention approved tanpa Anchor yang resolve tetap dihitung sebagai pelanggaran audit. |
| `search_samples`, `search_p95_ms`, `search_within_target` | Ada sampel dan p95 pencarian unit <400 ms. |
| `audit` | Semua pelanggaran harus 0, termasuk projection menggantung, Anchor/lineage, Cross-Reference, approved mention, mismatch span, dan machine-unreviewed eligible. |
| `queue_pending`, `passed` | Queue aktif 0 dan `passed=true`. CLI keluar non-zero bila salah satu gerbang gagal. |

Sitasi respons RAG bersifat ephemeral, jadi tidak diaudit sebagai histori palsu. Runtime validator,
golden eval, SSE smoke, counter parity/fallback, dan parity full-catalog pada verifier adalah
buktinya. Audit terjadwal tetap memeriksa seluruh proyeksi persisten dan memicu alarm Telegram
bila satu saja pelanggaran bernilai >0.

Jika `uncovered_pages` menemukan halaman heading-only, jangan mengecualikannya dari denominator.
Naikkan profil derivasi dan materialisasikan blok struktural tersebut sebagai fallback `html`
exact. Jika p95 memburuk pada kata Arab umum, periksa pemakaian indeks full-text unit terlebih
dahulu; trigram adalah fallback typo/substring, bukan jalur utama sapuan seluruh katalog.

### Catatan khusus registry Citable Unit (B-1)

Tabel `citable_units` / `citable_unit_lineage` dijaga trigger `citable_registry_guard()`:
DML apa pun DITOLAK kecuali transaksinya diawali `SET LOCAL surau.registry_writer =
'unit-service'` (hanya service `internal/usecase/unitregistry` yang melakukannya).
Implikasi operasional:

- **Escape hatch insiden** (psql manual — pakai HANYA saat insiden, audit `hash_mismatch`
  akan menangkap perubahan teks di luar service):
  `BEGIN; SET LOCAL surau.registry_writer = 'unit-service'; <DML>; COMMIT;`
- **Migrasi data** yang menyentuh registry WAJIB membungkus SET LOCAL + DML dalam SATU
  blok `BEGIN;...COMMIT;` atau `DO $$ ... $$` di file migrasi — statement migrasi
  autocommit satu-satu (§2), jadi `SET LOCAL` telanjang adalah no-op.
- **Restore data-only** (`pg_restore --data-only`) butuh `--disable-triggers`; restore penuh
  normal aman (trigger dibuat SETELAH data di fase post-data).

### Catatan khusus versi normalisasi (B-5)

Profil persisten kini dibekukan sebagai `search-key` v1. Dua job berikut hanya memberi cap versi
setelah membuktikan nilai lama sama dengan keluaran Go v1:

```sh
/backfill -job=authors-name-search-v1-version
/backfill -job=quran-references-normalization-v1
```

Job pertama berjalan per chunk author; job kedua berjalan atomik per Work untuk legacy Quran
reference dan bridge. Drift menghentikan chunk/Work tanpa memberi cap sebagian. Data knowledge
Python lama sengaja tetap `NULL` sampai dinormalisasi ulang, jadi jangan menjadikan semua kolom
versi non-NULL sebagai target buta. Semantik, kolom, dan gerbang Go-Python lengkap ada di
[`docs/arabic-normalization.md`](arabic-normalization.md).

## 7. Checklist per-backfill (salin ke PR)

```text
[ ] EXPAND: migrasi up/down aditif, teruji bolak-balik (up-down-up) di DB kosong
[ ] Jalur tulis baru MENULIS kolom baru (insert + ON CONFLICT UPDATE) sejak deploy expand
[ ] Job backfill: idempotent, urut PK, tanpa bump updated_at, terdaftar di backfill.Jobs()
[ ] Live test pause/resume hijau (SURAU_LIVE_PG)
[ ] Drill di dev: run → Ctrl-C → resume → completed; endpoint publik 200 selama jalan
[ ] Metrik surau_backfill_* terlihat di Prometheus; pending = 0 setelah selesai
[ ] SWITCH reader aditif (lengan lama tetap) + test
[ ] Rencana CONTRACT tertulis (kapan, apa yang dihapus/dikunci) — boleh sesi lain
```

## 8. Log pemakaian ("dipakai oleh")

| # | Job | Tabel | Status | Catatan |
|---|---|---|---|---|
| 1 | `authors-name-search` | authors (3.187 baris dev) | SELESAI di dev (S6, 2026-07-08): drill pause di 500/3.187 → resume → completed; endpoint publik 200 sepanjang drill; pending=0 | Bukti produk: `/v1/authors?q=احمد` 19 → 209 hasil (192/192 nama ber-hamzah terjangkau); B-5 kini membekukan profil `search-key` v1 tanpa melipat `ء`/`ة`, dan cap versinya diberikan terpisah oleh `authors-name-search-v1-version` |
| 2 | `citable-units-kitab-pilot` | citable_units (dari book_*) | SELESAI B-1 (SESI 11, 2026-07-09): 4 buku eval nyata (797/7312/12876/22842) → 16.205 unit; predikat staleness = job yang sama melayani derive awal & re-derive pasca-re-import | 1 buku per chunk (reconcile atomik); laporan per-buku di stdout |
| 3 | `citable-units-kitab-rederive` | citable_units | Drill determinisme AC-1: re-run tanpa syarat atas buku ter-derive; TERBUKTI lokal 2026-07-09 — matched=16.205, minted=0, checksum registry MD5 identik | Juga jalur pemulihan setelah perubahan parser/profil yang disengaja (gelombang supersede diserap lineage) |
| 4 | `citable-units-quran` | citable_units + quran_citable_unit_bindings | Q-2 initial/stale-only; atomik satu surah, cursor circular, aman di-resume | Importer langsung reconcile surah tersentuh; trigger `units_stale_at` + compare-and-set source adalah recovery bila hook gagal/race |
| 5 | `citable-units-quran-rederive` | citable_units + quran_citable_unit_bindings | Drill determinisme semua surah derived; live test membuktikan re-run tidak menambah unit | Jalur pemulihan sesudah perubahan deriver non-primer; drift teks primer gagal tertutup |
| 6 | `quran-page-navigation-v1` | quran_ayahs.page_number | Q-2 rollout: isi NULL dari peta QPC Hafs v1 beku (6.236/6.236 ayat, halaman 1–604), resumable dan tak menimpa nilai existing | Jalankan sebelum `citable-units-quran -restart`; update page menandai surah stale lalu reconcile memperbarui `page_id` tanpa re-mint |
| 7 | `citable-units-kitab-catalog` | `citable_units`, `citable_unit_lineage`, `knowledge_mentions`, `citable_unit_catalog_queue` | K-1: seluruh denominator kitab published; delta-aware, satu buku atomik, priority wave O-4-2, exact mention binding | License Status tidak mengurangi target internal; proyeksi publik menerapkan B-4 secara terpisah |
| 8 | `citable-units-kitab-catalog-rederive` | registry + queue checksum | K-1 determinism pass seluruh N buku | Lulus hanya jika nol mutasi dan checksum sama dengan hasil catalog untuk setiap buku |

Q-2 juga memiliki drill migrasi populated khusus di CI:
`TestQuranCitableUnitMigrationDrill`. Drill menjalankan core migration `up→down→up` sambil menjaga
snapshot B-1, B-3, dan source row Quran legacy identik; index `CONCURRENTLY` diuji oleh job
round-trip migrasi umum di luar transaksi drill.
Dua unique index registry pengganti dibangun lebih dahulu lewat migrasi expand `CONCURRENTLY`,
lalu hanya di-swap secara metadata oleh core migration. Down Q-2 ditolak bila histori/keputusan
lisensi Quran sudah ada agar rollback tidak menghapus bukti takedown.
