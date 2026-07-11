ifneq ($(wildcard .env),)
include .env
export
else
$(warning WARNING: .env file not found! Using .env.example)
include .env.example
export
endif

BASE_STACK = docker compose -f docker-compose.yml
INTEGRATION_TEST_STACK = $(BASE_STACK) -f docker-compose-integration-test.yml
ALL_STACK = $(INTEGRATION_TEST_STACK)

# HELP =================================================================================================================
# This will output the help for each task
# thanks to https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
.PHONY: help

help: ## Display this help screen
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

compose-up: ### Run docker compose (without backend and reverse proxy)
	$(BASE_STACK) up --build -d db && docker compose logs -f
.PHONY: compose-up

compose-up-all: ### Run docker compose (with backend and reverse proxy)
	$(BASE_STACK) up --build -d
.PHONY: compose-up-all

compose-up-integration-test: ### Run docker compose with integration test
	$(INTEGRATION_TEST_STACK) up --build --abort-on-container-exit --exit-code-from integration-test db app integration-test; exit_code=$$?; \
	$(INTEGRATION_TEST_STACK) down --remove-orphans; exit $$exit_code
.PHONY: compose-up-integration-test

compose-down: ### Down docker compose
	$(ALL_STACK) down --remove-orphans
.PHONY: compose-down

swag-v1: ### swag init
	go tool swag init --parseDependency --templateDelims "[[,]]" -g internal/controller/restapi/router.go
.PHONY: swag-v1

deps: ### deps tidy + verify
	go mod tidy && go mod verify
.PHONY: deps

deps-audit: ### check dependencies vulnerabilities
	govulncheck ./...
.PHONY: deps-audit

fix-diff: ### Show code changes by `go fix`
	go fix -diff ./...
.PHONY: fix-diff

format: ### Run code formatter
	go fix ./...
	gofumpt -l -w .
	gci write . --skip-generated -s standard -s default
.PHONY: format

run: deps ### run API v1
	go mod download && \
	CGO_ENABLED=0 go run -tags migrate ./cmd/app
.PHONY: run

import-books-sample: ### Import a small reader sample from raw database
	go run ./cmd/import-books --book-ids=797 --release-key=sample
.PHONY: import-books-sample

import-reader-assets: ### Import translation/audio JSONL: make import-reader-assets FILE=assets.jsonl
	go run ./cmd/import-reader-assets --file='$(FILE)'
.PHONY: import-reader-assets

import-quran-assets: ### Import local QUL Quran exports: make import-quran-assets QUL_ARGS='--dry-run ...'
	go run ./cmd/import-quran-assets $(QUL_ARGS)
.PHONY: import-quran-assets

sync-quran-audio-r2: ### Sync Quran audio R2 manifest into Postgres: make sync-quran-audio-r2 QURAN_AUDIO_R2_ARGS='--dry-run ...'
	go run ./cmd/sync-quran-audio-r2 $(QURAN_AUDIO_R2_ARGS)
.PHONY: sync-quran-audio-r2

docker-rm-volume: ### remove docker volume
	docker volume rm surau-backend_pg-data
.PHONY: docker-rm-volume

linter-golangci: ### check by golangci linter
	golangci-lint run --new-from-merge-base=origin/main
.PHONY: linter-golangci

linter-hadolint: ### check by hadolint linter
	git ls-files --exclude='Dockerfile*' --ignored | xargs hadolint
.PHONY: linter-hadolint

linter-dotenv: ### check by dotenv linter
	dotenv-linter
.PHONY: linter-dotenv

test: ### run test
	go test -v -race -covermode atomic -coverpkg=./internal/...,./pkg/... -coverprofile=coverage.txt ./internal/... ./pkg/...
.PHONY: test

normalization-contract: ### Run the shared Go/Python Arabic search-key v1 corpus
	go test ./internal/quranutil ./internal/searchtext
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts.langextract_kg.test_arabic_normalize
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts.test_check_normalization_contract
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_normalization_contract.py
.PHONY: normalization-contract

diff-cover: ### coverage of new code vs origin/main (the CI ratchet, F1-E); uses live coverage when available
	@profiles="-profile coverage.txt"; \
	if [ -f coverage-live.txt ]; then profiles="$$profiles -profile coverage-live.txt"; fi; \
	git diff -U0 --no-color origin/main...HEAD | go run ./cmd/diffcover $$profiles
.PHONY: diff-cover

integration-test: ### run integration-test
	go clean -testcache && go test -v ./integration-test/...
.PHONY: integration-test

mock: ### run mockgen
	mockgen -source ./internal/repo/contracts.go -package usecase_test > ./internal/usecase/mocks_repo_test.go
	mockgen -source ./internal/usecase/contracts.go -package usecase_test > ./internal/usecase/mocks_usecase_test.go
.PHONY: mock

migrate-create:  ### create new migration
	migrate create -ext sql -dir migrations '$(word 2,$(MAKECMDGOALS))'
.PHONY: migrate-create

migrate-up: ### migration up
	migrate -path migrations -database '$(PG_URL)?sslmode=disable' up
.PHONY: migrate-up

bin-deps: ### install tools
	go install tool
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate
.PHONY: bin-deps

pre-commit: deps format linter-golangci test normalization-contract ### run pre-commit
.PHONY: pre-commit
