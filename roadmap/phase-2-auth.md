# Fase 2 — Auth & Identity

> **Terikat pada charter** (`roadmap/README.md`). Fase ini menutup sisa G12 charter (MFA admin,
> rotasi JWT dual-key) dan — mandat baru misi — menyiapkan **permukaan authz yang tumbuh**:
> editor/kurator (F4 K-9), **scholar-reviewer** (F6 W-0, gerbang klaim sensitif), identitas mesin
> ber-scope untuk pipeline ekstraksi (F6/1B) dan lapisan inferensi (F7). Berjalan paralel dengan
> fase konten; TIDAK memblokir 1B/3/4 — tetapi A-1 (RBAC) harus mendarat sebelum implementasi
> W-0/K-9. Ditulis 2026-07-07 di atas eksplorasi Fase 0 + verifikasi enforcement hari ini.

---

## 1. Understanding — auth pasca-hardening: jujurnya sudah bagus, dengan lubang yang bisa disebut

### 1.1 Yang SUDAH solid (verifikasi bukti, bukan pujian)

Hardening Jun 2026 (PR #28 dkk.) nyata dan berkualitas:

- **Sesi & token**: akses pendek (15m prod) + refresh (720h) dalam **keluarga sesi ber-rotasi
  atomik** dengan **deteksi reuse** (race di `RotateAuthSession` → `ErrInvalidRefreshToken`) +
  **alert admin** saat reuse terdeteksi; `users.token_version` membatalkan semua JWT lama seketika
  pada reset/ganti-password/ganti-email/hapus-akun; endpoint daftar/cabut sesi ada (ber-rate-limit).
- **Login aman**: lockout progresif (backoff eskalatif per email-hash), rate-limit **berbasis DB**
  (berlaku lintas instance) per email+IP untuk semua aksi auth, bcrypt cost 12, normalisasi huruf
  email, **timing konstan** anti-enumerasi.
- **Alur email matang**: verifikasi/reset/ganti-email dengan tautan + OTP 6-digit ber-TTL terpisah,
  single-use, antrean kirim (outbox) + retry berjadwal, notifikasi keamanan opsional lengkap;
  `EMAIL_DELIVERY_MODE=log` untuk dev.
- **Audit & kebersihan**: `auth_audit_logs` tersanitasi (tak pernah menyimpan password/JWT/token
  mentah), job cleanup ber-retensi, CLI `set-user-role` dengan guard last-admin untuk pemulihan.
- **HTTP**: helmet + HSTS + CORS ketat; `govulncheck`/gosec baseline bersih; dokumentasi FE auth
  1.200+ baris.

### 1.2 Mekanika authz HARI INI (diverifikasi ulang untuk mandat fase ini)

- **Model peran = satu kolom** `role ∈ {user, editor, admin}` + helper kapabilitas di entity
  (`CanReviewEditorial` = editor+admin; `CanPublishEditorial` = admin).
- **Enforcement hanya di 3 titik grup route** (`router.go:228` editorial editor+admin; `:278`
  editorial admin-only; `:290` grup /admin) + `middleware.RequireRoles`. Kabar baiknya: permukaan
  migrasi ke model lebih kaya KECIL.
- **Identitas mesin = SATU rahasia statis**: `COLLAB_SERVICE_TOKEN` (≥32 byte, banding
  constant-time, header `X-Internal-Token`) menjaga `/internal/*` — tanpa identitas per-layanan,
  tanpa scope, tanpa kedaluwarsa; rotasi = ganti env + restart dua layanan.
- **Mesin lain melewati HTTP sama sekali**: pipeline Python & semua importer memakai `PG_URL`
  penuh (kuasa DB total). W-0 (Fase 6) sudah memutuskan pembatasan **DB grants** untuk keluaran
  kelas-pending ekstraksi; importer tetap berkuasa penuh (melekat pada fungsinya, dijinakkan oleh
  staged-import K-0).
- JWT: HS256, klaim minimal (sub, `token_version`, sid), issuer/audience divalidasi; **satu
  secret statis — rotasi = restart + logout massal**, sehingga dalam praktik tidak pernah dirotasi.

### 1.3 Risiko residual — siapa yang terluka, seberapa parah, seberapa mendesak (penilaian saya)

| # | Risiko | Apa yang benar-benar bisa terjadi | Severity × Kemungkinan → Urgensi |
|---|---|---|---|
| R1 | **Akun admin tanpa MFA** | Satu password admin bocor (phishing/reuse) = seluruh platform: ubah peran, publish/unpublish semua konten, akses email campaign. Skenario terburuk bukan vandalisme kasar — tapi **penyuntingan halus konten agama** yang merusak kepercayaan secara diam-diam | Tinggi × Sedang → **P0** |
| R2 | **JWT_SECRET tunggal, rotasi = mati** | Bocor (env/laptop/backup) = pemalsu bisa MENANDATANGANI token siapa pun termasuk admin, tak terdeteksi sampai token_version berubah; karena rotasi menyakitkan, secret menua bertahun-tahun | Tinggi × Rendah–sedang → **P0–P1** |
| R3 | **Refresh 720h tanpa ikatan perangkat** | Refresh dicuri (malware/backup) = akses akun 30 hari; deteksi-reuse hanya menangkap bila pencuri & korban sama-sama memakai token | Sedang × Sedang → P1 |
| R4 | **Authz kasar utk tim yang tumbuh** | Saat kurasi F4–F6 hidup, orang yang cuma perlu "approve terjemahan" diberi admin penuh → blast-radius merayap; **gerbang scholar-reviewer (W-0) tak bisa dibangun** di model sekarang | Sedang HARI INI, Tinggi saat F4/6 → **P0 (memblokir W-0/K-9)** |
| R5 | **Satu service-token tak ber-scope** | Bocor dari collab-server = seluruh API internal atas nama "mesin"; audit tak bisa membedakan layanan mana | Sedang × Rendah–sedang → P1 |
| R6 | **Kredensial DB penuh di tangan skrip** | Laptop/env skrip bocor = database penuh (baca+tulis semua). W-0 grants menjinakkan ekstraksi; importer tersisa | Tinggi × Rendah → P1 (formalisasi) |
| R7 | **Tanpa step-up untuk aksi destruktif** | Sesi admin yang dibajak (XSS di FE, laptop tak terkunci) bisa langsung unpublish korpus / ganti peran tanpa tantangan ulang | Sedang × Sedang → P1 |
| R8 | **Deteksi anomali tipis** | Lonjakan gagal-login, reset massal, atau pembuatan admin baru tidak membangunkan siapa pun (satu-satunya alert hari ini: reuse refresh) | Sedang × Sedang → P1 (menumpang F1-B) |
| R9 | Ketergantungan satu penyedia email | Cloudflare Email tumbang = tak ada yang bisa verifikasi/reset selama outage (antrean menahan, tak hilang) | Rendah–sedang × Rendah → P2 |

### 1.4 Vonis arsitektur: layak dipertahankan — dievolusi, bukan diganti

Pertimbangan jujur terhadap re-arsitektur: **IdP eksternal / self-host IdP** (Auth0/Keycloak/ORY)
tertolak — biaya ops+lisensi+residensi-data untuk tim satu-developer, dan tak satu pun risiko
R1–R9 yang MEMBUTUHKANNYA. **Token opaque (sesi murni DB)** tertolak dengan catatan jujur:
`AuthenticateUserSession` hari ini SUDAH menyentuh DB tiap request (cek `token_version`+sesi),
jadi "statelessness" JWT sebenarnya sudah dibelanjakan — namun migrasi format token = blast radius
FE+mobile untuk keuntungan nol; JWT dipertahankan dan dibuat **agile** (A-4). Kesimpulan: evolusi
in-place di 6 inisiatif.

---

## 2. Vision — identitas yang membosankan dan pantas dipercaya

1. **Manusia**: setiap pemegang kuasa tinggi (admin, scholar-reviewer) ber-MFA; aksi destruktif
   menuntut step-up; peran mengikuti fungsi (bukan semua-jadi-admin).
2. **Kapabilitas, bukan kolom**: keputusan authz dibuat di SATU titik kebijakan berbasis
   kapabilitas (mis. "boleh approve klaim sensitif", "boleh publish produksi") — peran hanyalah
   bundel kapabilitas; fase konten memeriksa kapabilitas, tidak pernah string peran.
3. **Mesin = warga kelas satu**: tiap layanan/otomasi punya identitas bernama, ber-scope sempit,
   ber-kedaluwarsa, ter-audit — bukan satu rahasia bersama dan bukan `PG_URL` sakti.
4. **Kelincahan rahasia**: rotasi JWT & token layanan adalah operasi rutin tanpa drama (tanpa
   logout massal), dibuktikan drill.
5. **Terlihat**: peristiwa auth yang aneh membangunkan orang (menumpang F1-B).

**Bar kuantitatif fase ini** (menambah charter §2.3): MFA aktif pada 100% akun admin &
scholar-reviewer (editor: sesuai O-2-1); rotasi JWT tanpa-logout terbukti drill ≥1×/6 bulan;
0 pemeriksaan string-peran di luar titik kebijakan (ditegakkan test kontrak + lint); 100% mutasi
ber-kuasa (publish/approve/role/unpublish/delete) ter-audit dengan aktor+kapabilitas; token
layanan: per-konsumen, ber-scope, umur ≤90 hari, hash-at-rest, rotasi terdokumentasi; alert
anomali (gagal-login surge, reset massal, admin baru, perubahan peran) menyala ≤5 menit;
step-up wajib pada aksi destruktif kelas-atas.

---

## 3. Gap & opportunity analysis

| # | Celah | Prioritas | Effort | Catatan |
|---|---|---|---|---|
| A-G1 | Model peran kasar → RBAC ber-kapabilitas + peran baru (scholar-reviewer, kurator) | **P0** | Sedang | **Memblokir W-0/K-9**; permukaan migrasi kecil (3 titik + helper) |
| A-G2 | MFA + step-up utk kuasa tinggi (R1, R7) | **P0** | Sedang | Perlindungan tunggal terbesar terhadap skenario terburuk |
| A-G3 | Identitas mesin ber-scope (R5, R6) | P1 | Sedang | Selaras W-D2 (grants utk penulis massal; token ber-scope utk HTTP) |
| A-G4 | Kelincahan JWT: dual-key `kid` (R2) | P1 | Kecil–sedang | Charter G12 |
| A-G5 | Pengerasan refresh/sesi (R3) | P1 | Kecil | Umur + sliding + label perangkat |
| A-G6 | Alert anomali auth (R8) + ketahanan email (R9) | P1/P2 | Kecil | Menumpang F1-B; flag polling event CF sudah ada |

---

## 4. Roadmap — inisiatif Fase 2

Urutan: **A-1 dulu (memblokir fase konten), A-3 segera setelahnya (perlindungan terbesar), A-2 →
A-4 → A-5 → A-6 menyusul.** Semua aditif; tidak ada perubahan kontrak breaking bagi FE/mobile
(login/refresh existing tetap; MFA & step-up = alur tambahan terdokumentasi).

### A-1 — RBAC ber-kapabilitas + peran generasi konten  *(P0, effort sedang)*

**Rationale:** A-G1/R4; W-0 (gerbang klaim sensitif) dan K-9 (atribusi promosi reviewed) tak bisa
berdiri di atas admin-flag. **Isi:** titik kebijakan tunggal (modul policy) yang memetakan peran →
kapabilitas bernama (contoh kelas kapabilitas — bukan daftar final: kelola-pengguna,
publish-produksi, review-editorial, approve-klaim-netral, **approve-klaim-sensitif** [scholar],
kurasi-entitas, kelola-token-layanan); peran diperluas: `user, editor, curator, scholar_reviewer,
admin` (admin = superset); SEMUA pemeriksaan route/usecase pindah ke kapabilitas (3 titik grup +
helper entity — kecil); test kontrak membekukan matriks peran×kapabilitas; API kelola peran
existing (`PATCH /v1/admin/users/role`) diperluas aditif; audit mencatat kapabilitas yang dipakai.
**Keputusan bentuk (terkunci):** kapabilitas-di-kode dengan satu titik kebijakan — BUKAN tabel
RBAC dinamis di DB (runner-up ditolak: over-engineering untuk staf ≤10; ditinjau ulang bila butuh
pengecualian per-user) dan BUKAN policy-engine eksternal (OPA/casbin — bobot dependensi).
**AC:** peran scholar_reviewer ada dan HANYA ia (+admin) lolos gerbang approve-klaim-sensitif
(test); tidak ada pemeriksaan `role == "..."` di luar modul policy (lint/test menegakkan); matriks
kapabilitas terdokumentasi & beku-ber-test.
**DS:** Salman bisa memberi seseorang hak "review terjemahan" tanpa menjadikannya penguasa penuh
platform.

### A-2 — Identitas mesin & token layanan ber-scope  *(P1, effort sedang)* ✅ **SELESAI 2026-07-15**

**Rationale:** A-G3/R5/R6. **Isi:** registry identitas layanan (nama, scope, kedaluwarsa ≤90 hari,
hash-at-rest, dicabut per-identitas) menggantikan `COLLAB_SERVICE_TOKEN` tunggal — konsumen awal:
collab-server (scope tulis-draft), runner eval (scope baca), otomasi enrichment ber-HTTP, dan
**lapisan inferensi F7** (scope admin-prompt/registry — dicatat untuk U-0); audit membedakan
principal; **formalisasi jalur DB**: peran DB terpisah ber-grant sempit untuk pipeline ekstraksi
(mengeksekusi W-D2: hanya tulis tabel kelas-pending) dan untuk importer (terpisah dari kredensial
app); rotasi token layanan = runbook + dua-token-overlap (tanpa downtime).
**Blast radius:** collab-server perlu konfigurasi token baru (overlap membuat migrasi mulus);
skrip Python ganti kredensial DB.
**AC:** token collab yang dicabut berhenti bekerja tanpa restart app; pipeline ekstraksi secara
teknis TIDAK BISA meng-update status review (dibuktikan test grants — memenuhi AC W-0); setiap
panggilan `/internal/*` ter-audit dengan nama principal.
**DS:** kalau satu kunci mesin bocor, yang jatuh cuma satu pintu kecil — dan kelihatan siapa yang
memakainya.

**Bukti implementasi:** registry menyimpan principal bernama, scope kanonik, banyak credential,
expiry maksimal 90 hari, dan digest SHA-256 tanpa kolom plaintext. Verifier membaca DB pada setiap
request; test satu proses membuktikan T1+T2 sama-sama 200, lalu revoke T1 langsung menghasilkan 401
tanpa restart sementara T2 tetap 200. Manifest Fiber mengunci semua `/internal/*` di middleware
scope+audit fail-closed dengan retensi 90 hari. Collab hot-reload token dan pool DB secara atomik;
rag-eval serta enrichment membatasi header ke origin Surau; U-0 hanya membekukan scope
`prompt-registry:manage` dan `inference-budget:manage` sampai komponennya dibangun. Role DB
`surau_extraction_writer`, `surau_importer`, dan `surau_collab_store` adalah `NOLOGIN` dengan grant
eksplisit; login test nyata membuktikan pipeline tidak dapat mengubah status/isi reviewed,
DELETE/DDL/auth (SQLSTATE 42501), sementara smoke importer dan collab tetap berfungsi. Runbook
`docs/service-identity-rotation.md` mendokumentasikan overlap, rollback sebelum revoke, dan
rotasi login A→B tanpa downtime.

### A-3 — MFA (TOTP) + step-up untuk aksi destruktif  *(P0, effort sedang)*

**Rationale:** A-G2/R1/R7 — pertahanan tunggal terbesar. **Isi:** TOTP standar + recovery codes
(sekali pakai, hash-at-rest); WAJIB untuk admin & scholar_reviewer, opsional/atau-wajib editor
per O-2-1; **step-up** (tantangan MFA ulang / re-auth) pada aksi destruktif kelas-atas:
ganti-peran, publish/unpublish massal, hapus final-asset, kelola token layanan, ubah kebijakan;
alur enrollment + reset (recovery via kombinasi email+recovery-code; admin terakhir tetap punya
jalur CLI darurat yang sudah ada); login FE mendapat langkah OTP terdokumentasi (aditif — akun
tanpa MFA tak berubah alurnya).
**AC:** akun admin tanpa MFA terkunci dari kapabilitas kelas-atas sampai enroll (grace period
terdefinisi); aksi destruktif menolak sesi tanpa step-up segar (test); recovery-code sekali-pakai
terbukti.
**DS:** login admin Salman meminta kode dari aplikasinya — dan aksi paling berbahaya minta kode
lagi.

### A-4 — Kelincahan JWT: rotasi dual-key ber-`kid`  *(P1, effort kecil–sedang)* — ✅ **SELESAI 2026-07-16**

**Rationale:** A-G4/R2; charter G12. **Isi:** header `kid` pada token baru; verifikasi menerima
himpunan kunci aktif (lama+baru) selama jendela overlap; penerbitan pindah ke kunci baru seketika;
kunci lama pensiun setelah masa hidup token terlama; runbook + **drill rotasi tanpa-logout**
pertama sebagai bagian fase (jadwal 6-bulanan selanjutnya); secret tetap HS256 simetris
(runner-up ditolak: RS256/JWKS — penerbit tunggal & verifikasi internal semua, asimetri menambah
kompleksitas tanpa konsumen eksternal).
**AC:** drill: rotasi penuh di dev/prod tanpa satu pun sesi pengguna terputus; token ber-`kid`
lama tetap valid selama jendela; setelah jendela, kunci lama ditolak.
**DS:** "ganti kunci gedung" berubah dari renovasi menjadi rutinitas — pengguna tak merasakan
apa-apa.

**Bukti selesai:** [PR #152](https://github.com/alfariesh/surau-backend/pull/152) + perbaikan
deploy [PR #153](https://github.com/alfariesh/surau-backend/pull/153), rilis `api-v0.4.2` pada SHA
`2225b8a9427a82b3f7948b1745252fae8ef08387`. Drill dev dan prod membuktikan token no-`kid`,
old-kid, dan new-kid tetap valid selama overlap; rollback signer aman; setelah gerbang TTL+margin,
dua kelas token lama ditolak sementara new-kid dan refresh keluarga sesi yang sama tetap valid.
Snapshot **33 sesi dev** dan **35 sesi prod** tidak mengalami revoke tanpa pengganti,
`unexpected_canary_401=0`, container tidak restart, dan cleanup canary tuntas. Bukti operasional
lengkap serta jadwal drill berikutnya 2027-01-16 ada di `docs/jwt-key-rotation.md`.

### A-5 — Pengerasan refresh & sesi  *(P1, effort kecil)*

**Rationale:** A-G5/R3. **Isi:** umur refresh **720h → 336h (14 hari) sliding** (aktif memakai =
diperpanjang; diam 14 hari = login ulang — keputusan saya: keseimbangan UX mobile vs jendela
pencurian; runner-up 7 hari ditolak karena memaksa login-ulang mingguan di mobile, 720h status quo
ditolak karena jendela 30 hari); label perangkat/klien pada sesi (daftar-sesi yang ada jadi bisa
dibaca manusia: "Chrome di Mac", "Aplikasi Android"); deteksi-reuse & revoke-per-sesi existing
dipertahankan; peristiwa "login perangkat baru" tetap bernotifikasi.
**Blast radius:** pengguna yang benar-benar diam >14 hari harus login ulang (komunikasi rilis).
**AC:** refresh yang tak dipakai 14 hari ditolak; pemakaian aktif tak pernah terputus; sesi
tampil berlabel perangkat.
**DS:** daftar "perangkat yang sedang login" di akun terlihat jelas dan bisa dicabut satu-satu.

### A-6 — Alert anomali auth + ketahanan email  *(P1/P2, effort kecil)*

**Rationale:** A-G6/R8/R9; menumpang paket alert F1-B. **Isi:** ambang alert: lonjakan gagal-login
(global & per-akun), reset-password massal, pembuatan/perubahan peran tinggi, lonjakan lockout,
reuse-refresh (yang ada, diarahkan ke kanal O-F1-1); aktifkan polling event Cloudflare Email
(flag `EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED` yang sudah ada) + alert lonjakan bounce; slot
konfigurasi penyedia email cadangan (failover manual terdokumentasi — otomatisasi penuh tidak
dibayar sekarang).
**AC:** kelima alert teruji nyala lewat simulasi; bounce-surge tampil di metrik; runbook failover
email ada.
**DS:** kalau ada yang mencoba mendobrak pintu semalaman, Salman tahu paginya — bukan bulan
depannya.

---

## 5. Decisions & assumptions register

| ID | Keputusan | Rationale | Runner-up ditolak |
|---|---|---|---|
| A-D1 | Evolusi in-place; TANPA IdP eksternal/self-host | Tak ada risiko R1–R9 yang membutuhkannya; ops 1-dev; residensi data | Auth0/Keycloak/ORY (biaya+ops+overkill) |
| A-D2 | JWT dipertahankan + dibuat agile (`kid` dual-key, HS256) | Verifikasi sudah menyentuh DB per-request (statelessness sudah "terpakai"), tapi migrasi format = blast radius FE/mobile tanpa keuntungan | Token opaque (migrasi mahal, untung nol); RS256/JWKS (tanpa konsumen eksternal) |
| A-D3 | RBAC = kapabilitas-di-kode dengan SATU titik kebijakan + matriks beku-ber-test | Staf ≤10; permukaan enforcement kecil (3 titik); auditability > fleksibilitas dinamis | Tabel RBAC dinamis di DB (YAGNI); OPA/casbin (bobot dependensi) |
| A-D4 | Peran baru: `curator` & `scholar_reviewer`; admin tetap superset; scholar gate = kapabilitas khusus | Kebutuhan konkret W-0/K-9/H-2; nama selaras glosarium Fase 6 | Menumpuk semua di editor/admin |
| A-D5 | MFA = TOTP + recovery codes; TANPA SMS | SMS = biaya + SIM-swap risk; TOTP standar & offline | SMS OTP; email-OTP-sebagai-MFA (bukan faktor kedua sejati) |
| A-D6 | Identitas mesin dua jalur: token HTTP ber-scope utk layanan; peran-DB ber-grant utk penulis massal (pipeline, importer) | Selaras W-D2; bulk-write via HTTP = overhead tanpa nilai | Semua via HTTP (memperlambat ekstraksi); status quo satu token + PG_URL |
| A-D7 | Refresh 336h sliding | Kompromi UX-mobile vs jendela pencurian; sliding menghukum token diam saja | 720h tetap (jendela 30 hari); 168h (friksi mingguan) |
| A-D8 | Step-up berbasis MFA segar utk aksi destruktif kelas-atas | Sesi panjang tak boleh setara niat segar utk aksi tak-terbalikkan | Password-ulang saja (phishable sekali) |
| A-D9 | Verifikasi token membaca registry DB setiap request tanpa positive cache; audit `/internal/*` disimpan 90 hari dan fail-closed sebelum handler | Revoke/expiry harus berlaku pada proses hidup dan operasi internal tanpa audit tidak boleh lolos | Cache token positif (membuat jendela revoke); audit best-effort (kehilangan atribusi) |
| A-D10 | Role DB A-2 adalah group `NOLOGIN` terpisah untuk extraction, importer, dan collab; login per-environment hanya satu membership dan expiry ≤90 hari | Memperkecil blast radius setiap consumer serta memungkinkan rotasi pool A/B tanpa mengubah ownership schema | Satu role bersama; kredensial owner; role LOGIN dari migrasi |
| A-D11 | Scope U-0 dibekukan sekarang, tetapi principal/token tidak diterbitkan sebelum U-0 dibangun | Kontrak lintas-fase stabil tanpa secret menganggur yang dapat bocor | Menerbitkan token sekarang; menunda penamaan scope sampai U-0 |

**Asumsi:** A-A1 — FE/mobile sanggup menambah alur TOTP & step-up dalam jendela rilis normal
(alur lama tak pecah); A-A2 — daftar aksi destruktif kelas-atas final ditetapkan bersama matriks
kapabilitas A-1; A-A3 — kanal alert O-F1-1 sudah terjawab (default email berlaku).

> **Conflicts with charter: tidak ada.** G12 charter (MFA admin, dual-key JWT) dieksekusi di sini.
> Nota keselarasan (bukan konflik): mandat misi "service token ber-scope untuk pipeline Python"
> dieksekusi sebagai **dua jalur** sesuai keputusan W-D2 yang lebih dulu terkunci — penulis massal
> memakai peran-DB ber-grant sempit (bukan HTTP token), otomasi ber-HTTP memakai token ber-scope;
> AC W-0 ("transisi status mustahil dari pipeline") tetap terpenuhi persis.

---

## 6. Interfaces (seams)

**Fase 2 MENGEKSPOS:** titik kebijakan kapabilitas + matriks peran (dipakai editorial F3/F4,
kurasi F6 — gerbang scholar_reviewer W-0 berdiri di sini, promosi reviewed K-9, admin email);
identitas mesin ber-scope (collab, otomasi, runner eval, **lapisan inferensi F7/U-0**: scope
kelola prompt-registry & budget); jaminan MFA/step-up untuk aksi kelas-atas semua fase; peran-DB
ber-grant untuk pipeline/importer (memenuhi AC W-0); alur auth FE yang sudah terdokumentasi +
tambahan MFA.

**Fase 2 MENGONSUMSI:** F1-B (metrik/alert), F1-F (runbook rotasi & gitleaks), kanal O-F1-1;
kebutuhan peran dari F4 (K-9), F6 (W-0/O-6-1); tidak bergantung pada fase konten mana pun.

---

## 7. Open decisions (operator-owned)

**O-2-1 — Cakupan wajib MFA.**
*Kenapa penting:* keamanan vs friksi orang nyata yang kamu rekrut. *Opsi:* (a) **wajib untuk
admin & scholar_reviewer; opsional untuk editor/curator** — perlindungan inti tanpa menghambat
kontributor; (b) wajib untuk semua peran ber-kuasa termasuk editor; (c) wajib admin saja.
*Rekomendasi:* (a), naik ke (b) saat tim kurasi membesar. *Default aman:* (a).

**O-2-2 — Delegasi kuasa publish.**
*Kenapa penting:* hari ini publish/unpublish = admin-only; saat produksi konten tumbuh, ini jadi
bottleneck — tapi publish adalah kuasa yang salah pakai-nya paling terlihat publik. *Opsi:*
(a) **tetap admin-only sampai A-1+A-3 mendarat dan audit berjalan ≥1 bulan**, lalu delegasikan
kapabilitas publish ke `curator` terpilih; (b) delegasikan segera setelah A-1; (c) selamanya
admin-only. *Rekomendasi:* (a). *Default aman:* (a).

---

## 8. Conformance

Fase ini tidak menyentuh ayat/makna — tetapi ia adalah **penegak** Domain Integrity di lapisan
manusia: gerbang scholar_reviewer (A-1) adalah mekanisme yang membuat "klaim sensitif hanya lewat
review ulama" (W-0/W-D9) berlaku secara teknis, bukan sebagai niat; MFA+step-up (A-3) melindungi
integritas konten agama dari skenario pengambilalihan yang paling merusak (penyuntingan halus);
audit ber-kapabilitas memastikan setiap keputusan editorial/kurasi punya aktor yang bisa
dipertanggungjawabkan; dan identitas mesin ber-scope (A-2) menjamin pipeline otomatis tidak pernah
bisa menyetujui apa pun atas nama manusia.

## 9. North-star fit

Wiki yang mengklaim setiap kalimat punya pemilik harus bisa menjamin hal yang lebih dasar: setiap
KEPUTUSAN punya pemilik yang terbukti dirinya (MFA), berwenang secukupnya (kapabilitas), dan
tercatat (audit) — baik ia manusia maupun mesin. Fase ini murah dibanding fase konten, berjalan
paralel, dan tanpa satu inisiatifnya (A-1) gerbang paling sakral platform — review ulama atas
klaim sensitif — hanyalah komentar di kode.
