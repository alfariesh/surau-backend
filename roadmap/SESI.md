# SESI.md — Antrean Prompt Siap-Paste (untuk Salman)

> **Cara pakai (hanya ini yang perlu kamu ingat):**
> 1. Buka Claude Code di folder repo ini → paste prompt sesi paling atas yang belum dicentang.
> 2. Claude akan masuk **plan mode** → baca ringkasan rencananya → ketik setuju/approve.
> 3. Tunggu selesai → baca laporan akhirnya (bahasa awam) → centang sesinya di file ini.
> 4. Lanjut prompt berikutnya. **Satu sesi = satu prompt. Jangan gabung.**
>
> **Kalau sesi gagal/berantakan:** tutup, buka sesi BARU, paste prompt yang sama + tambahkan
> kalimat: *"Sesi sebelumnya gagal di tengah — periksa dulu keadaan branch/kode, rapikan, lalu
> lanjutkan."*
>
> **Menjawab keputusan (PK-x):** cukup buka sesi Claude dan ketik, misalnya: *"PK-1: saya pilih
> default aman semua"* atau *"PK-1 poin 2: saya pilih (b)"* — sesi akan mencatatnya ke
> PROGRAM.md. Tidak menjawab = default aman berlaku otomatis.

---

## ⚙️ PERSIAPAN SEKALI SAJA (manual, hanya kamu yang bisa)

- [x] **Buat bot Telegram**: buka Telegram → cari **@BotFather** → ketik `/newbot` → ikuti
  langkahnya → simpan **token**-nya. Lalu chat bot barumu sekali (tekan Start). Token ini akan
  diminta sesi S1 — berikan dengan menaruhnya di file `.env` saat sesi memintanya (JANGAN paste
  token di chat).

---

## GELOMBANG 0 — Selamatkan Data

- [x] **SESI 1 — Commit fondasi + enkripsi backup + drill restore (E1+E2)** ✅ 2026-07-07 —
  drill #1 lulus (241 dtk, prod); catatan: token bot terlanjur tertempel di chat sesi — rotasi
  via @BotFather dianjurkan (lihat laporan sesi), lalu perbarui `/etc/surau-backup/env` di 2 VPS.

```text
Sebelum mulai: commit folder roadmap/ dan CLAUDE.md ke main (pesan: "chore(roadmap): program plan fase 0-9 + CLAUDE.md").
Lalu kerjakan E1+E2 dari roadmap/PROGRAM.md §1: (E1) enkripsi client-side dump backup sebelum naik R2, kunci terpisah dari kredensial bucket; (E2) drill restore pertama memakai ops/backup/surau-pg-restore-check + jadwalkan restore-check mingguan otomatis + dead-man alert 26 jam — semua alarm & laporan ke BOT TELEGRAM (keputusan O-F1-1; minta saya isi token bot & chat ID via .env saat dibutuhkan).
Rujukan detail: roadmap/phase-1-foundations.md inisiatif F1-A dan roadmap/phase-8-production.md P8-2.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, checklist Definition of Done di CLAUDE.md, centang milestone di roadmap/PROGRAM.md dan roadmap/SESI.md, merge, verifikasi dev-api. Akhiri dengan laporan bahasa awam + kirim satu pesan Telegram uji ke saya.
```

- [x] **SESI 2 — PITR + snapshot deploy aman (E3+E6)** ✅ 2026-07-07 — pgBackRest di kedua VPS
  (RPO ≤5 mnt); drill PITR lulus 82 dtk; snapshot pra-deploy terenkripsi → R2 retensi 7 hari.

```text
Kerjakan E3+E6 dari roadmap/PROGRAM.md §1: (E3) WAL-archiving/PITR ke R2 sehingga RPO turun dari 24 jam ke ≤1 jam, dibuktikan demonstrasi pemulihan point-in-time; (E6) snapshot pra-deploy ikut diunggah ke R2 dengan retensi 7 hari.
Rujukan: roadmap/phase-1-foundations.md F1-A (bagian WAL/PITR & snapshot).
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done di CLAUDE.md, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 3 — Jinakkan importer buku (E4) — PALING PENTING** ✅ 2026-07-07 — hard-delete
  dihapus; alur stage→review→approve + tombstone reversibel; suite 8 skenario ditulis DULU
  (merah membuktikan defect, hijau setelah implementasi); larangan re-import di CLAUDE.md dicabut.

```text
Kerjakan E4 dari roadmap/PROGRAM.md §1: importer Shamela (cmd/import-books) saat ini MENGHAPUS permanen halaman/heading yang hilang di sumber baru dan ikut menghapus kerja editorial (defect D1 kritis). Ubah menjadi staged-diff + tombstone + persetujuan eksplisit, dan TULIS SUITE TEST-NYA DULU sebelum mengubah perilaku.
Rujukan: roadmap/phase-4-kitab-editorial.md §1.2 defect D1/D6 + inisiatif K-0 poin importer; playbook di roadmap/phase-1-foundations.md F1-H bila perlu.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion K-0 bagian importer. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam. Setelah sesi ini selesai, larangan re-import di CLAUDE.md boleh kamu perbarui statusnya.
```

- [x] **SESI 4 — Tutup celah DoS publik + bersih-bersih identitas repo (E5 + F1-F)** ✅ 2026-07-08 —
  offset publik ter-cap 10k; headings ter-paginasi (default 200, aditif); %/_ di search jadi literal;
  module di-rename ke alfariesh/surau-backend; gitleaks menjaga CI (terbukti menolak dummy secret).

```text
Kerjakan E5 dari roadmap/PROGRAM.md §1 (clamp offset publik ke 10k, paginasi endpoint headings dengan default besar yang aman, escape metakarakter ILIKE di semua search reader — defect D2/D4/D5 di roadmap/phase-4-kitab-editorial.md §1.2) DAN F1-F dari roadmap/phase-1-foundations.md (rename module path dari github.com/evrone/go-clean-template ke identitas Surau, hapus kode mati amqp_rpc/nats_rpc, tambah pemindaian secret di CI).
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion kedua item. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

## GELOMBANG 1 — Fondasi

- [x] **SESI 5 — Mata & telinga sistem (F1-B)** ✅ 2026-07-08 — log ber-request_id+trace_id;
  trace HTTP→SQL→LLM terbukti; Grafana https://dev-api.surau.org/grafana & /grafana prod;
  KELIMA alert dibuktikan menyala (simulasi dev) → Telegram; prod menyusul lengkap saat rilis
  api-v berikutnya (stack sudah hidup, app instrumented menunggu tag).

```text
Kerjakan F1-B dari roadmap/phase-1-foundations.md: request-ID masuk ke setiap baris log, distributed tracing (OpenTelemetry) dari HTTP → database → panggilan eksternal, perbaiki label Prometheus yang hardcoded, metrik RED per endpoint + antrean email, dan 5 alert dasar (5xx-surge, p95, email stuck, backup heartbeat, disk) — semua alert ke bot Telegram yang sudah ada.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion F1-B. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + tunjukkan cara saya membuka dashboard-nya.
```

- [x] **SESI 6 — Loop tahan-banting + playbook data besar (F1-C + F1-H)** ✅ 2026-07-08 —
  5 loop background diawasi (panic pulih sendiri + backoff + drain saat shutdown); email gagal-final
  kini punya tombol kirim-ulang admin (`POST /admin/emails/messages/{id}/resend`, drill dev sukses);
  playbook `docs/data-change-playbook.md` lahir + runner backfill resumable (`/backfill` di image app,
  metrik `surau_backfill_*`) dipakai backfill nyata `authors-name-search` — pencarian penulis
  `q=احمد` naik 19 → 209 hasil (192/192 nama ber-hamzah kini terjangkau ejaan polos);
  pause→resume terbukti tanpa kehilangan progres (drill dev: pause di 500/3.187 → resume →
  completed; endpoint publik tetap 200) dan drill dead-letter tuntas end-to-end (alert
  Telegram menyala → resend → email tiba → alert pulih).

```text
Kerjakan F1-C dan F1-H dari roadmap/phase-1-foundations.md: (F1-C) panic-recovery + backoff untuk 4 loop background, email gagal-final jadi dead-letter yang terlihat + bisa dikirim ulang via admin; (F1-H) playbook expand-contract + pola job backfill resumable ter-metrik yang dipakai minimal satu backfill nyata.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion keduanya. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 7 — Kontrak API terkunci + DB terlihat (F1-D + F1-G)** ✅ 2026-07-08 —
  kode error dibekukan (±100 entri; memperbaiki ejaan kalimat TAK BISA lagi mengubah kode —
  dijaga test kontrak AST, dibuktikan dgn uji mutasi); semua bentuk error kini ber-`code`+
  `request_id` (envelope kaya 409, 429 limiter ber-`retry_after`, 404 catch-all, error framework
  — bocor nilai panic ke body 500 ikut tertutup); `/v1/quran/search` tak lagi salah ber-cache;
  Postgres di-tuning per RAM host (slow-query log 200ms, pg_stat_statements) + dashboard DB
  5 panel + alert koneksi>80% → Telegram; aktif di dev & prod (pgbackrest check lulus).

```text
Kerjakan F1-D dan F1-G dari roadmap/phase-1-foundations.md: (F1-D) kode error jadi eksplisit di titik emit dengan tabel kompatibilitas beku ber-test (tanpa breaking change), envelope error kaya diberi code+request_id, dokumentasikan & selaraskan kebijakan cache PublicCache dengan edge worker; (F1-G) tuning ringan Postgres + slow-query log + exporter metrik DB ke dashboard.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 8 — CI yang bisa dipercaya (F1-E)** ✅ 2026-07-08 — akar flakiness ditemukan &
  diperbaiki (healthcheck db + readiness-wait eksplisit; bukan sleep); retry kini 1× dan
  TERPAKAINYA = alarm Telegram; job baru: round-trip 60 pasang migrasi (terbukti simetris) +
  smoke boot aplikasi nyata; test read-path editorial di-unskip (fixture mandiri); kode baru
  <70% coverage otomatis menggagalkan PR (PR ini sendiri 92,5%); rag-eval smoke tiap Senin
  02:00 WIB vs dev-api (non-gating; TEMUAN: buku korpus eval 797/7312/12876 belum pernah
  diimpor ke dev — smoke kini pre-check korpus dan lapor "dilewati" sampai buku diimpor
  [tugas data menyusul]). Bonus: bug "route tak dikenal dijawab 401" yang membuat PR #71
  ter-merge MERAH diperbaiki — soak 10-run hijau tanpa retry (bukti AC-1: lihat catatan
  PROGRAM.md). ⚠️ Tersisa 1 langkah manual Salman: tambah secrets `TELEGRAM_BOT_TOKEN` +
  `TELEGRAM_CHAT_ID` di GitHub repo Settings → Secrets agar alarm CI hidup.

```text
Kerjakan F1-E dari roadmap/phase-1-foundations.md: akar-masalahi flakiness integration test lalu turunkan retry 3→1 dengan alarm, tambah job CI round-trip migrasi (up-down-up), smoke-test bootstrap internal/app, unskip TestLiveAyahEditorialReadPath dengan fixture mandiri, ratchet coverage kode baru ≥70%, dan jadwalkan rag-eval smoke non-gating.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 9 — Kunci ganda untuk akun berkuasa (A-3)** ✅ 2026-07-09 (PR #74) — MFA TOTP
  (kode 6 digit dari aplikasi authenticator) + 10 recovery code sekali-pakai, WAJIB untuk admin;
  login admin kini minta kode setelah password (akun biasa TAK berubah). Aksi paling berbahaya
  (publish/unpublish, hapus aset final, ganti peran) minta kode LAGI meski sudah login ("step-up",
  segar 10 menit). Admin baru punya masa tenggang 7 hari untuk enroll sebelum terkunci dari aksi
  itu. Kehilangan HP: reset via OTP email + recovery code (semua sesi keluar), atau CLI darurat
  admin. Secret authenticator ter-enkripsi di database. ⚠️ 1 langkah manual Salman sebelum rilis
  prod (tag api-v berikutnya): set `MFA_ENCRYPTION_KEY` (32+ karakter acak) di `.env.production`
  VPS supaya rotasi kunci JWT nanti (A-4) tidak membuat authenticator yatim.

```text
Kerjakan A-3 dari roadmap/phase-2-auth.md: MFA TOTP + recovery codes (wajib untuk admin & scholar_reviewer sesuai default O-2-1), dan step-up (tantangan MFA ulang) untuk aksi destruktif kelas-atas. Alur login lama pengguna biasa tidak berubah.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-3. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + panduan singkat cara saya meng-enroll MFA akun saya.
```

- [x] **SESI 10 — Peran yang benar (A-1)** ✅ 2026-07-09 (PR #76) — otorisasi tak lagi memakai
  "cap admin" kasar: kini ada daftar **kapabilitas** (mis. "boleh review editorial", "boleh
  publish", "boleh kelola pengguna") dan tiap peran adalah bundel kapabilitas, dikunci di satu
  tempat + test kontrak. Dua peran baru: **curator** (kurasi entitas) & **scholar_reviewer**
  (satu-satunya, selain admin, yang boleh menyetujui klaim sensitif nanti di wiki — wajib MFA).
  Perilaku hak akses yang sudah ada tidak berubah; API ganti-peran menerima peran baru tanpa
  merusak apa pun. Test khusus menjaga tak ada lagi pengecekan peran "diam-diam" tersebar di
  kode. Empat kapabilitas masa-depan (klaim wiki, token layanan) sudah didaftarkan tapi belum
  dipakai rute mana pun — menunggu fase wiki/A-2. **Gerbang W1-auth (A-1+A-3) tuntas**; sisa
  A-4/A-5/A-6 opsional kapan saja, dan Gelombang 2 (Content Backbone) sudah boleh dimulai —
  PK-1 (lisensi) sudah dijawab (default aman) — Gelombang 2 terbuka penuh.

```text
Kerjakan A-1 dari roadmap/phase-2-auth.md: RBAC ber-kapabilitas dengan satu titik kebijakan, peran baru curator & scholar_reviewer, semua pemeriksaan peran pindah ke kapabilitas, matriks peran×kapabilitas dibekukan dengan test kontrak, API kelola peran diperluas aditif.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-1. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

> **✅ CHECKPOINT KEPUTUSAN — PK-1 (Lisensi) TERJAWAB (Salman, 2026-07-09): default aman (a/a/a).**
> Detail di roadmap/PROGRAM.md §5 PK-1 + CLAUDE.md. Gelombang 2 terbuka penuh, termasuk SESI 15 (B-4).

## GELOMBANG 2 — Content Backbone

- [x] **SESI 11 — Registry Citable Unit + pilot (B-1)** — ✅ **SELESAI 2026-07-09**: tabel
  `citable_units` + `citable_unit_lineage` + trigger penjaga tulis (satu jalur tulis lewat service
  `internal/usecase/unitregistry`); parser baru `readerutil.StructureMixedContent` (toleran-tag,
  granularitas paragraf); deriver kitab + reconcile deterministik (UUIDv5, lineage supersede/mint);
  2 job backfill (`citable-units-kitab-pilot` + `-rederive` drill) F1-H; hook `PublishPageDraft`;
  loop audit `citable_unit_audit` (default aktif, alert Telegram `sum(surau_citable_audit_violations)>0`).
  **Pilot lokal 4 buku eval nyata (797/7312/12876/22842) → 16.205 unit; re-run determinisme 100%
  (checksum registry MD5 identik, minted=0); audit 0 pelanggaran.** Semua AC B-1 terpenuhi (bukti
  di docs/citable-units.md). diff-cover 83,9%.

```text
ultracode. Kerjakan B-1 dari roadmap/phase-1b-content-backbone.md: registry Citable Unit bersama + satu service tulis + lifecycle/lineage, deriver kitab dari parser readerutil yang ada, pilot backfill pada set kecil buku nyata (termasuk buku 797), job audit nol-sitasi-menggantung terjadwal. Pakai playbook F1-H untuk backfill.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-1 (termasuk determinisme ID pada re-run). Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 12 — Alamat kanonik & resolusi (B-2)** — ✅ **SELESAI 2026-07-10**: grammar
  canonical Quran/kitab/range diratifikasi di `docs/anchors.md` tanpa mengubah 16.205 Anchor B-1;
  `GET /v1/anchors/resolve` menerima canonical, legacy `ayah_key`, `toc-{heading_id}+book_id`, dan
  `book_id+page_id`; lineage split/merge/multi-hop mengembalikan seluruh target aktif dan cycle
  masuk audit. Gerbang 20.500 unit aktif (50 warm-up + 500 sampel HTTP lokal) menghasilkan p50
  0,952 ms, **p95 1,277 ms**, max 3,535 ms. Tidak ada migrasi, backfill, perubahan frontend, atau
  rilis produksi.

```text
Kerjakan B-2 dari roadmap/phase-1b-content-backbone.md: spesifikasi grammar Anchor sebagai kontrak terdokumentasi + kapabilitas resolusi (anchor kanonik DAN legacy: ayah_key, toc-{heading_id}, page → unit aktif + redirect), p95 ≤50ms.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-2. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 13 — Registry rujukan silang (B-3)** — ✅ **SELESAI 2026-07-11**: registry umum
  menyimpan empat jenis hubungan, metode+confidence+5 status review, bukti berversi, dan jejak
  aktor; query publik incoming/outgoing hanya approved dengan `work_total` berbeda-kitab. Bridge
  Quran memakai dual-write atomik dan backfill pause/resume/rerun; parity `EXCEPT` dua arah serta
  integration test membuktikan endpoint dan embed reader lama identik, termasuk saat backfill
  parsial. Tautan kitab→kitab baru baru terlihat setelah approved. Uji 40.000 edge mencatat p95
  38,493 ms (campuran) dan 13,486 ms (heading berulang), di bawah target 200 ms; migrasi
  up→down→up hijau.

```text
Kerjakan B-3 dari roadmap/phase-1b-content-backbone.md: registry Cross-Reference umum (kind cites/quotes/explains/parallel, metode+confidence+review_status 5-nilai existing, jejak bukti) + migrasi paralel dari quran_book_references TANPA mengubah kontrak endpoint publik lama; backlink query dua arah.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-3. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 14 — Normalisasi kanonik + identitas mesin (B-5 + B-6)** — ✅ **SELESAI
  2026-07-11**: `search-key` v1 dibekukan dengan korpus emas bersama, Unicode 15.0, parity
  Go↔Python, dan gerbang immutable CI; seluruh teks turunan baru membawa versi sementara legacy
  yang tak terbukti tetap `NULL`. Registry `generation_runs` immutable kini wajib bagi seluruh
  enrichment machine aktif; generator, QA, importer atomik, aset final/draft, Citable Unit, dan
  Cross-Reference menjaga Provenance Class serta tuple model+prompt+run. API kurasi dan Swagger
  memaparkan identity typed. Migrasi replay-safe/up→down→up, 152 tes Python, integration HTTP
  penuh, dan live Go serial+race hijau.

```text
Kerjakan B-5 dan B-6 dari roadmap/phase-1b-content-backbone.md: (B-5) bekukan normalisasi Arab v1 dari quranutil.NormalizeKey + korpus vektor emas + gerbang kesetaraan Go↔Python di CI + kolom versi pada teks turunan; (B-6) identitas generation-run (model+versi-prompt+run) wajib untuk semua keluaran LLM baru di jalur enrichment yang aktif.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion keduanya. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 15 — Gerbang lisensi platform (B-4)** — ✅ **SELESAI 2026-07-11**: seluruh kitab
  kini memiliki License Status; Citable Unit mewarisi status Edition dengan override unit
  fail-closed. Publikasi/mutasi konten publik baru wajib literal `permitted` di API, importer, dan
  database; karya lama bertanda grandfather tetap tayang sampai audit `restricted` menurunkannya
  dari seluruh jalur publik. Laporan cakupan terlindungi menyediakan hitungan lengkap, antrean
  prioritas berdasarkan karya grandfathered dan penggunaan pembaca nyata, ETag, MFA segar, serta
  histori keputusan append-only. Kontrak publik aditif, cache fail-closed, RAG mengecualikan
  keluaran machine yang belum reviewed. Integration dari database kosong, live serial+race,
  migrasi up→down-all→up, worker 22/22+typecheck, dan diff-cover 73,6% semuanya hijau.

```text
Kerjakan B-4 dari roadmap/phase-1b-content-backbone.md sesuai jawaban PK-1 di roadmap/PROGRAM.md §5: adopsi enum license_status ke kitab (Work/Edition, pewarisan ke unit), publish BARU wajib permitted, karya existing di-grandfather sesuai keputusan O-1B-1, laporan cakupan lisensi untuk saya.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-4. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

---

## SETELAH SESI 15 — perpanjang antrean ini

- [x] **SESI 16 — Susun antrean gelombang berikutnya** — ✅ **SELESAI 2026-07-11**: antrean
  SESI 17–26 Gelombang 3 ditambahkan.

```text
Baca roadmap/PROGRAM.md §2 (Gelombang 3) + status centang di roadmap/SESI.md dan PROGRAM.md. Perbarui roadmap/SESI.md: tambahkan antrean prompt sesi siap-paste untuk Gelombang 3 (Q-1, Q-2, K-1 [ultracode], Q-4, Q-6, A-2, A-4, A-5, U-0, U-6) memakai format & aturan yang sama persis dengan sesi-sesi sebelumnya, termasuk checkpoint keputusan (PK-2 sebelum W4, O-4-2 untuk arah K-1). Tandai sesi yang layak "ultracode". Jangan mengubah bagian lain file.
```

## GELOMBANG 3 — Konten inti (Quran + industrialisasi kitab + benih retrieval)

- [x] **SESI 17 — Editorial Quran setara kitab (Q-1)** — ✅ **SELESAI 2026-07-11**:
  draft/publish+ETag, race 412, revisi+restore+origin, grandfather populated tanpa perubahan API
  publik, importer draft-default/explicit-publish, gerbang `published+permitted`, dan bukti tunggal
  jalur tulis selesai end-to-end.

```text
Kerjakan Q-1 dari roadmap/phase-3-quran.md: naikkan editorial surah dan ayah ke workflow standar kitab — draft/publish, ETag dengan 412/428/If-Match: *, riwayat revisi yang bisa di-restore, dan origin rest/import; grandfather data existing sebagai published tanpa mengubah isi atau API baca publik; importer menulis draft secara default dan hanya publish lewat flag eksplisit; konten publik tetap wajib published+permitted.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion Q-1, termasuk test concurrent edit dan pembuktian tidak ada jalur tulis yang melewati workflow. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 18 — Anchor + Citable Unit Quran + gerbang anti-tafsir (Q-2)** — ✅ **SELESAI
  2026-07-12**: memakai registry/resolver/lineage/bridge B-1–B-3 yang sudah hidup; Citable Unit
  ayah, terjemahan beratribusi+berlisensi, footnote tertaut, dan transliterasi kini deterministik;
  seluruh locator legacy FE resolvable (termasuk fallback+backfill page QPC v1 yang dipagari
  smoke deploy); lisensi/takedown fail-closed; rujukan approved lama tetap parity; generated
  column+indeks dan live gate yang dirujuk U-6 memastikan Quran tidak pernah
  eligible untuk retrieval interpretatif. Migrasi populated, rederive, integration, live+race,
  worker-cache bypass, dan `make pre-commit` hijau.

```text
Kerjakan Q-2 dari roadmap/phase-3-quran.md dengan mengaudit dan MEMAKAI hasil B-2/B-3 yang sudah hidup — jangan menduplikasi atau menulis ulang registry Anchor, resolver, maupun bridge Quran: lengkapi Citable Unit deterministik untuk ayah sebagai teks primer, rendering terjemahan per sumber+ayat beratribusi dan berlisensi, footnote tertaut, serta transliterasi; pastikan seluruh locator legacy FE yang tersisa resolvable; tegakkan di level data/indeks bahwa unit Quran tidak pernah eligible untuk retrieval interpretatif; pertahankan parity rujukan approved lama dan rujuk test anti-tafsir ini dari U-6.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion Q-2, memetakan mana yang sudah dipenuhi B-2/B-3 dan gap yang benar-benar tersisa. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

> **⚠️ CHECKPOINT KEPUTUSAN — O-4-2 (arah backfill K-1).** Bila ingin memilih sendiri,
> jawab sebelum SESI 19: (a) tafsir & syarah dulu; (b) urut trafik baca; atau (c) merata.
> Tidak menjawab = default aman PROGRAM.md §5 PK-6: **(a), dengan trafik sebagai tie-break**.

- [x] **SESI 19 — Industrialisasi seluruh katalog + migrasi sitasi RAG (K-1)**

```text
ultracode. Kerjakan K-1 dari roadmap/phase-4-kitab-editorial.md sesuai keputusan/default O-4-2: backfill 100% buku published dengan runner F1-H yang resumable dan ter-metrik, keraskan deriver pilot untuk footnote/quran_quote/HTML, petakan Provenance Class + generation identity B-6 + pewarisan License Status B-4, re-anchor knowledge_mentions dari halaman ke unit, lalu migrasikan sitasi book-RAG lewat dual-write legacy+unit di belakang flag → verifikasi parity → tukar default → pertahankan fallback legacy satu rilis; unit machine-unreviewed wajib tidak eligible secara struktural untuk retrieval interpretatif.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion K-1, termasuk determinisme 100%, audit sitasi menggantung = 0, anchor tetap resolve setelah edit, rollout tanpa big-bang, dan bukti 100% katalog published termaterialisasi. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 20 — Sitemap/feed + slug permanen Quran (Q-4)** — ✅ **SELESAI 2026-07-15**:
  sitemap lengkap dan feed incremental untuk surah/ayah id+en kini hanya memakai
  `published+permitted`, dengan `lastmod` efektif langsung dari database dan hreflang sesuai
  ketersediaan nyata; registry slug append-only memberi redirect 308 langsung A→C serta menolak
  reuse/penghapusan; laporan operator merinci cakupan ar/id/en × surah/ayah. Integration test
  database kosong membuktikan kesetaraan sitemap 100%, lastmod ≤5 menit, lisensi, RBAC, dan
  redirect lama; live invariant membuktikan lastmod persis, `missing_slug=0`, dan p95 680 µs.
  Migrasi up/down/up terlindungi, worker-cache, Swagger/docs, dan `make pre-commit` hijau.

```text
Kerjakan Q-4 dari roadmap/phase-3-quran.md: sediakan data sitemap/feed untuk halaman surah dan ayah dengan lastmod dari updated_at efektif, hreflang id/en sesuai ketersediaan, dan hanya konten published+permitted; formalkan registry slug sehingga perubahan slug menyisakan redirect permanen dan slug lama tetap resolvable; tambah laporan cakupan editorial per bahasa untuk operator.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion Q-4, termasuk 100% halaman published masuk sitemap, akurasi lastmod ≤5 menit, dan test redirect slug lama. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + tunjukkan URL sitemap dan laporan cakupannya.
```

- [x] **SESI 21 — Notifikasi Quran yang terbukti & sopan waktu (Q-6)** — ✅ **SELESAI
  2026-07-15**: semua push OneSignal kini mempunyai ledger delivery+attempt accepted/failed,
  alasan tersanitasi, UUID retry yang sama, metrik durable, dashboard, dan alert massal berbujet
  2m45s. Reminder memiliki dedupe harian atomik di atas cooldown 20 jam, lease lintas instance,
  quiet-hours timezone/DST fail-closed, serta retry yang berhenti tepat pada 21:00 lokal. Loop F1-C
  yang sama menangani wake event, recovery, backoff 30 detik–15 menit/`Retry-After`, panic, dan
  last-success sehat. Test lintas restart/crash, race 12 worker, dua tanggal lokal, DST New York,
  06:59/07:00/20:59/21:00, retry melewati batas, migrasi bolak-balik, counter restart, alert
  first-batch, integration, live PostgreSQL serial, dan `make pre-commit` semuanya hijau.

```text
Kerjakan Q-6 dari roadmap/phase-3-quran.md: simpan jejak delivery OneSignal accepted/failed beserta alasan, ekspor metrik+alert kegagalan massal ≤5 menit, tambah kunci dedupe idempoten per user+jenis+hari di atas cooldown 20 jam existing, terapkan quiet-hours per timezone user (default 21:00–07:00 waktu lokal untuk reminder non-kritis), dan masukkan loop reminder ke supervisi F1-C dengan recovery, backoff, serta metrik last-success.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion Q-6, termasuk test lintas restart untuk anti-duplikat dan batas waktu lokal pengguna. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + tunjukkan cara saya melihat jumlah reminder terkirim/gagal.
```

- [x] **SESI 22 — Identitas mesin + token layanan ber-scope (A-2)** — ✅ **SELESAI
  2026-07-15**: registry principal+scope dan token hash-at-rest ≤90 hari; revoke live tanpa restart;
  seluruh `/internal/*` ber-audit principal 90 hari; collab/rag-eval/enrichment dimigrasikan lewat
  overlap T1/T2 dan U-0 dibekukan tanpa secret; role DB extraction/importer/collab sempit dengan
  pending-only guard; runbook rotasi, Swagger, migrasi bolak-balik, unit/integration/Python/Node,
  live PostgreSQL serial+race, diff-cover 75,5%, dan `make pre-commit` hijau.

```text
Kerjakan A-2 dari roadmap/phase-2-auth.md: registry identitas layanan dengan nama principal, scope, kedaluwarsa ≤90 hari, hash-at-rest, dan pencabutan per-identitas; migrasikan collab-server (scope tulis-draft), runner eval (baca), otomasi enrichment HTTP, dan U-0 (kelola prompt-registry/budget) lewat overlap dua token tanpa downtime; audit setiap /internal/* dengan nama principal; formalkan role DB terpisah ber-grant sempit untuk pipeline ekstraksi pending-only dan importer, plus runbook rotasi.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-2, termasuk bukti token collab yang dicabut berhenti tanpa restart, test grants yang melarang pipeline mengubah review status, serta migrasi konsumen tanpa putus layanan. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [x] **SESI 23 — Rotasi JWT tanpa logout (A-4)** — ✅ **SELESAI 2026-07-16**:
  token baru membawa `kid`, verifier strict hanya menerima kunci lama+baru yang aktif, dan token
  no-`kid` yang masih hidup kompatibel hanya lewat `legacy_kid`. Hot reload memindahkan signer
  tanpa restart; rollback menjaga kedua verifier. Drill dev lalu prod membuktikan token lama tetap
  valid selama overlap dan `401` setelah TTL terlama+margin, sementara new-kid serta refresh sesi
  yang sama tetap `200`. Snapshot 33 sesi dev dan 35 sesi prod tidak mengalami revoke tanpa
  pengganti/401 tak terduga; canary bersih. PR #152+#153, rilis `api-v0.4.2`, runbook, CI penuh,
  dan artifact dev/prod lengkap; drill berikutnya paling lambat 2027-01-16.

```text
Kerjakan A-4 dari roadmap/phase-2-auth.md: tambahkan kid pada token baru, verifikasi terhadap himpunan kunci HS256 aktif lama+baru selama overlap, pindahkan penerbitan ke kunci baru seketika, lalu tolak kunci lama setelah masa hidup token terlama; tulis runbook dan lakukan drill rotasi pertama di dev lalu prod tanpa logout pengguna.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-4, termasuk kompatibilitas token tanpa kid yang masih hidup, strategi rollback aman, bukti token lama valid selama overlap lalu ditolak setelahnya, serta nol sesi terputus pada drill. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api dan hasil drill. Laporan akhir bahasa awam.
```

- [x] **SESI 24 — Refresh 14 hari + sesi berlabel perangkat (A-5)** — ✅ **SELESAI
  2026-07-18**: refresh kini memakai jendela diam 14 hari yang sliding; sesi aktif terus
  diperpanjang, tepat 14 hari diam ditolak tanpa alarm reuse palsu, dan sesi existing ber-expiry
  30 hari tetap kompatibel berdasarkan aktivitas terakhir. Daftar sesi mendapat `device_label`
  aditif yang mudah dibaca dengan fallback aman saat metadata kosong/tak dikenal, sementara raw
  field lama tetap ada. Rotasi atomik, deteksi reuse+revoke keluarga, revoke satu sesi, dan
  notifikasi perangkat baru tetap teruji. Swagger, panduan rilis FE/mobile, unit/race,
  integration Docker, live PostgreSQL, `make pre-commit`, serta canary auth dev disertakan; tidak
  ada migrasi schema.

```text
Kerjakan A-5 dari roadmap/phase-2-auth.md: ubah umur refresh dari 720h menjadi 336h (14 hari) sliding sehingga pemakaian aktif memperpanjang sesi dan token diam 14 hari ditolak; tambahkan label perangkat/klien yang terbaca manusia pada daftar sesi; pertahankan deteksi reuse, revoke per-sesi, rotasi, dan notifikasi perangkat baru; dokumentasikan perubahan perilaku untuk komunikasi rilis FE/mobile.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-5, termasuk test waktu untuk sesi aktif vs diam, kompatibilitas sesi existing, dan label perangkat yang aman saat metadata klien tidak lengkap. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 25 — Lapisan inferensi LLM bersama (U-0)**

```text
Kerjakan U-0 dari roadmap/phase-7-unified-rag.md di atas identitas layanan A-2 dan generation_runs B-6: bangun registry provider/model per tugas (rewrite/rerank/embed/jawab/judge), registry prompt + skema jawaban ber-versi di DB, metering token+biaya per panggilan ke trace F1-B, baseline/cap+alert 80% dengan penolakan anggun sesuai default aman O-7-3, cache yang aman, serta failover dua provider; semua call-site LLM aktif wajib memakai lapisan ini tanpa jalur ad-hoc.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion U-0, inventaris migrasi seluruh call-site, bukti setiap panggilan membawa task+model+prompt+run+token+biaya, test failover dua provider, serta perilaku saat cap terlampaui. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + tunjukkan cara saya melihat biaya harian dan pagarnya.
```

- [ ] **SESI 26 — Mulai eval-as-gate (U-6, tahap W3)**

```text
Mulai tahap W3 U-6 dari roadmap/phase-7-unified-rag.md: perluas harness BookRAG existing menjadi fondasi eval lintas-korpus; hidupkan sekarang kasus kitab + keamanan Quran dari Q-2 (anti-tafsir dan routing ayat→tafsir) + not-found + injeksi-lewat-konten, lalu siapkan kategori/seed id↔ar, validitas struktur, lensa-tak-meratakan, hadith, dan wiki untuk diaktifkan saat U-1/U-3/U-4/H-7/W-7 mendarat; prioritaskan asersi deterministik, pakai LLM-judge ber-rubrik-versi hanya untuk groundedness/ikhtilaf dengan sampling manusia; tampilkan pass-rate per kategori dan naikkan CI bertahap dari non-gating ke gate PR retrieval/release, tanpa reroute buku atau pensiun tree sebelum parity menang.
Masuk PLAN MODE dulu; rencana wajib memetakan setiap Acceptance Criterion U-6 menjadi (a) tercapai pada tahap W3 atau (b) sengaja ditunda dengan owner/dependensi eksplisit U-1/U-3/U-4/H-7/W-7, serta menyertakan mutation-test yang membuktikan gate menahan regresi. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang SESI.md dan catat progres tahap W3 di PROGRAM.md, tetapi JANGAN tandai U-6 selesai penuh sebelum seluruh dependensinya mendarat; merge dan verifikasi dev-api. Laporan akhir bahasa awam.
```

> **⚠️ CHECKPOINT KEPUTUSAN — PK-2 (Hadith): WAJIB sebelum W4; memblokir H-0 total.**
> Jawab `PK-2: default aman semua` atau pilih per poin di PROGRAM.md §5. Default: Bukhari lalu
> Muslim; hanya sumber machine-readable berlisensi jelas/terbuka; penomoran kanon yang paling
> lazim dikutip; grading internal-koleksi saja; terjemahan matn publik hanya yang reviewed.
> Verifikasi sumber legal tetap gerbang mutlak meski semua default dipakai.
