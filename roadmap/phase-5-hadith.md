# Fase 5 — Hadith Reader (Greenfield Masterplan)

> **Terikat pada charter** (`roadmap/README.md`), **kontrak Fase 1B** (Anchor/Citable Unit/
> Cross-Reference/provenance/lisensi/normalisasi — dipakai verbatim), **TEMPLATE kitab Fase 4**
> (`roadmap/phase-4-kitab-editorial.md` — pola deriver unit, editorial, identitas katalog, SEO,
> span entitas, rujukan ter-kurasi, plus daftar defect §1.2 sebagai daftar-larangan), dan **seam
> Reader Experience Fase 3** (`roadmap/phase-3-quran.md` §6.2 — diperluas, tidak dibangun ulang).
> Ditulis 2026-07-07. Greenfield diverifikasi ulang hari ini: nol entity/usecase/route/migrasi
> hadith — hanya sebutan insidental (enum interest, contoh string API, satu baris seed taksonomi).

---

## 1. Understanding — titik berangkat: nol kode, banyak bahan

Hadith belum ada di codebase — dan justru itu keunggulannya: domain ini bisa lahir langsung di
atas semua yang sudah dibayar mahal oleh Quran dan kitab, tanpa mewarisi utang retrofit mereka.
Bahan yang SUDAH tersedia dan menunggu dipakai:

1. **Kontrak 1B lengkap**: registry Citable Unit + lineage, resolusi Anchor, registry
   Cross-Reference (termasuk kind `parallel` — persis kebutuhan takhrij), enum lisensi, kelas
   provenance + identitas generation-run, normalisasi Arab kanonik ber-versi.
2. **Template kitab Fase 4**: pola deriver unit di skala (K-1), Work/Edition + antrean kurasi
   duplikat (K-2), rujukan ter-kurasi dengan re-resolve + antrean (K-3), SEO port (K-4), span
   entitas approved-only (K-6), loop editorial tertutup (K-9) — dan **daftar defect §1.2** yang
   menjadi daftar-larangan desain (lihat §2.2).
3. **Embrio ekstraksi yang relevan langsung** (diverifikasi): kelas sitasi
   `hadith_reference`, `athar`, `isnad_chain` di `scripts/langextract_kg/prompts.py:61–66` —
   artinya rujukan hadith DARI teks kitab sudah bisa diekstraksi; kelas mention
   `person`/`person_reference` (fondasi rijal); taksonomi domain ilmu ter-seed (baris 'hadith'
   di migrasi `20260525000004`).
4. **Skema `knowledge_*` sudah ada** (entities/labels/aliases/candidates dengan `review_status`
   5-nilai standar) — perawi dan otoritas penilai TIDAK butuh tabel baru.
5. **Seam Reader Experience**: registry tipe saved-item, progress LWW-monotonic, sync, notifikasi
   ber-keandalan (Q-6) — hadith tinggal MENDAFTARKAN tipe baru.
6. **Fakta katalog yang harus diakui**: koleksi hadith kemungkinan besar SUDAH ada di katalog
   kitab sebagai buku Shamela biasa (halaman/heading datar, tanpa identitas per-hadith). Hubungan
   antara "kopi buku" dan korpus hadith terstruktur adalah keputusan desain eksplisit (H-D1).

**Kompleksitas yang khas hadith** (tidak ada padanannya di kitab):
- **Penomoran multi-edisi**: hadith yang sama bernomor beda antar cetakan/penomoran (mis. skema
  penomoran yang berbeda antar edisi Bukhari/Muslim). Nomor = bagian identitas → harus milik edisi.
- **Grading kontensius by nature**: satu riwayat dinilai berbeda oleh ulama berbeda; label global
  tunggal = pelanggaran Domain Integrity (charter D11).
- **Isnad**: rantai perawi terurut dengan lafaz periwayatan (haddathana/akhbarana/'an) yang
  bermakna bagi ahli — struktur, bukan teks polos; dan perawi = calon entitas wiki.
- **Riwayat paralel (takhrij)**: satu matan muncul di banyak koleksi dengan rantai/lafaz berbeda —
  menautkannya adalah klaim ilmiah, bukan deduplikasi teknis.

---

## 2. Vision — hadith yang lahir benar

### 2.1 Model konseptual (decision-complete, tanpa DDL)

- **Hierarki**: Koleksi (= Work di registry K-2, mis. Sahih al-Bukhari) → **Edisi** (cetakan/
  penomoran tertentu + muhaqqiq + lisensi; satu edisi dideklarasikan sebagai **kanon penomoran**
  per koleksi — keputusan O-5-1) → struktur kitab/bab (abwab, pohon dangkal ter-validasi) →
  **entri hadith** (atom identitas) → unit-unit di bawahnya.
- **Anchor hadith** (kontrak 1B C1): korpus `hadith`; lingkup Work = koleksi; locator kanonik =
  nomor hadith menurut edisi kanon koleksi itu; **penomoran edisi lain = alias ter-peta** (pola
  yang sama dengan keputusan Q3-D3 untuk mushaf — identitas tunggal + alias, bukan identitas
  ganda); jalur struktural kitab/bab = alias navigasi; bentuk rentang = rentang nomor dalam satu
  koleksi. Anchor tidak pernah didaur ulang; suntingan memakai lineage 1B.
- **Citable Unit**: **matn = unit sitasi atomik** (kelas provenance `source`, ter-atribusi
  koleksi+edisi, lisensi mewarisi edisi); **isnad = struktur tertaut** (posisi perawi terurut:
  teks nama + bentuk ternormalisasi C5 + lafaz periwayatan + tautan-mention ke entitas) DAN
  tersedia sebagai unit teks tersendiri agar bisa dikutip; terjemahan matn per (sumber, hadith) =
  unit rendering ter-atribusi penerjemah (pola Q3-D4); catatan editorial = kelas `editorial`.
- **Grading Assertion** (glosarium charter — di sinilah ia hidup pertama kali): klaim ter-atribusi
  (target = anchor hadith — atau spesifik jalur riwayat bila sumbernya menilai jalur; otoritas =
  entitas `knowledge_entities`; **verdict dari kosakata terkontrol-namun-extensible**
  [sahih/hasan/da'if/mawdu' + gabungan] DENGAN lafaz asli verbatim dipertahankan; sitasi sumber
  penilaian = anchor/kutipan — klaim tentang klaim tetap ber-provenance; metode
  import/ekstraksi-mesin/manusia + confidence + `review_status` 5-nilai standar). **Tidak ada
  kolom "derajat" global. Tidak pernah.** Tampilan default = daftar "X menurut Y".
- **Takhrij/paralel**: Cross-Reference kind `parallel` antar anchor hadith (dan ke kopi-buku
  kitab) dengan metode+confidence+review — TANPA entitas "hadith-induk" yang memaksa identitas
  (klaim kesamaan riwayat adalah klaim, bukan fakta struktur).
- **Perawi & otoritas = `knowledge_entities`** (skema yang sudah ada) lewat antrean kandidat —
  TANPA tabel perawi paralel. Fase 5 menjadi penulis pertama ke registry entitas; kurasi &
  disambiguasi industrial = Fase 6.

### 2.2 Invarian desain sejak lahir (defect kitab di-desain-keluar — bukan diperbaiki belakangan)

Diadopsi sebagai syarat kelahiran, dipetakan 1:1 dari defect Fase 4 §1.2:

1. **Ingest non-destruktif & ber-versi** (anti-D1): rilis sumber ber-manifest; re-import =
   staged diff + tombstone + persetujuan; TIDAK ADA `DELETE` destruktif di jalur importer;
   suite test importer ada SEBELUM importer dipakai produksi (anti-D6).
2. **Integritas referensial internal penuh** (anti-D3): semua tabel korpus hadith ber-FK; data
   pengguna menunjuk lewat anchor + lineage (kebijakan K-D2), bukan kolom polos yang bisa
   menggantung.
3. **Query publik terikat batas** (anti-D2/D4): semua list ber-paginasi dengan clamp limit/offset
   sejak endpoint pertama.
4. **Search hanya lewat normalisasi kanonik C5 + pola ter-escape** (anti-D5/D8): tidak ada
   normalizer lokal baru; tidak ada ILIKE telanjang.
5. **Struktur ter-validasi saat ingest** (anti-D7): pohon bab dicek siklus/parent-hilang dengan
   alert, bukan flatten senyap.
6. **Semua keluaran mesin ber-identitas run** (B-6) dan **lisensi ter-gate** (B-4) sejak baris
   pertama; **editorial standar** (draft/publish + ETag + revisi) sejak fitur kurasi pertama —
   tidak ada era "single-state" seperti yang dialami editorial Quran.

### 2.3 Bar kuantitatif (menambah charter §2.3 baris Hadith)

Integritas korpus: jumlah entri per koleksi = manifest edisi kanon, dicek CI (angka pastinya milik
manifest edisi yang dipilih — bukan angka hafalan); importer deterministik (re-import identik =
no-op; ID stabil 100%). Grading: 100% grading yang tampil ber-atribusi otoritas + sumber; hadith
tanpa penilaian menampilkan status eksplisit "belum ada penilaian di sumber kami" — tidak pernah
kosong yang ambigu; 0 tampilan derajat tanpa pemilik. Anchor: resolusi ≤50ms p95; alias penomoran
edisi ter-peta 100% untuk edisi yang diimpor. Search: p95 <400ms; paginasi ter-clamp. Terjemahan:
sesuai kebijakan O-5-3 (default: matn publik = reviewed). Sitasi menggantung = 0 (audit 1B).
Retrieval (serah-terima Fase 7): metadata grading & ringkasan isnad IKUT pada setiap sitasi unit
matn; unit hadith lolos kelayakan interpretatif hanya sesuai aturan 1B C4.

---

## 3. Gap & opportunity analysis (greenfield → komponen yang harus ada, terurut)

| # | Komponen | Prioritas | Effort | Risiko khas |
|---|---|---|---|---|
| H-G1 | Model inti + Anchor + manifest edisi + importer staged (pilot per O1: Bukhari→Muslim) | **P0** | Besar | Kualitas & lisensi sumber data (O-5-4); kanon penomoran (O-5-1) |
| H-G2 | Matn/isnad sebagai unit 1B + reader API + search C5 | **P0** | Sedang–besar | — (pola sudah terbukti di K-1) |
| H-G3 | Grading Assertions + antrean kurasi | **P0** (nilai inti) | Sedang | Pilihan otoritas = sensitif manhaj (O-5-2); jangan pernah auto-grade oleh LLM |
| H-G4 | Isnad terstruktur + seam mention→entitas (umpan Fase 6) | P1 | Sedang–besar | Skala disambiguasi perawi (ribuan nama, banyak samaran) — nilai penuh menunggu Fase 6 |
| H-G5 | Rujukan silang: hadith→Quran; kitab→hadith (pakai kelas ekstraksi yang sudah ada); paralel intra-korpus | P1 | Sedang | Ekstraksi = klaim mesin → wajib antrean review (pola K-3) |
| H-G6 | Terjemahan multilingual + editorial penuh | P1 | Sedang | Kebijakan review matn (O-5-3) |
| H-G7 | Permukaan produk: SEO, saved/progress types, notifikasi, span entitas | P1–P2 | Sedang | Semuanya port pola (K-4/K-6, Q-4/Q-5/Q-6) |
| H-G8 | Serah-terima RAG: eligibility + metadata sitasi + seed eval | P1 | Kecil | — |

**Risiko terbesar fase**: (1) sumber korpus & grading yang lisensinya jelas — tanpa itu H-G1/H-G3
tak bisa mulai (gerbang O-5-4/O3); (2) godaan menyederhanakan grading menjadi satu label demi UI —
dilarang by design; (3) membangun tabel perawi sendiri "sementara" — dilarang (H-D5), karena
"sementara" akan permanen dan Fase 6 mewarisi dua sistem entitas.

---

## 4. Roadmap — urutan pembangunan Fase 5

Urutan: **H-0 → H-1 → (H-2 ∥ H-3) → H-4 → H-5 → H-6 → H-7.** H-0 menunggu keputusan O-5-1/O-5-4 +
1B terbangun; H-6 menunggu Q-4/Q-6/K-6 mendarat; pelajaran deriver K-1 dikonsumsi H-1 (deriver
hadith lebih sederhana — sumber terstruktur, bukan HTML bebas).

### H-0 — Fondasi korpus: model, manifest edisi, importer staged  *(P0, effort besar)*

**Rationale:** H-G1; semua invarian §2.2 lahir di sini. **Isi:** model hierarki + anchor per §2.1;
manifest edisi (identitas edisi, muhaqqiq, skema penomoran, lisensi, checksum sumber); importer
per-sumber (adapter) dengan rilis ber-versi, staged diff, tombstone, dry-run, dan suite test
ditulis lebih dulu; validasi struktur bab; pilot: satu koleksi (default O1: Bukhari) end-to-end
sebelum koleksi kedua.
**AC:** re-import rilis identik = no-op dengan ID stabil 100%; rilis yang menghapus entri
menghasilkan diff yang harus disetujui — tidak ada jalur delete langsung; jumlah entri = manifest
di CI; struktur bab tervalidasi (siklus → tolak + alert).
**DS:** koleksi pertama bisa dibuka per kitab/bab/nomor di lingkungan dev, dan Salman melihat
laporan import yang menyebut persis apa yang masuk.

### H-1 — Unit 1B + reader API + search  *(P0, effort sedang–besar)*

**Rationale:** H-G2. **Isi:** minting unit (matn `source`; isnad-teks; terjemahan rendering;
catatan editorial) via service tulis tunggal 1B; reader API: browse koleksi→kitab→bab→hadith,
detail hadith (matn + isnad terstruktur + grading + terjemahan + availability ala kitab), lompat
nomor ("bukhari 1"), paginasi ter-clamp; search matn/terjemahan via normalisasi C5 + trigram +
escaping (bar: p95 <400ms); anchor legacy tidak ada (greenfield) — semua anchor lahir kanonik.
**AC:** setiap hadith published resolvable via anchor ≤50ms p95; unit deterministik; endpoint
publik semua ber-clamp; search fuzz `%_\` aman.
**DS:** mencari "niat" menemukan hadith Arba'in #1; mengetik "bukhari 6018" langsung membuka
hadith-nya.

### H-2 — Grading Assertions + kurasi  *(P0 — nilai pembeda produk, effort sedang)*

**Rationale:** H-G3; charter D11. **Isi:** model Grading Assertion §2.1; import grading dari
sumber ber-lisensi (per O-5-2) dengan lafaz verbatim + pemetaan kosakata; antrean kurasi
(pending/ambiguous → approve/reject, peran editor existing) memakai pola K-3; tampilan & API:
daftar per-otoritas + status "belum ada penilaian"; TANPA grading yang dihasilkan LLM (ekstraksi
mesin dari teks sumber boleh SEBAGAI KLAIM ber-run-identity yang masuk antrean, tidak pernah
langsung tampil).
**AC:** satu hadith dengan dua penilaian berbeda menampilkan keduanya ter-atribusi + sumber
penilaian; 0 label tanpa pemilik di seluruh API; klaim grading hasil mesin tidak pernah tampil
publik tanpa approve.
**DS:** halaman hadith menunjukkan "sahih menurut X; da'if menurut Y" — persis janji charter.

### H-3 — Isnad terstruktur + seam perawi→entitas  *(P1, effort sedang–besar)*

**Rationale:** H-G4; umpan utama Fase 6. **Isi:** posisi perawi terurut (nama verbatim + bentuk C5
+ lafaz periwayatan + urutan); setiap posisi = mention yang masuk antrean kandidat entitas
(`knowledge_entity_candidates` — skema sudah ada); perawi ber-entitas tampil sebagai span yang bisa
diklik (pola K-6, approved-only); TANPA tabel perawi paralel (H-D5); disambiguasi massal bukan
target fase ini — antrean yang sehat adalah targetnya.
**AC:** isnad tampil terstruktur (bukan teks polos) dengan lafaz periwayatan; posisi perawi yang
sudah dikurasi menaut ke entitas dan resolvable; posisi yang belum = kandidat ber-antrean, tidak
menaut diam-diam.
**DS:** di satu hadith, nama perawi yang sudah dikurasi bisa diklik — dan Fase 6 mewarisi antrean
perawi yang rapi, bukan kekacauan.

### H-4 — Terjemahan multilingual + editorial penuh  *(P1, effort sedang)*

**Rationale:** H-G6. **Isi:** terjemahan matn per (sumber, hadith) ter-atribusi (manusia atau
mesin ber-run-identity B-6); kebijakan publik per O-5-3 (default: matn tampil publik hanya
reviewed; judul bab boleh generated berlabel); editorial standar penuh (draft/publish + ETag +
revisi + antrean feedback K-9) untuk terjemahan, judul bab, dan catatan.
**AC:** tidak ada terjemahan matn unreviewed yang tampil publik (bila default O-5-3 berlaku);
setiap suntingan ber-ETag + revisi; promosi reviewed tercatat reviewer-nya.
**DS:** terjemahan hadith yang tampil sudah melewati mata manusia — dan Salman bisa melihat siapa
yang mereview.

### H-5 — Rujukan silang & takhrij  *(P1, effort sedang)*

**Rationale:** H-G5. **Isi:** hadith→Quran via pola resolver K-3 di atas matn (anchor-only —
ayat dirujuk, tak pernah ditafsirkan); kitab→hadith memakai kelas ekstraksi yang SUDAH ada
(`hadith_reference`/`isnad_chain`) → klaim ber-confidence masuk antrean kurasi → registry
Cross-Reference; paralel intra-korpus (takhrij) sebagai kind `parallel` ter-kurasi; tautan Work
antara koleksi terstruktur dan kopi-buku Shamela-nya (K-2).
**AC:** dari satu hadith terlihat "diriwayatkan juga di [koleksi lain]" dan "dikutip di [kitab]"
— semuanya approved-only ber-confidence; dari halaman ayat terlihat hadith yang merujuknya;
re-resolve idempoten.
**DS:** takhrij ringkas muncul di halaman hadith — dan setiap tautannya bisa dilacak siapa/apa
yang mengklaimnya.

### H-6 — Permukaan produk: SEO, personal, notifikasi, span  *(P1–P2, effort sedang)*

**Rationale:** H-G7 — hadith adalah reader domain penuh, bukan API saja. **Isi:** SEO port pola
K-4/Q-4 (slug koleksi + halaman hadith, editorial SEO ber-workflow, sitemap/feed dengan lastmod);
tipe saved-item `hadith` (+rentang nomor) DIDAFTARKAN di registry seam Fase 3 + progress
per-koleksi (posisi baca LWW; milestone tamat-koleksi) + sync; notifikasi di lapisan Q-6
(hadith-harian opt-in, lanjut-baca, milestone koleksi — semua ber-flag preferensi, dedupe,
quiet-hours); span entitas di matn (pola K-6).
**AC:** halaman hadith masuk sitemap dengan lastmod akurat; saved/progress hadith sinkron lintas
perangkat lewat `/me/sync` yang sama; notifikasi hadith menghormati dedupe+quiet-hours; tidak ada
store personal paralel (diaudit).
**DS:** pengguna bisa menyimpan hadith, melanjutkan bacaan koleksinya di perangkat lain, dan
(bila opt-in) menerima satu hadith setiap pagi — tanpa ada sistem baru yang dibangun untuk itu.

### H-7 — Serah-terima RAG: eligibility, sitasi ber-grading, seed eval  *(P1, effort kecil)*

**Rationale:** H-G8; kontrak konsumsi Fase 7. **Isi:** unit matn masuk kelayakan interpretatif per
1B C4 (source = layak); **metadata sitasi wajib**: setiap sitasi unit matn membawa ringkasan
grading per-otoritas + identitas koleksi/edisi + ringkasan isnad — sehingga jawaban RAG tidak
mungkin mengutip hadith tanpa status keotentikannya ikut; seed golden eval: kasus atribusi grading,
kasus ikhtilaf penilaian, kasus "da'if tidak boleh tampil sebagai sahih", kasus takhrij.
**AC:** API unit/sitasi hadith selalu menyertakan payload grading ter-atribusi; ≥10 kasus eval
hadith masuk golden set Fase 7; test kelayakan menegaskan unit hadith unreviewed-machine tidak
pernah lolos.
**DS:** ketika kelak RAG menjawab dengan hadith, derajat dan penilainya SELALU tercetak di samping
kutipannya.

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| H-D1 | Hadith = korpus first-class terstruktur; kopi-buku Shamela dari koleksi yang sama TETAP ada dan ditaut di level Work (K-2) | Identitas per-hadith/grading/isnad mustahil di model halaman; menghapus kopi-buku = memiskinkan katalog | Memodelkan hadith sebagai buku ber-heading (kehilangan semua kekhasan); menghapus kopi-buku |
| H-D2 | Anchor: koleksi + nomor menurut EDISI KANON per koleksi; penomoran edisi lain = alias ter-peta | Cermin Q3-D3 (identitas tunggal + alias); nomor tanpa edisi = ambigu selamanya | Nomor "universal" buatan sendiri; identitas paralel per edisi |
| H-D3 | Matn = unit sitasi atomik; isnad = struktur tertaut + unit teks; grading+isnad ringkas IKUT di metadata sitasi | Mandat misi: "grading and isnad travel with the citation"; retrieval butuh matn, kejujuran butuh konteksnya | Hadith utuh satu blob (isnad tak terstruktur); isnad metadata-saja (tak bisa dikutip) |
| H-D4 | Verdict grading: kosakata terkontrol-extensible + lafaz asli verbatim selalu tersimpan | Istilah gabungan (mis. "hasan sahih") tak boleh dipaksa masuk kotak; pemetaan = interpretasi yang harus bisa diaudit | Enum kaku (menghancurkan nuansa); teks bebas tanpa pemetaan (tak bisa difilter) |
| H-D5 | Perawi & otoritas = `knowledge_entities` existing + antrean kandidat; TANPA tabel perawi paralel | Skema sudah ada; dua sistem entitas = utang yang charter larang; Fase 6 mewarisi antrean bersih | Tabel rijal sendiri "sementara" |
| H-D6 | Takhrij = Cross-Reference `parallel` ter-kurasi; TANPA entitas hadith-induk di v1 | Kesamaan riwayat = klaim ilmiah ber-confidence, bukan fakta struktur; merge paksa = flatten ikhtilaf | Master-hadith entity dengan auto-clustering |
| H-D7 | Invarian anti-defect §2.2 = syarat kelahiran (gate review desain & CI), bukan backlog | Retrofit selalu lebih mahal — bukti: K-0 | "Perbaiki nanti seperti kitab" |
| H-D8 | Grading TIDAK PERNAH dihasilkan LLM; ekstraksi mesin dari sumber = klaim ber-antrean, bukan tampilan | Garis merah Domain Integrity; grading = perkara otoritas manusia | Auto-grading / "confidence score keotentikan" buatan model |
| H-D9 | Tipe personal hadith (saved/progress) didaftarkan di seam Fase 3; sync & notifikasi lewat jalur yang sama | Q3-D7/K-D7; satu substrat personal | Store personal hadith sendiri |
| H-D10 | Importer per-adapter dengan manifest ber-versi; tanpa scraping runtime | Reprodusibilitas + lisensi bisa diaudit per rilis | Sinkronisasi live dari situs pihak ketiga |

**Asumsi:** H-A1 — default O1 berlaku (mulai Bukhari, lalu Muslim); H-A2 — sumber korpus + data
grading berlisensi-jelas tersedia/diperoleh (gerbang O-5-4; bila tidak, fase tertahan di H-0 dan
itu KEPUTUSAN yang benar); H-A3 — nilai rijal penuh menunggu kurasi Fase 6 (fase ini menjamin
antrean bersih, bukan disambiguasi selesai); H-A4 — pelajaran deriver K-1 terdokumentasi sebelum
H-1.

> **Conflicts with charter: TIDAK ADA pembatalan — satu penajaman sequencing.** Charter §4.3
> menyebut perawi "sebagai calon Knowledge Entity" (Fase 6). Fase ini menajamkan: Fase 5 menjadi
> **penulis pertama** ke `knowledge_entities`/`knowledge_entity_candidates` (skema existing) untuk
> perawi & otoritas via antrean kurasi — Fase 6 tetap pemilik taksonomi/disambiguasi/halaman
> entitas dan WAJIB menerima antrean ini sebagai input, bukan membangun registry kedua. Ditandai
> untuk diperiksa Fase 6 dan direkonsiliasi Fase 9.

---

## 6. Interfaces (seams)

**Fase 5 MENGEKSPOS:**
- **Anchor hadith + alias penomoran multi-edisi** (resolusi via 1B) — dikonsumsi kitab (rujukan),
  Fase 6 (backlink entitas), Fase 7 (sitasi).
- **Unit matn ber-grading**: kontrak "sitasi membawa keotentikan" — payload grading per-otoritas +
  koleksi/edisi + ringkasan isnad pada setiap sitasi (H-7) — kontrak konsumsi utama Fase 7.
- **Grading Assertion capability** (per-otoritas, ter-kurasi, lafaz verbatim) — pola yang bisa
  dipakai ulang untuk penilaian lain (mis. keotentikan atsar) di masa depan.
- **Antrean perawi→entitas** (mention + kandidat ber-review) — input langsung Fase 6.
- **Perluasan seam Reader Experience**: tipe saved-item/progress hadith, event notifikasi hadith.
- **Permukaan SEO hadith** (slug/sitemap/editorial) — satu pola dengan Quran & kitab.

**Fase 5 MENGONSUMSI:** 1B B-1…B-5 + C1–C5 (semua kontrak backbone); Fase 4: template deriver
(K-1), Work/Edition + kurasi duplikat (K-2), pola rujukan ter-kurasi (K-3), pola SEO (K-4), pola
span (K-6), pola loop editorial (K-9), dan **daftar defect §1.2 sebagai daftar-larangan**; Fase 3:
seam Reader Experience §6.2 + keandalan notifikasi Q-6 + infra sitemap Q-4; F1-B/C/H; keputusan
operator O1/O3/O-5-1…4.

---

## 7. Open decisions (operator-owned)

**O-5-1 — Edisi kanon penomoran per koleksi.**
*Kenapa penting:* nomor hadith adalah cara dunia mengutip; salah pilih kanon = seluruh tautan
eksternal canggung selamanya. *Opsi:* (a) **penomoran yang paling lazim dikutip per koleksi**
(untuk Bukhari/Muslim: skema penomoran standar yang dipakai mayoritas rujukan modern); (b) ikut
penomoran sumber data apa adanya; (c) penomoran internal sendiri. *Rekomendasi:* (a) — dan edisi
lain tetap terpeta sebagai alias. *Default aman:* (a).

**O-5-2 — Otoritas grading yang diimpor pertama.**
*Kenapa penting:* ini pilihan manhaj yang terlihat publik; salah framing = tuduhan keberpihakan.
*Opsi:* (a) hanya penilaian internal-koleksi (mis. komentar at-Tirmidzi atas riwayatnya) — paling
aman, cakupan tipis; (b) **(a) + 1–2 otoritas takhrij yang paling luas dipakai**, semua
ter-atribusi ketat; (c) himpunan luas multi-manhaj sejak awal. *Rekomendasi:* (b) — dengan framing
produk "kami melaporkan penilaian, bukan menilai" (charter §2.1). *Default aman:* (a).

**O-5-3 — Kebijakan publik terjemahan matn.**
*Kenapa penting:* salah terjemah sabda Nabi ﷺ lebih berbahaya daripada salah terjemah prosa kitab.
*Opsi:* (a) **matn publik hanya yang reviewed** (judul bab boleh generated berlabel); (b) sama
seperti kitab (generated tampil berlabel — O-4-4a); (c) generated hanya untuk pengguna opt-in.
*Rekomendasi:* (a) — lebih ketat daripada kitab, sengaja. *Default aman:* (a).

**O-5-4 — Akuisisi sumber korpus & data grading (lisensi).**
*Kenapa penting:* gerbang mutlak H-0/H-2; teks koleksi + terjemahan + data grading masing-masing
punya pemegang hak berbeda. *Opsi:* (a) **hanya sumber machine-readable berlisensi jelas/terbuka**,
cakupan mengikuti ketersediaan; (b) + negosiasi lisensi untuk sumber tertutup prioritas; (c) impor
dulu urusan belakangan — DITOLAK oleh desain (gerbang B-4). *Rekomendasi:* (a) mulai sekarang, (b)
untuk pelengkap bernilai tinggi. *Default aman:* (a) — dan yang tak jelas tetap `needs_review`,
tidak pernah publik.

---

## 8. Conformance

Domain ini adalah ujian terberat Domain Integrity — dan jawabannya struktural: grading per-otoritas
dengan lafaz verbatim, tanpa label global, tanpa auto-grading LLM (H-D4/H-D8); da'if tidak mungkin
tampil sebagai sahih karena derajat selalu ter-atribusi dan ikut pada sitasi (H-7); ayat Quran
dirujuk anchor-only (H-5) — makna tetap mengalir dari hadith/kitab, tidak pernah dari teks ayat;
provenance source/editorial/machine + identitas run melekat sejak lahir; setiap tautan (takhrij,
rujukan, perawi→entitas) adalah klaim ter-review ber-confidence di registry 1B.

## 9. North-star fit

Hadith adalah korpus kedua RAG dan penghubung terpadat menuju wiki: setiap isnad adalah untaian
entitas perawi, setiap takhrij adalah jaring lintas-koleksi, setiap grading adalah suara ulama yang
ter-atribusi. Dibangun greenfield di atas backbone + template kitab — dengan defect lama
di-desain-keluar sejak lahir — domain ini membuktikan tesis seluruh roadmap: korpus baru kini bisa
lahir LANGSUNG benar, cepat, dan seragam. Ketika Fase 6 membuka antrean perawinya dan Fase 7
menyaring retrieval berdasarkan keotentikan ter-atribusi, janji "wiki Islam yang bisa dipercaya
sampai ke sumbernya" tinggal selangkah.
