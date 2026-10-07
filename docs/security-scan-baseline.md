# Security Scan Baseline

Last checked: 2026-10-07

## govulncheck

`go tool govulncheck ./...`

- 0 called vulnerabilities and 0 in imported packages (Go 1.26.8, OpenTelemetry v1.45.0,
  gRPC v1.83.2). Before 2026-10-07 the scan reported 8 called vulnerabilities: six in the Go
  1.26.5 standard library (`net/http`, `crypto/tls`, `html/template`, `net/url`,
  `encoding/xml`, `encoding/asn1`; fixed in 1.26.6), GO-2026-6505 in OpenTelemetry v1.44.0
  and GO-2026-6348 in gRPC v1.82.1. The image that built dev/prod ran Go 1.26.4, and a
  `-mode=binary` scan of that binary reported 11.
- Still listed under "modules you require" but not called: GO-2026-6354/6355 and GO-2026-5932
  (`golang.org/x/crypto` v0.55.0), GO-2026-6179/6180 (`golang.org/x/mod` v0.38.0), and
  GO-2026-5841 (`github.com/klauspost/compress` v1.18.6). Re-check them when one of those
  modules is next upgraded.

**Gate.** `.github/workflows/vuln-gate.yml` runs this scan on every branch push, weekly on
`main` (Monday 08:00 WIB), and on demand; a failure on `main` also raises the Telegram alarm
once the `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID` repository secrets exist (CI alarms are no-ops
without them).
The `main` ruleset requires its `runner / vuln-gate` check, so GitHub refuses to move `main`
to a commit whose scan did not pass, with or without a PR: push the branch first, wait for the
check, then fast-forward `main`. The PR-only CI job `runner / govulncheck` reports the same scan
on pull requests.

**One Go version.** The `toolchain` line in `go.mod` is the only place the Go version is set.
CI reads it through `actions/setup-go@v6` with `go-version-file: go.mod` (v5 reads only the
`go 1.26` line). The golang images set `GOTOOLCHAIN=local` and ignore that line, so both
Dockerfiles pin the same version and refuse to build when `go env GOVERSION` differs from it;
`internal/ci/toolchain` fails CI when a Dockerfile or workflow drifts. To upgrade Go, change the
`toolchain` line and both Dockerfiles' `FROM golang:` tags together.

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
