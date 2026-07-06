# Audit Quran dan Quran Reader Backend

Tanggal audit: 2026-06-27

Fokus audit: bug, performance issue, stability issue, code quality issue, gap test, dan saran fitur di area Quran, Quran reader, asset Quran, audio Quran, progres baca Quran, khatam, bookmark/saved item Quran, dan sinkronisasi personal yang terkait langsung dengan reader.

## Ringkasan Eksekutif

Area Quran sudah memiliki fondasi yang cukup kuat: validasi input berada di usecase, query SQL memakai parameter binding, response reader punya mode minimal, asset audio dipisah dari metadata, dan test untuk kontrak response dasar sudah ada. Namun ada beberapa risiko penting yang bisa terasa langsung di reader:

- Risiko tertinggi ada di pemilihan audio default, urutan track audio, idempotensi progres baca, dan event khatam. Ini dapat menyebabkan reader mendapat audio yang tidak lengkap, playlist salah urut, statistik membaca membengkak, atau notifikasi milestone terkirim berulang.
- Beberapa query dan importer membuat churn `updated_at` walau data tidak berubah. Ini bisa membuat cache, sitemap, sync, dan job downstream terlihat selalu berubah.
- Kontrak response `view=reader_minimal` sudah baik untuk endpoint surah, tetapi endpoint navigasi juz/hizb belum konsisten dengan kemampuan full editorial.
- Missing asset report untuk `audio_public` berpotensi menggandakan data karena dicross join dengan target bahasa.
- Test yang ada sudah menutup sebagian kontrak, tetapi belum menutup skenario yang paling rawan: kelengkapan coverage recitation default, urutan audio natural, retry progress stale, no-op khatam, duplicate missing assets, dan no-op importer.

Prioritas yang disarankan:

1. Perbaiki correctness audio reader: default recitation harus full coverage, track ordering harus numeric/natural, dan explicit recitation harus divalidasi playable.
2. Perbaiki idempotensi state personal: progress, activity counter, khatam mark/unmark, dan notifikasi milestone.
3. Kurangi churn data dari importer dan sync audio dengan update bersyarat.
4. Rapikan kontrak endpoint reader untuk juz/hizb, missing assets, dan activity historical range.
5. Tambahkan test regresi untuk semua kasus di atas sebelum menambah fitur besar.

## Scope yang Ditinjau

File utama yang diperiksa:

- `internal/controller/restapi/v1/quran.go`
- `internal/controller/restapi/v1/router.go`
- `internal/controller/restapi/v1/personal.go`
- `internal/controller/restapi/v1/sync.go`
- `internal/controller/restapi/v1/response/quran.go`
- `internal/usecase/quran/quran.go`
- `internal/usecase/personal/personal.go`
- `internal/entity/quran.go`
- `internal/repo/contracts.go`
- `internal/repo/persistent/quran_postgres.go`
- `internal/repo/persistent/personal_postgres.go`
- `internal/repo/persistent/khatam_postgres.go`
- `internal/repo/persistent/activity_postgres.go`
- `internal/repo/persistent/personal_sync_postgres.go`
- `internal/importer/quran.go`
- `internal/importer/quran_audio_r2.go`
- `cmd/import-quran-assets/main.go`
- `cmd/sync-quran-audio-r2/main.go`
- Migrasi database terkait Quran, audio, progress, khatam, activity, editorial, dan coverage count.
- Test Quran dan personal yang berkaitan langsung dengan reader.

Validasi yang dijalankan:

```bash
go test ./internal/repo/persistent ./internal/usecase/quran ./internal/controller/restapi/v1
```

Hasil: passed.

Catatan: audit ini membaca kondisi workspace saat ini, termasuk perubahan lokal yang sudah ada sebelum laporan dibuat.

## Temuan Prioritas Tinggi

### QUR-H01 - Default recitation bisa memilih audio yang tidak lengkap

Severity: High

Area: Quran audio, reader playback

Evidence:

- `internal/repo/persistent/quran_postgres.go:1755` sampai `internal/repo/persistent/quran_postgres.go:1783`
- `internal/repo/persistent/quran_postgres.go:1892` sampai `internal/repo/persistent/quran_postgres.go:2029`

Masalah:

`defaultPlayableRecitationID` dan `markDefaultRecitation` menentukan recitation playable dengan logika `playable_track_count = track_count` dan `track_count > 0`. Ini hanya memastikan semua track yang sudah terimport playable, bukan memastikan recitation memiliki coverage lengkap. Recitation dengan 1 track playable dapat lolos sebagai default jika prioritasnya lebih tinggi.

Dampak:

- Endpoint reader dengan `include_audio=true` tanpa `recitation_id` bisa memilih recitation yang sangat tidak lengkap.
- Response ayah bisa banyak tanpa audio.
- Manifest surah bisa menghasilkan `missing_ayah_keys` besar.
- Frontend reader tampak rusak walau status backend menganggap recitation playable.

Rekomendasi:

- Definisikan expected coverage per mode:
  - Ayah mode: harus match semua ayah Quran yang valid, idealnya `COUNT(DISTINCT track_key) = COUNT(quran_ayahs)`.
  - Surah mode: harus match semua 114 surah, atau mode manifest harus eksplisit sebagai full-surah track.
- Tambahkan kolom/materialized metric coverage per recitation, atau hitung via CTE saat memilih default.
- `HasPlayableAudio` jangan hanya berarti "semua imported track playable"; pisahkan menjadi `has_playable_audio`, `is_complete`, dan `coverage_percent`.
- Tambahkan test dengan recitation 1 track playable dan recitation lengkap. Default harus memilih yang lengkap.

### QUR-H02 - Urutan audio track memakai string lexicographic

Severity: High

Area: Quran audio manifest, reader playlist

Evidence:

- `internal/repo/persistent/quran_postgres.go:59`
- `internal/repo/persistent/quran_postgres.go:1960` sampai `internal/repo/persistent/quran_postgres.go:1970`

Masalah:

Query dan comparator mengurutkan `track_key` sebagai string. Untuk format seperti `1:1`, `1:2`, `1:10`, urutan lexicographic akan menaruh `1:10` sebelum `1:2`.

Dampak:

- Playlist ayah dalam reader bisa salah urut.
- Manifest surah dapat membuat player melompat secara tidak natural.
- Bug ini sangat terlihat untuk surah dengan lebih dari 9 ayah.

Rekomendasi:

- Untuk ayah mode, urutkan berdasarkan `surah_id, ayah_number`.
- Untuk surah mode, urutkan berdasarkan `surah_id`.
- Ubah `quranAudioTrackLess` agar memakai field numerik, bukan `TrackKey` string.
- Tambahkan test untuk keys `1:1`, `1:2`, `1:10`.

### QUR-H03 - Retry atau event stale bisa menaikkan activity counter

Severity: High

Area: Quran progress, reading analytics, sync replay

Evidence:

- `internal/repo/persistent/personal_postgres.go:391` sampai `internal/repo/persistent/personal_postgres.go:429`
- Pola serupa untuk kitab: `internal/repo/persistent/personal_postgres.go:87` sampai `internal/repo/persistent/personal_postgres.go:127`

Masalah:

`ON CONFLICT DO UPDATE RETURNING` tetap mengembalikan row walau event yang masuk stale atau duplicate. CTE activity setelahnya tetap melakukan increment `quran_events = 1`. Untuk retry request yang sama, event counter dapat naik berulang.

`quran_ayahs_read` lebih aman karena memakai delta `GREATEST`, tetapi `quran_events` tetap bisa inflate.

Dampak:

- Activity harian tidak idempotent.
- Sync replay atau retry mobile dapat membuat statistik event membaca terlalu tinggi.
- Streak atau heatmap yang memakai event count menjadi kurang dipercaya.

Rekomendasi:

- Tambahkan flag `changed` di CTE upsert. Activity hanya insert/update jika posisi atau `observed_at` benar-benar maju.
- Alternatif: tambahkan idempotency key dari client untuk event progress.
- Tambahkan test:
  - save ayah 10 sekali, retry ayah 10 observed_at sama, counter tidak berubah.
  - kirim ayah 9 setelah ayah 10, event stale tidak menaikkan counter.

### QUR-H04 - Mark khatam juz tidak idempotent untuk notifikasi dan updated_at

Severity: High

Area: Khatam, notification, sync freshness

Evidence:

- `internal/usecase/personal/personal.go:276` sampai `internal/usecase/personal/personal.go:285`
- `internal/repo/persistent/khatam_postgres.go:102` sampai `internal/repo/persistent/khatam_postgres.go:140`

Masalah:

`MarkKhatamJuz` memanggil notifier setiap repo call sukses. Query repo juga menyentuh `updated_at` cycle walau mark juz sudah ada karena `ON CONFLICT DO NOTHING` tidak dibedakan dari insert baru.

Dampak:

- Retry request dapat mengirim notifikasi milestone berulang.
- Sync snapshot melihat cycle berubah walau tidak ada data baru.
- Client dapat melakukan fetch ulang tidak perlu.

Rekomendasi:

- Ubah kontrak repo agar mengembalikan `created bool` atau `changed bool`.
- Kirim notifikasi hanya saat mark baru berhasil dibuat.
- Jangan update cycle `updated_at` jika `INSERT mark` tidak menghasilkan row.
- Tambahkan test no-op mark untuk memastikan notification tidak dipanggil ulang.

### QUR-H05 - Missing assets `audio_public` bisa terduplikasi per target language

Severity: High

Area: Asset reporting, admin/importer dashboard

Evidence:

- `internal/repo/persistent/quran_postgres.go:1113` sampai `internal/repo/persistent/quran_postgres.go:1129`

Masalah:

Branch `audio_public` pada CTE missing assets melakukan `CROSS JOIN target_langs`. Audio publik tidak bergantung pada bahasa target, sehingga default `id,en` dapat menggandakan setiap missing audio.

Dampak:

- Laporan missing asset berlebihan.
- Estimasi pekerjaan import audio bisa salah.
- Dashboard atau alert bisa menghasilkan false positive.

Rekomendasi:

- Untuk `audio_public`, jangan join ke `target_langs`.
- Set `target_lang` menjadi `NULL`, `global`, atau value tetap.
- Tambahkan test default target language `id,en` untuk memastikan missing audio tidak double count.

### QUR-H06 - No-op importer dan audio sync tetap mengubah `updated_at`

Severity: High

Area: Importer, cache invalidation, sitemap, sync stability

Evidence:

- Core importer melakukan update `updated_at = now()` pada banyak conflict path, misalnya `internal/importer/quran.go:1062`, `internal/importer/quran.go:1096`, `internal/importer/quran.go:1160`, `internal/importer/quran.go:1321`, `internal/importer/quran.go:1363`, `internal/importer/quran.go:1452`, `internal/importer/quran.go:1505`, `internal/importer/quran.go:1543`.
- R2 audio sync melakukan pola serupa di `internal/importer/quran_audio_r2.go:440` dan `internal/importer/quran_audio_r2.go:510`.

Masalah:

Saat data yang diimport sama persis, row tetap dianggap berubah karena `updated_at` diperbarui.

Dampak:

- Public cache dan sitemap bisa invalidasi terus-menerus.
- Sync snapshot dapat menganggap data berubah tanpa perubahan konten.
- Observability dan audit trail sulit dibaca karena semua import terlihat sebagai update nyata.

Rekomendasi:

- Gunakan guard `WHERE existing.col IS DISTINCT FROM excluded.col` pada `DO UPDATE`.
- Untuk data besar, simpan checksum per source file atau per entity.
- `updated_at` hanya berubah jika konten yang memengaruhi API response berubah.
- Tambahkan test importer no-op: import fixture dua kali, `updated_at` tidak berubah pada run kedua.

### QUR-H07 - Kontrak editorial berbeda antara endpoint surah dan juz/hizb

Severity: High

Area: API contract, Quran reader full view, SEO/editorial

Evidence:

- Surah endpoint mengatur `includeEditorial := view != reader_minimal` di `internal/controller/restapi/v1/quran.go:348` sampai `internal/controller/restapi/v1/quran.go:362`.
- Juz/hizb memakai helper tanpa flag editorial di `internal/controller/restapi/v1/quran.go:448` sampai `internal/controller/restapi/v1/quran.go:499`.
- Repo navigasi hard-coded `includeEditorial=false` di `internal/repo/persistent/quran_postgres.go:680`.

Masalah:

`view=reader_minimal` sudah bekerja untuk surah. Namun `view=full` pada juz/hizb tidak dapat mengembalikan editorial ayah, karena repo contract tidak membawa opsi itu.

Dampak:

- API shape berbeda berdasarkan cara user membuka reader.
- Frontend yang berpindah dari surah ke juz/hizb dapat kehilangan metadata editorial.
- Dokumentasi full view berpotensi ambigu.

Rekomendasi:

- Tambahkan option struct untuk list ayah: `IncludeEditorial`, `IncludeEditorialHTML`, `IncludeAudio`, `View`.
- Propagasikan ke `JuzAyahs` dan `HizbAyahs`.
- Jika editorial sengaja hanya untuk detail/surah, dokumentasikan eksplisit di OpenAPI dan response docs.

### QUR-H08 - Explicit recitation id tidak divalidasi apakah playable

Severity: High

Area: Quran audio, API ergonomics

Evidence:

- `internal/repo/persistent/quran_postgres.go:1653` sampai `internal/repo/persistent/quran_postgres.go:1669`

Masalah:

`resolveAudioRecitationID` hanya memastikan recitation visible ada. Recitation visible tanpa playable tracks tetap diterima, lalu endpoint dapat mengembalikan response 200 dengan audio kosong.

Dampak:

- Client sulit membedakan "tidak ada audio karena recitation invalid" vs "ayah ini belum punya track".
- Reader dapat gagal memutar tanpa error yang jelas.

Rekomendasi:

- Jika `include_audio=true` dan client memilih `recitation_id`, validasi minimal ada playable track yang relevan.
- Untuk manifest, validasi coverage surah yang diminta.
- Tambahkan response availability reason, misalnya `audio.available=false`, `reason=recitation_incomplete`.

### QUR-H09 - Surah-mode audio tanpa segment timestamp sulit dipakai oleh ayah reader

Severity: High

Area: Quran audio model, manifest, ayah-level playback

Evidence:

- `internal/repo/persistent/quran_postgres.go:1411` sampai `internal/repo/persistent/quran_postgres.go:1502`
- `internal/repo/persistent/quran_postgres.go:2032` sampai `internal/repo/persistent/quran_postgres.go:2063`

Masalah:

Untuk surah-mode, `audioTracksForAyahs` hanya mengaitkan track ke ayah jika ada segment mapping. `missingManifestAyahKeys` juga menganggap ayah hilang jika tidak ada segment. Jika ada full-surah audio playable tetapi tanpa segments, manifest bisa menganggap semua ayah missing.

Dampak:

- Full-surah audio tidak bisa dimanfaatkan reader per ayah.
- Admin dapat melihat missing yang salah.
- Player tidak punya fallback untuk memutar full-surah track.

Rekomendasi:

- Pisahkan konsep "track playable" dan "ayah segment available".
- Manifest surah perlu field seperti `mode=surah`, `has_segments`, dan `segment_coverage_percent`.
- Jika tanpa segment, expose full-surah track untuk player continuous mode dan jangan label semua ayah sebagai missing audio, kecuali labelnya memang "missing segment".

## Temuan Prioritas Menengah

### QUR-M01 - Endpoint activity terlalu membatasi historical range

Severity: Medium

Area: Reading activity, analytics, calendar/heatmap

Evidence:

- `internal/usecase/personal/personal.go:355` sampai `internal/usecase/personal/personal.go:403`

Masalah:

`GetReadingActivity` memanggil `resolveActivityDate(to, now)` yang menolak tanggal lebih dari 2 hari dari tanggal server. Ini cocok untuk "hari ini menurut client", tetapi terlalu ketat untuk query historis.

Dampak:

- Client tidak bisa mengambil activity bulan lalu dengan `to` tanggal historis.
- Heatmap atau calendar reader menjadi sulit dibuat.

Rekomendasi:

- Pisahkan validasi "client today" dari validasi "activity range end".
- Batasi range dengan max days, misalnya 31, 90, atau 366, bukan harus dekat dengan server now.

### QUR-M02 - Activity range tidak zero-fill hari kosong

Severity: Medium

Area: Reading activity, frontend ergonomics

Evidence:

- `internal/repo/persistent/activity_postgres.go:121` sampai `internal/repo/persistent/activity_postgres.go:180`

Masalah:

Query hanya mengembalikan row yang ada di `reading_activity`. Hari tanpa aktivitas tidak muncul.

Dampak:

- Frontend calendar/heatmap harus melakukan fill sendiri.
- Contract lebih rawan beda implementasi antar client.

Rekomendasi:

- Gunakan `generate_series(from, to, interval '1 day')` dan left join activity.
- Atau dokumentasikan bahwa client wajib zero-fill.

### QUR-M03 - Search memakai transaction biasa walau komentar menyebut read-only

Severity: Medium

Area: Search, DB transaction semantics

Evidence:

- `internal/repo/persistent/quran_postgres.go:750` sampai `internal/repo/persistent/quran_postgres.go:760`

Masalah:

Komentar mengatakan read-only transaction, tetapi kode memakai `Pool.Begin(ctx)`, bukan `BeginTx` dengan `ReadOnly: true`.

Dampak:

- Minor stability/code clarity issue.
- Database tidak mendapat sinyal read-only.

Rekomendasi:

- Ganti ke `BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})`.
- Atau ubah komentar jika memang tidak ingin read-only.

### QUR-M04 - Search bisa makin berat saat jumlah translation/source bertambah

Severity: Medium

Area: Search performance

Evidence:

- `internal/repo/persistent/quran_postgres.go:1236` sampai `internal/repo/persistent/quran_postgres.go:1363`

Masalah:

Search memakai `COUNT(*) OVER()`, lateral translation/transliteration, dan fallback any translation. Untuk 6236 ayah saat ini aman, tetapi jumlah translation source dapat bertambah dan membuat query makin berat.

Dampak:

- Search latency bisa naik saat asset translation bertambah.
- `COUNT(*) OVER()` memaksa database menghitung seluruh result yang match.

Rekomendasi:

- Pertimbangkan materialized search table per language/source.
- Tambahkan `tsvector` dengan `unaccent` jika pencarian latin/Indonesia makin penting.
- Untuk reader UX, pertimbangkan approximate total atau `has_more` daripada full count.

### QUR-M05 - Query juz/hizb belum punya index khusus

Severity: Medium

Area: Navigation performance

Evidence:

- Migrasi awal memiliki index surah/page/text, tetapi belum terlihat index `quran_ayahs(juz_number, surah_id, ayah_number)` atau `quran_ayahs(hizb_number, surah_id, ayah_number)`.
- Endpoint terkait memakai filter navigasi di `internal/repo/persistent/quran_postgres.go:645` sampai `internal/repo/persistent/quran_postgres.go:723`.

Masalah:

Data Quran hanya 6236 ayah, sehingga scan masih kecil. Namun endpoint juz/hizb adalah jalur reader yang bisa sering dipanggil.

Dampak:

- Latency masih mungkin aman sekarang, tetapi index murah dan membuat query plan stabil.

Rekomendasi:

- Tambahkan index:
  - `quran_ayahs(juz_number, surah_id, ayah_number)`
  - `quran_ayahs(hizb_number, surah_id, ayah_number)`

### QUR-M06 - Batch sync progress melakukan DB call satu per entry

Severity: Medium

Area: Offline sync, mobile replay performance

Evidence:

- `internal/controller/restapi/v1/sync.go:90` sampai `internal/controller/restapi/v1/sync.go:172`

Masalah:

Batch sync replay memanggil usecase per progress item. Dengan limit banyak entry, ini menjadi banyak round trip DB.

Dampak:

- Offline replay setelah lama tidak sinkron bisa lambat.
- Activity counter dan conflict handling makin sulit dijaga konsisten.

Rekomendasi:

- Tambahkan bulk repo method untuk progress kitab dan Quran.
- Jalankan batch dalam transaction per request.
- Tetap return error per item jika perlu, tetapi hit database dalam bulk statement.

### QUR-M07 - Saved item tag menghitung panjang byte, bukan rune

Severity: Medium

Area: Saved item Quran, i18n

Evidence:

- `internal/usecase/personal/personal.go:551` sampai `internal/usecase/personal/personal.go:557`

Masalah:

Validasi `len(tag) > 64` menghitung byte. Tag Arab, Indonesia dengan karakter non-ASCII, atau emoji dapat ditolak lebih cepat dari ekspektasi user.

Dampak:

- UX bookmark/tag untuk konten Quran bisa terasa inkonsisten.

Rekomendasi:

- Gunakan `utf8.RuneCountInString(tag)` atau batas byte yang didokumentasikan eksplisit.
- Jika tag disimpan untuk search, pertimbangkan normalisasi unicode.

### QUR-M08 - Invalid status book reference diam-diam menjadi approved

Severity: Medium

Area: Quran reference API contract

Evidence:

- `internal/usecase/quran/quran.go:282` sampai `internal/usecase/quran/quran.go:314`
- `internal/usecase/quran/quran.go:361` sampai `internal/usecase/quran/quran.go:368`

Masalah:

`normalizeStatus` mengubah status kosong atau tidak dikenal menjadi `approved`. Untuk parameter API, invalid value lebih baik menghasilkan 400 agar client error cepat terlihat.

Dampak:

- Typo di client diam-diam menghasilkan data approved.
- Debugging filter status lebih sulit.

Rekomendasi:

- Untuk input user, bedakan empty default dengan invalid value.
- Return `ErrQuranInvalidStatus` atau error setara untuk status tidak dikenal.

### QUR-M09 - `listQuranSurahs` default tidak menyertakan info, tapi SEO/editorial butuh kontrak jelas

Severity: Medium

Area: Surah index, SEO, reader bootstrap

Evidence:

- `internal/controller/restapi/v1/quran.go:30` sampai `internal/controller/restapi/v1/quran.go:44`

Masalah:

Default `include_info=false` baik untuk ringan, tetapi sekarang surah memiliki SEO/editorial fields. Perlu dipastikan frontend reader dan SEO page memilih kontrak yang tepat.

Dampak:

- Reader bootstrap bisa membutuhkan request tambahan.
- SEO page bisa lupa meminta metadata editorial.

Rekomendasi:

- Dokumentasikan `include_info` dan field editorial dengan jelas di OpenAPI.
- Pertimbangkan endpoint bootstrap khusus reader yang ringan tetapi lengkap untuk kebutuhan awal.

### QUR-M10 - Import Quran selalu membutuhkan translation path

Severity: Medium

Area: Importer ergonomics, operations

Evidence:

- `internal/importer/quran.go:381` sampai `internal/importer/quran.go:383`
- `internal/importer/quran.go:524` sampai `internal/importer/quran.go:550`

Masalah:

Validator mewajibkan translation JSON. Jika operator hanya ingin import audio atau memperbaiki surah info, flow tetap membutuhkan file translation.

Dampak:

- Operasi asset parsial menjadi kurang fleksibel.
- Automation lebih sulit dibuat modular.

Rekomendasi:

- Pisahkan mode import: core Quran text, translation, transliteration, editorial, audio.
- Validasi required file berdasarkan mode/flag.

### QUR-M11 - R2 audio sync stats `Missing` terlihat belum benar-benar dihitung

Severity: Medium

Area: Audio sync observability

Evidence:

- `cmd/sync-quran-audio-r2/main.go` mencetak missing count.
- `internal/importer/quran_audio_r2.go` memiliki field stats `Missing`, tetapi jalur yang dibaca tidak terlihat mengisinya secara berarti.

Masalah:

Output CLI dapat memberi kesan ada metrik missing, tetapi nilainya mungkin selalu nol.

Dampak:

- Operator dapat melewatkan gap audio.
- Monitoring job import kurang berguna.

Rekomendasi:

- Hitung expected manifest berdasarkan Quran ayahs/surahs atau compare database existing vs manifest.
- Jika belum didukung, hapus output `Missing` agar tidak misleading.

### QUR-M12 - Audio availability response belum memberi alasan detail

Severity: Medium

Area: Reader UX, API diagnostics

Evidence:

- `internal/repo/persistent/quran_postgres.go:1786` sampai `internal/repo/persistent/quran_postgres.go:1821`
- `internal/controller/restapi/v1/response/quran.go:186` sampai `internal/controller/restapi/v1/response/quran.go:199`

Masalah:

Availability audio terutama tahu ada audio di response atau tidak. Ia belum menjelaskan apakah recitation tidak lengkap, source tidak playable, R2 public URL kosong, atau ayah tidak punya segment.

Dampak:

- Frontend sulit memilih fallback.
- Debugging asset audio harus melihat DB langsung.

Rekomendasi:

- Tambahkan `reason` atau `missing_reason` pada availability.
- Tambahkan daftar recitation alternatif yang complete/playable pada endpoint source/recitation.

## Temuan Kualitas Kode dan Konsistensi

### QUR-L01 - Contract option list ayah mulai melebar, butuh option struct

Severity: Low

Area: Code quality

Evidence:

- `SurahAyahs`, `JuzAyahs`, dan `HizbAyahs` membawa kombinasi page, limit, translation source, include audio, recitation id, editorial flag, dan view di beberapa layer.

Masalah:

Signature method akan semakin panjang saat fitur reader bertambah.

Rekomendasi:

- Buat struct seperti `QuranAyahListOptions`.
- Gunakan option yang sama di surah/juz/hizb/page/search agar kontrak konsisten.

### QUR-L02 - Error mapping Quran cukup baik, tetapi beberapa invalid input masih dinormalisasi

Severity: Low

Area: API consistency

Evidence:

- Error mapping Quran ada di `internal/controller/restapi/v1/quran.go:523` sampai `internal/controller/restapi/v1/quran.go:552`.
- Status reference invalid dinormalisasi di usecase.

Masalah:

Sebagian input invalid menghasilkan 400, sebagian lainnya fallback default.

Rekomendasi:

- Buat tabel input: mana yang default jika kosong, mana yang 400 jika unknown.
- Tambahkan test per parameter.

### QUR-L03 - Hard-coded reader views bisa tumbuh menjadi string tersebar

Severity: Low

Area: API contract

Evidence:

- `internal/controller/restapi/v1/quran.go:14` sampai `internal/controller/restapi/v1/quran.go:17`

Masalah:

Saat view bertambah, validasi string di controller bisa tersebar.

Rekomendasi:

- Buat enum/helper validasi view di usecase/entity contract.
- Dokumentasikan view di OpenAPI.

### QUR-L04 - Importer audio segment behavior perlu dokumentasi shape manifest

Severity: Low

Area: Operations, importer maintainability

Evidence:

- `internal/importer/quran.go:879` sampai `internal/importer/quran.go:971`
- `internal/importer/quran.go:1026` sampai `internal/importer/quran.go:1028`

Masalah:

Segment hanya ditambahkan jika dapat dikaitkan ke ayah number. Jika sumber asset memakai struktur timestamp berbeda, segment bisa tidak masuk tanpa operator sadar.

Rekomendasi:

- Dokumentasikan format manifest yang didukung.
- Tambahkan warning/stat untuk skipped segments.

### QUR-L05 - SQL besar di repo Quran perlu guard test yang lebih semantik

Severity: Low

Area: Maintainability

Evidence:

- Test column order sudah ada di `internal/repo/persistent/quran_postgres_test.go:12` sampai `internal/repo/persistent/quran_postgres_test.go:90`.

Masalah:

Test column count membantu, tetapi query besar masih bisa rusak secara semantik tanpa diketahui.

Rekomendasi:

- Tambahkan integration test dengan test DB untuk query penting:
  - list surah ayahs reader minimal
  - list juz/hizb full
  - audio manifest
  - search with translation source
  - missing assets

## Gap Test yang Perlu Ditutup

Prioritas test regresi:

1. Default recitation tidak boleh memilih recitation partial.
2. Track order harus natural: `1:1`, `1:2`, `1:10`.
3. Retry save Quran progress dengan payload sama tidak menaikkan `quran_events`.
4. Stale progress ayah lebih kecil tidak menaikkan `quran_events`.
5. Mark khatam juz yang sama dua kali tidak mengirim notifikasi kedua dan tidak mengubah `updated_at`.
6. Missing assets `audio_public` tidak double count saat target language default `id,en`.
7. No-op import Quran tidak mengubah `updated_at`.
8. No-op R2 audio sync tidak mengubah `updated_at`.
9. `view=full` untuk juz/hizb sesuai kontrak yang diputuskan: include editorial atau documented not included.
10. Historical reading activity range bisa mengambil data masa lalu sesuai batas yang disepakati.
11. Tag saved item non-ASCII dihitung sesuai batas karakter, bukan byte.
12. Invalid book reference status menghasilkan 400 jika kontrak diubah.

## Saran Fitur untuk Quran Reader

### FTR-01 - Endpoint halaman mushaf

Buat endpoint seperti:

```text
GET /quran/pages/{page_number}/ayahs
```

Alasan:

- Reader Quran sering bernavigasi berdasarkan halaman mushaf.
- Saat ini ada filter `page_number` di data ayah, tetapi endpoint eksplisit page akan lebih natural untuk frontend.
- Bisa memakai option yang sama dengan surah/juz/hizb: translation source, transliteration, audio, view.

### FTR-02 - Reader bootstrap endpoint

Buat endpoint protected atau mixed public seperti:

```text
GET /quran/reader/bootstrap
```

Isi yang disarankan:

- daftar surah ringan
- translation sources
- transliteration sources
- recitations dengan coverage/playable status
- user reading preference jika authenticated
- latest progress per surah atau current position jika authenticated

Manfaat:

- Mengurangi beberapa round trip awal reader.
- Frontend lebih sederhana.
- Bisa dicache sebagian untuk public data.

### FTR-03 - User Quran reader preferences

Simpan preferensi user:

- default translation source
- default recitation
- show transliteration
- font scale
- last selected view mode
- repeat mode/audio speed jika dibutuhkan

Manfaat:

- Reader terasa personal.
- Client tidak perlu mengirim query param yang sama terus-menerus.

### FTR-04 - Availability detail dan fallback audio

Tambahkan detail availability:

- `audio.available`
- `audio.reason`
- `audio.recitation_id`
- `audio.coverage_percent`
- `audio.fallback_recitation_ids`

Manfaat:

- Frontend bisa fallback otomatis.
- Admin bisa cepat tahu gap asset.

### FTR-05 - Offline audio manifest dengan checksum dan ukuran file

Tambahkan metadata:

- checksum
- byte size
- duration
- last_modified
- storage provider

Manfaat:

- Mobile app bisa download offline dengan validasi integritas.
- Cache invalidation lebih akurat.

### FTR-06 - Bookmark hydration endpoint

Buat endpoint untuk menghidrasi saved item Quran:

```text
GET /me/saved-items/hydrated?type=quran_ayah
```

Atau response saved item langsung membawa ringkasan ayah/range.

Manfaat:

- Halaman bookmark tidak perlu melakukan banyak request tambahan.
- Saved range dapat langsung menampilkan teks Arab dan translation ringkas.

### FTR-07 - Reading session analytics

Saat ini ayahs read diturunkan dari delta progress. Untuk analytics yang lebih akurat, tambahkan konsep session/event:

- session started/ended
- ayah viewed
- audio played/completed
- manual progress update

Manfaat:

- Statistik tidak bergantung penuh pada monotonic progress.
- Bisa membedakan membaca, mendengar, dan sekadar membuka ayah.

### FTR-08 - Tafsir atau explanation layer

Tambahkan layer tafsir ringan:

- per ayah explanation
- source/license metadata
- language/source selection
- `view=tafsir` atau endpoint detail khusus

Manfaat:

- Reader punya nilai pembelajaran lebih tinggi.
- Bisa tetap dipisahkan dari `reader_minimal` agar payload utama ringan.

### FTR-09 - Quran reference deep link dari kitab reader

Jika kitab memiliki referensi Quran, expose deep link ke Quran reader:

- surah/ayah key
- range start/end
- label display
- target URL atau route metadata

Manfaat:

- Pengalaman lintas kitab dan Quran lebih menyatu.

### FTR-10 - Admin coverage dashboard

Dashboard coverage asset:

- Quran text completeness
- translation coverage per source/lang
- transliteration coverage per source/lang
- audio coverage per recitation/mode
- segment coverage
- editorial/license status

Manfaat:

- Importer dan QA asset lebih mudah dikontrol.
- Bisa menjadi sumber alert sebelum release data Quran.

## Rekomendasi Urutan Pengerjaan

### Sprint 1 - Correctness reader

1. Fix default recitation coverage completeness.
2. Fix natural ordering audio tracks.
3. Validate explicit recitation playable/coverage.
4. Add regression tests untuk tiga item tersebut.

### Sprint 2 - Idempotensi personal state

1. Fix progress activity duplicate/stale event.
2. Fix khatam mark no-op notification dan `updated_at`.
3. Add tests untuk retry progress dan retry khatam.

### Sprint 3 - Data churn dan observability

1. Guard importer updates dengan `IS DISTINCT FROM`.
2. Guard R2 sync updates dengan `IS DISTINCT FROM`.
3. Fix missing assets audio duplication.
4. Pastikan CLI stats missing benar atau hapus output yang misleading.

### Sprint 4 - Contract cleanup

1. Buat `QuranAyahListOptions`.
2. Samakan kontrak `view=full` untuk surah/juz/hizb/page.
3. Dokumentasikan view, availability, source selection, dan editorial exposure di OpenAPI.

### Sprint 5 - Reader feature expansion

1. Tambahkan page endpoint.
2. Tambahkan reader bootstrap.
3. Tambahkan user preferences.
4. Tambahkan bookmark hydration.
5. Tambahkan offline audio manifest metadata.

## Catatan Positif

Beberapa bagian yang sudah baik dan sebaiknya dipertahankan:

- `reader_minimal` response sudah memangkas payload editorial untuk jalur reader surah.
- Validasi range surah, ayah, juz, hizb, page, limit, dan language ada di usecase.
- SQL dynamic untuk navigation segment memakai allowlist field, bukan string bebas.
- Audio URL response memprioritaskan `public_url` sebelum raw `audio_url`.
- Ada test untuk column order query dan response editorial exposure.
- Progress Quran memakai model monotonic per surah sehingga tidak mudah mundur karena event lama.
- Sync snapshot memakai overlap window dan deletion ID reconciliation untuk saved items.

## Kesimpulan

Backend Quran sudah siap menjadi fondasi reader yang serius, tetapi perlu beberapa perbaikan correctness sebelum fitur reader diperluas. Perbaikan paling bernilai adalah audio coverage/order, idempotensi progress/khatam, dan pengurangan churn importer. Setelah itu, fitur seperti page endpoint, bootstrap, preference, offline manifest, dan bookmark hydration akan jauh lebih aman dibangun di atas kontrak yang stabil.
