# Surau Backend — Master Roadmap & Engineering Charter

> **Status:** Charter Fase 0 — menggantikan placeholder. Ditulis oleh sesi arsitek (Claude Fable 5)
> pada 2026-07-06 setelah eksplorasi menyeluruh atas codebase ini.
> **Fungsi dokumen:** baseline yang WAJIB dibaca setiap fase berikutnya (1–9). Bar kualitas, prinsip
> RAG-readiness, dan glosarium di sini adalah acuan bersama; fase boleh menaikkan bar-nya, tetapi
> penyimpangan harus ditandai eksplisit dengan catatan "Conflicts with charter".
> **Cara membaca (untuk Salman):** bagian 1 = potret jujur kondisi sekarang; bagian 2 = tujuan dan
> standar; bagian 3 = daftar celah terurut; bagian 4 = urutan kerja yang direkomendasikan; bagian 7 =
> keputusan yang hanya bisa kamu ambil. Sisanya adalah kontrak teknis antar-fase.

---

## 1. Understanding — kondisi backend hari ini

### 1.1 Ringkasan arsitektur

Backend adalah **monolith Go tunggal** (Fiber + PostgreSQL/pgx + golang-migrate) dengan layering
bersih `entity → repo → usecase → controller` (`internal/`), satu sidecar Node untuk kolaborasi
editorial realtime (`collab-server/`, Yjs/Hocuspocus — Go tetap satu-satunya jalur tulis draft), satu
Cloudflare Worker untuk cache & rate-limit edge (`workers/api-cache/`), dan pipeline Python
eksperimental untuk ekstraksi pengetahuan (`scripts/langextract_kg/`). Deploy ke dua VPS:
`dev-api.surau.org` (push ke main) dan `api.surau.org` (tag `api-v*`), dengan snapshot DB pra-deploy
dan verifikasi `/version` pasca-deploy.

**Penilaian saya: fondasi rekayasa ini layak dipertahankan dan dibangun di atasnya, bukan diganti.**
Tidak ada alasan pindah bahasa, framework, atau memecah jadi microservices pada skala ini — biaya
migrasi besar, keuntungan nol. Yang perlu diganti/ditambah bukan fondasinya, melainkan **lapisan
konten** di atasnya (lihat 1.3 dan bagian 3).

### 1.2 Peta kematangan domain (berbasis bukti)

| Domain | Kematangan | Bukti kunci | Masalah terbesar |
|---|---|---|---|
| **Fondasi rekayasa** (layering, lint, CI, migrasi) | **Kuat** | 33 linter aktif (`.golangci.yml`), CI 10 job (lint, vuln-scan, unit, live, integration, collab), 57 pasang migrasi disiplin, tidak ada dependensi sirkular | Request-ID tidak masuk baris log; tanpa distributed tracing; kode mati `amqp_rpc`/`nats_rpc`; label Prometheus hardcoded `"my-service-name"` (`router.go`) |
| **Auth & identity** | **Kuat** (pasca-hardening Jun 2026) | Rotasi sesi refresh + deteksi reuse + alert admin, lockout progresif, rate-limit berbasis DB (lintas instance), `token_version` untuk revokasi instan, bcrypt cost 12, audit log; dokumentasi FE 1.200+ baris (`docs/auth-frontend.md`) | Tanpa MFA (terutama admin); `JWT_SECRET` tak bisa dirotasi tanpa restart; refresh token hidup 720 jam tanpa device binding |
| **Quran (teks primer)** | **Kuat di data, sedang di editorial** | Kunci kanonik `(surah_id, ayah_number)` + `ayah_key "s:a"` dengan CHECK constraints; importer QUL/Kemenag ber-checksum & idempoten; `license_status` menjadi gerbang API (hanya `permitted` tampil); trigger coverage; backlog correctness F01–F24/G1–G13 sudah dibereskan | Editorial surah/ayah **single-state, last-write-wins**: tanpa draft/publish, tanpa riwayat revisi, tanpa ETag (kontras dengan kitab); qira'at/riwayat teks tidak dimodelkan (hanya Hafs); `tafsir_range` baru pointer tanpa isi |
| **Kitab reader + editorial** | **Kuat di alur, lemah di kesiapan-RAG** | Workflow produksi `candidate→drafting→in_review→ready→published`, ETag If-Match (412/428/`*`), snapshot revisi (50 terakhir), collab sidecar sehat, kontrak multilingual berprinsip (exact-language + metadata availability, tanpa fallback diam-diam) | **Tidak ada ID paragraf yang bisa disitasi** (blok diparse in-memory oleh `readerutil.NormalizeContent`, tidak disimpan); **provenance tidak terpisahkan** (teks asli vs suntingan vs mesin); edisi/tahqiq tidak dimodelkan (kolom `type` integer tak terdekode); importer Shamela menimpa konten saat re-import tanpa rollback |
| **Book-RAG (satu buku)** | **Matang dalam scope-nya** | Retrieval "vectorless" ala PageIndex (tree TOC + beam search), validasi sitasi kutipan-eksak dengan retry perbaikan, prompt guardrail "Use only the SOURCE BLOCKS", kuota harian & rate-limit di edge worker | Hanya satu buku per pertanyaan; **golden eval cuma 7 kasus dan tidak dijadikan gate CI**; satu provider LLM tanpa abstraksi; tanpa cache hasil LLM |
| **Search (browse)** | **Dasar** | Quran: trigram + `similarity()` threshold 0.18, sadar-diakritik (`quranutil.NormalizeKey`); indeks trigram kitab sudah ada di migrasi | Tidak ada endpoint search kitab; tidak ada search lintas-korpus; ranking sederhana |
| **Ekstraksi pengetahuan** | **Eksperimental** | Pipeline `scripts/langextract_kg/` (7.700+ baris Python, prompt ber-versi, 4 task: mentions/terms/citations/relations); **skema DB `knowledge_*` sudah ada** (runs, mentions, entities, aliases, candidates, relations, claims — migrasi `20260525000003`) | Belum terintegrasi ke backend; dijalankan manual; belum ada kurasi/disambiguasi produksi |
| **Hadith** | **Kosong (greenfield)** | Tidak ada entity/route/migrasi hadith | — |
| **Wiki / entitas** | **Kosong, tapi embrio ada** | Skema `knowledge_*` di atas adalah titik mulai nyata | Belum ada API, kurasi, atau halaman entitas |
| **Ops produksi** | **Sedang** | Deploy dua-VPS dengan snapshot pra-deploy + verifikasi versi; backup harian `pg_dump` → zstd → R2 + checksum (`ops/backup/`); baseline security scan bersih | **Postgres satu instance tanpa replikasi/WAL-archiving** (RPO efektif 24 jam); **restore belum pernah di-drill**; observability tipis (tanpa APM/alerting di luar token-reuse; metrik DB tak ada) |

### 1.3 Temuan lintas-domain yang paling penting

Ini penemuan yang membentuk seluruh strategi di bawah — **jaringan penghubung antar-korpus sudah
tumbuh secara tidak sengaja, tersebar, dan saling tidak konsisten**:

1. **Embrio "sitasi silang" sudah ada dan bagus**: `BookQuranReference`
   (kitab→ayat, dengan `reference_kind`, `match_strategy`, `confidence`, `review_status` — hanya
   `approved` yang tampil di API publik, endpoint `GET /v1/quran/books/{book_id}/references`). Ini
   persis bentuk yang benar untuk SEMUA rujukan silang: tautan sebagai *klaim ter-atribusi yang
   direview*, bukan sekadar foreign key. Tapi hari ini ia satu-arah, satu-pasangan-korpus, dan hidup
   di dalam domain Quran.
2. **Embrio "graph pengetahuan" sudah ada**: tabel `knowledge_entities/mentions/relations/claims`
   plus pipeline ekstraksi ber-prompt-versi. Bahkan `quran_book_references` sudah punya FK ke
   `knowledge_mentions`. Belum ada yang memilikinya secara produk.
3. **Model lisensi terbaik ada di Quran saja**: enum `license_status`
   (unknown/needs_review/permitted/restricted/public_domain) dengan gerbang di query — kitab tidak
   punya padanannya, padahal risiko lisensi terbesar justru di korpus Shamela.
4. **Pola multilingual kitab/Quran sudah berprinsip** (exact-language, metadata availability,
   tanpa fallback diam-diam) — ini harus jadi standar wajib domain baru, bukan ditemukan ulang.
5. **Dua standar editorial yang saling bertentangan**: kitab punya draft/publish + ETag + revisi;
   editorial Quran single-state tanpa proteksi. Salah satunya harus menjadi standar (yang kitab).
6. **Normalisasi Arab ada dua implementasi paralel** (Go `internal/quranutil/normalize.go` dan
   Python `scripts/langextract_kg/arabic_normalize.py`) — belum ada versi kanonik ber-versi; hasil
   search/matching bisa diam-diam berbeda antar jalur.

### 1.4 Inventaris konsumen & permukaan produk yang harus dilayani

- **Frontend web Next.js** (SSR/SEO — konsumen `{items,total}`, metadata editorial SEO, slug).
- **Aplikasi mobile** (kontrak di `docs/mobile-backend-integration-guide.md`; push via OneSignal).
- **Frontend editorial** (workspace produksi + collab websocket + ETag).
- **Otomasi/enrichment** (skrip terjemahan/ringkasan/ekstraksi; service-token; `If-Match: *`).
- **Edge worker Cloudflare** (cache + kuota RAG) — konsumen internal kontrak API.
- **CI/eval tooling** (`cmd/rag-eval` menembak API hitam-kotak).
- **Mesin pencari/SEO** (via frontend, tapi datanya dari backend: editorial surah/ayah, slug).
- Kelas kemampuan yang **belum tercakup fase mana pun**: sitemap/feed data untuk SEO & sindikasi,
  ekspor data/dump publik, webhook/event keluar, analitik admin konten. (Saya tempatkan: sitemap/feed
  → Fase 3/4 sebagai output editorial; ekspor & webhook → backlog Fase 8; analitik admin → backlog.)

---

## 2. Vision

### 2.1 North-star dan dua prinsip yang tidak bisa ditawar

**Surau = wiki pengetahuan Islam paling lengkap** — Quran, Hadith, kitab turats, dan entitas
pengetahuan — yang **di-browse dan dicari** sebagai wiki, dan **ditanyai** lewat satu RAG terpadu.
Yang membedakan wiki dari "empat aplikasi baca berdampingan" adalah **jaringan penghubung**: setiap
potongan konten punya alamat kanonik, bisa disitasi, dan saling merujuk lintas korpus dengan atribusi.

Dua prinsip permanen (mekanisme boleh diperbaiki, prinsipnya tidak):

- **RAG Safety**: penafsiran agama HARUS bersumber dari karya ulama (tafsir/kitab/hadith). Sistem
  tidak boleh menurunkan makna ayat Quran langsung dari teks ayat via LLM. Ayat tampil sebagai teks
  primer yang disitasi dan sebagai jangkar rujukan — makna selalu mengalir dari karya ulama.
  *Penegakan arsitektural (bukan sekadar prompt): korpus Quran tidak pernah menjadi kandidat
  retrieval untuk pertanyaan makna — ia dieksekusi di lapisan data: unit Quran (ayah + terjemahannya)
  dikecualikan secara STATIS dari kelayakan retrieval interpretatif (aturan eligibilitas Fase 1B §2
  C2), sehingga tidak mungkin dilanggar oleh perubahan prompt. (Diselaraskan dengan Fase 1B pada
  review integrasi 2026-07-06 — semula berbunyi "Quran tidak punya Citable Unit interpretatif";
  unit Quran ADA untuk Lookup/Search/penyajian sitasi, yang tidak ada adalah kelayakan interpretatifnya.)*
- **Domain Integrity**: ikhtilaf direpresentasikan plural dan ter-atribusi (bukan diratakan);
  platform melaporkan pendapat ulama, tidak berfatwa dengan suaranya sendiri; grading hadith
  per-otoritas; teks asli / terjemahan / tambahan editorial / keluaran mesin selalu terpisahkan;
  edisi/tahqiq adalah identitas, bukan catatan kaki.

### 2.2 Arsitektur akhir yang dituju (dalam kata-kata)

Satu monolith Go modular di atas PostgreSQL — **ditambah satu lapisan yang hari ini belum ada dan
menjadi pusat seluruh roadmap: Content Backbone**. Backbone adalah kumpulan kontrak + tabel bersama
yang setiap korpus wajib patuhi:

1. **Alamat kanonik (Anchor)** untuk setiap titik/rentang konten di korpus mana pun.
2. **Citable Unit**: potongan konten granular (kira-kira paragraf) dengan ID stabil, kelas
   provenance, atribusi, dan status lisensi — inilah unit yang disitasi pembaca, diindeks search,
   dan di-retrieve RAG. Satu substrat untuk tiga permukaan akses (Lookup, Search, Ask).
3. **Registry rujukan silang (Cross-Reference)**: generalisasi `BookQuranReference` menjadi
   tautan any-corpus→any-corpus sebagai klaim ter-atribusi (metode, confidence, review).
4. **Kerangka provenance & lisensi** platform-wide (adopsi enum `license_status` Quran ke semua
   korpus; kelas provenance source/editorial/machine dengan identitas model+prompt untuk mesin).
5. **Normalisasi Arab kanonik ber-versi** — satu implementasi acuan yang dipakai search, matching
   rujukan, dan ekstraksi.

Hadith dan Wiki dibangun langsung DI ATAS backbone (bukan meniru kitab lalu di-retrofit). Unified
retrieval (Fase 7) tinggal menyusun ulang unit-unit yang sudah seragam — bukan menyatukan empat model
data yang berbeda.

### 2.3 Definition of Solid — bar yang harus dipenuhi setiap domain

Semua angka di bawah adalah **rekomendasi saya** (bukan pertanyaan untuk operator); fase boleh
menaikkannya dengan justifikasi. "Solid" berarti SEMUA baris baseline terpenuhi + tambahan domainnya.

**Baseline (berlaku untuk semua domain):**

| Dimensi | Target | Justifikasi singkat |
|---|---|---|
| Kebenaran fungsional | Setiap endpoint publik punya ≥1 integration test; setiap invariant korpus dijaga live-test yang **benar-benar berjalan di CI** | Pelajaran audit Quran: test yang tak dijalankan = liability |
| Cakupan test | ≥70% statement coverage untuk kode baru di usecase+repo | Budaya lint ketat sudah ada; 70% menjaga disiplin tanpa teater coverage |
| Latensi | p95 <200ms endpoint baca; <400ms search; RAG end-to-end p95 <30s dan ≤8 panggilan LLM per jawaban | Terukur wajar untuk single-VPS + edge cache; budget LLM mengunci biaya |
| Ketersediaan | 99,5%/bulan API publik (≈3,6 jam downtime); 5xx <0,5% request | Realistis untuk single-instance dengan auto-restart; naik setelah HA |
| Data & import | Importer idempoten dan **non-destruktif terhadap hasil editorial**; dry-run wajib; laporan coverage tiap import | Importer Shamela hari ini menimpa — ini bar, bukan status quo |
| Ketahanan bencana | Backup harian terverifikasi checksum (sudah ada); **drill restore kuartalan**; RPO ≤24 jam sekarang → ≤1 jam setelah WAL-archiving; RTO ≤4 jam | Backup tanpa drill restore = harapan, bukan jaminan |
| Keamanan | `govulncheck` bersih; rahasia hanya di env (terjaga — `.env*` di-gitignore) dengan prosedur rotasi terdokumentasi; MFA untuk akun admin | Kompromi admin = kompromi seluruh konten |
| Kontrak API | Perubahan breaking → path versi baru ATAU field paralel + masa deprecation 90 hari; envelope `{items,total}` dipertahankan | FE web + mobile hidup di atasnya; 90 hari cukup untuk siklus rilis mobile |
| Observability | Request-ID di setiap baris log; metrik per-endpoint; alert untuk 5xx-surge, error migrasi, kegagalan backup | Hari ini insiden hanya ketahuan kalau ada yang mengeluh |

**Tambahan per domain:**

- **Quran**: teks ayat immutable — setiap perubahan teks sumber harus lewat import ber-audit, tak
  pernah lewat editorial; invariant 114 surah / 6236 ayat / rentang juz-hizb dicek di CI; konten
  editorial mengikuti standar editorial kitab (draft/publish + ETag + revisi).
- **Kitab**: setiap heading terpecah menjadi Citable Units tersimpan (bukan parse in-memory);
  setiap unit membawa kelas provenance; edisi/tahqiq terdekode sebagai data (Work vs Edition).
- **Hadith** (saat dibangun): grading per-Authority sejak hari pertama; tidak ada kolom "derajat"
  global tunggal.
- **RAG/retrieval terpadu**: golden set ≥50 kasus lintas-korpus sebelum GA (sekarang 7);
  gate rilis pass-rate ≥90%; validitas sitasi 100% (kutipan harus ada di sumber — mekanisme sudah
  ada, dipertahankan); 0 klaim tanpa sitasi; kasus uji khusus anti-penafsiran-Quran-langsung dan
  anti-perataan-ikhtilaf.
- **Freshness**: konten yang dipublish tampil di search ≤5 menit dan tersedia sebagai Citable Unit
  RAG ≤1 jam (pipeline async boleh, tapi ter-SLA).

### 2.4 Prinsip RAG-readiness (syarat sebuah korpus boleh masuk RAG terpadu)

1. **Beralamat**: setiap unit punya Anchor stabil pada granularitas sitasi (± paragraf), yang tidak
   berubah ketika editorial menyunting di sekitarnya.
2. **Provenance terpisah**: kelas source/editorial/machine melekat di unit, dan retrieval bisa
   memfilter berdasarkan kelas (default RAG: hanya source + editorial yang direview).
3. **Ter-atribusi**: unit tahu karya, pengarang, edisi/tahqiq (dan penerjemah/qari bila relevan).
4. **Ter-lisensi**: `license_status` diperiksa saat query — konten non-`permitted` tidak pernah
   bocor ke jawaban.
5. **Rujukan = data**: hubungan antar konten disimpan sebagai Cross-Reference yang direview, dengan
   confidence — bukan disimpulkan ulang oleh LLM saat menjawab.
6. **Quran anchor-only**: ayat boleh disitasi/ditautkan, tidak pernah menjadi sumber interpretasi.
7. **Label kontensius per-otoritas**: grading/penilaian selalu himpunan pernyataan ter-atribusi.
8. **Multilingual eksplisit**: pola exact-language + availability metadata (standar kitab) wajib.
9. **Normalisasi deterministik ber-versi**: satu fungsi normalisasi Arab kanonik; versinya tercatat
   pada indeks/unit sehingga hasil lama bisa diaudit.
10. **Ber-eval**: korpus menyumbang golden cases (termasuk kasus "tidak ditemukan" dan kasus
    ikhtilaf) SEBELUM diaktifkan di retrieval terpadu.

### 2.5 Glosarium bersama (semua fase memakai istilah ini, jangan menciptakan sinonim)

| Istilah | Arti | Catatan implementasi hari ini |
|---|---|---|
| **Anchor** | Alamat kanonik stabil untuk titik/rentang konten di korpus mana pun | Sudah ada per-domain: `ayah_key "67:1"`, `toc-{heading_id}`; belum ada skema lintas-korpus |
| **Citable Unit** | Unit konten granular ber-ID stabil + provenance + atribusi + lisensi; unit yang disitasi, diindeks, dan di-retrieve | Belum ada; kandidat terdekat = `SourceBlock` yang hari ini hanya in-memory |
| **Cross-Reference** | Tautan antar dua Anchor sebagai klaim ter-atribusi: kind, method (mesin/manusia), confidence, review_status | Embrio: `BookQuranReference` (pola yang benar, scope masih sempit) |
| **Provenance Class** | Asal teks: `source` (penulis klasik) / `editorial` (manusia platform) / `machine` (LLM: model + versi prompt tercatat) | Parsial: `translation_status generated/reviewed`, `origin rest/collab/restore` |
| **Authority** | Pemegang pendapat/penilaian (ulama, mazhab, muhaqqiq, lembaga) tempat klaim diatribusikan | Belum ada sebagai entitas |
| **Work / Edition** | Karya abstrak vs wujud edisi/tahqiq konkret (muhaqqiq, penerbit) | Belum dimodelkan; kolom `type`/`major_release` di books tak terdekode |
| **License Status** | `unknown / needs_review / permitted / restricted / public_domain`; hanya `permitted` tampil publik | Sudah ada di Quran; diangkat jadi standar platform |
| **Knowledge Entity / Mention** | Entitas kanonik wiki / kemunculannya dalam teks; Mention→Entity adalah klaim ter-atribusi ber-confidence | Skema `knowledge_*` sudah ada, belum berproduk |
| **Grading Assertion** | Penilaian keotentikan (hadith dsb.) oleh satu Authority atas satu unit — selalu jamak, tak pernah label global | Belum ada (hadith greenfield) |
| **Retrieval Surfaces** | Tiga jalur akses konten: **Lookup** (alamat langsung), **Search** (browse/pencarian), **Ask** (RAG Q&A) | Lookup kuat; Search parsial; Ask books-only |
| **Ikhtilaf** | Perbedaan pendapat sah yang wajib direpresentasikan plural & ter-atribusi | Prinsip; mekanisme dirancang Fase 5–7 |
| **EvidencePack** | Kontrak tunggal hasil retrieval: paket bukti seragam (unit + teks eksak + provenance + grading/isnad + breadcrumb + confidence + `evidence_origin: curated/retrieved`); composer jawaban hanya mengonsumsi ini | Ditambahkan oleh Fase 7 (tulis-ulang 2026-07-07) |

---

## 3. Gap & opportunity analysis (terurut berdasarkan leverage)

Prioritas: **P0** = menghalangi fase lain / risiko eksistensial; **P1** = leverage tinggi;
**P2** = penting, bisa menyusul. Effort: kecil/sedang/besar (relatif).

| # | Celah / peluang | Prioritas | Effort | Risiko jika dibiarkan | Memblokir |
|---|---|---|---|---|---|
| G1 | **Content Backbone belum ada** (Anchor lintas-korpus, Citable Unit, Cross-Reference registry, provenance & lisensi platform-wide) | **P0** | Besar | Hadith & Wiki lahir dengan asumsi identitas/provenance yang tak kompatibel → rework mahal; RAG terpadu jadi proyek "menyatukan 4 model data" | Fase 3–7 |
| G2 | **Keselamatan data ops**: restore tak pernah di-drill; tanpa WAL-archiving; Postgres single-instance | **P0** | Kecil–sedang | Kehilangan sampai 24 jam data + ketidakpastian restore — satu-satunya risiko yang bisa mengakhiri produk dalam sehari | Tidak ada, tapi eksistensial — **kerjakan sekarang, jangan menunggu Fase 8** |
| G3 | **Citable Unit kitab** (persist blok paragraf + provenance per unit + dekode edisi) | **P0** | Besar | Sitasi RAG rapuh (granularitas heading, bisa patah saat editorial); provenance tercampur = pelanggaran Domain Integrity saat korpus tumbuh | Fase 5 (pola), 6, 7 |
| G4 | **Eval RAG terlalu tipis & tak menjadi gate** (7 kasus, manual) | **P1** | Sedang | Perubahan retrieval/prompt/model tak terdeteksi merusak kualitas; kepercayaan pada RAG terpadu tak bisa dibangun | Fase 7 GA |
| G5 | **Editorial Quran di bawah standar kitab** (single-state, tanpa draft/publish/revisi/ETag) | **P1** | Sedang | Konten SEO yang sedang menopang trafik bisa rusak oleh saling-timpa editor tanpa jejak | Fase 3 |
| G6 | **Lisensi kitab tidak dimodelkan** (Shamela redistribusi?; QUL per-resource) | **P1** | Sedang | Risiko hukum/etika: menerbitkan teks yang tak boleh disebarluaskan | Fase 4, keputusan operator O3 |
| G7 | **Search sebagai produk** belum ada (endpoint kitab tak ada; lintas-korpus tak ada) | **P1** | Sedang–besar | "Wiki" tanpa pencarian yang layak bukan wiki; SEO & UX browse tertahan | Fase 7 (bagian search) |
| G8 | **Observability**: request-ID tak di log, tanpa tracing/alerting/metrik DB | **P1** | Sedang | Insiden produksi lambat terdiagnosis; regresi performa tak terlihat | Fase 8, tapi fondasinya di Fase 1 |
| G9 | **Abstraksi inferensi LLM** (multi-provider, budget, cache, registry prompt-versi) | **P2** | Sedang | Terkunci ke satu provider; biaya tak terkendali saat ekstraksi diskalakan | Fase 6–7 |
| G10 | **Qira'at/riwayat & skema penomoran mushaf alternatif** | **P2** | Besar | Klaim "teks primer impeccable" belum penuh; sitasi lintas-mushaf rapuh | Tidak memblokir; keputusan scope di Fase 3 |
| G11 | **Importer Shamela destruktif saat re-import** | **P2** | Sedang | Update rilis Shamela berisiko menimpa; mitigasi sementara: editorial hidup di tabel edit terpisah | Fase 4 |
| G12 | Kebersihan: kode mati `amqp_rpc`/`nats_rpc`, label Prometheus hardcoded, MFA admin, rotasi JWT dual-key | **P2** | Kecil | Utang kecil menumpuk | Fase 1–2 |

**Risiko terbesar (ringkas untuk operator):** (1) kehilangan data karena restore tak teruji;
(2) membangun Hadith/Wiki sebelum kontrak backbone terkunci — kesalahan arsitektur yang paling mahal
diperbaiki belakangan; (3) sitasi & provenance yang rapuh membuat justru nilai-jual utama (jawaban
ber-dalil yang bisa dipercaya) tidak bisa dijamin; (4) eval yang tipis membuat kualitas RAG tidak
bisa dibuktikan, hanya diyakini.

**Titik mulai paling ber-leverage:** drill restore + WAL-archiving (murah, eksistensial) → kontrak
Content Backbone (menentukan bentuk semua fase berikutnya) → Citable Unit kitab (langsung memberi
nilai: sitasi presisi, dan menjadi pola Hadith).

---

## 4. Roadmap — kritik dekomposisi & urutan fase yang direkomendasikan

### 4.1 Kritik atas dekomposisi awal operator

Proposal awal (1 Foundations · 2 Auth · 3 Quran · 4 Kitab · 5 Hadith · 6 Wiki · 7 RAG · 8 Production
· 9 Konsolidasi) **hampir benar**, dengan tiga koreksi:

1. **Jaringan penghubung yatim-piatu.** Identitas kanonik, sitasi silang, provenance, lisensi, dan
   normalisasi Arab tidak dimiliki fase mana pun — padahal bukti di 1.3 menunjukkan embrionya sudah
   tumbuh liar di tiga tempat berbeda. Jika dibiarkan, Fase 3–6 akan menemukan ulang jawaban yang
   berbeda-beda. **Rekomendasi: jadikan fase sendiri — "Content Backbone" — setelah Fase 1, sebelum
   fase domain.** Ini fase kontrak+fondasi tipis, bukan pembangunan besar: hasilnya adalah keputusan
   dan lapisan bersama minimal yang fase domain implementasikan di wilayahnya masing-masing.
2. **Fase 7 terlalu sempit sebagai "RAG".** Search browse dan RAG Q&A harus memakan substrat yang
   sama (Citable Unit + indeks). Memisahkan keduanya menghasilkan dua pipeline indeks. **Rekomendasi:
   perluas Fase 7 menjadi "Unified Retrieval" — search lintas-korpus + RAG terpadu + lapisan
   inferensi LLM (abstraksi provider, budget, cache, registry prompt).**
3. **Dua item Fase 8 tidak boleh menunggu.** Drill restore + WAL-archiving (G2) adalah pekerjaan
   kecil dengan risiko eksistensial — jalankan sebagai "quick-win ops" segera setelah Fase 1
   memetakannya, di luar antrean fase. Auth (Fase 2) independen dan boleh berjalan paralel kapan pun.

**Penomoran (keputusan operator, rekomendasi saya = Opsi A):**
- **Opsi A (direkomendasikan):** pertahankan nomor fase yang ada; sisipkan backbone sebagai
  **Fase 1B** dengan file `roadmap/phase-1b-content-backbone.md`. Tidak ada renumbering, semua
  referensi di `fable-prompt.md` tetap sah.
- Opsi B: renumber penuh (backbone = Fase 2, dst.) — lebih rapi, tapi menuntut menyunting semua
  prompt fase. Tidak sepadan.

### 4.2 Urutan yang direkomendasikan

```
Fase 0  Charter (dokumen ini)
Fase 1  Foundations ──────────────┐        + quick-win ops (drill restore, WAL) SEGERA
Fase 1B Content Backbone ─────────┤  ← kontrak yang mengikat semua fase konten
Fase 2  Auth (paralel, independen)│
Fase 3  Quran primer  ────────────┤─ konsumen backbone
Fase 4  Kitab + editorial ────────┤─ konsumen backbone; pola untuk hadith
Fase 5  Hadith (greenfield) ──────┤─ butuh 1B + 4
Fase 6  Wiki / entitas ───────────┤─ butuh 1B + 3 + 4 + 5; menyerap langextract
Fase 7  Unified Retrieval ────────┤─ capstone; butuh 4 + 5 + 6
Fase 8  Production hardening ─────┘─ menutup SLO/DR/observability; eval-as-gate
Fase 9  Konsolidasi → PROGRAM.md
```

### 4.3 Mandat per fase (ringkas — tiap fase menulis roadmap detailnya sendiri, terikat charter ini)

Untuk setiap fase: rationale · outcome · **AC** (acceptance criterion — kondisi teknis yang bisa
dicek benar) · **DS** (done-signal — hal yang bisa Salman lihat/lakukan sendiri).

**Fase 1 — Foundations.** Rationale: fondasi kuat tapi punya titik buta observability & utang kecil.
Outcome: platform yang insidennya bisa didiagnosis dan utangnya bersih. Fokus yang saya tetapkan:
request-ID masuk semua baris log; adopsi OpenTelemetry (tracing HTTP→DB→LLM); alerting dasar
(5xx-surge, backup gagal, disk); hapus kode mati amqp/nats; perbaiki label Prometheus; dokumentasikan
& perluas kebijakan cache HTTP yang SUDAH ada (middleware `PublicCache`: Cache-Control/ETag/304 —
koreksi review integrasi 2026-07-06; semula charter mengira kebijakan ini belum ada) dan selaraskan
invalidasinya dengan edge worker. **AC:** satu request
bisa ditelusuri dari log ke trace lintas lapisan; alert menyala saat backup gagal (diuji dengan
simulasi). **DS:** Salman bisa buka satu dashboard dan melihat kesehatan API + diberi tahu otomatis
saat ada yang rusak. *Quick-win ops (dari G2) dieksekusi bersamaan: drill restore pertama +
WAL-archiving → RPO ≤1 jam; **AC:** restore dari backup R2 ke instance kosong berhasil, terdokumentasi,
dijadwalkan kuartalan.*

**Fase 1B — Content Backbone.** Rationale: G1; kontrak sebelum konstruksi. Outcome: keputusan +
lapisan bersama untuk Anchor, Citable Unit, Cross-Reference registry, provenance & lisensi
platform-wide, normalisasi Arab kanonik ber-versi. Fase ini MEMUTUSKAN kontrak dan membangun bagian
bersama minimal; implementasi per-korpus dikerjakan fase domain. **AC:** ada satu skema penamaan
Anchor lintas-korpus yang terdokumentasi; Cross-Reference dapat menautkan dua korpus berbeda dengan
review-status & confidence; enum lisensi berlaku di kitab (bukan hanya Quran). **DS:** Salman bisa
melihat satu tautan "kitab X mengutip ayat Y" dan "hadith Z dikutip kitab X" yang bentuk datanya
sama, masing-masing dengan status review.

**Fase 2 — Auth & identity** (paralel). Rationale: sudah kuat; sisa risiko nyata terukur. Fokus yang
saya tetapkan: MFA admin (TOTP), rotasi JWT dual-key tanpa restart, peninjauan umur refresh-token
(720h → rekomendasi 168h/7 hari + sliding), alert anomali auth dasar. **AC:** rotasi secret tanpa
downtime terverifikasi; login admin butuh faktor kedua. **DS:** login admin Salman meminta kode OTP.

**Fase 3 — Quran primer (tanpa RAG).** Rationale: data kuat; editorial & scope teks perlu naik
kelas. Fokus: samakan standar editorial dengan kitab (draft/publish + ETag + revisi — G5); ekspos
API editorial ayah yang tersisa; sitemap/feed SEO dari data editorial; keputusan scope qira'at
(G10) dieskalasi ke operator dengan rekomendasi; Anchor ayat dideklarasikan sebagai kontrak backbone.
**AC:** menyunting editorial surah/ayah mensyaratkan If-Match dan meninggalkan revisi yang bisa
di-restore. **DS:** dua editor menyunting halaman surah yang sama tidak bisa saling menimpa
diam-diam; ada riwayat perubahan yang bisa dilihat dan dikembalikan.

**Fase 4 — Kitab + editorial.** Rationale: G3, G6, G11 — di sinilah interpretasi hidup, maka
kualitas datanya menentukan RAG. Fokus: Citable Unit tersimpan (materialisasi `SourceBlock` +
migrasi backfill paralel dari parser yang sudah ada); provenance class per unit; model Work/Edition
(dekode `type`, tabel edisi); lisensi kitab (G6); importer non-destruktif (versioned re-import);
audit-trail re-publish *(koreksi review 2026-07-07: publish multi-aset TERNYATA sudah atomik —
mandat awal charter menyuruh membangunnya; sebaliknya temuan Fase 4 menaikkan urgensi importer:
hard-delete + FK cascade menghapus editorial — defect D1 phase-4)*. Blast radius: menambah, bukan mengubah kontrak — endpoint reader lama
tetap; sitasi RAG pindah bertahap ke unit-ID (paralel, bukan big-bang). **AC:** setiap sitasi
book-RAG menunjuk unit-ID stabil yang tetap valid setelah heading disunting; setiap unit punya
provenance class; re-import Shamela tidak mengubah konten published tanpa diff yang direview.
**DS:** jawaban RAG menautkan langsung ke paragraf persisnya, dan tetap benar setelah editor
merapikan bab tersebut.

**Fase 5 — Hadith (greenfield).** Rationale: korpus kedua terpenting; kesempatan membangun langsung
di atas backbone. Fokus: model koleksi/kitab/bab/hadith + matn/isnad; narator sebagai calon
Knowledge Entity; **Grading Assertion per-Authority sejak hari pertama**; penomoran multi-edisi
(mis. penomoran berbeda antar cetakan) sebagai bagian identitas; ingest & kurasi. Wajib memakai
kontrak identity/provenance/sitasi Fase 1B & pola editorial Fase 4. **AC:** satu hadith yang
dinilai berbeda oleh dua otoritas menampilkan kedua penilaian ter-atribusi; setiap hadith punya
Anchor stabil yang bisa dirujuk kitab & wiki. **DS:** halaman hadith menunjukkan "sahih menurut X,
da'if menurut Y" — bukan satu label.

**Fase 6 — Wiki / entitas.** Rationale: perekat lintas korpus; embrio `knowledge_*` + langextract
sudah ada — bangun di atasnya, jangan dari nol (ganti hanya jika fase menemukan alasan konkret).
Fokus: taksonomi entitas (dirancang fase ini); pipeline mention→entity dengan disambiguasi &
kurasi manusia (Mention adalah klaim ber-confidence); halaman entitas dengan backlink ke
ayat/hadith/kitab; governance klaim (siapa boleh menegaskan, bagaimana koreksi). **AC:** satu
entitas (mis. seorang perawi) punya halaman dengan backlink dari ≥2 korpus berbeda; setiap
mention-link punya provenance (mesin vs manusia) dan bisa dibantah/dikoreksi. **DS:** Salman bisa
membuka "Imam Syafi'i" dan melihat semua tempat beliau disebut lintas Quran-tafsir/kitab/hadith.

**Fase 7 — Unified Retrieval (search + RAG + lapisan inferensi).** Rationale: capstone; semua
korpus kini seragam ber-unit. Fokus: search lintas-korpus (produk browse) dan RAG lintas-korpus di
atas substrat yang sama; keputusan teknis yang sudah saya kunci: mulai dari lexical+struktural
(pola PageIndex yang terbukti) lalu tambahkan embeddings sebagai **hybrid** via pgvector di
Postgres yang sama (runner-up yang ditolak: vector-DB eksternal — menambah moving part sebelum
skala menuntutnya); abstraksi provider LLM multi-model + budget + cache (G9); registry prompt
ber-versi (pola langextract diangkat); penegakan RAG Safety di lapisan data; filter
lisensi/otoritas/bahasa saat retrieval; eval ≥50 kasus sebagai gate (G4). **AC:** satu pertanyaan
fiqh mengembalikan jawaban bersitasi dari ≥2 korpus dengan ikhtilaf ter-atribusi bila ada; suite
eval berjalan sebagai gate dan menahan rilis di bawah pass-rate 90%. **DS:** Salman bertanya satu
soal dan melihat jawaban yang mengutip tafsir + hadith sekaligus, dengan tiap kutipan bisa diklik
ke sumbernya.

**Fase 8 — Production hardening.** Rationale: menutup sisa G2/G8 + skala. Fokus: SLO formal
(99,5% → jalur ke 99,9%), HA Postgres (replika atau managed — dieskalasi sebagai keputusan biaya
O4), kapasitas & load-test, keamanan lanjutan, biaya LLM & infra ter-budget, eval-as-CI-gate
permanen. **AC:** kegagalan instance app/DB tunggal tidak menghilangkan data melebihi RPO 1 jam dan
pulih ≤ RTO 4 jam dalam drill. **DS:** Salman tahu persis berapa lama situs mati pada skenario
terburuk, karena sudah pernah dilatih, bukan diperkirakan.

**Fase 9 — Konsolidasi.** Sesuai prompt: satu PROGRAM.md, rekonsiliasi konflik antar fase, antrean
keputusan operator terpadu.

---

## 5. Decisions & assumptions register

Keputusan teknis yang SAYA ambil (fase berikutnya boleh menantang dengan bukti, tapi jangan
menyimpang diam-diam):

| ID | Keputusan | Rationale | Runner-up yang ditolak |
|---|---|---|---|
| D1 | Pertahankan monolith Go+Fiber+Postgres; tidak ada microservices | Skala saat ini + satu tim; fondasi terbukti kuat (1.2) | Memecah per domain — biaya ops & konsistensi lintas-korpus tak sepadan |
| D2 | Content Backbone menjadi fase sendiri (1B) sebelum fase domain | Bukti 1.3: embrio tersebar & tak konsisten; retrofit lebih mahal daripada kontrak di depan | Menitipkan ke Fase 1 (terlalu penuh) atau ke tiap domain (divergen) |
| D3 | Citable Unit granularitas paragraf, dimaterialisasi di DB, backfill dari parser `readerutil` yang sudah ada; migrasi paralel (jalur lama tetap hidup sampai jalur unit terbukti) | Sitasi presisi + provenance per-unit adalah prasyarat Domain Integrity; parser sudah teruji | Chunking on-the-fly saat indexing (tak bisa disitasi stabil); chunking berbasis token (memotong makna) |
| D4 | Generalisasi pola `BookQuranReference` menjadi Cross-Reference registry lintas-korpus | Pola (kind/method/confidence/review) sudah benar & teruji di produksi | Mendesain ulang dari nol; atau tautan sebagai FK polos tanpa atribusi |
| D5 | Enum `license_status` Quran diangkat jadi standar platform | Sudah terbukti sebagai gerbang query; konsistensi | Model lisensi per-domain |
| D6 | Provenance class `source/editorial/machine` + identitas model & versi prompt untuk semua keluaran LLM, berlaku sejak sekarang untuk pekerjaan baru | Murah jika dari awal, mustahil direkonstruksi belakangan | Menunda sampai Fase 7 |
| D7 | Retrieval terpadu: lexical+struktural dulu, embeddings menyusul sebagai hybrid via pgvector — **DIREVISI 2026-07-07 oleh Fase 7 (tulis-ulang)**: hybrid dense menjadi pilar inti KELAS-TERBUKA sejak GA (ber-gerbang mini-eval id↔ar); kelas ber-jangkar tetap traversal registry ter-kurasi (0 LLM); stance pgvector-di-Postgres-yang-sama & tanpa vector-DB eksternal TETAP | Pola vectorless terbukti di book-RAG; pgvector = nol moving part baru | Vector-DB eksternal (Qdrant/pinecone) — prematur |
| D8 | Standar editorial platform = pola kitab (draft/publish + ETag + revisi); editorial Quran dimigrasikan ke sana | Dua standar paralel (1.3 poin 5) adalah bug arsitektur | Membiarkan Quran single-state |
| D9 | Normalisasi Arab: satu implementasi kanonik ber-versi (sumber kebenaran di Go; pipeline Python mengonsumsi hasil/port yang dites kesetaraannya) | Dua implementasi paralel = hasil matching diam-diam beda | Membiarkan duplikat |
| D10 | Envelope `{items,total}`, apierror, dan ETag dipertahankan; perubahan kontrak selalu additive/versioned dengan deprecation 90 hari | FE web + mobile live | Redesign envelope (blast radius besar, keuntungan kecil) |
| D11 | Hadith grading = Grading Assertion per-Authority (tanpa kolom derajat global) | Domain Integrity; grading memang kontensius | Label global dengan catatan kaki |
| D12 | Observability: OpenTelemetry + request-ID di log (Fase 1); alerting minimal sebelum fase konten besar | Titik buta terbesar fondasi saat ini | APM komersial penuh sejak awal (biaya) |
| D13 | Collab sidecar (Yjs/Hocuspocus) dipertahankan; Go tetap satu-satunya jalur tulis | Arsitektur terbukti sehat (laporan kitab) | Menulis ulang collab di Go |

**Asumsi yang saya andalkan** (rekonsiliasi di Fase 9): A1 — frontend Next.js & mobile dapat
mengikuti masa deprecation 90 hari; A2 — sumber data hadith yang layak lisensi tersedia (keputusan
O1); A3 — anggaran LLM cukup untuk ekstraksi entitas bertahap (keputusan O4); A4 — file fase akan
dibaca dari disk oleh sesi berikutnya sesuai `fable-prompt.md`.

---

## 6. Interfaces (seams) — kontrak antar fase

Dinyatakan sebagai kapabilitas (bukan skema). Fase yang MENGEKSPOS wajib menjaga stabilitasnya;
fase yang MENGONSUMSI tidak boleh menemukan padanan sendiri.

| Kontrak | Diekspos oleh | Dikonsumsi oleh |
|---|---|---|
| Skema Anchor lintas-korpus (cara menamai titik/rentang di korpus mana pun) | Fase 1B | Semua fase konten, FE, RAG |
| Citable Unit (unit ber-ID stabil + provenance + lisensi + atribusi) | Fase 1B (kontrak) + Fase 3/4/5/6 (implementasi per korpus) | Fase 7 (indeks search & retrieval), FE (deep-link sitasi) |
| Cross-Reference registry (klaim tautan ter-review antar Anchor) | Fase 1B | Fase 3–7 (tafsir→ayat, hadith→ayat, kitab→hadith, mention→entity) |
| Provenance & lisensi platform-wide | Fase 1B | Semua; gerbang wajib di Fase 7 |
| Normalisasi Arab kanonik ber-versi | Fase 1B | Search, matcher rujukan, ekstraksi, eval |
| Identitas & peran (user/editor/admin, service-token, sesi) | Fase 2 | Editorial semua domain, collab, governance wiki |
| Anchor ayat + teks primer Quran (baca-saja bagi domain lain) | Fase 3 | Fase 4–7 (sitasi & tautan; TIDAK PERNAH sebagai sumber makna) |
| Pola editorial standar (draft/publish + ETag + revisi) | Fase 4 (pemilik pola) | Fase 3 (adopsi), 5, 6 |
| Korpus hadith ber-unit + Grading Assertion per-Authority | Fase 5 | Fase 6 (narator→entity), 7 (filter keotentikan) |
| Graph entitas + mention-link ber-confidence + governance klaim | Fase 6 | Fase 7 (grounding & disambiguasi), FE (halaman wiki + backlink) |
| Lapisan inferensi LLM (provider, budget, cache, registry prompt) + eval harness sebagai gate | Fase 7 | Fase 4/6 (enrichment — CATATAN sequencing: Fase 4/6 berjalan lebih dulu memakai klien LLM existing + kontrak identitas generation-run dari 1B B-6; abstraksi penuh menyusul di Fase 7, BUKAN prasyarat mereka), Fase 8 (gate CI) |
| SLO, DR (RPO/RTO), alerting | Fase 1 (dasar) + Fase 8 (formal) | Semua |

---

## 7. Open decisions — hanya yang dimiliki operator (jawab di Fase 9 bila tidak lebih awal)

**O1 — Scope korpus hadith pertama.**
*Kenapa penting:* menentukan biaya lisensi/ingest, dan otoritas grading mana yang tampil.
*Opsi:* (a) Mulai Bukhari + Muslim dengan 1–2 sumber grading yang diakui — cepat, risiko rendah,
nilai langsung; (b) Kutub as-Sittah sekaligus — lebih lengkap, ingest & QA jauh lebih berat;
(c) menunggu kemitraan resmi data hadith — paling aman legal, paling lambat.
*Rekomendasi:* (a), lalu bertahap. *Default aman jika diam:* (a).

**O2 — Kebijakan penyajian materi kontensius (fiqh, sekte, takfir).**
*Kenapa penting:* liabilitas & kepercayaan; menentukan framing produk.
*Opsi:* (a) Semua tampil dengan atribusi ketat + framing "platform melaporkan, tidak memutus";
(b) materi sensitif tertentu disembunyikan dari RAG (hanya browse); (c) daftar-hitam topik.
*Rekomendasi:* (a) + penandaan kategori sensitif yang membuat RAG menjawab lebih konservatif
(selalu multi-pendapat, tanpa sintesis). *Default aman:* (b) untuk takfir/sekte, (a) untuk fiqh.

**O3 — Postur lisensi korpus Shamela & aset QUL.**
*Kenapa penting:* risiko hukum/etika penerbitan ulang; menyentuh hampir semua konten kitab.
*Opsi:* (a) Audit lisensi per-karya, hanya publish yang jelas boleh (`permitted`) — lambat tapi
bersih; (b) publish semua sambil audit berjalan — cepat, berisiko; (c) batasi karya bermasalah ke
tampilan kutipan-pendek.
*Rekomendasi:* (a) dengan prioritas karya yang paling dibaca; mekanisme `license_status` sudah ada.
*Default aman:* karya berstatus `unknown` tidak dipublish baru; yang telanjur publik ditinjau dulu.

**O4 — Selera biaya infrastruktur & LLM.**
*Kenapa penting:* menentukan kecepatan Fase 6–8 (ekstraksi entitas berbayar per-token; HA Postgres
= biaya bulanan tambahan).
*Opsi:* (a) hemat: single VPS + WAL-archiving, ekstraksi bertahap per-buku prioritas; (b) menengah:
tambah replika/managed Postgres + budget ekstraksi bulanan tetap; (c) agresif: managed DB + ekstraksi
korpus penuh di depan.
*Rekomendasi:* (a) sekarang → (b) saat trafik/tim editorial tumbuh. *Default aman:* (a).

**O5 — Bahasa berikutnya setelah ar/id/en.**
*Kenapa penting:* tiap bahasa = biaya terjemahan+review berkelanjutan.
*Rekomendasi:* tunda; kunci kualitas id dulu (pasar inti), en menyusul. *Default aman:* tidak
menambah bahasa.

---

## 8. Conformance

Charter ini menegakkan RAG Safety secara **arsitektural** (unit Quran dikecualikan statis dari
retrieval interpretatif — bagian 2.1/2.4 prinsip 6, mekanisme di Fase 1B C2) dan Domain Integrity lewat backbone (provenance class, atribusi per-unit,
Grading Assertion per-Authority, Cross-Reference sebagai klaim ter-review, gerbang lisensi saat
query). Setiap fase konten wajib menutup dokumennya dengan pernyataan konformans terhadap kedua
prinsip ini; Fase 9 memeriksa drift antar dokumen.

## 9. North-star fit

Semua jalan di dokumen ini menuju satu hal: **setiap potongan ilmu di Surau punya alamat, asal-usul,
pemilik pendapat, dan lisensi — sehingga ia bisa di-browse sebagai wiki, ditemukan lewat search, dan
dikutip oleh RAG tanpa pernah mengarang.** Fondasi (F1/F1B/F2) membuat platformnya tepercaya; fase
domain (F3–F6) membuat korpusnya seragam dan saling terhubung; F7 menyatukannya menjadi produk; F8
membuatnya tahan produksi. Ketika seorang pengguna bertanya dan menerima jawaban yang mengutip
tafsir, hadith ber-grading ter-atribusi, dan halaman entitas ulamanya — masing-masing bisa diklik
sampai ke paragraf sumber — saat itulah north-star tercapai.

---

## Status fase (living checklist)

- [x] **Fase 0** — Charter ini (`README.md`) *(diselaraskan pada review integrasi 2026-07-06)*
- [x] **Fase 1** — Foundations → `phase-1-foundations.md` *(roadmap ditulis; + quick-win ops: drill restore, WAL)*
- [x] **Fase 1B** — Content Backbone → `phase-1b-content-backbone.md` *(roadmap ditulis; fase baru — lihat 4.1)*
- [x] **Fase 2** — Auth & identity → `phase-2-auth.md` *(roadmap ditulis 2026-07-07; evolusi in-place: RBAC ber-kapabilitas + scholar_reviewer [prasyarat W-0/K-9], MFA+step-up, identitas mesin ber-scope, dual-key JWT)*
- [x] **Fase 3** — Quran primer + reader-product (no RAG) → `phase-3-quran.md` *(roadmap ditulis 2026-07-07; mendeklarasikan seam Reader Experience lintas-korpus — lihat nota konflik di dokumennya)*
- [x] **Fase 4** — Kitab + editorial → `phase-4-kitab-editorial.md` *(roadmap ditulis 2026-07-07; memuat daftar defect terverifikasi — D1 importer = kritis; template untuk Fase 5)*
- [x] **Fase 5** — Hadith → `phase-5-hadith.md` *(roadmap ditulis 2026-07-07; greenfield di atas template Fase 4 dengan defect di-desain-keluar; Fase 5 = penulis pertama ke knowledge_entities — lihat nota penajaman utk Fase 6)*
- [x] **Fase 6** — Wiki / entitas → `phase-6-wiki.md` *(roadmap ditulis 2026-07-07; industrialisasi di atas 16 tabel knowledge_* existing; governance klaim + scholar-reviewer didesain di sini; menerima antrean perawi Fase 5)*
- [x] **Fase 7** — Unified Retrieval + Answer Composition → `phase-7-unified-rag.md` *(DITULIS-ULANG 2026-07-07 atas mandat "recommend, don't inherit": paradigma graph-anchored hybrid + EvidencePack + komposisi/personalisasi jawaban [madhhab/gaya/dalil/hikmah]; tree di-demote→pensiun; D7 direvisi — lihat register)*
- [x] **Fase 8** — Production hardening → `phase-8-production.md` *(roadmap ditulis 2026-07-07; SLO+error-budget, DR berkalender + enkripsi backup [temuan PII], ops LLM/eval-gate/flywheel, HA ber-pemicu O-8-1)*
- [x] **Fase 9** — Konsolidasi → `PROGRAM.md` *(ditulis 2026-07-07 — ROADMAP 0–9 LENGKAP: jalur execute-early "Selamatkan Data", program gelombang W1–W7, 8 konflik RESOLVED, konformans tanpa drift, 33 keputusan → 7 paket + antrean; langkah berikutnya = Salman menjawab §5 PROGRAM.md & mulai sesi implementasi §6)*

> Catatan untuk sesi fase berikutnya: baca charter ini dulu; pakai glosarium bagian 2.5 apa adanya;
> tandai setiap penyimpangan sebagai "Conflicts with charter" dengan usulan resolusi.
