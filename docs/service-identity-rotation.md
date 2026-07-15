# Runbook rotasi identitas layanan dan role database

Runbook A-2 memakai overlap A/B: credential baru dibuktikan dahulu, konsumen
dipindahkan, pekerjaan lama dibiarkan selesai, baru credential lama dicabut.
Tidak ada operasi “unrevoke”; bila pencabutan salah, terbitkan credential baru.

## Aturan aman

- Secret tidak masuk Git, migration, argv, shell history, atau log. Gunakan
  direktori root-owned mode `0700` dan file mode `0600`.
- Token maksimum 90 hari; standar operasional 30 hari. Role login PostgreSQL
  memakai `VALID UNTIL` maksimum 90 hari dan hanya satu group role.
- Jangan revoke A sebelum B terbukti lewat smoke dan audit. Rollback sebelum
  revoke adalah mengembalikan file/config ke A; setelah revoke, buat C.
- Jangan membuat login password di migration. Migration hanya membuat group
  `NOLOGIN`: `surau_extraction_writer`, `surau_importer`, dan
  `surau_collab_store`.

## Menerbitkan dan memutar token HTTP

1. GET principal melalui API admin dan simpan ETag. Terbitkan T2 dengan
   `If-Match` dan MFA segar. Arahkan respons sekali-pakai ke file operator mode
   `0600`, ambil field `credential.token`, lalu hapus respons mentah setelah
   secret masuk secret store. Jangan mencetak respons ke terminal/CI.
2. Sebelum cutover, panggil permukaan konsumen dengan T1 dan T2. Keduanya harus
   sukses dan audit harus memperlihatkan principal serta token ID yang benar.
3. Ubah secret store/file konsumen ke T2 secara atomik. Job eval/enrichment baru
   memakai T2; job lama dengan T1 dibiarkan selesai. Setelah audit hanya
   memperlihatkan T2, revoke T1 melalui endpoint per-token.
4. Buktikan T1 langsung 401, T2 tetap sukses, dan catat audit. Hapus file
   respons penerbitan serta konfigurasi fallback lama.

### Drill collab tanpa restart

Catat sebelum cutover:

```sh
docker inspect -f '{{.Id}} {{.State.StartedAt}}' app collab
```

Buat satu draft aktif dan catat checksum/`updated_at`-nya. Tulis T2 ke file
sementara mode `0600`, lalu rename atomik menjadi file
`COLLAB_SERVICE_TOKEN_FILE`. `collab-server` membaca ulang pada request
berikutnya dan hanya menukar token setelah
`GET /internal/collab/whoami` mengembalikan `collab-server` dengan
`collab:draft:write`. Kandidat gagal mempertahankan T1.

Setelah T2 tampil di audit, revoke T1. Tanpa membuat ulang container, buktikan:

- request `whoami` dengan T1 mendapat 401;
- request dengan T2 mendapat 200;
- output `docker inspect` ID dan `StartedAt` app+collab tidak berubah;
- draft aktif tetap tersimpan setelah satu edit dan reload.

Health collab memeriksa PostgreSQL dan `whoami`, bukan sekadar port terbuka.

## Membuat login role PostgreSQL A/B

Masuk ke `psql` melalui kanal operator. Ganti nama bertanggal sesuai lingkungan:

```sql
CREATE ROLE surau_extraction_202607_b
  LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
  VALID UNTIL '2026-08-14 00:00:00+00';
GRANT surau_extraction_writer TO surau_extraction_202607_b;
```

Set password dengan meta-command interaktif agar tidak masuk argv/log:

```text
\password surau_extraction_202607_b
```

Gunakan pola yang sama untuk satu membership saja:

| Login environment | Group tunggal | Secret konsumen |
|---|---|---|
| `surau_extraction_<date>_{a,b}` | `surau_extraction_writer` | `LANGEXTRACT_PG_URL` |
| `surau_importer_<date>_{a,b}` | `surau_importer` | `IMPORTER_PG_URL` |
| `surau_collab_<date>_{a,b}` | `surau_collab_store` | `COLLAB_PG_URL_FILE` |

Pastikan `rolsuper=false`, `rolcreaterole=false`, `rolbypassrls=false`, expiry
≤90 hari, dan membership hanya group yang dituju. Jangan memberi ownership,
grant option, default privilege, `GRANT ALL`, atau grant seluruh tabel schema.

## Cutover role database

### Extraction dan importer

1. Buat login B, jalankan smoke dengan B, lalu arahkan job baru ke dedicated
   URL B. Pipeline wajib membuktikan insert default `pending` dan penolakan
   SQLSTATE `42501` untuk status approved, UPDATE status, rewrite row reviewed,
   DELETE, DDL, serta akses `users/auth_*`.
2. Importer wajib menjalankan Shamela staged import + identical no-op, satu
   reader-asset fixture, dan fixture Quran. Uji negatif akses auth/personal dan
   DELETE.
3. Biarkan job A selesai. Setelah tidak ada sesi A, jalankan
   `ALTER ROLE <role_a> NOLOGIN`, revoke membership, lalu drop role A. Jangan
   memutus job aktif di tengah transaksi.

Selama satu overlap saja, owner URL boleh dipakai jika
`ALLOW_LEGACY_DB_CREDENTIALS=true`. Setelah B stabil, hapus flag dan `PG_URL`
dari environment job; tanpa dedicated URL, job harus gagal sebelum menulis.

### Collab database tanpa memutus WebSocket

Tulis DSN B ke file sementara mode `0600`, lalu rename atomik ke
`COLLAB_PG_URL_FILE`. Pool kandidat menjalankan preflight SQL yang memerlukan
SELECT+UPSERT `collab_documents`; hanya setelah lolos pool aktif ditukar.
Query berjalan di pool A dibiarkan drain dan WebSocket tidak direstart.
Kandidat gagal mempertahankan A. Setelah health dan write/read draft lolos,
jadikan role A `NOLOGIN`, tunggu pool A drain, lalu cabut/drop A.

## Rollout aplikasi A-2

1. Migration A-2 membutuhkan migration login dengan `CREATEROLE` (atau
   superuser) dan ownership seluruh tabel/view `public`. Preflight app berjalan
   sebelum migration menyentuh schema sehingga kegagalan privilege tidak
   meninggalkan schema DIRTY.
2. Deploy registry sambil mempertahankan `COLLAB_SERVICE_TOKEN` satu rilis.
   App memasukkannya sekali sebagai T1 legacy, hanya hash, expiry 30 hari, dan
   tidak memperpanjangnya saat restart.
3. Lakukan overlap token+role seperti di atas. Hapus fallback direct env setelah
   cutover. U-0 belum diterbitkan token sampai komponennya benar-benar dibangun.
4. Verifikasi proxy internet mengembalikan 404 untuk `/internal/*`; jaringan
   privat dengan token valid tetap bekerja dan setiap panggilan memiliki row
   audit.

## Rollback dan insiden

- Sebelum revoke/`NOLOGIN`: kembalikan file atomik ke A; consumer terus hidup.
- Setelah revoke token: terbitkan C, validasi, lalu pindahkan file ke C. Jangan
  mengubah `revoked_at` di database.
- Setelah role A `NOLOGIN`: bila B gagal tetapi A belum dicabut membership-nya,
  operator boleh `ALTER ROLE A LOGIN` dengan expiry baru ≤90 hari; audit dan
  selesaikan B. Setelah role di-drop, buat C.
- Token bocor: revoke token atau seluruh principal sesuai blast radius, cari
  token ID/principal pada `service_request_audit_logs`, lalu rotasi semua secret
  store yang memuat credential tersebut.
