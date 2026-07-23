# Observability (F1-B): logs ber-request-ID, tracing, metrik, dashboard, alert

## Cara membuka dashboard (untuk operator)

1. Buka `https://dev-api.surau.org/grafana/` (dev) atau `https://api.surau.org/grafana/` (prod).
2. Login: user `admin`, password = nilai `GRAFANA_ADMIN_PASSWORD` di `.env.production` VPS tsb.
3. Dashboard: menu ☰ → Dashboards → folder **Surau** → **Surau Health**.
   Panel: rate/error/p95 per endpoint (RED), antrean email, umur sukses terakhir tiap loop
   background, jumlah reminder accepted/failed, alasan kegagalan reminder, disk/RAM/CPU host,
   serta umur backup & PITR-check.
4. Melihat trace: menu ☰ → Explore → pilih datasource **Tempo** → paste `trace_id` dari log,
   atau query `{}` untuk trace terbaru.

### Melihat reminder kemarin (Q-6)

1. Di kanan atas dashboard **Surau Health**, klik pemilih waktu lalu pilih **Yesterday**.
2. Panel **Streak reminders — accepted vs failed** menampilkan jumlah logical reminder yang
   diterima atau ditolak OneSignal pada rentang itu. `accepted` berarti OneSignal menerima
   permintaan; angka ini bukan konfirmasi bahwa notifikasi pasti tampil di perangkat pengguna.
3. Panel **Streak reminder failures — reasons** memecah percobaan gagal berdasarkan alasan aman,
   misalnya rate-limit, timeout, atau tidak ada subscriber.

Dashboard memakai timezone browser operator dan Prometheus menyimpan tujuh hari. OneSignal tetap
dinonaktifkan di dev secara default; karena itu panel akan nol/kosong sampai ada fixture drill atau
delivery dev yang sengaja diaktifkan tanpa menyasar pengguna nyata.

## Korelasi log ↔ trace (AC F1-B)

- Setiap respons membawa header `X-Request-ID` (dan `X-Trace-ID` saat tracing aktif).
- Setiap baris log request-scoped (baris akses + error handler) membawa field `request_id` dan
  `trace_id`; span HTTP membawa atribut `surau.request_id` — bisa loncat dari log ke trace dan
  sebaliknya.
- Rantai span: HTTP (otelfiber) → SQL (otelpgx, setiap query pgx) → panggilan keluar (otelhttp:
  LLM, Cloudflare Email, OneSignal).
- Alur debug insiden: ambil `request_id` dari keluhan/error envelope → `docker compose logs app |
  grep <request_id>` → salin `trace_id` → buka di Grafana Explore/Tempo.

## Arsitektur

- Aplikasi: `METRICS_ENABLED=true` mengekspos `/metrics` (fiberprometheus, label service =
  `APP_NAME`) — HANYA di jaringan compose internal; edge Caddy tetap 404. `OTEL_ENABLED=true`
  mengirim span OTLP ke `tempo:4318` (sampling `OTEL_TRACE_SAMPLE_RATIO`).
- Stack (profil compose `observability`, deploy rutin TIDAK menyentuhnya):
  Prometheus :9090 (retensi 7d/1GB) · Tempo :3200/:4318 (retensi 48 jam) · Grafana :3000
  (dipublish loopback; Caddy route `/grafana*`) · node_exporter :9100 (+ textfile collector
  `/var/lib/node_exporter/textfile` — skrip backup menulis heartbeat ke sini) ·
  postgres-exporter :9187 (F1-G, internal-only; `--collector.stat_statements`) — metrik DB
  (koneksi, statements, dead-tuples, autovacuum) + panel "DB *" di dashboard; ukuran
  tabel/index top-20 datang dari collector app (`surau_db_relation_*`). Panel slow-statements
  butuh db berjalan dgn preload `pg_stat_statements` (docs/deploy-vps.md §Tuning Postgres).
- Provisioning Grafana dari `ops/observability/grafana/provisioning/` (datasource, dashboard,
  contact point Telegram, 11 alert rules) — semua file di git, tiba di VPS via checkout deploy.

## Alert (semua → Telegram, prefix env)

| Rule | Kondisi | Arti |
|---|---|---|
| 5xx surge | rate 5xx >0.2 rps selama 2m | API error beruntun (DB down / rilis buruk) |
| p95 latency breach | p95 >500ms selama 10m | API melambat |
| email stuck / dead letter | antrean tertua >30m ATAU failed >0 | pipeline email macet/gagal permanen — remediasi: kirim ulang via `POST /v1/admin/emails/messages/{id}/resend` (F1-C, lihat docs/admin-email-api.md §Resend) |
| OneSignal mass delivery failure | ≥5 attempt gagal DAN rasio gagal ≥50% dalam rolling 5m, bertahan 1m | gangguan massal push; periksa kredensial, rate-limit, status provider, dan log loop reminder |
| OneSignal erasure stale | ada provider erasure belum `verified` setelah 24h | privacy SLA terlewati; ikuti `docs/onesignal-erasure.md` memakai HMAC audit saja |
| OneSignal erasure provider auth | attempt erasure mendapat `401/403` dalam 5m | App API Key ditolak; perbaiki secret tanpa menyalin key/UUID/JWT ke log atau tiket |
| backup heartbeat stale | sukses terakhir >26 jam | dead-man backup (lapis dashboard; watchdog S1 tetap ada) |
| disk space low | sisa <15% | disk hampir penuh |
| app down | scrape gagal 3m | app mati/boot-loop (termasuk schema DIRTY) |
| db connections near max | koneksi >80% max_connections 5m | pool bocor/beban tak wajar — cek pg_stat_activity (F1-G) |

Ambang ditulis DI DALAM ekspresi PromQL di
[rules.yml](../ops/observability/grafana/provisioning/alerting/rules.yml) (pola `> bool X`) —
mengubah ambang = edit satu angka + `docker compose --profile observability restart grafana`.

Untuk alarm OneSignal, scrape Prometheus 15 detik + evaluasi rule 1 menit + `for: 1m` + Telegram
`group_wait: 30s` memberi batas desain terburuk sekitar 2 menit 45 detik setelah ambang terpenuhi,
sehingga masih di bawah Acceptance Criterion Q-6 yaitu lima menit. Reminder yang dilewati karena
quiet-hours/timezone bukan attempt provider dan tidak masuk rasio kegagalan ini.

Metrik audit Q-6 yang tersedia di Prometheus adalah
`surau_notification_delivery_attempts_total` (hasil provider + alasan),
`surau_notification_deliveries_total` (logical delivery terminal), dan
`surau_notification_reminder_skips_total` (reminder yang tidak dicoba). Rule alarm memakai gauge
rolling `surau_notification_delivery_attempts_5m`, yang dihitung langsung dari attempt persisten;
ini membuat batch kegagalan pertama tetap terlihat walau belum ada sampel counter sebelumnya.
Query rule/dashboard memakai deduplikasi replica karena setiap instance API membaca total database
global yang sama.

Provider erasure mengekspor `surau_onesignal_erasure_queue`,
`surau_onesignal_erasure_attempts_total`, dan `surau_onesignal_erasure_stale`. Semua label
low-cardinality; UUID, JWT, ciphertext, HMAC penuh, dan secret tidak pernah menjadi label metrik.

## Rollout / update stack di VPS

```sh
cd /srv/surau/backend
# 1) .env.production wajib punya: METRICS_ENABLED/OTEL_*, GRAFANA_*, OBS_ENV_LABEL
# 2) generate contact point Telegram — SEKALI per host. Nilai ditulis literal
#    (interpolasi $ENV Grafana merusak chat-id numerik); ambil dari
#    /etc/surau-backup/env dan tulis ke /etc/surau-obs/contact-points.yml
#    mengikuti bentuk template ops/observability/.../contact-points.yml
#    (bottoken tanpa kutip, chatid DALAM kutip, [label] env di message).
sudo mkdir -p /etc/surau-obs   # lalu tulis file seperti di atas, chmod 644
sudo docker compose --env-file .env.production -f docker-compose.prod.yml \
  --profile observability up -d
# route edge (sekali per host): tambahkan blok /grafana* dari deploy/Caddyfile.tmpl
sudo systemctl reload caddy
```

Konfig berubah di git → `git pull` terjadi otomatis saat deploy berikutnya → jalankan ulang
`up -d`/`restart` service terkait (deploy TIDAK melakukannya otomatis — preseden db/PITR).

## Simulasi alert (pola pembuktian AC — dilakukan di DEV)

1. 5xx & app-down: `docker compose stop db` ±3 menit → start lagi.
2. p95: turunkan ambang rule ke `0.001` sementara + beberapa request → revert.
3. email: `INSERT INTO email_messages (id, ..., status) VALUES (..., 'failed')` → hapus lagi.
4. backup heartbeat: tulis timestamp `now-27h` ke
   `/var/lib/node_exporter/textfile/surau_backup_last_success_timestamp.prom` → pulihkan.
5. disk: naikkan ambang rule ke `< bool 99` sementara → revert.
6. OneSignal Q-6: dengan `ONESIGNAL_ENABLED=false`, gunakan fixture drill dev untuk menulis 5
   attempt gagal dengan rasio ≥50% ke counter persisten; ukur sampai rule FIRING dan pesan Telegram
   diterima (wajib ≤5 menit), lalu bersihkan fixture/counter dev dan pastikan rule RESOLVED. Jangan
   memakai API key produksi atau menyasar user nyata.
