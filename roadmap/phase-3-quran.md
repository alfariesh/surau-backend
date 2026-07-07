# Fase 3 — Quran: Lapisan Teks Primer + Produk Reader (TANPA RAG)

> **Terikat pada charter** (`roadmap/README.md`) dan **kontrak Fase 1B**
> (`roadmap/phase-1b-content-backbone.md`): glosarium dipakai verbatim; Anchor ayat dideklarasikan
> per kontrak C1; unit Quran mengikuti C2 (ada untuk Lookup/Search/sitasi — dikecualikan statis
> dari retrieval interpretatif). Tafsir/interpretasi di luar scope (milik Fase 4 dan Fase 7).
> Ditulis 2026-07-07 di atas eksplorasi Fase 0 + dua verifikasi lanjutan (postur riwayat pada
> skema audio; audit lapisan reader-product).

---

## 1. Understanding — Quran hari ini: dua lapisan dengan kematangan berbeda

### 1.1 Lapisan teks primer — kuat, dengan tiga lubang yang bisa disebut

Bukti kekuatan (ringkas — detail di eksplorasi Fase 0):
- **Identitas kanonik sudah benar**: `(surah_id, ayah_number)` + `ayah_key "s:a"` ber-CHECK
  constraint; 114/6236 terjaga; navigasi juz (1–30) / hizb (1–60) / halaman dari metadata QPC;
  backlog correctness F01–F24/G1–G13 selesai (trigger coverage, importer audio transaksional,
  validasi rentang ayat, constraint SEO surah).
- **Dua script terpisah tugas**: QPC Hafs (tampilan) + Imlaei Simple (pencarian) dengan
  `search_text` ternormalisasi; pencarian trigram multi-bidang (Arab/terjemahan/transliterasi)
  dengan threshold 0.18 per-transaksi.
- **Sumber ter-atribusi dan ter-lisensi**: terjemahan/transliterasi per-sumber dengan
  `license_status` (hanya `permitted` tampil), coverage dijaga trigger, `is_default`
  deterministik; recitations ber-lisensi dengan aturan playable `public_url ?? audio_url` dan
  segmen milidetik per ayat (seek-to-ayah dalam track surah).
- **Importer disiplin**: QUL + Kemenag, checksum idempoten, dry-run, `quran_import_runs` sebagai
  jejak audit.

Tiga lubang yang terverifikasi:
1. **Editorial di bawah standar kitab (charter G5).** `quran_surah_editorial` dan
   `quran_ayah_editorial` single-state: tanpa draft/publish, tanpa ETag/If-Match, tanpa riwayat
   revisi — last-write-wins diam-diam. Kontras langsung dengan editorial kitab (412/428, snapshot
   50 revisi, restore). Konten SEO yang sedang menopang trafik justru yang paling telanjang.
2. **Riwayat/qira'at tidak dimodelkan sama sekali** — dikonfirmasi ulang: tabel `quran_recitations`
   hanya punya `reciter_name`/`style`/`metadata` JSONB; kata "hafs" hanya menempel di slug ID
   resource QUL (mis. `...murattal-hafs-953`), bukan data terstruktur. Teks pun Hafs-only by
   construction (kedua script adalah edisi Hafs). Klaim "teks primer ter-atribusi penuh" belum
   jujur di dimensi ini.
3. **Identitas edisi teks belum dideklarasikan sebagai data.** Script QPC Hafs & Imlaei adalah
   *edisi mushaf tertentu* (penomoran Kufi, tata halaman Madani), tetapi tidak ada deklarasi
   Work/Edition — halaman (`page_number`) tidak mengikat diri ke edisi mana pun secara eksplisit.

### 1.2 Lapisan produk reader — inti matang, pinggiran bolong (audit 2026-07-07)

Yang sudah **production-ready**:
- **Progress**: last-read per surah (`ayah_key`, persen, page/juz/hizb ter-resolve), konflik
  multi-perangkat ditangani **monotonic last-write-wins** via `observed_at` (event basi tidak bisa
  memundurkan posisi), delta aktivitas diserialisasi `FOR UPDATE` (bekas bug double-count G8 —
  kini dijaga live race-test).
- **Khatam**: siklus (satu aktif per user, unique partial index), tanda selesai per-juz idempoten,
  finalisasi manual (sengaja — anti salah-tandai), riwayat siklus, notifikasi milestone 10/20/30 +
  selesai.
- **Saved items**: 4 tipe (`quran_ayah`, `quran_range`, `book_page`, `book_heading`) + label/note/
  tags; upsert-by-target; filter surah/tag.
- **Sync offline**: `GET /me/sync?since=` (snapshot delta ber-cursor `server_time`, overlap window,
  at-least-once) + `POST /me/progress/batch` (replay antrean offline 100+100, error per-entri tak
  membatalkan batch).
- **Preferensi kaya**: bahasa UI/konten, `arabic_level`, `reader_mode`, sumber terjemahan &
  recitation pilihan, minat, 3 flag notifikasi.

Yang **bolong** (terverifikasi absen di server):
- **Posisi audio tidak tersimpan** — resume lintas-perangkat untuk mendengarkan tidak mungkin;
  hanya posisi baca teks yang sinkron.
- **Reminder fire-and-forget** — kirim ke OneSignal tanpa jejak delivery/failure; loop reminder
  punya cooldown 20 jam + kandidat per-timezone, tapi tanpa quiet-hours dan tanpa metrik.
- **Tanpa reading-plan** — `daily_goal_minutes` tersimpan tapi tidak dipakai server; khatam tanpa
  pacing (target tanggal → kuota harian) padahal semua bahannya (juz marks, reminder loop) ada.
- **Patologi cap 10k saved-items** — melewati 10.000, `/me/sync` menyerah dan menyuruh klien
  full-resync; tanpa kuota/arsip.
- **Tanpa PostHog di backend** (dikonfirmasi nol referensi) — analitik produk sepenuhnya frontend;
  event yang hanya diketahui server (kirim/gagalnya reminder, khatam terkonfirmasi server, konflik
  sync) tidak terlihat di analitik mana pun.
- **Tanpa sitemap/feed** (nol di kode & docs) — halaman SEO `/surah/{slug}` (dan `/surah/{slug}/{ayah}`
  yang sedang dibangun frontend) tidak punya sumber sitemap dari data editorial.

### 1.3 Temuan struktural: "Reader Experience" sudah lintas-korpus secara de-facto

Progress, saved-items, sync, dan activity **sudah melayani Quran DAN kitab dari tabel + usecase
yang sama** (`usecase/personal`; tipe item campur `quran_ayah`/`book_page`; snapshot sync memuat
keduanya; bucket aktivitas mencatat `quran_ayahs_read` dan `kitab_pages_read`). Artinya "shared
home" yang ditanyakan misi ini **bukan sesuatu yang perlu diciptakan — ia sudah ada** dan tinggal
diangkat menjadi seam resmi.

> **Conflicts with charter (under-coverage — bukan kontradiksi):** charter §4.1 menamai enam
> lapisan penghubung tetapi TIDAK menamai lapisan **Reader Experience** (progress/khatam/saved/
> sync/notifikasi/eksposur-analitik) yang nyatanya lintas-korpus dan akan dipakai hadith (Fase 5).
> **Resolusi yang saya tetapkan:** Fase 3 menjadi *owner-of-record* kontrak Reader Experience
> (karena Quran adalah produk pendorongnya hari ini) dan mendeklarasikannya sebagai seam di §6;
> Fase 4 mengonsumsinya (sudah terjadi de-facto), Fase 5 WAJIB mengadopsinya (menambah tipe item/
> progress hadith, bukan membangun paralel). Fase 9 dipersilakan menimbang apakah seam ini layak
> dipromosikan jadi dokumen fase tersendiri; tidak ada perubahan bar charter.

---

## 2. Vision — "impeccable, citable, dan enak dipakai setiap hari"

Lapisan Quran disebut solid ketika:

1. **Teks primer tak tergoyahkan**: teks ayat hanya bisa berubah lewat import ber-audit (tidak
   pernah lewat editorial); identitas edisi (Work/Edition per script) terdeklarasi; setiap sumber
   (terjemahan/transliterasi/audio) ter-atribusi penerjemah/qari + **riwayah** + lisensi.
2. **Anchor ayat = kontrak backbone yang hidup** (deklarasi resmi di §6): `ayah_key` kanonik,
   rentang se-surah, alias surah/juz/hizb/halaman — semuanya resolvable via kapabilitas resolusi
   1B; unit Quran (ayah + rendering terjemahan + footnote) tersedia untuk Lookup/Search/sitasi dan
   **tidak pernah layak retrieval interpretatif** (1B C2).
3. **Editorial setara kitab** (G5 tertutup): draft/publish + ETag + revisi + restore untuk
   editorial surah & ayah; import menulis draft; publish adalah keputusan.
4. **Produk reader lengkap di tempat yang penting**: posisi baca DAN dengar sinkron
   lintas-perangkat; khatam dengan rencana (target tanggal → pacing → reminder); reminder yang
   bisa dibuktikan terkirim; saved-items dengan kebijakan kuota yang tidak meledak di 10k.
5. **Terlihat oleh mesin pencari dan analitik**: sitemap/feed dari data editorial dengan lastmod
   akurat; inventaris event analitik backend-otoritatif terdefinisi (PostHog tetap di frontend).

**Bar kuantitatif fase ini** (menambah charter §2.3 baris Quran):
teks: 0 mutasi teks ayat di luar import ber-audit; kedua script lengkap 6236/6236. Navigasi:
juz/hizb/halaman menutup 6236 ayat tepat sekali (uji CI). Editorial: 100% tulisan editorial lewat
jalur draft/publish ber-ETag; restore revisi berfungsi. Anchor: 100% bentuk anchor legacy
resolvable; resolusi p95 ≤50ms (1B). Audio: 100% recitation visible playable; atribusi riwayah
100% recitation visible. SEO: 100% halaman editorial published ada di sitemap; lastmod akurat ≤5
menit; slug lama redirect permanen. Reader-experience: race-test progress tetap hijau di CI;
duplikasi reminder ≈0 (kunci dedupe); kegagalan delivery terlihat ≤5 menit di metrik (F1-B).

---

## 3. Gap & opportunity analysis (terurut leverage)

| # | Celah / peluang | Prioritas | Effort | Catatan |
|---|---|---|---|---|
| Q-G1 | Editorial Quran single-state (G5) — risiko saling-timpa konten SEO live | **P0** | Sedang | Mandat charter; pola sudah ada di kitab, tinggal diadopsi |
| Q-G2 | Adopsi backbone: unit + Anchor ayat + bridge rujukan + eligibility | **P0** | Sedang | Prasyarat nilai Fase 4 (tafsir→ayat presisi) & Fase 6/7 |
| Q-G3 | Riwayah/edisi tidak terstruktur (audio & teks) | P1 | Kecil–sedang | Atribusi = sekarang; multi-riwayah TEKS = keputusan O-3-1 (rekomendasi: tunda) |
| Q-G4 | Sitemap/feed SEO tidak ada; slug stability belum ada mekanisme redirect | P1 | Kecil–sedang | Trafik SEO adalah strategi produk saat ini; halaman per-ayah FE sedang dibangun |
| Q-G5 | Posisi audio tidak tersimpan (tanpa resume lintas-perangkat) | P1 | Kecil | Bahan sudah ada (recitation_id preferensi, segmen ms) |
| Q-G6 | Reminder tanpa jejak delivery/quiet-hours; loop tanpa supervisi F1-C | P1 | Kecil–sedang | Fire-and-forget = kegagalan senyap; menumpang metrik F1-B |
| Q-G7 | Khatam tanpa reading-plan (pacing) padahal semua bahan ada | P1 | Sedang | Keputusan bentuk produk = O-3-4 (rekomendasi: versi ringan) |
| Q-G8 | Saved-items: patologi cap 10k, tanpa kuota/arsip | P2 | Kecil | Perbaiki sebelum jadi insiden nyata |
| Q-G9 | Analitik: event server-otoritatif tak terlihat (PostHog FE-only) | P2 | Kecil | Kontrak eksposur dulu, bukan build PostHog backend |
| Q-G10 | Search-as-browse: belum ada lompat-referensi ("2:255"), phrase match, highlight | P2 | Kecil | Trigram dipertahankan; semantik = Fase 7 |
| Q-G11 | Kedalaman teks tambahan: word-by-word, tajwid berwarna | P2/tunda | Besar | Produk + lisensi = keputusan O-3-2; arsitektur unit siap menampungnya |

---

## 4. Roadmap — inisiatif Fase 3

Urutan: **Q-1 dan Q-2 dulu (paralel; Q-2 menunggu 1B B-1…B-3 terbangun), lalu Q-3/Q-4/Q-5/Q-6
paralel, Q-7 setelah Q-6, Q-8/Q-9/Q-10 menyelip kapan saja.** Semua perubahan API publik bersifat
aditif; perubahan perilaku ditandai per inisiatif.

### Q-1 — Editorial Quran naik ke standar kitab (menutup G5)  *(P0, effort sedang)*

**Rationale:** Q-G1; charter D8. **Outcome:** editorial surah & ayah punya draft/publish + ETag +
riwayat revisi + restore, dengan API admin ter-route; konten publik tetap hanya yang published +
`permitted`.
**Isi:** adopsi pola editorial kitab apa adanya (412/428/`If-Match: *`, snapshot revisi, restore,
origin rest/import); baris editorial existing di-grandfather sebagai published (bentuk migrasi:
expand — status workflow ditambahkan, tidak ada perubahan isi); importer editorial
(`import-quran-surah-editorial`, `import-quran-ayah-editorial`) menulis **draft** secara default
dengan flag publish eksplisit untuk konten self-authored tepercaya (preseden kitab: skrip
enrichment memakai `If-Match: *`).
**Blast radius:** alur import CLI + FE admin editorial (fitur baru); API baca publik tidak berubah.
**AC:** dua penyuntingan bersamaan atas editorial surah yang sama tidak bisa saling menimpa tanpa
412; setiap perubahan efektif meninggalkan revisi yang bisa di-restore; tidak ada jalur tulis
editorial yang melewati workflow ini.
**DS:** Salman melihat riwayat perubahan halaman editorial surah dan bisa mengembalikan versi
sebelumnya — sama seperti yang sudah dia lihat di kitab.

### Q-2 — Deklarasi Anchor ayat + unit Quran + bridge rujukan (adopsi 1B)  *(P0, effort sedang)*

**Rationale:** Q-G2; mandat misi + charter §4.3. **Outcome:** Quran resmi menjadi korpus pertama
yang penuh di atas backbone.
**Isi:** deklarasi Anchor (spesifikasi §6 di bawah — `ayah_key` kanonik di-grandfather verbatim);
minting unit Quran di registry 1B: **ayah** (jenis teks-primer), **rendering terjemahan** per
(sumber, ayah) ter-atribusi penerjemah+lisensi, **footnote terjemahan** sebagai unit tertaut
(bahan tafsir-pointer masa depan), transliterasi sebagai rendering; pemetaan anchor legacy
(`ayah_key`, surah, juz/hizb, halaman) ke kapabilitas resolusi; bridge `quran_book_references` →
registry Cross-Reference (bersama 1B B-3; endpoint publik lama tidak berubah); **aturan
eligibility ditulis sebagai test**: query retrieval-interpretasi apa pun tidak pernah mengembalikan
unit korpus Quran.
**AC:** 100% bentuk anchor yang dipakai FE hari ini resolvable; unit Quran deterministik 100%
(re-mint = ID sama); test eligibility anti-penafsiran lulus dan dirujuk Fase 7; rujukan approved
lama ter-query setara lewat registry baru.
**DS:** tautan ayat mana pun (termasuk yang lama) selalu membuka ayat yang benar, dan "dikutip
oleh N kitab" di halaman ayat berasal dari satu registry yang sama dengan korpus lain.

### Q-3 — Identitas edisi & atribusi riwayah  *(P1, effort kecil–sedang)*

**Rationale:** Q-G3. **Outcome:** klaim atribusi menjadi jujur dan terstruktur: setiap recitation
menyatakan riwayah-nya; setiap script teks menyatakan edisinya.
**Isi:** deklarasi Work/Edition untuk teks (QPC Hafs — penomoran Kufi, tata halaman Madani; Imlaei
Simple sebagai script pencarian edisi yang sama) sebagai metadata edisi yang diekspos API
(bahan panel atribusi/provenance untuk pembaca); field riwayah/qira'ah terstruktur pada recitations
(mengangkat fakta dari slug QUL + `config/quran_recitation_metadata.json` menjadi data; backfill
manual kecil — belasan recitation); normalisasi identitas qari (satu qari = satu identitas walau
banyak resource). Multi-riwayah **TEKS** tidak dikerjakan di fase ini (keputusan O-3-1) — tetapi
skema penomoran kanonik dideklarasikan milik edisi, sehingga edisi/riwayah lain kelak menjadi
edisi paralel dengan pemetaan anchor, bukan perombakan identitas.
**AC:** 100% recitation visible memiliki riwayah terstruktur; API mengekspos identitas edisi teks;
tidak ada lagi informasi riwayah yang hanya hidup di slug.
**DS:** di aplikasi, setiap qari tampil dengan riwayat bacaannya (mis. "Hafs 'an 'Asim") — bukan
sekadar nama.

### Q-4 — Permukaan SEO: sitemap/feed + kontrak slug  *(P1, effort kecil–sedang)*

**Rationale:** Q-G4; SEO adalah strategi akuisisi produk saat ini (editorial surah selesai,
halaman per-ayah menyusul di FE). **Outcome:** mesin pencari melihat halaman editorial tepat waktu.
**Isi:** data sitemap/feed dari editorial + konten (surah & per-ayah; lastmod dari `updated_at`
efektif; hreflang id/en sesuai ketersediaan editorial per bahasa; hanya yang published+`permitted`);
kontrak slug diformalkan (stabilitas sudah dijanjikan `docs/quran-api.md` — tambahkan: perubahan
slug menyisakan redirect permanen, tidak pernah menghapus slug lama); laporan cakupan editorial
(berapa surah/ayah published per bahasa) untuk mengarahkan produksi konten.
**AC:** sitemap memuat 100% halaman editorial published dengan lastmod akurat ≤5 menit; slug yang
diubah tetap resolvable lewat slug lamanya; laporan cakupan tersedia untuk operator.
**DS:** Salman bisa membuka satu URL sitemap dan melihat semua halaman surah/ayah yang siap
diindeks Google — dan angka cakupan editorial yang masih kosong per bahasa.

### Q-5 — Kontinuitas lintas-perangkat: posisi audio + polish sync  *(P1, effort kecil)*

**Rationale:** Q-G5, Q-G8. **Outcome:** pengalaman "lanjutkan dari tempat terakhir" berlaku untuk
mendengarkan, bukan hanya membaca.
**Isi:** posisi dengar per user (recitation + track + offset ms + `observed_at` monotonic — pola
LWW progress dipakai ulang), masuk snapshot `/me/sync` dan batch replay; kebijakan saved-items:
kuota lunak per-user dengan peringatan sebelum cap teknis 10k + jalur arsip (soft-hide) alih-alih
full-resync mendadak.
**Blast radius:** aditif (field/endpoint baru di sync & progress); klien lama tetap berfungsi.
**AC:** posisi dengar tersinkron antar dua perangkat pada recitation yang sama dengan semantik LWW
yang sama teruji race-nya; pengguna dengan >10k item tetap ter-sync tanpa full-resync paksa.
**DS:** mulai dengar murottal di ponsel, lanjut di web dari detik yang sama.

### Q-6 — Keandalan notifikasi & reminder  *(P1, effort kecil–sedang)*

**Rationale:** Q-G6. **Outcome:** reminder bisa dibuktikan terkirim, tidak dobel, dan sopan waktu.
**Isi:** jejak delivery (respons OneSignal dicatat: accepted/failed + alasan → metrik F1-B + alert
gagal-massal); kunci dedupe idempoten per (user, jenis, hari) — cooldown 20 jam yang ada
dipertahankan sebagai lapisan kedua; quiet-hours per timezone user (default 21:00–07:00 waktu
lokal untuk reminder non-kritis — angka rekomendasi saya, bisa diubah operator); loop reminder
masuk pola supervisi F1-C (panic-recovery, backoff, metrik last-success).
**AC:** kegagalan pengiriman terlihat di metrik ≤5 menit; tidak ada duplikat reminder pada hari
yang sama per user bahkan lintas restart; tidak ada reminder di luar jendela waktu lokal user.
**DS:** Salman bisa melihat "kemarin: X reminder terkirim, Y gagal" — dan tidak ada lagi keluhan
notifikasi dobel/tengah-malam.

### Q-7 — Reading plan ringan di atas khatam  *(P1, effort sedang; bentuk = O-3-4)*

**Rationale:** Q-G7; semua bahan ada (juz marks, reminder loop, preferensi). **Outcome:** khatam
punya rencana: target tanggal → pacing → status on/off-track → reminder yang relevan.
**Isi (rekomendasi "versi ringan"):** target tanggal pada siklus khatam; server menghitung kuota
harian (juz/halaman) dan status on-track/off-track dari marks yang ada; reminder harian memakai
status itu (integrasi Q-6); tanpa mesin adaptif/AI — deterministik dan bisa dijelaskan. Sinkron ke
`/me/sync`.
**AC:** siklus dengan target tanggal menghasilkan kuota harian yang benar secara aritmetika dan
status on/off-track yang berubah saat marks berubah; reminder memuat konteks pacing.
**DS:** "Kamu tinggal 12 juz, 9 hari lagi — 1⅓ juz per hari" muncul dari server yang sama yang
mencatat khatammu.

### Q-8 — Kontrak eksposur analitik (PostHog tetap di frontend)  *(P2, effort kecil)*

**Rationale:** Q-G9; mandat misi eksplisit: bukan build PostHog backend. **Outcome:** daftar
event yang hanya diketahui server terdefinisi dan tersedia untuk dikonsumsi.
**Isi:** inventaris event backend-otoritatif (khatam selesai terkonfirmasi-server; milestone juz;
reminder terkirim/gagal; konflik sync/batch-replay; editorial published; import selesai) +
bagaimana FE/operator mengonsumsinya: (a) field respons yang bisa diteruskan FE ke PostHog,
(b) endpoint ringkasan angka untuk operator, (c) emitter server-side opsional di belakang flag —
**kontraknya ditulis sekarang, implementasi emitter ditunda** (masuk backlog Fase 8; charter §1.4
"analitik admin → backlog" tetap dihormati).
**AC:** dokumen inventaris event ada dan minimal jalur (a) terpakai untuk khatam+reminder; tidak
ada dependensi PostHog di backend.
**DS:** dashboard PostHog Salman bisa menampilkan angka khatam & reminder yang sebelumnya tidak
terlihat oleh frontend.

### Q-9 — Search-as-browse polish  *(P2, effort kecil)*

**Rationale:** Q-G10. **Isi:** parsing kueri referensi ("2:255", "al-baqarah 255" → lompat
langsung), phrase/exact match di atas trigram yang ada, data highlight offset di hasil; TANPA
embeddings/semantik (Fase 7).
**AC:** kueri berbentuk referensi mengembalikan lompatan-langsung sebagai hasil teratas
deterministik; highlight offset tersedia di respons.
**DS:** mengetik "2:255" langsung membawa ke Ayat Kursi.

*(Q-G11 — word-by-word/tajwid — sengaja TIDAK menjadi inisiatif: menunggu keputusan O-3-2;
arsitektur unit 1B sudah siap menampungnya sebagai jenis unit "kata" bila di-opt-in.)*

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| Q3-D1 | `ayah_key "s:a"` di-grandfather VERBATIM sebagai locator kanonik Anchor ayat; penomoran kanonik = milik edisi teks yang dipakai (Kufi/QPC Hafs) | Sudah kanonik, URL-safe, dipakai FE & rujukan; mengubahnya = migrasi tanpa nilai | ID ayat global 1–6236 (kehilangan keterbacaan; dua sistem paralel) |
| Q3-D2 | Rentang anchor ayat = se-surah (from..to, to ≥ from) — mengikuti CHECK constraint existing; kebutuhan lintas-surah = daftar rentang | Cocok dengan data & praktik sitasi; constraint sudah menegakkannya | Rentang bebas lintas-surah (kompleksitas resolusi tanpa kasus nyata) |
| Q3-D3 | Skema penomoran mushaf alternatif (bila kelak perlu) = pemetaan alias per-edisi di atas anchor kanonik, bukan identitas kedua | Identitas tunggal + alias murah; identitas ganda = kekacauan rujukan | Multi-numbering sebagai identitas paralel |
| Q3-D4 | Unit Quran: ayah (teks-primer), rendering terjemahan per (sumber, ayah), footnote, transliterasi (rendering); SEMUA dikecualikan dari retrieval interpretatif | 1B C2/B-D7; terjemahan adalah rendering interpretif — disitasi sebagai rendering, bukan sumber makna | Hanya ayah yang jadi unit (footnote/terjemahan tak teralamatkan — menyulitkan sitasi & tafsir-pointer) |
| Q3-D5 | Editorial Quran mengadopsi pola kitab APA ADANYA (bukan varian baru); import menulis draft + flag publish eksplisit | charter D8; dua standar = bug arsitektur; preseden skrip kitab (`If-Match: *`) | Workflow ringan khusus Quran (memperbanyak pola lagi) |
| Q3-D6 | Multi-riwayah TEKS ditunda (O-3-1 default); yang wajib sekarang: atribusi riwayah terstruktur pada audio + deklarasi edisi teks | Nilai atribusi tinggi & murah; teks multi-riwayah = proyek besar tanpa permintaan produk terverifikasi | Import teks Warsh dkk. sekarang (biaya besar, kompleksitas anchor lintas-edisi) |
| Q3-D7 | Reader Experience = seam lintas-korpus yang dimiliki Fase 3 (owner-of-record); Fase 5 wajib adopsi, dilarang membangun paralel | Temuan §1.3: sudah lintas-korpus de-facto di `usecase/personal` | Fase baru tersendiri (menunda tanpa manfaat; operator bisa menimbang ulang di Fase 9) |
| Q3-D8 | Posisi audio memakai pola LWW-monotonic yang sama dengan progress baca | Pola teruji race; konsistensi semantik sync | Semantik khusus audio (dua aturan konflik utk satu produk) |
| Q3-D9 | Analitik: PostHog tetap frontend; backend hanya kontrak eksposur + emitter opsional tertunda | Mandat misi; menghindari dependensi vendor di jalur kritis | Klien PostHog server-side sekarang |
| Q3-D10 | Reading plan versi ringan deterministik (target tanggal → kuota → on/off-track), tanpa mesin adaptif | Semua bahan ada; bisa dijelaskan ke pengguna; adaptif = kompleksitas tanpa bukti kebutuhan | Rekomendasi adaptif/AI pacing |

**Asumsi:** Q3-A1 — 1B B-1…B-3 terbangun sebelum Q-2 dieksekusi (desain tidak menunggu); Q3-A2 —
FE bersedia mengadopsi panel atribusi/edisi & halaman per-ayah sesuai kontraknya (kerja FE di luar
scope repo ini); Q3-A3 — daftar riwayah recitation existing bisa dilengkapi dari metadata QUL +
verifikasi manual (belasan entri); Q3-A4 — keputusan O-3-x memakai default aman bila operator diam.

---

## 6. Interfaces (seams)

### 6.1 DEKLARASI RESMI — Anchor ayat (kontrak 1B C1, dikonsumsi Fase 4–7)

- **Korpus:** `quran`; lingkup Work implisit (satu karya); **edisi teks** dideklarasikan sebagai
  metadata atribusi (Q-3), BUKAN bagian identitas anchor — penomoran kanonik mengikuti edisi yang
  diimpor hari ini (Kufi/QPC Hafs).
- **Locator kanonik:** `ayah_key` bentuk `surah:ayah` (contoh yang sudah hidup di sistem:
  `"67:1"`) — di-grandfather verbatim; surah 1–114, ayah ≥1 sesuai jumlah per surah.
- **Rentang:** from..to dalam satu surah (to ≥ from); kebutuhan lintas-surah dinyatakan sebagai
  daftar rentang.
- **Alias resolvable (bukan identitas):** surah utuh, juz (1–30), hizb (1–60), halaman mushaf
  (per edisi), slug surah (via registry slug Q-4).
- **Granularitas lebih halus (reserved):** kata (menunggu O-3-2) — akan menjadi jenis unit baru di
  bawah ayah, bukan perubahan skema anchor.
- **Ketahanan:** teks ayat immutable → anchor ayat tak pernah pindah; suntingan editorial/terjemahan
  memakai lineage unit 1B; alias slug berubah = redirect permanen (Q-4).
- **Provenance anchor:** rujukan APA PUN ke anchor ini adalah klaim ter-atribusi via registry
  Cross-Reference 1B (metode + confidence + review) — anchor sendiri netral, tautannya tidak.

### 6.2 Seam lain yang Fase 3 EKSPOS

- **Unit Quran + aturan eligibility** (Q-2): ayah/rendering/footnote di registry 1B; test
  anti-penafsiran yang dirujuk eval Fase 7.
- **Reader Experience contracts** (owner-of-record, §1.3): model progress LWW-monotonic +
  serialisasi aktivitas; registry tipe saved-item (Fase 5 MENAMBAH tipe hadith di registry ini);
  bentuk snapshot `/me/sync` + batch replay; targeting reminder + flag preferensi + quiet-hours;
  inventaris event analitik (Q-8). Kitab memakai semuanya hari ini; hadith wajib memakai jalur
  yang sama.
- **Permukaan SEO** (Q-4): kontrak sitemap/feed + slug registry + laporan cakupan editorial.
- **Metadata atribusi** (Q-3): identitas edisi teks + riwayah audio untuk panel provenance FE.

### 6.3 Yang Fase 3 KONSUMSI

1B B-1…B-3 + C4/C5 (registry unit, resolusi anchor, cross-reference, lisensi, normalisasi);
pola editorial kitab (Fase 4 pemilik pola — implementasinya sudah ada di kode hari ini);
F1-B/C (metrik & supervisi untuk Q-6); F1-H (bila backfill diperlukan); keputusan operator O-3-1…4.

---

## 7. Open decisions (operator-owned)

**O-3-1 — Scope teks multi-riwayah (Warsh, Qalun, dst.).**
*Kenapa penting:* kelengkapan ilmiah vs biaya besar (import, tampilan, pemetaan penomoran antar
edisi). *Opsi:* (a) **Tunda** — Hafs-only, tapi kerjakan atribusi & deklarasi edisi sekarang (Q-3)
sehingga pintu terbuka rapi; (b) tambah satu riwayah kedua (Warsh) sebagai pilot edisi paralel;
(c) program multi-riwayah penuh. *Rekomendasi:* (a) — belum ada sinyal permintaan produk; arsitektur
sudah disiapkan agar (b)/(c) tidak pernah menjadi perombakan. *Default aman:* (a).

**O-3-2 — Kedalaman teks tambahan: word-by-word (terjemahan/transliterasi per kata) & tajwid berwarna.**
*Kenapa penting:* fitur belajar yang kuat; butuh resource berlisensi (QUL menyediakan) + effort
besar; menambah jenis unit "kata". *Opsi:* (a) tidak sekarang; (b) pilot word-by-word Indonesia
untuk beberapa surah populer; (c) penuh + tajwid. *Rekomendasi:* (b) setelah Q-1…Q-6 selesai, bila
metrik produk menunjukkan kebutuhan belajar. *Default aman:* (a).

**O-3-3 — Penambahan korpus terjemahan & qari (dan lisensinya).**
*Kenapa penting:* daya tarik produk vs kewajiban lisensi/atribusi per resource. *Opsi:* (a) tetap
dengan yang ada; (b) shortlist terkurasi (1–2 terjemahan id/en populer + 2–3 qari) dengan verifikasi
lisensi sebelum import; (c) buka lebar. *Rekomendasi:* (b). *Default aman:* (a) — dan apa pun yang
diimpor mengikuti gerbang `license_status` yang sudah ada.

**O-3-4 — Bentuk produk reading-plan.**
*Kenapa penting:* fitur retensi utama; menentukan effort Q-7. *Opsi:* (a) **versi ringan**
deterministik (target tanggal → kuota harian → on/off-track + reminder); (b) plan fleksibel
multi-jenis (juz/halaman/menit, jadwal per hari); (c) tunda semua. *Rekomendasi:* (a) — bahan sudah
ada, nilai retensi jelas. *Default aman:* (a).

---

## 8. Conformance

RAG Safety: fase ini justru MEMPERKUAT penegakan — unit Quran dikecualikan statis dari retrieval
interpretatif dan aturan itu menjadi test yang dirujuk Fase 7 (Q-2); tafsir tetap di luar scope
(`tafsir_range` tetap pointer; isinya milik Fase 4). Domain Integrity: terjemahan diperlakukan
sebagai rendering interpretif ber-atribusi penerjemah (Q3-D4); riwayah/edisi menjadi data
terstruktur (Q-3); konten editorial (keutamaan/asbabun-nuzul/intisari) berkelas provenance
`editorial`, ter-atribusi penulis+reviewer, license-gated, dan TIDAK PERNAH disajikan sebagai
tafsir ulama — kebijakan sumber-kutipan di dalam tubuh editorial dieskalasi ke governance (Fase 6)
bila diperketat.

## 9. North-star fit

Quran adalah pintu masuk produk (SEO + reader harian) SEKALIGUS tulang punggung sitasi seluruh
wiki: setiap tafsir, hadith, dan artikel entitas kelak menaut ke anchor yang dideklarasikan fase
ini. Dengan teks yang tak tergoyahkan, atribusi yang jujur sampai riwayah, editorial yang aman
disunting, dan pengalaman baca-dengar yang kontinu lintas perangkat — Quran menjadi bukti pertama
bahwa "ilmu yang bisa dipercaya sampai ke sumbernya" juga bisa terasa nyaman dipakai setiap hari.
