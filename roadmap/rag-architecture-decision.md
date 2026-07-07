# RAG Architecture Decision — Surau (evaluasi komparatif)

> Dibuat 2026-07-07 dari panel evaluasi 5-paradigma (riset web kondisi 2025-2026) terhadap kebutuhan
> keras Surau (sitasi presisi, akurasi akidah, graph terkurasi yang sudah ada, Go/Postgres, self-host,
> biaya). **Ini input untuk Fase 7 & Fase 9.** Verdict: **VALIDASI Fase 7, tajamkan eksekusi — jangan
> ubah strateginya.**

## Skor fit (terhadap kebutuhan keras)
| Paradigma | Skor | Verdict |
|---|---|---|
| **Hybrid / structural-first** (arah Fase 7) | **9/10** | adopt-as-core |
| **Agentic + hierarchical** (PageIndex/CRAG/self-RAG) | **8/10** | adopt-as-core |
| **GraphRAG** | **7/10** | adopt paradigmanya, **BUKAN** tool-nya |
| **Vector / dense** (pgvector/Milvus/dst.) | **6/10** | nanti, candidate-gen only |
| **Frameworks** (LangChain/LangGraph/LlamaIndex/DSPy) | **4/10** | pinjam offline, hindari runtime |

## Temuan inti — jawaban atas pertanyaan operator
1. **GraphRAG = paradigma, BUKAN tool Microsoft.** Traversal deterministik atas graph terkurasi yang
   Anda **sudah punya** (Cross-Reference registry + relasi) = ideal untuk sitasi: tiap hop adalah klaim
   yang sudah direview, tak bisa mengarang tautan. **Microsoft-GraphRAG (LLM meng-ekstrak graph + menulis
   community summary) TERLARANG** — ia memanufaktur tautan tak-direview + klaim tak-terlacak = *aqidah-unsafe
   by construction* (community citations tak bisa dilacak ke sumber; riset 2601.08773: ekstraksi-LLM lebih
   salah & diam-diam melewatkan data vs graph deterministik). Registry Anda **adalah** graph-index yang
   tool itu coba (gagal) rekonstruksi dengan pipeline LLM mahal.
2. **Vector: kalah untuk sitasi.** "Mirip secara semantik" ≠ "benar secara ilmiah" — vektor memanufaktur
   tautan tak-direview yang charter larang. Vektor **nanti**, di dalam Postgres yang sama (pgvector,
   model BGE-M3 self-host), **hanya candidate-generation** (recall cross-lingual id→ar, parafrase,
   discovery browse, saran tautan untuk direview manusia) — **tak pernah** memutus sitasi/tautan/rerank
   grading. **Milvus/Qdrant/Weaviate = overkill** (justified hanya >~100M vektor; korpus Anda ribuan-jutaan).
3. **Frameworks: jangan runtime.** LangChain/LangGraph/LlamaIndex = Python sidecar di jalur kritis sitasi
   untuk tim Go; "source-node-ID citation" ≠ atribusi book+page+grade terverifikasi. **Pinjam offline:**
   DSPy untuk mengoptimasi *prompt sintesis jawaban* lalu hand-port ke Go; pola metrik RAGAS/DeepEval untuk
   eval-as-gate; bentuk state-machine LangGraph sebagai template mental controller Go.
4. **Tooling: Custom-Go, tegas.** Graph traversal = recursive CTE (query SQL) — bukan alasan Neo4j/Milvus/
   Python. Tolak RAPTOR (node ringkasan = parafrase, tak bisa disitasi).

## Arsitektur yang direkomendasikan
**Agentic RAG deterministik-struktur-dulu di Go atas Postgres, LLM dikurung ke READ-and-PHRASE saja.**
Empat mode retrieval di belakang satu router + satu gerbang sitasi tak-bisa-dilewati:
1. **Router** (deterministik, Go): klasifikasi query (anchor ada? scope satu-karya vs lintas-korpus?
   densitas entitas?); log lane yang menyala untuk eval.
2. **Structural / graph-traversal** (inti lintas-korpus): recursive CTE atas relasi + Cross-Reference,
   dengan `WHERE review_status='approved' AND confidence>=threshold` **di-bake ke SQL** (RAG-safety
   by construction); kembalikan anchor + rantai edge (trajectory) + grade/isnad di payload edge.
3. **PageIndex tree** (scope satu-karya): pertahankan `bookrag.go` apa adanya.
4. **Lexical** (lantai recall): tsvector + pg_trgm (sudah ada) + ekspansi sinonim via entity-graph
   sebagai jembatan murah pra-vektor untuk pertanyaan tematik tanpa-anchor.
5. **Exact-quote validator + repair-retry**: gerbang **keras** di SETIAP lane (termasuk vektor nanti).

Bungkus 1–5 dalam **controller agentic terbatas** (CRAG/self-RAG): retrieve → grade evidence →
(re-retrieve | **abstain**) → answer → validate → repair, dengan **budget iterasi keras** + early-exit
ber-confidence. Graph/registry adalah **tools yang dipanggil** controller — LLM tak pernah memilih hop
bebas, menulis edge, atau membuat community-summary.

## Tajamkan Fase 7 (VALIDASI + 7 penajaman)
1. Formalkan **traversal registry sebagai mode retrieval bernama** dengan gerbang `approved`+confidence di SQL.
2. **Aturan keras arsitektur:** LLM boleh MEMBACA subgraph tereview, tak pernah menulis edge / memilih hop / membuat community-summary.
3. **Perlakukan penundaan vektor sebagai recall-gap yang DIUKUR:** tambah kasus tematik-tanpa-anchor + cross-lingual + ikhtilaf ke golden set **sekarang**; tambah ekspansi sinonim entity-graph sebagai jembatan pra-vektor.
4. **Router eksplisit & bisa dites** (sinyal anchor/scope/densitas; log lane; rantai fallback structural→lexical→(vektor nanti)→**not-found**; not-found menyala saat confidence rendah, bukan memaksa jawaban tipis).
5. **Kategori eval IKHTILAF-COMPLETENESS** — gerbang tak boleh puas oleh "diam yang tersitasi rapi".
6. Reframe tree-retry `bookrag.go` jadi **controller CRAG/self-RAG terbatas** lintas-korpus dengan budget keras + adaptive routing (kontrol biaya agentic 3-10x di satu VPS).
7. **Log trajectory traversal penuh** sebagai bagian catatan sitasi, bukan hanya node final.

## Amandemen charter D7 (tanpa pembatalan — 3 klausa penjelas)
- **(a) Batas scope:** saat diadopsi, vektor = kanal **candidate-generation only**; tak pernah menegaskan
  sitasi/tautan answer-time, tak pernah rerank grading diam-diam, tak pernah menarik Quran ke assembler interpretatif.
- **(b) Gerbang adopsi:** pgvector rilis hanya setelah baseline lexical/struktural lolos eval non-regresi
  yang MENCAKUP tematik-tanpa-anchor, cross-lingual-parafrase, ikhtilaf-completeness, + tes negatif "tak ada
  cross-reference bersumber dari kemiripan".
- **(c) Pin stack:** model = BGE-M3 self-host; fusi = RRF + BM25; cross-encoder reranker self-host (bukan API
  eksternal); exact-quote validator = gerbang keras di jalur vektor juga.
- Opsional: klausa D-series yang menamai **graph traversal deterministik** sebagai mode retrieval kelas-satu.

## Risiko TERBESAR (bukan arsitektur — melainkan graph tak lengkap)
**Under-retrieval yang menyamar jadi presisi:** celah cakupan senyap di graph terkurasi → jawaban
tersitasi-percaya-diri tapi **TIDAK LENGKAP**, terutama **ikhtilaf yang diratakan karena kelalaian** (mengutip
2 posisi madzhab yang tertaut sambil melewatkan posisi ke-3 yang tak-tertaut = tampak rapi, tapi *harm akidah*).
Lebih berbahaya dari over-retrieval karena **lolos gerbang naif** "apakah setiap sitasi valid". Mitigasi:
kategori eval ikhtilaf-completeness; fallback recall lexical+sinonim agar edge hilang turun jadi "tak ada
tautan terkurasi ditemukan" yang jujur; not-found eksplisit saat confidence rendah.

## Keputusan yang jadi milik operator
1. Budget iterasi controller agentic (mis. 5 tool-call / 3 refine / 2 verify) + ambang confidence early-exit vs abstain — tradeoff biaya/akurasi di satu VPS.
2. Metrik recall-gap konkret di golden set (tematik-tanpa-anchor + cross-lingual) yang mengizinkan mulai kerja vektor.
3. Model ikhtilaf sebagai typed-edge/node kelas-satu SEKARANG (disagrees-with, mayoritas/minoritas) vs retrofit nanti.
4. Cross-encoder reranker self-host vs tanpa reranker pada skala korpus sekarang.
5. Workflow + throughput review manusia untuk kandidat cross-reference dari discovery lexical/vektor (siapa, cadence, ambang promosi).
