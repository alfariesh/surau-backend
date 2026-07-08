# Observability (F1-B): logs ber-request-ID, tracing, metrik, dashboard, alert

## Cara membuka dashboard (untuk operator)

1. Buka `https://dev-api.surau.org/grafana/` (dev) atau `https://api.surau.org/grafana/` (prod).
2. Login: user `admin`, password = nilai `GRAFANA_ADMIN_PASSWORD` di `.env.production` VPS tsb.
3. Dashboard: menu ☰ → Dashboards → folder **Surau** → **Surau Health**.
   Panel: rate/error/p95 per endpoint (RED), antrean email, umur sukses terakhir tiap loop
   background, disk/RAM/CPU host, umur backup & PITR-check.
4. Melihat trace: menu ☰ → Explore → pilih datasource **Tempo** → paste `trace_id` dari log,
   atau query `{}` untuk trace terbaru.

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
  `/var/lib/node_exporter/textfile` — skrip backup menulis heartbeat ke sini).
- Provisioning Grafana dari `ops/observability/grafana/provisioning/` (datasource, dashboard,
  contact point Telegram, 6 alert rule) — semua file di git, tiba di VPS via checkout deploy.

## Alert (semua → Telegram, prefix env)

| Rule | Kondisi | Arti |
|---|---|---|
| 5xx surge | rate 5xx >0.2 rps selama 2m | API error beruntun (DB down / rilis buruk) |
| p95 latency breach | p95 >500ms selama 10m | API melambat |
| email stuck / dead letter | antrean tertua >30m ATAU failed >0 | pipeline email macet/gagal permanen |
| backup heartbeat stale | sukses terakhir >26 jam | dead-man backup (lapis dashboard; watchdog S1 tetap ada) |
| disk space low | sisa <15% | disk hampir penuh |
| app down | scrape gagal 3m | app mati/boot-loop (termasuk schema DIRTY) |

Ambang ditulis DI DALAM ekspresi PromQL di
[rules.yml](../ops/observability/grafana/provisioning/alerting/rules.yml) (pola `> bool X`) —
mengubah ambang = edit satu angka + `docker compose --profile observability restart grafana`.

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
