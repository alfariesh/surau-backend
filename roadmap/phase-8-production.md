# Fase 8 — Production Hardening: Observability, Performance, Security, DR

> **Terikat pada charter** (`roadmap/README.md`). Pembagian kerja yang sudah ditetapkan charter
> dihormati: quick-win eksistensial (WAL/PITR, drill restore pertama, dead-man backup, snapshot
> pra-deploy ke R2, runbook DIRTY) DIEKSEKUSI DINI di Fase 1 (F1-A) dan fondasi observability di
> F1-B — **Fase 8 memformalkannya menjadi PROGRAM operasi**: SLO & error-budget, kalender drill,
> kepemilikan gate, kapasitas & HA, keamanan operasional, dan ops untuk permukaan baru yang
> diciptakan fase konten (inferensi LLM U-0, eval-gate U-6, pgvector U-1/R-4, flywheel kurasi,
> importer staged K-0, permukaan MFA/RBAC Fase 2). Ditulis 2026-07-07.

---

## 1. Understanding — seberapa siap-produksi platform ini sebenarnya

### 1.1 Yang sudah layak (bukti)

- **Deploy disiplin**: dua VPS (main→dev, tag `api-v*`→prod + Release), snapshot DB pra-deploy,
  verifikasi `/readyz` + `/version` pasca-deploy, prune cache build; compose prod dengan
  healthcheck `pg_isready`, app bind 127.0.0.1 di belakang proxy TLS (Caddy/CF).
- **Backup harian nyata**: systemd timer 04:00 UTC, `pg_dump --format=custom` → zstd → R2 +
  checksum; **skrip `surau-pg-restore-check` sudah ada dan memvalidasi invariant**
  (books≥1, pages≥1, `quran_ayahs=6236`) — bahan drill yang tinggal dijadwalkan.
- **CI berlapis** (10 job) + baseline keamanan bersih (`govulncheck`/gosec); edge worker dengan
  kuota RAG & cache ber-versi; kuota/rate auth berbasis DB.
- **Pasca-F1/F2 (asumsi eksekusi)**: PITR + drill pertama + alert backup (F1-A), request-ID/
  tracing/alert dasar (F1-B), supervisi loop (F1-C), tuning & metrik DB (F1-G), MFA/RBAC/rotasi
  (Fase 2).

### 1.2 Celah produksi yang tersisa (temuan, diprioritaskan risiko uptime & integritas data)

| # | Temuan | Risiko |
|---|---|---|
| P1 | **Dump backup berisi PII (email, hash password) dan TIDAK dienkripsi client-side** sebelum naik ke R2 — kompromi bucket/kredensial rclone = bocor data pengguna | **Tinggi** — integritas kepercayaan; perbaikan murah |
| P2 | **Restore-check tak terjadwal** (skrip ada, tak pernah jalan otomatis); drill = sekali (F1-A) tanpa kadensi & skenario | Tinggi — "backup tanpa restore teruji = harapan" |
| P3 | **Tanpa auto-rollback deploy**: migrasi gagal → schema DIRTY → boot-loop sampai `migrate force` manual; runbook ada (F1-A), belum pernah DILATIH | Sedang–tinggi |
| P4 | **Single-instance Postgres & app**: kegagalan disk/host = downtime jam-an + kehilangan ≤RPO; belum ada keputusan HA ber-pemicu | Sedang (kemungkinan rendah, dampak tinggi) → keputusan O-8-1 |
| P5 | **Permukaan ops baru belum punya program**: biaya LLM (cap/alert/failover U-0 perlu dioperasikan), eval-gate perlu PEMILIK & ritual agar tetap hijau, backfill embedding = job mahal yang bisa jebol anggaran, antrean flywheel kurasi = kerja manusia berkelanjutan tanpa metrik kesehatan | Sedang — inilah "operasional" fase konten |
| P6 | **Collab sidecar tanpa watchdog** (crash = editor kehilangan realtime sampai ada yang sadar); nancy continue-on-error (vuln dep tak memblokir); tanpa kalender patch/rotasi; tanpa proses insiden tertulis | Sedang |
| P7 | Kapasitas buta: tanpa load-test, tanpa proyeksi disk (unit + embedding halfvec 1,5–3GB + arsip WAL), pool/tuning belum diverifikasi di beban | Sedang |

---

## 2. Vision — dari "jalan" menjadi "dioperasikan"

Platform disebut production-hardened ketika: (1) **janji tertulis** — SLO dengan error-budget yang
punya konsekuensi; (2) **pemulihan adalah rutinitas** — drill berkalender, bukan heroik; (3)
**biaya AI berpagar** — tak ada satu loop pun yang bisa menghabiskan anggaran bulanan dalam
semalam; (4) **gate kualitas berpemilik** — eval bukan skrip yang membusuk; (5) **keamanan
beririama** — patch/rotasi/review pada kalender, insiden punya prosedur; (6) **pekerjaan manusia
(kurasi) terlihat seperti sistem** — antreannya ber-SLA dan ber-alarm.

**Target yang saya tetapkan (rekomendasi, angka bukan TBD):**

| Dimensi | Target |
|---|---|
| SLO ketersediaan API publik | 99,5%/bulan sekarang; **99,9% hanya setelah HA (O-8-1)** — jangan menjanjikan yang arsitekturnya belum dibayar |
| Error budget | 0,5%/bulan; budget habis → bekukan fitur, kerjakan reliability (kebijakan tertulis) |
| Latensi | p95: baca <200ms, search <400ms, Ask <30s (budget U); kesegaran indeks ≤1 jam |
| DR | RPO ≤1 jam (PITR), RTO ≤4 jam; **drill kuartalan** + restore-check otomatis **mingguan**; recovery migrasi-DIRTY dilatih |
| Backup | Dead-man 26 jam (F1-A); **enkripsi client-side sebelum R2**; retensi bertingkat: harian 30 hari, mingguan 6 bulan, bulanan 2 tahun |
| Biaya LLM | Cap harian per O-7-3 (baseline→2×), alert 80%, circuit-breaker error/cost-rate; **backfill embedding butuh persetujuan bila estimasi >5% cap bulanan** |
| Eval-gate | Kategori keamanan = blokir mutlak; golden set tumbuh ≥1 kasus/bulan; audit LLM-judge sampling bulanan; break-glass terdokumentasi + post-mortem |
| Antrean kurasi | Umur antrean p95 ≤30 hari (selaras K-3), alert >45 hari; throughput mingguan terlihat |
| Keamanan | Vuln critical ≤72 jam, high ≤2 minggu; rotasi: JWT drill 6-bulanan (A-4), token layanan ≤90 hari (A-2), token CF/R2 kuartalan; review akses admin bulanan |
| Kapasitas | Load-test 3× puncak lulus budget, kuartalan; proyeksi disk 6 bulan di dashboard |

---

## 3. Gap & opportunity analysis (terurut risiko)

P0: P1 (enkripsi backup), P2 (kadensi drill + restore-check otomatis), P5-eval (gate berpemilik).
P1: P5-LLM (ops biaya/failover), P3 (latihan rollback), P7 (kapasitas/load-test), P6-collab/vuln.
P2: P4 (HA — keputusan ber-pemicu), P5-flywheel (metrik antrean), status page, P6-insiden formal.

---

## 4. Roadmap — inisiatif Fase 8

Urutan: **P8-1 & P8-2 dulu (janji + pemulihan), P8-4/P8-5 begitu U-0/U-6 hidup, sisanya paralel.**

### P8-1 — Program SLO & error-budget  *(P0, effort kecil–sedang)*

**Isi:** definisi SLO per permukaan (tabel §2) di atas metrik F1-B; dashboard SLO + laporan
mingguan otomatis ke kanal O-F1-1; kebijakan error-budget tertulis (budget habis → rilis fitur
berhenti, kerja reliability sampai pulih); review bulanan 30 menit (ritual, bukan rapat).
**AC:** dashboard SLO hidup dengan burn-rate; kebijakan tertulis dan pernah DIPAKAI sekali (nyata
atau simulasi); laporan mingguan tiba otomatis.
**DS:** Salman menerima "rapor kesehatan" mingguan — dan tahu kapan platform sedang berhutang
keandalan.

### P8-2 — DR sebagai rutinitas: kalender drill + restore otomatis + enkripsi backup  *(P0, effort kecil–sedang)*

**Isi:** `surau-pg-restore-check` dijadwalkan **mingguan otomatis** (gagal = alert); kalender
drill **kuartalan** dengan matriks skenario bergilir (disk hilang → restore penuh; korupsi →
PITR ke titik; migrasi gagal → DIRTY recovery + redeploy tag lama; kehilangan VPS → rebuild dari
runbook); **enkripsi client-side dump sebelum R2** (kunci di host backup, terpisah dari kredensial
R2; PII pengguna tidak boleh telanjang di bucket); retensi bertingkat (30 hari/6 bulan/2 tahun);
latihan rollback deploy pertama (P3) masuk drill #1; hasil tiap drill = laporan singkat (durasi
vs RTO, temuan).
**AC:** dua drill kuartalan berturut lulus target RPO/RTO; restore-check mingguan hijau ≥8 minggu;
dump di R2 terenkripsi (dibuktikan restore dari artefak terenkripsi); rollback migrasi-DIRTY
terlatih ≤RTO.
**DS:** pertanyaan "kalau server mati malam ini?" dijawab dengan laporan drill terakhir, bukan
keyakinan.

### P8-3 — Kapasitas & keputusan HA ber-pemicu  *(P1–P2, effort sedang)*

**Isi:** suite load-test (profil: baca reader, search, sync, Ask) dengan budget per kelas —
dijalankan kuartalan + sebelum rilis besar; verifikasi pool/tuning F1-G di beban; proyeksi disk
6-bulan (unit + embedding + WAL) di dashboard; **dokumen keputusan HA** dengan pemicu tertulis
(lihat O-8-1): tetap single+PITR sekarang → warm-standby saat pemicu tercapai; runbook promote
standby disiapkan bersamaan keputusan.
**AC:** load-test 3× puncak saat ini lulus semua budget; proyeksi kapasitas tampil; dokumen HA
ber-pemicu disetujui operator.
**DS:** pertumbuhan tidak lagi mengejutkan — grafiknya terlihat 6 bulan lebih dulu.

### P8-4 — Ops inferensi LLM (mengoperasikan U-0)  *(P0–P1 begitu U-0 hidup, effort sedang)*

**Isi:** dashboard biaya/token per-tugas per-hari; **cap keras** (per-request ≤8 panggilan; cap
harian per O-7-3) + alert 80% + penolakan anggun; **circuit-breaker** pada lonjakan error-rate/
cost-rate (runaway loop RAG atau batch ekstraksi TIDAK BISA menghabiskan anggaran semalam — uji
dengan loop yang sengaja dilepas); drill failover provider (matikan primer → sekunder melayani);
ops prompt-registry (naik versi = changelog + eval hijau WAJIB sebelum aktif — gerbang yang sama
dengan P8-5); ops cache (metrik hit-rate; invalidasi terikat versi indeks); **backfill embedding**
= job F1-H ber-meter dengan **pratinjau biaya** (estimasi token×harga; >5% cap bulanan → butuh
persetujuan eksplisit).
**AC:** loop runaway tersimulasi terhenti oleh cap + alert; drill failover lulus; tidak ada versi
prompt aktif tanpa eval hijau; backfill di atas ambang tertahan menunggu persetujuan.
**DS:** tagihan AI tidak pernah lagi bisa "kaget" — pagar, alarm, dan tombol setop semuanya nyata.

### P8-5 — Eval-as-gate sebagai program berpemilik  *(P0 begitu U-6 hidup, effort kecil–sedang)*

**Isi:** kepemilikan eksplisit — perubahan kategori/ambang gate disetujui operator; gate = check
wajib CI untuk PR yang menyentuh retrieval/prompt + gate rilis tag; **kategori pemblokir mutlak**
(anti-tafsir, injeksi, validitas-sitasi, ikhtilaf) tidak bisa di-skip; **break-glass** ada tapi
mahal (justifikasi tercatat + post-mortem wajib); ritual pemeliharaan: ≥1 kasus baru/bulan dari
feedback nyata (metrik U-8), review cakupan kategori kuartalan, audit sampling LLM-judge bulanan;
anti-flake: deterministik-dulu, retry eval = alarm bukan kruk (pola F1-E).
**AC:** PR yang sengaja dirusak tertahan gate di CI; satu break-glass simulasi meninggalkan jejak
lengkap; metrik pertumbuhan golden set berjalan 3 bulan berturut.
**DS:** kualitas jawaban agama dijaga oleh gerbang yang tidak bisa dilewati diam-diam — bahkan
oleh pengembangnya sendiri.

### P8-6 — Irama keamanan operasional  *(P1, effort sedang)*

**Isi:** update dependensi otomatis (bot mingguan) + **scan vuln menjadi pemblokir** (nancy lepas
dari continue-on-error, dengan allowlist ber-alasan); SLA patch (critical ≤72 jam, high ≤2
minggu); **kalender rotasi** dieksekusi (JWT drill 6-bulanan A-4; token layanan ≤90 hari A-2;
token CF/R2 kuartalan — termasuk yang tertunda lama); review akses admin & log peran bulanan;
tabletop insiden pertama (skenario: kredensial admin bocor) + runbook insiden (severity ladder,
komunikasi, siapa memutus apa — realistis untuk operasi satu-orang: eskalasi = kanal O-F1-1);
template post-mortem tanpa-menyalahkan.
**AC:** scan vuln memblokir PR ber-vuln critical (diuji); kalender rotasi kuartal pertama
tereksekusi penuh; satu tabletop selesai dengan temuan tertulis.
**DS:** keamanan berhenti menjadi proyek dan mulai menjadi kebiasaan berkalender.

### P8-7 — Ops mesin konten: importer, flywheel, collab, MFA  *(P1–P2, effort kecil–sedang)*

**Isi:** alur persetujuan staged-import (K-0) sebagai prosedur ops (siapa mereview diff, SLA
keputusan, drill re-import tetap hijau di CI); **kesehatan antrean flywheel & kurasi sebagai
metrik ops** (umur p95 ≤30 hari, alert >45 hari, throughput mingguan — mencakup antrean K-3/H-5/
W-2 dan registry-miss U-8); collab sidecar mendapat watchdog (healthcheck compose + restart
policy + alert crash); ops permukaan Fase 2 (metrik cakupan MFA peran wajib, runbook pemulihan
admin-terkunci [CLI existing — didokumentasikan], review perubahan peran di laporan mingguan).
**AC:** alert umur-antrean menyala di simulasi; collab yang dibunuh paksa pulih otomatis + alert;
cakupan MFA tampil di dashboard; satu re-import produksi berjalan lewat alur persetujuan.
**DS:** pekerjaan manusia (kurasi, review diff import) punya lampu indikator seperti mesin —
kelihatan saat menumpuk, bukan saat sudah meledak.

### P8-8 — Manajemen rilis & insiden  *(P2, effort kecil)*

**Isi:** smoke-suite pasca-deploy otomatis (health + beberapa kasus eval smoke + cek versi) di
kedua lingkungan; jalur rilis tetap tag-based (canary tidak relevan di single-instance —
keputusan sadar); status page publik = keputusan O-8-2; post-mortem dipakai untuk insiden nyata
pertama.
**AC:** deploy prod otomatis menjalankan smoke dan menolak menandai sukses bila gagal; satu
post-mortem nyata/simulasi terarsip.
**DS:** setiap rilis membuktikan dirinya sendiri dalam menit pertama — bukan menunggu keluhan.

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| P8-D1 | SLO 99,5% dipertahankan sampai HA dibayar; 99,9% hanya setelah O-8-1 memilih standby | Jangan menjanjikan sembilan yang arsitekturnya belum ada | Menjanjikan 99,9% di single-instance (fiksi) |
| P8-D2 | Dump backup dienkripsi client-side; kunci terpisah dari kredensial R2 | Dump berisi PII (email, hash) — kompromi bucket ≠ boleh bocor data | Mengandalkan enkripsi at-rest R2 saja (kredensial rclone = kunci semuanya) |
| P8-D3 | Restore-check otomatis mingguan + drill kuartalan ber-matriks skenario | Skrip validasi sudah ada; frekuensi murah, kepercayaan mahal | Drill tahunan (terlalu jarang untuk berubah jadi rutinitas) |
| P8-D4 | HA = keputusan ber-pemicu tertulis (O-8-1), bukan default; interim: PITR + drill | Biaya bulanan nyata vs kemungkinan rendah; pemicu membuat keputusan tidak emosional | Standby sekarang (bayar sebelum perlu); mengabaikan selamanya |
| P8-D5 | Backfill embedding butuh pratinjau-biaya + persetujuan >5% cap bulanan | Job satu-tombol yang bisa berharga jutaan rupiah harus punya rem | Backfill bebas (runaway budget); larangan total (menghambat R-4/U-1) |
| P8-D6 | Kategori eval pemblokir tidak bisa di-skip; break-glass mahal & tercatat | Gerbang yang bisa dilewati diam-diam = bukan gerbang | Gate advisory; skip bebas saat buru-buru |
| P8-D7 | Scan vuln jadi pemblokir dengan allowlist ber-alasan | continue-on-error = teater keamanan | Tetap advisory |
| P8-D8 | Canary tidak diadopsi di single-instance; kompensasi: smoke pasca-deploy + rollback terlatih | Canary butuh ≥2 instance; smoke+drill memberi 80% nilainya | Meniru canary secara artifisial |
| P8-D9 | Antrean kurasi diperlakukan sebagai sistem produksi (SLA umur + alert), bukan backlog editorial | Flywheel U-8 & janji K-3 bergantung pada throughput manusia yang terlihat | Menganggapnya urusan editorial semata |

**Asumsi:** P8-A1 — F1-A/B/C/G dan Fase 2 A-2/A-4 sudah/akan mendarat sebelum program ini
diformalkan (Fase 8 memformalkan, tidak menggantikan); P8-A2 — U-0/U-6 hidup sebelum P8-4/P8-5
diaktifkan penuh; P8-A3 — operator menjawab O-8-1/O-8-2/O-8-3 (default aman berlaku); P8-A4 —
satu-operator tetap kenyataan (semua ritual dirancang ≤2 jam/minggu total).

> **Conflicts with charter: tidak ada.** Charter §4.3 menugaskan F8 "SLO formal, HA (O4),
> kapasitas, keamanan lanjutan, biaya LLM, eval-as-CI-gate permanen" — semuanya dieksekusi di
> sini; pembagian dengan F1 (quick-win dieksekusi dini, F8 memformalkan) mengikuti keputusan
> charter §4.1 poin 3 apa adanya.

---

## 6. Interfaces (seams)

**Fase 8 MENGEKSPOS:** SLO & error-budget yang mengikat semua fase (rilis berhenti saat budget
habis); kalender drill & runbook teruji (DR, rollback, failover, rotasi); gerbang rilis final
(eval-gate + smoke pasca-deploy) tempat semua fase konten menggantungkan kualitasnya; pagar biaya
AI (cap/breaker/persetujuan-backfill) yang dipatuhi U-0/enrichment; metrik kesehatan antrean
manusia (kurasi/diff-import) untuk operator.

**Fase 8 MENGONSUMSI:** F1-A/B/C/G (fondasi DR & observability), Fase 2 A-2/A-4 (rotasi &
identitas mesin), U-0/U-6 (meter biaya & harness eval), K-0 (alur staged-import), metrik antrean
W-2/K-3/H-5/U-8, kanal O-F1-1, keputusan operator O4/O-7-3/O-8-1..3.

---

## 7. Open decisions (operator-owned)

**O-8-1 — Postur HA & anggarannya (penajaman O4 charter).**
*Kenapa penting:* satu-satunya cara jujur menuju 99,9% dan RTO menit-an; biaya bulanan nyata.
*Opsi:* (a) **tetap single-VPS + PITR + drill** (Rp0 tambahan; RTO jam-an) dengan **pemicu
tertulis** untuk naik kelas (mis. pengguna aktif harian melewati ambang, ada kesepakatan SLA
pihak ketiga, atau pendapatan menutup biaya); (b) warm-standby VPS kecil sekarang (biaya bulanan;
RTO menit; jalur ke 99,9%); (c) managed Postgres (biaya tertinggi, ops termudah).
*Rekomendasi:* (a) → (b) saat pemicu tercapai. *Default aman:* (a).

**O-8-2 — Status page publik.**
*Kenapa penting:* transparansi insiden vs permukaan komunikasi baru yang harus dirawat.
*Opsi:* (a) belum — komunikasi insiden via kanal produk; (b) status page sederhana.
*Rekomendasi:* (a) sampai ada konsumen API eksternal/B2B. *Default aman:* (a).

**O-8-3 — Anggaran waktu manusia untuk antrean kurasi (bahan bakar flywheel).**
*Kenapa penting:* SLA umur-antrean ≤30 hari (P8-7) mustahil tanpa jam manusia yang dialokasikan;
ini keputusan sumber daya, bukan teknis. *Opsi:* (a) **4–8 jam/minggu** dari editor yang ada;
(b) rekrut kurator paruh-waktu saat antrean stabil >ambang; (c) program relawan ter-moderasi
(butuh governance W-6 matang). *Rekomendasi:* (a) sekarang, (b) berdasarkan data throughput.
*Default aman:* (a).

---

## 8. Conformance

Fase ini menjaga kedua prinsip charter pada dimensi operasional: gate eval yang memblokir mutlak
(P8-5) adalah penegakan RAG Safety yang tak bisa dilewati kelelahan atau tenggat; pagar biaya
(P8-4) memastikan guardrail tidak pernah "dimatikan sementara demi hemat"; enkripsi backup (P8-2)
melindungi amanah data pengguna; dan metrik antrean kurasi (P8-7) memastikan janji-janji Domain
Integrity (review manusia atas klaim, grading, rujukan) tidak mati pelan-pelan karena antreannya
tak terlihat.

## 9. North-star fit

North-star yang sudah menyala di Fase 7 harus TETAP menyala pada jam 3 pagi: fase ini adalah
perbedaan antara demo yang mengesankan dan amanah yang dijaga — janji tertulis, pemulihan yang
dilatih, biaya yang berpagar, gerbang yang berpemilik, dan pekerjaan manusia yang terlihat.
Dengan ini, "wiki Islam yang bisa dipercaya" berlaku bukan hanya untuk isi jawabannya, tetapi
untuk keberadaan platformnya sendiri.
