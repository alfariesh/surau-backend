# Step 1: Modules caching
FROM golang:1.26.4-alpine3.23 AS modules

COPY go.mod go.sum /modules/

WORKDIR /modules

RUN go mod download

# Step 2: Builder
FROM golang:1.26.4-alpine3.23 AS builder

COPY --from=modules /go/pkg /go/pkg
COPY . /app

WORKDIR /app

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags migrate -o /bin/app ./cmd/app && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /bin/backfill ./cmd/backfill && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /bin/rag-eval ./cmd/rag-eval && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /bin/import-books ./cmd/import-books

# Step 3: Final
FROM scratch

COPY --from=builder /app/config /config
COPY --from=builder /app/migrations /migrations
COPY --from=builder /bin/app /app
# F1-H: ops-only resumable backfill CLI, run via
# `docker compose exec app /backfill -job=...` (docs/data-change-playbook.md).
COPY --from=builder /bin/backfill /backfill
# K-1 rollout gate: the same immutable image carries its Book-RAG smoke/golden
# cases, so a dual/unit promotion cannot depend on tools installed on the VPS.
COPY --from=builder /bin/rag-eval /rag-eval
COPY --from=builder /app/eval /eval
# E4/B-1: ops-only staged importer (safe by default: removals stage, never
# delete), run via `docker compose run --rm --no-deps -v <src>:/shamela
# --entrypoint /import-books app -book-ids=... -source-dir=/shamela`.
COPY --from=builder /bin/import-books /import-books
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

CMD ["/app"]
