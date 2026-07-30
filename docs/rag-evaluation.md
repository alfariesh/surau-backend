# Evaluasi RAG U-6

Dokumen ini adalah kontrak operator untuk fondasi evaluasi lintas-korpus.
Tahap W3 mengaktifkan 12 skenario; U-6 tetap **parsial** sampai U-1, U-3,
U-4, H-7, dan W-7 mengaktifkan sedikitnya 52 skenario.

## Sumber kebenaran

- `eval/u6/catalog.json` adalah `eval-catalog-v1`: kategori aktif/seed,
  owner, dependensi, profil, kuota, dan status migrasi BookRAG.
- `eval/u6/w3_http.jsonl` berisi tujuh skenario HTTP terhadap korpus dev.
- Lima skenario konstruksi dipetakan dari nama test Go yang dibekukan di
  katalog. Test yang dilewati tidak dihitung lulus; case yang tidak
  menghasilkan hasil membuat gate gagal.
- Laporan mesin memakai `eval-report-v1` dan membawa hash byte-exact katalog.
  Membandingkan atau menggabungkan snapshot katalog berbeda dilarang.

Golden BookRAG lama tetap disimpan dan pemanggilan CLI lama
`rag-eval -cases ...` tetap didukung. ID buku lama yang belum ada di dev tidak
diimpor ulang dan tidak ikut profil W3 aktif.

## Profil dan 12 skenario W3

| Profil | Skenario | Peran |
|---|---:|---|
| `pr` | 5 | dua injeksi-lewat-konten, dua validitas sitasi negatif, satu gerbang konstruksi anti-tafsir Quran |
| `scheduled` | 12 | semua PR ditambah tiga kitab, dua routing QS 2:183 ke tafsir, dan dua not-found |
| `release` | 12 | cakupan sama dengan scheduled, tanpa retry |

Kedua kasus validitas sitasi menolak locator salah (page serta pasangan
unit/Anchor yang tidak resolvable) dan kutipan yang tidak ada di bukti.

Kasus routing QS 2:183 memakai Tafsir as-Sa'di `book_id=42`,
`heading_id=104`, `page_id=123`. Sitasi wajib berupa Citable Unit kitab/tafsir
dan Anchor `quran/...` ditolak. Gerbang konstruksi wajib memakai nama persis
`TestLiveQuranCitableUnitsNeverInterpretiveEligible`.

BookRAG tetap melayani buku melalui tree existing. Katalog W3 membekukan
`serving=tree` dan `tree_retired=false`; perubahan status tanpa artefak parity
dengan SHA commit dan hash katalog yang sama ditolak.

## Kebijakan lulus

Hanya asersi deterministik yang menentukan gate:

- pass-rate seluruh case aktif minimal 90%;
- kategori `content-injection`, `citation-validity`, dan
  `quran-anti-tafsir` wajib 100%;
- kategori aktif tanpa hasil atau di bawah kuota minimum gagal;
- kategori/angka ringkasan yang tidak cocok dengan hasil per-case gagal;
- PR dan rilis memakai `-retries 0`;
- scheduled boleh `-retries 1`, tetapi `first_attempt_failed=true` tetap
  menjadi alarm.

`compare` membandingkan candidate dengan tree pada case ID, profil, dan hash
katalog yang sama. Candidate tidak boleh menghilangkan case, mengubah case
tree yang lulus menjadi gagal, menurunkan pass-rate kategori, menambah
kegagalan pemblokir, atau membuat `citation-validity` kurang dari 100%.
Artefak parity tidak otomatis melakukan reroute.

Mutation-test CI sengaja membuat satu injeksi gagal ketika nilai keseluruhan
masih 91,67%; gate harus keluar non-zero. Mutation lain membuktikan regresi
candidate, reroute tanpa parity, dan pensiun tree tanpa parity juga ditolak.

## Perintah

Pemanggilan lama:

```sh
go run ./cmd/rag-eval -cases eval/bookrag_smoke.jsonl
```

Profil HTTP W3:

```sh
go run ./cmd/rag-eval run \
  -catalog eval/u6/catalog.json \
  -profile scheduled \
  -cases eval/u6/w3_http.jsonl \
  -retries 1 \
  -output json
```

Gabungkan hasil test bernama, tampilkan dashboard, lalu gate:

```sh
go run ./cmd/rag-eval report \
  -catalog eval/u6/catalog.json \
  -profile pr \
  -go-test-json u6-go-test.jsonl \
  -output json > u6-report.json

go run ./cmd/rag-eval report \
  -catalog eval/u6/catalog.json \
  -profile pr \
  -go-test-json u6-go-test.jsonl \
  -output markdown

go run ./cmd/rag-eval gate \
  -catalog eval/u6/catalog.json \
  -report u6-report.json
```

Parity:

```sh
go run ./cmd/rag-eval compare \
  -baseline tree-report.json \
  -candidate candidate-report.json
```

## Judge advisory dan sampling manusia

`POST /v1/eval/judge` hanya menerima token mesin berscope `rag-eval:read`.
Pemanggil tidak dapat mengirim task key, system prompt, atau rubrik bebas.
Server hanya menerima rubrik immutable:

- `groundedness-v1`, aktif pada jawaban positif kitab/tafsir W3;
- `ikhtilaf-v1`, tersedia tetapi baru diaktifkan setelah composer U-3
  menghasilkan struktur ikhtilaf.

Server selalu memanggil task U-0 `rag-judge`. Respons membawa versi dan
SHA-256 rubrik, keputusan/alasan, model, versi prompt, Generation Run, token,
biaya, cache, dan failover. Judge gagal, budget habis, atau token tidak
tersedia dilaporkan `skipped/error`; hasil itu tidak boleh membuat asersi
deterministik yang gagal menjadi lulus.

Sampling manusia dipilih deterministik dari `bulan + case_id`: 20% dari hasil
judge selesai, minimum dua (atau seluruh case bila kurang dari dua). Artefak
JSON memuat pertanyaan, jawaban, kutipan, alasan judge, dan status
`pending_human_review`. Reviewer mengisi hasil di luar laporan immutable;
perbedaan manusia-vs-judge menjadi bahan revisi rubrik baru, bukan perubahan
diam-diam pada `v1`.

## Tahapan CI

- PR: job stabil `runner / rag-eval / U-6 PR gate` menjalankan lima kategori
  keamanan/konstruksi terhadap PostgreSQL sementara dan mutation-test.
- Scheduled: seluruh profil W3 berjalan terhadap dev, menulis dashboard
  Markdown/JSON dan paket sampling. Workflow bukan pemeriksaan PR, tetapi
  kegagalan, retry pertama, atau judge hilang menyalakan alarm Telegram.
- Rilis: sebelum deploy tag, SHA yang sama wajib sudah hidup sebagai
  `dev-<SHA7>`. Profil release tanpa retry, PostgreSQL construction gate,
  threshold 90%, dan semua kategori pemblokir 100% harus lulus.

Tidak ada workflow U-6 yang membuat tag produksi.

## Kategori yang sengaja belum aktif

| Kategori | Minimum | Owner/dependensi aktivasi |
|---|---:|---|
| id↔ar / lintas-korpus | 8 | U-1; seed pertanyaan Indonesia-Arab dan expected Anchor sudah ada |
| validitas struktur | 3 | U-3; menunggu EvidencePack dan composer |
| ikhtilaf completeness | 3 | U-3; kemudian aktifkan `ikhtilaf-v1` |
| lensa-tak-meratakan | 6 | U-4 bergantung U-3; semua kombinasi gaya×madhhab |
| hadith | 10 | H-7; grading per-otoritas, lafaz, da'if, takhrij, eligibility |
| wiki | 10 | W-7; biografi, “menurut ulama X”, dan confusion entitas |

Jumlah minimum adalah 12 + 40 = 52. Jangan menandai U-6 selesai penuh sebelum
seluruh owner tersebut mendarat dan profil aktif mencapai kuota.
