# Surau Web Reader Prototype

Prototype frontend ringan untuk melihat backend reader yang sudah dibuat. Tidak
pakai framework; hanya HTML, CSS, JS, dan server proxy kecil agar browser tidak
terhalang CORS saat fetch backend lokal.

## Run

Pastikan backend jalan di `http://127.0.0.1:8080`, lalu:

```sh
node web-reader/server.mjs
```

Buka:

```text
http://127.0.0.1:8090
```

Opsional:

```sh
BACKEND_URL=http://127.0.0.1:8080 PORT=8090 node web-reader/server.mjs
```

## Scope

- List kitab published.
- Detail kitab.
- TOC per bab/subbab.
- Page reader per halaman seperti Shamela.
- Reader per TOC section.
- Arabic original dari `original_html`.
- Translation Markdown sederhana dari `translation.content`.
- Label `Generated` / `Reviewed by ...`.

Ini prototype untuk menilai rasa baca. Belum ada auth, progress, bookmark,
admin edit, atau audio player lengkap. Mode halaman sengaja hanya menampilkan
Arabic page content; translation/audio tetap canonical di mode bab/subbab.
