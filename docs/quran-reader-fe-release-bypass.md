# Quran Reader FE Release Bypass

> **Status:** keputusan operasional sementara  
> **Diputuskan oleh:** Salman  
> **Tanggal:** 2026-07-22  
> **Scope:** frontend Quran reader saja; dokumen ini tidak mengubah kontrak backend, data, atau
> kebijakan lisensi permanen.

## Tujuan

Membuka jalur konsumsi API produksi untuk rilis awal frontend Quran reader tanpa menjadikan semua
pekerjaan editorial dan audit sebagai blocker. Rilis tetap harus dihentikan untuk masalah yang
berdampak langsung pada kebenaran teks Quran, keamanan, atau reader yang tidak dapat digunakan.

## Keputusan sementara

Frontend **boleh dirilis** dengan data yang saat ini sudah dapat dikonsumsi dari
`https://api.surau.org`, termasuk:

- teks Arab Quran;
- terjemahan Indonesia Kemenag berstatus `permitted`;
- transliterasi Latin Kemenag berstatus `permitted`;
- navigasi surah, ayat, juz, hizb, dan halaman;
- daftar recitation yang dikembalikan endpoint publik;
- audio recitation dengan `has_playable_audio=true`, termasuk fallback dari `public_url` ke
  `audio_url` bila `public_url` tidak tersedia.

Untuk pilihan awal audio, frontend memakai recitation dengan `is_default=true`. Pengguna hanya
boleh memilih ID yang berasal dari respons `GET /v1/quran/recitations`; frontend tidak menyimpan
atau mengarang ID recitation sendiri.

## Pengecualian audio sementara

Delapan recitation produksi saat keputusan ini dibuat memiliki coverage playable 100%, tetapi
masih berstatus lisensi `needs_review`. Backend saat ini tetap mengembalikannya karena visibility
audio ditentukan oleh `is_visible=true`.

Frontend **diizinkan memakai jalur publik yang sudah hidup tersebut untuk rilis awal**, tetapi:

- jangan menampilkan label `permitted` atau klaim bahwa lisensinya sudah selesai diaudit;
- jangan menambah recitation baru sebagai bagian bypass ini;
- jangan menyalin audio baru ke CDN Surau tanpa audit sumber;
- jangan memakai audio untuk produk turunan, distribusi ulang terpisah, atau download permanen
  sampai lisensinya selesai diaudit;
- jika backend kemudian menyembunyikan suatu recitation, frontend harus menerima respons tersebut
  dan tidak menghidupkannya kembali dari cache atau URL yang disimpan.

### Conflicts with charter

Charter platform menetapkan hanya konten dengan `license_status=permitted` yang boleh tampil
publik. Pengecualian audio di atas bertentangan dengan aturan tersebut dan diterima **hanya sebagai
bypass rilis FE sementara**, bukan preseden kebijakan permanen.

Usulan resolusi: audit lisensi dilakukan per-recitation. Recitation yang terbukti boleh digunakan
diubah menjadi `permitted`; yang tidak memiliki bukti memadai disembunyikan dari reader. Setelah
itu backend harus menerapkan gerbang fail-closed sehingga audio non-`permitted` tidak dapat muncul
publik.

## Blocker super-ketat

Rilis FE **WAJIB dihentikan** bila salah satu kondisi berikut terjadi:

1. Teks Arab salah, ayat tertukar/hilang, `ayah_key` salah, atau jumlah korpus tidak 6.236 ayat.
2. Surah, ayat, juz, hizb, atau halaman mengarah ke ayat yang salah.
3. Terjemahan atau transliterasi ditampilkan untuk ayat yang berbeda dari teks Arabnya.
4. Atribusi sumber terjemahan/transliterasi hilang atau status publiknya bukan `permitted`.
5. Audio default tidak playable, audio ayat menunjuk track ayat lain, atau autoplay melompati
   urutan ayat secara salah.
6. API produksi tidak sehat, terjadi error berulang yang membuat reader utama tidak dapat dipakai,
   atau respons melanggar kontrak list `{items,total}`.
7. Data editorial/nonpublik, token, kredensial, atau informasi pengguna bocor ke respons publik.
8. Cache frontend tetap menyajikan konten setelah backend melakukan takedown atau menyembunyikan
   sumber.

Selain delapan kondisi tersebut, kekurangan berikut **bukan blocker rilis awal** selama UI memiliki
fallback yang jujur:

- belum semua qari selesai audit lisensi;
- belum ada download/offline audio;
- belum ada resume posisi audio lintas perangkat;
- metadata riwayah/qira'ah belum lengkap;
- satu recitation tidak memiliki `public_url` tetapi masih memiliki `audio_url` yang playable;
- fitur admin/editorial belum tersedia di frontend.

## Kontrak konsumsi FE

```text
GET /v1/quran/translation-sources?lang=id
GET /v1/quran/recitations
GET /v1/quran/surahs/{surah_id}/ayahs
    ?lang=id
    &include_translation=true
    &include_audio=true
    &recitation_id={optional_id_from_recitations}
GET /v1/quran/surahs/{surah_id}/audio?recitation_id={optional_id_from_recitations}
```

Aturan rendering:

- gunakan `is_default=true` sebagai recitation awal;
- tampilkan audio hanya jika `has_playable_audio=true`;
- untuk track, prioritaskan `public_url`, kemudian `audio_url`;
- hormati `availability`, `translation_missing`, dan `missing_ayah_keys`;
- jangan membuat fallback terjemahan lintas bahasa secara lokal;
- jangan meng-cache atau menghidupkan kembali sumber yang sudah tidak dikembalikan API.

## Exit criteria bypass

Bypass berakhir setelah:

1. seluruh recitation yang tetap publik memiliki keputusan audit dan bukti;
2. hanya recitation `permitted` yang dapat dikembalikan reader publik;
3. frontend telah diuji terhadap takedown dan empty-state audio;
4. recitation tanpa izin memadai sudah disembunyikan;
5. smoke produksi membuktikan teks, terjemahan, transliterasi, dan audio default tetap sinkron.

Dokumen ini harus dihapus atau ditandai `SELESAI` setelah exit criteria terpenuhi.
