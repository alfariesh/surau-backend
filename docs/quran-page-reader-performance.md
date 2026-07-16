# Kinerja reader halaman Quran

Dokumen ini mencatat investigasi independen untuk:

```text
GET /v1/quran/pages/{page_number}/ayahs?view=reader_minimal
```

## Baseline produksi

Baseline diambil dari rilis `0.4.3` oleh workflow terikat dan read-only
[`Quran Page Performance`](https://github.com/alfariesh/surau-backend/actions/runs/29508303040).
Load dibatasi maksimal 10 request serentak, 200 request per proses, dan 60 detik.

| Jalur / skenario | Sampel sukses | p50 | p95 | Error |
|---|---:|---:|---:|---:|
| Cloudflare, halaman representatif, c1 | 12 | 2.525 dtk | 5.745 dtk | 0 |
| Origin hostname, halaman representatif, c1 | 8 | 2.458 dtk | 3.964 dtk | 0 |
| Origin langsung, halaman representatif, c1 | 12 | 2.214 dtk | 4.165 dtk | 0 |
| Cloudflare, halaman 585, c1 | 4 | 10.563 dtk | 10.643 dtk | 0 |
| Origin langsung, ramp c5 | 4/9 | 8.015 dtk | 13.375 dtk | 5 |
| Origin langsung, ramp c10 | 1/10 | 866 md | 866 md | 9 |
| Cloudflare, c10 | 4/14 | 2.600 dtk | 3.220 dtk | 10 |

Opsi `include_translation=false`, `view=full`, dan `include_audio=true` tidak
menghilangkan kelambatan. TTFB hampir sama dengan waktu total; transfer dan
serialisasi badan respons di trace hanya mengambil kurang dari satu milidetik.
Cloudflare dan origin menunjukkan kelas latensi yang sama, sehingga edge bukan
penyebab utama.

## Bukti akar masalah

Trace aplikasi menempatkan lebih dari 99,9% waktu request pada satu SELECT
hydration Citable Unit. Request contoh halaman 585 menghabiskan 10.251 dtk di
SELECT tersebut dari total 10.253 dtk; kerja non-database sekitar 2 md. Jumlah
query per request konstan, jadi ini bukan N+1.

`pg_stat_statements` untuk statement itu (`queryid=-5311392410561715345`)
mencatat delta 74 panggilan, 628.812 dtk waktu eksekusi, dan 20.449.034 shared
buffer hit: rata-rata 8.497 dtk dan 276.338 buffer hit per panggilan, tanpa
physical read. Saat c10, sepuluh backend PostgreSQL aktif tanpa wait event dan
CPU aplikasi tetap rendah. Bottleneck karena kerja CPU PostgreSQL atas data yang
sudah berada di memori, bukan disk, pool aplikasi, atau serialisasi JSON.

`EXPLAIN (ANALYZE, BUFFERS)` halaman 585 menunjukkan plan lama mengevaluasi
ulang view lisensi campuran untuk setiap binding: `citable_units` di-scan 259
kali, menghasilkan 756.162–756.206 shared hit dan sekitar 16,3 dtk. Plan custom
untuk halaman yang sama masih 81 md/5.405 hit. Halaman kecil pun dapat memilih
pengulangan view yang buruk: halaman 1 memerlukan 4,5–16,5 dtk tergantung plan.

Akar masalah terkonfirmasi adalah bentuk join terhadap
`citable_units_with_effective_license`: planner meratakan view lisensi lintas
korpus dan salah mengestimasi kardinalitas, lalu mengulang evaluasinya per
binding Quran. Index primer Citable Unit, index binding unit, index
ayah-role, dan index halaman sudah ada dan digunakan; menambah index bukan
solusi untuk pengulangan view tersebut.

## Perbaikan

Perbaikan tetap memakai view lisensi kanonik sebagai keputusan publik:

1. View virtual dipisah menjadi cabang non-Quran dan Quran yang saling eksklusif
   dengan `UNION ALL`; kolom dan seluruh aturan lisensinya tetap sama.
2. Hydration melakukan lookup `LATERAL` terikat `unit_id`, `corpus='quran'`,
   dan `effective_license_status='permitted'` dengan `LIMIT 1`.
3. Tidak ada materialized view, cache baru, salinan status lisensi, penghapusan
   pemeriksaan lisensi, atau perubahan response JSON.

Fixture produksi 6.236 ayah/39.875 Citable Unit dengan
`plan_cache_mode=force_generic_plan` menghasilkan p95 hydration lokal 3,06 md
(20 sampel) dan mempunyai sub-gate hydration `<20 md`; perilaku lama sekitar
32 md bahkan pada fixture lokal sehingga gagal. Gate deployment terpisah tetap
menegakkan target end-to-end origin `<200 md`. Migration telah diuji down/up;
jumlah dan hash identitas Quran tidak berubah.

## Invariant publikasi

- Hanya hasil view dengan `effective_license_status='permitted'` yang dapat
  di-hydrate.
- Aturan translation, transliteration, footnote, script restricted, dan script
  grandfather berdasarkan checksum tidak berubah.
- Citable Unit tetap `provenance_class='source'`; Quran tidak pernah menjadi
  interpretive retrieval eligible. RAG Safety tidak dipindahkan ke prompt.
- Anchor dan `primary_unit_id` tetap identitas unit aktif yang sama.
- Peta QPC Hafs v1 tetap 6.236 ayah, halaman 1–604, dengan checksum snapshot
  kanonik yang diuji.

## Kebijakan cache

Keputusan eksplisitnya adalah tetap **bypass/revalidate**:
`Cache-Control: public, max-age=0, must-revalidate` dan
`CF-Cache-Status: DYNAMIC`. Worker produksi wajib menambahkan
`X-Surau-Cache: BYPASS`; dev tidak memiliki route Worker sehingga header itu wajib tidak ada.
Ini mempertahankan pencabutan lisensi secara langsung. Caching spekulatif tidak dipakai untuk
memenuhi target performa.

## Pengukuran dan gate

Jalankan workflow `Quran Page Performance` dengan versi exact:

- `baseline`: mencatat Cloudflare, origin langsung, trace, `pg_stat_statements`,
  runtime, dan `EXPLAIN (ANALYZE, BUFFERS)` tanpa menggagalkan rilis lama.
- `postdeploy`: mengulang matriks yang sama dan menggagalkan run bila origin
  mixed-page c10 atau worst-page c10 memiliki p95 `>=200 md`, response tidak
  memenuhi kontrak, lisensi bukan `permitted`, identitas Citable Unit hilang,
  atau kebijakan cache berubah.

Artefak juga memuat fingerprint jumlah+isi untuk teks Quran, metadata surah,
translation beserta footnote, transliteration, seluruh Quran Citable Unit dan
Anchor, binding, sumber lisensi, serta lisensi efektif. Hash peta halaman dan
sampel availability selama cutover ikut dibandingkan sebelum/sesudah.
