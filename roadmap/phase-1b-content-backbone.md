# Fase 1B — Content Backbone (kontrak lintas-korpus)

> **Terikat pada charter** (`roadmap/README.md`): menjalankan keputusan D2–D6 & D9 dan memakai
> glosarium §2.5 verbatim (Anchor, Citable Unit, Cross-Reference, Provenance Class, Authority,
> Work/Edition, License Status). Fase ini adalah SEAM yang dikonsumsi Fase 3–7 — kontraknya
> decision-complete; implementasi per-korpus tetap milik fase domain.
> Bergantung pada mekanisme Fase 1: playbook expand-contract & backfill resumable (F1-H),
> metrik/alert (F1-B), supervisi job (F1-C).
> Ditulis 2026-07-06 setelah verifikasi langsung atas ketiga embrio (DDL
> `quran_book_references`, skema `knowledge_*`, parser `SourceBlock`).

---

## 1. Understanding — embrio yang sudah ada (dan seberapa jauh bisa dipakai)

Temuan terpenting fase ini: **pola "klaim ter-review" sudah menjadi konvensi de-facto lintas dua
subsistem yang dibangun terpisah** — backbone tinggal memformalkannya, bukan menciptakan bahasa baru.

1. **`quran_book_references`** (migrasi `20260526000001`, baris 179–234) adalah Cross-Reference
   dalam bentuk hampir final: jejak bukti (`source_text` + `normalized_text`), jenis tautan
   (`reference_kind`: surah_ayah/surah/quote/ambiguous), metode (`match_strategy`), keyakinan
   (`confidence NUMERIC` 0–1 ber-CHECK), status kurasi (`review_status`:
   pending/approved/rejected/ambiguous/needs_review), rentang target ber-constraint konsistensi
   (from/to ayah + derived `ayah_key`), FK opsional ke `knowledge_mentions`, dan **indeks dua arah**
   (per-buku DAN per-surah) — artinya backlink query sudah terbukti. Kekurangannya satu: ia
   satu-pasangan-korpus (kitab→Quran) dan hidup di dalam domain Quran.
2. **Skema `knowledge_*`** (migrasi `20260525000003`) memakai **enum `review_status` 5-nilai yang
   persis sama** di mentions/entities/labels/aliases/candidates/relations, plus `confidence
   NUMERIC(4,3)` dan `certainty` (explicit/probable/ambiguous), plus konsep **run** ekstraksi
   (`knowledge_extraction_runs`) dengan prompt ber-versi di pipeline Python. Ini bukti disiplin
   klaim + identitas-mesin sudah dipraktikkan.
3. **Parser `SourceBlock`** (`internal/readerutil/content.go:72`; kinds paragraph/heading/
   quran_quote/html + `SourceFootnote` + `QuranCitations` inline) sudah menghasilkan unit
   granularitas paragraf — **tetapi hanya in-memory**: tanpa ID, tanpa persistence, sehingga sitasi
   book-RAG hari ini berhenti di granularitas heading/page dan rapuh terhadap suntingan.
4. **`license_status`** (enum 5-nilai + gerbang query "hanya `permitted` tampil") hidup di Quran
   saja; kitab — korpus dengan risiko lisensi terbesar — tidak punya padanannya.
5. **Normalisasi Arab ada dua implementasi paralel** (Go `quranutil.NormalizeKey`; Python
   `arabic_normalize.py`) tanpa versi dan tanpa uji kesetaraan — hasil matching bisa diam-diam
   berbeda antara search, resolver rujukan, dan ekstraksi.
6. **Anchor hari ini per-domain dan campur logis/fisik**: `ayah_key "s:a"` (logis, kanonik, bagus);
   kitab memakai `heading_id`/`page_id` (artefak impor — stabil hanya karena impor tak pernah
   menata ulang) dan anchor URL `toc-{heading_id}`; `printed_page`/`part` fisik dan ambigu.

**Kesimpulan Understanding:** semua bahan mentah backbone sudah ada dan saling hampir-kompatibel.
Yang belum ada: (a) substrat unit yang persisten dan ber-ID, (b) generalisasi rujukan ke
any-corpus→any-corpus, (c) satu grammar Anchor lintas-korpus + kapabilitas resolusi, (d) lisensi &
provenance sebagai kerangka platform, (e) normalisasi kanonik ber-versi.

---

## 2. Vision — lima kontrak backbone (decision-complete)

Backbone = **lima kontrak + satu lapisan bersama minimal**. Setiap fase domain mengimplementasikan
kontraknya di korpusnya; Fase 7 hanya mengonsumsi. Bentuk serialisasi byte-level (sintaks persis,
nama kolom/endpoint) diputuskan sesi implementasi — yang dikunci di sini adalah komponen, semantik,
dan invariannya.

### C1 — Anchor: alamat kanonik lintas-korpus

Kontrak:
- **Komponen alamat** (berurut): (1) diskriminator korpus; (2) lingkup Work/Edition bila korpus
  memuat banyak karya (kitab, hadith; Quran ber-lingkup implisit karena satu karya); (3) hierarki
  locator **logis** (mis. surah→ayah; kitab→heading→unit-ordinal; koleksi→bab→nomor-hadith);
  (4) bentuk **rentang** opsional (anchor-awal..anchor-akhir, wajib satu lingkup karya).
- **Logis, bukan fisik.** Halaman cetak (`printed_page`, `part`, `page_id`) menjadi metadata
  locator sekunder untuk tampilan/sitasi akademik — bukan identitas. (Hari ini kitab beralamat
  fisik; unit ordinal di bawah heading menggantikannya sebagai identitas.)
- **Sekali dicetak, tak pernah didaur ulang.** Anchor/ID yang dihapus menjadi tombstone; tidak ada
  penggunaan ulang alamat untuk konten berbeda.
- **Tahan suntingan via lineage, bukan via kekekalan teks**: suntingan yang memecah/menggabung unit
  mencetak unit BARU dan menandai pendahulunya superseded dengan tautan silsilah; anchor lama
  **selalu terselesaikan** ke penerusnya (rantai redirect), tidak pernah 404.
- **Kapabilitas resolusi** (dibangun sekarang): menerima anchor apa pun — kanonik ATAU legacy
  (`ayah_key`, `toc-{heading_id}`, page) — dan mengembalikan unit/baris aktif + status
  (active/superseded/tombstoned) + rantai redirect. Target: p95 ≤50ms (lookup terindeks);
  100% anchor legacy yang dipakai FE hari ini resolvable.

### C2 — Citable Unit: satu substrat untuk Lookup, Search, dan Ask

Kontrak:
- **Satu registry fisik bersama** untuk unit semua korpus (keputusan; runner-up ditolak: tabel unit
  per-korpus + view gabungan — divergen, lifecycle/lineage terduplikasi 4×). Tabel korpus yang ada
  (mis. `quran_ayahs`, `book_pages`) tetap sumber kebenaran teks tampilan; registry memegang
  identitas, lifecycle, provenance, lisensi, dan teks ternormalisasi untuk pengindeksan. **Semua
  tulisan ke registry lewat satu service bersama** (usecase) — tidak ada jalur tulis langsung —
  dengan pemeriksaan invarian sinkron agar registry dan tabel korpus tidak pernah hanyut.
- **Granularitas ± paragraf** (charter D3). Pemetaan per korpus: kitab = `SourceBlock`
  (paragraph/heading/quran_quote) + footnote sebagai **jenis unit sendiri** yang tertaut ke unit
  induknya (footnote harus bisa dialamatkan — footnote tafsir adalah konten bernilai); Quran =
  ayah (jenis: teks-primer) dan tiap terjemahan-per-ayah (jenis: rendering-terjemahan, ter-atribusi
  ke sumber penerjemahnya); hadith (nanti) = matn/isnad per riwayat; wiki = klaim/paragraf artikel.
- **Muatan wajib tiap unit**: identitas stabil opaque + jalur Anchor kanonik; lingkup struktural
  induk + ordinal; teks (+ html bila ada); bahasa; **Provenance Class** + detailnya (source → rilis
  impor/edisi; editorial → aktor; machine → model + versi prompt + run); slot atribusi
  (Work/Edition, pengarang, penerjemah/qari bila relevan — slotnya kontrak sekarang, pemodelan
  Work/Edition milik fase domain); **License Status** (mewarisi dari Work/Edition, boleh override
  per unit — mis. tambahan editorial milik platform di dalam karya berlisensi ketat);
  status lifecycle (active/superseded/tombstoned) + lineage; versi normalisasi teks-turunannya;
  `updated_at` (ETag).
- **Invarian determinisme**: menjalankan ulang deriver di atas sumber yang tidak berubah
  menghasilkan himpunan unit identik dengan ID identik (pencocokan hash-konten + ordinal).
  Pilot wajib ≥99,5% stabil; produksi 100% (selisih = bug).
- **Invarian nol-sitasi-menggantung**: job audit berkala menghitung sitasi/rujukan yang menunjuk
  unit tanpa resolusi — angkanya harus 0; pelanggaran memicu alert (memakai F1-B/C).
- **Aturan RAG Safety pada substrat** (penegakan statis, bukan prompt): permukaan **Ask** untuk
  interpretasi hanya boleh menarik unit dengan (korpus ≠ Quran) DAN (class = source, ATAU class ∈
  {editorial, machine} yang berstatus review disetujui). Unit korpus Quran (ayah + terjemahannya)
  tersedia penuh untuk **Lookup/Search** dan untuk dirender sebagai sitasi — tidak pernah sebagai
  sumber makna.

> **Conflicts with charter (refinement, bukan pembatalan):** charter §2.1 menulis "Quran tidak
> punya Citable Unit interpretatif". Fase ini merumuskannya lebih presisi: **Quran PUNYA Citable
> Unit** (agar Lookup/Search/penyajian sitasi berdiri di substrat yang sama) — yang tidak ada
> adalah **kelayakan interpretatif**: aturan eligibilitas di atas mengecualikan korpus Quran dari
> retrieval-interpretasi secara statis dan dapat diuji. Resolusi: prinsip charter tetap ditegakkan,
> mekanismenya dipertegas; Fase 7 wajib menguji aturan ini di eval (kasus anti-penafsiran-langsung).
> **Status: RESOLVED** — charter §2.1 dan §8 telah diselaraskan ke rumusan ini pada review
> integrasi 2026-07-06; nota ini dipertahankan sebagai jejak keputusan.

### C3 — Cross-Reference registry: generalisasi pola yang sudah terbukti

Kontrak (generalisasi langsung dari `quran_book_references` — dipertahankan karena terbukti):
- **Ujung tautan = dua Anchor** (sumber → target), korpus apa pun, granularitas unit atau lebih
  kasar (rentang, heading, karya). Presisi target diekspresikan oleh granularitas Anchor — tidak
  lagi oleh jenis khusus ("surah_only" lama menjadi anchor ber-granularitas surah).
- **Setiap tautan adalah klaim ter-atribusi**, membawa: jenis; **metode** (resolver-deterministik /
  ekstraksi-LLM dengan model+versi-prompt+run / manusia dengan aktor); **confidence** 0–1;
  **review_status** memakai enum 5-nilai yang sudah ada verbatim; **jejak bukti** (kutipan sumber +
  bentuk ternormalisasinya + versi normalisasi); waktu & asal-usul pembuatan.
- **Kosakata jenis awal** (terkurasi, bisa bertambah lewat kurasi — bukan bebas): `cites`
  (menyebut/merujuk), `quotes` (mengutip verbatim), `explains` (menjelaskan target — relasi
  tafsir-atas-ayat dan syarah-atas-hadith), `parallel` (teks/riwayat paralel). Kind lama dari
  tabel Quran terpetakan: surah_ayah/surah → `cites` dengan presisi anchor berbeda; quote →
  `quotes`; "ambiguous" berhenti menjadi jenis — ambiguitas adalah kombinasi confidence rendah +
  `review_status=ambiguous`.
- **Arah disimpan sekali, backlink lewat indeks dua arah** (pola indeks per-buku/per-surah yang
  sudah ada diangkat jadi kewajiban registry).
- **Paparan publik**: hanya `approved` (pola existing dipertahankan); ambang confidence adalah
  urusan konsumen (API memaparkan nilainya, tidak menyembunyikan di bawah gerbang review).
- **Batas dengan `knowledge_*`**: registry ini untuk tautan **konten↔konten**. Tautan
  mention→entity (span-level, volume tinggi) tetap milik domain Wiki (Fase 6) — tetapi WAJIB
  memakai disiplin klaim yang sama (sudah: confidence + review_status identik), sehingga Fase 6
  tidak menciptakan bahasa kedua.
- **Migrasi**: `quran_book_references` di-bridge ke registry umum secara **paralel** (expand →
  salin → pembaca internal pindah → tabel lama menjadi view/dibekukan). **Blast radius publik:
  nol** — endpoint `GET /v1/quran/books/{book_id}/references` mempertahankan kontrak responsnya
  (perubahan hanya aditif).

### C4 — Kerangka Provenance & License platform

Kontrak:
- **Enum `license_status` Quran diadopsi verbatim** (unknown/needs_review/permitted/restricted/
  public_domain) untuk semua korpus. Lisensi melekat di lingkup tertinggi yang masuk akal
  (Work/Edition), **mewarisi ke unit**, dengan override per-unit. Gerbang ditegakkan **saat
  query** di semua jalur baca (pola Quran), bukan saat tulis.
- **Rollout gerbang di kitab** (perubahan perilaku — bentuk migrasinya): publish BARU mensyaratkan
  `permitted`; karya yang telanjur publik di-grandfather sambil diaudit (keputusan takedown =
  O-1B-1). Setiap karya WAJIB punya nilai lisensi (boleh `unknown`) — 100% coverage kolom, bukan
  100% kepastian hukum.
- **Provenance Class immutable** (source/editorial/machine = siapa yang MENGARANG teks itu);
  status review adalah dimensi terpisah yang bisa berubah. Terjemahan `generated` yang direview
  manusia tetap class machine + review approved — tidak pernah "naik kelas".
- **Identitas keluaran mesin wajib**: model + versi prompt + run untuk SEMUA keluaran LLM
  (terjemahan, ringkasan, ekstraksi, resolusi rujukan). Konsep **generation run** menggeneralisasi
  `knowledge_extraction_runs` yang sudah ada; metadata longgar di JSONB (praktik
  `SectionTranslation.metadata` hari ini) naik menjadi bidang kontrak yang diwajibkan untuk
  keluaran baru. (Selaras charter D6 — berlaku sejak sekarang untuk pekerjaan baru; backfill
  metadata lama tidak diwajibkan.)
- **Kelayakan retrieval default** (dikonsumsi Fase 7, ditetapkan di sini): source selalu layak;
  editorial & machine layak hanya bila review disetujui; korpus Quran tidak pernah layak untuk
  interpretasi (lihat C2). Permukaan lain (Search/Lookup) menampilkan semua yang `permitted`
  dengan label kelas provenance-nya.

### C5 — Normalisasi Arab kanonik ber-versi

Kontrak:
- **Satu sumber kebenaran di Go** (charter D9): perilaku `quranutil.NormalizeKey` saat ini
  dibekukan sebagai **v1** — profil "search-key" (strip tashkil/tatweel U+064B–U+0655/U+0670/
  U+0640, penyatuan varian alef/ya/waw-hamza, kolaps spasi). Perbaikan apa pun = **v2**, bukan
  penambalan diam-diam.
- **Korpus vektor emas** (kasus uji: hamzah, tatweel, tashkil bertumpuk, campuran Latin-Arab,
  spasi) menjadi definisi eksekusi profil; **pipeline Python wajib lulus 100% vektor yang sama di
  CI** (gerbang kesetaraan) — dua implementasi boleh hidup, satu semantik yang berkuasa.
- **Setiap teks turunan mencatat versi normalisasinya** (kolom versi pada unit/indeks/jejak bukti
  rujukan); kenaikan versi = reindex terencana memakai playbook backfill F1-H — tidak pernah
  reindex diam-diam.
- Profil bernama + versi adalah mekanisme ekstensi (mis. profil pencocokan qira'at kelak);
  hari ini cukup satu profil — jangan overdesign.

---

## 3. Gap & opportunity analysis

| # | Celah (embrio → kontrak) | Prioritas | Effort | Catatan |
|---|---|---|---|---|
| B1 | `SourceBlock` in-memory → registry Citable Unit persisten + lifecycle/lineage | **P0** | Besar | Bagian terbesar fase; memblokir sitasi presisi (F4) dan desain F5/6/7 |
| B2 | Anchor per-domain campur fisik/logis → grammar tunggal + kapabilitas resolusi + pemetaan legacy | **P0** | Sedang | Murah relatif; nilai besar: FE & sitasi tidak pernah 404 |
| B3 | `quran_book_references` satu-pasangan → registry Cross-Reference umum + bridge | P1 | Sedang | Pola sudah benar; kerjanya generalisasi + migrasi paralel |
| B4 | Lisensi hanya di Quran → adopsi platform + gerbang publish kitab | P1 | Sedang | Perubahan perilaku publik — butuh O-1B-1 |
| B5 | Dua normalisasi tanpa versi → v1 beku + vektor emas + gerbang kesetaraan CI | P1 | Kecil | Kerjaan kecil, mencegah kelas bug "hasil beda antar jalur" selamanya |
| B6 | Provenance mesin opsional (JSONB bebas) → identitas model+prompt+run wajib utk keluaran baru | P1 | Kecil | Menegakkan charter D6 sebelum volume enrichment naik |
| B7 | Tak ada audit integritas rujukan → job nol-sitasi-menggantung + metrik | P2 | Kecil | Menumpang F1-B/C |

**Risiko fase ini sendiri** (dan mitigasinya): (a) **over-engineering registry** — dilawan dengan
pilot-first pada segelintir buku sebelum menggeneralisasi; (b) **drift registry vs tabel korpus** —
dilawan dengan satu service tulis + invarian sinkron + audit berkala; (c) **skala registry saat
hadith masuk** — baris terindeks biasa; partisi ditunda sampai ada bukti kebutuhan (jangan bayar
di muka).

---

## 4. Roadmap — inisiatif fase 1B

Urutan: **B-1 → B-2 → B-3 → B-4/B-5 paralel → B-6**. Semua memakai playbook F1-H (expand-contract,
backfill resumable); tidak ada perubahan breaking pada API publik.

### B-1 — Registry Citable Unit + lifecycle + pilot kitab  *(P0, effort besar)*

**Rationale:** B1; substrat tunggal di bawah Lookup/Search/Ask (charter D3).
**Outcome:** unit paragraf ber-ID stabil hidup di database dengan provenance/lisensi/lineage, dan
terbukti deterministik pada korpus nyata.
**Isi:** registry bersama + service tulis tunggal; deriver kitab dari parser `readerutil` yang
sudah ada; **pilot backfill** pada set kecil buku nyata — termasuk buku ber-sitasi-Quran padat yang
sudah dipakai eval (buku 797 dkk.) — membuktikan: determinisme ID pada re-run, pemetaan footnote,
lineage saat suntingan editorial menyentuh halaman pilot; job audit nol-sitasi-menggantung berjalan
terjadwal. Industrialisasi seluruh katalog = Fase 4.
**AC:** re-run deriver pada sumber tak berubah menghasilkan ID identik ≥99,5% di pilot (target 100%
setelah perbaikan); satu suntingan yang memecah paragraf pilot menghasilkan unit baru + predecessor
superseded + anchor lama tetap terselesaikan; audit menggantung = 0; registry hanya menerima
tulisan lewat service bersama — jalur tulis lain adalah pelanggaran invarian yang terdeteksi audit.
**DS:** Salman membuka satu paragraf kitab lewat tautannya, editor menyunting bab itu, dan tautan
lama tetap membawa ke paragraf yang benar.

### B-2 — Grammar Anchor + kapabilitas resolusi + pemetaan legacy  *(P0, effort sedang)*

**Rationale:** B2. **Outcome:** satu cara menamai konten di semua korpus; tidak ada tautan mati.
**Isi:** spesifikasi grammar (komponen & semantik C1) didokumentasikan sebagai kontrak; kapabilitas
resolusi dibangun (kanonik + legacy `ayah_key`/`toc-{heading_id}`/page → unit aktif + redirect);
FE tidak perlu berubah (legacy tetap sah selamanya — pemetaan, bukan deprecation).
**AC:** 100% bentuk anchor yang dipakai FE hari ini terselesaikan lewat kapabilitas resolusi;
resolusi p95 ≤50ms; anchor unit dari B-1 terselesaikan termasuk lewat rantai lineage.
**DS:** tautan lama mana pun yang pernah dibagikan tetap membuka konten yang benar.

### B-3 — Registry Cross-Reference umum + bridge dari `quran_book_references`  *(P1, effort sedang)*

**Rationale:** B3. **Outcome:** satu tempat untuk semua tautan konten↔konten, dua arah, terkurasi.
**Isi:** registry sesuai C3; migrasi paralel isi tabel lama (100% baris approved terjaga);
endpoint publik existing tetap berkontrak sama (aditif belaka); resolver rujukan Quran menulis ke
registry baru; backlink query (per-target) tersedia untuk FE/fase lain.
**AC:** seluruh referensi approved lama ter-query lewat registry baru dengan hasil setara; satu
tautan uji lintas-korpus non-Quran (mis. kitab→kitab `quotes`) dapat dibuat, direview, dan tampil
hanya setelah approved.
**DS:** halaman ayat bisa menampilkan "dikutip oleh N kitab" — dan angka itu benar.

### B-4 — Adopsi lisensi platform + gerbang publish kitab  *(P1, effort sedang; perubahan perilaku)*

**Rationale:** B4; charter O3. **Outcome:** tidak ada penerbitan baru tanpa status lisensi; yang
lama diaudit tanpa mematikan produk.
**Isi:** enum & pewarisan sesuai C4; publish baru mensyaratkan `permitted`; karya publik existing
di-grandfather + antrean audit berprioritas (karya paling dibaca dulu); laporan cakupan lisensi.
**Blast radius:** publik — konten `restricted` yang teraudit bisa turun (kebijakan O-1B-1);
internal — alur publish editorial mendapat satu prasyarat baru.
**AC:** 100% karya memiliki nilai `license_status`; publish tanpa `permitted` tertolak dengan error
kontrak yang jelas; laporan audit berjalan.
**DS:** Salman bisa melihat daftar "karya yang belum jelas lisensinya" mengecil dari waktu ke waktu.

### B-5 — Normalisasi v1 beku + vektor emas + gerbang kesetaraan  *(P1, effort kecil)*

**Rationale:** B5/charter D9. **Outcome:** satu semantik normalisasi yang dipatuhi dua runtime.
**AC:** suite vektor emas lulus 100% di Go dan Python dalam CI; semua teks turunan baru mencatat
versi profil; perubahan perilaku normalisasi tanpa kenaikan versi gagal di CI.
**DS:** hasil pencarian dan pencocokan rujukan tidak lagi bisa "beda nasib" antar bagian sistem.

### B-6 — Identitas keluaran mesin (generation run) untuk pekerjaan baru  *(P1, effort kecil)*

**Rationale:** B6/charter D6. **Outcome:** setiap keluaran LLM baru dapat dijawab "model apa,
prompt versi berapa, run yang mana".
**AC:** jalur enrichment yang aktif (terjemahan/ringkasan/ekstraksi/resolusi) menolak menulis
keluaran tanpa identitas run+model+versi-prompt; unit/rujukan class machine memaparkan identitas
itu di API kurasi.
**DS:** saat sebuah terjemahan dipertanyakan, Salman bisa tahu persis mesin dan versi mana yang
menghasilkannya.

**Dependensi keluar:** Fase 3 (unit & anchor Quran resmi), Fase 4 (industrialisasi backfill +
migrasi sitasi RAG ke unit-ID), Fase 5/6 (lahir langsung di registry), Fase 7 (indeks & retrieval
di atas substrat ini). **Fase 5/6 tidak boleh mendesain model datanya sebelum B-1…B-3 terkunci.**

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| B-D1 | Satu registry unit fisik bersama; tabel korpus tetap sumber teks tampilan; satu service tulis | Substrat tunggal = indeks & lintas-korpus trivial; lifecycle sekali | Tabel unit per-korpus + view (divergen, lineage 4×); registry memindahkan teks tampilan (migrasi besar tanpa nilai) |
| B-D2 | Anchor logis; halaman fisik = metadata sekunder | Halaman = artefak cetakan; identitas harus survive edisi/suntingan | Menjadikan page_id identitas (patah saat edisi berbeda) |
| B-D3 | Ketahanan-suntingan via lineage/redirect (mint-baru + supersede), bukan ID berbasis-konten murni | Hash-konten berubah saat typo diperbaiki; lineage mempertahankan sejarah & resolusi | Content-addressed ID murni; ID posisi murni (bergeser saat sisip) |
| B-D4 | Enum `review_status` 5-nilai & skala confidence existing diadopsi verbatim di semua kontrak | Sudah konsisten di dua subsistem; nol biaya konversi | Enum baru "lebih rapi" (memecah data existing tanpa manfaat) |
| B-D5 | Kosakata kind Cross-Reference terkurasi (cites/quotes/explains/parallel) + presisi via granularitas anchor | Kind bebas = taksonomi liar; presisi bukan jenis | Kind bebas string; kind lama per-pasangan-korpus |
| B-D6 | Footnote = jenis unit sendiri tertaut ke induk | Footnote tafsir bernilai sitasi; harus beralamat | Footnote sebagai metadata non-addressable |
| B-D7 | Unit Quran ada (teks-primer + rendering terjemahan), eligibilitas interpretatif = aturan statis yang mengecualikan korpus Quran | Search/Lookup/sitasi butuh substrat sama; penegakan lebih kuat & teruji daripada "tidak punya unit" | Quran tanpa unit sama sekali (memaksa jalur search/sitasi paralel) — lihat nota konflik §2 C2 |
| B-D8 | Lisensi melekat di Work/Edition, mewarisi, override per-unit; gerbang saat query; publish baru wajib `permitted`, existing grandfathered | Pola Quran terbukti; grandfather menjaga produk hidup selama audit | Gerbang saat tulis saja (bocor lewat jalur baca lama); takedown massal segera |
| B-D9 | Normalisasi: Go sumber kebenaran, Python diikat vektor emas di CI | charter D9; dua runtime nyata hari ini | Port paksa pipeline Python ke Go sekarang (biaya besar, nilai kecil) |
| B-D10 | Pilot-first (segelintir buku) sebelum industrialisasi backfill | Membuktikan determinisme/lineage murah sebelum jutaan baris | Backfill seluruh katalog langsung di 1B (risiko besar, umpan balik lambat) |
| B-D11 | Provenance class immutable; review = dimensi terpisah | "Siapa mengarang" ≠ "sudah diperiksa"; retrieval butuh keduanya | Class yang bisa "naik kelas" setelah review (menghapus jejak asal) |

**Asumsi:** B-A1 — parser `readerutil` memadai sebagai deriver pilot (bila ada kelas konten yang
gagal terpecah rapi, perbaikan parser masuk Fase 4, pilot memilih buku yang representatif);
B-A2 — F1-H (playbook backfill) selesai sebelum B-1 backfill; B-A3 — keputusan O3/O-1B-1 operator
tersedia sebelum gerbang lisensi kitab diaktifkan (default aman berlaku bila diam); B-A4 — volume
unit kitab (ratusan ribu–jutaan baris) ditangani Postgres ter-tuning F1-G tanpa partisi.

---

## 6. Interfaces (seams)

**Backbone MENGEKSPOS (kontrak stabil — fase lain memakai verbatim, tidak membuat padanan):**
- **Resolusi Anchor**: anchor apa pun (kanonik/legacy) → unit/baris aktif + status + redirect.
- **Registry Citable Unit**: pendaftaran & pengambilan unit dengan identitas/lineage/provenance/
  lisensi; satu-satunya jalur tulis unit; kontrak deriver (determinisme, pemetaan jenis).
- **Registry Cross-Reference**: buat/kurasi/query dua-arah tautan konten↔konten ter-klaim.
- **Kerangka lisensi & provenance**: enum, pewarisan, gerbang query, identitas generation-run.
- **Normalisasi kanonik**: profil+versi + vektor emas + gerbang kesetaraan.
- **Aturan kelayakan retrieval default** (C4) — Fase 7 menegakkannya, tidak merumuskannya ulang.

**Backbone MENGONSUMSI:** F1-H (bentuk migrasi & backfill) dan F1-B/C (metrik, alert, supervisi
job) — prasyarat untuk IMPLEMENTASI 1B, bukan untuk penetapan kontraknya; F1-F (konvensi
modul-domain untuk penempatan service bersama — dipakai bila sudah tersedia, tidak memblokir);
keputusan operator O3/O-1B-1 untuk aktivasi gerbang lisensi; lapisan peran yang SUDAH ada di kode
hari ini (user/editor/admin) untuk kurasi rujukan & lisensi — pengerasan auth oleh Fase 2 berjalan
paralel dan BUKAN prasyarat 1B.

**Kontrak yang backbone TIDAK miliki (sengaja):** pemodelan Work/Edition (Fase 4/5 — backbone hanya
mewajibkan slot atribusinya); taksonomi & tautan entitas (Fase 6 — dengan disiplin klaim yang sama);
indeks search & embeddings (Fase 7).

---

## 7. Open decisions (operator-owned)

**O-1B-1 — Nasib karya yang sudah publik selama audit lisensi (turunan operasional O3).**
*Kenapa penting:* menyeimbangkan risiko hukum/etika vs produk yang tiba-tiba kehilangan konten.
*Opsi:* (a) **Tetap tayang selama audit; takedown segera hanya bila teraudit `restricted`** —
produk stabil, ada jendela risiko selama antrean audit; (b) turunkan semua `unknown` sekarang,
naikkan kembali setelah `permitted` — paling aman legal, produk kehilangan sebagian besar katalog
mendadak; (c) tetap tayang tapi karya `unknown` disembunyikan dari search/RAG (hanya bisa diakses
via tautan langsung) sampai teraudit.
*Rekomendasi:* (a), dengan antrean audit berprioritas trafik. *Default aman jika diam:* (a) —
dan (sesuai O3) karya `unknown` tidak pernah dipublish BARU.

---

## 8. Conformance

Backbone adalah tempat RAG Safety dan Domain Integrity berubah dari prinsip menjadi **properti
data yang bisa diuji**: korpus Quran dikecualikan dari kelayakan interpretatif secara statis (C2);
setiap unit memisahkan source/editorial/machine dan membawa atribusi + lisensi (C4); setiap
rujukan silang adalah klaim ter-review ber-bukti dan ber-confidence — makna tidak pernah menyelinap
lewat tautan tanpa atribusi (C3). Fase 7 tinggal menegakkan dan mengujinya, bukan menciptakannya.

## 9. North-star fit

Yang membuat Surau "wiki" dan bukan empat aplikasi baca paralel adalah lapisan ini: satu cara
menamai ilmu, satu cara mengutipnya, satu cara menautkannya, dan satu standar kejujuran tentang
asal-usulnya. Setiap fase sesudah ini — Quran, kitab, hadith, wiki, retrieval — tinggal mengisi
substrat yang sama, sehingga pada Fase 7 "menyatukan" bukan lagi proyek, melainkan konsekuensi.
