.PHONY: run build test vet redis logo-sync

run:
	go run ./cmd/api

# Standalone logo backfill (Mongo + R2 only; no Redis/thscore needed).
# Idempotent — safe to re-run anytime to retry failed downloads.
logo-sync:
	go run ./cmd/logo-sync

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

redis:
	docker compose up -d redis
