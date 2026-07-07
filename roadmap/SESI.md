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

- [ ] **Buat bot Telegram**: buka Telegram → cari **@BotFather** → ketik `/newbot` → ikuti
  langkahnya → simpan **token**-nya. Lalu chat bot barumu sekali (tekan Start). Token ini akan
  diminta sesi S1 — berikan dengan menaruhnya di file `.env` saat sesi memintanya (JANGAN paste
  token di chat).

---

## GELOMBANG 0 — Selamatkan Data

- [ ] **SESI 1 — Commit fondasi + enkripsi backup + drill restore (E1+E2)**

```text
Sebelum mulai: commit folder roadmap/ dan CLAUDE.md ke main (pesan: "chore(roadmap): program plan fase 0-9 + CLAUDE.md").
Lalu kerjakan E1+E2 dari roadmap/PROGRAM.md §1: (E1) enkripsi client-side dump backup sebelum naik R2, kunci terpisah dari kredensial bucket; (E2) drill restore pertama memakai ops/backup/surau-pg-restore-check + jadwalkan restore-check mingguan otomatis + dead-man alert 26 jam — semua alarm & laporan ke BOT TELEGRAM (keputusan O-F1-1; minta saya isi token bot & chat ID via .env saat dibutuhkan).
Rujukan detail: roadmap/phase-1-foundations.md inisiatif F1-A dan roadmap/phase-8-production.md P8-2.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, checklist Definition of Done di CLAUDE.md, centang milestone di roadmap/PROGRAM.md dan roadmap/SESI.md, merge, verifikasi dev-api. Akhiri dengan laporan bahasa awam + kirim satu pesan Telegram uji ke saya.
```

- [ ] **SESI 2 — PITR + snapshot deploy aman (E3+E6)**

```text
Kerjakan E3+E6 dari roadmap/PROGRAM.md §1: (E3) WAL-archiving/PITR ke R2 sehingga RPO turun dari 24 jam ke ≤1 jam, dibuktikan demonstrasi pemulihan point-in-time; (E6) snapshot pra-deploy ikut diunggah ke R2 dengan retensi 7 hari.
Rujukan: roadmap/phase-1-foundations.md F1-A (bagian WAL/PITR & snapshot).
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done di CLAUDE.md, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 3 — Jinakkan importer buku (E4) — PALING PENTING**

```text
Kerjakan E4 dari roadmap/PROGRAM.md §1: importer Shamela (cmd/import-books) saat ini MENGHAPUS permanen halaman/heading yang hilang di sumber baru dan ikut menghapus kerja editorial (defect D1 kritis). Ubah menjadi staged-diff + tombstone + persetujuan eksplisit, dan TULIS SUITE TEST-NYA DULU sebelum mengubah perilaku.
Rujukan: roadmap/phase-4-kitab-editorial.md §1.2 defect D1/D6 + inisiatif K-0 poin importer; playbook di roadmap/phase-1-foundations.md F1-H bila perlu.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion K-0 bagian importer. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam. Setelah sesi ini selesai, larangan re-import di CLAUDE.md boleh kamu perbarui statusnya.
```

- [ ] **SESI 4 — Tutup celah DoS publik + bersih-bersih identitas repo (E5 + F1-F)**

```text
Kerjakan E5 dari roadmap/PROGRAM.md §1 (clamp offset publik ke 10k, paginasi endpoint headings dengan default besar yang aman, escape metakarakter ILIKE di semua search reader — defect D2/D4/D5 di roadmap/phase-4-kitab-editorial.md §1.2) DAN F1-F dari roadmap/phase-1-foundations.md (rename module path dari github.com/evrone/go-clean-template ke identitas Surau, hapus kode mati amqp_rpc/nats_rpc, tambah pemindaian secret di CI).
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion kedua item. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

## GELOMBANG 1 — Fondasi

- [ ] **SESI 5 — Mata & telinga sistem (F1-B)**

```text
Kerjakan F1-B dari roadmap/phase-1-foundations.md: request-ID masuk ke setiap baris log, distributed tracing (OpenTelemetry) dari HTTP → database → panggilan eksternal, perbaiki label Prometheus yang hardcoded, metrik RED per endpoint + antrean email, dan 5 alert dasar (5xx-surge, p95, email stuck, backup heartbeat, disk) — semua alert ke bot Telegram yang sudah ada.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion F1-B. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + tunjukkan cara saya membuka dashboard-nya.
```

- [ ] **SESI 6 — Loop tahan-banting + playbook data besar (F1-C + F1-H)**

```text
Kerjakan F1-C dan F1-H dari roadmap/phase-1-foundations.md: (F1-C) panic-recovery + backoff untuk 4 loop background, email gagal-final jadi dead-letter yang terlihat + bisa dikirim ulang via admin; (F1-H) playbook expand-contract + pola job backfill resumable ter-metrik yang dipakai minimal satu backfill nyata.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion keduanya. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 7 — Kontrak API terkunci + DB terlihat (F1-D + F1-G)**

```text
Kerjakan F1-D dan F1-G dari roadmap/phase-1-foundations.md: (F1-D) kode error jadi eksplisit di titik emit dengan tabel kompatibilitas beku ber-test (tanpa breaking change), envelope error kaya diberi code+request_id, dokumentasikan & selaraskan kebijakan cache PublicCache dengan edge worker; (F1-G) tuning ringan Postgres + slow-query log + exporter metrik DB ke dashboard.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 8 — CI yang bisa dipercaya (F1-E)**

```text
Kerjakan F1-E dari roadmap/phase-1-foundations.md: akar-masalahi flakiness integration test lalu turunkan retry 3→1 dengan alarm, tambah job CI round-trip migrasi (up-down-up), smoke-test bootstrap internal/app, unskip TestLiveAyahEditorialReadPath dengan fixture mandiri, ratchet coverage kode baru ≥70%, dan jadwalkan rag-eval smoke non-gating.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 9 — Kunci ganda untuk akun berkuasa (A-3)**

```text
Kerjakan A-3 dari roadmap/phase-2-auth.md: MFA TOTP + recovery codes (wajib untuk admin & scholar_reviewer sesuai default O-2-1), dan step-up (tantangan MFA ulang) untuk aksi destruktif kelas-atas. Alur login lama pengguna biasa tidak berubah.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-3. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam + panduan singkat cara saya meng-enroll MFA akun saya.
```

- [ ] **SESI 10 — Peran yang benar (A-1)**

```text
Kerjakan A-1 dari roadmap/phase-2-auth.md: RBAC ber-kapabilitas dengan satu titik kebijakan, peran baru curator & scholar_reviewer, semua pemeriksaan peran pindah ke kapabilitas, matriks peran×kapabilitas dibekukan dengan test kontrak, API kelola peran diperluas aditif.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion A-1. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

> **⏸️ CHECKPOINT KEPUTUSAN — sebelum lanjut ke Gelombang 2:** jawab **PK-1 (Lisensi)** di
> roadmap/PROGRAM.md §5. Caranya: buka sesi Claude, ketik *"PK-1: saya pilih default aman"* (atau
> pilihanmu sendiri per poin). Sambil menunggu keputusanmu, SESI 11–12 tetap boleh jalan.

## GELOMBANG 2 — Content Backbone

- [ ] **SESI 11 — Registry Citable Unit + pilot (B-1)** *(besar — pakai ultracode)*

```text
ultracode. Kerjakan B-1 dari roadmap/phase-1b-content-backbone.md: registry Citable Unit bersama + satu service tulis + lifecycle/lineage, deriver kitab dari parser readerutil yang ada, pilot backfill pada set kecil buku nyata (termasuk buku 797), job audit nol-sitasi-menggantung terjadwal. Pakai playbook F1-H untuk backfill.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-1 (termasuk determinisme ID pada re-run). Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 12 — Alamat kanonik & resolusi (B-2)**

```text
Kerjakan B-2 dari roadmap/phase-1b-content-backbone.md: spesifikasi grammar Anchor sebagai kontrak terdokumentasi + kapabilitas resolusi (anchor kanonik DAN legacy: ayah_key, toc-{heading_id}, page → unit aktif + redirect), p95 ≤50ms.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-2. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 13 — Registry rujukan silang (B-3)**

```text
Kerjakan B-3 dari roadmap/phase-1b-content-backbone.md: registry Cross-Reference umum (kind cites/quotes/explains/parallel, metode+confidence+review_status 5-nilai existing, jejak bukti) + migrasi paralel dari quran_book_references TANPA mengubah kontrak endpoint publik lama; backlink query dua arah.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-3. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 14 — Normalisasi kanonik + identitas mesin (B-5 + B-6)**

```text
Kerjakan B-5 dan B-6 dari roadmap/phase-1b-content-backbone.md: (B-5) bekukan normalisasi Arab v1 dari quranutil.NormalizeKey + korpus vektor emas + gerbang kesetaraan Go↔Python di CI + kolom versi pada teks turunan; (B-6) identitas generation-run (model+versi-prompt+run) wajib untuk semua keluaran LLM baru di jalur enrichment yang aktif.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion keduanya. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

- [ ] **SESI 15 — Gerbang lisensi platform (B-4)** *(⚠️ butuh PK-1 sudah dijawab)*

```text
Kerjakan B-4 dari roadmap/phase-1b-content-backbone.md sesuai jawaban PK-1 di roadmap/PROGRAM.md §5: adopsi enum license_status ke kitab (Work/Edition, pewarisan ke unit), publish BARU wajib permitted, karya existing di-grandfather sesuai keputusan O-1B-1, laporan cakupan lisensi untuk saya.
Masuk PLAN MODE dulu; rencana wajib menyebut cara memenuhi setiap Acceptance Criterion B-4. Setelah saya setujui: kerjakan sampai tuntas — branch fitur, test, Definition of Done, centang PROGRAM.md & SESI.md, merge, verifikasi dev-api. Laporan akhir bahasa awam.
```

---

## SETELAH SESI 15 — perpanjang antrean ini

- [ ] **SESI 16 — Susun antrean gelombang berikutnya**

```text
Baca roadmap/PROGRAM.md §2 (Gelombang 3) + status centang di roadmap/SESI.md dan PROGRAM.md. Perbarui roadmap/SESI.md: tambahkan antrean prompt sesi siap-paste untuk Gelombang 3 (Q-1, Q-2, K-1 [ultracode], Q-4, Q-6, A-2, A-4, A-5, U-0, U-6) memakai format & aturan yang sama persis dengan sesi-sesi sebelumnya, termasuk checkpoint keputusan (PK-2 sebelum W4, O-4-2 untuk arah K-1). Tandai sesi yang layak "ultracode". Jangan mengubah bagian lain file.
```
