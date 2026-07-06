# Audit Quran Reader Berbasis Go Best Practices

Tanggal audit: 2026-06-27

Dokumen ini adalah audit tambahan untuk area Quran reader dengan fokus Go best practices. Dokumen ini tidak menggantikan `docs/quran-reader-audit.md`; ia memperketat audit sebelumnya dengan rubric Go, pgx/Postgres, testing, lint, safety, security, dan beberapa skill Samber sebagai pembanding praktik modern.

## Ringkasan Eksekutif

Area Quran reader sudah punya beberapa fondasi Go yang sehat:

- Request context mengalir dari Fiber handler ke usecase dan repo.
- Query repo memakai parameter binding dan dynamic column yang di-allowlist.
- `rows.Close()` dan `rows.Err()` umumnya sudah konsisten.
- Response reader memakai preallocation untuk slice mapping.
- Error domain dipetakan dengan `errors.Is`.
- Repo target Go 1.26 dan sudah punya toolchain kuat: `gofumpt`, `gci`, `golangci-lint`, `govulncheck`, `mockgen`, `testify`, dan `pgx`.

Namun ada beberapa temuan penting dari sudut Go production quality:

1. Correctness reader audio masih perlu diperkuat: default recitation bisa partial, audio sorting masih string-based, dan explicit recitation hanya dicek visible, bukan playable.
2. Idempotency state personal belum sempurna: `ON CONFLICT` pada progress tetap bisa menaikkan activity event saat retry/stale, dan khatam no-op masih menyentuh `updated_at` serta memicu notifier.
3. Kontrak API/usecase/repo Quran mulai melebar karena banyak parameter boolean/string. Ini melanggar guideline "fungsi <=4 parameter" dari `golang-code-style` dan melemahkan evolusi kontrak reader.
4. Performance claim belum disertai benchmark/EXPLAIN. Untuk query search, missing assets, audio manifest, dan batch replay, rekomendasi harus diperlakukan sebagai hypothesis sampai diukur.
5. Samber package belum dipakai repo. Jadi `samber/lo`, `samber/oops`, dan `samber/slog-*` sebaiknya dianggap opsi arsitektural masa depan, bukan dependency yang otomatis ditambahkan.

## Repo Baseline

Evidence:

- `go.mod:3` sampai `go.mod:5`: repo target `go 1.26` dan `toolchain go1.26.3`.
- `go.mod:7` sampai `go.mod:15`: tools mencakup `gci`, `migrate`, `golangci-lint`, `swag`, `mockgen`, `govulncheck`, `gofumpt`.
- `go.mod:17` sampai `go.mod:37`: stack utama mencakup Fiber, pgx, zerolog, testify, mockgen, sqlite test helper, dan x/sync.
- `.golangci.yml:1` sampai `.golangci.yml:102`: lint sudah ketat, termasuk `errcheck`, `errorlint`, `gosec`, `govet`, `noctx`, `rowserrcheck`, `sqlclosecheck`, `paralleltest`, `thelper`, `tparallel`, `gocritic`, `gocognit`, dan formatter `gofumpt/gci/goimports`.
- `Makefile:86` sampai `Makefile:100`: target lint dan test utama sudah ada, dengan `go test -v -race -covermode atomic`.
- `Makefile:124`: pre-commit menjalankan deps, format, lint, dan test.
- Search repo untuk `github.com/samber`, `samber/lo`, `samber/oops`, dan `slog-*` tidak menemukan dependency Samber aktual.

Implikasi:

- Audit ini memakai skill Samber sebagai rubric, bukan instruksi untuk menambah dependency.
- Untuk koleksi sederhana, default tetap stdlib `slices`/`maps` dan loop Go idiomatik.
- Untuk logging, repo sudah memakai zerolog; migrasi ke slog/Samber hanya layak jika ada keputusan observability lintas service.

## Skill Matrix

| Skill/Rubric | Prinsip yang dipakai | Area repo yang diaudit |
| --- | --- | --- |
| `golang-pro` | context propagation, error wrapping, table-driven tests, race/lint discipline | controller, usecase, repo, importer |
| `golang-code-style` | fungsi fokus, parameter <=4, option struct, early return, preallocation wajar | method Quran usecase/repo/controller |
| `golang-structs-interfaces` | interface kecil, accept interface return struct, compile-time checks, kontrak consumer | `usecase.Quran`, `repo.QuranRepo` |
| `golang-naming` | MixedCaps, error naming, status enum jelas, subtest names | view string, status filter, error domain |
| `golang-error-handling` | `%w`, `errors.Is`, log-or-return, low-cardinality messages | repo errors, controller logging |
| `golang-context` | context dari request ke DB, no mid-request `context.Background` | Fiber handler, usecase, repo, CLI boundary |
| `golang-database` | pgx, parameterized SQL, `BeginTx`, rows lifecycle, `ON CONFLICT`, batch, indexes | `quran_postgres.go`, personal/khatam repo |
| `golang-performance` | ukur sebelum optimasi, DB hot path lebih penting dari micro-alloc | search, audio manifest, missing assets |
| `golang-benchmark` | `b.Loop` Go 1.26, benchstat, no single-run claims | benchmark gaps untuk mapper/search/import |
| `golang-testing` | table tests, observable behavior, integration build tags, race | Quran tests, repo tests, regression gaps |
| `golang-lint` | gunakan `.golangci.yml` sebagai source of truth | lint gate dan quality checklist |
| `golang-safety` | nil/empty slices, byte vs rune, numeric/range guard, no panic path | response mapping, saved tags, validation |
| `golang-security` | parameterized SQL, trust boundary, PII logs, rate limit | public Quran endpoints dan personal endpoints |
| `golang-samber-lo` | stdlib first, `lo` hanya untuk transform kompleks, no `lo.Must` in prod | slice/map transform candidates |
| `golang-samber-oops` | structured errors with stable messages if adopted | future repo/usecase error context |
| `golang-samber-slog` | sampling/formatting/routing pipeline if adopting slog | future logging architecture |

## Temuan Prioritas Tinggi

### QUR-GOBP-H01 - Default recitation bisa partial tetapi ditandai playable

Severity: High

Skill lens: `golang-database`, `golang-performance`, `golang-testing`, `golang-safety`

Evidence:

- `docs/quran-api.md:220` sampai `docs/quran-api.md:230`
- `docs/quran-api.md:542` sampai `docs/quran-api.md:543`
- `internal/repo/persistent/quran_postgres.go:1755` sampai `internal/repo/persistent/quran_postgres.go:1783`
- `internal/repo/persistent/quran_postgres.go:1892` sampai `internal/repo/persistent/quran_postgres.go:1914`
- `internal/repo/persistent/quran_postgres.go:2026` sampai `internal/repo/persistent/quran_postgres.go:2027`

Masalah:

`defaultPlayableRecitationID` memilih recitation visible dengan `COUNT(track) > 0` dan semua track yang sudah ada playable. Ini belum membuktikan coverage Quran lengkap. `HasPlayableAudio` juga berarti "semua imported track playable", bukan "recitation lengkap untuk reader".
Dokumentasi API memakai frasa "full playable recitation" untuk default audio, sehingga istilah domain di docs lebih kuat daripada invariant yang saat ini dijamin kode.

Dampak reader:

- `include_audio=true` tanpa `recitation_id` bisa memilih recitation dengan hanya sebagian kecil track.
- Playlist dan ayah list dapat mengembalikan audio kosong untuk banyak ayah.
- API tampak sehat karena recitation dianggap playable, padahal user experience rusak.

Rekomendasi Go-idiomatic:

- Buat helper repo yang jelas namanya, misalnya `completePlayableRecitationID(ctx)`, bukan memperluas makna `HasPlayableAudio`.
- Hitung expected coverage berdasarkan mode:
  - ayah mode: distinct playable track key harus sama dengan jumlah ayah Quran.
  - surah mode: distinct playable surah track harus sama dengan jumlah surah, atau exposenya harus jelas sebagai full-surah mode.
- Tambahkan field domain seperti `IsComplete`, `CoveragePercent`, atau `PlayableCoverage`.
- Tambahkan table-driven repo test untuk recitation partial vs complete.
- Jangan klaim performance improvement tanpa `EXPLAIN` atau benchmark; correctness dulu.

### QUR-GOBP-H02 - Sorting audio track masih lexicographic string

Severity: High

Skill lens: `golang-database`, `golang-performance`, `golang-testing`

Evidence:

- `internal/repo/persistent/quran_postgres.go:45` sampai `internal/repo/persistent/quran_postgres.go:59`
- `internal/repo/persistent/quran_postgres.go:1640` sampai `internal/repo/persistent/quran_postgres.go:1650`
- `internal/repo/persistent/quran_postgres.go:1960` sampai `internal/repo/persistent/quran_postgres.go:1970`

Masalah:

SQL dan comparator memakai `track_key` sebagai string. Key seperti `1:10` akan lebih kecil dari `1:2` secara lexicographic.

Dampak reader:

- Playlist audio ayah bisa salah urut.
- Surah audio manifest dapat membuat player melompat ke ayah yang tidak natural.
- Bug ini terlihat pada surah dengan lebih dari 9 ayah.

Rekomendasi Go-idiomatic:

- Jangan parse string di hot path jika field numerik sudah ada.
- Comparator harus memakai `TrackType`, `SurahID`, dan `AyahNumber` numerik.
- SQL order untuk ayah mode sebaiknya `ORDER BY t.track_type, t.surah_id, t.ayah_number, s.segment_index`.
- Tambahkan test kecil untuk `quranAudioTrackLess`: `1:1`, `1:2`, `1:10`.
- Untuk manifest, tambahkan integration test agar SQL order dan Go comparator sepakat.

### QUR-GOBP-H03 - `ON CONFLICT` progress masih bisa inflate activity event pada retry/stale

Severity: High

Skill lens: `golang-database`, `golang-testing`, `golang-safety`

Evidence:

- `internal/repo/persistent/personal_postgres.go:391` sampai `internal/repo/persistent/personal_postgres.go:415`
- `internal/repo/persistent/personal_postgres.go:417` sampai `internal/repo/persistent/personal_postgres.go:429`

Masalah:

`ON CONFLICT DO UPDATE RETURNING` tetap menghasilkan row walau request adalah retry atau event stale. CTE `activity` tetap increment `quran_events = 1`.

Dampak reader:

- Offline sync/retry mobile dapat menggandakan event activity.
- `quran_ayahs_read` relatif lebih aman karena delta `GREATEST`, tetapi `quran_events` tetap dapat membengkak.
- Statistik reading activity dan streak support menjadi kurang dipercaya.

Rekomendasi Go-idiomatic:

- Buat CTE `changed` eksplisit yang hanya true ketika incoming progress benar-benar maju.
- Filter CTE activity dengan `WHERE changed`.
- Alternatif kontrak repo: return `saved, changed, error`.
- Tambahkan table-driven integration tests:
  - first save ayah 10 increments once.
  - retry same ayah/same observed_at does not increment.
  - stale ayah 9 after ayah 10 does not increment.

### QUR-GOBP-H04 - Khatam mark/unmark no-op tetap menyentuh state dan notifier

Severity: High

Skill lens: `golang-database`, `golang-error-handling`, `golang-testing`

Evidence:

- `internal/repo/persistent/khatam_postgres.go:98` sampai `internal/repo/persistent/khatam_postgres.go:128`
- `internal/repo/persistent/khatam_postgres.go:142` sampai `internal/repo/persistent/khatam_postgres.go:168`
- `internal/usecase/personal/personal.go:276` sampai `internal/usecase/personal/personal.go:287`

Masalah:

Repo menyebut mark idempotent, tetapi no-op tetap meng-update cycle `updated_at`. Usecase juga memanggil notifier setiap repo call sukses, tanpa tahu apakah mark baru dibuat.

Dampak reader:

- Retry mark juz dapat mengirim milestone notification berulang.
- Sync snapshot dapat melihat data berubah walau tidak ada perubahan domain.
- Client melakukan refresh tidak perlu.

Rekomendasi Go-idiomatic:

- Ubah repo contract menjadi return `cycle, changed, error`, atau masukkan `Changed bool` ke result domain.
- Update `updated_at` hanya jika `mark` atau `unmark` menghasilkan row.
- Notify hanya jika `changed == true`.
- Test notifier dengan mock dan assertion count.

### QUR-GOBP-H05 - Search transaction dikomentari read-only tetapi memakai `Begin`

Severity: High

Skill lens: `golang-database`, `golang-context`, `golang-error-handling`

Evidence:

- `internal/repo/persistent/quran_postgres.go:750` sampai `internal/repo/persistent/quran_postgres.go:760`

Masalah:

Komentar menyebut read-only transaction untuk scope `SET LOCAL`, tetapi kode memakai `r.Pool.Begin(ctx)`, bukan `BeginTx` dengan access mode read-only.

Dampak reader:

- Ini bukan bug fungsional langsung, tetapi kontrak kode menipu pembaca.
- DB tidak mendapat sinyal read-only.
- Saat query search berubah makin kompleks, semantic drift seperti ini membuat review sulit.

Rekomendasi Go-idiomatic:

- Pakai `BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})` jika pgx version mendukung access mode yang sesuai.
- Jika pgx access mode tidak dipakai karena alasan compatibility, ubah komentar agar akurat.
- Tambahkan test kecil atau lint review note untuk memastikan `SET LOCAL` tidak bocor session.

### QUR-GOBP-H06 - Missing asset `audio_public` dicross join ke target language

Severity: High

Skill lens: `golang-database`, `golang-performance`, `golang-testing`

Evidence:

- `internal/repo/persistent/quran_postgres.go:1030` sampai `internal/repo/persistent/quran_postgres.go:1136`
- Khusus audio: `internal/repo/persistent/quran_postgres.go:1113` sampai `internal/repo/persistent/quran_postgres.go:1129`

Masalah:

`audio_public` tidak bergantung bahasa, tetapi query melakukan `CROSS JOIN target_langs`. Dengan default target `id,en`, missing audio dapat muncul dua kali.

Dampak reader/admin:

- Dashboard missing asset bisa double count.
- Operator bisa mengira gap audio lebih besar dari kenyataan.
- Alert admin bisa noisy.

Rekomendasi Go-idiomatic:

- Pisahkan asset global dari asset localized.
- Untuk `audio_public`, set `target_lang` menjadi `NULL` atau `"global"` dan jangan cross join.
- Tambahkan regression test dengan target default dua bahasa.

### QUR-GOBP-H07 - Importer dan R2 sync melakukan no-op update yang churn `updated_at`

Severity: High

Skill lens: `golang-database`, `golang-performance`, `golang-benchmark`

Evidence:

- `internal/importer/quran.go:1069`, `internal/importer/quran.go:1106`, `internal/importer/quran.go:1172`, `internal/importer/quran.go:1331`, `internal/importer/quran.go:1369`, `internal/importer/quran.go:1429`, `internal/importer/quran.go:1456`, `internal/importer/quran.go:1517`, `internal/importer/quran.go:1549`, `internal/importer/quran.go:1580`
- `internal/importer/quran_audio_r2.go:456` sampai `internal/importer/quran_audio_r2.go:470`
- `internal/importer/quran_audio_r2.go:517` sampai `internal/importer/quran_audio_r2.go:528`

Masalah:

Banyak `ON CONFLICT DO UPDATE` selalu set `updated_at = now()`, walau konten tidak berubah.

Dampak reader:

- Cache dan sync dapat melihat data Quran berubah terus.
- Sitemaps/editorial freshness bisa menjadi noisy.
- Import ulang asset yang sama menciptakan write amplification.

Rekomendasi Go-idiomatic:

- Gunakan `WHERE existing.col IS DISTINCT FROM EXCLUDED.col` pada `DO UPDATE`.
- Untuk data besar, gunakan checksum yang sudah ada di beberapa area editorial.
- Tambahkan integration test no-op import dua kali, assert `updated_at` stabil.
- Jangan benchmark sebelum correctness no-op jelas; setelah itu ukur jumlah row updated dan durasi import.

### QUR-GOBP-H08 - Kontrak Quran list ayah terlalu banyak parameter

Severity: High

Skill lens: `golang-code-style`, `golang-structs-interfaces`, `golang-naming`

Evidence:

- `internal/usecase/contracts.go:196` sampai `internal/usecase/contracts.go:245`
- `internal/repo/contracts.go:285` sampai `internal/repo/contracts.go:325`
- `internal/usecase/quran/quran.go:96` sampai `internal/usecase/quran/quran.go:256`
- `internal/controller/restapi/v1/quran.go:352` sampai `internal/controller/restapi/v1/quran.go:363`

Masalah:

Method reader membawa banyak parameter: lang, translation source, include translation, include audio, include editorial, recitation id, range, dan navigation kind. Ini melewati guideline Go style fungsi <=4 parameter dan membuat evolusi fitur rawan salah urutan boolean.

Dampak reader:

- Penambahan `view`, `page`, fallback recitation, atau editorial mode akan makin sulit.
- Boolean positional rawan tertukar saat refactor.
- Juz/hizb tidak punya include editorial karena kontrak lama tidak membawa option itu.

Rekomendasi Go-idiomatic:

- Buat option struct, misalnya:

```go
type QuranAyahListOptions struct {
    Lang              string
    TranslationSource string
    IncludeTranslation bool
    IncludeAudio       bool
    IncludeEditorial   bool
    RecitationID       string
    Limit              uint64
    Offset             uint64
}
```

- Untuk range target, gunakan struct terpisah seperti `QuranAyahRange` atau method khusus `ListSurahAyahs(ctx, surahID, range, opts)`.
- Terapkan bertahap: repo option dulu, lalu usecase, lalu controller.
- Tambahkan compile-time interface checks setelah contract berubah.

### QUR-GOBP-H09 - `view=reader_minimal` sudah optimal di surah, tetapi tidak konsisten di juz/hizb

Severity: High

Skill lens: `golang-structs-interfaces`, `golang-code-style`, `golang-testing`

Evidence:

- `internal/controller/restapi/v1/quran.go:348` sampai `internal/controller/restapi/v1/quran.go:362`
- `internal/controller/restapi/v1/quran.go:448` sampai `internal/controller/restapi/v1/quran.go:499`
- `internal/repo/persistent/quran_postgres.go:676` sampai `internal/repo/persistent/quran_postgres.go:680`

Masalah:

Surah endpoint menghindari editorial join untuk `reader_minimal`. Ini bagus. Tetapi helper juz/hizb tidak membawa `includeEditorial`; repo hard-code `includeEditorial=false`.

Dampak reader:

- Full view punya bentuk data berbeda tergantung endpoint.
- Future SEO/editorial view untuk juz/hizb akan sulit ditambahkan tanpa breaking refactor.

Rekomendasi Go-idiomatic:

- Masukkan `View` atau `IncludeEditorial` ke option struct.
- Definisikan kontrak resmi:
  - `reader_minimal`: tanpa editorial.
  - `full`: editorial light jika tersedia.
  - detail ayah: editorial HTML jika dibutuhkan.
- Tambahkan controller tests untuk surah, juz, dan hizb agar response shape konsisten.

## Temuan Prioritas Menengah

### QUR-GOBP-M01 - Explicit `recitation_id` hanya dicek visible, bukan playable

Severity: Medium

Skill lens: `golang-database`, `golang-error-handling`, `golang-testing`

Evidence:

- `internal/repo/persistent/quran_postgres.go:1653` sampai `internal/repo/persistent/quran_postgres.go:1668`
- `internal/repo/persistent/quran_postgres.go:1786` sampai `internal/repo/persistent/quran_postgres.go:1820`

Masalah:

Jika caller mengirim `recitation_id`, repo hanya memastikan recitation visible. Tidak ada validasi bahwa recitation punya track playable untuk ayah/surah yang diminta.

Dampak reader:

- API bisa 200 dengan audio kosong.
- Frontend sulit membedakan recitation invalid, incomplete, atau ayah tidak punya segment.

Rekomendasi:

- Untuk `include_audio=true`, validasi playable coverage minimal untuk target query.
- Tambahkan availability reason seperti `recitation_not_playable`, `track_missing`, atau `segment_missing`.
- Jangan hanya expose `audio_available=false`; reader butuh alasan eksplisit agar bisa membedakan recitation tersembunyi, recitation visible tetapi belum playable, track missing, dan segment missing.
- Jangan expose raw DB error; tetap gunakan domain error dan `errors.Is`.

### QUR-GOBP-M02 - Surah-mode audio tanpa segment dianggap missing per ayah

Severity: Medium

Skill lens: `golang-database`, `golang-safety`, `golang-testing`

Evidence:

- `internal/repo/persistent/quran_postgres.go:1411` sampai `internal/repo/persistent/quran_postgres.go:1456`
- `internal/repo/persistent/quran_postgres.go:2032` sampai `internal/repo/persistent/quran_postgres.go:2063`
- `internal/importer/quran.go:1008` sampai `internal/importer/quran.go:1028`

Masalah:

Surah-mode track hanya dihubungkan ke ayah jika ada segment. Importer juga drop segment jika `ayahNumber <= 0`. Full-surah audio playable tanpa segment bisa terlihat missing untuk semua ayah.

Dampak reader:

- Continuous surah player bisa punya audio, tetapi ayah-level highlight tidak tersedia.
- Missing manifest conflates missing track dengan missing segment.

Rekomendasi:

- Pisahkan `track_coverage` dan `segment_coverage`.
- Manifest response sebaiknya memiliki `has_segments` dan `segment_missing_ayah_keys`.
- Availability audio perlu membedakan "file surah playable tetapi segment per ayah belum tersedia" dari "audio benar-benar tidak ada".
- Test manifest untuk surah-mode tanpa segment.

### QUR-GOBP-M03 - Batch progress sync masih row-by-row

Severity: Medium

Skill lens: `golang-database`, `golang-performance`, `golang-testing`

Evidence:

- `internal/controller/restapi/v1/sync.go:90` sampai `internal/controller/restapi/v1/sync.go:107`
- `internal/controller/restapi/v1/sync.go:112` sampai `internal/controller/restapi/v1/sync.go:172`

Masalah:

Batch replay memproses entry satu per satu. Ini sederhana dan memberi error per item, tetapi banyak DB round trip untuk offline replay besar.

Dampak reader:

- Mobile yang lama offline dapat sync lambat.
- Idempotency activity makin penting karena retry batch bisa banyak.

Rekomendasi:

- Pertahankan response per item, tetapi tambahkan repo bulk method.
- Gunakan batch size 100 sampai 1000 sesuai rekomendasi `golang-database`.
- Uji dengan benchmark atau integration timing, bukan asumsi.

### QUR-GOBP-M04 - Activity historical range terlalu ketat dan tidak zero-fill

Severity: Medium

Skill lens: `golang-safety`, `golang-database`, `golang-testing`

Evidence:

- `internal/usecase/personal/personal.go:351` sampai `internal/usecase/personal/personal.go:403`
- `internal/repo/persistent/activity_postgres.go:121` sampai `internal/repo/persistent/activity_postgres.go:180`

Masalah:

`resolveActivityDate` menolak tanggal lebih dari dua hari dari server today. Cocok untuk "client local today", tetapi tidak cocok untuk historical activity. Query repo juga hanya mengembalikan row yang ada, bukan zero-filled days.

Dampak reader:

- Calendar/heatmap Quran tidak bisa mengambil periode lama secara natural.
- Client harus melakukan zero-fill sendiri.

Rekomendasi:

- Pisahkan validator `today` dan validator historical range.
- Gunakan `generate_series` untuk zero-fill di SQL, atau dokumentasikan kontrak client-fill secara eksplisit.
- Test range historis dan empty-day behavior.

### QUR-GOBP-M05 - Tag saved item memakai byte length, bukan rune count

Severity: Medium

Skill lens: `golang-safety`, `golang-security`

Evidence:

- `internal/usecase/personal/personal.go:551` sampai `internal/usecase/personal/personal.go:557`

Masalah:

`len(tag)` menghitung byte. Tag Arab atau karakter non-ASCII bisa ditolak sebelum mencapai batas karakter yang user harapkan.

Dampak reader:

- Bookmark/tag Quran multilingual terasa tidak konsisten.

Rekomendasi:

- Jika batas dimaksud karakter, gunakan `utf8.RuneCountInString`.
- Jika batas dimaksud byte storage, dokumentasikan sebagai byte limit.
- Tambahkan test tag Arab/Indonesia non-ASCII.

### QUR-GOBP-M06 - Invalid Quran reference status diam-diam fallback ke approved

Severity: Medium

Skill lens: `golang-error-handling`, `golang-naming`, `golang-testing`

Evidence:

- `internal/usecase/quran/quran.go:23`
- `internal/usecase/quran/quran.go:282` sampai `internal/usecase/quran/quran.go:313`
- `internal/usecase/quran/quran.go:361` sampai `internal/usecase/quran/quran.go:367`

Masalah:

`normalizeStatus` mengubah status tidak dikenal menjadi `"approved"`. Empty default masuk akal, tetapi typo client sebaiknya 400.

Dampak reader:

- Filter status sulit di-debug.
- API terlihat berhasil tetapi hasil tidak sesuai maksud client.

Rekomendasi:

- Bedakan empty value dan invalid value.
- Tambahkan sentinel error seperti `ErrInvalidQuranReferenceStatus`.
- Test invalid status di usecase dan controller.

### QUR-GOBP-M07 - R2 sync stats `Missing` dicetak, tetapi belum tampak dihitung

Severity: Medium

Skill lens: `golang-error-handling`, `golang-testing`, `golang-safety`

Evidence:

- `internal/importer/quran_audio_r2.go:52` sampai `internal/importer/quran_audio_r2.go:59`
- `internal/importer/quran_audio_r2.go:96` sampai `internal/importer/quran_audio_r2.go:132`
- `cmd/sync-quran-audio-r2/main.go:28` sampai `cmd/sync-quran-audio-r2/main.go:36`

Masalah:

Stats memiliki field `Missing` dan CLI mencetaknya, tetapi jalur sync yang terlihat tidak mengisinya.

Dampak reader/admin:

- Operator bisa mengira missing audio sudah dihitung.
- Monitoring import misleading.

Rekomendasi:

- Hitung expected manifest coverage, atau hapus output missing sampai valid.
- Tambahkan unit test untuk stats parsing.
- Jika missing butuh DB compare, namai field lebih spesifik, misalnya `MissingFromManifest`.

### QUR-GOBP-M08 - Import Quran mewajibkan translation path untuk semua mode

Severity: Medium

Skill lens: `golang-code-style`, `golang-error-handling`, `golang-database`

Evidence:

- `internal/importer/quran.go:368` sampai `internal/importer/quran.go:391`
- `internal/importer/quran.go:510` sampai `internal/importer/quran.go:555`

Masalah:

Validator mewajibkan translation path walau operator mungkin hanya ingin import audio, transliteration, atau surah info.

Dampak reader/admin:

- Operasi asset parsial lebih sulit.
- Automation import harus membawa file yang tidak selalu relevan.

Rekomendasi:

- Pisahkan mode import eksplisit: core text, translation, transliteration, surah info, audio.
- Validasi required file berdasarkan mode.
- Error message tetap lowercase jika mengikuti Go error string convention.

## Temuan Prioritas Rendah dan Code Quality

### QUR-GOBP-L01 - `reader_minimal` string sebaiknya menjadi typed view

Severity: Low

Skill lens: `golang-naming`, `golang-safety`

Evidence:

- `internal/controller/restapi/v1/quran.go:14` sampai `internal/controller/restapi/v1/quran.go:17`
- `internal/controller/restapi/v1/quran.go:501` sampai `internal/controller/restapi/v1/quran.go:510`

Masalah:

View masih string di controller. Saat view bertambah, string validation dapat tersebar.

Rekomendasi:

- Buat type lokal/domain, misalnya `type QuranAyahView string`.
- Validasi tetap di boundary controller/usecase.
- Test table untuk empty, `full`, `reader_minimal`, dan invalid.

### QUR-GOBP-L02 - Response slice empty sudah baik, pertahankan pola preallocation

Severity: Positive/Low

Skill lens: `golang-code-style`, `golang-safety`, `golang-samber-lo`

Evidence:

- `internal/controller/restapi/v1/response/quran.go:82` sampai `internal/controller/restapi/v1/response/quran.go:90`
- `internal/controller/restapi/v1/response/quran.go:138` sampai `internal/controller/restapi/v1/response/quran.go:156`
- `internal/controller/restapi/v1/response/quran.go:159` sampai `internal/controller/restapi/v1/response/quran.go:184`

Catatan:

Mapper response memakai `make([]T, 0, len(...))`, sehingga JSON empty slice cenderung `[]`, bukan `null`. Ini sesuai `golang-safety`.

Rekomendasi:

- Pertahankan manual loop karena sederhana dan tidak butuh `samber/lo`.
- `lo.Map` bisa dipertimbangkan hanya jika transform kompleks dan repo memang memutuskan memakai Samber.

### QUR-GOBP-L03 - Error wrapping repo sudah cukup, tetapi pesan masih campuran kapitalisasi

Severity: Low

Skill lens: `golang-error-handling`, `golang-naming`

Evidence:

- Banyak error repo memakai pola `fmt.Errorf("QuranRepo - ...: %w", err)`, misalnya `internal/repo/persistent/quran_postgres.go:686` sampai `internal/repo/persistent/quran_postgres.go:703`.

Masalah:

Wrapping dengan `%w` sudah benar, tetapi Go convention untuk error string biasanya lowercase dan low-cardinality. Prefix `QuranRepo - Method - stage` konsisten dengan repo, jadi ini bukan blocker.

Rekomendasi:

- Jangan ubah massal tanpa keputusan repo-wide.
- Jika refactor, pilih format stabil seperti `"quran repo list navigation ayahs query: %w"`.
- Hindari menyisipkan variable high-cardinality di message; taruh di structured log jika tersedia.

### QUR-GOBP-L04 - CLI boundary boleh memakai `context.Background`, request path tidak

Severity: Positive/Low

Skill lens: `golang-context`

Evidence:

- Request path memakai `ctx.UserContext()`, misalnya `internal/controller/restapi/v1/quran.go:37`, `internal/controller/restapi/v1/quran.go:112`, dan `internal/controller/restapi/v1/quran.go:352`.
- CLI memakai `context.Background()` di `cmd/sync-quran-audio-r2/main.go:23`.

Catatan:

Ini sesuai Go context best practice: `context.Background()` boleh di entrypoint seperti CLI/main, sementara request path meneruskan context dari handler.

Rekomendasi:

- Untuk CLI yang panjang, pertimbangkan signal-aware context dengan `signal.NotifyContext`.
- Jangan buat `context.Background()` di tengah usecase/repo.

### QUR-GOBP-L05 - Dynamic navigation column sudah memakai allowlist

Severity: Positive/Low

Skill lens: `golang-database`, `golang-security`

Evidence:

- `internal/repo/persistent/quran_postgres.go:656` sampai `internal/repo/persistent/quran_postgres.go:680`

Catatan:

Dynamic column masuk ke SQL melalui `fmt.Sprintf`, tetapi nilainya berasal dari `quranNavigationColumn(kind)` allowlist. Ini sesuai database/security best practice.

Rekomendasi:

- Pertahankan allowlist.
- Tambahkan test invalid kind jika belum ada.

## Deep Follow-up Audit: Temuan yang Masih Missed

Section ini adalah pass kedua setelah audit Go-centric utama. Fokusnya pada hal yang tidak selalu tampak sebagai bug unit-level, tetapi berpengaruh ke kontrak reader, biaya public endpoint, dan stabilitas sync.

### QUR-GOBP-H10 - Kontrak docs "full playable recitation" tidak dijamin invariant kode

Severity: High

Skill lens: `golang-database`, `golang-testing`, `golang-documentation`, `golang-safety`

Evidence:

- `docs/quran-api.md:220` sampai `docs/quran-api.md:230`
- `docs/quran-api.md:542` sampai `docs/quran-api.md:543`
- `internal/repo/persistent/quran_postgres.go:1755` sampai `internal/repo/persistent/quran_postgres.go:1774`
- `internal/repo/persistent/quran_postgres.go:1892` sampai `internal/repo/persistent/quran_postgres.go:1914`
- `internal/repo/persistent/quran_postgres.go:2026` sampai `internal/repo/persistent/quran_postgres.go:2027`

Masalah:

Docs menyatakan default dipilih ketika ada `full playable recitation`, tetapi kode hanya membuktikan semua track yang sudah diimport playable. Jika hanya 100 track dari recitation ayah yang terimport dan semuanya punya URL, `HasPlayableAudio` bisa benar di model hasil scan, sementara coverage Quran lengkap belum dijamin.

Dampak reader:

- FE mengikuti docs dan memilih `is_default=true`, tetapi playlist bisa bolong.
- Contract API terlihat menjanjikan full Quran coverage, padahal field `has_playable_audio` hanya menjelaskan playable ratio untuk imported tracks.
- Bug ini rawan lolos unit test jika fixture test selalu memakai `TrackCount=6236`.

Rekomendasi Go-idiomatic:

- Pisahkan nama dan invariant: `HasPlayableAudio` boleh tetap berarti imported tracks playable, tetapi default selection harus memakai helper yang mengecek coverage expected.
- Untuk ayah mode, cek `COUNT(DISTINCT track_key)` terhadap total ayah Quran atau total ayah aktif di `quran_ayahs`.
- Untuk surah mode, cek 114 surah tracks plus segment coverage jika fitur highlight/ayah seek dianggap wajib.
- Tambahkan integration test DB untuk partial import yang semua track-nya playable tetapi tidak boleh menjadi default.
- Update docs bila keputusan produk memang menerima partial recitation; jangan biarkan docs dan kode memakai istilah "full playable" dengan makna berbeda.

### QUR-GOBP-M09 - Public Quran search belum punya limiter khusus dan offset tidak dibatasi

Severity: Medium

Skill lens: `golang-security`, `golang-performance`, `golang-database`, `golang-benchmark`

Evidence:

- `internal/controller/restapi/v1/router.go:101` sampai `internal/controller/restapi/v1/router.go:114`
- `internal/controller/restapi/v1/quran.go:386` sampai `internal/controller/restapi/v1/quran.go:400`
- `internal/usecase/quran/quran.go:260` sampai `internal/usecase/quran/quran.go:279`
- `internal/usecase/quran/quran.go:344` sampai `internal/usecase/quran/quran.go:350`
- `internal/repo/persistent/quran_postgres.go:1236` sampai `internal/repo/persistent/quran_postgres.go:1363`

Masalah:

Semua route `/quran` public memakai `PublicCache`, tetapi `/quran/search` tidak punya limiter khusus. Search juga menerima offset tidak terbatas: `clampOffset` hanya memotong nilai negatif. Query search sendiri mahal karena memakai trigram, lateral translation search, ranking, dan `COUNT(*) OVER()` sebelum `LIMIT/OFFSET`.

Dampak reader:

- Query publik dengan banyak variasi `q`, `lang`, dan offset dapat membuat cache churn dan tetap membebani DB.
- Offset besar bisa memaksa database melewati banyak row hasil ranking sebelum mengembalikan halaman kecil.
- Risiko DoS ringan meningkat karena endpoint ini tidak berada di balik auth dan tidak punya budget request seperti personal writes.

Rekomendasi Go-idiomatic:

- Tambahkan limiter khusus untuk `/quran/search` dengan key IP atau kombinasi IP/user-agent, terpisah dari static Quran endpoints.
- Tambahkan `maxOffset` di usecase Quran, atau pindah ke cursor/keyset jika deep paging memang dibutuhkan.
- Pertimbangkan memisahkan endpoint cached/static Quran dari search route agar kebijakan cache dan limiter lebih spesifik.
- Ukur dengan `EXPLAIN (ANALYZE, BUFFERS)` untuk query pendek, query populer, dan offset besar sebelum mengklaim optimasi.
- Tambahkan load/regression test yang memastikan offset ekstrem ditolak atau dikap sesuai kontrak.

### QUR-GOBP-M10 - `PublicCache` memberi 304 setelah biaya handler tetap dibayar

Severity: Medium

Skill lens: `golang-performance`, `golang-security`, `golang-code-style`

Evidence:

- `internal/controller/restapi/middleware/cache.go:16` sampai `internal/controller/restapi/middleware/cache.go:23`
- `internal/controller/restapi/middleware/cache.go:31` sampai `internal/controller/restapi/middleware/cache.go:49`
- `internal/controller/restapi/v1/router.go:101` sampai `internal/controller/restapi/v1/router.go:114`

Masalah:

`PublicCache` memanggil `ctx.Next()` dulu, lalu membaca body response, menghitung SHA-256, set ETag, dan baru membandingkan `If-None-Match`. Artinya request conditional GET tetap menjalankan handler, query DB, JSON encode, dan hash body sebelum menjadi 304.

Dampak reader:

- Untuk endpoint static kecil, ini masih cukup baik sebagai bandwidth cache.
- Untuk `/quran/search`, 304 tidak mengurangi beban DB karena search sudah selesai dihitung.
- Nama middleware bisa membuat reviewer mengira cache ini melindungi origin load, padahal mekanismenya lebih tepat disebut response validator.

Rekomendasi Go-idiomatic:

- Dokumentasikan `PublicCache` sebagai validator header, bukan server-side cache.
- Untuk endpoint static Quran, pertimbangkan ETag berbasis version/updated_at yang bisa dibandingkan sebelum membangun body.
- Untuk search, prioritaskan limiter dan query budget, bukan mengandalkan ETag.
- Hindari server-side in-memory cache kompleks sebelum ada metric hit-rate, cardinality query, dan memory budget yang jelas.

### QUR-GOBP-M11 - Range ayah surah belum divalidasi terhadap `ayah_count`

Severity: Medium

Skill lens: `golang-safety`, `golang-error-handling`, `golang-database`, `golang-testing`

Evidence:

- `internal/usecase/quran/quran.go:187` sampai `internal/usecase/quran/quran.go:224`
- `internal/repo/persistent/quran_postgres.go:564` sampai `internal/repo/persistent/quran_postgres.go:626`
- `migrations/20260526000001_create_quran_reference_tables.up.sql:24` sampai `migrations/20260526000001_create_quran_reference_tables.up.sql:32`

Masalah:

Usecase memvalidasi `surahID`, nilai negatif, dan `to < from`, tetapi tidak memastikan `from`/`to` berada dalam `quran_surahs.ayah_count`. SQL repo hanya menambahkan predicate `ayah_number >= from` dan `ayah_number <= to`.

Dampak reader:

- Request seperti `from=999` pada surah pendek bisa sukses dengan list kosong, bukan error range yang jelas.
- Request `to=999` dapat terlihat seperti "full surah" karena semua ayah existing lolos predicate.
- FE bug sulit ditemukan karena server tidak membedakan empty valid range dari invalid ayah range.

Rekomendasi Go-idiomatic:

- Tambahkan repo/usecase helper untuk membaca `ayah_count` dan validasi range sebelum list ayah.
- Jika ingin menghindari extra query, kembalikan metadata surah + ayah dalam satu query dan map `ErrInvalidQuranRange` ketika range melewati count.
- Tambahkan table-driven tests untuk `from=0,to=n`, `from>to`, `from>ayah_count`, dan `to>ayah_count`.
- Dokumentasikan apakah out-of-range harus `400 invalid quran range` atau `404 ayah not found`.

### QUR-GOBP-M12 - Constraint navigasi Quran belum defensif untuk juz, hizb, dan page

Severity: Medium

Skill lens: `golang-database`, `golang-safety`, `golang-testing`

Evidence:

- `migrations/20260526000001_create_quran_reference_tables.up.sql:55` sampai `migrations/20260526000001_create_quran_reference_tables.up.sql:71`
- `internal/repo/persistent/quran_postgres.go:1220` sampai `internal/repo/persistent/quran_postgres.go:1233`
- `internal/repo/persistent/quran_postgres.go:450` sampai `internal/repo/persistent/quran_postgres.go:458`

Masalah:

Schema `quran_ayahs` hanya memastikan `ayah_number > 0` dan `ayah_key` sesuai. Kolom `page_number`, `juz_number`, dan `hizb_number` nullable tetapi tidak punya CHECK range. Usecase memvalidasi request segment, tetapi tetap mempercayai angka navigasi yang diimport.

Dampak reader:

- Importer atau data source salah bisa menghasilkan daftar juz/hizb/page yang tidak valid.
- Navigation summary dan ayah list bisa menampilkan segment di luar domain Quran tanpa kegagalan cepat.
- Bug data quality baru ketahuan di UI, bukan saat import/migration.

Rekomendasi Go-idiomatic:

- Tambahkan migration terpisah dengan CHECK: `juz_number BETWEEN 1 AND 30`, `hizb_number BETWEEN 1 AND 60`, dan `page_number > 0` jika nilai tidak null.
- Tambahkan validation report di importer untuk ayah count per surah dan range navigation.
- Jalankan migration dengan preflight query yang mencari row invalid agar rollout aman.
- Tambahkan integration test import fixture invalid yang gagal sebelum data masuk public reader.

### QUR-GOBP-M13 - Quran saved item no-op masih mengubah `updated_at` dan memicu sync churn

Severity: Medium

Skill lens: `golang-database`, `golang-performance`, `golang-testing`, `golang-safety`

Evidence:

- `internal/repo/persistent/personal_postgres.go:514` sampai `internal/repo/persistent/personal_postgres.go:548`
- `internal/repo/persistent/personal_postgres.go:635` sampai `internal/repo/persistent/personal_postgres.go:680`
- `internal/repo/persistent/personal_sync_postgres.go:174` sampai `internal/repo/persistent/personal_sync_postgres.go:198`

Masalah:

`UpsertSavedItem` selalu set `updated_at = now()` pada conflict, walau label/note/tags efektif sama. `UpdateSavedItem` juga selalu set `updated_at` sebelum tahu field PATCH mengubah nilai. Sync snapshot mengambil saved items berdasarkan `updated_at >= since`, sehingga no-op write tetap masuk payload.

Dampak reader:

- Retry bookmark Quran atau autosave label/tag dapat membuat sync payload berubah tanpa perubahan domain.
- Client multi-device menerima saved item "changed" padahal nilainya sama.
- Cache/sync cursor menjadi noisy, terutama untuk user yang sering menyimpan ayah.

Rekomendasi Go-idiomatic:

- Tambahkan `WHERE` pada `ON CONFLICT DO UPDATE` memakai `IS DISTINCT FROM` terhadap nilai hasil `COALESCE`.
- Untuk PATCH, tambahkan predicate `WHERE` yang hanya update jika field set berbeda dari stored value.
- Pertimbangkan return `changed bool` untuk saved item write, sama seperti rekomendasi progress/khatam.
- Tambahkan integration test: upsert Quran ayah saved item dua kali dengan payload sama, assert `updated_at` stabil dan sync kedua kosong.

### QUR-GOBP-L06 - Positive pattern: sync snapshot sudah memakai transaction semantics yang kuat

Severity: Positive/Low

Skill lens: `golang-database`, `golang-context`, `golang-safety`

Evidence:

- `internal/repo/persistent/personal_sync_postgres.go:13` sampai `internal/repo/persistent/personal_sync_postgres.go:21`
- `internal/repo/persistent/personal_sync_postgres.go:23` sampai `internal/repo/persistent/personal_sync_postgres.go:39`
- `internal/repo/persistent/personal_sync_postgres.go:48` sampai `internal/repo/persistent/personal_sync_postgres.go:55`
- `internal/repo/persistent/personal_sync_postgres.go:61` sampai `internal/repo/persistent/personal_sync_postgres.go:83`

Catatan:

`SyncSnapshot` sudah memakai `BeginTx` dengan `pgx.RepeatableRead` dan `pgx.ReadOnly`, menginisialisasi slice response ke empty slice, dan memakai overlap window agar cursor sync tidak melewatkan row yang commit dekat batas waktu. Ini contoh bagus untuk area reader personal.

Rekomendasi:

- Pertahankan pola ini saat menambah data personal Quran baru.
- Jika nanti saved item/progress write mengembalikan `changed bool`, sync snapshot tidak perlu ikut berubah; cukup bergantung pada `updated_at` yang lebih bersih.
- Gunakan pattern read-only `BeginTx` ini sebagai pembanding untuk `SearchAyahs`, yang saat ini komentarnya sudah read-only tetapi masih memakai `Begin`.

## Bagian Khusus Samber

Repo saat ini tidak memakai `github.com/samber/*`. Karena itu, rekomendasi Samber harus konservatif.

### `samber/lo`

Kapan relevan:

- Transform slice/map berulang yang kompleks, misalnya grouping, uniq, chunking, flattening.
- Error-aware transform yang bisa memakai `lo.MapErr`.

Kapan tidak perlu:

- Mapper response sederhana seperti `QuranReaderAyahs`.
- Hot path yang belum diprofiling.
- Operasi yang sudah ada di stdlib Go 1.26 seperti `slices` dan `maps`.

Catatan audit:

- Manual loop di response Quran saat ini lebih tepat daripada menambah dependency.
- Jika batch replay nanti butuh chunking, `lo.Chunk` bisa dipertimbangkan, tetapi stdlib/manual loop juga cukup.

### `samber/oops`

Kapan relevan:

- Jika tim ingin structured errors yang membawa code, domain, trace, user/tenant context, dan public message.
- Jika observability membutuhkan error attributes melewati layer repo/usecase/controller.

Kapan tidak perlu:

- Jika repo tetap memakai domain sentinel errors dan zerolog boundary logging.
- Jika belum ada standar APM/log aggregation yang membutuhkan error attributes portable.

Catatan audit:

- Jangan campur aduk `fmt.Errorf` dan `oops` per file tanpa design. Kalau diadopsi, mulai dari boundary baru atau package tertentu.
- Untuk sekarang, rekomendasi utama tetap `%w`, `errors.Is`, dan low-cardinality logging.

### `samber/slog-*`

Kapan relevan:

- Jika repo migrasi ke `log/slog` Go 1.21+ dan butuh sampling, fanout, routing, PII formatting, atau sink multiple.

Kapan tidak perlu:

- Repo saat ini memakai zerolog. Migrasi logging adalah keputusan platform, bukan fix Quran reader.

Catatan audit:

- Untuk Quran reader, yang penting adalah log message stabil, tidak membawa PII, dan tidak log-and-return berlebihan.
- Jika memakai slog/Samber nanti, sampling harus berada sebelum formatting agar tidak boros CPU.

## Test Gap dan Validation Checklist

### Coverage yang sudah ada dan perlu dipertahankan

- `internal/repo/persistent/quran_postgres_test.go` sudah punya unit coverage untuk `markDefaultRecitation`, source audio fallback, no default tanpa playable audio, `quranAudioTrackLess` basic track type, `missingManifestAyahKeys`, navigation allowlist, dan metadata availability.
- `internal/controller/restapi/v1/quran_test.go` sudah meng-cover `reader_minimal` pada surah/juz/hizb route dan editorial omission untuk surah reader minimal.
- `integration-test/quran_test.go` sudah menguji multilingual Quran contract, default audio exposure, dan availability response.
- Gap yang tersisa bukan sekadar "belum ada test", tetapi belum cukup DB/integration/load oriented untuk membuktikan SQL ordering, full coverage recitation, idempotency no-op, dan public search cost.

### Regression tests prioritas

1. `defaultPlayableRecitationID` tidak memilih recitation partial.
2. `markDefaultRecitation` hanya menandai recitation complete sebagai default.
3. `quranAudioTrackLess` natural order: `1:1`, `1:2`, `1:10`.
4. `audioTracksForSurah` SQL order sesuai order numerik.
5. Retry `SaveQuranProgress` tidak menaikkan `quran_events`.
6. Stale `SaveQuranProgress` tidak menaikkan `quran_events`.
7. `MarkKhatamJuz` no-op tidak mengirim notifier kedua.
8. `MarkKhatamJuz` no-op tidak mengubah `updated_at`.
9. Missing `audio_public` tidak double count untuk target default `id,en`.
10. Import Quran no-op tidak mengubah `updated_at`.
11. R2 audio sync no-op tidak mengubah `updated_at`.
12. Invalid Quran reference status menghasilkan 400 jika contract diubah.
13. Tag saved item non-ASCII dihitung sesuai karakter, bukan byte, jika itu keputusan produk.
14. `reader_minimal` surah/juz/hizb contract konsisten.
15. Docs/API contract test untuk "full playable recitation" vs partial imported playable tracks.
16. `/quran/search` menolak atau meng-cap offset ekstrem sesuai kontrak baru.
17. Search load/EXPLAIN scenario untuk query populer, query pendek, dan offset besar.
18. `PublicCache` test yang mendokumentasikan 304 terjadi setelah body tersedia, agar tidak disalahartikan sebagai origin cache.
19. Surah ayah range out-of-count menghasilkan error domain yang konsisten.
20. Import fixture dengan `juz_number`, `hizb_number`, atau `page_number` invalid gagal sebelum data masuk reader.
21. Saved item Quran no-op tidak mengubah `updated_at` dan tidak muncul ulang di sync snapshot.

### Test style

- Gunakan table-driven tests dengan `name` field.
- Test usecase dengan mock/stub interface, bukan database.
- Test SQL correctness/idempotency dengan integration test atau test DB fixture.
- Untuk Go 1.26 benchmark baru, gunakan `b.Loop`.
- Untuk search performance, simpan output `EXPLAIN (ANALYZE, BUFFERS)` sebagai catatan audit/PR, bukan assertion brittle di unit test.
- Race gate utama sudah ada di `Makefile:98` sampai `Makefile:100`.

### Lint/quality gates

Command yang relevan:

```bash
go test ./internal/repo/persistent ./internal/usecase/quran ./internal/controller/restapi/v1
golangci-lint run ./internal/repo/persistent ./internal/usecase/quran ./internal/controller/restapi/v1
make test
make linter-golangci
make deps-audit
```

Catatan:

- `make test` menjalankan race dan coverage untuk `./internal/... ./pkg/...`.
- `golangci-lint` sudah mengaktifkan `noctx`, `rowserrcheck`, `sqlclosecheck`, `errcheck`, `errorlint`, dan `gosec`.
- Untuk audit dokumen ini, baseline targeted test cukup sebagai smoke check; lint opsional dapat gagal karena existing unrelated issues.

## Remediation Roadmap

### Sprint 1 - Correctness Quran reader

1. Perbaiki default recitation agar hanya memilih coverage lengkap.
2. Perbaiki natural ordering audio track di SQL dan comparator Go.
3. Validasi explicit `recitation_id` terhadap playable/coverage target.
4. Selaraskan docs "full playable recitation" dengan invariant kode dan test DB partial coverage.
5. Validasi `from`/`to` ayah terhadap `ayah_count` atau dokumentasikan contract out-of-range.
6. Tambahkan tests untuk lima hal di atas.

### Sprint 2 - Idempotency dan DB semantics

1. Perbaiki `SaveQuranProgress` agar activity hanya berubah saat progress benar-benar berubah.
2. Perbaiki khatam mark/unmark agar no-op tidak menyentuh `updated_at`.
3. Ubah kontrak repo/usecase khatam menjadi return `changed bool`.
4. Perbaiki `SearchAyahs` agar komentar dan transaction mode selaras, idealnya `BeginTx` read-only.
5. Tambahkan max offset atau cursor policy untuk `/quran/search`.
6. Tambahkan limiter khusus public Quran search dan dokumentasikan bahwa `PublicCache` bukan origin cache.
7. Bersihkan no-op churn saved item Quran dengan `IS DISTINCT FROM`.
8. Tambahkan regression tests untuk retry/stale/no-op dan saved item sync churn.

### Sprint 3 - Observability, import stability, dan API cleanup

1. Guard importer dan R2 sync update dengan `IS DISTINCT FROM`.
2. Benahi stats `Missing` pada R2 sync atau hapus output misleading.
3. Fix missing assets `audio_public` agar language-independent.
4. Tambahkan CHECK constraint defensif untuk `juz_number`, `hizb_number`, dan `page_number` setelah preflight data.
5. Introduce `QuranAyahListOptions` untuk mengurangi parameter boolean/string.
6. Dokumentasikan `reader_minimal`, full view, availability reason, dan editorial exposure di OpenAPI.

## Kesimpulan

Backend Quran reader sudah berada di jalur yang baik untuk Go service production: context diteruskan, SQL eksplisit, lint config kuat, dan response mapper cukup idiomatik. Risiko terbesar bukan di style dasar, tetapi di correctness domain yang terlihat melalui audio reader, idempotency personal state, dan kontrak API yang mulai melebar.

Langkah terbaik berikutnya adalah memperbaiki correctness audio dan idempotency sebelum menambah fitur reader baru. Setelah itu, option struct dan test coverage akan membuat evolusi Quran reader lebih aman dan lebih mudah direview.
