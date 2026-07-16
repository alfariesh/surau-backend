# Profil kinerja Quran page reader

Runbook ini khusus untuk kontrak publik:

```text
GET /v1/quran/pages/{page_number}/ayahs?view=reader_minimal
```

Tujuannya bukan membuat angka benchmark terlihat bagus. Durasi setiap sampel berakhir setelah
body respons selesai dibaca, bukan ketika header pertama tiba. Setiap sampel juga memvalidasi isi
respons: jumlah ayah QPC, `page_number`, identitas UUID dan Anchor kanonik untuk seluruh Citable
Unit (termasuk terjemahan, transliterasi, dan catatan kaki), hubungan catatan kaki ke induknya,
keunikan `ayah_key`, dan `license_status=permitted`. Respons yang cepat tetapi kehilangan salah
satu invariant tersebut adalah kegagalan.

## Menjalankan workflow

Jalankan GitHub Actions **Quran Page Performance** dari branch default dengan tiga input:

- `environment`: `dev` atau `prod`;
- `phase`: `baseline` untuk bukti sebelum perubahan, `postdeploy` untuk bukti sesudah perubahan
  sekaligus gerbang p95;
- `expected_version`: nilai persis dari `/version`, misalnya `dev-ba1c142` atau `0.4.3`.

Job memakai GitHub Environment yang sama dengan deploy. Profil produksi karena itu tetap
memerlukan approval operator dan memakai SSH host key yang sudah dipin. Workflow tidak menerima
perintah shell, SQL, URL, halaman, jumlah request, atau concurrency dari pengguna.

Artefak disimpan 30 hari. Kegagalan request, timeout, execution plan, atau gerbang performa tetap
dikemas dan diunggah sebelum job dinyatakan gagal, sehingga kasus buruk tidak menghilangkan
buktinya. Isinya hanya timing, header cache, execution plan, statistik resource,
jumlah/checksum data, dan profile stack bila `perf` sudah tersedia di host. Isi teks Quran,
credential, `.env.production`, dan body respons tidak disimpan.

## Batas keselamatan

`cmd/quran-page-bench` menolak konfigurasi di luar batas berikut, termasuk ketika dipanggil di
luar workflow:

- concurrency maksimum 10;
- total measured + warm-up maksimum 200 request per invocation;
- durasi invocation maksimum 60 detik;
- response maksimum 4 MiB;
- abort pada timeout, non-200, response invalid, Citable Unit hilang, atau lisensi non-permitted.

Script origin memeriksa resource sebelum setiap tahap. Profil berhenti bila koneksi database
mencapai 40 dari konfigurasi maksimum 50 atau CPU service API sudah mencapai 85%. Tidak ada
`pg_stat_statements_reset`, DDL, `ANALYZE`, `VACUUM`, mutasi data, atau cache flush.

`EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)` berjalan di transaksi `READ ONLY` dengan
`statement_timeout=20s`, `lock_timeout=250ms`, lalu `ROLLBACK`.

Ramp c10 juga mengambil snapshot `pg_stat_activity` dan resource container sekali per detik
(maksimum 20 snapshot). Ini membedakan waktu eksekusi database dari antrean pool aplikasi.
Hanya konfigurasi runtime non-rahasia yang dicatat: ukuran pool, batas CPU/memori, mode prefork,
dan status/sampling tracing.

## Matriks tetap

Halaman representatif dipilih dari frozen QPC Hafs map:

| Halaman | Ayah | Alasan |
|---:|---:|---|
| 48 | 1 | jumlah minimum |
| 1 | 7 | pembuka dan smoke kontrak deploy |
| 421 | 8 | sekitar median |
| 585 | 42 | jumlah maksimum/worst case |
| 604 | 15 | batas akhir mushaf |

Baseline membandingkan reader default, tanpa terjemahan untuk Arab/Indonesia, full view, audio,
serta ramp concurrency 1, 2, 5, dan 10. Jalur yang diukur terpisah:

1. `api.surau.org`, melalui Cloudflare Worker;
2. `origin-api.surau.org`, melewati Worker cache tetapi masih melalui jaringan Cloudflare;
3. active application slot pada `127.0.0.1` di VPS.

Target `<200 ms` berlaku pada jalur ketiga. Jalur Cloudflare tetap dilaporkan agar biaya jaringan
dan kebijakan edge terlihat jelas.

## Kebijakan cache yang dikunci

Semua Quran GET tetap `X-Surau-Cache: BYPASS`, `CF-Cache-Status: DYNAMIC`, dan
`Cache-Control: public, max-age=0, must-revalidate`. Benchmark memeriksa ketiga header tersebut
pada jalur Cloudflare dan `Cache-Control` pada origin. Keputusan ini disengaja: pencabutan lisensi
harus langsung berlaku dan tidak boleh menunggu objek edge kedaluwarsa. Conditional ETag masih
boleh menghasilkan `304`, tetapi request tetap mencapai origin.

Optimasi endpoint tidak boleh mengubah kebijakan ini tanpa keputusan produk dan audit lisensi
terpisah.

## Membaca bukti

- `public-*.json`: timing dari runner ke URL publik Cloudflare.
- `origin-hostname-*.json`: timing ke hostname origin yang masih diproksi Cloudflare.
- `quran-page-origin.tar.gz`: raw-origin timing, runtime snapshot, `pg_stat_statements` sebelum
  dan sesudah, index/table statistics, checksum corpus, execution plans halaman 1/585, dan profile
  CPU bila tersedia. Hingga 30 trace Tempo yang dirujuk oleh `X-Trace-ID` ikut disimpan untuk
  memecah durasi HTTP, antrean antar-query, dan durasi query pgx; trace yang belum sempat diekspor
  dicatat eksplisit sebagai missing.
- `content_hashes`: hash SHA-256 body per halaman. Dalam satu snapshot data, lebih dari satu hash
  untuk halaman/query yang sama berarti isi respons berubah selama pengukuran dan perbandingan
  harus diulang.

Akar penyebab hanya boleh dinyatakan bila critical-path trace dan minimal satu bukti independen
(execution plan/`pg_stat_statements` atau CPU profile) menunjuk komponen yang sama. Setelah fix,
`postdeploy` wajib lulus p95 `<200 ms` untuk 70 request campuran dan 70 request halaman 585 pada
concurrency 10, dengan nol error.
