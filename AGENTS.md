# AGENTS.md — Panduan sesi untuk repo surau-backend

## Konteks proyek
Backend **Go** (Fiber + pgx + PostgreSQL + golang-migrate) untuk **Surau** — wiki ilmu Islam:
Quran, kitab/turats (Shamela), hadith (greenfield), entitas pengetahuan — menuju satu retrieval
terpadu (search + RAG bersitasi). Frontend = app **Next.js TERPISAH** (jangan sentuh/rencanakan
di repo ini). Operator (Salman) **non-developer** — balas dalam **bahasa Indonesia**, jelaskan
istilah teknis dengan dampak produknya; identifier/komentar kode tetap English.

## MULAI DARI SINI (sumber kebenaran)
0. **`roadmap/SESI.md`** — antrean prompt sesi siap-paste milik operator (urutan eksekusi
   konkret). Bila sesi ini dimulai dari salah satu prompt di sana: ikuti aturannya, dan di
   akhir sesi CENTANG sesi tsb. di SESI.md.
1. **`roadmap/PROGRAM.md`** — urutan eksekusi (gelombang W0–W7), jalur execute-early
   "Selamatkan Data" (E1–E6), antrean keputusan operator (§5), dan **START HERE** 5 sesi
   pertama (§6). Living document — perbarui saat milestone selesai.
2. **`roadmap/README.md`** (charter) — glosarium kanonik (pakai istilahnya VERBATIM: Anchor,
   Citable Unit, Cross-Reference, Provenance Class, License Status, EvidencePack, dll.),
   definition-of-solid (angka target), prinsip & keputusan D1–D13.
3. **`roadmap/phase-*.md`** — detail tiap inisiatif (rationale, AC, DS, register keputusan).
   Jangan memutus ulang apa yang sudah diputuskan di register; kalau menemukan alasan kuat untuk
   menyimpang, tulis nota "Conflicts with charter" + usulan resolusi, jangan menyimpang diam-diam.

**Status saat ini:** roadmap 0–9 LENGKAP (2026-07-07). Implementasi dimulai dari PROGRAM.md §6
(S1: enkripsi backup + drill restore). Keputusan operator yang belum terjawab → pakai **default
aman** yang tertulis di PROGRAM.md §5.

## Keputusan operator yang SUDAH terjawab
- **O-F1-1** (2026-07-07): kanal alarm & laporan = **bot Telegram** (email = cadangan teknis).
- **PK-1 Lisensi** (2026-07-09): **default aman (a/a/a)** — audit lisensi per-karya, hanya
  `permitted` publik, `unknown` TAK PERNAH dipublish baru (O3-a); karya telanjur publik tetap tayang,
  takedown hanya yang teraudit `restricted` (O-1B-1-a); terjemahan mesin `generated` tampil BERLABEL
  + antrean review, RAG tetap dikecualikan (O-4-4-a). Membuka B-4 (SESI 15).

## Aturan yang MENGIKAT semua sesi (non-negotiable)
- **RAG Safety:** makna/tafsir TIDAK PERNAH diturunkan LLM dari teks ayat Quran. Ayat = teks
  primer yang disitasi / jangkar rujukan; interpretasi hanya dari tafsir/kitab/hadith. Penegakan
  = level data/indeks (eligibility), bukan prompt.
- **Domain Integrity:** ikhtilaf disajikan plural & ter-atribusi (tak pernah diratakan — termasuk
  oleh personalisasi); grading hadith per-otoritas + lafaz verbatim, TANPA label global, TANPA
  auto-grading LLM; provenance `source/editorial/machine` terpisah di tingkat unit; semua
  keluaran LLM baru wajib identitas model + versi-prompt + run (kontrak B-6); klaim wiki approved
  wajib sitasi sumber.
- **Kontrak API hidup** (FE web + mobile bergantung): envelope list `{items,total}`; ETag
  optimistic-locking `If-Match` (412/428/`*`); taksonomi `apierror`; perubahan breaking =
  versioned/additive + masa deprecation 90 hari.
- **Lisensi:** hanya konten `license_status=permitted` yang tampil publik; konten `unknown`
  tidak pernah dipublish baru.

## ⚠️ LARANGAN / zona bahaya
- **Larangan re-import buku DICABUT (E4 selesai 2026-07-07):** importer kini staged-diff +
  soft-tombstone + persetujuan eksplisit — default TIDAK PERNAH menghapus; baris hilang hanya
  di-stage, tombstone baru diterapkan via `-approve-removals=<run-id>` setelah review (drift =
  abort). Suite `TestLiveBookImport*` menjaga kontrak ini di CI. Alur operasional: README
  §Re-import safety.
- File `.env*` berisi rahasia (gitignored) — jangan commit, jangan tampilkan isinya di output.
- Migrasi: pasangan timestamped up/down; pola `NOT VALID` → preflight → `VALIDATE`; perubahan
  data besar wajib playbook F1-H (expand-contract, backfill resumable) — tanpa downtime endpoint
  publik.
- Deploy: push `main` → dev-api.surau.org; tag `api-vX.Y.Z` → api.surau.org (+ GitHub Release).
  Migrasi gagal saat deploy = schema DIRTY → boot-loop; runbook di `docs/deploy-vps.md` §6.
  Tidak ada auto-rollback.

## Protokol sesi (agar tiap sesi konsisten)

**Awal sesi:**
- Baca `roadmap/PROGRAM.md` dulu (posisi gelombang + keputusan terbaru) sebelum mengerjakan apa pun.
- Kerja di branch fitur (`feat/...`, `fix/...`, `chore/...`) — **jangan commit langsung ke
  `main`** (push ke main = auto-deploy ke dev-api).

**Definition of Done — sebuah perubahan BELUM selesai sebelum:**
1. Test menyertainya (unit; integration untuk endpoint publik baru/berubah; live-test bila
   menyentuh invariant korpus) dan `make pre-commit` hijau.
2. Kontrak API berubah → **Swagger di-regenerate** DAN dokumen kontrak terkait di `docs/*.md`
   diperbarui — FE web/mobile membaca docs ini; API berubah tanpa docs = merusak konsumen.
3. Migrasi baru = pasangan up/down yang teruji bolak-balik.
4. Milestone roadmap selesai atau keputusan baru diambil → **perbarui `roadmap/PROGRAM.md`**
   (centang/catat); keputusan operator baru → tambahkan ke "Keputusan terjawab" di file ini.
5. Tidak ada artefak sementara ikut ter-commit (dump, coverage, file eksperimen — pakai tmp/
   lokal).

**Setelah merge ke `main` (auto-deploy dev):** verifikasi di dev-api.surau.org — `/version`
menunjukkan SHA baru + smoke endpoint yang diubah. Merge ≠ selesai; terverifikasi di dev =
selesai.

**Rilis prod:** hanya via tag `api-vX.Y.Z`, hanya setelah perubahan hidup & teruji di dev
(soak). Workflow memverifikasi `/version` prod pasca-deploy — periksa hasilnya, jangan diasumsikan.

## Kebiasaan & perintah repo
- Layering: `entity → repo (persistent/webapi) → usecase → controller (restapi/v1) → router`;
  request/response structs di paket masing-masing; Swagger di-regenerate saat kontrak berubah.
- Test: `make test` (unit+coverage) · `make integration-test` (Docker) · live tests:
  `SURAU_LIVE_PG=... go test -p 1` (serial, invariant korpus — HARUS benar-benar jalan di CI).
- Lint: golangci-lint strict (33 linter, `--new-from-merge-base`); `make pre-commit` sebelum PR.
- Bar kualitas (charter §2.3): kode baru usecase/repo coverage ≥70%; setiap endpoint publik ≥1
  integration test; p95 baca <200ms, search <400ms; paginasi publik selalu ter-clamp; search
  ILIKE selalu ter-escape; normalisasi Arab hanya via profil kanonik ber-versi (1B C5 —
  `internal/quranutil/normalize.go` = sumber kebenaran).
- Commit kecil & konvensional (lihat riwayat: `fix(quran): ...`, `chore(deploy): ...`).

## Peta cepat
`internal/usecase/*` domain logic · `internal/repo/persistent/*` SQL · `internal/importer/*`
importer Quran/buku · `internal/controller/restapi/v1/*` handlers · `migrations/` skema ·
`scripts/langextract_kg/` pipeline ekstraksi entitas (Python) · `collab-server/` sidecar Yjs ·
`workers/api-cache/` edge worker · `eval/` + `cmd/rag-eval` harness eval RAG · `ops/backup/`
skrip backup/restore · `docs/` kontrak FE & runbook.
