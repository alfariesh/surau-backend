# Kontrak identitas generation-run

Last updated: 2026-07-11

Dokumen ini adalah kontrak normatif B-6 untuk semua keluaran LLM baru yang disimpan oleh jalur
enrichment aktif. Setiap keluaran machine wajib dapat dijawab dengan tiga fakta: run mana yang
membuatnya, model apa yang dipakai, dan versi prompt apa yang dipakai.

Book-RAG real-time tidak termasuk karena jawabannya tidak disimpan sebagai enrichment. Resolver
Quran juga tidak termasuk machine: ia deterministik dan tetap memakai method `resolver`.

## Registry immutable

`generation_runs` menyimpan satu descriptor untuk satu run:

```ts
type GenerationRun = {
  id: string;             // UUID
  task_name: string;      // non-empty, vocabulary tetap terbuka
  model_id: string;
  prompt_version: string;
  provider?: string;
  metadata: Record<string, unknown>;
  created_at: string;
};

type GenerationIdentity = {
  run_id: string;
  model_id: string;
  prompt_version: string;
};
```

Descriptor tidak dapat di-update atau dihapus. Retry boleh mendaftarkan UUID yang sama hanya
jika seluruh descriptor identik; tuple yang bertentangan gagal dan tidak menimpa data lama.
`GenerationIdentity` adalah bentuk typed yang ditampilkan pada aset/kontrak kurasi, sedangkan
`task_name`, provider, dan metadata operasional tetap berada di registry.

`knowledge_extraction_runs.id` sekaligus menjadi FK one-to-one ke `generation_runs.id`. Run lama
di-backfill dari model, prompt, task, parameter, scope, dan waktu yang memang sudah tercatat;
tidak ada identitas rekaan.

## Provenance Class

Aturan platform-wide:

- Provenance Class `machine` wajib menunjuk `generation_run_id` terdaftar;
- Provenance Class `source` dan `editorial` tidak boleh meminjam run machine;
- Cross-Reference dengan method `human` atau `resolver` juga tidak boleh membawa run machine;
- review manusia tidak mengubah Provenance Class `machine` dan tidak menghapus run asal;
- draft baru tanpa aset final machine sebagai sumber adalah `editorial` tanpa `generation`;
- row lama yang asalnya tidak dapat dibuktikan adalah `legacy_unknown` tanpa `generation`;
- `legacy_unknown` hanya penanda migrasi. Writer baru tidak boleh membuat row baru dengan kelas
  tersebut.

Database menegakkan pasangan kelas-run dan FK. Citable Unit mengunci Provenance Class dan run
setelah dicetak. Cross-Reference machine mengunci run serta mencocokkan tuple legacy
`method_detail` dengan registry.

## Generator aktif dan versi prompt

Satu invocation membuat satu UUID run per keluarga prompt aktif dan memakai UUID itu untuk semua
row keluarga tersebut:

| Keluaran | `task_name` | `prompt_version` |
|---|---|---|
| Terjemahan bagian kitab | `reader_translation` | `reader-translation-v1` |
| Ringkasan Arab | `reader_summary` | `reader-summary-v1` |
| Terjemahan ringkasan | `reader_summary_translation` | `reader-summary-translation-v1` |
| Terjemahan katalog buku/penulis/kategori | `catalog_translation` | `catalog-translation-v1` |
| Ekstraksi knowledge | nilai task ekstraksi (`mentions`, `terms`, `citations`, `relations`) | versi di `scripts/langextract_kg/prompts.py` |

Invocation reader yang mengerjakan terjemahan bagian dan terjemahan ringkasan sekaligus memakai
dua run berbeda. `--resume` mempertahankan row lama apa adanya dan hanya menulis row baru dengan
run invocation saat ini; ia tidak mengganti identitas row yang dilewati.

## Kontrak JSONL enrichment

Setiap row **teks machine** untuk `cmd/import-reader-assets` wajib membawa:

```json
{
  "kind": "translation",
  "book_id": 797,
  "heading_id": 10,
  "lang": "id",
  "content": "...",
  "provenance_class": "machine",
  "generation": {
    "run_id": "550e8400-e29b-41d4-a716-446655440000",
    "model_id": "glm-5.1",
    "prompt_version": "reader-translation-v1"
  }
}
```

Aturan ini berlaku untuk `translation`, `heading_summary`,
`book_metadata_translation`, `author_translation`, dan `category_translation`. Audio bukan teks
machine dan justru tidak boleh membawa `provenance_class` atau `generation`.

Importer membaca dan memvalidasi **seluruh file sebelum membuka transaksi tulis**. File ditolak
seluruhnya bila satu row kehilangan identitas, UUID rusak, prompt tidak cocok dengan `kind`, atau
satu `run_id` digunakan bersama tuple model/prompt yang berbeda. Error menyebut nomor baris.
Registrasi run dan seluruh upsert aset dilakukan dalam satu transaksi; kegagalan DB menggulung
semuanya.

QA `scripts/qa_reader_assets.py` dan `scripts/qa_catalog_assets.py` menerapkan kontrak yang sama
sebelum import. `translation_status=reviewed` tetap hanya label review: row tersebut masih
`machine` dan wajib mempertahankan `generation` asalnya.

Ekstraksi knowledge memakai bentuk yang sama pada JSONL mention, chunk audit, dan rejection:
`provenance_class:"machine"` ditambah `generation {run_id,model_id,prompt_version}`. Versi prompt
berasal dari `scripts/langextract_kg/prompts.py`. QA menolak UUID rusak, tuple yang hilang atau
bertentangan, dan `run_id` top-level yang berbeda dari `generation.run_id`. Sebelum mention
ditulis, writer DB juga membuktikan tuple tersebut sama dengan pasangan
`generation_runs`/`knowledge_extraction_runs`; jalur `--write-db=false` tetap menghasilkan JSONL
yang beridentitas lengkap.

File review mentah `*.langextract.jsonl` juga membawa Provenance Class dan descriptor pada setiap
document. File `*.raw_chunks/chunk-*.json` membungkus output model mentah bersama descriptor yang
sama. Penempelan identity ke JSONL dilakukan melalui temporary file + atomic replace; konflik
descriptor tidak meninggalkan file setengah tertulis. Field tambahan ini tetap kompatibel dengan
loader/visualizer LangExtract.

## Production translation, summary, dan catalog

Draft serta aset final untuk metadata buku, penulis, kategori, terjemahan bagian, dan ringkasan
membawa `provenance_class` dan `generation` typed. Alurnya:

1. Saat draft pertama dibuat, backend memeriksa aset final untuk target yang sama. Jika aset final
   itu `machine`, draft mewarisi run asal; jika tidak ada sumber machine, draft menjadi
   `editorial` tanpa generation. Payload klien tidak boleh menentukan label ini.
2. Ketika draft machine diedit atau direview, kelas `machine` dan run asal dipertahankan, termasuk
   bila manusia mengubah teks draft.
3. Revision snapshot menyimpan keduanya; restore mengembalikan identitas dari snapshot yang
   dipilih. Snapshot lama tanpa field B-6 tetap `legacy_unknown`, bukan ditebak `editorial`.
4. Publish menyalin kelas dan run dari draft ke aset final. Review terhadap aset machine tidak
   merelabelnya; penggantian lintas kelas hanya boleh ketika draft membawa teks yang benar-benar
   berbeda, sehingga aset baru memakai provenance miliknya sendiri.
5. Row lama tetap `legacy_unknown`. Jika teksnya benar-benar ditulis ulang manusia, writer
   menaikkannya menjadi `editorial`; perubahan label tanpa perubahan teks ditolak.

Dengan demikian status `draft|pending_review|approved|rejected` dan label
`generated|reviewed` tidak pernah dipakai sebagai pengganti Provenance Class.

## Kontrak kurasi

- `GET /v1/editorial/citable-units/{id}` menampilkan Provenance Class dan `generation` untuk unit
  machine, beserta teks ternormalisasi, versi profil, lifecycle, serta lineage.
- Cross-Reference list/get menampilkan `generation` untuk `method=machine`. `method_detail`
  dipertahankan agar client lama tetap kompatibel.
- Untuk `source`/`editorial` Citable Unit dan `resolver`/`human` Cross-Reference, field
  `generation` tidak dikirim (`omitempty`). Client harus memperlakukannya sebagai tidak ada,
  bukan sebagai identitas yang hilang.

Detail shape endpoint berada di `docs/citable-units.md` dan `docs/cross-references.md`.
