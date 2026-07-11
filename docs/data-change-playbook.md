# Playbook Perubahan Data Besar (F1-H) — expand-contract + backfill resumable

Panduan WAJIB untuk setiap perubahan skema/data yang menyentuh banyak baris atau tabel besar
(book_pages 295rb+ baris, book_headings 182rb+, dst.) — supaya endpoint publik TIDAK PERNAH
down dan pekerjaan besar bisa dihentikan/dilanjutkan tanpa kehilangan progres.

Konsumen pertama: backfill `authors-name-search` (sesi F1-C/F1-H). Konsumen berikutnya yang
sudah direncanakan: backfill pilot Citable Unit (B-1, roadmap/phase-1b-content-backbone.md).

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

Pantau: Grafana → Prometheus query `surau_backfill_rows_done{job="..."}` vs `rows_total`;
`surau_backfill_pending_rows` harus turun ke 0 dan TETAP 0 (drift = jalur tulis baru bocor).

## 6. Verifikasi & rollback

- Selesai backfill: `pending_rows = 0`, status `completed`, spot-check nilai
  (`SELECT ... LIMIT 5`), lalu smoke endpoint publik yang terdampak.
- Endpoint publik harus tetap 200 SELAMA backfill jalan (buktikan dengan curl saat drill).
- Rollback EXPAND = down migration (drop kolom turunan — aman, re-derivable).
- Rollback SWITCH = deploy sebelumnya (lengan lama masih ada — itu alasan fallback aditif).
- CONTRACT hanya setelah ≥1 siklus soak tanpa alarm; sesudah contract, rollback = restore
  (mahal) — karena itu paling akhir.

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
