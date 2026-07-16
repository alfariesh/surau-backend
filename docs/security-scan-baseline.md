# Security Scan Baseline

Last checked: 2026-05-30

## govulncheck

`go tool govulncheck ./...`

- No called vulnerabilities found.
- The scan reported vulnerable imported/required packages that are not reached by current code paths.

## gosec

Actionable application-code scan:

`go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude-generated ./...`

- Current result: 0 issues.
- `G115` integer conversion findings were fixed with explicit bounds/type changes.
- `G304` importer/eval CLI path reads are suppressed inline with `#nosec G304` because those commands intentionally read operator-supplied local files.

Raw generated-code baseline:

`go run github.com/securego/gosec/v2/cmd/gosec@latest ./...`

- `G103` unsafe usage in generated protobuf files:
  - `docs/proto/v1/auth.pb.go`
  - `docs/proto/v1/task.pb.go`
  - `docs/proto/v1/translation.history.pb.go`

Raw `gosec ./...` still reports generated protobuf `G103` unsafe usage from `protoc-gen-go`. Use `-exclude-generated` for actionable application-code scans; do not hand-edit generated protobuf unsafe blocks.
(Catatan 2026-07-08: file `docs/proto/*.pb.go` yang dirujuk baseline lama sudah DIHAPUS bersama transport gRPC — baris di atas dipertahankan hanya sebagai konteks historis.)

## Secret scanning (gitleaks) + runbook rotasi rahasia

Sejak 2026-07-08 CI menjalankan job `gitleaks` (default rules + `.gitleaks.toml`) pada setiap PR
— commit yang membawa secret nyata akan MERAH dan tidak bisa merge. False positive fixture/test
ditambahkan ke allowlist `.gitleaks.toml`, bukan di-skip.

**Bila secret terlanjur ter-commit (atau terdeteksi bocor):**

1. **Anggap bocor permanen** — history git tidak dianggap bersih walau di-force-push.
2. **Rotasi SUMBERNYA dulu**, baru bersihkan repo:
   - Kunci penandatangan JWT → **jangan** mengganti `JWT_SECRET` atau me-restart app secara
     manual. Jalankan prosedur insiden di [`docs/jwt-key-rotation.md`](jwt-key-rotation.md):
     tambahkan kunci baru, pindahkan penerbitan seketika, pertahankan verifier lama+baru selama
     umur token terlama, lalu pensiunkan kunci bocor. Prosedur ini mencabut kunci bocor tanpa
     logout massal dan menghasilkan bukti token lama/baru yang tersanitasi.
   - Kredensial R2 (`R2_ACCESS_KEY_ID/SECRET`) → buat token baru di Cloudflare → update
     `/etc/surau-backup/env` di kedua VPS + `/etc/surau-backup/pgbackrest.conf` → `pgbackrest
     check` + jalankan `surau-backup-watchdog` untuk memastikan hijau.
   - Token bot Telegram → @BotFather `/revoke` → update `/etc/surau-backup/env` kedua VPS →
     `surau-notify "test"`.
   - `POSTGRES_PASSWORD`/`PG_URL` → ubah role password di db → update `.env.production` →
     recreate app (perlu jendela singkat).
   - Kunci deploy SSH → generate pasangan baru → update `authorized_keys` VPS + GitHub secret
     `*_VPS_SSH_PRIVATE_KEY`.
3. Hapus nilai dari file yang ter-commit + tambahkan pola ke `.gitleaks.toml` HANYA bila memang
   bukan secret; kalau secret nyata: biarkan gitleaks tetap menjaga.
4. Catat insiden (apa, kapan, rotasi apa) di PR/issue terkait.

Rotasi berkala enam-bulanan dan rotasi insiden JWT memakai runner A-4 yang sama; bedanya,
insiden dimulai segera dan jendela overlap dipilih dari token hidup yang sudah terbit, bukan
menunggu kalender.
