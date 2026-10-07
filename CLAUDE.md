# CLAUDE.md — Panduan kerja repo surau-backend

## Proyek & operator
- Backend **Go** (Fiber + pgx + PostgreSQL + golang-migrate) untuk **Surau** — wiki ilmu Islam:
  Quran, kitab/turats (Shamela), hadith, entitas pengetahuan, dan tanya-jawab AI bersitasi (RAG).
  Frontend web (Next.js) & mobile ada di repo lain — jangan diubah/direncanakan dari sini.
- Operator (Salman) **bukan developer**. Balas dalam bahasa Indonesia dan jelaskan dampak produk,
  bukan jargon. Identifier, komentar kode, dan pesan commit tetap bahasa Inggris.
- Putuskan sendiri hal teknis dengan alasan yang jelas. Tanya operator **hanya** untuk: rilis prod,
  menghapus data nyata, mengubah kontrak API, atau keputusan produk/keagamaan.
- Per 2026-10-07 belum ada pengguna nyata: boleh bergerak cepat, tetapi tetap dengan bukti dan test.

## Alur kerja
1. Mulai dari `main` terbaru di branch baru (`feat/…`, `fix/…`, `refactor/…`, `chore/…`).
2. **Bukti dulu, baru ubah.** Bug → tulis test yang GAGAL di kode lama → perbaiki → test lulus.
   Klaim seperti "kode ini mati" atau "ini aman" dibuktikan dengan alat/test, bukan asumsi.
3. Commit kecil per tema, pesan konvensional (`fix(auth): …`, `refactor(repo): …`).
4. **PR tidak wajib.** Setelah verifikasi di bawah hijau: merge ke `main` lalu push. Push `main` =
   auto-deploy ke dev-api.surau.org — lalu buktikan di dev: `/version` menunjukkan SHA baru dan
   endpoint yang berubah lolos smoke test.
5. **Rilis prod** (tag `api-vX.Y.Z` → api.surau.org + GitHub Release) **hanya dengan persetujuan
   operator**, setelah teruji di dev. Periksa hasil verifikasi `/version` prod di workflow-nya.
6. Laporan akhir dalam bahasa awam: apa yang berubah & dampaknya bagi pengguna, buktinya, apa yang
   sengaja tidak dikerjakan & alasannya, dan apa yang butuh keputusan operator.

## Verifikasi sebelum menyatakan "selesai" (setara CI)
- Build & lint: `go build ./... && go vet ./...`, lalu
  `go tool golangci-lint run --new-from-merge-base=origin/main` → 0 isu. Kode yang *dipindah*
  dihitung kode baru, jadi utang lint lamanya ikut dinilai.
- Unit + race:
  `go test -race -covermode atomic -coverpkg=./internal/...,./pkg/... -coverprofile=coverage.txt ./internal/... ./pkg/...`
- Live test (Postgres 18.4, semua migrasi): perintah persis job "runner / tests / live" di
  `.github/workflows/ci.yml` (serial `-p 1`, `SURAU_LIVE_PG=…`) beserta dua gerbang latensinya.
  Wajib bila menyentuh SQL, migrasi, atau invariant korpus.
- Integration HTTP: `make compose-up-integration-test`. Tanpa `make`: ekspor variabel dari
  `.env.example`, lalu `docker compose -f docker-compose.yml -f docker-compose-integration-test.yml up --build --abort-on-container-exit --exit-code-from integration-test db app integration-test`.
  Endpoint publik baru/berubah wajib punya ≥1 integration test.
- Migrasi baru: pasangan up/down; round-trip `up → down -all (0 objek tersisa) → up` dengan skema
  identik, plus drill Q-1/Q-2/Q-4 (job "runner / migrations / round-trip").
- Kontrak API berubah: regenerate Swagger dengan
  `go tool swag init --parseDependency --templateDelims "[[,]]" -g internal/controller/restapi/router.go`
  dan perbarui `docs/*.md` terkait. `TestSwaggerDocumentsExactlyTheMountedV1Routes` menolak rute
  hantu maupun rute yang tak terdokumentasi.
- Diff coverage kode baru ≥70%:
  `git diff -U0 --no-color origin/main...HEAD | go run ./cmd/diffcover -profile coverage.txt -profile coverage-live.txt`
- `go tool govulncheck ./...` tanpa temuan baru. Tidak ada artefak sementara (dump, coverage,
  file eksperimen) yang ikut ter-commit.
- `make pre-commit` menjalankan sebagian besar langkah di atas bila `make` tersedia. Mesin operator
  saat ini tidak punya Go/make/gcc/pip: pakai toolchain Go yang diunduh ke scratchpad, dan jalankan
  race/live/integration/yamllint lewat container Docker.

## Jebakan yang sudah pernah memakan korban
- Menghapus tabel/kolom/fitur: cari juga rujukan berupa **string SQL**, config, workflow CI,
  compose, dan docs — analisis statis tidak melihatnya.
- Dependensi opsional (`*UseCase` yang bisa nil) hanya boleh masuk ke field/param bertipe interface
  lewat guard `!= nil`. Interface berisi pointer nil lolos cek `!= nil`, lalu panic saat dipanggil.
- `app.Test` milik Fiber memakai koneksi in-memory yang mengabaikan timeout/deadline. Uji
  streaming dan timeout lewat koneksi TCP sungguhan.
- Jangan `_ =` hasil penulisan pasca-commit (audit, event, status). Buat atomik dalam satu
  transaksi, atau buat kegagalannya terlihat (metrik + alert Telegram) seperti
  `surau_editorial_trail_write_failures_total`.
- Kode mati dicari dengan `deadcode` (golang.org/x/tools/cmd/deadcode), bukan hanya linter `unused`.

## Aturan produk yang tidak boleh dilanggar
- **RAG safety:** makna/tafsir TIDAK PERNAH diturunkan LLM dari teks ayat Quran. Ayat = teks primer
  yang dikutip / jangkar rujukan; interpretasi hanya dari tafsir/kitab/hadith. Ditegakkan di level
  data/indeks (eligibility), bukan lewat prompt.
- **Integritas ilmu:** ikhtilaf disajikan plural & ter-atribusi (tidak pernah diratakan, termasuk
  oleh personalisasi); grading hadith per-otoritas + lafaz verbatim, TANPA label global, TANPA
  auto-grading LLM; provenance `source/editorial/machine` terpisah per unit; setiap keluaran LLM
  baru wajib membawa identitas model + versi prompt + run; klaim wiki approved wajib bersitasi.
- **Lisensi:** hanya konten `license_status=permitted` yang tampil publik; `unknown` tidak pernah
  dipublish baru; karya yang telanjur publik tetap tayang, takedown hanya yang teraudit
  `restricted`; terjemahan mesin tampil berlabel dan dikecualikan dari RAG.
- **Kontrak API hidup** (FE web + mobile): envelope list `{items,total}`; ETag optimistic locking
  `If-Match` (412/428/`*`); registry kode error beku di `internal/controller/restapi/apierror`
  (entri lama tidak dihapus/diubah); perubahan breaking hanya versioned/aditif + deprecation 90 hari.
- **Rahasia:** file `.env*` (kecuali `*.example`) berisi rahasia — jangan ditampilkan, jangan di-commit.

## Database, migrasi, deploy
- Migrasi = pasangan timestamped up/down yang teruji bolak-balik. Constraint baru: `NOT VALID` →
  preflight → `VALIDATE`. Perubahan data besar mengikuti `docs/data-change-playbook.md`
  (expand-contract, backfill resumable lewat `cmd/backfill`) tanpa downtime endpoint publik.
- Penulis publikasi mengunci baris `books` lebih dulu, baru project/baris publikasinya (urutan kunci
  global B-4), supaya tidak deadlock dengan takedown lisensi.
- Re-import buku aman: importer staged-diff + soft-tombstone; tombstone baru diterapkan via
  `-approve-removals=<run-id>` setelah review (README §Re-import safety).
- App menjalankan migrasi saat boot; migrasi gagal = schema DIRTY dan boot ditolak. Pemulihan:
  `docs/deploy-vps.md` §Pemulihan schema DIRTY. Tidak ada auto-rollback.

## Keputusan operator
- O-F1-1 (2026-07-07): alarm & laporan lewat bot Telegram (email sebagai cadangan teknis).
- PK-1 Lisensi (2026-07-09): default aman (a/a/a) — lihat aturan Lisensi di atas.
- 2026-10-07: selama belum ada pengguna, PR tidak wajib; alur `roadmap/SESI.md` dan plan mode
  tidak dipakai lagi.
- Keputusan baru → tambahkan di sini (tanggal + ringkasan).

## Peta kode
- `internal/app/app.go` wiring use case, router, dan loop latar · `internal/controller/restapi/`
  router + middleware, `v1/` handler · `internal/usecase/*` logika domain ·
  `internal/repo/persistent/*` SQL, `internal/repo/webapi/*` klien eksternal · `internal/entity`
  tipe domain · `internal/importer/*` importer Quran/buku · `migrations/` skema · `docs/` kontrak
  FE + runbook (`docs/swagger.*` hasil generate) · `scripts/langextract_kg/` ekstraksi entitas
  (Python) · `collab-server/` sidecar Yjs · `workers/api-cache/` edge worker · `eval/` +
  `cmd/rag-eval` eval RAG · `ops/` backup, observability, deploy.
- Layering: entity → repo → usecase → controller → router. Normalisasi Arab hanya lewat profil
  kanonik ber-versi (`internal/quranutil/normalize.go`). ILIKE selalu di-escape; paginasi publik
  selalu di-clamp. Target latensi: p95 baca <200 ms, search <400 ms.

## Referensi (baca bila relevan — bukan ritual tiap sesi)
- `roadmap/README.md` — glosarium istilah domain (Anchor, Citable Unit, Cross-Reference,
  Provenance Class, License Status, EvidencePack, …); pakai istilahnya apa adanya.
- `roadmap/PROGRAM.md`, `roadmap/phase-*.md` — latar belakang inisiatif & keputusan (arsip).
- `roadmap/SESI.md` — arsip antrean sesi lama; tidak dipakai lagi.
