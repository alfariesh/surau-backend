# PROGRAM — Satu Rencana Eksekusi Lintas-Domain (Fase 9, Konsolidasi)

> **Status:** hasil rekonsiliasi SEPULUH dokumen roadmap (charter + fase 1, 1B, 2–8), semuanya
> dibaca-ulang dari disk dan diinventarisasi 2026-07-07. Kedelapan nota konflik antar-dokumen
> berstatus RESOLVED (lihat §3); tidak ada konflik terbuka tersisa.
> **Cara pakai (untuk Salman):** (1) baca §1 — pekerjaan penyelamatan data yang jalan SEKARANG;
> (2) jawab antrean keputusan §5 sesuai tenggat-gelombangnya (semuanya punya default aman —
> diam = default berlaku); (3) jalankan sesi implementasi mengikuti §6 lalu §2. Dokumen fase
> tetap sumber detail (AC/DS per inisiatif); PROGRAM.md hanya memuat URUTAN dan KEPUTUSAN.
> Perbarui dokumen ini setiap milestone selesai (living document).

---

## 1. GELOMBANG 0 — Jalur EXECUTE-EARLY: "Selamatkan Data" (jangan menunggu apa pun)

Kriteria masuk jalur ini: risiko **kehilangan data, kebocoran data, atau DoS publik** dengan
effort kecil. Semuanya adalah irisan dari inisiatif fase (rujukan di kurung) yang ditarik ke
depan — bukan pekerjaan baru.

| ID | Pekerjaan | Kenapa tidak boleh menunggu | Asal |
|---|---|---|---|
| E1 ✅ | **Enkripsi client-side dump backup sebelum naik R2** (kunci terpisah dari kredensial bucket) — **SELESAI 2026-07-07 (S1)**: age keypair, artefak `.dump.zst.age`, kunci di host + escrow offline | Dump berisi PII — email & hash password pengguna; kompromi kredensial rclone hari ini = bocor data pengguna | P8-2 (P8-D2) |
| E2 ✅ | **Drill restore pertama + `surau-pg-restore-check` terjadwal mingguan + dead-man alert 26 jam** — **SELESAI 2026-07-07 (S1)**: drill #1 lulus di prod 241 dtk (lihat docs/backup-restore-r2.md §Drill log); timer mingguan + watchdog + alarm Telegram hidup di prod & dev | Backup tanpa restore teruji = harapan; kegagalan backup hari ini SENYAP | F1-A / P8-2 |
| E3 ✅ | **WAL-archiving / PITR ke R2** → RPO 24 jam menjadi ≤1 jam — **SELESAI 2026-07-07 (S2)**: pgBackRest (archive_timeout=300 ⇒ RPO ≤5 mnt) di prod & dev; drill PITR lulus 82 dtk (docs/backup-restore-r2.md §Drill log #2) | Satu-satunya kegagalan yang bisa mengakhiri produk dalam sehari | F1-A (charter G2) |
| E4 ✅ | **Importer Shamela jadi staged-diff + tombstone, suite test DITULIS DULU** — **SELESAI 2026-07-07 (S3)**: hard-delete dihapus dari kode; stage→review→approve (drift guard); suite `TestLiveBookImport*` (8 skenario) jadi gerbang CI; **larangan re-import DICABUT** | Defect D1 (KRITIS): re-import dulunya hard-delete + cascade MENGHAPUS kerja editorial & meng-orphan data pengguna | K-0 (D1/D6) |
| E5 ✅ | **Perbaikan DoS publik**: clamp offset (cap 10k), paginasi endpoint headings, escape metakarakter ILIKE — **SELESAI 2026-07-08 (S4)**: reader clamp 10k; headings limit default/max 200 (aditif, total = count penuh); escapeLike di SEMUA situs ILIKE + query dibatasi 200 rune; terverifikasi live di dev | Endpoint publik tanpa auth bisa dipakai membebani DB dengan satu URL | K-0 (D2/D4/D5) |
| E6 ✅ | **Snapshot pra-deploy ikut diunggah ke R2** (retensi 7 hari) — **SELESAI 2026-07-07 (S2)**: `surau-predeploy-snapshot` (terenkripsi age) dipanggil kedua workflow deploy; prune 7 hari lokal+R2 | Jaring pengaman rollback hari ini hidup ±20 menit di disk lokal VPS | F1-A |

**Keputusan yang dibutuhkan gelombang ini:** hanya **O-F1-1** (kanal alarm — lihat §5.0),
karena mulai E2 sistem akan mulai "berteriak" dan harus ada telinga yang mendengarnya.

---

## 2. PROGRAM GELOMBANG W1–W7

Prinsip urutan: **critical path** = F1-H → B-1..B-3 → K-1 → U-1..U-3 → GA Ask. Dua jalur
paralel yang tidak boleh tertinggal: **A-1 → W-0** (tanpa RBAC, gerbang klaim sensitif wiki
mati) dan **H-0..H-7** (tanpanya, Ask GA tanpa hadith). Effort per gelombang memakai label
dokumen fase (kecil/sedang/besar); tidak ada tanggal — gerbang-keluar yang menentukan pindah
gelombang.

### W1 — Fondasi (F1 + awal F2)
**Isi:** F1-B ✅ **SELESAI 2026-07-08 (S5)** (observability inti — request-ID+trace_id di semua log request-scoped; OTel HTTP→pgx→webapi ke Tempo; RED+email+loop metrik; Grafana /grafana + 6 alert Telegram; kelima alert DIBUKTIKAN menyala via simulasi dev; AC trace-follow terbukti end-to-end) · F1-C ✅ **SELESAI 2026-07-08 (S6)** (supervisi loop: recover+backoff+jitter+drain 5 loop via `internal/app/loop.go`, panic tak lagi mematikan proses [AC1: loop_test.go]; email dead-letter → endpoint admin `POST /admin/emails/messages/{id}/resend` [AC2 terverifikasi end-to-end di dev]) · F1-H ✅ **SELESAI 2026-07-08 (S6)** (playbook
`docs/data-change-playbook.md` + runner backfill resumable `internal/backfill` + CLI `cmd/backfill` ber-metrik `surau_backfill_*`; dipakai backfill NYATA `authors-name-search` — recall pencarian penulis TERUKUR di dev: `q=احمد` 19→209 hasil; 192/192 penulis ber-hamzah di NAMA kini 100% terjangkau ejaan polos (angka 1.087 sebelumnya keliru — itu termasuk kecocokan biografi via lengan lama); pause/resume terbukti live-test + drill dev (pause di 500/3.187 → resume → completed, endpoint publik 200 sepanjang drill) — PRASYARAT W2 TERPENUHI) · F1-F (rename module + kode mati — kerjakan SEBELUM
kode baru menumpuk) · F1-D ✅ **SELESAI 2026-07-08 (S7)** (kontrak error terkunci: registry kode beku ±100 entri + test kontrak AST yang menolak edit kalimat tanpa registrasi [AC-1 dibuktikan mutation-test]; SEMUA bentuk error ber-`code`+`request_id` termasuk envelope kaya 409, 429 limiter ber-`retry_after`, 404 catch-all, ErrorHandler framework [AC-2] — sekaligus menutup bocor nilai panic ke body 500; 8 envelope legacy dibekukan ber-test; `/v1/quran/search` kini `no-store`; kebijakan PublicCache + sumber kebenaran versi cache worker didokumentasikan sebagai kontrak) · F1-E ✅ **SELESAI 2026-07-08 (S8, PR #72)** (CI tepercaya: akar flakiness integration DIPERBAIKI — db healthcheck + readiness-wait eksplisit di TestMain, bukan sleep; retry 3→1 dan attempt>1 = alarm Telegram+warning [F1-D8]; job round-trip migrasi up→down→up dengan diff schema [60 pasang migrasi terbukti simetris]; smoke bootstrap `TestLiveAppBootstrap` + kontrak 5 loop ber-unit-test membunuh 0% `app.go` [AC-2]; `TestLiveAyahEditorialReadPath` di-unskip dengan fixture mandiri; ratchet coverage kode baru ≥70% via `cmd/diffcover` gagal-kan PR + total dilaporkan per-PR [AC-3, dogfood 92,5%]; rag-eval smoke cron mingguan non-gating vs dev-api [F1-D9]; BONUS: bug 401-vs-404 catch-all yang membuat PR #71 merge merah diperbaiki — Auth per-subtree; catatan: secrets TELEGRAM_* di GitHub menunggu Salman) · F1-G ✅ **SELESAI 2026-07-08 (S7)** (tuning Postgres ber-env di compose [dev 2GB default / prod 4GB override], slow-query log 200ms, pg_stat_statements, postgres-exporter + 5 panel DB + alert koneksi>80% → Telegram; review pool: app 10 + collab 5 vs max_connections 50) ·
**A-3 (MFA + step-up)** ✅ **SELESAI 2026-07-09 (S9, PR #74)** (TOTP + recovery codes sekali-pakai
[hash-at-rest] + step-up: TOTP wajib admin [helper `entity.RoleRequiresMFA`, scholar_reviewer 1
baris di A-1]; login akun ber-MFA membalas challenge `{mfa_required,mfa_token}` lalu `/v1/auth/mfa/verify`
[AC login aditif — akun non-MFA tak berubah, ber-test]; **AC-1** grace enrollment [`users.mfa_enforced_from`
backfill admin + stamp API/CLI, default 7 hari] → 403 `mfa_enrollment_required` [integration];
**AC-2** step-up segar [`auth_sessions.mfa_verified_at` disalin saat rotasi, default 10 mnt] pada
publish/unpublish/hapus-final-asset [grup editorialAdmin] + `PATCH /admin/users/role` → 403
`mfa_step_up_required` [integration]; **AC-3** recovery code sekali-pakai [konsumsi atomik, live +
concurrent double-spend + integration]; reset kehilangan-HP = OTP email + recovery code → sesi dicabut;
CLI darurat `cmd/reset-user-mfa`; secret TOTP ter-enkripsi AES-256-GCM [`pkg/cryptobox`]; 8 kode error
beku; Swagger+docs FE diperbarui; **nota infra:** `make test` kini `-coverpkg` → gerbang F1-E mengkredit
coverage lintas-paket [total jujur ~28→~50%, diff A-3 77,7%]; ⚠️ set `MFA_ENCRYPTION_KEY` di prod sebelum
A-4) · **A-1 (RBAC ber-kapabilitas + scholar_reviewer)** ✅ **SELESAI 2026-07-09 (S10, PR #76)**
(otorisasi pindah ke SATU titik `internal/policy`: 7 kapabilitas bernama + matriks beku peran×kapabilitas
[admin=superset]; peran baru `curator` + `scholar_reviewer` [migrasi CHECK round-trip; API kelola-peran
aditif]; `middleware.RequireCapability` menggantikan RequireRoles di 3 gerbang [perilaku live byte-identik:
review-editorial{editor,admin}, publish-production{admin}+step-up, manage-users{admin}]; **AC-1** hanya
scholar_reviewer+admin lolos approve-sensitive-claim [test]; **AC-2** tak ada `role==` cek-akses di luar
policy [test AST kontrak, terbukti menangkap pelanggaran suntikan]; **AC-3** matriks beku golden-twin +
didokumentasikan di docs/auth-frontend.md; audit mencatat kapabilitas; RoleRequiresMFA dipindah ke policy
[scholar_reviewer kini mandat MFA]; 4 kapabilitas [curate-entities/approve-neutral&sensitive-claim/
manage-service-tokens] dideklarasikan utk W-0/W-5/W-6/A-2 tapi belum ada rute; diff-coverage 98,4%).
**Gerbang keluar:** request-ID→trace hidup + 5 alert teruji; playbook F1-H terpakai ≥1 backfill
nyata; CI 10-run hijau tanpa retry ✅ **TERBUKTI 2026-07-08** (workflow integration-soak, 10/10
lulus dalam 10,5 menit tanpa satu pun retry:
https://github.com/alfariesh/surau-backend/actions/runs/28953164078); login admin ber-MFA ✅
**TERBUKTI 2026-07-09 (S9)**; matriks kapabilitas beku-ber-test ✅ **TERBUKTI 2026-07-09 (S10)**.
**Gerbang keluar W1 (auth) TERPENUHI** — sisa A-4/A-5/A-6 (JWT dual-key, refresh harden, alert anomali)
menyusul kapan saja; W2 (Content Backbone) dapat mulai.
**Keputusan:** O-2-1 (cakupan MFA — cepat, lihat PK-3).

### W2 — Content Backbone (1B)
**Isi:** B-1 ✅ **SELESAI 2026-07-09 (SESI 11)** (registry `citable_units`+`citable_unit_lineage`
bersama + satu service tulis `internal/usecase/unitregistry` ditegakkan trigger DB [C2]; parser baru
`readerutil.StructureMixedContent` toleran-tag → granularitas paragraf; deriver kitab + reconcile
deterministik [UUIDv5 natural-key, ordinal dicetak-sekali, lineage supersede/mint/rescue]; 2 job
backfill F1-H [`citable-units-kitab-pilot` + `-rederive` drill determinisme]; hook `PublishPageDraft`;
loop audit `citable_unit_audit` [default aktif] + alert Telegram `sum(surau_citable_audit_violations)>0`;
**pilot 4 buku eval nyata → 16.205 unit, re-run determinisme 100% [checksum identik], audit 0
pelanggaran; semua AC terpenuhi**; kontrak di `docs/citable-units.md`; diff-cover 83,9%) → B-2 (Anchor
+ resolusi + legacy) → B-3 (Cross-Reference umum + bridge rujukan Quran); paralel: B-5 (normalisasi v1
+ vektor emas + gerbang kesetaraan CI), B-6 (identitas generation-run); B-4 (lisensi platform +
gerbang publish kitab) — PK-1 ✅ terjawab (default aman a/a/a).
**Gerbang keluar:** determinisme pilot ≥99,5% (target 100%); 100% anchor legacy resolvable ≤50ms;
rujukan approved lama setara via registry baru; suite kesetaraan normalisasi hijau di dua runtime.
**Aturan keras (charter D2):** Fase hadith/wiki DILARANG mendesain model datanya sebelum
B-1..B-3 terkunci.

### W3 — Konten inti (Quran + industrialisasi kitab + benih retrieval)
**Isi:** Q-1 (editorial Quran → standar kitab) ∥ Q-2 (deklarasi Anchor ayat + unit Quran +
test anti-tafsir) ∥ **K-1 (industrialisasi Citable Unit seluruh katalog + migrasi sitasi RAG —
critical path, effort besar)** ∥ Q-4 (SEO sitemap/slug) + Q-6 (keandalan notifikasi);
sisa F2: A-2 (identitas mesin ber-scope), A-4 (dual-key JWT + drill), A-5 (refresh 336h);
**U-0 (lapisan inferensi) + U-6 (eval-harness → gate) DIMULAI DI SINI** — Fase 7 mensyaratkan
keduanya "sejak hari pertama", dan enrichment kitab langsung ikut menumpang U-0.
**Gerbang keluar:** editorial Quran ber-ETag+revisi; test eligibilitas anti-tafsir lulus (dirujuk
U-6); 100% buku published ter-unit dengan sitasi dual-write terverifikasi; eval berjalan di CI
(non-gating → gating bertahap); setiap panggilan LLM ber-meter.
**Keputusan:** O-4-2 (prioritas korpus — PK-6) mengarahkan urutan backfill K-1.

### W4 — Perluasan konten + kelahiran hadith
**Isi:** K-2 (Work/Edition) ∥ K-3 (rujukan tafsir→ayat + antrean kurasi) ∥ K-4 (SEO kitab) ∥
K-9 (loop editorial) ∥ K-6 (span entitas — setelah K-1) ∥ Q-3/Q-5/Q-7 (riwayah, posisi audio,
reading plan) ∥ **H-0 → H-1 (fondasi korpus hadith + unit + reader — menunggu PK-2)**.
**Gerbang keluar:** rujukan eksplisit ≥95% auto-link ber-confidence; sitemap kitab hidup;
koleksi hadith pertama browsable dengan importer staged teruji.

### W5 — Hadith dalam + Wiki
**Isi:** H-2 (Grading Assertions) ∥ H-3 (isnad + antrean perawi→entitas) → H-4/H-5
(terjemahan; takhrij & rujukan silang) → H-6/H-7 (produk; serah-terima RAG dengan sitasi
ber-grading); **W-0 (service kurasi + governance — butuh A-1)** → W-1 (taksonomi + jembatan
Work) → **W-2 (disambiguasi: SLA top-500 perawi SEBELUM span hadith dibuka luas)** → W-3
(halaman entitas + backlink + SEO) ∥ W-4 (relasi + derived-from-isnad).
**Gerbang keluar:** hadith dgn dua penilaian tampil ter-atribusi keduanya; transisi status
knowledge_* mustahil via SQL langsung; top-500 perawi terkurasi; halaman entitas dengan backlink
≥2 korpus hidup.
**Keputusan:** O-6-1 (scholar-reviewer — memblokir klaim sensitif/W-5-rijal; PK-3), O-8-3 (jam
kurasi — memblokir SLA antrean; PK-4).

### W6 — Retrieval terpadu (capstone)
**Isi:** U-1 (indeks dua-himpunan + embedding ber-gerbang-mini-eval) → U-2 (resolver + traversal
registry + flywheel) → U-3 (EvidencePack + composer + skema jawaban) → U-4 (preferensi & lensa)
∥ U-5 (Search terpadu) → U-7 (guardrail runtime); parity-reroute endpoint book-RAG lama + pensiun
tree per-buku; W-7 (grounding handoff); sisa produk: K-5/K-7/K-8, Q-8/Q-9, W-5 (jarh wa ta'dil),
W-6 (dispute), U-8 (tier riset + flywheel matang).
**Gerbang keluar (GA Ask):** eval ≥50 kasus, pass-rate ≥90%, validitas sitasi 100%, kategori
keamanan (anti-tafsir/injeksi/ikhtilaf/lensa) lulus mutlak; jawaban lintas-korpus dengan grading
menempel; budget panggilan sesuai target (ber-jangkar ≤2, terbuka ≤4).
**Keputusan:** PK-5 (materi sensitif & suara platform) HARUS terjawab sebelum GA Ask.

### W7 — Formalisasi produksi (F8 penuh)
**Isi:** P8-1 (SLO & error-budget) · P8-2 (kadensi drill formal — lanjutan E-lane) · P8-3
(kapasitas + keputusan HA) · P8-4 (ops inferensi: cap/breaker/failover-drill/persetujuan-backfill)
· P8-5 (eval-gate berpemilik + break-glass) · P8-6 (irama keamanan: vuln-blocking, kalender
rotasi, tabletop) · P8-7 (ops mesin konten: antrean ber-SLA, watchdog collab, cakupan MFA) ·
P8-8 (rilis & insiden).
**Gerbang keluar:** dua drill kuartalan berturut lulus RPO/RTO; loop-runaway tersimulasi terhenti
oleh cap; laporan SLO mingguan berjalan; satu re-import produksi lewat alur persetujuan.

---

## 3. Rekonsiliasi konflik — SEMUA RESOLVED (ratifikasi program)

| # | Konflik/nota | Resolusi (sudah dieksekusi in-session) |
|---|---|---|
| 1 | Charter D7 ("embedding menyusul") vs Fase 7 v2 (hybrid = pilar inti kelas-terbuka) | Charter D7 diberi nota revisi 2026-07-07; pgvector-di-Postgres tetap; R-D4/R-D5 lama resmi digantikan U-D3/U-D4 |
| 2 | Charter menyuruh bangun "publish multi-aset atomik" vs bukti F4 (SUDAH atomik) | Charter §4.3 dikoreksi; scope → audit-trail re-publish (K-9); urgensi pindah ke importer D1 |
| 3 | F5 H-D5 (penulis pertama knowledge_entities) vs kepemilikan F6 | Diterima F6 sebagai input resmi (W-D1); antrean H-3 = beban kerja W-2 ber-SLA |
| 4 | Seam Reader Experience tidak ada di charter §4.1 | F3 §1.3 dinyatakan owner-of-record; F4/F5 mengonsumsi — tercatat di checklist charter |
| 5 | Entitas Work wiki vs Work/Edition katalog K-2 | Jembatan 1:1 (W-D4) — satu identitas karya, tanpa duplikasi |
| 6 | Fase 7 ditulis-ulang vs kontrak fase korpus | Semua kontrak dikonsumsi verbatim (H-7, K-D4, K-3/H-5, W-7); EvidencePack ditambahkan ke glosarium charter |
| 7 | Gate backfill embedding (P8-D5) vs kapabilitas U-1 | Konsisten: U-1 kapabilitas, P8-4 rem operasional (pratinjau-biaya >5% cap → persetujuan) |
| 8 | Normalisasi Arab dua-implementasi (charter D9 / 1B C5 / F4 K-D9/D8) | Satu semantik: Go v1 beku + vektor emas + gerbang kesetaraan CI; reader melebur ke C5 |

**Pernyataan program:** tidak ada konflik terbuka; edit-edit charter saling konsisten
(diverifikasi inventaris disk 2026-07-07).

## 4. Pemeriksaan konformans lintas-domain — tidak ada drift

- **RAG Safety** (rantai penegakan): 1B C2 (unit Quran dikecualikan statis dari kelayakan
  interpretatif) → Q-2 (test anti-tafsir yang dirujuk eval) → U-1 (indeks interpretatif TANPA
  Quran secara konstruksi) → `quote_only` di perakitan EvidencePack → kategori eval anti-tafsir
  = blokir mutlak (U-6/P8-5). Tidak ada fase yang diam-diam menafsirkan ayat.
- **Ikhtilaf tidak diratakan**: grading per-otoritas tanpa label global (H-D4/H-D8) → grading+
  isnad WAJIB ikut sitasi (H-7, diuji struktural) → panel ikhtilaf tak bisa disembunyikan lensa
  personalisasi mana pun (U-D9, 5 invarian) → kategori eval "lensa-tak-meratakan" (U-6).
  Personalisasi TIDAK PERNAH menyentuh eligibility retrieval.
- **Provenance & mesin**: identitas generation-run wajib (B-6) → mesin-unreviewed keluar dari
  kelayakan interpretatif (K-D4, termasuk ringkasan yang dulu ikut ranking) → grading tak pernah
  dihasilkan LLM (H-D8) → transisi status knowledge hanya via service ber-audit (W-0) → klaim
  approved wajib sitasi sumber (W-D8). Konsisten ujung-ke-ujung.

---

## 5. ANTREAN KEPUTUSAN TERPADU (33 keputusan → 1 item segera + 7 paket)

Setiap keputusan muncul TEPAT SATU KALI. Diam = **default aman** berlaku. Urutan = apa yang
paling memblokir.

### 5.0 — O-F1-1 — Kanal alarm & laporan — ✅ **TERJAWAB (Salman, 2026-07-07): TELEGRAM**
Alarm (backup gagal, error melonjak) DAN laporan (drill, SLO mingguan) dikirim via **bot
Telegram**. Implementasi S1/E2 dan paket alert F1-B memakai kanal ini; email tetap tersedia
sebagai cadangan teknis bila bot gagal terkirim.

### PK-1 — Lisensi & Konten Existing — ✅ **TERJAWAB (Salman, 2026-07-09): DEFAULT AMAN (a/a/a)**
Ketiga sub-poin = opsi (a): **(O3)** audit lisensi per-karya, hanya `permitted` yang publish,
`unknown` TAK PERNAH dipublish baru, prioritas karya paling dibaca; **(O-1B-1)** karya yang telanjur
publik tetap tayang selama audit, takedown segera HANYA yang teraudit `restricted`; **(O-4-4)**
terjemahan mesin `generated` tetap tampil di reader publik BERLABEL + investasi antrean review (RAG
tetap dikecualikan darinya). Memblokir B-4 (SESI 15) — kini terbuka. Konsumen: B-4 (enum
license_status + gerbang publish), K-1 (backfill katalog), dan setiap jalur publish konten.

Gabungan **O3 + O-1B-1 + O-4-4**. Pertanyaan intinya: bagaimana kita memperlakukan ribuan kitab
Shamela yang status lisensinya belum jelas, dan konten terjemahan mesin yang belum direview?
1. **Postur audit lisensi (O3):** (a) audit per-karya, hanya `permitted` yang publish — bersih
   tapi lambat; (b) publish sambil audit — cepat, berisiko hukum/etika. **Rek & default: (a)**,
   prioritas karya paling dibaca; yang `unknown` tidak pernah dipublish BARU.
2. **Nasib karya yang telanjur publik selama audit (O-1B-1):** (a) tetap tayang, takedown segera
   hanya yang teraudit `restricted`; (b) turunkan semua `unknown` sekarang; (c) sembunyikan dari
   search/RAG, tautan langsung tetap. **Rek & default: (a).**
3. **Terjemahan mesin `generated` di reader publik (O-4-4):** (a) tetap tampil BERLABEL + investasi
   antrean review (RAG sudah dikecualikan darinya); (b) hanya reviewed yang publik (katalog
   menyusut drastis); (c) opt-in pengguna. **Rek & default: (a).**

### PK-2 — Paket Hadith  *(jawab sebelum W4; memblokir H-0 total)*
Gabungan **O1 + O-5-1 + O-5-2 + O-5-3 + O-5-4**.
1. **Scope koleksi pertama (O1):** (a) Bukhari lalu Muslim — nilai cepat, risiko kecil; (b) Kutub
   as-Sittah sekaligus; (c) tunggu kemitraan data. **Rek & default: (a).**
2. **Sumber data & lisensinya (O-5-4) — GERBANG MUTLAK:** (a) hanya sumber machine-readable
   berlisensi jelas/terbuka; (b) + negosiasi lisensi untuk pelengkap bernilai tinggi. **Rek: (a)
   mulai, (b) menyusul. Default: (a)** — tanpa sumber legal, H-0 memang harus menunggu.
3. **Edisi kanon penomoran per koleksi (O-5-1):** (a) penomoran yang paling lazim dikutip dunia;
   (b) ikut penomoran sumber data apa adanya. **Rek & default: (a)** (edisi lain jadi alias).
4. **Otoritas grading pertama (O-5-2) — pilihan manhaj yang terlihat publik:** (a) hanya
   penilaian internal-koleksi; (b) (a) + 1–2 otoritas takhrij yang paling luas dipakai,
   atribusi ketat. **Rek: (b)** dengan framing "melaporkan, bukan menilai". **Default: (a).**
5. **Terjemahan matn publik (O-5-3):** (a) hanya yang reviewed (lebih ketat dari kitab —
   sengaja); (b) generated berlabel seperti kitab. **Rek & default: (a).**

### PK-3 — Tim & Kuasa  *(O-2-1 di W1; O-6-1 sebelum W5)*
Gabungan **O-2-1 + O-2-2 + O-6-1 + O-6-3 + O-4-3**.
1. **Cakupan wajib MFA (O-2-1):** (a) wajib admin + scholar-reviewer, opsional editor; (b) wajib
   semua peran ber-kuasa. **Rek & default: (a).**
2. **Delegasi kuasa publish (O-2-2):** (a) tetap admin-only sampai RBAC+MFA+audit berjalan
   ≥1 bulan, lalu delegasi ke curator terpilih; (b) delegasi segera. **Rek & default: (a).**
3. **Siapa scholar-reviewer (O-6-1) — gerbang klaim sensitif:** (a) 1–2 reviewer tepercaya yang
   sudah mereview konten Surau; (b) dewan kecil ber-SOP; (c) tunda → kelas klaim sensitif tetap
   TERTUTUP. **Rek: (a)→(b). Default: (c)** — tertutup sampai ada nama.
4. **Kontribusi publik wiki (O-6-3):** (a) lapor-saja ber-rate-limit; (b) usul-suntingan. 
   **Rek & default: (a).**
5. **Anotasi pengguna (O-4-3):** (a) privat-saja dulu; (b) bisa dibagikan. **Rek & default: (a).**

### PK-4 — Anggaran & Sumber Daya  *(baseline di W3; cap & HA di W6–W7)*
Gabungan **O4 + O-7-3 + O-8-1 + O-8-3 + O-3-3**.
1. **Selera biaya umum (O4):** (a) hemat: single VPS + PITR, ekstraksi bertahap; (b) menengah:
   + replika/managed DB + budget ekstraksi tetap. **Rek: (a)→(b) saat tumbuh. Default: (a).**
2. **Pagar biaya LLM (O-7-3):** (a) cap keras sekarang; (b) ukur baseline sebulan → cap 2×
   baseline. **Rek & default: (b)** (metering jalan dulu).
3. **Postur HA (O-8-1):** (a) single+PITR dengan PEMICU tertulis untuk naik kelas; (b)
   warm-standby sekarang; (c) managed DB. **Rek & default: (a)** — 99,9% hanya dijanjikan
   setelah (b)/(c) dibayar.
4. **Jam manusia untuk antrean kurasi (O-8-3) — bahan bakar flywheel:** (a) 4–8 jam/minggu dari
   editor yang ada; (b) rekrut kurator paruh waktu berdasarkan data throughput. **Rek: (a) lalu
   (b). Default: (a).**
5. **Penambahan korpus terjemahan/qari Quran (O-3-3):** (a) tetap yang ada; (b) shortlist
   terkurasi + verifikasi lisensi. **Rek: (b). Default: (a).**

### PK-5 — Materi Sensitif & Suara Platform  *(jawab sebelum GA Ask, W6)*
Gabungan **O2 + O-7-1 + O-7-2 + O-6-2**. Inti: bagaimana platform berbicara tentang perkara
yang diperselisihkan — di jawaban AI dan di halaman wiki.
1. **Framing materi kontensius (O2):** (a) semua tampil dengan atribusi ketat + framing
   "melaporkan, bukan memutus", kategori sensitif dipaksa multi-pendapat tanpa sintesis;
   (b) sembunyikan kategori tertentu dari RAG (browse-only). **Rek: (a) + penandaan kategori.
   Default: (b) untuk takfir/sekte, (a) untuk fiqh.**
2. **Derajat hadith di jawaban Ask (O-7-1):** (a) semua approved tampil berlabel; (b) default
   sahih/hasan + toggle "tampilkan semua derajat". **Rek: (b) untuk Ask, (a) untuk Search.
   Default: (b).**
3. **Pertanyaan fatwa/hukum personal (O-7-2):** (a) paparkan posisi ulama ter-atribusi +
   disclaimer + arahan konsultasi — tidak pernah memutus. **Rek & default: (a).**
4. **Tampilan jarh wa ta'dil & label mazhab (O-6-2):** (a) tampil penuh per-otoritas dengan
   framing pelaporan; (b) kedalaman hanya di halaman entitas, tidak di tooltip/lintasan baca.
   **Rek: (a) dengan batas penyajian (b). Default: (b).**

### PK-6 — Produk (semua ber-default-aman; jawab santai, kecuali #1 berguna cepat)
1. **Prioritas korpus backfill/SEO/tautan (O-4-2):** rek & default: **kategori tafsir & syarah
   dulu**, tie-break trafik. *(Berguna sejak W3 — mengarahkan K-1.)*
2. **Teks multi-riwayah Quran (O-3-1):** rek & default: **tunda** — atribusi riwayah audio jalan
   sekarang (Q-3), arsitektur sudah siap bila kelak dibuka.
3. **Word-by-word & tajwid (O-3-2):** rek: pilot word-by-word SETELAH backbone bila metrik
   belajar menunjukkan kebutuhan. Default: belum.
4. **Bentuk reading-plan (O-3-4):** rek & default: **versi ringan deterministik** (target
   tanggal → kuota → on/off-track).
5. **Kedalaman audiobook kitab (O-4-1):** rek & default: resume + identitas/lisensi qari dulu;
   forced-alignment = evaluasi setelah metrik dengar terlihat.
6. **Lensa mazhab (O-7-4):** rek & default: **opt-in eksplisit di pengaturan, TIDAK ditanya di
   onboarding**, framing selalu berlabel, panel ikhtilaf tak pernah hilang.
7. **Gaya jawaban default (O-7-5):** rek & default: **standar** (tersimpan per-user setelah
   dipilih).

### PK-7 — Kecil-operasional (kapan saja)
1. **Lingkup auto-link mesin wiki (O-6-4):** rek & default: manual-first untuk orang; auto hanya
   alias unik Term/Work ≥0.95 ber-sampling.
2. **Status page publik (O-8-2):** rek & default: belum — sampai ada konsumen API eksternal.
3. **Bahasa berikutnya (O5):** rek & default: tidak menambah — kunci kualitas id, en menyusul.

---

## 6. START HERE — lima sesi implementasi pertama

| Sesi | Isi | Definisi selesai |
|---|---|---|
| **S1** ✅ | E1 (enkripsi backup) + E2 (drill restore #1 + restore-check mingguan + dead-man) — **SELESAI 2026-07-07** | Restore dari R2 terbukti + laporan drill pertama di tangan Salman; dump terenkripsi; backup gagal = alarm |
| **S2** ✅ | E3 (WAL/PITR) + E6 (snapshot pra-deploy → R2) — **SELESAI 2026-07-07** | Pemulihan point-in-time ≤1 jam terdemonstrasikan |
| **S3** ✅ | E4 (importer staged — TEST DULU, lalu staged-diff+tombstone) — **SELESAI 2026-07-07** | Fixture re-import destruktif TIDAK BISA menghapus editorial tanpa diff yang disetujui; **larangan re-import dicabut** |
| **S4** ✅ | E5 (clamp offset, paginasi headings, escape ILIKE) + F1-F (rename module, hapus kode mati) — **SELESAI 2026-07-08**: module = github.com/alfariesh/surau-backend; kode mati amqp/nats sudah tiada (sisa docs dibersihkan); gitleaks aktif di CI (gerbang DIBUKTIKAN dgn PR dummy-secret yang ditolak); docs/module-conventions.md lahir | Fuzz publik aman; repo beridentitas Surau |
| **S5** ✅(F1-B) | F1-B (request-ID→log, tracing, 5 alert) **SELESAI 2026-07-08** → lanjut F1-H (playbook backfill, sesi berikutnya) | Satu request tertelusur ujung-ke-ujung ✅; playbook siap → **masuk W2 (B-1 pilot)** |
| **S6** ✅ | F1-C (supervisi 5 loop + dead-letter email resend admin) + F1-H (playbook + backfill resumable `authors-name-search`) — **SELESAI 2026-07-08** | Panic di loop tak menjatuhkan proses (test); email gagal-final bisa dikirim ulang (drill dev end-to-end); backfill nyata di-pause→resume tanpa kehilangan progres di dev; **B-1 (SESI 11) tinggal pakai playbook** |
| **S7** ✅ | F1-D (kontrak error terkunci + kontrak cache) + F1-G (baseline Postgres + metrik DB) — **SELESAI 2026-07-08** | Ubah kalimat error ≠ ubah kode (test kontrak, dibuktikan mutasi); semua error ber-code+request_id; DB tak lagi kotak hitam: slow-query log + dashboard DB + alert koneksi; tuning tercatat di compose/env & aktif di dev+prod |

**Sambil S1–S5 berjalan, Salman menjawab:** **PK-1** (memblokir W2), lalu mencicil PK-2
(memblokir W4) dan PK-3 poin 1. *(O-F1-1 sudah terjawab: Telegram — lihat §5.0.)*

### Template prompt sesi implementasi (copy-paste, isi bagian [kurung])

```text
Kerjakan [S1 — E1+E2 / atau satu inisiatif, mis. F1-B] dari roadmap/PROGRAM.md.

1. Baca dulu: roadmap/PROGRAM.md (posisi & keputusan) + dokumen fase terkait:
   [mis. roadmap/phase-1-foundations.md bagian F1-A + roadmap/phase-8-production.md P8-2].
2. MASUK PLAN MODE dulu. Roadmap hanya memberi WHAT/WHY/Acceptance-Criteria — detail
   implementasi (HOW) kamu temukan sendiri dari kode. Rencanamu WAJIB menyebut bagaimana
   SETIAP Acceptance Criterion inisiatif ini dipenuhi, dan boleh menyimpang dari asumsi
   roadmap bila kode menunjukkan jalan lebih baik (catat sebagai nota).
3. Setelah kusetujui: implementasi + test di branch fitur, lalu jalankan checklist
   "Definition of Done" di CLAUDE.md sebelum menyatakan selesai — termasuk mencentang
   milestone di roadmap/PROGRAM.md dan verifikasi di dev-api setelah merge.
```

Aturan ukuran sesi: **satu baris tabel START HERE atau satu inisiatif fase per sesi** — besar
boleh, campur-aduk jangan. Untuk pekerjaan sapuan lebar (mis. K-0 multi-defect, K-1 backfill
katalog), tambahkan kata **"ultracode"** di prompt agar sesi memakai orkestrasi multi-agen.

---

## 7. Living document

Perbarui PROGRAM.md pada tiap gerbang-gelombang: centang isi gelombang, catat keputusan yang
terjawab (pindahkan dari §5 ke catatan keputusan), dan tambahkan pelajaran yang mengubah urutan.

**Pelajaran S1 (2026-07-07):** asumsi "backup harian sudah berjalan" ternyata hanya benar untuk
VPS dev — **VPS prod sama sekali tidak punya backup** sampai sesi S1 memasangnya (stack backup
dipasang Juni saat masih satu VPS; saat dev/prod dipisah, prod tidak ikut). Moral: klaim
infrastruktur di dokumen ≠ keadaan mesin — verifikasi langsung di host sebelum menandai aman.
Prefix R2 kini terpisah: `postgres/prod/` vs `postgres/dev/`. Kedua host sementara memakai token
R2 yang sama — rotasi ke token ter-scope per-host masuk antrean P8-6/backlog rotasi kredensial.
Dokumen fase (roadmap/phase-*.md) tetap sumber kebenaran untuk AC/DS per inisiatif — jangan
duplikasi ke sini. Konflik baru antar-dokumen di masa depan mengikuti pola yang sama: nota
"Conflicts with charter" di dokumen fase + rekonsiliasi di sini.
