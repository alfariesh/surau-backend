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
