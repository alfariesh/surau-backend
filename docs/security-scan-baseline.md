# Security Scan Baseline

Last checked: 2026-05-28

## govulncheck

`go tool govulncheck ./...`

- No called vulnerabilities found.
- The scan reported vulnerable imported/required packages that are not reached by current code paths.

## gosec

`go run github.com/securego/gosec/v2/cmd/gosec@latest ./...`

Current non-auth/generated baseline:

- `G115` integer conversion warnings in importer/book RAG support code:
  - `internal/importer/importer.go`
  - `internal/repo/persistent/bookrag_postgres.go`
- `G304` variable file path reads in importer/eval tooling:
  - `internal/rageval/rageval.go`
  - `internal/importer/quran_audio_r2.go`
  - `internal/importer/quran.go`
  - `internal/importer/importer.go`
- `G103` unsafe usage in generated protobuf files:
  - `docs/proto/v1/auth.pb.go`
  - `docs/proto/v1/task.pb.go`
  - `docs/proto/v1/translation.history.pb.go`

These findings are outside the auth email notification implementation. No new finding was reported in the auth notification usecase or repository code.
