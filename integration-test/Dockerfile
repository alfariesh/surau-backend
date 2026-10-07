# Step 1: Modules caching
FROM golang:1.26.8-alpine3.23 AS modules

COPY go.mod go.sum /modules/

WORKDIR /modules

RUN go mod download

# Step 2: Tests
FROM golang:1.26.8-alpine3.23

COPY --from=modules /go/pkg /go/pkg
COPY . /app

WORKDIR /app

# go.mod's toolchain line is the single Go version source (CI reads it through
# setup-go's go-version-file). The golang image sets GOTOOLCHAIN=local, so a
# base image that drifted from that line would silently compile with its own
# Go, as golang:1.26.4 did while go.mod pinned go1.26.5. Refuse instead.
RUN expected="$(sed -n 's/^toolchain //p' go.mod)" && \
    actual="$(go env GOVERSION)" && \
    if [ "$actual" != "$expected" ]; then \
      echo "image runs $actual but go.mod pins ${expected:-no toolchain}" >&2; \
      exit 1; \
    fi

RUN go env -w CGO_ENABLED=0 \
    && go env -w GOOS=linux  \
    && go env -w GOARCH=amd64

CMD ["go", "test", "-v", "./integration-test/..."]
