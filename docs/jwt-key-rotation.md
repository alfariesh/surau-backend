# Runbook rotasi kunci JWT HS256 tanpa logout

Runbook ini menjalankan A-4: token baru membawa `kid` (ID kunci), penerbit
berpindah ke kunci baru seketika, dan backend serta Cloudflare Worker menerima
kunci lama+baru selama overlap. Kunci lama baru dibuang setelah semua access
token lama pasti habis masa hidupnya.

Jalur operator adalah workflow GitHub Actions **JWT Key Rotation**. Workflow
[`.github/workflows/jwt-rotation.yml`](../.github/workflows/jwt-rotation.yml) dan
runner [`ops/auth/rotate-jwt-keyset.sh`](../ops/auth/rotate-jwt-keyset.sh) adalah
sumber kebenaran. Jangan mengedit keyset, metadata rotasi, token canary, atau
secret Worker dengan tangan.

Kontrak API login/refresh tidak berubah dan aplikasi web/mobile tetap
memperlakukan token sebagai string opaque. Token ber-`kid` hanya dicocokkan ke
kunci dengan ID persis itu dan algoritma HS256; ID asing atau signature salah
tidak mencoba kunci lain. Token tanpa `kid` hanya boleh memakai `legacy_kid`.

## Kapan drill dinyatakan lulus

Satu lingkungan lulus hanya bila bukti tersanitasi menunjukkan semua hal ini:

1. Setelah aktivasi, refresh berikutnya langsung menghasilkan token
   `kid=<new_kid>`.
2. Selama overlap, token `kid=lama`, token `kid=baru`, dan pada drill pertama
   token hidup tanpa `kid` tetap valid. Di backend statusnya `200`; pada Worker
   produksi identitasnya tetap `user`.
3. Gerbang waktu menolak pensiun dini. Setelah gerbang lewat dan `retire`
   selesai, token lama dan token tanpa `kid` mendapat `401`, token baru tetap
   valid, dan refresh dari keluarga sesi yang sama tetap `200`.
4. ID container, `StartedAt`, dan `RestartCount` tidak berubah sejak baseline
   rotasi. Artinya perpindahan kunci memakai hot reload `SIGHUP`, bukan restart.
5. Keluarga sesi canary tetap aktif tepat satu, jumlah sesi aktif canary tidak
   berubah, tidak ada sesi lama yang dicabut tanpa pengganti, dan tidak ada
   `401` canary yang tidak diharapkan.
6. Canary non-admin sementara terhapus setelah `retire` sukses. Bukti tidak
   memuat token, password, material kunci, email canary, atau dump environment.

Restart normal saat **rilis kode A-4 pertama** bukan bagian dari rotasi kunci.
Baseline container diambil setelah A-4 sehat; sejak `start` sampai `retire`,
container itu harus tetap sama.

## Perbedaan dev dan prod

| Lingkungan | Jalur verifikasi | Perlakuan Worker | Drill pertama |
|---|---|---|---|
| `dev` | Origin/backend langsung di `dev-api.surau.org` | Tidak ada route Worker dev. Jangan pernah unggah keyset dev ke Worker produksi. Matriks Worker dibuktikan oleh test otomatis. | Deploy A-4 menangkap token no-`kid`, lalu otomatis menjalankan prepare dan activation drill setelah app sehat. Operator melanjutkan dengan `status`, menunggu gerbang, lalu `retire`. |
| `prod` | Origin lokal VPS dan API publik `api.surau.org` | Workflow memasang verifier dual-key sebelum aktivasi, lalu single-key sebelum backend final reload. Live smoke memakai header operasional `X-Surau-JWT-Identity`. | Deploy A-4 hanya menangkap token no-`kid`. Setelah deploy sehat, operator menjalankan `start`, lalu `status` dan `retire`. |

Urutan wajib adalah dev sampai lulus, soak dan tinjau bukti, baru prod dengan
release yang sama.

## Perlindungan akses sebelum mulai

- GitHub Environment `dev` dan `prod` harus aktif. `prod` wajib memakai required
  reviewer dan deployment policy yang hanya mengizinkan branch `main` (workflow
  rotasi) serta tag rilis `api-v*` (workflow deploy prod). Job rotasi sendiri
  tetap menolak dispatch selain dari `main`.
- Secret VPS harus berada di Environment masing-masing. Kredensial Cloudflare
  hanya ada di Environment `prod`: `Workers Scripts:Edit` pada account yang
  menjalankan `surau-api-cache`, ditambah `Workers Routes:Edit` hanya untuk zona
  `surau.org`.
- Jangan memakai token Cloudflare global, akun manusia, atau keyset dari
  lingkungan lain. Salinan keyset Worker hanya hidup sementara di runner dan
  harus dihancurkan setelah pemakaian.
- Pastikan tidak ada deploy atau rotasi JWT lain yang berjalan. Workflow memakai
  concurrency lock per lingkungan dan tidak membatalkan run yang sedang aktif.
- `HTTP_USE_PREFORK_MODE=false`, `/version` tepat sama dengan input
  `expected_version`, serta `/healthz` dan `/readyz` harus sehat. Salah satu
  ketidaksesuaian menghentikan aksi sebelum mutasi berikutnya.
- Image A-4 harus sudah lulus test Go, test lifecycle shell, test Worker, dan
  typecheck. Untuk prod, release hanya dibuat setelah commit yang sama lolos di
  dev. Workflow prod juga menolak `start` bila tidak menemukan artifact
  retirement dev yang masih tersimpan dan berasal dari run sukses.

## Upgrade pertama: kompatibilitas token tanpa `kid`

Ini terjadi satu kali per lingkungan dan otomatis menjadi bagian deploy A-4:

1. Image A-4 dibangun saat app lama masih melayani pengguna.
2. `bootstrap-jwt-keyset.sh` membandingkan `JWT_SECRET` di dotenv dengan signer
   yang benar-benar hidup, tanpa mencetak nilai atau hash. Nilai yang sama
   dimasukkan sebagai kunci aktif dan `legacy_kid`.
3. Fallback lama untuk MFA dan tautan unsubscribe dipin ke konfigurasi mandiri
   agar keduanya tidak ikut berubah saat `JWT_SECRET` JWT dihapus.
4. Tepat sebelum container lama diganti, deploy menjalankan `capture-legacy`.
   Runner membuat user canary non-admin sementara, memverifikasinya secara
   lokal, lalu login ke binary lama. Token harus masih hidup dan headernya harus
   tidak mempunyai `kid`; bila tidak, deploy berhenti.
5. Setelah app A-4 sehat, keyset awal membuat token baru memakai `kid` lama,
   tetapi token hidup tanpa `kid` masih diverifikasi melalui `legacy_kid`.

Capture dilakukan sedekat mungkin dengan cutover agar sisa umur token cukup
untuk bukti overlap. Aksi `capture-legacy` tersedia di workflow untuk recovery
terkendali, tetapi bukan langkah rutin. State no-`kid` yang masih hidup dapat
dilanjutkan idempoten; bila tokennya sudah habis, recapture hanya diizinkan saat
keyset masih `stable` dan signer hidup terbukti masih menerbitkan token tanpa
`kid`. Signer yang sudah menerbitkan `kid` selalu ditolak.

Setelah pensiun pertama, `legacy_kid` hilang permanen. Rotasi berikutnya tidak
menghidupkan kompatibilitas no-`kid` lagi dan tidak memerlukan capture legacy.

## Menjalankan workflow

Buka **Actions → JWT Key Rotation → Run workflow**, pilih branch `main`, lalu
isi:

- `environment`: `dev` atau `prod`;
- `action`: salah satu aksi pada tabel berikut;
- `expected_version`: nilai `version` yang persis dari endpoint `/version`,
  misalnya `dev-abcdef0` atau `0.5.0`.

| Aksi | Kapan dipakai | Hasil aman |
|---|---|---|
| `start` | Memulai rotasi rutin; pada drill pertama prod, jalankan segera setelah deploy A-4 sehat. Drill pertama dev sudah menjalankannya dari deploy. | Membuat kunci baru, memasang verifier, memindahkan penerbit, menguji rollback, lalu mengaktifkan kunci baru lagi dengan overlap penuh. |
| `status` | Sebelum tindakan, setelah `start`, saat menunggu, setelah kegagalan, dan setelah `retire`. | Hanya membaca ID/state/timestamp tersanitasi, memeriksa container dan sesi canary, lalu membuat artifact bukti 90 hari. |
| `rollback` | Hanya bila ada anomali sebelum `retire_not_before`. | Penerbit kembali ke kunci lama, tetapi kedua verifier tetap aktif sehingga token baru tidak terputus. Prod juga mempertahankan Worker dual-key. |
| `retire` | Hanya setelah `retirement_due=true` dan bukti overlap ditinjau. | Menyiapkan file single-key, memperbarui Worker prod, baru hot-reload backend; membuktikan penolakan token lama dan kontinuitas refresh, lalu membersihkan canary. |
| `capture-legacy` | Recovery khusus pada upgrade pertama, sebelum binary lama diganti. | Menangkap satu sesi canary no-`kid`; tidak boleh dipakai untuk rotasi rutin. |

Contoh ekuivalen melalui GitHub CLI, tanpa rahasia di argumen:

```bash
gh workflow run jwt-rotation.yml --ref main \
  -f environment=dev -f action=status -f expected_version='dev-abcdef0'
```

Jangan menebak versi. Baca `/version` target terlebih dahulu, lalu cocokkan lagi
dengan artifact workflow.

## Urutan drill pertama

### Dev

1. Merge A-4 ke `main` dan tunggu deploy dev sehat. Deploy menangkap no-`kid`
   serta menjalankan `prepare` + `activate-drill` otomatis.
2. Jalankan `status` dengan versi dev yang tepat. Unduh artifact tersanitasi dan
   pastikan state `active`, dua key ID ada, `active_kid` adalah kunci baru, serta
   `retire_not_before` tercatat.
3. Tinjau bukti overlap dan nol gangguan pada bagian di bawah. Jangan menyentuh
   prod bila satu gate dev belum lulus.
4. Tunggu sampai `status` menunjukkan `retirement_due=true`, lalu jalankan
   `retire`. Jangan memperpendek waktu tunggu.
5. Jalankan `status` lagi. State final harus `retired`, hanya satu key ID, tanpa
   `legacy_kid`, sesi canary sudah dibuktikan kontinu, dan cleanup sukses.

### Prod

1. Setelah dev lulus dan soak, rilis tag A-4. Deploy prod menangkap token no-`kid`
   tepat sebelum cutover dan tidak mengaktifkan signer baru.
2. Segera setelah `/version`, `/healthz`, dan `/readyz` prod sehat, jalankan
   `start`. Workflow menaruh keyset dual di Worker **sebelum** backend menerbitkan
   token berkunci baru.
3. Jalankan `status` dan tinjau artifact `start`. Pastikan live smoke Worker
   membaca token lama/no-`kid` dan token baru sebagai `user` selama overlap.
4. Tunggu `retirement_due=true`. Jalankan `status` kapan pun tanpa mengubah
   state; jangan menjalankan deploy lain selama jendela ini.
5. Jalankan `retire`. Workflow mengubah Worker menjadi single-key terlebih
   dahulu, membuktikan token lama menjadi `guest` di Worker, lalu me-reload
   backend dan membuktikan keduanya `401` di origin. Refresh canary yang sama
   harus tetap `200` dengan `kid` baru.
6. Jalankan `status` terakhir dan isi bukti prod. Hanya setelah itu A-4 boleh
   ditandai selesai.

Retirement final juga mengosongkan fallback JWT lama di backend dan menghapus
secret `JWT_SECRET` Worker. Kunci MFA dan unsubscribe yang sudah dipin tetap
berdiri sendiri, jadi alur itu tidak ikut berubah.

Untuk rotasi rutin enam bulanan, jalankan `start → status → retire → status` di
dev, tinjau bukti, lalu ulangi di prod. Tidak ada lagi capture no-`kid`.

## Cara gerbang overlap dihitung

Runner tidak bergantung pada tebakan operator. Saat aktivasi, ia menghitung:

```text
overlap = max(
  JWT_ACCESS_TOKEN_EXPIRY yang dikonfigurasi,
  exp - iat token no-kid yang benar-benar ditangkap,
  exp - iat token old-kid yang benar-benar diterbitkan
) + 5 menit
```

Nilai token aktual melindungi upgrade bila konfigurasi binary lama berbeda dari
binary A-4. Tambahan lima menit menutup selisih jam dan token yang terbit di
batas cutover. Jika TTL tidak valid, token tidak dapat didekode dengan aman,
atau nilainya di luar batas 24 jam aplikasi, runner memakai fallback `24h5m`.

CLI mencatat `retire_not_before` secara atomik dan menolak `retire` sebelum
waktu itu. Aktivasi ulang setelah rollback memberi jendela penuh baru agar token
yang terbit selama rollback tetap terlindungi.

## Bukti otomatis Acceptance Criterion A-4

`start` tidak sekadar mengganti konfigurasi. Ia menjalankan matriks berikut:

1. Siapkan kunci baru tanpa mengubah penerbit; token no-`kid` dan old-kid tetap
   `200`.
2. Pasang kedua verifier di Worker prod, baru aktifkan signer baru dan hot reload
   backend.
3. Buktikan no-`kid`, old-kid, dan new-kid diterima selama overlap.
4. Coba pensiun dini dan wajib mendapat penolakan dari CLI.
5. Rollback penerbit ke kunci lama sambil mempertahankan kedua verifier; buktikan
   token new-kid yang telanjur terbit tetap valid dan refresh menerbitkan
   old-kid.
6. Aktifkan kembali kunci baru dengan overlap penuh; buktikan old-kid tetap valid
   dan refresh langsung menerbitkan new-kid.

`retire` menjalankan matriks final:

| Probe | Selama overlap | Setelah retire |
|---|---:|---:|
| Token no-`kid` upgrade pertama | backend `200`; Worker prod `user` | backend `401`; Worker prod `guest` |
| Token `kid=lama` | backend `200`; Worker prod `user` | backend `401`; Worker prod `guest` |
| Token `kid=baru` | backend `200`; Worker prod `user` | backend `200`; Worker prod `user` |
| Refresh canary, keluarga sesi sama | `200` | `200` |

Pada waktu retire, token operasional lama memang seharusnya sudah expired. Agar
penolakan tidak hanya terbukti karena expiry, CI juga membuat token lama yang
masih unexpired, membuang verifier lamanya, lalu membuktikan token itu ditolak
sementara token baru tetap valid. Bukti deterministik berasal dari:

```bash
go test ./pkg/jwt ./cmd/jwt-keyset
bash ops/auth/test-rotate-jwt-keyset.sh
npm --prefix workers/api-cache test
npm --prefix workers/api-cache run typecheck
```

Dev melewati live smoke Worker karena route yang tersedia hanya produksi. Test
Worker yang sama tetap wajib hijau; ini membuktikan lookup `kid` persis,
kompatibilitas no-`kid` selama overlap, penolakan setelah retire, dan fail-closed
saat `JWT_KEYSET` invalid.

## Bukti nol sesi terputus

Runner membuat canary lewat jalur registrasi publik, memastikan role-nya `user`
dan bukan admin, lalu menyimpan state rahasia hanya di direktori root pada VPS.
Setiap refresh token bersifat single-use; successor disimpan atomik segera
setelah dipakai agar retry tidak pernah memakai token lama lagi.

Pada setiap fase, runner wajib membuktikan:

- satu keluarga sesi canary tetap aktif tepat satu dan `session_id` tetap sama;
- total sesi aktif yang terlihat canary tetap sama;
- snapshot sesi pengguna lain tidak mempunyai pencabutan baru tanpa pengganti;
- `unexpected_canary_401=0`; dua `401` token pensiun adalah expected probe;
- container ID dan `StartedAt` sama, serta delta `RestartCount=0`;
- log reload memuat persis `active_kid`, `legacy_kid`, dan jumlah kunci yang
  diharapkan; reload invalid mempertahankan snapshot valid terakhir.

Sebelum menyatakan prod lulus, cocokkan jendela waktu workflow dengan metrik
`401` produksi. Setiap lonjakan yang tidak dapat dijelaskan menahan keputusan
drill. Jangan menyebut “nol logout” hanya dari satu curl.

Canary dan state credential dihapus otomatis hanya setelah retirement runtime
selesai. Jika run gagal, state sengaja dipertahankan agar recovery aman; jangan
menghapusnya untuk memulai ulang.

## Rollback dan recovery aman

Mulai selalu dengan aksi `status`, lalu ikuti tabel ini:

| Keadaan | Tindakan aman |
|---|---|
| `prepare` atau deploy Worker gagal sebelum aktivasi | Pengguna masih memakai signer lama. Perbaiki penyebab dan ulangi `start` dengan versi yang sama. |
| Anomali setelah aktivasi, sebelum `retire_not_before` | Jalankan `rollback`. Kedua verifier tetap ada, jadi token baru tetap valid. Setelah penyebab selesai, ulangi `start`; gerbang overlap dimulai penuh lagi. |
| Reload ditolak | Runtime mempertahankan snapshot terakhir yang valid. Jangan restart atau edit JSON; perbaiki mount/permission melalui kode otomasi, lalu ulangi aksi yang sama. |
| Retry menemukan state `prepared`, `active`, atau `rolled_back` | Runner melanjutkan state tersimpan dan tidak membuat kunci ketiga. Gunakan aksi yang sama; jangan hapus sidecar. |
| `start` diulang ketika gerbang sudah lewat | Runner menolak rollback drill terlambat. Lanjutkan maju dengan `retire`, bukan menghidupkan kunci lama. |
| File retirement sudah dibuat tetapi Worker/update final gagal | Runtime backend tetap dual-key sampai final reload. Perbaiki penyebab dan ulangi `retire`; operasi file dan CLI idempotent. |
| Cleanup terputus setelah retirement | Jalankan `status`, lalu ulangi `retire` untuk menuntaskan verifikasi dan cleanup. Jangan hapus user/state secara manual. |
| ID container, `StartedAt`, atau `RestartCount` berubah | Hentikan dan investigasi deploy, OOM, atau restart. Bukti hot reload batal dan harus diulang dari baseline A-4 baru. |

Larangan keras: jangan rollback ke image sebelum A-4 ketika dua kunci aktif.
Binary lama hanya mengenal satu `JWT_SECRET` dan akan menolak token kunci baru.
Jangan menghapus kunci baru dari Worker saat rollback. Setelah gerbang lewat,
selesaikan rotasi ke depan; jangan mendaur ulang kunci lama.

Jika ada dugaan kunci bocor, gunakan prosedur respons insiden. Keselamatan dapat
memerlukan pencabutan segera dan logout terkontrol; jangan melemahkan guard CLI
demi mempertahankan janji zero-logout.

## Bukti yang boleh disimpan

Artifact workflow boleh berisi SHA/release, `/version`, ID `kid`, state dan waktu
rotasi, status probe, angka sesi agregat, serta pernyataan container tidak
berubah. Jangan simpan token, refresh token, password, email canary, keyset,
`JWT_SECRET`, secret Cloudflare, hash rahasia, atau output `docker inspect` penuh.

### Drill dev pertama

| Bukti | Nilai aktual |
|---|---|
| Deploy run, SHA, `/version` | [run 29477348259](https://github.com/alfariesh/surau-backend/actions/runs/29477348259) sukses; SHA `2225b8a9427a82b3f7948b1745252fae8ef08387`; publik `dev-2225b8a`; `/healthz` dan `/readyz` `OK`. |
| Capture no-`kid` tepat sebelum cutover | `legacy-recaptured` 2026-07-16 06:50:04 UTC dari binary lama `dev-3ee4919`; probe no-`kid` `200`. |
| `start`/activation otomatis dan artifact `status` | Kunci `dev-20260716T065012Z` disiapkan 06:50:13 UTC dan aktif 06:50:23 UTC; [status run 29479745260](https://github.com/alfariesh/surau-backend/actions/runs/29479745260) sukses. |
| Old no-`kid` + old-kid + new-kid valid saat overlap | Ketiganya `200`; rollback penerbit ke old-kid `200`, lalu aktivasi ulang new-kid `200`. |
| `retire_not_before`, waktu retire, artifact final | Gerbang 07:10:23 UTC; dipensiunkan 07:25:00 UTC melalui [run 29479818273](https://github.com/alfariesh/surau-backend/actions/runs/29479818273); [status final 29479895821](https://github.com/alfariesh/surau-backend/actions/runs/29479895821) menunjukkan `retired` dan hanya satu kunci. |
| Old no-`kid` + old-kid `401`; new-kid + refresh `200` | Dua token lama masing-masing `401`; new-kid `200`; keluarga refresh canary tetap aktif dan menerbitkan token new-kid. |
| Container ID/StartedAt sama; delta restart `0` | `container_unchanged=true` dan `container_restarts=0` dari prepare sampai retirement final. |
| Canary aktif `1`; total sesi canary tetap; snapshot sesi lain; unexpected `401=0`; revoke tanpa pengganti `0` | Keluarga canary aktif; snapshot **33** sesi pengguna lain; `unexpected_canary_401=0`; `unreplaced_session_revocations=0`; cleanup akhir `canary_users=0`. |
| Review metrik/log `401` dev | Log memuat tepat dua `401` yang memang diharapkan untuk probe retirement; `unexpected_canary_401=0`. Metrik HTTP saat review tidak mempunyai seri `401`, sehingga keputusan memakai pasangan bukti log bernomor request-ID + guard canary, bukan mengklaim metrik yang tidak ada. |
| Test deterministik backend/CLI/Worker | `make pre-commit`, integration, Go JWT/keyset, lifecycle shell, Worker **30/30**, typecheck, shellcheck, actionlint, yamllint, dan gitleaks hijau pada SHA rilis. |
| Keputusan | **LULUS 2026-07-16** — token lama hidup selama overlap lalu ditolak setelah gerbang, tanpa sesi terputus. |

### Drill prod pertama

| Bukti | Nilai aktual |
|---|---|
| Release/tag/SHA, deploy run, `/version` | [release `api-v0.4.2`](https://github.com/alfariesh/surau-backend/releases/tag/api-v0.4.2), SHA `2225b8a9427a82b3f7948b1745252fae8ef08387`; [deploy run 29480067927](https://github.com/alfariesh/surau-backend/actions/runs/29480067927) sukses; publik `0.4.2`; `/healthz` dan `/readyz` `OK`. |
| Capture no-`kid` tepat sebelum cutover | `legacy-captured` 2026-07-16 07:34:49 UTC dari binary lama `0.4.1`; probe no-`kid` `200`. |
| Environment approval + kredensial Worker least-privilege | Setiap aksi prod melewati required reviewer GitHub Environment. Token Cloudflare: account `Workers Scripts:Edit` + zona `surau.org` saja `Workers Routes:Edit`; SSH memakai host key terpin. |
| `start` run + receipt Worker dual-key | [run 29480973809](https://github.com/alfariesh/surau-backend/actions/runs/29480973809) sukses; Worker `surau-api-cache` terunggah dan route terpasang, receipt Version ID `426d0f1d-ea12-4792-97e2-a23c6af4b071`; signer baru aktif sesudahnya. |
| Old no-`kid` + old-kid + new-kid valid di origin dan Worker saat overlap | Ketiganya `200` di origin/public route dan beridentitas `user` di Worker; rollback old-kid dan aktivasi ulang new-kid juga lulus. [Status 29481125565](https://github.com/alfariesh/surau-backend/actions/runs/29481125565) tetap `active`. |
| `retire_not_before`, waktu retire, receipt Worker single-key | [Status gerbang 29482132527](https://github.com/alfariesh/surau-backend/actions/runs/29482132527) membuktikan `retirement_due=true` setelah 08:06:16 UTC. [Retire 29482329916](https://github.com/alfariesh/surau-backend/actions/runs/29482329916) mengunggah `JWT_KEYSET` single-key, menghapus fallback `JWT_SECRET` Worker, lalu memensiunkan backend pada 08:08:09 UTC. |
| Old no-`kid` + old-kid ditolak; new-kid + refresh tetap valid | Token no-`kid` dan old-kid masing-masing `401` di origin dan menjadi `guest` di Worker; new-kid `200`/`user`; refresh keluarga sesi yang sama `200` dan menerbitkan new-kid. |
| Container ID/StartedAt sama; delta restart `0` | `container_unchanged=true` dan `container_restarts=0` dari prepare sampai retirement final; hot reload final mencatat satu kunci dan `legacy_kid` kosong. |
| Canary aktif `1`; total sesi canary tetap; snapshot sesi lain; unexpected `401=0`; revoke tanpa pengganti `0` | Keluarga canary aktif sepanjang pembuktian; snapshot **35** sesi pengguna lain; `unexpected_canary_401=0`; `unreplaced_session_revocations=0`. |
| Review metrik `401` produksi | Selama overlap log mencatat **0** `401`. Retirement menghasilkan tepat **4** `401` yang diharapkan: dua probe origin dan dua probe public route; sesudah probe kembali **0**. Prometheus tidak mengekspor seri `status_code=401`, sehingga bukti keputusan adalah empat request-ID log + guard canary, tanpa mengklaim seri metrik yang tidak tersedia. |
| Cleanup canary dan secret sementara | [Status final 29482446425](https://github.com/alfariesh/surau-backend/actions/runs/29482446425) menunjukkan `retired` dan satu kunci; state credential remote `removed`; `canary_users=0`; export Worker/runner sementara dihapus; fallback Worker lama terhapus. |
| Keputusan dan `next_due` enam bulan | **LULUS 2026-07-16** — nol sesi pengguna terputus; jadwal berikutnya paling lambat **2027-01-16**. |

Prod hanya boleh ditandai lulus setelah seluruh kolom mempunyai bukti nyata dan
drill dev untuk release yang sama sudah lulus.

### Catatan kegagalan aman drill pertama

- [Deploy dev 29476104945](https://github.com/alfariesh/surau-backend/actions/runs/29476104945)
  berhenti setelah capture karena proses `psql` mengambil stdin SSH; versi publik
  tetap binary lama sehingga belum ada cutover. [PR #153](https://github.com/alfariesh/surau-backend/pull/153)
  menambahkan regresi test dan memperbaiki aliran stdin sebelum drill diulang.
- [Start prod 29480455483](https://github.com/alfariesh/surau-backend/actions/runs/29480455483)
  gagal saat route Worker belum berizin. Bukti log menyatakan signer tetap kunci
  lama; state hanya `prepared`. Setelah token Cloudflare diberi
  `Workers Routes:Edit` khusus `surau.org`, retry memakai `new_kid` yang sama dan
  baru mengaktifkan signer sesudah Worker berhasil. Tidak ada logout pada kedua
  kegagalan aman ini.
