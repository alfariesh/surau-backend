# Fase 4 — Kitab/Turats: Reader + Editorial (fase konten terberat)

> **Terikat pada charter** (`roadmap/README.md`), **kontrak Fase 1B**
> (`roadmap/phase-1b-content-backbone.md` — Citable Unit/provenance/lisensi/normalisasi dipakai
> verbatim), dan **seam Reader Experience Fase 3** (`roadmap/phase-3-quran.md` §6.2 — dikonsumsi &
> diperluas, TIDAK dibangun ulang). Domain ini adalah TEMPLATE yang Fase 5 (Hadith) tiru: pola unit,
> editorial, identitas katalog, dan SEO-nya.
> Ditulis 2026-07-07 di atas eksplorasi Fase 0 + dua audit lanjutan (jalur enrichment/editorial;
> audit bug tertarget dengan verifikasi klaim per-klaim) + verifikasi langsung (slug/mention/notifikasi/cache).

---

## 1. Understanding — kitab hari ini: produk kaya dengan retakan serius di fondasi data

### 1.1 Yang solid (dan diverifikasi ulang fase ini)

- **Model baca lengkap**: katalog (kategori/pengarang/buku + terjemahan katalog), halaman, heading
  tree, section, TOC bersarang, playlist audio; kontrak multilingual berprinsip (exact-language +
  availability metadata).
- **Editorial production matang**: workflow `candidate→drafting→in_review→ready→published` per
  (buku, bahasa); draft/publish + ETag (412/428/`*`) + snapshot revisi (50 terakhir) + restore;
  collab Yjs dengan Go sebagai satu-satunya jalur tulis. **Dua klaim lama TERBANTAH oleh audit
  fase ini**: (a) publish multi-aset TERNYATA SUDAH atomik — satu transaksi dengan `FOR UPDATE` +
  completeness check (`editorial_production_postgres.go:335–385`); (b) TOC availability TERNYATA
  bukan N+1 — satu mega-query LATERAL (`reader_postgres.go:640–720`).
- **Struktur tak bisa dirusak editor**: editorial hanya menyunting KONTEN (halaman/heading/
  metadata) — tidak ada operasi create/delete/reorder heading. Pohon heading immutable
  pasca-import → sitasi per `heading_id` stabil selama importer tidak mengamuk (lihat D1).
- **Provenance mesin setengah jadi — lebih baik dari dugaan charter**: ringkasan heading tersimpan
  di tabel sendiri dengan metadata kaya (provider, model, `style_version "reader-summary-v1"`,
  `generated_at`, status generated/reviewed + reviewer; migrasi `20260525000002`); terjemahan
  generated membawa profile/style_version/model di metadata. Fondasi B-6 (1B) sudah ada preseden.
- **PublicCache aman** (diverifikasi langsung, `middleware/cache.go`): conditional-GET murni —
  handler selalu jalan, ETag = hash body segar, 304 hanya saat byte-identik. Laporan "cache bisa
  bleed lintas query/lang" = false positive; TIDAK masuk daftar defect.

> **Conflicts with charter (koreksi faktual — RESOLVED):** mandat Fase 4 di charter §4.3
> mencantumkan "publish multi-aset atomik" sebagai pekerjaan — audit membuktikan publish SUDAH
> atomik (satu transaksi + `FOR UPDATE`); scope diturunkan menjadi audit-trail re-publish (K-9).
> Klaim Fase 0 "partial publish possible" dan "N+1 TOC availability" terbantah bukti. Charter §4.3
> telah diselaraskan pada 2026-07-07. Sebaliknya, klaim charter "importer menimpa saat re-import"
> ternyata LEBIH BURUK dari tertulis: hard-delete + FK cascade yang menghapus editorial (D1).

### 1.2 Daftar defect terverifikasi (audit bug — terpisah dari pekerjaan fitur)

| # | Defect | Severity | Bukti | Blast radius |
|---|---|---|---|---|
| D1 | **Re-import Shamela HARD-DELETE halaman/heading yatim** (`DELETE ... WHERE NOT (id = ANY($2))`, `importer.go:680–682, 718–720`) → FK CASCADE **menghapus editorial** (`book_page_edits`/`book_heading_edits` ON DELETE CASCADE) + section_translations/audio; **meng-orphan** progress & saved_items; TANPA diff/audit pra-import; jalur ini **nol unit test** (D6) | **KRITIS** | importer.go + migrasi 20260522000002 | Sekali re-import rilis Shamela baru = kerja editorial hilang permanen + data pengguna menggantung; inilah risiko tunggal terbesar domain ini |
| D2 | **OFFSET publik tanpa batas** di list halaman (`reader.go:313–318`; `clampOffset` reader tidak meng-clamp — kontras personal yang cap 10k) → full scan O(N), vektor DoS tanpa auth | **TINGGI** | reader.go:631–667 | Ketersediaan API publik |
| D3 | **`saved_items` tanpa FK** ke pages/headings (migrasi `20260530000003` — kolom integer polos) → bersama D1: item menunjuk konten yang tak ada, diam-diam | **TINGGI** | migrasi saved_items | Korupsi data pengguna senyap |
| D4 | **Endpoint headings tanpa paginasi** — mengembalikan SEMUA heading (`reader.go:365–390`, `Total: len(headings)`) | SEDANG | reader.go | Buku besar = respons raksasa; DoS ringan |
| D5 | **Metakarakter ILIKE tak di-escape** (`"%"+query+"%"`, `reader_postgres.go:1537–1548`) — `%`/`_` user mengubah semantik pola + scan mahal | SEDANG | reader_postgres.go | Search buku/pengarang/heading |
| D6 | **Importer kitab nol unit test** (hanya `assets_test.go`; jalur delete/idempotensi tak teruji — kontras importer Quran yang punya test) | SEDANG | internal/importer/ | Jalur kehilangan-data tak terjaga |
| D7 | **Pohon heading tanpa deteksi siklus** (`toc.go:105–145`): parent hilang → diam-diam jadi root; siklus A↔B → rekursi tak berujung (risiko crash) | RENDAH–SEDANG | toc.go | Data import cacat → TOC salah senyap / crash |
| D8 | **Normalisasi Arab bercabang** — reader punya `normalizeReaderArabicSearchText` sendiri (menambah ء→ا, ة→ه yang tak ada di `quranutil.NormalizeKey`); search heading bahkan tanpa normalisasi query-time | RENDAH | reader_postgres.go:1554–1582 | Kata sama tak ketemu lintas domain — persis kasus 1B C5 |
| D9 | **`COALESCE(he.content, h.content) ILIKE` melewati indeks trigram** (indeks ada di kolom, ekspresi tidak) | RENDAH | reader_postgres.go:580 | Perf search heading pada buku ber-edit |
| — | *Diperiksa & BERSIH:* N+1 TOC (satu query); cache bleed (false positive, §1.1); total vs items list pages (konsisten) | — | — | — |

**Satu risiko-kebijakan (bukan bug):** konten machine berstatus `generated` (belum direview) tampil
publik begitu proyek produksi published — **by design & terdokumentasi** (README: "translation
status is informational only"; label reviewed tampil di reader). Ditangani sebagai kebijakan
(O-4-4) + gerbang eligibility 1B untuk retrieval, bukan sebagai defect.

### 1.3 Celah arsitektur & fitur (di luar defect)

1. **Citable Unit belum termaterialisasi** — `SourceBlock` masih in-memory
   (`readerutil.StructureSourceContent`); sitasi RAG berhenti di heading/page. Ini pekerjaan
   konsumen-backbone #1 dan pembuktian kontrak 1B di skala nyata.
2. **Work/Edition mati suri**: `type/printed/major_release/minor_release` tersimpan & terekspos
   tapi NOL kode yang menafsirkannya; tanpa deteksi karya-duplikat; muhaqqiq/tahqiq tak ada.
3. **Rujukan tafsir→ayat setengah hidup**: resolver hanya jalan saat import (`--resolve-references`),
   tanpa tooling re-resolve, tanpa endpoint kurasi antrean `pending/ambiguous` (yang tampil publik
   hanya `approved` — benar, tapi antreannya buntu).
4. **SEO kitab NOL** (diverifikasi): tanpa slug buku/pengarang/kategori, tanpa tabel editorial SEO,
   tanpa sitemap — kontras tajam dengan Quran yang polanya sudah jadi (Q-4).
5. **Audiobook dangkal**: audio per-heading (URL+durasi), `narrator` teks-bebas, TANPA lisensi,
   TANPA segmen milidetik (kontras Quran), TANPA resume posisi-dengar; playlist polos. (Draft audio
   editorial ADA di workflow produksi — `section_audio_edits` + endpoint audio-draft.)
6. **Mention entitas tak pernah sampai ke pembaca** (diverifikasi nol di reader): pipeline
   langextract menulis `knowledge_mentions` ber-offset terhadap konten halaman, tapi respons reader
   tak membawa satu span pun — hanya rujukan Quran yang tampil.
7. **Reader Experience kitab tertinggal dari Quran**: tanpa posisi-dengar, tanpa koleksi/folder,
   tanpa highlight/anotasi (saved-items = simpan-nanti saja).
8. **Notifikasi kitab greenfield total** (diverifikasi): event yang ada hanya khatam/reminder Quran
   + alert auth.
9. **Loop editorial tak menutup**: feedback votes hanya ditonton (tanpa antrean prioritas), promosi
   `generated→reviewed` tanpa jalur, re-publish menimpa tanpa snapshot perubahan, output skill
   enrichment (temp-books-enriched) tanpa identitas run/seed-book.

---

## 2. Vision — korpus ulama yang setiap paragrafnya bisa dipercaya, dikutip, dan dinavigasi

Kitab disebut solid ketika: (1) **tidak ada jalur yang bisa menghapus kerja editorial atau
meng-orphan data pengguna** — importer menjadi versioned & non-destruktif dengan diff yang direview;
(2) **setiap paragraf adalah Citable Unit** ber-provenance/atribusi/lisensi — sitasi RAG menunjuk
paragraf dan selamat dari suntingan; (3) **karya ≠ edisi**: katalog tahu "Ihya edisi tahqiq X" vs
"Ihya edisi Y"; (4) **tafsir menaut ke ayat dengan presisi ter-kurasi** dua arah; (5) **kitab
setara Quran sebagai produk**: slug+SEO+sitemap, audiobook dengan identitas & resume, span entitas
yang bisa diklik, anotasi & koleksi, notifikasi yang relevan; (6) **mesin tunduk pada manusia**:
konten machine berlabel, ber-identitas run, dan hanya yang direview masuk kelayakan retrieval.

**Bar kuantitatif fase ini** (menambah charter §2.3): re-import: 0 penghapusan destruktif tanpa
diff yang direview + drill re-import di CI (fixture berubah → editorial selamat); materialisasi:
100% buku published ber-unit, determinisme 100%, sitasi-menggantung mingguan = 0; kurasi rujukan:
100% rujukan `explicit surah:ayah` terhubung otomatis (confidence ≥0.8) — sisanya masuk antrean,
antrean pending tidak tumbuh >30 hari; SEO: 100% buku published ber-slug & masuk sitemap dengan
lastmod ≤5 menit; API publik: offset ter-cap (10k, selaras personal), headings ter-paginasi
(default limit 200), p95 search <400ms dengan escaping; notifikasi kitab: dedupe ≈0 duplikat,
hormati quiet-hours Q-6.

---

## 3. Gap & opportunity analysis (terurut leverage)

**Defect (P0 semua, lihat §1.2):** D1 (kritis — kerjakan pertama), D2/D3/D4 (publik & data
pengguna), D5/D6/D7/D9 (menyusul cepat), D8 (diselesaikan lewat adopsi 1B C5, bukan tambalan lokal).

**Fitur/arsitektur:**

| # | Peluang | Prioritas | Effort | Memblokir |
|---|---|---|---|---|
| K-G1 | Materialisasi Citable Unit seluruh katalog + migrasi sitasi RAG ke unit | **P0** | Besar | Fase 5 (template), 6 (span), 7 (retrieval presisi) |
| K-G2 | Importer versioned non-destruktif (dengan D1/D6) | **P0** | Sedang | Keamanan semua konten & editorial |
| K-G3 | Work/Edition + deteksi karya-duplikat + muhaqqiq | P1 | Sedang | Atribusi 1B C4; katalog Fase 5 meniru |
| K-G4 | Rujukan tafsir→ayat: resolver→unit, re-resolve, antrean kurasi | P1 | Sedang | Nilai inti Fase 7 |
| K-G5 | SEO kitab (port pola Quran Q-4) | P1 | Sedang | Akuisisi produk |
| K-G6 | Span entitas di reader (irisan Fase 4) | P1 | Sedang | Fase 6 (halaman entitas menunggu span hidup) |
| K-G7 | Audiobook: resume + identitas/lisensi qari; segmen = O-4-1 | P1/P2 | Kecil / Besar | — |
| K-G8 | Loop editorial menutup: antrean feedback, promosi reviewed, snapshot re-publish, identitas run enrichment | P1 | Kecil–sedang | Kualitas korpus jangka panjang |
| K-G9 | Anotasi/highlight + koleksi (perluasan seam Fase 3) | P2 | Sedang | — |
| K-G10 | Notifikasi kitab (di atas Q-6) | P2 | Kecil–sedang | — |

---

## 4. Roadmap — inisiatif Fase 4

Urutan: **K-0 → K-1 → (K-2 ∥ K-3 ∥ K-4) → K-5/K-6 → K-7/K-8/K-9.** K-1 menunggu 1B B-1…B-3 + F1-H;
K-5 (bagian resume) & K-8/K-9 menunggu Fase 3 Q-5/Q-6 mendarat; selebihnya independen.

### K-0 — Sprint pengerasan: tutup defect terverifikasi  *(P0, effort sedang)*

**Rationale:** §1.2 — satu defect kritis + delapan pendukung; test dulu, ubah perilaku kemudian.
**Isi:** (1) **Importer aman** (bersama K-G2): re-import menjadi **staged & versioned** — hitung
diff terhadap rilis sebelumnya, karantina baris yang akan hilang (soft-tombstone, bukan DELETE),
tolak eksekusi destruktif tanpa persetujuan eksplisit ber-laporan; suite test importer ditulis
SEBELUM perubahan (kasus: re-import identik = no-op; halaman hilang di sumber = tombstone +
editorial selamat; progress/saved-items tak menggantung). (2) Clamp offset publik (selaras cap 10k
personal) + paginasi headings (aditif: default limit 200, param limit/offset — FE lama tetap jalan).
(3) Escaping metakarakter ILIKE + `ESCAPE` di semua jalur search reader. (4) Guard siklus/parent
hilang di TOC build + alert (F1-B) + validasi saat import. (5) Indeks ekspresi untuk search heading
ber-edit (D9). (6) Kebijakan orphan saved_items/progress: TANPA FK CASCADE naif (menghapus data
pengguna diam-diam itu salah) — item yang target-nya di-tombstone menampilkan status "konten tidak
tersedia" dan job perbaikan menautkan ulang via lineage unit 1B bila penerus ada.
**Blast radius:** alur import ops (langkah persetujuan baru); API publik hanya aditif/clamp.
**AC:** fixture re-import yang menghapus halaman TIDAK bisa menghapus editorial/orphan data tanpa
diff yang disetujui; `offset=10^9` ter-clamp; fuzz `%_\` di search aman; suite importer hijau di CI;
TOC dengan siklus buatan tidak crash dan ter-alert.
**DS:** update rilis Shamela bisa dijalankan tanpa takut kerja editor atau bookmark pengguna lenyap
— dan kalau ada yang akan hilang, Salman melihat daftarnya DULU.

### K-1 — Industrialisasi Citable Unit + migrasi sitasi RAG  *(P0, effort besar)*

**Rationale:** K-G1; pembuktian kontrak 1B di skala penuh; charter D3. **Outcome:** setiap paragraf
buku published adalah unit ber-ID stabil dengan provenance/atribusi/lisensi; sitasi book-RAG
menunjuk unit.
**Isi:** backfill seluruh katalog published dengan runner F1-H (resumable, ter-metrik) memakai
deriver pilot 1B yang dikeraskan (pelajaran pilot: footnote, blok quran_quote, konten html);
pemetaan kelas provenance per unit (source untuk teks Shamela; machine untuk ringkasan/terjemahan
dengan identitas model+style_version yang SUDAH ada di metadata); lisensi mewarisi dari
Work/Edition (K-2, gerbang B-4); **hook re-anchoring mention** (offset mention `knowledge_mentions`
diterjemahkan dari koordinat halaman ke koordinat unit saat materialisasi — prasyarat K-6);
**migrasi sitasi RAG paralel**: dual-write sitasi (heading/page lama + unit-ID baru) di belakang
flag → verifikasi kesetaraan → tukar default → jalur lama dipertahankan sebagai fallback satu rilis.
**Kelayakan retrieval ditegakkan di sini**: unit machine yang belum direview (termasuk ringkasan
yang hari ini ikut ranking pohon RAG) DIKECUALIKAN dari kelayakan interpretatif per 1B C4 —
ranking boleh memakai ringkasan reviewed; yang generated hanya boleh jadi sinyal non-kutipan
dengan label, tidak pernah jadi sumber jawaban.
**AC:** 100% buku published termaterialisasi dengan determinisme 100%; jawaban book-RAG mengutip
anchor unit yang tetap resolve setelah halamannya disunting; tidak ada unit machine-unreviewed yang
lolos kelayakan interpretatif (test); audit menggantung mingguan = 0.
**DS:** jawaban RAG menaut ke paragraf persis — dan tautannya tetap benar minggu depan setelah
editor merapikan bab itu.

### K-2 — Identitas Work/Edition + kurasi karya-duplikat  *(P1, effort sedang)*

**Rationale:** K-G2/charter; slot atribusi 1B C4 butuh pemiliknya. **Isi:** dekode berbasis-bukti
field `type/printed/major/minor` dari data Shamela nyata (audit nilai aktual → dokumentasikan
artinya); registry Work (karya abstrak) + keanggotaan edisi (buku existing = edisi) + metadata
tahqiq/muhaqqiq/penerbit; heuristik deteksi duplikat (nama+pengarang ternormalisasi) → antrean
kurasi manusia (bukan auto-merge); lisensi melekat di edisi (B-4). API katalog aditif
(pengelompokan karya; endpoint lama tak berubah).
**AC:** dua buku Shamela yang merupakan karya sama dapat ditautkan ke satu Work lewat antrean
kurasi; setiap buku published punya edisi dengan atribusi minimal (karya, pengarang, status
lisensi); tidak ada merge otomatis tanpa review.
**DS:** halaman "Ihya Ulumiddin" menampilkan edisi-edisinya, bukan dua entri misterius yang sama.

### K-3 — Rujukan tafsir→ayat presisi + antrean kurasi  *(P1, effort sedang)*

**Rationale:** K-G4; di sinilah nilai "tafsir menjelaskan ayah X" menjadi data ter-kurasi.
**Isi:** resolver menunjuk **unit** (bukan halaman) sebagai sumber; tooling re-resolve batch
(idempoten, ber-versi normalisasi C5 — jalankan ulang saat versi naik atau alias surah bertambah);
endpoint kurasi antrean `pending/ambiguous` untuk editor (approve/reject/koreksi rentang — memakai
peran existing); metrik cakupan (berapa % kutipan eksplisit terhubung; umur antrean); rujukan
tampil dua arah via registry 1B (halaman ayat Quran menampilkan "ditafsirkan oleh").
**AC:** rujukan explicit surah:ayah terhubung otomatis ≥95% dengan confidence ≥0.8; antrean pending
bisa dikerjakan dari API dan tidak tumbuh >30 hari; re-resolve tidak menduplikasi (idempoten).
**DS:** dari Ayat Kursi, Salman melihat daftar kitab yang menafsirkannya — dan editor punya kotak
masuk rujukan ragu untuk diputuskan.

### K-4 — SEO kitab: port pola Quran  *(P1, effort sedang)*

**Rationale:** K-G5 (verifikasi: nol slug/editorial/sitemap kitab); pola sudah jadi di Quran (Q-4).
**Isi:** slug stabil buku/pengarang/kategori + registry redirect; editorial SEO buku (meta/deskripsi
kurasi) memakai workflow standar (draft/publish + ETag + revisi — pola yang sama, bukan varian);
integrasi sitemap/feed dengan infrastruktur Q-4 (lastmod dari updated_at efektif; hanya
published+permitted); data terstruktur (karya/pengarang) mengikuti K-2.
**AC:** 100% buku published ber-slug stabil dan masuk sitemap dengan lastmod akurat ≤5 menit;
perubahan slug menyisakan redirect permanen; editorial SEO kitab lewat draft/publish ber-ETag.
**DS:** halaman kitab mulai muncul layak di Google dengan URL yang bisa dibaca manusia — seperti
halaman surah.

### K-5 — Audiobook: resume + identitas qari (segmen = keputusan O-4-1)  *(P1 kecil; segmen P2 besar)*

**Rationale:** K-G7. **Isi (sekarang):** posisi-dengar per (user, buku/heading, audio) memakai pola
LWW Q-5 apa adanya + masuk `/me/sync`; identitas narator/qari terstruktur + `license_status` pada
aset audio (adopsi B-4 — hari ini TANPA lisensi); kontinuitas playlist (next/prev + posisi).
**Isi (bila O-4-1 memilih):** segmen timestamp per unit/kalimat via forced alignment — pilot pada
buku populer dulu.
**AC (sekarang):** resume dengar lintas-perangkat teruji race; 100% aset audio ber-lisensi &
identitas narator terstruktur.
**DS:** berhenti dengar di menit 12 di ponsel, lanjut di menit 12 di web.

### K-6 — Span entitas di teks kitab (irisan Fase 4 dari Wiki)  *(P1, effort sedang)*

**Rationale:** K-G6; ekstraksi & skema SUDAH ada (`knowledge_mentions` ber-offset + review_status),
yang absen hanya jalur data ke pembaca. **Batas tegas:** Fase 4 = mengalirkan span approved ke
respons reader (offset pada konten unit, target = anchor entitas) — halaman entitas, relasi,
disambiguasi, glossary = **Fase 6**.
**Isi:** re-anchoring offset mention → unit (hook K-1); field span aditif di respons section/read
(hanya mention `approved`; provenance machine+review ikut); klik = anchor entitas (resolusi 1B —
halamannya menyusul di Fase 6, FE boleh menampilkan pratinjau minimal dari label entitas yang sudah
ada di `knowledge_entity_labels`).
**AC:** respons baca buku ter-materialisasi memuat span mention approved dengan offset yang tepat
terhadap konten unit; span selamat dari suntingan via lineage; tidak ada mention pending/rejected
yang bocor.
**DS:** nama "Imam Syafi'i" di teks kitab bisa diklik — pertama kalinya isi wiki terasa hidup di
dalam bacaan.

### K-7 — Anotasi, highlight, koleksi (perluasan seam Reader Experience)  *(P2, effort sedang)*

**Rationale:** K-G9; saved-items hanya simpan-nanti. **Isi:** highlight/anotasi ber-anchor unit
(+rentang karakter) — privat dulu (O-4-3); koleksi/folder di atas saved-items; ikut `/me/sync`;
tipe baru didaftarkan di registry tipe saved-item milik seam Fase 3 (BUKAN tabel paralel).
**AC:** anotasi menunjuk unit + rentang dan tetap benar setelah suntingan (lineage; bila unit
terpecah → anotasi menunjuk penerus dengan penanda "perlu tinjau"); sinkron lintas perangkat.
**DS:** pengguna bisa menstabilo paragraf dan menemukannya lagi — di perangkat mana pun.

### K-8 — Notifikasi kitab (di atas keandalan Q-6)  *(P2, effort kecil–sedang)*

**Rationale:** K-G10 (greenfield terverifikasi). **Isi:** event set awal — buku baru published yang
cocok `interests` user; nudge lanjut-baca (dari progress, dengan cooldown); event workflow
editorial untuk editor (proyek masuk review/publish); milestone baca; semua lewat lapisan Q-6
(dedupe, quiet-hours, jejak delivery) + flag preferensi baru per jenis (opt-out).
**AC:** tiap jenis punya flag preferensi; dedupe & quiet-hours terbukti di test; kegagalan delivery
terlihat di metrik.
**DS:** "Kitab baru di kategori favoritmu sudah tersedia" — dan bisa dimatikan sekali sentuh.

### K-9 — Menutup loop editorial  *(P1, effort kecil–sedang)*

**Rationale:** K-G8. **Isi:** feedback votes → antrean prioritas di dashboard produksi (ambang
dislike memunculkan heading untuk ditinjau); jalur promosi `generated→reviewed` dengan atribusi
reviewer (endpoint kurasi, bukan flip manual DB); snapshot + catatan-perubahan saat re-publish
(publish tetap atomik — ini audit trail-nya); identitas generation-run untuk output skill
enrichment/temp-books-enriched (adopsi B-6: run + seed-book id di metadata sebelum import — tanpa
ini asal-usul aset enrichment tak bisa dibedakan dari import katalog).
**AC:** heading paling-dikeluhkan muncul otomatis di antrean; setiap promosi reviewed tercatat
reviewer-nya; re-publish meninggalkan diff yang bisa dilihat; 100% aset enrichment baru membawa
identitas run.
**DS:** keluhan pembaca atas terjemahan benar-benar menggerakkan antrean kerja editor — bukan
sekadar angka yang ditonton.

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| K-D1 | Re-import = staged diff + tombstone (soft) + persetujuan; TIDAK PERNAH hard-delete langsung | D1 kritis; editorial & data pengguna harus selamat by construction | FK CASCADE dibiarkan + backup (backup ≠ pencegahan); menolak semua re-import (menghambat update korpus) |
| K-D2 | Orphan saved_items/progress: tanpa FK CASCADE; status "konten tidak tersedia" + repair via lineage unit | Menghapus data pengguna diam-diam lebih buruk daripada menandai | FK ON DELETE CASCADE (kehilangan senyap); FK RESTRICT (memblokir importer selamanya) |
| K-D3 | Sitasi RAG bermigrasi ke unit-ID secara paralel (dual-write → verifikasi → swap → fallback satu rilis) | Kontrak publik hidup; charter D10 | Big-bang swap; menunda sampai Fase 7 (kehilangan pembuktian 1B) |
| K-D4 | Ringkasan LLM `generated` keluar dari kelayakan interpretatif; hanya reviewed yang boleh menyumbang jawaban | 1B C4; machine-in-retrieval-path adalah pelanggaran senyap hari ini | Status quo (ranking & konteks memakai teks mesin tak direview) |
| K-D5 | Work/Edition: kurasi manusia atas kandidat duplikat; TANPA auto-merge | Identitas karya = klaim; salah merge = korupsi atribusi | Auto-merge heuristik |
| K-D6 | Span entitas: hanya mention `approved`; re-anchor ke unit saat materialisasi; halaman entitas = Fase 6 | Disiplin klaim 1B; batas fase tegas | Menampilkan semua mention (bocor mesin belum direview); menunda semua ke Fase 6 (kehilangan nilai reader sekarang) |
| K-D7 | Anotasi/koleksi dibangun sebagai perluasan seam Fase 3 (registry tipe saved-item), privat dulu | Q3-D7; satu substrat personal | Tabel anotasi paralel; fitur sosial sekarang |
| K-D8 | Audiobook: resume + identitas/lisensi sekarang; forced alignment = keputusan operator terpisah | Nilai/biaya timpang; alignment = pipeline ML baru | Alignment untuk semua buku sekarang |
| K-D9 | Normalisasi reader dilebur ke 1B C5: perbedaan foldings (ء, ة) diputuskan di vektor emas v1 — kandidat kuat: adopsi foldings reader sebagai bagian v1 karena recall lebih baik | Satu semantik; D8 selesai lewat kontrak, bukan tambalan | Dua normalizer selamanya |
| K-D10 | Headings dipaginasi dengan default besar (200) — aditif, bukan breaking | D4; FE lama aman | Membiarkan unbounded; hard-break dengan limit kecil |

**Asumsi:** K-A1 — pilot 1B (B-1) selesai & pelajarannya terdokumentasi sebelum K-1 industrialisasi;
K-A2 — nilai `type/printed` Shamela bisa didekode dari data nyata (kalau tidak, Work/Edition tetap
jalan dengan kurasi manual penuh); K-A3 — mention offsets langextract cukup akurat untuk re-anchor
(kalau drift, re-ekstraksi per-unit masuk Fase 6); K-A4 — Q-5/Q-6 (Fase 3) mendarat sebelum
K-5/K-7/K-8 dieksekusi.

---

## 6. Interfaces (seams)

**Fase 4 MENGEKSPOS (dan menjadi TEMPLATE Fase 5):**
- **Pola deriver Citable Unit di skala** (K-1): pemetaan struktur→unit, penanganan footnote/kutipan,
  determinisme, dual-write sitasi — hadith meniru pola ini untuk matn/isnad.
- **Pola identitas katalog** (K-2): Work/Edition + antrean kurasi duplikat — hadith memakai bentuk
  yang sama untuk koleksi/edisi penomoran.
- **Pola editorial standar** (pemilik: fase ini — sudah terbukti + diperkeras K-9): draft/publish/
  ETag/revisi/atomic-publish + antrean feedback + promosi reviewed.
- **Kapabilitas rujukan ter-kurasi** (K-3): resolver→unit + re-resolve + antrean — Fase 7 memfilter
  `approved`; Fase 5 memakai pola yang sama untuk hadith→ayat.
- **Span entitas pada unit** (K-6): kontrak offset + provenance — Fase 6 membangun halaman entitas
  di atasnya.
- **Perluasan Reader Experience**: tipe saved-item baru (highlight/koleksi), posisi-dengar — masuk
  registry seam Fase 3.
- **SEO kitab** (K-4): slug+editorial+sitemap — satu pola dengan Quran.

**Fase 4 MENGONSUMSI:** 1B B-1…B-5 + C4/C5 (registry, resolusi, cross-ref, lisensi, normalisasi);
F1-H (backfill runner), F1-B/C (metrik/supervisi); Fase 3 Q-4 (infra sitemap), Q-5 (pola resume),
Q-6 (keandalan notifikasi), seam Reader Experience §6.2; keputusan operator O-1B-1/O-3-x/O-4-x.

---

## 7. Open decisions (operator-owned)

**O-4-1 — Kedalaman audiobook.**
*Kenapa penting:* segmen per-kalimat/paragraf (seperti Quran) butuh pipeline forced-alignment ML +
QA per buku — investasi nyata; tanpa itu audiobook tetap berfungsi (per-heading + resume).
*Opsi:* (a) **resume + identitas/lisensi saja** (K-5 sekarang); (b) + pilot alignment pada 5–10
buku terpopuler; (c) program alignment penuh. *Rekomendasi:* (a) sekarang, evaluasi (b) setelah
metrik dengar terlihat. *Default aman:* (a).

**O-4-2 — Prioritas korpus untuk materialisasi + SEO + tautan tafsir.**
*Kenapa penting:* urutan backfill/kurasi menentukan nilai yang terasa lebih dulu; ini pilihan
editorial, bukan teknis. *Opsi:* (a) **kategori tafsir & syarah dulu** (langsung menyalakan nilai
tafsir→ayat dan menyiapkan Fase 7); (b) berdasarkan trafik baca; (c) merata. *Rekomendasi:* (a)
dengan (b) sebagai tie-break. *Default aman:* (a).

**O-4-3 — Anotasi/highlight: privat vs bisa dibagikan.**
*Kenapa penting:* berbagi anotasi = permukaan moderasi konten baru (komentar pengguna atas teks
agama). *Opsi:* (a) **privat-saja** dulu; (b) bisa dibagikan via tautan; (c) publik/sosial.
*Rekomendasi:* (a) — moderasi UGC belum punya rumah sampai governance Fase 6. *Default aman:* (a).

**O-4-4 — Kebijakan tampilan konten mesin `generated` (belum direview) di reader publik.**
*Kenapa penting:* hari ini tampil berlabel (by design, README) — cepat menghadirkan terjemahan,
tapi kualitas tak terjamin manusia. *Opsi:* (a) **pertahankan tampil + label jelas** dan investasi
antrean review (K-9); (b) hanya reviewed yang publik (katalog terjemahan langsung menyusut drastis);
(c) tampil hanya untuk pengguna yang opt-in "tampilkan terjemahan mesin". *Rekomendasi:* (a) —
dengan catatan RAG SUDAH dikecualikan dari konten unreviewed (K-D4), jadi risiko tersisa hanya di
tampilan baca berlabel. *Default aman:* (a).

---

## 8. Conformance

Fase ini adalah tempat RAG Safety & Domain Integrity paling diuji — dan jawabannya struktural:
setiap paragraf kitab menjadi unit ber-provenance (source vs editorial vs machine + identitas
model/run) sehingga retrieval tak mungkin mengutip suara platform sebagai suara ulama (K-1, K-9);
konten mesin yang belum direview keluar dari kelayakan interpretatif (K-D4); tautan tafsir→ayat
adalah klaim ter-kurasi ber-confidence di registry 1B — ayat tetap anchor yang dirujuk, tak pernah
sumber makna (K-3); mention entitas hanya tampil setelah `approved` (K-6); dan Work/Edition
memastikan atribusi mengikat ke edisi/tahqiq yang benar (K-2).

## 9. North-star fit

Kitab adalah jantung wiki: korpus tempat interpretasi hidup, tempat RAG mengambil dalil, dan pola
yang hadith tiru. Fase ini mengubahnya dari "reader yang bagus dengan fondasi data retak" menjadi
korpus ulama yang setiap paragrafnya beralamat, ber-asal-usul, ber-lisensi, tertaut ke ayat yang
dijelaskannya, dan aman dari importer-nya sendiri — sambil mengejar ketertinggalan produknya
(SEO, audiobook, entitas yang bisa diklik, anotasi, notifikasi) memakai pola yang sudah dibayar
Quran. Ketika Fase 5 membuka dokumen ini, semua yang perlu ia lakukan adalah meniru.
