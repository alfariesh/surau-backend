# Kontrak grammar Anchor dan resolusi (Fase 1B / B-2 + Q-2)

Last updated: 2026-07-12

Dokumen ini adalah kontrak normatif untuk **Anchor** dan endpoint publik
`GET /v1/anchors/resolve`. Anchor adalah alamat logis lintas-korpus; **Citable Unit** adalah
potongan konten yang alamat itu tunjuk. Grammar dan perilaku di bawah bersifat aditif terhadap
API reader yang sudah ada: `ayah_key`, `toc-{heading_id}`, dan page tetap sah selamanya, bukan
jalur deprecation.

## Grammar kanonik

Notasi EBNF berikut adalah sumber kebenaran sintaks B-2:

```ebnf
anchor        = point | range ;
range         = point, "..", point ;
point         = quran-surah | quran-ayah | quran-unit | kitab-work | kitab-heading | kitab-unit ;

quran-surah   = "quran/", positive-int ;
quran-ayah    = quran-surah, ":", positive-int ;
quran-unit    = quran-ayah, "/u/", positive-int ;
kitab-work    = "kitab/", positive-int ;
kitab-heading = kitab-work, "/h/", positive-int ;
kitab-unit    = kitab-work, "/h/", heading-scope, "/u/", positive-int ;

heading-scope = "0" | positive-int ;
positive-int  = nonzero-digit, { digit } ;
nonzero-digit = "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" ;
digit         = "0" | nonzero-digit ;
```

Aturan byte-level:

- Seluruh Anchor maksimal **512 byte**, case-sensitive, dan hanya memakai ASCII lowercase sesuai
  token grammar. Spasi, control character, Unicode, percent-encoding, query string, dan fragment
  bukan bagian Anchor.
- Angka ditulis desimal tanpa tanda dan tanpa leading zero. Nilai `0` hanya sah sebagai
  `heading-scope` unit front-matter; `book_id`, `heading_id`, surah, ayah, dan `ordinal` selalu
  positif.
- Komponen angka berada pada rentang integer PostgreSQL: `1..2147483647` (atau
  `0..2147483647` khusus `heading-scope` front-matter). Nilai di luarnya ditolak.
- Parser memvalidasi bentuk; resolver lalu memvalidasi apakah Work dan locator itu benar-benar ada.
  Contoh: grammar menerima angka surah positif, tetapi `quran/115:1` tidak terselesaikan karena
  tidak ada pada korpus Quran.
- Formatter selalu menghasilkan tepat satu serialisasi di atas. Anchor yang sudah dicetak tidak
  pernah dinormalisasi ulang, didaur ulang, atau dipindahkan ke konten berbeda.

### Profil aktif

| Profil | Contoh | Semantik |
|---|---|---|
| Quran surah | `quran/73` | Surah 73 sebagai target logis tanpa menganggapnya rujukan ke setiap ayah. |
| Quran ayah | `quran/73:4` | Ayah 4 dalam surah 73; Quran adalah satu Work implisit. |
| Quran Citable Unit | `quran/73:4/u/2` | Rendering/footnote bernomor stabil di bawah ayah 73:4. |
| Kitab Work | `kitab/797` | Karya kitab dengan `book_id=797`. |
| Kitab heading | `kitab/797/h/11` | Heading logis 11 dalam kitab 797. |
| Kitab Citable Unit | `kitab/797/h/11/u/42` | Unit ordinal 42 di bawah heading 11. |
| Kitab front-matter unit | `kitab/797/h/0/u/3` | Unit ordinal 3 sebelum heading pertama. |
| Range | `quran/73:4..quran/73:10` | Dua batas logis; bukan daftar ayah 4–10. |

`page_id`, `printed_page`, dan `part` adalah locator fisik sekunder. Nilai itu dapat membantu
tampilan atau sitasi akademik, tetapi tidak pernah menjadi identitas kanonik.

### Invarian range

- Kedua batas ditulis sebagai point lengkap; shorthand seperti `quran/73:4..10` tidak sah.
- Kedua batas wajib berada dalam korpus dan Work yang sama. Dua Anchor kitab harus memiliki
  `book_id` sama. Range Quran wajib memakai dua boundary ayah dalam surah yang sama; Anchor
  surah tidak dapat menjadi boundary range.
- Pada range Quran, ayah akhir tidak boleh mendahului ayah awal.
- Resolver menyelesaikan **boundary saja**: `boundaries[0]` berperan `start` dan
  `boundaries[1]` berperan `end`. Endpoint tidak mengekspansi atau mengembalikan semua unit di
  antaranya.
- Range kitab boleh memakai profil point berbeda dalam Work yang sama karena resolusi hanya
  menyatakan dua batas, bukan menjanjikan operasi pengurutan atau ekspansi di antara keduanya.

## Endpoint publik

```http
GET /v1/anchors/resolve
```

Endpoint tidak memerlukan autentikasi. Query harus cocok dengan **tepat satu** bentuk berikut:

| `requested.form` | Query | Contoh | `canonical_anchor` hasil |
|---|---|---|---|
| `canonical` | `anchor=<canonical point/range>` tanpa scope legacy | `?anchor=kitab%2F797%2Fh%2F11%2Fu%2F42` | Anchor kanonik yang sama. |
| `legacy_ayah_key` | `anchor=<surah>:<ayah>` | `?anchor=73%3A4` | `quran/73:4`. |
| `legacy_toc` | `anchor=toc-<heading_id>&book_id=<book_id>` | `?anchor=toc-11&book_id=797` | `kitab/797/h/11`. |
| `legacy_page` | `book_id=<book_id>&page_id=<page_id>` tanpa `anchor` | `?book_id=797&page_id=12` | `null`, karena page fisik bukan Anchor. |
| `legacy_quran_surah` | `surah_id=<surah>` | `?surah_id=73` | `quran/73`. |
| `legacy_quran_range` | `surah_id`, `from_ayah_number`, `to_ayah_number` | `?surah_id=73&from_ayah_number=1&to_ayah_number=4` | `quran/73:1..quran/73:4`. |
| `legacy_quran_juz` | `juz_number=<1..30>` | `?juz_number=29` | `null`; dua boundary ayah dikembalikan. |
| `legacy_quran_hizb` | `hizb_number=<1..60>` | `?hizb_number=57` | `null`; dua boundary ayah dikembalikan. |
| `legacy_quran_page` | `page_number=<positive>` | `?page_number=574` | `null`; dua boundary ayah dikembalikan. |

`book_id`, `page_id`, dan semua angka dalam legacy query harus positif dan tanpa bentuk ambigu.
Contoh yang ditolak: query kosong, `toc-11` tanpa `book_id`, canonical Anchor dengan `book_id`
tambahan, `anchor` bersama `page_id`, page dengan salah satu scope hilang, parameter duplikat, atau
nama parameter di luar sembilan parameter kontrak. Keluarga locator Quran tidak boleh dicampur
dengan `anchor`/locator kitab atau satu sama lain. Legacy mapping adalah kontrak permanen agar tautan
FE lama tetap dapat dibuka; FE baru sebaiknya menyimpan Anchor kanonik.

## Bentuk respons

Respons sukses adalah satu objek ringkas, bukan HTTP redirect. Hal ini disengaja karena satu page
atau satu unit yang terpecah dapat memiliki lebih dari satu tujuan aktif.

```ts
type AnchorResolutionResponse = {
  requested: {
    form:
      | "canonical"
      | "legacy_ayah_key"
      | "legacy_toc"
      | "legacy_page"
      | "legacy_quran_surah"
      | "legacy_quran_range"
      | "legacy_quran_juz"
      | "legacy_quran_hizb"
      | "legacy_quran_page";
    anchor?: string;
    book_id?: number;
    page_id?: number;
    surah_id?: number;
    from_ayah_number?: number;
    to_ayah_number?: number;
    juz_number?: number;
    hizb_number?: number;
    page_number?: number;
  };
  canonical_anchor: string | null;
  boundaries: AnchorBoundary[];
};

type AnchorBoundary = {
  role: "point" | "start" | "end";
  canonical_anchor: string | null;
  status: "active" | "superseded" | "tombstoned";
  active_targets: AnchorTarget[];
  redirect_chain: AnchorRedirect[];
};

type AnchorTarget = {
  target_type: "citable_unit" | "quran_surah" | "quran_ayah" | "book" | "book_heading" | "book_page";
  corpus: "kitab" | "quran";
  canonical_anchor?: string;
  unit_id?: string;
  primary_unit_id?: string;
  primary_unit_anchor?: string;
  book_id?: number;
  heading_id?: number;
  page_id?: number;
  surah_id?: number;
  ayah_key?: string;
  navigation_url: string;
  updated_at: string;
};

type AnchorRedirect = {
  from: string;
  to: string;
  reason: "edit" | "content_move" | "legacy_alias" | "legacy_page";
  depth: number;
};
```

Field opsional yang tidak relevan boleh dihilangkan. Respons tidak membawa teks atau HTML; FE
mengikuti `navigation_url` atau memanggil endpoint reader terkait untuk mengambil isi.

Untuk point, `boundaries` berisi satu item ber-role `point`. Untuk range, top-level
`canonical_anchor` adalah Anchor range lengkap dan `boundaries` berisi tepat dua item terurut
`start`, lalu `end`. Untuk legacy page, top-level dan boundary `canonical_anchor` bernilai `null`.

Contoh unit lama yang telah terpecah:

```json
{
  "requested": {
    "form": "canonical",
    "anchor": "kitab/797/h/11/u/42"
  },
  "canonical_anchor": "kitab/797/h/11/u/42",
  "boundaries": [
    {
      "role": "point",
      "canonical_anchor": "kitab/797/h/11/u/42",
      "status": "superseded",
      "active_targets": [
        {
          "target_type": "citable_unit",
          "corpus": "kitab",
          "canonical_anchor": "kitab/797/h/11/u/57",
          "unit_id": "d13a09ca-4060-53b2-8d9f-d7459b2fd7ad",
          "book_id": 797,
          "heading_id": 11,
          "page_id": 12,
          "navigation_url": "/v1/books/797/toc/11/read",
          "updated_at": "2026-07-10T03:00:00Z"
        }
      ],
      "redirect_chain": [
        {
          "from": "kitab/797/h/11/u/42",
          "to": "kitab/797/h/11/u/57",
          "reason": "edit",
          "depth": 1
        }
      ]
    }
  ]
}
```

## Semantik resolusi

### Canonical dan legacy

- `quran/{surah_id}` menyelesaikan baris `quran_surahs` aktif. Targetnya bertipe
  `quran_surah`, membawa `surah_id`, dan memakai detail surah sebagai `navigation_url`.
- `quran/{ayah_key}` dan legacy `ayah_key` menyelesaikan baris Quran aktif yang sama. Targetnya
  bertipe `quran_ayah`, membawa `ayah_key`, dan memakai detail ayah sebagai `navigation_url`.
  Setelah Q-2 selesai di ayah itu, target juga membawa `primary_unit_id` dan
  `primary_unit_anchor`; fallback ayah legacy tetap resolvable selama backfill berlangsung.
- `quran/{ayah_key}/u/{ordinal}` memakai walker lineage B-1/B-2 yang sama. Target aktif bertipe
  `citable_unit`, membawa `unit_id`, dan menavigasi ke `/v1/quran/ayahs/{ayah_key}`. Unit lama
  yang superseded mengikuti penerusnya; sumber non-permitted atau rendering stale gagal tertutup.
- Locator `surah_id`, range ayah, juz, hizb, dan halaman mushaf diproyeksikan menjadi satu point
  atau dua boundary ayah. Aggregate tidak mencetak Anchor baru dan tidak mengekspansi isi.
- `kitab/{book_id}` menyelesaikan Work publik aktif ke target `book`.
- `kitab/{book_id}/h/{heading_id}` dan legacy `toc-{heading_id}` menyelesaikan heading logis.
  Bila registry B-1 sudah tersedia untuk heading tersebut, `active_targets` berisi seluruh
  Citable Unit aktif yang license-eligible dalam urutan dokumen; jika bukunya belum pernah
  di-derive, resolver mengembalikan fallback `book_heading` aktif. Buku derived dengan seluruh
  unit terfilter mengembalikan target kosong, bukan fallback kasar.
- Unit kitab aktif mengembalikan dirinya sebagai satu target `citable_unit` hanya bila override
  lisensinya `NULL` (mewarisi) atau `permitted`.
- `navigation_url` unit memakai reader kasar yang sudah ada: route heading untuk `heading_id>0`,
  atau route page untuk unit front-matter. Presisi unit tetap dibawa oleh `canonical_anchor` dan
  `unit_id`; tidak ada fragment DOM rekaan yang belum didukung FE.
- Legacy page mengembalikan seluruh Citable Unit aktif yang license-eligible pada page tersebut
  dalam urutan dokumen; sibling non-eligible tidak menyembunyikan sibling eligible.
  Buku yang belum masuk pilot/derivasi B-1 tetap resolvable melalui satu fallback `book_page`.
- Urutan target deterministik: urutan dokumen saat ini, lalu Anchor sebagai tie-breaker stabil.
  Hasil fan-out yang bertemu kembali pada target sama di-deduplicate.

### Lifecycle dan redirect DAG

`status` adalah lifecycle point **yang diminta**, bukan ringkasan target akhirnya:

- `active`: point masih aktif; target aktif dikembalikan langsung. Canonical point aktif mempunyai
  `redirect_chain` kosong; input legacy dapat mempunyai edge pemetaan sintetis di bawah.
- `superseded`: resolver menelusuri seluruh directed acyclic graph (DAG) lineage sampai semua
  penerus `active` yang license-eligible. Split 1→N, merge N→1, dan multi-hop didukung; edge yang
  menyentuh unit non-eligible tidak dipaparkan pada `redirect_chain` publik.
- `tombstoned`: alamat pernah sah dan tidak didaur ulang. Resolver tetap menjawab `200`; target
  aktif dan redirect dapat kosong bila konten memang tidak mempunyai penerus.

`redirect_chain` memuat setiap edge intermediate secara deterministik, bukan hanya pasangan awal
dan ujung. `depth=1` adalah edge keluar langsung dari unit yang diminta; depth bertambah per hop.
Cycle menandakan korupsi registry: resolver gagal tertutup dengan `500 internal_server_error`, tidak
mengembalikan hasil parsial, dan audit Citable Unit melaporkannya sebagai pelanggaran
`lineage_cycle` agar alarm operasional menyala.

Nilai `reason` membedakan asal edge:

- `edit` dan `content_move` adalah edge persisted pada DAG Citable Unit.
- `legacy_alias` adalah edge sintetis depth 1 dari `73:4` → `quran/73:4`, atau dari `toc-11` →
  `kitab/{book_id}/h/11`.
- `legacy_page` adalah edge sintetis depth 1 dari locator non-Anchor
  `page:{book_id}:{page_id}` menuju setiap Anchor unit kanonik pada page. Jika page hanya dapat
  memakai fallback `book_page` tanpa Anchor, chain kosong.
- Bila pemetaan legacy dilanjutkan oleh lineage persisted, depth edge lineage digeser satu tingkat
  sehingga urutan hop tetap jujur.

Heading, page, Work, dan ayah yang aktif memakai `status=active`. Lifecycle
`superseded|tombstoned` saat ini berasal dari Citable Unit registry.

## Visibility, errors, dan cache

- Resolver memakai gerbang visibility endpoint reader yang sudah ada. Anchor logis surah/ayah
  tetap menjadi alamat struktural permanen untuk parity rujukan lama; Anchor anak Citable Unit
  Quran wajib current dan license-eligible. Kitab unpublished/deleted tidak dapat ditemukan.
  Endpoint ini tidak boleh menjadi jalur samping untuk mengetahui konten tersembunyi.
- Anchor berformat benar tetapi tidak dikenal, locator legacy tidak dikenal, atau target di luar
  visibility publik menjawab envelope standar `404` dengan code `anchor_not_found` dan message
  `anchor not found`.
- Bentuk/angka/query yang tidak sah atau ambigu menjawab envelope standar `400` dengan code
  `invalid_anchor` dan message `invalid anchor`.
- Error internal, termasuk cycle lineage, memakai envelope standar `500 internal_server_error`.
  Client harus bercabang pada HTTP status atau `code`, bukan teks `message`.
- Respons sukses memakai weak body-hash `ETag` dan
  `Cache-Control: public, max-age=0, must-revalidate`; `Last-Modified` juga dikirim ketika respons
  memiliki waktu pembaruan target. Browser boleh menyimpan respons untuk conditional request,
  tetapi wajib memvalidasi ulang ke origin sebelum memakainya. `If-None-Match` yang cocok mendapat
  `304`.
- Seluruh prefix `/v1/anchors` **tidak** masuk cache L1/KV Cloudflare Worker. Setiap request mencapai
  gerbang visibility backend sehingga perubahan lineage atau lisensi tidak tertahan salinan edge
  lama.

## SLO p95 ≤50ms

Budget B-2 diukur pada HTTP end-to-end lokal: request klien test → Fiber handler → parser →
usecase → PostgreSQL → serialisasi respons. Internet, TLS publik, Cloudflare, dan waktu render FE
tidak dihitung.

Gerbang performa deterministik wajib:

1. Seed sedikit di atas pilot nyata, minimal **20.000 Citable Unit**, termasuk active langsung,
   lineage multi-hop/split, heading, page, dan Quran.
2. Lakukan 50 request warm-up, lalu sedikitnya 500 request HTTP keep-alive dengan campuran canonical
   aktif, legacy `ayah_key`, legacy toc, legacy page, range, dan lineage.
3. Urutkan seluruh durasi sampel; nearest-rank p95 adalah sampel pada indeks
   `ceil(0,95 × N)` (1-based). Test gagal bila p95 **lebih besar dari 50ms** dan melaporkan N, p50,
   p95, serta maksimum.
4. Simpan pemeriksaan `EXPLAIN (ANALYZE, BUFFERS)` per kelas lookup. Rencana query harus memakai:
   `uq_citable_units_anchor` untuk canonical unit, UNIQUE `quran_ayahs(ayah_key)` untuk Quran,
   `book_headings_pkey` serta salah satu indeks ekuivalen `book_pages_pkey`/
   `idx_book_pages_book_page` untuk fallback legacy, indeks
   `idx_citable_units_scope_position` untuk heading, `idx_citable_units_book_page` untuk page,
   dan `citable_unit_lineage_pkey` untuk traversal. Sequential scan besar pada jalur lookup adalah
   kegagalan review, bukan alasan menaikkan budget.

Bukti ratifikasi B-2 pada 2026-07-10: workload **20.500 unit aktif**, 50 warm-up, dan 500 sampel
HTTP keep-alive campuran menghasilkan p50 **0,952 ms**, p95 **1,277 ms**, dan maksimum
**3,535 ms**. Fixture juga memuat distribusi lineage historis terpisah agar `EXPLAIN` membuktikan
penggunaan indeks pada graph katalog, bukan hanya pada graph uji yang sangat kecil.

## Namespace yang dicadangkan

Prefix `hadith/`, `wiki/`, dan `entity/` dicadangkan agar tidak dipakai untuk arti lain. Profilnya
belum aktif pada B-2 dan akan mendapat grammar locator sendiri pada fase domain terkait:

- `hadith` kelak memuat Work/Edition koleksi dan locator hadith logis.
- `wiki` kelak menunjuk Citable Unit artikel/klaim.
- `entity` kelak menunjuk identitas entitas pengetahuan; ia sengaja berbeda dari Citable Unit
  `wiki` agar “siapa/apa konsepnya” tidak tercampur dengan “potongan tulisan tentangnya”.

Sampai profil itu diratifikasi, resolver menolak bentuknya sebagai `400 invalid_anchor`; prefix
tersebut tidak boleh dicetak oleh fitur lain.
