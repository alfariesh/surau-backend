# Konvensi Modul-Domain (F1-F)

Cetakan baku untuk menambah domain baru (hadith, wiki, retrieval — Fase 1B/5/6) tanpa membuat
`internal/app/app.go` membengkak menjadi god-file. Pola ini MENDESKRIPSIKAN bentuk yang sudah
dipakai semua domain existing (reader, quran, personal, editorial, user, bookRAG) — domain baru
wajib mengikutinya.

## Bentuk lapisan (per domain)

```
internal/entity/<domain>.go                  ← tipe domain murni (tanpa dependency framework)
internal/repo/contracts.go                   ← interface <Domain>Repo + tipe filter
internal/repo/persistent/<domain>_postgres.go← implementasi SQL (squirrel/pgx)
internal/repo/webapi/…                       ← klien eksternal bila perlu (LLM, email, R2)
internal/usecase/contracts.go                ← interface <Domain> (permukaan usecase)
internal/usecase/<domain>/<domain>.go        ← logika domain; validasi & clamp DI SINI
internal/controller/restapi/v1/<domain>.go   ← handler Fiber + anotasi Swagger
internal/controller/restapi/v1/request/      ← struct request per paket
internal/controller/restapi/v1/response/     ← struct response per paket (envelope {items,total})
```

## Aturan wiring di `internal/app/app.go`

1. Repo dibangun sekali di `initUseCases`, lalu usecase: `<domain>.New(<domain>Repo, …deps)`.
2. Instance masuk struct `useCases` → diteruskan ke `httpserver`/router — controller TIDAK PERNAH
   membangun repo sendiri.
3. Loop background per-domain = satu `loopSpec` (nama metrik, interval, fungsi pass) yang
   didaftarkan di `buildLoopSpecs` dan dijalankan `runSupervisedLoop` (`internal/app/loop.go`,
   F1-C): panic-recovery per pass, backoff+jitter saat gagal beruntun, drain ber-timeout saat
   shutdown, metrik `surau_loop_runs_total{loop,result}` otomatis. JANGAN menulis goroutine
   ticker telanjang. Job batch/one-shot (backfill dsb.) TIDAK memakai loop app — lihat
   `docs/data-change-playbook.md`.

## Aturan yang tidak boleh dilanggar domain baru

- Endpoint list publik: envelope `{items,total}`; `limit` di-clamp (default 50/200, max 200);
  `offset` di-clamp max 10000; query pencarian dibatasi 200 rune dan pola ILIKE WAJIB lewat
  `persistent.escapeLike`. Delapan envelope legacy (`users/activity/projects/candidates/
  events/revisions/feedbacks`) DIBEKUKAN apa adanya (test kontrak di `v1/response`) — list
  BARU dilarang meniru mereka.
- Pesan error = kontrak (F1-D): setiap kalimat pesan baru WAJIB didaftarkan di
  `apierror/registry.go` (test kontrak menolak literal tak terdaftar); JANGAN mengubah kalimat
  lama — kodenya beku. Detail per-instance masuk `details` (`errorResponseWithDetails`),
  bukan ke kalimat pesan.
- Endpoint GET publik yang stabil masuk grup `middleware.PublicCache()`; endpoint dinamis
  (search/q-param) di dalam grup ter-cache WAJIB dikecualikan via
  `middleware.ExcludePath(...)` (no-store).
- Mutasi ber-ETag mengikuti pola optimistic-locking (`If-Match`, 412/428).
- Interface baru di contracts.go → jalankan `make mock` (mock ter-regenerate, jangan diedit
  manual).
- Kontrak API berubah → regen Swagger (`make swag-v1`) + perbarui docs kontrak FE terkait.
- Test: usecase unit (mock repo), endpoint publik baru ≥1 integration test, invariant korpus →
  live test (`SURAU_LIVE_PG`).

## Checklist scaffold domain baru

1. Entity + migrasi (pasangan up/down, aditif — pola F1-H).
2. Interface repo + implementasi persistent + filter (ikuti clamp di atas).
3. Interface usecase + paket usecase (validasi, clamp, normalisasi bahasa bila relevan).
4. Controller + request/response + anotasi Swagger + router group.
5. Wiring `initUseCases` (+ loop background bila ada) — SATU blok per domain, seragam.
6. `make mock` · `make swag-v1` · test tiga lapis · update docs kontrak FE.
