# Fase 6 — Wiki / Knowledge Entities (Greenfield Masterplan)

> **Terikat pada charter** (`roadmap/README.md`) dan **kontrak 1B** (Anchor/Cross-Reference/
> provenance/lisensi/normalisasi verbatim). **Input wajib yang diterima fase ini:** antrean
> perawi→entitas dari Fase 5 (H-D5 — Fase 5 = penulis pertama `knowledge_entities`; nota
> penajaman diterima, TANPA registry paralel), span entitas approved-only dari Fase 4 (K-6) dan
> Fase 5 (H-3), serta pola SEO/editorial dari Fase 3/4. Fase ini MEMBANGUN DI ATAS skema
> `knowledge_*` + pipeline langextract yang sudah ada — bukan di sampingnya.
> Ditulis 2026-07-07 setelah verifikasi langsung skema & jalur tulis.

---

## 1. Understanding — fondasi yang sudah terbangun (dan satu lubang arsitektur)

### 1.1 Aset yang diverifikasi hari ini

**16 tabel `knowledge_*` sudah ada** (migrasi `20260525000003` + `20260525000004`), dan lebih
matang dari yang diduga siapa pun:

- **Inti**: `knowledge_entities` (+`entity_labels` multilingual, +`entity_aliases`),
  `knowledge_mentions` (span teks ber-offset + confidence + `review_status` 5-nilai standar),
  `knowledge_relations` (subjek-predikat-objek + `certainty` explicit/probable/ambiguous),
  `knowledge_claims`.
- **Mesin resolusi**: `knowledge_entity_candidates` — antrean disambiguasi **per-strategi**
  `(mention_id, entity_id, strategy)` dengan `score`, `reasons` JSONB, `review_status`; dan
  `knowledge_entity_links` — klaim identitas entitas↔entitas dengan `link_type`
  **`same_as / possibly_same_as / not_same_as / merge / split`**, score, `decision_status`,
  `reviewer_notes`. Artinya: **mesin merge/split ber-review + memori keputusan-negatif
  (`not_same_as` mencegah saran merge buruk berulang) SUDAH dimodelkan** — Fase 6
  mengindustrialisasi, bukan mendesain dari nol.
- **Jejak ekstraksi**: `extraction_runs`, `prompt_versions` (prompt ber-versi DI DATABASE),
  `extraction_documents`/`chunks`, `source_spans`, `extraction_rejections` (QA rejects tercatat).
- **Taksonomi**: `knowledge_taxonomies` + `entity_taxonomy_links`, ter-seed domain ilmu (baris
  'hadith' dll. — dipakai glossary).
- **Pipeline** `scripts/langextract_kg/` (7.700+ baris): 4 task ber-prompt-versi; kelas mention
  person/person_reference/theonym/place/work_title/group/institution; kelas istilah 8 domain;
  guard nama-umum (أحمد, محمد → ambiguous, tak pernah auto-merge); **relations/claims DINONAKTIFKAN
  default** (berisiko tinggi — benar).

### 1.2 Lubang arsitektur yang harus ditutup lebih dulu

**Tidak ada satu pun jalur tulis Go ke `knowledge_*`** (diverifikasi: satu-satunya kode Go yang
menyentuhnya adalah FK resolver Quran). Semua tulisan — termasuk transisi status yang bermakna
kurasi — hari ini adalah **SQL mentah dari Python** (`scripts/langextract_kg/db.py`). Tidak ada
service, tidak ada audit siapa-memutuskan-apa, tidak ada ETag, tidak ada peran. Untuk pipeline
ekstraksi eksperimental itu wajar; untuk WIKI yang klaim-klaimnya dibaca publik dan menjadi
grounding RAG, itu disqualifying. Menutup lubang ini = inisiatif pertama (W-0).

### 1.3 Garis batas dengan fase lain (ditegaskan, sesuai mandat)

- **Fase 4 (K-6) / Fase 5 (H-3)** memiliki: plumbing span (offset mention pada unit, re-anchoring,
  penyajian approved-only di teks). **Fase 6** memiliki: halaman entitas yang diklik span itu,
  taksonomi, disambiguasi, relasi, glossary, backlink, governance klaim.
- **Fase 5** mengirim antrean kandidat perawi + otoritas grading → Fase 6 mengkurasi & memberi
  halaman. Jarh wa ta'dil (penilaian atas PERAWI) = Fase 6 (memakai pola Grading Assertion H).
- **Fase 7** mengonsumsi: alias untuk query expansion, mention-link untuk filter retrieval,
  ringkasan entitas reviewed sebagai unit eligible, anchor entitas sebagai grounding.

---

## 2. Vision — wiki yang setiap kalimatnya punya pemilik

### 2.1 Taksonomi entitas (keputusan — datar di tipe, kaya di klaim)

**Lima tipe inti** (selaras kelas ekstraksi yang sudah menghasilkan data): **Person** (perawi,
ulama, sahabat, penguasa — peran BUKAN subtipe kaku, melainkan klaim ber-atribusi; satu orang
boleh perawi+faqih+mufassir sekaligus), **Place**, **Work** (JEMBATAN ke registry Work K-2 —
lihat W-D4), **Term/Concept** (istilah fiqh/aqidah/mustalah/tasawwuf/adab/bahasa — di-tag domain
via `knowledge_taxonomies` yang ter-seed → inilah glossary), **Group/Institution** (kabilah,
mazhab, lembaga). **Theonym** = kebijakan khusus: tidak pernah auto-link; konten Asma'ul Husna
bila dibuat adalah halaman konsep editorial ber-governance ketat, bukan hasil pipeline. Tipe
baru (mis. Event) = ekstensi terkurasi kemudian, bukan v1.

Atribut khas rijal (kunya/nasab/laqab sebagai alias terstruktur; tabaqa/generasi; tahun wafat)
= **klaim ber-atribusi dengan sitasi sumber**, karena di dunia nyata angka-angka ini diperselisihkan.

### 2.2 Prinsip yang mengikat semua desain fase ini

1. **Satu registry** (mandat misi + H-D5): semua entitas hidup di `knowledge_entities`; merge/
   split lewat `knowledge_entity_links` + lineage/redirect (anchor entitas stabil selamanya —
   pola 1B).
2. **Setiap tautan adalah klaim**: mention→entitas (kandidat approved), entitas↔entitas
   (same_as/merge), relasi, dan atribut — semuanya ber-metode/score/review, tidak ada plumbing
   netral.
3. **Klaim non-derived tanpa sitasi sumber = tidak bisa approved.** Sitasi sumber = anchor 1B ke
   ayat/hadith/paragraf kitab. Wiki ini tidak berbicara dengan suaranya sendiri.
4. **Mesin mengusulkan, manusia memutuskan, turunan dihitung**: tiga metode klaim — machine
   (ber-run-identity, selalu pending), human (ber-aktor), **derived** (dihitung deterministik
   dari data terkurasi — mis. relasi narrated-from dari posisi isnad; bukan LLM, bisa diaudit).
5. **Graph hidup di Postgres** (recursive CTE untuk rantai guru-murid/isnad; indeks yang ada) —
   TANPA graph-DB terpisah; pgvector menyusul di Fase 7 di samping, bukan menggantikan.

### 2.3 Bar kuantitatif (menambah charter §2.3)

Kurasi: 0 transisi status di luar service Go ber-audit (ditegakkan grants + audit); 100% klaim
approved non-derived punya sitasi sumber. Disambiguasi: presisi auto-link ≥98% (diukur sampling
bulanan); antrean Fase 5 ter-SLA — **top-500 perawi terfrekuensi terkurasi sebelum span hadith
dibuka luas**; setiap alias dengan ≥2 entitas approved punya halaman disambiguasi. Halaman:
p95 <300ms (backlink ter-paginasi); 100% entitas published ber-slug + masuk sitemap (lastmod ≤5
menit); label/ringkasan mengikuti pola exact-language. Relasi: 0 relasi sensitif tampil tanpa
approve scholar-reviewer; relasi hasil ekstraksi mesin 100% lahir pending.

---

## 3. Gap & opportunity analysis

| # | Komponen | Prioritas | Effort | Catatan risiko |
|---|---|---|---|---|
| W-G1 | Jalur tulis & governance: service kurasi Go + peran + audit (menutup §1.2) | **P0** | Sedang | Tanpa ini semua kurasi = SQL tanpa jejak; memblokir semuanya |
| W-G2 | Taksonomi + konsolidasi identitas + anchor/slug entitas + jembatan Work↔K-2 | **P0** | Sedang | Klasifikasi entitas hasil ekstraksi lama |
| W-G3 | Disambiguasi industrial (ranking berkonteks, auto-link terbatas, merge/split tooling, SLA antrean Fase 5) | **P0–P1** | Besar | Ribuan rijal bernama mirip; guard nama-umum sudah ada — jangan dilonggarkan |
| W-G4 | Halaman entitas + backlink + disambiguation pages + glossary + SEO | P1 | Sedang | Produk yang membuat wiki TERLIHAT |
| W-G5 | Relasi ber-tipe + derived-from-isnad + gerbang scholar utk kelas sensitif | P1 | Sedang | relations/claims langextract tetap pending-only |
| W-G6 | Kedalaman rijal: jarh wa ta'dil per-otoritas (pola Grading Assertion), tabaqa, nama terstruktur | P1–P2 | Sedang | Sangat sensitif manhaj (O-6-2) |
| W-G7 | Koreksi/dispute publik + riwayat keputusan klaim | P2 | Kecil–sedang | Rate-limit + antrean, bukan crowdsourcing |
| W-G8 | Serah-terima grounding RAG (expansion/filter/eligibility + seed eval) | P1 | Kecil | Kontrak konsumsi Fase 7 |

**Risiko terbesar fase**: (1) membuka span luas sebelum kurasi mengejar → wiki penuh halaman
kosong/salah-link — mitigasi: SLA antrean + span hanya approved (sudah dikontrak K-6/H-3);
(2) auto-link over-eager pada nama manusia — mitigasi: kebijakan W-D5 (manual-first untuk Person);
(3) klaim sensitif tanpa kerangka — mitigasi: kelas risiko + scholar-reviewer WAJIB ada sebelum
W-5/W-6 menyala (O-6-1).

---

## 4. Roadmap — inisiatif Fase 6

Urutan: **W-0 → W-1 → W-2 → (W-3 ∥ W-4) → W-5/W-6 → W-7.** W-2 mengonsumsi antrean H-3 begitu
tersedia; W-3 butuh pola SEO Q-4/K-4; W-7 menutup fase menuju Fase 7.

### W-0 — Layanan kurasi & governance klaim (menutup jalur tulis)  *(P0, effort sedang)*

**Rationale:** §1.2; charter menandai governance sebagai celah lintas-domain — mekanismenya
didesain DI SINI. **Isi:** service kurasi Go menjadi SATU-SATUNYA jalur transisi status
(approve/reject/merge/split/link/dispute) atas mentions/candidates/entities/relations/claims —
dengan audit (siapa/kapan/alasan), ETag, dan peran; pipeline Python tetap menulis LANGSUNG hanya
keluaran kelas-pending miliknya (runs/documents/chunks/mentions/spans/candidates/rejections)
dengan grants DB yang secara teknis menutup kemampuan mengubah status; **kelas risiko klaim**:
(a) struktural/netral (tempat lahir, karya, tahun wafat) → editor boleh approve; (b) sensitif
(afiliasi mazhab, posisi aqidah, jarh wa ta'dil) → **peran scholar-reviewer baru** + sitasi sumber
wajib; (c) yang platform tak boleh ucapkan (takfir, polemik sekte) → hanya kutipan ter-atribusi
dengan framing pelaporan, tidak pernah klaim bersuara platform (charter §2.1). Aturan emas: klaim
non-derived tanpa anchor sumber tidak bisa approved.
**AC:** transisi status via SQL langsung tidak mungkin lagi (grants membuktikan); setiap keputusan
kurasi punya jejak audit lengkap; klaim tanpa sitasi tertolak di service; peran scholar-reviewer
ditegakkan pada kelas (b).
**DS:** untuk klaim apa pun di wiki, Salman bisa bertanya "siapa yang menyetujui ini, kapan,
berdasarkan sumber apa" — dan selalu ada jawabannya.

### W-1 — Taksonomi, identitas, dan jembatan Work  *(P0, effort sedang)*

**Rationale:** W-G2. **Isi:** lima tipe inti §2.1 + peran-sebagai-klaim; klasifikasi/normalisasi
entitas hasil ekstraksi yang sudah ada; **anchor entitas** dideklarasikan (kontrak 1B C1: korpus
`entity`; ID stabil + slug; merge → redirect via lineage — memakai `merge/split` di
`knowledge_entity_links` yang sudah ada); **jembatan Work**: mention `work_title` diresolusi ke
registry Work K-2 (satu identitas karya lintas katalog & wiki; halaman karya = data katalog +
klaim wiki — TANPA entitas karya duplikat); kebijakan theonym ditegakkan (tanpa auto-link).
**AC:** setiap entitas punya tepat satu tipe inti + anchor + slug; merge apa pun meninggalkan
redirect yang resolvable; entitas Work menaut 1:1 ke Work K-2; nol theonym ter-auto-link.
**DS:** "Imam an-Nawawi" adalah SATU identitas — sebagai pengarang di katalog, sebagai entitas di
wiki, sebagai penilai di hadith — bukan tiga.

### W-2 — Disambiguasi industrial + konsumsi antrean Fase 5  *(P0–P1, effort besar)*

**Rationale:** W-G3; ribuan rijal menunggu. **Isi:** ranking kandidat berfitur konteks di atas
mesin per-strategi yang sudah ada (`reasons` JSONB diisi bukti): kedekatan isnad (guru/murid pada
posisi bersebelahan = sinyal kuat), konsistensi era/tabaqa, ko-okurensi entitas dalam unit yang
sama, korpus asal; **kebijakan auto-link**: HANYA alias unik tanpa kolisi registry + score ≥0.95
→ approved-machine ber-sampling audit (target presisi ≥98%); **Person default manual-first**
(O-6-4); tooling merge/split di atas `knowledge_entity_links` (same_as/not_same_as sebagai memori
keputusan); SLA antrean Fase 5: top-500 perawi terfrekuensi dikurasi sebelum span hadith dibuka
luas; dashboard antrean (umur, ambiguitas, throughput).
**AC:** setiap kandidat ter-ranking dengan alasan terlihat; auto-link terbatas persis pada
kebijakan (diaudit); presisi sampling terukur bulanan ≥98%; `not_same_as` mencegah saran berulang;
antrean top-500 selesai sebelum gerbang span-luas hadith.
**DS:** editor membuka antrean dan melihat "kemungkinan besar ini Fulan bin Fulan — karena
gurunya X dan muridnya Y" — memutuskan dalam detik, bukan menit riset.

### W-3 — Halaman entitas, backlink, glossary, SEO  *(P1, effort sedang)*

**Rationale:** W-G4 — inilah produk wiki-nya. **Isi:** halaman entitas = ringkasan editorial
(workflow standar draft/publish+ETag+revisi; multilingual exact-language) + klaim ter-atribusi
per kelompok + **backlink lintas korpus** (mention approved di kitab/hadith + kemunculan isnad +
karya di katalog + grading yang dia keluarkan — semua via registry yang ada, ter-paginasi) +
halaman disambiguasi untuk alias bersama; **glossary** per domain (taksonomi ter-seed) untuk
entitas Term; slug/SEO/sitemap port pola Q-4/K-4 (redirect permanen; hanya published+permitted).
**AC:** halaman perawi menampilkan backlink dari ≥2 korpus; alias dengan ≥2 entitas → halaman
disambiguasi otomatis; 100% entitas published masuk sitemap dengan lastmod akurat; ringkasan
hanya lewat workflow editorial.
**DS:** klik "Imam Syafi'i" di teks kitab membuka halaman yang menunjukkan biografinya (dengan
sumber), semua tempat beliau disebut, karya-karyanya, dan siapa gurunya — janji charter jadi
nyata di layar.

### W-4 — Relasi ber-tipe + relasi turunan isnad  *(P1, effort sedang)*

**Rationale:** W-G5. **Isi:** kosakata predikat terkurasi (orang↔orang: guru-murid,
meriwayatkan-dari, kerabat; orang↔karya: mengarang/meringkas/mensyarah — konsisten K-2;
orang↔tempat: lahir/wafat/tinggal; orang↔kelompok: berafiliasi [sensitif → kelas (b)]; karya↔karya:
syarah/mukhtasar; istilah↔istilah: lebih-luas/sinonim); **relasi `meriwayatkan-dari` diturunkan
DETERMINISTIK dari posisi isnad terkurasi Fase 5** (method=derived, bukti = anchor hadith;
dihitung ulang saat kurasi berubah — bukan LLM); ekstraksi relations/claims langextract diaktifkan
HANYA ke pending + evidence span wajib; kelas sensitif tak tampil tanpa scholar-approve.
**AC:** graph guru-murid perawi terbangun otomatis dari isnad terkurasi dengan setiap edge
menunjuk bukti hadith-nya; 0 relasi sensitif tampil tanpa approve; 100% relasi mesin lahir pending.
**DS:** di halaman seorang perawi terlihat jaring "meriwayatkan dari / diriwayatkan oleh" — dan
setiap garis bisa diklik ke hadith buktinya.

### W-5 — Kedalaman rijal: jarh wa ta'dil per-otoritas  *(P1–P2, effort sedang; gerbang O-6-1/O-6-2)*

**Rationale:** W-G6; melengkapi grading hadith (Fase 5) dengan penilaian PERAWI. **Isi:** pola
Grading Assertion dipakai ulang apa adanya — target = entitas perawi; otoritas = entitas (Ibn
Hajar, adz-Dzahabi, …); lafaz verbatim (thiqah, saduq, matruk, …) + pemetaan kosakata terkontrol;
sumber sitasi wajib; kelas sensitif → scholar-reviewer; tabaqa & nama terstruktur (kunya/nasab/
laqab sebagai alias ber-tipe) untuk memperkuat disambiguasi W-2.
**AC:** halaman perawi menampilkan penilaian per-otoritas ter-atribusi + sumber (tanpa label
global); data tabaqa/nama memperbaiki ranking kandidat secara terukur.
**DS:** "thiqah menurut Ibn Hajar; layyin menurut fulan" — pluralitas yang jujur, seperti di
hadith.

### W-6 — Koreksi, dispute, dan riwayat klaim  *(P2, effort kecil–sedang)*

**Rationale:** W-G7; charter: "bagaimana koreksi/sengketa bekerja saat korpus tumbuh". **Isi:**
endpoint lapor publik (rate-limited, tanpa akun boleh — pola translation-feedback) → antrean
dispute → re-review dengan jejak; riwayat keputusan klaim terekspos di API kurasi (versi, aktor,
alasan); klaim yang disengketakan tampil dengan penanda status sampai diputus.
**AC:** laporan publik masuk antrean ber-rate-limit; setiap klaim punya riwayat keputusan yang
bisa dilihat kurator; dispute tidak menghilangkan klaim diam-diam (status, bukan delete).
**DS:** pembaca yang menemukan biografi keliru bisa melapor — dan laporan itu sampai ke meja
kurator, bukan lubang hitam.

### W-7 — Serah-terima grounding RAG  *(P1, effort kecil)*

**Rationale:** W-G8; kontrak konsumsi Fase 7. **Isi:** kapabilitas **query expansion** (alias
multilingual + bentuk C5 per entitas), **filter retrieval ber-entitas** ("apa kata X tentang Y" →
unit yang mention-nya approved ke entitas X), ringkasan entitas reviewed = unit eligible (kelas
editorial, C4), anchor entitas sebagai grounding sitasi; seed golden eval entitas (pertanyaan
biografis ber-sumber, pertanyaan "menurut ulama X", kasus disambiguasi nama).
**AC:** ketiga kapabilitas tersedia sebagai kontrak yang bisa dipanggil Fase 7; ≥10 kasus eval
entitas masuk golden set; unit ringkasan unreviewed tidak pernah eligible.
**DS:** kelak bertanya "apa pendapat Imam Syafi'i tentang qunut" menarik jawaban yang benar-benar
tersaring ke karya beliau — karena wiki tahu siapa "beliau".

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| W-D1 | Industrialisasi DI ATAS `knowledge_*` existing; nol tabel registry baru untuk konsep yang sudah dimodelkan | Mandat misi + bukti §1.1 (merge/split/kandidat sudah ada); registry kedua = utang terlarang | Rancang ulang skema "lebih bersih" |
| W-D2 | Transisi status HANYA via service Go ber-audit; Python menulis langsung hanya keluaran pending miliknya (dipisah grants DB) | Governance tanpa jalur tulis tunggal = fiksi; ekstraksi tetap produktif | Semua lewat Go (memperlambat riset ekstraksi); status quo SQL bebas |
| W-D3 | Taksonomi datar 5 tipe inti + peran/domain sebagai klaim | Kelas ekstraksi sudah menghasilkan bentuk ini; ontologi dalam membusuk; peran itu jamak & diperselisihkan | Pohon subclass dalam (scholar⊂person⊂…) |
| W-D4 | Entitas Work = jembatan 1:1 ke registry Work K-2 (bukan duplikat) | Satu identitas karya lintas katalog & wiki; backlink karya gratis | Entitas karya wiki tersendiri (dua kebenaran) |
| W-D5 | Auto-link: HANYA alias unik + score ≥0.95, approved-machine ber-sampling; **Person manual-first** | Presisi > cakupan untuk nama manusia; guard nama-umum pipeline dipertahankan | Auto-link agresif ber-threshold global; manual semua (antrean tak terkejar utk Term/Work) |
| W-D6 | Merge/split = klaim ber-lineage + redirect; `not_same_as` = memori negatif | Anchor entitas tak boleh mati; keputusan buruk tak boleh terulang | Hard-merge destruktif |
| W-D7 | Tiga metode klaim: machine (pending selalu) / human / **derived** (deterministik dari data terkurasi, bukti anchor) | Relasi isnad→guru-murid bisa dihitung tanpa LLM — auditable & murah | Semua relasi via ekstraksi LLM |
| W-D8 | Klaim non-derived approved WAJIB sitasi sumber (anchor 1B) | "Attribution & non-authorial voice" charter — wiki melaporkan, tak berfatwa | Klaim tanpa sumber "sementara" |
| W-D9 | Kelas risiko klaim (netral/sensitif/terlarang-bersuara-platform) + peran scholar-reviewer utk sensitif | Mekanisme governance yang charter tugaskan ke fase ini | Satu tingkat review utk semua (terlalu longgar utk jarh, terlalu berat utk tempat-lahir) |
| W-D10 | Graph di Postgres (CTE rekursif); TANPA graph-DB; pgvector menyusul Fase 7 berdampingan | Skala rijal (puluhan ribu node) nyaman di Postgres; moving part baru tak terjustifikasi | Neo4j/graph-DB terpisah |
| W-D11 | Jarh wa ta'dil = Grading Assertion pattern (per-otoritas + lafaz verbatim) atas entitas perawi | Konsistensi platform: satu pola utk semua penilaian ter-atribusi | Model penilaian rijal tersendiri |
| W-D12 | Kontribusi publik v1 = lapor-saja (rate-limited) → antrean; TANPA suggest-edit/UGC | Moderasi konten agama butuh kerangka yang belum ada; mulai aman | Wiki terbuka ala Wikipedia |

**Asumsi:** W-A1 — antrean H-3 (Fase 5) mulai terisi sebelum W-2 berjalan penuh (kalau Fase 5
tertunda, W-2 tetap jalan atas mention kitab dari ekstraksi existing); W-A2 — tersedia minimal
satu scholar-reviewer sebelum W-5/klaim sensitif menyala (O-6-1 — kalau belum, kelas (b) tetap
tertutup dan itu benar); W-A3 — volume entitas (puluhan ribu) & backlink tertampung Postgres
ter-tuning F1-G; W-A4 — span K-6/H-3 tetap approved-only (kontrak fase 4/5 dipegang).

> **Conflicts with charter: tidak ada.** Nota penajaman Fase 5 (H-D5 — Fase 5 penulis pertama
> `knowledge_entities`) DITERIMA sebagai input resmi fase ini; antrean kandidatnya menjadi beban
> kerja W-2 dengan SLA eksplisit. Governance yang charter tandai sebagai celah lintas-domain kini
> punya mekanisme (W-0/W-D9) — sesuai penugasan charter §4.1, bukan penyimpangan.

---

## 6. Interfaces (seams)

**Fase 6 MENGEKSPOS:**
- **Anchor + slug entitas** (resolusi 1B, redirect pasca-merge) — dikonsumsi span K-6/H-3, FE,
  Fase 7.
- **Halaman entitas + backlink lintas korpus + disambiguasi + glossary** (produk browse).
- **Kapabilitas grounding RAG** (W-7): query expansion ber-alias, filter retrieval ber-entitas,
  ringkasan reviewed sebagai unit eligible.
- **Mekanisme governance klaim** (kelas risiko + scholar-review + dispute + riwayat) — dipakai
  ulang domain mana pun yang menampilkan klaim (grading Fase 5 kelak bisa menumpang antrean
  dispute yang sama).
- **Graph relasi terkurasi** (guru-murid/meriwayatkan-dari ber-bukti) — bahan navigasi FE dan
  sinyal retrieval Fase 7.

**Fase 6 MENGONSUMSI:** skema `knowledge_*` + pipeline langextract (§1.1, apa adanya); antrean
perawi/otoritas Fase 5 (H-3/H-D5); span plumbing K-6/H-3; registry Work K-2; pola editorial/SEO
standar (Fase 3/4); kontrak 1B C1–C5; peran Fase 2 + peran scholar-reviewer baru (W-0);
F1-B/C (metrik antrean, supervisi job derived); keputusan operator O-6-1…4.

---

## 7. Open decisions (operator-owned)

**O-6-1 — Siapa scholar-reviewer.**
*Kenapa penting:* kelas klaim sensitif (jarh wa ta'dil, afiliasi mazhab) TIDAK menyala tanpa
peran ini terisi; ini keputusan SDM/kepercayaan, bukan teknis. *Opsi:* (a) mulai dengan 1–2
reviewer tepercaya yang sudah mereview konten Surau (pola `reviewed_by` yang ada); (b) bentuk
dewan kecil dengan SOP tertulis; (c) tunda semua klaim sensitif. *Rekomendasi:* (a) sekarang →
(b) saat volume naik. *Default aman:* (c) — kelas sensitif tetap tertutup sampai ada nama.

**O-6-2 — Kebijakan tampilan klaim sensitif (jarh wa ta'dil, mazhab).**
*Kenapa penting:* framing yang salah membuat platform tampak menghakimi ulama/kelompok. *Opsi:*
(a) tampil penuh per-otoritas dengan framing pelaporan ("dinilai X oleh Y") — konsisten grading
hadith; (b) tampil hanya di halaman entitas (tidak di tooltip/span); (c) hanya untuk pengguna
masuk. *Rekomendasi:* (a) dengan (b) sebagai batas penyajian — kedalaman di halaman, bukan di
lintasan baca. *Default aman:* (b).

**O-6-3 — Cakupan kontribusi publik.**
*Kenapa penting:* menentukan beban moderasi. *Opsi:* (a) lapor-saja (W-6); (b) + usul-suntingan
berantrian; (c) UGC terbuka. *Rekomendasi:* (a) di v1 — naik ke (b) hanya setelah metrik antrean
sehat. *Default aman:* (a).

**O-6-4 — Lingkup auto-link mesin.**
*Kenapa penting:* trade-off kecepatan pengisian wiki vs risiko salah-orang. *Opsi:* (a)
manual-first untuk Person; auto hanya Term/Work ber-alias unik (kebijakan W-D5); (b) auto juga
untuk Person ber-threshold; (c) manual semua. *Rekomendasi:* (a). *Default aman:* (a).

---

## 8. Conformance

Wiki adalah tempat paling mudah bagi makna tanpa pemilik menyelinap — maka fase ini menjadikannya
mustahil secara struktural: setiap klaim ber-metode/atribusi/review; klaim approved wajib sitasi
sumber ke anchor korpus (W-D8); kelas sensitif di belakang scholar-review dan yang terlarang
disuarakan platform hanya hadir sebagai kutipan ter-atribusi (W-D9); mention→entitas dan
merge/split adalah klaim ber-confidence dengan memori keputusan (W-D5/6); relasi turunan membawa
bukti anchor hadith-nya (W-D7); dan ayat Quran hanya pernah menjadi SUMBER SITASI klaim — tidak
pernah ditafsirkan oleh pipeline mana pun di fase ini.

## 9. North-star fit

Inilah lapisan yang mengubah empat korpus menjadi SATU wiki: entitas adalah simpul tempat ayat,
hadith, kitab, dan katalog bertemu — dan grounding yang membuat RAG Fase 7 bisa menjawab "menurut
siapa" dengan presisi. Dengan registry yang sudah ada diindustrialisasi (bukan dibangun ulang),
antrean rijal Fase 5 yang mengalir masuk, governance yang membuat setiap kalimat punya pemilik,
dan halaman-halaman yang akhirnya bisa diklik dari dalam teks — "wiki Islam paling lengkap" berhenti
menjadi metafora arsitektur dan mulai menjadi produk yang dilihat pengguna.
