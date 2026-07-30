# Operasi lapisan inferensi U-0

Dokumen ini adalah runbook operator untuk provider, harga, metering, cache, failover, dan pagar
biaya LLM. Semua Book-RAG serta generator aktif wajib melewati lapisan ini; provider secret hanya
berada pada environment API/VPS.

## Cara melihat biaya harian dan pagar

Cara termudah:

1. buka Grafana → folder **Surau** → dashboard **Surau Health**;
2. lihat panel **LLM cost today** untuk total hari ini;
3. lihat **LLM daily breakdown — task / provider** untuk sumber biayanya;
4. lihat **LLM budget guard** untuk pemakaian harian/bulanan, progres baseline, dan ambang 80%.

Cara melalui API admin:

```sh
curl -sS \
  -H 'Authorization: Bearer <admin-jwt>' \
  'https://dev-api.surau.org/v1/admin/inference/usage?group_by=day'

curl -i -sS \
  -H 'Authorization: Bearer <admin-jwt>' \
  'https://dev-api.surau.org/v1/admin/inference/budget'
```

Endpoint usage selalu berbentuk `{items,total}` dan menerima `group_by=day|task|provider|model`,
serta `from`/`to` RFC3339 dengan rentang maksimum 366 hari. Nilai biaya disimpan sebagai integer
`nano_usd`; 1 USD = 1.000.000.000 nano-USD. Panel Grafana mengubahnya menjadi USD untuk dibaca.

## Kebijakan biaya

- Hari dan bulan dihitung dalam zona waktu `Asia/Jakarta`.
- Revisi awal mengukur 30 hari. Setelah itu sistem membuat cap bulanan sebesar 2× total baseline
  dan cap harian sebesar 2× hari termahal.
- Bila baseline masih nol, masa pengukuran diperpanjang dan alarm konfigurasi menyala; sistem
  tidak pernah membuat cap nol.
- Dalam mode `enforce`, alarm Telegram menyala saat cap harian atau bulanan mencapai **80%**.
- Reservasi maksimum dilakukan dengan row lock sebelum provider dipanggil. Panggilan paralel tidak
  dapat bersama-sama melewati sisa cap.
- Token/cost dari provider dipakai bila tersedia. Jika usage tidak tersedia, estimasi konservatif
  dan harga registry dipakai; panel **LLM exact vs estimated cost / cache hit-rate** menunjukkan
  proporsinya.

Saat cap terlampaui, cache hit berbiaya nol tetap dilayani tetapi provider baru tidak dipanggil.
JSON mengembalikan HTTP `503`, code `inference_budget_exceeded`, `Retry-After`, `retry_after`, dan
waktu reset. SSE mengirim satu event `error` terstruktur lalu berhenti sebelum delta jawaban.
Generator keluar dengan kode `75`, mempertahankan output lama, dan dapat dilanjutkan dengan
`--resume`. Judge/eval gagal atau dilewati; cap tidak pernah dianggap sebagai hasil lulus.

## Override admin

Perubahan budget membuat revision append-only. Admin harus memiliki capability
`manage-service-tokens`, MFA yang masih segar, ETag dari GET, dan alasan:

```sh
curl -i -sS \
  -H 'Authorization: Bearer <admin-jwt>' \
  'https://dev-api.surau.org/v1/admin/inference/budget'

curl -sS -X PATCH \
  -H 'Authorization: Bearer <admin-jwt>' \
  -H 'If-Match: "<etag-dari-get>"' \
  -H 'Content-Type: application/json' \
  --data '{"mode":"enforce","daily_cap_nano_usd":500000000,"monthly_cap_nano_usd":10000000000,"reason":"penyesuaian operasional bertiket"}' \
  'https://dev-api.surau.org/v1/admin/inference/budget'
```

Gunakan `disabled` hanya untuk insiden terkontrol dan dokumentasikan alasannya. Jangan mengubah
row kebijakan lewat SQL karena itu menghilangkan audit revisi.

## Provider dan harga

Route default:

| Prioritas | Provider/model | Sumber harga |
|---|---|---|
| primer | SumoPod / `glm-5.1` | katalog akun `INFERENCE_SUMOPOD_CATALOG_URL`, disinkronkan saat boot |
| sekunder | DeepSeek / `deepseek-v4-flash` | [dokumentasi harga resmi DeepSeek](https://api-docs.deepseek.com/quick_start/pricing/) |

Keputusan operator 2026-07-30 untuk **dev** memakai satu provider SumoPod dengan model
`deepseek-v4-pro`. Harga snapshot akun yang berlaku:

| Jenis token | USD per 1 juta token |
|---|---:|
| input | 0,50 |
| cached input | 0,004 |
| output | 0,95 |

Konfigurasi dev memakai versi immutable
`sumopod-account-2026-07-30-deepseek-v4-pro` dan
`INFERENCE_SECONDARY_ENABLED=false`. Ini adalah failover yang sengaja dinonaktifkan, bukan dua
provider dengan nama berbeda. Suite integration tetap membuktikan kemampuan failover dua
provider; dev belum tahan outage SumoPod penuh sampai provider independen diaktifkan kembali.

Boot/readiness gagal tertutup bila route berbayar tidak memiliki harga, hash manifest DB berbeda
dari binary, credential salah satu route hilang, atau katalog SumoPod tidak dapat memberi harga
model primer. Harga disimpan sebagai versi immutable; panggilan lama tetap dapat diaudit dengan
harga yang berlaku saat itu.

Jika katalog harga provider tidak tersedia, operator boleh memasang **satu tuple lengkap** dari
dashboard akun:

```dotenv
INFERENCE_SUMOPOD_PRICE_VERSION=sumopod-account-2026-07-30-deepseek-v4-pro
INFERENCE_SUMOPOD_INPUT_USD_PER_MILLION=0.50
INFERENCE_SUMOPOD_CACHED_INPUT_USD_PER_MILLION=0.004
INFERENCE_SUMOPOD_OUTPUT_USD_PER_MILLION=0.95
```

Tuple parsial atau harga negatif menolak boot. Mengubah tarif wajib memakai `PRICE_VERSION` baru;
versi yang sudah masuk ledger tidak boleh digunakan ulang dengan nominal berbeda.

Environment rahasia API:

- `RAG_LLM_API_KEY` untuk SumoPod;
- `DEEPSEEK_API_KEY` untuk DeepSeek;
- `INFERENCE_CACHE_ENCRYPTION_KEY`, minimal 32 byte dan terpisah dari kedua credential provider.

Jalankan workflow manual **Inference DEV provider preflight** dari branch `main` sebelum
me-merge perubahan provider. Pemeriksaan memastikan semua credential route aktif terisi, model
primer benar-benar tersedia, dan harga berasal dari katalog atau tuple operator lengkap; nilainya
tidak pernah dicetak atau disalin keluar VPS. U-0 tidak boleh di-merge bila preflight ini merah.

Jangan menyalin nilai ketiganya ke registry, trace, cache payload, tiket, log, atau generator.
Rotasi token gateway generator mengikuti overlap T1/T2 principal `u0-inference` pada
[`service-identity-rotation.md`](service-identity-rotation.md).

## Failover dan pemulihan

Maksimum dua provider dicoba bila `INFERENCE_SECONDARY_ENABLED=true`. Timeout/network,
408/429/5xx, output kosong, atau output yang gagal
JSON Schema boleh pindah ke sekunder; request-invalid 400 tidak. Bila keduanya gagal, API
mengembalikan `503 inference_provider_unavailable`.

Jika `INFERENCE_SECONDARY_ENABLED=false`, hanya route primer yang diregistrasikan. Jangan
menduplikasi key/endpoint primer sebagai provider kedua karena itu memberi kesan ketahanan palsu.

Untuk batch LangExtract, provider/model dipin setelah output sukses pertama. Gangguan setelah pin
menghentikan batch agar satu run tidak mencampur model. Perbaiki provider, lalu resume sebagai
session/run baru.

Langkah insiden:

1. periksa **LLM budget guard**: bedakan cap, harga/config, dan provider outage;
2. cari `request_id` pada log, lalu buka `trace_id` di Tempo; trace hanya menyimpan metadata,
   bukan prompt, pertanyaan, output, atau secret;
3. periksa `/v1/admin/inference/registry` untuk dua route aktif dan versi harga;
4. untuk cap, tunggu reset atau buat revision override beralasan;
5. untuk provider, perbaiki credential/katalog tanpa mengubah route menjadi jalur tak bermeter;
6. pastikan `/readyz` kembali hijau dan lakukan smoke terisolasi.

## Cache aman

Key HMAC mencakup task, prompt/schema/model, seluruh input ter-render, parameter, bahasa, filter,
profil/lensa, style, versi indeks, dan versi sumber. Payload memakai AES-256-GCM dengan domain key
terpisah; cache key juga diikat sebagai authenticated associated data sehingga ciphertext valid
tidak dapat dipindah ke baris cache lain. TTL kelas: rewrite 24 jam, rerank 1 jam, embed 30 hari,
answer 10 menit, dan judge tidak dicache. Enrichment persisten juga tidak dicache agar Provenance
Class dan Generation Run tidak tersamarkan. Ciphertext rusak atau key salah dianggap miss dan
tidak dibuka ke caller.

## Bukti atribusi

Setiap provider attempt memiliki:

- task-key/kelas, provider/model, prompt version/hash, schema version;
- Generation Run B-6, input/cached/output token, biaya nano-USD dan sumber hitung;
- cache status, outcome, request/trace ID, serta urutan failover.

Database menolak tuple yang kosong atau tidak cocok dengan `generation_runs`. Live invariant:

```sql
SELECT count(*) AS attribution_violations
FROM inference_attempts a
JOIN generation_runs g ON g.id = a.generation_run_id
WHERE a.task_key = ''
   OR a.provider_key = ''
   OR a.model_key = ''
   OR a.prompt_version = ''
   OR a.response_schema_version = ''
   OR a.usage_source = ''
   OR a.cost_source = ''
   OR g.model_id <> a.provider_model_id
   OR g.prompt_version <> a.prompt_version;
```

Hasil yang sehat adalah `0`.

Setelah smoke Book-RAG pada dev, jalankan workflow manual **Inference DEV verification** dari
`main`. Workflow ini menolak deploy bila `/version` bukan SHA workflow, readiness/metrics gagal,
manifest/route/harga/scope A-2 tidak lengkap, belum ada provider attempt termeter, atau live
attribution violations tidak nol. Query bersifat baca-saja dan tidak mencetak prompt, output,
pertanyaan, maupun secret.

## Inventaris call-site aktif

| Jalur | Task U-0 |
|---|---|
| Book-RAG tree full/block/retry | `bookrag-tree-full`, `bookrag-tree-block`, `bookrag-tree-retry` |
| Book-RAG answer/repair | `bookrag-answer`, `bookrag-answer-repair` |
| Reader translation/summary | `reader-translation`, `reader-summary`, `reader-summary-translation` |
| Catalog translation | `catalog-translation` |
| LangExtract | `langextract-mentions`, `langextract-terms`, `langextract-citations`, `langextract-relations` |
| MiMo eval dev | route registry dev melalui gateway |
| `cmd/rag-eval` | Book-RAG HTTP, sehingga otomatis masuk ledger U-0 |

`internal/repo/webapi/inference_provider.go` adalah satu-satunya adapter yang boleh mengenal path
provider. Contract test Go/Python menolak `/chat/completions`, `/embeddings`, provider SDK/API key,
atau constructor provider di luar adapter dan fixture test yang diizinkan. Task `embed` sudah ada
di registry tetapi route-nya nonaktif sampai mini-eval U-1 memilih model Arab klasik↔Indonesia.
