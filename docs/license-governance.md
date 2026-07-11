# Kitab License Governance (B-4)

Last updated: 2026-07-11

Dokumen ini adalah kontrak operasional **License Status** kitab. Keputusan produk yang berlaku
adalah PK-1/O3/O-1B-1: hanya status literal `permitted` yang boleh menghasilkan publikasi baru;
karya yang sudah publik sebelum B-4 tetap tayang selama audit, lalu segera hilang dari seluruh
jalur baca publik bila diputuskan `restricted`.

## Batas Work / Edition saat ini

Registry Work/Edition penuh memang baru dibangun di K-2. Sampai itu tersedia, satu baris `books`
adalah batas **Edition** dan sekaligus batas **Work sementara** untuk keputusan lisensi. Keputusan
ini tidak menebak metadata tahqiq/penerbit yang belum ada; K-2 kelak dapat memindahkan status ke
Work/Edition eksplisit tanpa mengubah vocabulary atau kontrak Citable Unit.

Setiap Edition memiliki salah satu nilai non-null berikut:

| `license_status` | Arti operasional B-4 |
|---|---|
| `unknown` | Belum diaudit; tidak boleh dipublish baru. |
| `needs_review` | Bukti sedang/harus ditinjau; tidak boleh dipublish baru. |
| `permitted` | Satu-satunya status yang membuka publikasi baru. |
| `restricted` | Tidak boleh tampil publik; takedown berlaku segera. |
| `public_domain` | Temuan hukum dicatat, tetapi bukan izin publish otomatis. Kurator tetap harus mengambil keputusan eksplisit `permitted` bila publikasi hendak dibuka. |

`books.license_status` selalu terisi (default aman `unknown`). Alasan, URL bukti, aktor, dan waktu
keputusan disimpan bersama riwayat audit immutable.

## Pewarisan ke Citable Unit

`citable_units.license_status` tetap nullable sebagai override unit. API kurasi memaparkan tiga
field agar tidak ada tebakan di client:

- `license_status`: override unit; `null` berarti mewarisi.
- `effective_license_status`: hasil akhir `COALESCE(unit override, Edition)`.
- `license_source`: `unit_override` atau `edition`.

Dengan begitu perubahan lisensi Edition langsung diwarisi unit yang tidak memiliki override.
Gerbang publik selalu memeriksa Edition/Work lebih dahulu; override `permitted` bukan jalan
belakang untuk membuka Work yang tidak publik. Di dalam Work yang lolos gerbang itu, permukaan
publik berbasis registry (Anchor dan Cross-Reference) hanya mengembalikan unit dengan override
`null` (mewarisi) atau literal `permitted`. Override `unknown`, `needs_review`, `restricted`, dan
`public_domain` disembunyikan per unit. Jika satu heading/page berisi campuran, sibling yang
eligible tetap tampil. Jika buku sudah pernah di-derive tetapi seluruh unit locator tersebut
tidak eligible, resolver tidak boleh mengarang fallback heading/page kasar; fallback hanya untuk
buku yang memang belum pernah di-derive.

Batas B-4 ini sengaja tidak merekonstruksi Reader HTML atau bukti Book RAG dari potongan registry.
Keduanya masih tersusun dari halaman/heading korpus dan migrasi komposisi per-unit adalah K-1.
Sampai K-1 selesai, override unit bukan alat takedown teks Reader/RAG; bila satu potongan wajib
segera tidak tampil di sana, Edition/Work harus ditandai `restricted`. Ini menjaga implementasi
jujur tanpa menebak cara menyambung ulang halaman yang dapat mengubah teks sumber.

## Gerbang publikasi dan grandfathering

Database dan aplikasi sama-sama menjaga jalur berikut:

- katalog kitab: hidden/draft/archived → `published`;
- publish metadata, halaman, dan heading sumber yang akan langsung mengubah karya publik;
- publish production project dan final reader assets;
- re-import Shamela atau import reader asset yang akan langsung mengubah karya publik.

Semua publikasi baru itu menolak status selain `permitted` dengan HTTP `409` dan kode stabil:

```json
{
  "error": "license not permitted",
  "code": "license_not_permitted",
  "request_id": "..."
}
```

Draft tetap boleh disiapkan tanpa mempublikasikannya. Ini memungkinkan audit dan pekerjaan
editorial berjalan paralel, tetapi gerbang terakhir tidak dapat dilewati.

Saat migrasi B-4 pertama kali berjalan, publikasi katalog dan production project yang sudah
`published` mendapat timestamp grandfather. Hanya baris itulah yang boleh tetap terlihat ketika
statusnya masih `unknown`, `needs_review`, atau `public_domain`. Grandfather tidak dibuat untuk
publikasi baru, berakhir ketika konten di-unpublish/diarsipkan, dan tidak pernah mengalahkan
`restricted`.

Seluruh reader, Book RAG, Anchor, Cross-Reference, rujukan Quran↔kitab, serta proyeksi data personal
memakai satu view `public_book_publications`. Karena itu takedown tidak bergantung pada setiap query
mengingat aturan lisensi secara terpisah.

## Laporan cakupan untuk operator

Endpoint berikut membutuhkan `CapReviewEditorial` (editor, scholar reviewer, atau admin sesuai
matriks policy):

```http
GET /v1/editorial/license-audit?status=unresolved&limit=50&offset=0
GET /v1/editorial/books/{book_id}/license
```

Filter `status` menerima `unresolved` (default: `unknown` + `needs_review`), kelima status literal,
atau `all`. Bentuk laporan:

```json
{
  "items": [
    {
      "book_id": 797,
      "book_title": "...",
      "license_status": "unknown",
      "grandfathered": true,
      "registered_reader_count": 120,
      "saved_item_count": 35,
      "last_activity_at": "2026-07-11T00:00:00Z",
      "updated_at": "2026-07-11T00:00:00Z"
    }
  ],
  "total": 900,
  "counts": {
    "total": 1000,
    "unresolved": 900,
    "unknown": 850,
    "needs_review": 50,
    "permitted": 80,
    "restricted": 10,
    "public_domain": 10,
    "grandfathered": 700
  },
  "generated_at": "2026-07-11T00:00:00Z"
}
```

Antrean mendahulukan karya grandfathered, lalu jumlah **pembaca terdaftar**, jumlah saved item,
aktivitas terakhir, dan ID. Backend belum memiliki hit counter pembaca anonim, sehingga laporan
tidak mengarang metrik trafik yang tidak tersedia.

Perubahan keputusan membutuhkan `CapPublishProduction`, MFA segar, alasan, dan `If-Match` dari
GET sebelumnya:

```http
PATCH /v1/editorial/books/797/license
If-Match: W/"..."
Content-Type: application/json

{
  "license_status": "permitted",
  "reason": "Izin penerbit diterima dan diverifikasi",
  "evidence_url": "https://example.org/evidence"
}
```

Header hilang menghasilkan `428 if_match_header_required`; keputusan basi menghasilkan
`412 precondition_failed`. `If-Match: *` hanya untuk keputusan unconditional yang disengaja.

## Cache dan perubahan status

Prefix publik `/v1/books`, `/v1/anchors`, dan `/v1/cross-references` selalu melewati cache L1/KV
Worker. Origin menjawab `Cache-Control: public, max-age=0, must-revalidate` dengan ETag. Client
boleh menyimpan body untuk conditional request, tetapi wajib memvalidasi ulang sebelum memakai
salinan itu. Dengan demikian salinan stale tidak dapat menghidupkan kembali karya `restricted`.

## Terjemahan mesin

B-4 tidak mengubah PK-1/O-4-4: aset `generated` yang sudah publik tetap tampil dengan label
Provenance Class/review yang benar. Reader dan RAG sengaja berbeda: Book RAG hanya memakai aset
final yang `provenance_class=source` atau yang status akhirnya sudah `reviewed`
(`translation_status` untuk katalog/terjemahan, `summary_status` untuk ringkasan). Karena status
review adalah dimensi terpisah, aset `machine + reviewed` tetap berkelas `machine` dan boleh masuk
retrieval; aset `machine + generated` tetap dikecualikan dari metadata prompt, tree/summary,
pencarian leksikal, dan bukti halaman. License Status dan Provenance Class adalah dua dimensi
berbeda.
