# Kontrak normalisasi Arab `search-key` v1

Last updated: 2026-07-11

Dokumen ini adalah kontrak normatif B-5 untuk semua teks turunan yang dipakai sebagai kunci
search/linking. Teks hasil normalisasi tidak pernah menjadi teks tampilan atau bukti kutipan.
Sumber kebenaran implementasi adalah `internal/quranutil.NormalizeKey`; paket
`internal/searchtext` adalah satu-satunya jalur resmi untuk menulis turunannya ke PostgreSQL.

## Identitas profil

```text
ProfileName    = "search-key"
ProfileVersion = 1
```

Semantik v1 dibekukan sebagai berikut:

- hapus tatweel dan tanda Quran/harakat pada rentang `U+0610..U+061A`, `U+064B..U+065F`,
  `U+0670`, dan `U+06D6..U+06ED`;
- lipat `أ|إ|آ|ٱ -> ا`, `ى -> ي`, `ؤ -> و`, dan `ئ -> ي`;
- pertahankan `ء` dan `ة`; keduanya sengaja tidak dilipat;
- pertahankan semua huruf dan digit menurut tabel Unicode 15.0 yang dipin dari Go, termasuk
  Latin dan angka Arab;
- ubah tanda baca, simbol, emoji, dan pemisah lain menjadi spasi;
- rapikan seluruh whitespace menjadi satu spasi serta hapus spasi awal/akhir;
- pertahankan kapitalisasi Latin dan jangan melakukan normalisasi Unicode tambahan.

Hasilnya idempoten: menormalisasi hasil v1 sekali lagi harus menghasilkan byte yang sama.
Perilaku query-time lama yang mungkin melipat `ء` atau `ة` bukan profil persisten dan tidak boleh
disalin ke writer baru.

## Korpus emas Go-Python

Satu korpus bersama berada di
`internal/quranutil/normalization_v1_vectors.json`. Korpus ini mencakup hamzah, tatweel, seluruh
rentang harakat, tanda baca Arab/Latin, simbol/emoji, campuran Latin-Arab, digit, whitespace,
dan input kosong.

Go (`internal/quranutil/normalize_test.go`) dan Python
(`scripts/langextract_kg/test_arabic_normalize.py`) membaca file yang sama. Klasifikasi
huruf/digit v1 juga berasal dari satu tabel bersama
`internal/quranutil/normalization_v1_unicode_ranges.json`, bukan versi Unicode runtime.
Pengujian exhaustive memeriksa seluruh code point `U+000000..U+10FFFF`, selain korpus contoh.
Job CI `normalization-contract` mewajibkan kesetaraan 100 persen di kedua runtime.

Perubahan output v1 dilarang. Semantik baru harus diperkenalkan sebagai fungsi dan korpus v2,
menaikkan `ProfileVersion`, serta membawa rencana re-normalisasi data. Jangan mengganti checksum
v1 untuk membuat perubahan semantik terlihat lulus. Gerbang CI membandingkan byte seluruh
artefak v1 dengan merge-base PR; perubahan checksum dan fixture di PR yang sama tetap ditolak.
Selector aktif hanya dapat berpindah ke v2 bila versi naik tepat satu dan seluruh artefak v2
baru tersedia.

Python mempunyai dua fungsi dengan tujuan berbeda:

- `normalized_key` memilih implementasi aktif `search_key_v1` dan dipakai untuk nilai turunan
  persisten.
- `normalized_grounding_key` mempertahankan kemampuan memetakan hasil kembali ke span sumber.
  Fungsi grounding bukan profil search dan tidak boleh menggantikan `normalized_key`.

## Kolom turunan ber-versi

Writer resmi menulis teks dan versi dalam operasi yang sama:

| Tabel | Teks turunan | Kolom versi |
|---|---|---|
| `authors` | `name_search` | `name_search_normalization_version` |
| `quran_book_references` | `normalized_text` | `normalization_version` |
| `quran_cross_reference_bridge` | `normalized_text` | `normalization_version` |
| `knowledge_mentions` | `normalized_text` | `normalization_version` |
| `knowledge_entities` | `normalized_name_ar` | `normalization_version` |
| `knowledge_entity_aliases` | `normalized_alias` | `normalization_version` |
| `citable_units` | `text_normalized` | `normalization_version` |
| `cross_references` | `evidence_normalized` | `normalization_version` |

Trigger database menolak insert baru atau perubahan teks turunan tanpa versi. Trigger juga
menolak versi tanpa teks. Update row legacy yang tidak menyentuh pasangan teks-versi tetap
diperbolehkan, sehingga deploy expand tidak memutus pembacaan data lama.

`NULL` pada kolom versi berarti **legacy yang belum terbukti**, bukan v1. Jangan mengisi `1`
hanya karena output lama tampak serupa. Khususnya, data knowledge yang dahulu dinormalisasi oleh
Python tetap `NULL` sampai benar-benar dinormalisasi ulang dengan kontrak bersama.

`quran_ayahs.search_text` tidak termasuk tabel di atas. Nilai itu adalah teks sumber Imlaei/simple
dari impor Quran, bukan hasil `NormalizeKey`, sehingga tidak diberi versi profil ini.

## Backfill F1-H

Dua job resumable hanya memberi cap v1 setelah memverifikasi asal dan hasilnya:

```sh
/backfill -job=authors-name-search-v1-version
/backfill -job=quran-references-normalization-v1
```

`authors-name-search-v1-version` menghitung ulang nama dengan Go v1. Nilai legacy yang berbeda
menggagalkan chunk tanpa menulis sebagian; nilai `NULL` dibuat dan dicap v1 secara atomik.

`quran-references-normalization-v1` berjalan per Work dan memverifikasi
`quran_book_references` beserta `quran_cross_reference_bridge` sebelum memberi cap. Satu drift
menggagalkan seluruh Work. Job dapat diulang untuk resume melalui checkpoint `backfill_jobs`.

Setelah selesai, verifikasi status `completed`, `pending_rows=0`, dan smoke endpoint Quran
reference lama. Row Python legacy yang belum diproses bukan kegagalan backfill; `NULL` adalah
penandaan yang jujur sampai re-normalisasi khusus tersedia.

## Kontrak API legacy

`BookQuranReference.normalization_version` ditambahkan secara aditif. Client harus menerima
angka `1` untuk row yang terverifikasi dan `null` untuk row legacy yang belum dapat dibuktikan.
Field lama, envelope `{items,total}`, filter, dan aturan approved-only tidak berubah.
