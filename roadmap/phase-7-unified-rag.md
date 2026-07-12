# Fase 7 — Unified Retrieval + Answer Composition (Capstone)

> **Terikat pada charter** (`roadmap/README.md`) dan mengonsumsi VERBATIM kontrak korpus:
> unit & eligibility 1B (C2/C4), sitasi-ber-grading hadith (H-7), eksklusi konten mesin
> unreviewed (K-D4), rujukan silang ter-kurasi approved-only (K-3/H-5), grounding entitas (W-7).
> Nilai non-negotiable: RAG safety (Quran anchor-only), fidelitas sitasi eksak, grading+isnad
> ikut setiap sitasi, ikhtilaf tak pernah diratakan.
> **Dokumen ini MENGGANTIKAN versi 2026-07-07 sebelumnya** atas mandat operator
> ("RECOMMEND, don't inherit"): paradigma retrieval dinalar ulang dari prinsip pertama —
> keputusan lama R-D4/R-D5 (pohon PageIndex dipertahankan permanen untuk mode scoped; vektor
> "menyusul belakangan") DIREVISI di sini. Ditulis ulang 2026-07-07 setelah dua verifikasi
> tambahan (profil biaya panggilan LLM; bentuk preferensi & jawaban) dan satu opini arsitek
> independen yang konvergen.

---

## 1. Understanding — kenyataan hari ini, dibaca ulang tanpa warisan

### 1.1 Book-RAG existing: tepat untuk zamannya, salah untuk zaman berikutnya

Fakta terverifikasi (bukan kesan):

- **LLM dipakai sebagai navigator retrieval.** Mode full-tree (buku ≤450 heading): 1–3 panggilan.
  Mode block-tree (buku besar): beam-search sampai 6 putaran × 6 blok = **worst-case 38 panggilan
  LLM per pertanyaan; tipikal 6–14** (`bookrag.go:19–33`, aritmetika terverifikasi). Inilah sumber
  biaya DAN p95 30 detik — retrieval menunggu antrean LLM berkali-kali secara serial.
- **Jawaban monolitik**: satu `Answer string` + marker `[n]` + array sitasi (`entity/rag.go:84–92`).
  Tidak ada struktur dalil/penjelasan/ikhtilaf; bahasa jawaban dipilih heuristik kasar
  `looksEnglish()` (`bookrag.go:1752` — cek kata "what/why/how").
- **Satu buku per pertanyaan** — by construction.
- **Yang benar-benar berharga dan harus hidup terus**: validasi kutipan-eksak terhadap teks
  sumber + retry perbaikan (mekanisme anti-halusinasi terbaik di repo ini); harness eval
  hitam-kotak (`cmd/rag-eval` — baru 7 kasus); jejak `include_trace`; kuota edge (10/menit,
  50/hari user, 100/hari guest).
- **Preferensi pengguna hari ini** (`entity/user.go:90–106`): bahasa UI/konten, `arabic_level`,
  **`reader_mode`** (arabic_translation/translation_only/arabic_only — langsung reusable untuk
  preferensi tampilan nash), minat, pilihan sumber terjemahan/qari. **Belum ada**: madhhab, gaya
  jawaban, preferensi sumber/ulama.

### 1.2 Aset yang mengubah kalkulus paradigma

Sejak book-RAG dirancang, fase 1B–6 menciptakan aset yang belum ada waktu itu:

1. **Registry Cross-Reference ter-kurasi** (approved-only, ber-confidence, dua-arah): tafsir→ayat,
   kitab→hadith, hadith→ayat, takhrij paralel. Ini **relevansi terverifikasi manusia** — sinyal
   retrieval berpresisi tertinggi yang mungkin ada; tak ada embedding yang bisa mengalahkannya.
2. **Registry Citable Unit** ber-provenance/lisensi/eligibility + normalisasi Arab kanonik C5.
3. **Graph entitas ter-kurasi** (alias multilingual, relasi derived-dari-isnad, disambiguasi
   ber-antrean) — puluhan ribu simpul, traversal dangkal.
4. **Kontrak payload sitasi**: grading+isnad hadith wajib ikut (H-7); edisi/karya kitab (K-2).
5. pgvector tersedia di Postgres yang sama; pg_trgm sudah dipakai.

### 1.3 Distribusi kelas pertanyaan (dasar routing)

Bentuk domain ini membuat mayoritas pertanyaan **ber-jangkar** — menyebut ayat, hadith, ulama,
karya, atau istilah yang bisa diresolusi ke Anchor: "apa makna ayat X", "syarah hadith Arba'in
#1", "pendapat Syafi'i tentang Y", "hadith tentang niat". Sisanya **terbuka** ("kenapa kita
puasa?") dan — kelak — **riset multi-hop** ("bandingkan empat mazhab tentang X berikut dalilnya").
Paradigma yang benar melayani ketiganya dengan mesin berbeda, bukan satu mesin untuk semua.

---

## 2. Vision — rekomendasi arsitektur + model jawaban

### 2.1 GOAL A — Rekomendasi paradigma: **Graph-anchored hybrid** (tiga mesin, satu kontrak, satu router)

**Kandidat yang ditimbang**: (A) pertahankan/kembangkan tree LLM-navigator; (B) dense/vector-first;
(C) graph-first di atas registry ter-kurasi; (D) hybrid dense+lexical untuk semua; (E) agentic
multi-step. **Rekomendasi: C sebagai tulang punggung + D sebagai recall untuk kelas terbuka +
E terkurung untuk riset — dirutekan per kelas pertanyaan.** Opini arsitek independen (diberi
fakta tanpa kesimpulan saya) tiba di arsitektur yang sama.

**Kontrak tunggal — `EvidencePack`** (istilah glosarium baru, ditambahkan ke charter §2.5):
setiap mesin retrieval mengembalikan paket bukti seragam — ID unit + teks kanonik eksak +
provenance/lisensi + payload sitasi korpus (grading+isnad untuk hadith; karya/edisi untuk kitab)
+ breadcrumb TOC + confidence + **`evidence_origin: curated | retrieved`**. Composer HANYA
mengonsumsi EvidencePack; quote-validator existing memvalidasi terhadap pack; unit Quran masuk
pack hanya ber-flag `quote_only` (ditegakkan di kode perakitan pack, bukan prompt). Dengan satu
kontrak, fidelitas sitasi menjadi invarian sistem — bukan harapan per-paradigma.

**Mesin 1 — kelas ber-jangkar (mayoritas): traversal registry, NOL panggilan LLM retrieval.**
Resolver deterministik (parsing referensi ayah/koleksi+nomor; alias entitas via trigram+C5 dengan
ambang confidence) → SQL traversal Cross-Reference approved-only + graph entitas → EvidencePack
terurut confidence. Dense bisa mengutip "kata yang tepat dari paragraf yang salah"; traversal
registry tidak bisa — inilah parit akurasi Surau, dan membuangnya demi embedding berarti membuang
aset paling mahal yang dibangun fase 1B–6.

**Mesin 2 — kelas terbuka: hybrid dense+lexical dengan ekspansi registry.**
Satu panggilan rewrite murah (pertanyaan id/en → istilah Arab + varian terjemahan + kandidat
jangkar — sering MENGUBAH pertanyaan terbuka menjadi ber-jangkar: "puasa" → صيام + Al-Baqarah 183)
→ fusi RRF antara lexical (FTS/trigram di atas teks C5 + terjemahan reviewed) dan **pgvector**
(embedding multilingual ber-versi) → **ekspansi registry** atas kandidat teratas (unit yang
ditemukan menarik tautan ter-kurasinya — struktur terverifikasi memperkaya recall probabilistik)
→ rerank LLM batched opsional di bawah budget.

**Mesin 3 — riset multi-hop (v2): agentic TERKURUNG, asinkron.**
Agen dengan tool whitelist ketat (resolve / traverse / hybrid-search / fetch-unit), step-cap
keras, output terstruktur yang MEWAJIBKAN satu blok sitasi ter-atribusi per posisi (ikhtilaf
dijaga secara struktural) — jalur lambat opt-in, bukan jalur 30 detik.

**Cascade C→D + flywheel kurasi (mekanisme bernama).** Registry miss pada pertanyaan ber-jangkar
→ fallback hybrid ber-scope, jawaban melabeli bukti `retrieved` (bukan `curated`), dan **kueri
yang gagal tercatat sebagai tugas kurasi** yang mengalir ke antrean K-3/H-5 — kegagalan retrieval
hari ini menjadi pertumbuhan registry besok. Untuk tim satu-developer, ini investasi akurasi
jangka panjang terbaik yang tersedia.

**Vonis atas tree/PageIndex: demote, jangan hapus — lalu pensiunkan.** Ia bootstrap yang rasional
(tanpa indeks, satu buku) tetapi gagal secara struktural sebagai inti: O(kedalaman) panggilan LLM
serial per pertanyaan; scope satu-buku; seleksi heading adalah langkah retrieval probabilistik
dengan plafon akurasi rendah (judul bab klasik = sinyal lemah). Perannya sekarang: **mesin
fallback untuk buku yang belum ter-unit/ter-embed** (jalur migrasi inkremental tanpa big-bang
re-index), pensiun per-buku begitu unitnya termaterialisasi dan eval membuktikan jalur baru
menang. Yang dipertahankan permanen: quote-validator+repair (naik jadi komponen bersama), harness
eval (diperluas), breadcrumb TOC (jadi metadata di setiap unit — terbukti membantu jawaban
menempatkan kutipan).

**Runner-up yang ditolak — hybrid dense+lexical sebagai inti untuk SEMUA kelas (D-untuk-semua):**
(a) untuk kelas ber-jangkar ia menggantikan tautan terverifikasi dengan kemiripan probabilistik —
akurasi lebih buruk untuk kelas mayoritas; (b) ia menaruh teknologi belum-terbukti di jalur
kritis: perilaku embedding multilingual pada Arab klasik (fiqh/hadith) yang ditanya dari bahasa
Indonesia belum teruji — dalam desain terpilih, kegagalan embedding hanya menurunkan recall kelas
terbuka, tidak merusak kelas ber-jangkar. (B dense-first tertolak a fortiori; A tree-sebagai-inti
tertolak oleh aritmetika §1.1.)

**Tools spesifik yang ditimbang (dan pemicu peninjauan ulangnya).** Paradigma A–E di atas mencakup
keluarga tools berikut; dinilai terhadap bentuk Surau (graph TER-KURASI + fidelitas sitasi +
1-developer + Go/Postgres), bukan terhadap tren:

| Tool | Apa dia | Kapan dia menang | Kenapa bukan untuk Surau sekarang | Yang diadopsi / pemicu revisit |
|---|---|---|---|---|
| **Microsoft GraphRAG** | LLM meng-ekstrak entitas/relasi otomatis → komunitas (Leiden) → ringkasan-komunitas → local/global search | Korpus TANPA struktur ter-kurasi, butuh jawaban "global" | Nilai intinya = graph OTOMATIS; Surau sudah punya graph ter-review — mengganti klaim ter-kurasi dgn ekstraksi mentah justru melanggar Domain Integrity (tautan = klaim ter-atribusi); indexing LLM atas jutaan unit Arab klasik = biaya besar; ringkasan-komunitas = konten mesin yg per K-D4 tak boleh interpretatif tanpa review | Diadopsi: pembedaan local/global ≈ routing ber-jangkar/terbuka; "global answer" versi halal = ringkasan entitas/karya REVIEWED (W-3) + tier riset U-8. Catatan: pipeline langextract→antrean kurasi KITA adalah "GraphRAG yang benar utk domain agama" — mesin mengusulkan, manusia memutuskan |
| **Neo4j** | Graph-DB penuh (Cypher, algoritma graph) | Traversal variable-length dalam di jutaan+ edge; analitik graph real-time | Kueri kita dangkal (isnad 3–9 hop; guru-murid; backlink = indexed join → CTE milidetik); store kedua = kelas bug sinkronisasi teks↔tautan↔grading + pajak ops 1-dev (U-D5, W-D10) | Revisit: > jutaan edge ATAU kebutuhan analitik graph interaktif — dan itu pun mulai sebagai job analitik offline, bukan hot path |
| **FalkorDB** | Graph-DB ringan sebagai module Redis (OpenCypher, sparse matrices) | Latensi-graph rendah saat CTE mulai kewalahan, dengan ops lebih ringan dari Neo4j | Tetap store kedua + bahasa kueri baru; kami bahkan tidak menjalankan Redis hari ini (F1-D4); masalah yang ia selesaikan (latensi graph pada skala besar) belum kami miliki | Kandidat PERTAMA bila pemicu Neo4j tercapai tapi ingin footprint kecil |
| **Milvus** (+ resep graph_rag-nya) | Vector-DB terdedikasi; resepnya: triplet+entitas sebagai vektor, NER LLM, CoT rerank | Skala vektor >10M / multi-tenant / recall-latency headroom besar | Skala kita squarely pgvector (halfvec ±384 ≈ 1,5–3 GB); deployment cluster = etcd/pulsar/minio; resep graph-rag-nya memakai triplet auto-extracted (dokumennya sendiri: "early stages") — pola multi-way-retrieval+rerank-nya SUDAH ada di desain ini di atas relasi ter-kurasi | Revisit: >10M unit atau HNSW build mengganggu ingest (sudah tercatat) |
| **LangGraph** | Framework orkestrasi agent (Python/JS): state machine, checkpoint, human-in-loop | Agen kompleks multi-langkah, resume lintas-sesi, tim yang hidup di Python | Backend Go — berarti sidecar Python DI JALUR JAWAB (runtime+deploy+failure-class baru); tier agentic kami SENGAJA terkurung (4 tools whitelist + step-cap) = state machine kecil deterministik yang wajar ditulis langsung | Diadopsi: polanya (state eksplisit + checkpoint utk tier riset async). Revisit: bila U-8 tumbuh jadi job riset panjang multi-sesi → worker Python+LangGraph DI LUAR hot path (antrean async) adalah jalur sah |

**Embeddings realistis-biaya**: pgvector `halfvec` dimensi kecil (±384) — jutaan unit ≈ 1,5–3 GB
termasuk indeks HNSW, muat di VPS; embed tafsir & hadith DULU, long-tail kitab lazy; ber-versi
(ganti model = reindex via playbook F1-H). **TANPA vector-DB / graph-DB eksternal** — bukan hanya
karena skala (graph puluhan ribu simpul + traversal dangkal = recursive CTE milidetik), tetapi
karena **fidelitas sitasi menuntut konsistensi transaksional antara teks-tautan-grading**: satu
Postgres memberikannya gratis; penyimpanan kedua memperkenalkan kelas bug sinkronisasi di mana
sitasi menunjuk versi teks basi.

**Aritmetika budget panggilan** (vs 6–38 hari ini): ber-jangkar bersih **1–2** (0 retrieval +
1 jawab + ~0,2 repair); ber-jangkar berfrasa-kabur **2–3** (+1 router/rewrite model kecil);
terbuka **2–4**; riset ter-cap **5–8**. p95 membaik karena retrieval menjadi latensi-SQL.

### 2.2 GOAL B — Komposisi & personalisasi jawaban (model yang saya tetapkan)

**Jawaban = dokumen terstruktur ber-skema-versi, bukan paragraf.** Komponen (skema jawaban
ber-versi di registry prompt; FE merender komponen; endpoint lama mendapat renderer flat agar
kontrak tak pecah):

| Komponen | Isi | Aturan integritas |
|---|---|---|
| **Ringkasan-posisi** | Jawaban inti; PLURAL bila posisi ulama berbeda | Tidak pernah satu verdict pada perkara ikhtilaf |
| **Dalil** | Ayat (nash Arab + terjemah sesuai preferensi tampilan) & hadith (matn + **badge grading per-otoritas** + koleksi/nomor) & nukilan kitab | Semua kutipan tervalidasi-eksak; grading SELALU tampil apa pun gaya |
| **Penjelasan/Syarah** | Prosa komposisi bersitasi dari EvidencePack | Setiap klaim ber-marker sitasi; 0 klaim tanpa sitasi |
| **Hikmah** | Rasional/hikmah di balik hukum | **HANYA bila bersumber unit ulama ter-atribusi — tidak pernah hikmah karangan model** (aturan kunci) |
| **Panel Ikhtilaf** | Posisi per-mazhab/otoritas, ter-atribusi, dengan dalil masing-masing | Selalu hadir bila posisi berbeda — apa pun preferensi |
| **Rujukan** | Daftar sitasi penuh ber-anchor (klik → paragraf sumber) | `evidence_origin` berlabel (curated/retrieved) |
| **Istilah** | Istilah teknis → link entitas glossary (W-3) | Hanya entitas approved |

**Model preferensi** (perluasan ADITIF `user_preferences` — via seam Reader Experience Fase 3;
guest memakai parameter per-request yang mencerminkan field yang sama):

- **`madhhab`** (BARU; default: netral/none) — lensa penyusunan.
- **Preferensi sumber/ulama** (BARU) — pembobotan urutan dalam himpunan eligible (mis. dahulukan
  kutipan dari koleksi/pengarang favorit).
- **Gaya** (BARU): `ringkas` (ringkasan + dalil inti) / `standar` / `syarah` (penjelasan penuh +
  hikmah + istilah); **tahdzib** = register ringkas-ilmiah (varian penyajian ringkas yang menjaga
  presisi istilah). Gaya mengatur inklusi-komponen + budget token.
- **Bahasa jawaban**: deteksi + parameter eksplisit (menggantikan heuristik `looksEnglish` —
  kelemahan terverifikasi §1.1); nash Arab **selalu** tersedia di Dalil.
- **Tampilan nash** = pakai-ulang **`reader_mode`** existing (arab+terjemah / terjemah / arab).
- **Glossing istilah** = pakai-ulang **`arabic_level`** existing (pemula → istilah diberi
  penjelasan; mahir → ringkas).

**Invarian Lensa × Domain Integrity** (ditegakkan struktural di composer + diuji kategori eval):

1. Lensa madhhab/sumber hanya boleh **mengurutkan, membingkai, dan mengatur kedalaman** — TIDAK
   PERNAH menyembunyikan bahwa mazhab lain berbeda: Panel Ikhtilaf selalu hadir bila posisi
   berbeda (minimal ringkasan satu-baris per posisi pada gaya `ringkas`).
2. Grading hadith selalu tampil, termasuk pada gaya `ringkas`.
3. Framing lensa selalu BERLABEL ("berdasarkan preferensi mazhab Anda…") — pembaca tahu sedang
   memakai kacamata apa.
4. Kategori sensitif (kebijakan O2 charter) memaksa template konservatif (multi-posisi tanpa
   sintesis) — mengalahkan preferensi apa pun.
5. Personalisasi TIDAK PERNAH mengubah eligibility retrieval (C4) — hanya komposisi; cache
   jawaban ber-kunci (pertanyaan-normal, filter, **profil-lensa, gaya, bahasa**, versi indeks).

### 2.3 Bar kuantitatif (mengunci angka charter §2.3 + menambah)

Panggilan LLM: median ber-jangkar ≤2, terbuka ≤4, riset ≤8 (cap keras); p95 end-to-end <30 detik
dipertahankan sebagai batas atas (ekspektasi nyata jauh membaik — retrieval SQL p95 <500ms);
Search terpadu p95 <400ms; kesegaran indeks & embedding ≤1 jam sejak publish; validitas sitasi
100% (kutipan tervalidasi-eksak); 0 klaim tanpa sitasi; **100% respons Ask valid terhadap skema
jawaban ber-versi**; eval-gate ≥50 kasus, pass-rate rilis ≥90%, kategori keamanan (anti-tafsir,
injeksi, lensa-tak-meratakan) = blokir mutlak; 0 unit Quran / mesin-unreviewed di himpunan
interpretatif (test konstruksi indeks); flywheel: 100% registry-miss ber-jangkar tercatat ke
antrean kurasi.

---

## 3. Gap & opportunity analysis

| # | Celah → kemampuan | Prioritas | Effort | Risiko utama |
|---|---|---|---|---|
| U-G1 | Infra inferensi ad-hoc → lapisan bersama (provider/budget/cache/prompt-registry) | **P0** | Sedang | — |
| U-G2 | Tanpa indeks lintas-korpus → indeks unit dua-himpunan + pilar embedding | **P0** | Sedang–besar | Kualitas embedding Arab-klasik↔id BELUM TERBUKTI → eval dulu sebelum dipercaya |
| U-G3 | Eval 7 kasus non-gating → ≥50 kasus sebagai GATE (paralel sejak hari 1) | **P0** | Sedang | Tanpa ini migrasi paradigma tak bisa diberkati |
| U-G4 | Tanpa resolver/router → query understanding + traversal registry (mesin 1) | **P0** | Sedang | Salah-resolusi entitas (Ibn Hajar al-Asqalani vs al-Haytami) → ambang + disambiguasi |
| U-G5 | Jawaban monolitik → EvidencePack + composer + skema jawaban terstruktur | **P0–P1** | Besar | Kompleksitas prompt komposisi → skema ber-versi + eval struktur |
| U-G6 | Tanpa personalisasi → model preferensi + lensa ber-invarian | P1 | Sedang | Sensitivitas madhhab (O-7-4) |
| U-G7 | Search browse per-domain → Search terpadu | P1 | Sedang | — |
| U-G8 | Guardrail runtime (fatwa/sensitif/injeksi-lewat-konten) | P1 | Sedang | Teks korpus = input tak tepercaya |
| U-G9 | Tier riset agentik + flywheel kurasi ber-metrik | P2 | Sedang | Runaway → cap keras + async |

---

## 4. Roadmap — inisiatif U-0…U-8

Urutan: **U-0 ∥ U-6(eval) sejak hari pertama → U-1 → U-2 → U-3 → U-4 ∥ U-5 → U-7 → U-8.**
Dependensi korpus: kitab (K-1) dulu; hadith (H-1/H-7) & wiki (W-7) diaktifkan per-flag saat
mendarat — arsitektur tidak berubah saat korpus bertambah.

### U-0 — Lapisan inferensi LLM bersama  *(P0, effort sedang; carry-over sah)*

Registry provider multi-model per-tugas (rewrite/rerank/embed/jawab/judge), metering token+biaya
(cap harian, alert 80%, tolak-anggun), **registry prompt & skema-jawaban ber-versi di DB**
(mengangkat pola `knowledge_prompt_versions`), identitas generation-run B-6 di semua panggilan,
cache (kandidat/embedding agresif; jawaban singkat ber-kunci §2.2-5 dan BERLABEL), failover.
**AC:** dua provider ter-failover teruji; setiap panggilan tercatat (tugas, model, versi prompt,
token, biaya) di trace F1-B; tembus cap = respons jelas.
**DS:** Salman melihat biaya per-hari dan tahu pagarnya bekerja.

### U-1 — Indeks unit dua-himpunan + pilar embedding  *(P0, effort sedang–besar)*

Indeks browse (semua `permitted`, berlabel — Quran ikut) vs indeks interpretatif (eligibility C4:
tanpa Quran, tanpa mesin-unreviewed K-D4 — absen SECARA KONSTRUKSI); teks C5 + label korpus/
bahasa/lisensi + payload sitasi + breadcrumb TOC; embedding `halfvec` ±384-dim ber-versi —
**tafsir & hadith diembed dulu, long-tail lazy**; kesegaran ≤1 jam (job supervisi F1-C, resumable
F1-H). **Gerbang kualitas embedding**: model dipilih lewat mini-eval id↔ar SEBELUM masuk jalur
kueri (U-6 menyediakan kasusnya).
**AC:** test konstruksi membuktikan 0 unit Quran/mesin-unreviewed di indeks interpretatif; SLA
kesegaran terukur; embedding ber-versi per unit; rebuild resumable.
**DS:** konten baru muncul di pencarian dalam hitungan menit — dan mustahil jawaban agama
bersumber dari yang belum pantas.

### U-2 — Query understanding + mesin traversal registry  *(P0, effort sedang)*

Parser referensi (ayah/koleksi+nomor/slug karya — memperluas pola Q-9) + entity-linking kueri via
alias W-7 (ambang confidence; di bawah ambang → perlakukan sebagai terbuka atau minta
disambiguasi) + deteksi bahasa + klasifikasi kelas (ber-jangkar/terbuka/hukum-personal/riset);
traversal SQL Cross-Reference approved-only + graph entitas → EvidencePack `curated`;
**cascade C→D**: miss → hybrid ber-scope berlabel `retrieved` + **tercatat ke antrean kurasi
K-3/H-5** (flywheel, ber-metrik).
**AC:** pertanyaan ber-jangkar bersih terjawab dengan 0 panggilan LLM retrieval; salah-resolusi
di bawah ambang → jalur disambiguasi, bukan jawaban pede-salah; setiap registry-miss tercatat.
**DS:** "apa makna QS 2:183" langsung menarik tafsir ter-kurasi — cepat, murah, dan bisa dilacak
kenapa paragraf itu yang muncul.

### U-3 — EvidencePack + composer + skema jawaban terstruktur  *(P0–P1, effort besar)*

Kontrak EvidencePack (§2.1) sebagai tipe internal bersama; composer satu-panggilan (+repair) yang
mengisi komponen §2.2 HANYA dari pack; quote-validator existing diangkat jadi komponen bersama
lintas korpus; aturan Hikmah-bersumber; panel Ikhtilaf dirakit dengan pengelompokan per-otoritas
SEBELUM prompt (struktural); renderer flat untuk kompatibilitas endpoint lama; not-found & bahasa
jawaban eksplisit (mengganti `looksEnglish`).
**AC:** 100% respons valid skema; kutipan tervalidasi-eksak; hadith di Dalil selalu membawa
grading (uji struktural); pertanyaan ikhtilaf menghasilkan panel multi-posisi ter-atribusi;
endpoint lama tetap berkontrak sama via renderer flat.
**DS:** jawaban tampil sebagai dokumen ilmiah rapi — ringkasan, dalil ber-derajat, penjelasan,
hikmah yang ada sumbernya, dan daftar rujukan yang bisa diklik.

### U-4 — Model preferensi & lensa personalisasi  *(P1, effort sedang)*

Field preferensi baru (madhhab/gaya/preferensi-sumber) aditif via seam Reader Experience + param
per-request untuk guest; lensa di composer sesuai invarian §2.2 (urut/bingkai/kedalaman — tak
pernah menyembunyikan); reuse `reader_mode` & `arabic_level`; cache jawaban ber-kunci profil.
**Blast radius:** aditif murni — klien lama tanpa preferensi mendapat perilaku netral.
**AC:** invarian lensa lolos kategori eval "lens-tak-meratakan" (pertanyaan khilafiyah dengan
preferensi madhhab tetap menampilkan panel ikhtilaf; uji semua kombinasi gaya×madhhab pada kasus
golden); framing berlabel; guest dapat lensa via param.
**DS:** pengguna bermazhab Syafi'i melihat posisi Syafi'i didahulukan DENGAN label — dan tetap
melihat mazhab lain berbeda.

### U-5 — Unified Search API (browse)  *(P1, effort sedang)*

Search lintas-korpus di indeks browse: lompat-referensi deterministik, ekspansi alias entitas,
filter eksplisit (korpus/bahasa/karya/derajat-hadith), highlight, paginasi ter-clamp, hasil
berlabel provenance/lisensi; endpoint lama per-domain tetap.
**AC:** p95 <400ms; kueri referensi = hasil teratas deterministik; fuzz metakarakter aman.
**DS:** satu kotak cari menemukan ayat, hadith ber-derajat, paragraf kitab, dan halaman ulama.

### U-6 — Eval-as-gate diperluas (berjalan paralel sejak hari pertama)  *(P0, effort sedang)*

Leburan benih per-korpus → ≥50 kasus: kitab existing + hadith H-7 (atribusi grading, ikhtilaf,
"da'if-tak-tampil-sahih", takhrij) + wiki W-7 (biografis, "menurut ulama X", **confusion entitas**)
+ keamanan-Quran (anti-tafsir; routing ayat→tafsir) + lintas-korpus + **id↔ar** (gerbang model
embedding) + not-found + **injeksi-lewat-konten** + kategori BARU: **validitas struktur komposisi**
dan **lensa-tak-meratakan-ikhtilaf**; asersi deterministik dulu, LLM-judge ber-rubrik-versi hanya
untuk groundedness/ikhtilaf (audit manusia sampling); **gating**: CI utk PR retrieval + gate rilis
≥90% + kategori keamanan blokir-mutlak; **parity-gate migrasi**: endpoint book-RAG lama di-reroute
per-buku hanya setelah jalur baru ≥ tree pada golden kitab; tree pensiun per-buku setelahnya.
Gerbang konstruksi anti-tafsir Q-2 yang WAJIB dipanggil suite ini adalah
`internal/repo/persistent/quran_citable_unit_live_test.go::TestLiveQuranCitableUnitsNeverInterpretiveEligible`;
U-6 tidak boleh menggantinya dengan asersi prompt atau sampling hasil.
**AC:** PR yang sengaja dirusak tertahan gate; dashboard pass-rate per kategori; tidak ada
reroute tanpa parity; tidak ada pensiun tree tanpa eval menang.
**DS:** "mesin baru lebih baik" adalah angka di dashboard, bukan klaim.

### U-7 — Guardrail runtime  *(P1, effort sedang)*

Pertahanan injeksi (konten sumber = data, hierarki instruksi, jawaban hanya-dari-pack); mode
sensitif O2 (template konservatif); kebijakan hukum-personal O-7-2 (paparan posisi + disclaimer +
arahan konsultasi — platform tidak memutus); penolakan sopan di luar domain.
**AC:** suite injeksi lulus; pertanyaan fatwa personal menghasilkan perilaku kebijakan; kategori
sensitif tak pernah tersintesis tunggal di eval.
**DS:** platform menjawab seperti pustakawan ulama yang jujur — dan tak bisa disetir teks jahil
di dalam korpus.

### U-8 — Tier riset agentik + pematangan flywheel  *(P2, effort sedang)*

Agen terkurung (tool whitelist, step-cap, async, output terstruktur per-posisi) untuk pertanyaan
riset; dashboard flywheel kurasi (registry-miss → tugas → approved → hit-rate `curated` naik);
feedback jawaban → kandidat kasus eval (loop kualitas); analitik (perluasan inventaris Q-8).
**AC:** pertanyaan riset selesai ≤8 panggilan dengan posisi ter-atribusi per mazhab; hit-rate
`curated` untuk kelas ber-jangkar naik antar-kuartal (metrik flywheel); ≥1 kasus eval baru/bulan
lahir dari feedback nyata.
**DS:** pertanyaan perbandingan mazhab yang dulu mustahil kini dijawab rapi — dan sistem makin
pintar justru dari pertanyaan yang dulu gagal.

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| U-D1 | Router per kelas pertanyaan; tiga mesin; SATU kontrak EvidencePack | Kelas mayoritas ber-jangkar dilayani deterministik; fidelitas sitasi jadi invarian kontrak | Satu mesin untuk semua (hybrid-semua / tree-semua) |
| U-D2 | Kelas ber-jangkar = traversal registry approved-only, 0 LLM retrieval | Tautan ter-kurasi = presisi maksimum; LLM tak pernah menurunkan tautan saat menjawab | Dense/rerank utk pertanyaan ber-jangkar |
| U-D3 | Kelas terbuka = rewrite-call → hybrid RRF (lexical C5 ⊕ pgvector) → ekspansi registry | Rewrite sering meng-anchor-kan; hybrid menambah recall id↔ar; ekspansi memperkaya dgn struktur terverifikasi | Lexical-only (recall lintas-bahasa lemah); dense-only (presisi & atribusi lemah) |
| U-D4 | Tree/PageIndex: demote jadi fallback per-buku → pensiun setelah parity+cakupan; quote-validator & harness & breadcrumb dipertahankan | Aritmetika 6–38 panggilan; plafon akurasi seleksi-heading; jalur migrasi inkremental | Pertahankan permanen utk scoped (keputusan lama R-D4 — DIREVISI); hapus langsung (big-bang) |
| U-D5 | pgvector `halfvec` ±384-dim di Postgres yang sama; embed tafsir/hadith dulu, lazy long-tail; TANPA vector/graph-DB eksternal | Konsistensi transaksional teks-tautan-grading; skala muat VPS; ops 1-dev | Vector-DB eksternal; Neo4j (traversal dangkal = CTE milidetik) |
| U-D6 | Keamanan di KONSTRUKSI indeks + flag `quote_only` di perakitan pack | Mustahil > dilarang; dua lapisan independen | Guardrail prompt-only |
| U-D7 | Jawaban = skema komponen ber-versi; endpoint lama dapat renderer flat | Komposisi ilmiah & personalisasi butuh struktur; kompat terjaga | Tetap string monolitik; breaking change format |
| U-D8 | Hikmah hanya-bersumber (unit ulama ter-atribusi); tak pernah karangan model | Garis Domain Integrity paling rawan di komponen paling "lunak" | Hikmah generatif berlabel |
| U-D9 | Lensa personalisasi = urutan/bingkai/kedalaman; lima invarian §2.2; eligibility tak tersentuh | Personalisasi tanpa flattening — syarat mutlak misi | Filter-out posisi lain sesuai mazhab; tanpa personalisasi sama sekali |
| U-D10 | Bahasa jawaban = deteksi + param eksplisit; `looksEnglish` diganti | Heuristik kata-tanya Inggris rapuh (terverifikasi) | Mempertahankan heuristik |
| U-D11 | Cascade C→D dengan label `evidence_origin` + pencatatan miss ke antrean kurasi (flywheel) | Gagal-diam dilarang; kegagalan = bahan pertumbuhan registry | Diam-diam fallback tanpa label/pencatatan |
| U-D12 | Agentic hanya sebagai tier riset terkurung async (v2) | Nilai nyata utk multi-hop; risiko runaway dikurung cap+whitelist | Agentic sebagai jalur default |
| U-D13 | Cache jawaban ber-kunci (pertanyaan-normal, filter, profil-lensa, gaya, bahasa, versi indeks), TTL pendek, berlabel | Personalisasi tanpa kebocoran cache antar-profil | Cache global tanpa profil (jawaban salah-lensa) |
| U-D14 | Preferensi baru dititip di seam Reader Experience (aditif); guest via param | Q3-D7 — satu substrat personal; nol breaking | Tabel preferensi RAG terpisah |

**Asumsi:** U-A1 — model embedding multilingual yang lolos mini-eval id↔ar tersedia via provider
OpenAI-compatible (kriteria: kualitas Arab klasik, dimensi ±384, biaya; jika TIDAK lolos, kelas
terbuka berjalan lexical+rewrite saja dulu — degradasi yang direncanakan, bukan kegagalan);
U-A2 — K-1 (unit kitab) mendarat sebelum reroute buku apa pun; H-7/W-7 menyusul per-flag;
U-A3 — kuota edge tetap garis depan; U-A4 — keputusan O-7-1…5 memakai default aman bila diam.

> **Conflicts with charter (ditandai + charter diedit kecil):**
> 1. **D7 DIREVISI**: semula "lexical+struktural dulu, embeddings menyusul sebagai hybrid".
>    Rekomendasi ini menjadikan hybrid dense **pilar inti kelas-terbuka sejak GA** (dengan gerbang
>    mini-eval), bukan "menyusul belakangan". Stance pgvector-di-Postgres-yang-sama & tanpa
>    vector-DB eksternal TETAP. Register charter D7 diberi nota revisi 2026-07-07.
> 2. **Dokumen phase-7 versi sebelumnya digantikan**: keputusan lamanya R-D4 (tree permanen untuk
>    scoped) dan R-D5 (vektor menyusul) direvisi menjadi U-D4/U-D3.
> 3. **Glosarium charter §2.5 bertambah**: `EvidencePack`.

---

## 6. Interfaces (seams)

**Fase 7 MENGEKSPOS:** Search terpadu (browse); Ask terpadu dengan **skema jawaban terstruktur
ber-versi** + renderer flat kompat; kontrak internal **EvidencePack** (korpus baru mana pun yang
memenuhi 1B otomatis ikut ter-retrieve); lapisan inferensi bersama (dipakai enrichment F4/F6 dan
gate F8); eval-gate + dashboard (diwariskan ke Fase 8 sebagai gate permanen); **umpan flywheel
kurasi** ke antrean K-3/H-5 (registry-miss → tugas); field preferensi baru via seam Reader
Experience (FE onboarding/settings mengonsumsinya).

**Fase 7 MENGONSUMSI (verbatim, tanpa pelemahan):** 1B C1–C5 (unit, anchor, cross-ref, lisensi,
normalisasi, eligibility); H-7 (grading+isnad ikut sitasi); K-D4 (mesin-unreviewed keluar);
K-3/H-5 (tautan approved-only + antrean kurasi); K-2 (atribusi karya/edisi); W-7 (alias expansion,
filter entitas, ringkasan reviewed, confusion-set); Q-9 (parsing referensi); kuota edge; F1-B/C/H;
kebijakan operator O2/O4/O-7-1…5.

---

## 7. Open decisions (operator-owned)

**O-7-1 — Postur default keotentikan hadith di jawaban Ask.** *(carry-over)*
Opsi: (a) semua approved tampil ber-label derajat; (b) **default sahih/hasan, da'if muncul via
toggle "tampilkan semua derajat"**; (c) kecualikan da'if. *Rekomendasi:* (b) utk Ask, (a) utk
Search. *Default aman:* (b).

**O-7-2 — Kebijakan pertanyaan fatwa/hukum personal.** *(carry-over)*
Opsi: (a) **paparan posisi ter-atribusi + disclaimer + arahan konsultasi**; (b) tolak halus;
(c) jawab bebas (tertolak by design). *Rekomendasi & default aman:* (a).

**O-7-3 — Pagar biaya LLM bulanan.** *(carry-over)*
Opsi: (a) cap keras + alert 80%; (b) **ukur baseline sebulan → cap 2× baseline**; (c) tanpa pagar.
*Rekomendasi:* (b)→(a). *Default aman:* (b).

**O-7-4 — Lensa madhhab: default, framing, dan onboarding.** *(BARU — sensitif)*
*Kenapa penting:* fitur pembeda produk sekaligus titik tuduhan keberpihakan bila salah framing.
*Opsi:* (a) **opt-in eksplisit di pengaturan (default netral); framing selalu berlabel; TIDAK
ditanya saat onboarding** — paling aman, adopsi lebih lambat; (b) ditawarkan saat onboarding
dengan pilihan "tanpa preferensi" menonjol; (c) tanpa lensa madhhab sama sekali.
*Rekomendasi:* (a) sekarang, evaluasi (b) setelah invarian lensa terbukti di produksi.
*Default aman:* (a).

**O-7-5 — Gaya jawaban default.** *(BARU)*
*Opsi:* (a) **standar**; (b) ringkas (mobile-first); (c) syarah. *Rekomendasi:* (a) — dengan
gaya tersimpan per-user setelah dipilih. *Default aman:* (a).

---

## 8. Conformance

RAG Safety ditegakkan tiga lapis independen: indeks interpretatif tanpa unit Quran (konstruksi),
flag `quote_only` di perakitan EvidencePack (kode), dan kategori eval anti-tafsir yang memblokir
mutlak — makna hanya mengalir dari tafsir/kitab/hadith lewat tautan ter-kurasi (U-D2), tak pernah
diturunkan LLM. Domain Integrity hidup di kontrak jawaban: grading per-otoritas menempel di setiap
Dalil hadith (H-7) apa pun gayanya; Panel Ikhtilaf tak bisa disembunyikan lensa mana pun (U-D9,
diuji kategori eval sendiri); Hikmah hanya-bersumber (U-D8); provenance source/editorial/mesin
tak pernah tercampur karena terpisah di tingkat unit; `evidence_origin` membuat pembaca tahu mana
bukti ter-kurasi dan mana hasil pencarian; dan platform menolak berfatwa dengan suaranya sendiri
(O-7-2).

## 9. North-star fit

Fase ini mengubah north-star dari arsitektur menjadi pengalaman: satu pertanyaan → dokumen jawaban
ilmiah yang tersusun — ringkasan posisi, dalil ber-derajat dengan nash-nya, penjelasan, hikmah
yang ada sumbernya, dan peta ikhtilaf yang jujur — dipersonalisasi ke mazhab, gaya, dan bahasa
pembaca TANPA pernah menyembunyikan keragaman pendapat. Di bawahnya, aset paling khas Surau —
registry tautan ter-kurasi dan graph entitas — akhirnya menjadi mesin retrieval itu sendiri;
setiap pertanyaan yang gagal justru menumbuhkannya; dan setiap rilis harus membuktikan diri di
gerbang eval sebelum menyentuh pengguna. Wiki-nya bisa ditelusuri, ditanya, dipercaya — dan kini
juga terasa ditulis untuk masing-masing pembacanya.
