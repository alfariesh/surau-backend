# Fase 1 — Fondasi & Cross-Cutting Solidity

> **Terikat pada charter** (`roadmap/README.md`): bar "definition of solid" §2.3, glosarium §2.5,
> dan keputusan D1/D2/D12 dipakai apa adanya. Quick-win ops yang charter tandai eksistensial
> (drill restore + WAL-archiving) DIMASUKKAN ke fase ini, bukan Fase 8.
> Ditulis 2026-07-06 setelah dua audit lanjutan khusus fondasi (kontrak error/konkurensi;
> test/CI/ops) di atas eksplorasi Fase 0.

---

## 1. Understanding — kondisi fondasi hari ini (berbasis bukti)

### 1.1 Yang sudah kuat — dan TIDAK butuh investasi besar

Temuan audit lanjutan justru **menaikkan** penilaian atas beberapa area yang tadinya diragukan:

- **Kontrak error 100% konsisten di titik emit.** Seluruh 19 file controller memakai satu helper
  `errorResponse()` (`internal/controller/restapi/v1/error.go:15`) — ±409 call, nol bentuk ad-hoc
  `fiber.Map`. Setiap error membawa `error/code/message/request_id`; 429 menyertakan header
  `Retry-After`. Disiplin status code bersih (tidak ada 200-berisi-error; tidak ada 5xx yang
  seharusnya 4xx pada sampel).
- **Envelope list seragam.** 26 endpoint list memakai pola `{items,total}` (beberapa memakai kunci
  legacy `users`/`projects`/`feedbacks` — pola sama, nama kunci beda; dicatat sebagai legacy di
  F1-D, bukan diubah).
- **HTTP caching sudah ada dan benar** — middleware `PublicCache`
  (`internal/controller/restapi/middleware/cache.go`): weak ETag SHA256 atas body,
  `Cache-Control: public, max-age=300, stale-while-revalidate=86400`, `Last-Modified` dari
  `updated_at`, dan `304 If-None-Match`. Terpasang di endpoint baca publik reader.
- **Optimistic locking ditegakkan atomik di SQL** (predikat `WHERE updated_at = $expected` dalam
  satu statement — bukan check-then-write), parser `If-Match` menangani `W/`, `*`, dan multi-value
  (`v1/etag.go`).
- **Disiplin migrasi kuat.** 57 pasang up/down; pola `NOT VALID` → `VALIDATE` dengan komentar
  preflight; tiga migrasi kompleks terbaru saya periksa reversibel bersih.
- **Kelas bug konkurensi yang pernah ada sudah dikeraskan dengan pola + test**: double-count
  progress (fix `1edef99`: `FOR UPDATE` pada CTE baseline + live-test race) dan import audio
  setengah-commit (fix `86833eb`: satu transaksi utuh + live-test rollback FK). Editorial
  multi-write bertransaksi; upsert `ON CONFLICT` idempoten di mana-mana.
- **Layering bersih dan pantas dipertahankan** (charter D1): `entity → repo → usecase → controller`,
  kontrak repo memisahkan `persistent/` dari `webapi/`, tanpa dependensi sirkular. 33 linter aktif.

> **Conflicts with charter (koreksi faktual — RESOLVED):** mandat Fase 1 di charter §4.3 (mewarisi
> temuan eksplorasi Fase 0) menyuruh "putuskan kebijakan cache HTTP (Cache-Control/304)" — seolah
> kebijakan itu belum ada. Audit membuktikan kebijakan itu SUDAH ada dan sehat di
> `middleware/cache.go` (`PublicCache`). Resolusi: inisiatif cache diturunkan dari "membangun"
> menjadi "mendokumentasikan + memperluas cakupan + menyelaraskan dengan edge worker" (bagian dari
> F1-D). Charter §4.3 telah diselaraskan pada review integrasi 2026-07-06; bar charter lain tidak
> berubah.

### 1.2 Fault-line yang nyata (di sinilah fase ini bekerja)

1. **Jalur data-bencana belum bisa dipercaya.** Backup harian sehat (pg_dump → zstd → R2 +
   checksum; timer systemd 04:00 UTC), **tetapi**: kegagalan upload rclone hanya exit non-zero ke
   journald — **tidak ada notifikasi apa pun**; tidak ada WAL-archiving → recovery paling halus =
   24 jam (RPO 24h); snapshot pra-deploy hanya hidup lokal ±20 menit; `surau-pg-restore-check`
   sudah bagus (restore ke container sementara + validasi `quran_ayahs == 6236`) **tapi tidak
   pernah dijadwalkan/di-drill**. Kegagalan migrasi saat deploy → schema DIRTY → boot-loop sampai
   operator manual `migrate force` (tanpa auto-rollback, `docs/deploy-vps.md` §6).
2. **Titik buta observability.** Request-ID dibuat dan dikembalikan di response, **tapi tidak masuk
   baris log** (logger middleware tak memakainya); tidak ada distributed tracing sama sekali; label
   Prometheus hardcoded `"my-service-name"` (`router.go`); tidak ada metrik DB, metrik antrean
   email, atau alerting apa pun di luar deteksi reuse refresh-token.
3. **Background loop rapuh secara senyap.** Empat loop di `internal/app/app.go` (auth-cleanup,
   auth-alert, reminder, email-dispatch): **tanpa panic recovery** (panic di satu loop = seluruh
   proses mati — recovery middleware hanya melindungi lapisan HTTP), tanpa backoff/jitter, tanpa
   drain saat shutdown. Email gagal 5x lalu **diam selamanya di DB** — tidak ada penanda
   dead-letter, tidak ada alert, tidak ada jalur kirim-ulang.
4. **Kode error digantungkan pada teks pesan.** `apierror.Code()` hanya memetakan 7 kode eksplisit;
   sisanya **diturunkan dari kalimat pesan** (snake_case otomatis). Mengedit ejaan pesan =
   mengubah kode mesin yang dikonsumsi FE/mobile — kontrak yang bisa pecah tanpa ada yang sadar.
   Envelope error kaya (`ProductionPublishBlocked`, `ProductionProjectConflict` di
   `v1/response/error.go`) juga tidak membawa `code`/`request_id`.
5. **Kepercayaan test tidak merata.** Coverage total ±51%; **0% di `internal/app`** (bootstrap +
   keempat loop), 0 test di `usecase/task`, `usecase/notification`, `usecase/authmeta`; integration
   suite lulus lewat **kruk retry 3x** (sleep 15/30 detik) tanpa akar-masalah; tidak ada uji
   round-trip migrasi (up→down→up) di CI; `rag-eval` tidak pernah berjalan di CI; satu live test
   di-skip karena bergantung korpus (`TestLiveAyahEditorialReadPath`).
6. **Higiene identitas & sisa template.** Module path masih `github.com/evrone/go-clean-template`
   (terlihat di semua import); kode mati `amqp_rpc`/`nats_rpc` masih ikut coverage; `app.go` 606
   baris wiring 8 domain — belum krisis, tapi tanpa konvensi akan membengkak saat hadith/wiki/1B
   masuk.
7. **Postgres berjalan dengan default polos** (`postgres:18.4-alpine`, tanpa tuning
   shared_buffers/work_mem, tanpa slow-query logging, volume lokal) — cukup hari ini, buta besok.

### 1.3 Penilaian bentuk layering (mandat "shape of the layering itself")

Tidak ada re-arsitektur besar yang dibenarkan bukti. Yang saya putuskan: (a) monolith + clean-arch
dipertahankan; (b) **konvensi modul-domain** perlu diformalkan sebelum fase greenfield — pola wiring
per-domain yang seragam agar `app.go` tidak menjadi file dewa dan agar Fase 5/6 men-scaffold dengan
bentuk yang sama; (c) paket utilitas di luar layer (`internal/importer`, `quranutil`, `readerutil`)
sah sebagai kelas "tooling", tidak perlu dipaksa masuk layer.

---

## 2. Vision — "rock-solid" untuk fondasi ini

Fondasi disebut solid ketika **empat jaminan** berdiri, masing-masing dengan angka (melengkapi bar
charter §2.3):

1. **Data tidak bisa hilang diam-diam.** RPO ≤1 jam (WAL-archiving kontinu ke R2; hari ini 24 jam),
   RTO ≤4 jam dibuktikan drill (bukan estimasi), drill restore kuartalan terjadwal, dan **kegagalan
   backup mustahil senyap** (dead-man switch: tidak ada backup sukses 26 jam → alert).
2. **Insiden terlihat sebelum pengguna mengeluh.** 100% baris log request-scoped membawa
   request-ID; 100% handler HTTP + semua panggilan eksternal (DB, LLM, email, R2) ber-span trace;
   alert menyala ≤5 menit sejak kondisi (5xx-surge, p95 breach, email stuck, backup heartbeat,
   disk, schema DIRTY).
3. **Proses tidak mati atau macet diam-diam.** Panic di loop background tidak menjatuhkan proses;
   loop yang gagal beruntun ter-backoff dan ter-alert; email yang gagal final menjadi dead-letter
   yang terlihat dan bisa dikirim ulang; shutdown men-drain kerja yang sedang berjalan.
4. **Kontrak dan pipeline bisa dipercaya.** Kode error dipilih eksplisit di titik emit (tak lagi
   diturunkan dari kalimat); integration suite hijau 10 run berturut tanpa retry; CI menguji
   round-trip migrasi; coverage kode baru ≥70% (charter) dan total naik dari 51% ke **≥60%** di
   akhir fase (target realistis: sebagian besar dari menguji bootstrap & loop yang kini 0%).

Yang secara sadar TIDAK dikejar fase ini: HA/replika Postgres (Fase 8 + keputusan biaya O4),
abstraksi penuh lapisan LLM (Fase 7), kontrak konten backbone (Fase 1B). Fondasi menyiapkan
**mekanismenya** — bukan kontennya (lihat §5 D1-6).

---

## 3. Gap & opportunity analysis (terurut leverage)

| # | Celah | Prioritas | Effort | Kenapa penting (bahasa produk) |
|---|---|---|---|---|
| P0-1 | Restore tak pernah di-drill; tanpa WAL; backup bisa gagal senyap; snapshot pra-deploy tak durable | **P0** | Kecil–sedang | Satu-satunya kegagalan yang bisa mengakhiri produk dalam sehari; perbaikannya murah dan cepat |
| P0-2 | Tanpa request-ID-di-log / tracing / alerting; label metrik salah | **P0** | Sedang | Tanpa ini, semua fase berikutnya men-debug dengan mata tertutup; wajib ada SEBELUM lonjakan kompleksitas 1B–7 |
| P1-1 | Loop background tanpa panic-recovery/backoff; email dead-letter senyap | P1 | Kecil–sedang | Email verifikasi yang hilang = user terkunci diam-diam; panic tunggal = seluruh API mati |
| P1-2 | Kode error diturunkan dari teks pesan; envelope kaya tanpa code/request_id | P1 | Kecil | Kontrak mobile/FE bisa pecah karena perbaikan ejaan — kelas bug yang paling bodoh untuk dialami |
| P1-3 | Kepercayaan CI: retry-3x integration, tanpa uji round-trip migrasi, 0% coverage bootstrap/loop, rag-eval tak pernah jalan | P1 | Sedang | "CI hijau" harus berarti sesuatu; fase-fase berikutnya menumpang pada kepercayaan ini |
| P1-4 | Belum ada playbook expand-contract + pola backfill resumable | P1 | Kecil | Prasyarat langsung backfill besar 1B/F4 (Citable Unit) tanpa downtime |
| P2-1 | Module path template, kode mati amqp/nats, konvensi modul-domain belum ditulis | P2 | Kecil | Higiene identitas; mencegah `app.go` membengkak saat 3 domain baru masuk |
| P2-2 | Postgres default tanpa tuning/slow-query-log/metrik | P2 | Kecil | Kebutaan kapasitas; murah dicicil sekarang |
| P2-3 | Tanpa idempotency-key untuk POST pembuat-resource; rate-limit origin utk endpoint mahal (search) belum ada | P2 | Sedang | Bisa menunggu; unique-constraint + edge quota sudah menahan sebagian besar risiko |

---

## 4. Roadmap — inisiatif fase 1

Urutan eksekusi yang direkomendasikan: **F1-A dulu (hitungan hari), F1-B menyusul paralel; F1-C/D/H
kecil dan bisa diselipkan; F1-E berjalan kontinu; F1-F/G kapan saja di sela**. Semua internal —
tidak ada perubahan kontrak publik kecuali penambahan aditif (dicatat per inisiatif).

### F1-A — Jalur hidup data: PITR + drill restore + backup yang tak bisa gagal senyap  *(P0, effort kecil–sedang)*

**Rationale:** P0-1; charter menandainya eksistensial. Semua bahan sudah ada (skrip backup,
restore-check dengan validasi invariant) — yang belum ada: kontinuitas (WAL), pembuktian (drill),
dan kejujuran (alert).
**Outcome:** kehilangan data maksimal 1 jam; pemulihan terlatih ≤4 jam; kegagalan backup selalu
berbunyi.
**Isi:** WAL-archiving kontinu ke R2 + base-backup berkala (bentuk migrasi: aditif di sisi ops,
tanpa menyentuh aplikasi; tooling final dipilih saat implementasi — arah saya: pgBackRest, runner-up
wal-g, karena verifikasi & retensi bawaannya lebih lengkap); drill restore kuartalan memakai
`surau-pg-restore-check` (drill pertama = bagian fase ini, hasil + durasi didokumentasikan);
dead-man switch backup (alert bila >26 jam tanpa backup sukses) + `OnFailure` systemd → kanal
notifikasi; snapshot pra-deploy ikut diunggah ke R2 (retensi 7 hari); runbook pemulihan schema
DIRTY dilengkapi (langkah `migrate force` terdokumentasi per skenario).
**AC:** restore penuh dari R2 ke instance kosong lulus validasi invariant (books ≥1, pages ≥1,
`quran_ayahs = 6236`) dalam ≤4 jam terdokumentasi; pemulihan point-in-time ke ≤1 jam sebelum titik
"insiden" berhasil didemonstrasikan; backup yang sengaja digagalkan menghasilkan notifikasi ≤26 jam.
**DS:** Salman menerima laporan drill pertama (berapa lama, apa hasilnya) dan satu notifikasi uji
"backup gagal" di kanalnya — bukti sistem berteriak saat rusak.

### F1-B — Observability inti: request-ID di log, tracing, metrik, paket alert  *(P0, effort sedang)*

**Rationale:** P0-2; charter D12. **Outcome:** satu insiden bisa ditelusuri ujung-ke-ujung; regresi
terlihat ≤5 menit.
**Isi:** request-ID diinjeksikan ke logger context sehingga 100% baris log request-scoped
membawanya; adopsi OpenTelemetry (span HTTP → pgx → klien webapi: LLM, Cloudflare Email, R2;
ekspor ke backend trace ringan self-host dulu); perbaiki label service Prometheus dari config;
metrik RED per endpoint + metrik antrean email (kedalaman, umur pesan tertua, jumlah stuck) +
metrik per-loop (last-success); paket alert minimal: 5xx-surge, p95 breach, email stuck, backup
heartbeat (dari F1-A), disk, schema DIRTY saat boot.
**AC:** satu request nyata dapat diikuti dari baris log ber-request-ID ke trace berujung query DB
dan panggilan eksternal; kelima alert teruji menyala lewat kondisi yang disimulasikan di dev.
**DS:** satu dashboard kesehatan yang bisa Salman buka, dan notifikasi otomatis datang saat ada
yang rusak — tanpa menunggu keluhan pengguna.

### F1-C — Supervisi background job & pipeline email yang jujur  *(P1, effort kecil–sedang)*

**Rationale:** P1-1. **Outcome:** proses tahan panic; tidak ada pekerjaan yang mati diam-diam.
**Isi:** panic-recovery per loop (pulih + log + lanjut, bukan mati proses); backoff + jitter saat
gagal beruntun; drain timeout saat shutdown; email yang melewati batas percobaan menjadi
**dead-letter yang terlihat** (status final + alert + endpoint admin kirim-ulang — aditif, di bawah
peran admin yang sudah ada); metrik dari F1-B menjadikannya terpantau.
**AC:** panic yang disuntik ke satu loop di lingkungan uji tidak menjatuhkan proses dan loop pulih
otomatis; email yang gagal final tampak di metrik/alert dan berhasil dikirim ulang via jalur admin.
**DS:** email verifikasi/reset yang gagal tidak pernah hilang diam-diam — ada tanda dan ada tombol
kirim ulang.

### F1-D — Kontrak API dikunci sebagai kontrak sungguhan  *(P1, effort kecil)*

**Rationale:** P1-2 + penyelarasan cache (lihat "Conflicts with charter" §1.1). **Outcome:**
kontrak mesin stabil terhadap penyuntingan manusia.
**Isi:** kode error menjadi first-class di titik emit — bukan diturunkan dari kalimat; **seluruh
kode fallback yang sudah dipakai hari ini di-snapshot menjadi tabel eksplisit** (kompatibel — tidak
ada kode yang berubah nilainya) dan dijaga test kontrak; envelope error kaya
(`ProductionPublishBlocked`, `ProductionProjectConflict`) diberi `code` + `request_id` (aditif);
kunci envelope legacy (`users`/`projects`/`feedbacks`) dibekukan-didokumentasikan, endpoint list
BARU wajib literal `items`; kebijakan cache `PublicCache` didokumentasikan sebagai kontrak,
diperluas ke endpoint baca publik yang belum tercakup (verifikasi cakupan rute Quran), dan
diselaraskan dengan invalidasi edge worker (satu sumber kebenaran versi cache).
**Blast radius:** nol perubahan breaking — semua aditif/pembekuan; FE/mobile tak perlu berubah.
**AC:** mengubah teks pesan error mana pun tidak dapat mengubah kode mesinnya (dijaga test kontrak
yang membekukan daftar kode); semua bentuk error termasuk varian kaya membawa `code` + `request_id`.
**DS:** kalimat error bisa diperbaiki bahasanya kapan saja tanpa merusak aplikasi mobile.

### F1-E — Kepercayaan test & CI  *(P1, effort sedang)*

**Rationale:** P1-3; pelajaran audit Quran di charter ("test yang tak dijalankan = liability").
**Outcome:** CI hijau = boleh dipercaya.
**Isi:** akar-masalahi flakiness integration (readiness-wait eksplisit, bukan sleep) lalu turunkan
retry dari 3 → 1 dengan **alert bila attempt >1** (retry menjadi alarm, bukan kruk); job CI baru:
round-trip migrasi (DB kosong → up semua → down semua → up semua); smoke-test bootstrap `internal/app`
(start app dengan config uji, cek /healthz + keempat loop hidup) untuk membunuh 0% coverage di titik
paling kritis; unskip `TestLiveAyahEditorialReadPath` dengan fixture mandiri; ratchet coverage
(kode baru ≥70% gagal-kan PR; total dilaporkan per-PR, target akhir fase ≥60%); `rag-eval` smoke
dijadwalkan (non-gating — gate penuh milik Fase 7 per charter) supaya regresi RAG terlihat lebih awal.
**AC:** integration suite lulus 10 run berturut tanpa retry; CI memiliki job round-trip migrasi dan
smoke bootstrap; PR dengan kode baru di bawah 70% coverage gagal otomatis.
**DS:** badge hijau di GitHub benar-benar berarti "aman" — dan Salman diberi tahu bila suite mulai
tidak sehat, bukan disembunyikan retry.

### F1-F — Higiene struktural & identitas modul  *(P2, effort kecil)*

**Rationale:** P2-1. **Outcome:** repo beridentitas benar dan siap menerima 3 domain baru dengan
bentuk seragam.
**Isi:** rename module path dari `github.com/evrone/go-clean-template` ke identitas Surau
(mekanis, diff luas, risiko runtime nol — kerjakan SEKARANG selagi domain baru belum menambah
import); hapus `amqp_rpc`/`nats_rpc` + bersihkan noise coverage; tulis **konvensi modul-domain**
(pola wiring init per-domain yang dipakai `app.go`, template struktur usecase/repo/controller/test
untuk scaffold Fase 1B/5/6); tambah pemindaian secret (mis. gitleaks) ke CI + runbook rotasi rahasia
(pelengkap `.env*` yang sudah di-gitignore; rotasi JWT dual-key tetap milik Fase 2).
**AC:** tidak ada lagi import path template di repo; CI menolak commit berisi secret (diuji dengan
dummy); dokumen konvensi modul-domain ada dan `app.go` mengikuti polanya.
**DS:** internal — laporan singkat bahwa nama modul benar, kode mati hilang, dan ada "cetakan" baku
untuk domain baru.

### F1-G — Baseline Postgres & visibilitas kapasitas  *(P2, effort kecil)*

**Rationale:** P2-2. **Outcome:** DB tidak lagi kotak hitam; kapasitas terlihat sebelum menggigit.
**Isi:** tuning ringan terdokumentasi sesuai RAM VPS (shared_buffers, effective_cache_size,
work_mem); slow-query logging + exporter metrik Postgres (bergabung ke dashboard F1-B); review
ukuran pool aplikasi vs max_connections; sanity autovacuum. HA/replika tetap Fase 8 (O4).
**AC:** metrik DB (koneksi, slow query, ukuran tabel/index, umur autovacuum) tampil di dashboard;
konfigurasi tuning tercatat di compose/env produksi.
**DS:** grafik kesehatan database ada di dashboard yang sama.

### F1-H — Playbook perubahan data besar (expand-contract + backfill resumable)  *(P1, effort kecil)*

**Rationale:** P1-4; ini mekanisme yang fase 1B/4 butuhkan untuk backfill Citable Unit tanpa
downtime — fondasi menyiapkan mekanisme, 1B menyiapkan kontennya (charter D2/D3).
**Isi:** formalkan pola yang sudah dipraktikkan diam-diam (NOT VALID → preflight → VALIDATE)
menjadi playbook tertulis; standar bentuk migrasi expand-contract (tambah-jalur-baru → backfill →
alihkan-pembaca → kontrak); pola job backfill ter-chunk yang **bisa di-pause/resume** dengan metrik
progres (menumpang supervisi F1-C dan metrik F1-B).
**AC:** playbook ada dan dipakai oleh minimal satu backfill nyata yang bisa dihentikan lalu
dilanjutkan tanpa kehilangan progres dan tanpa downtime endpoint publik.
**DS:** perubahan data besar (mis. pemecahan paragraf kitab nanti) berlangsung tanpa situs mati.

**Dependensi antar-inisiatif:** F1-C dan F1-G menumpang metrik F1-B; F1-H menumpang F1-B/C;
F1-A independen dan paling dulu. **Untuk Fase 1B:** desain/penetapan kontraknya boleh berjalan
kapan saja (tidak menunggu apa pun di fase ini), tetapi IMPLEMENTASI-nya (backfill pilot, job
audit) menunggu F1-H (playbook backfill) dan F1-B/C (metrik + supervisi job). Fase 2–4 bisa
berjalan tanpa menunggu F1-E/F/G rampung.

---

## 5. Decisions & assumptions register

| ID | Keputusan fase ini | Rationale | Runner-up ditolak |
|---|---|---|---|
| F1-D1 | PITR via WAL-archiving kontinu ke R2 + base-backup berkala; arah tooling pgBackRest (runner-up wal-g) — keputusan final di implementasi, bentuknya terkunci | RPO 1 jam tak tercapai dengan dump harian; R2 sudah menjadi tujuan backup | Naik ke managed Postgres sekarang (biaya + migrasi; ditunda ke Fase 8/O4) |
| F1-D2 | Kode error jadi eksplisit di titik emit; SEMUA kode fallback existing dibekukan apa adanya sebagai tabel kompatibilitas | Kontrak tak boleh bergantung pada ejaan kalimat; pembekuan menghindari breaking change | Redesain taksonomi kode dari nol (memecahkan kontrak FE/mobile tanpa perlu) |
| F1-D3 | Kunci envelope legacy (`users`/`projects`/`feedbacks`) dibekukan; endpoint baru wajib `items` literal | Mengubahnya = breaking untuk FE live; keseragaman ditegakkan ke depan saja | Migrasi dual-key sekarang (biaya koordinasi FE > manfaat) |
| F1-D4 | Supervisi loop tetap in-process (recover+backoff+drain), TANPA memperkenalkan job-queue/scheduler eksternal | Skala saat ini tidak membenarkan moving part baru; email_queue di DB sudah pola outbox yang benar | Redis/River/cron eksternal — ditinjau ulang bila volume job membesar |
| F1-D5 | Observability: OpenTelemetry + eksporter self-host ringan dulu; APM komersial ditunda | Charter D12; biaya nol-dulu, kontrak instrumentasi tetap portabel | Langganan APM penuh sejak sekarang |
| F1-D6 | Batas fondasi vs 1B: fondasi memiliki MEKANISME (playbook expand-contract, backfill runner, supervisi, flag), 1B memiliki KONTRAK KONTEN (Anchor/Citable Unit/Cross-Reference/lisensi/normalisasi) | Menjawab mandat "apakah invariant konten ikut fondasi": tidak — tapi mekanismenya iya; mencegah 1B menciptakan plumbing sendiri | Menarik kontrak konten ke Fase 1 (membebani fase; melanggar charter D2) |
| F1-D7 | Rename module path dikerjakan di fase ini | Diff makin mahal setiap domain baru; risiko runtime nol | Membiarkan nama template selamanya |
| F1-D8 | Integration retry diturunkan ke 1 + alert saat terpakai (bukan 0 langsung) | Retry sebagai alarm transisi sampai de-flake terbukti 10-run hijau | Langsung 0 retry (berisiko memblokir merge karena flake infra CI di luar kendali) |
| F1-D9 | `rag-eval` masuk CI sebagai smoke terjadwal non-gating | Deteksi dini regresi tanpa mendahului gate resmi Fase 7 | Menjadikannya gate sekarang (golden set baru 7 kasus — belum layak gate per charter) |

**Asumsi:** A-F1-1 — VPS punya ruang disk untuk WAL lokal sementara + R2 menampung arsip (biaya R2
kecil, masuk O4 charter); A-F1-2 — kanal notifikasi alert tersedia (lihat Open decision di §7);
A-F1-3 — frontend tidak bergantung pada nilai kode error selain yang terdokumentasi (pembekuan
F1-D menjaga ini benar).

---

## 6. Interfaces (seams)

**Fase ini MENGEKSPOS ke fase lain:**
- **Jaminan DR**: RPO ≤1 jam, RTO ≤4 jam ter-drill — dasar SLO Fase 8.
- **Kontrak observability**: request-ID di log + trace ujung-ke-ujung + konvensi metrik/alert —
  dipakai semua fase; Fase 7 menambahkan metrik LLM di atasnya.
- **Kontrak error/envelope terkunci** (kode eksplisit + tabel beku + cache policy terdokumentasi) —
  dikonsumsi FE/mobile/edge worker dan semua controller baru.
- **Supervisi job + pola outbox email + dead-letter admin** — dipakai pipeline enrichment/backfill
  fase konten.
- **Playbook expand-contract + backfill resumable** — prasyarat langsung backfill 1B (Citable Unit)
  dan F4.
- **Konvensi modul-domain + template scaffold** — bentuk baku untuk Fase 1B/5/6.
- **CI yang bisa dipercaya** (round-trip migrasi, ratchet coverage, smoke bootstrap, slot eval
  terjadwal) — tempat Fase 7/8 menggantungkan gate.

**Fase ini MENGONSUMSI:** bar & glosarium charter (§2.3/2.5); kanal notifikasi pilihan operator
(§7); tidak ada dependensi ke fase konten.

---

## 7. Open decisions (operator-owned)

Hanya satu — sisanya sudah tercakup keputusan charter O4 (selera biaya).

**O-F1-1 — Kanal notifikasi alert & laporan drill.**
*Kenapa penting:* semua alarm F1-A/B/C bermuara ke kanal ini; kalau kanalnya tidak pernah dibaca,
seluruh sistem peringatan sia-sia.
*Opsi:* (a) **Email ke alfarieshsalman@gmail.com** — nol setup, risiko tenggelam di inbox;
(b) **Telegram/WhatsApp bot** — paling mungkin terbaca cepat, setup kecil sekali-jalan;
(c) **Keduanya** — email untuk laporan (drill, ringkasan), chat untuk alarm mendesak.
*Rekomendasi:* (c). *Default aman jika diam:* (a) — email, karena alamatnya sudah pasti ada.

---

## 8. Conformance

Fase ini tidak menyentuh ayat, makna, atau materi kontensius. Kontribusinya pada RAG Safety &
Domain Integrity bersifat mekanis: supervisi/observability memastikan pipeline provenance tidak
gagal diam-diam, playbook backfill (F1-H) adalah jalan aman bagi 1B menegakkan Citable Unit +
provenance tanpa merusak data, dan CI yang tepercaya adalah tempat gate eval anti-penafsiran-Quran
(Fase 7) akan berdiri.

## 9. North-star fit

Wiki yang mengklaim "setiap ilmu bisa dipercaya sampai ke paragraf sumbernya" harus lebih dulu bisa
menjamin hal yang lebih sederhana: datanya tidak hilang, kesalahannya terlihat, emailnya sampai,
dan kontraknya tidak pecah karena perbaikan ejaan. Fase ini membeli kepercayaan itu dengan biaya
terkecil di seluruh roadmap — dan setiap fase sesudahnya menumpang di atasnya.
