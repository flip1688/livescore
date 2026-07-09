.PHONY: run build test vet redis logo-sync build-linux deploy

# --- deploy (systemd on the VPS) ---------------------------------------
# Override per invocation or export in your shell profile:
#   make deploy DEPLOY_HOST=me@1.2.3.4 DEPLOY_DIR=/opt/livescore
DEPLOY_HOST ?= user@your-server
DEPLOY_DIR  ?= /opt/livescore
GOARCH      ?= amd64
BIN_DIR     := bin

run:
	go run ./cmd/api

# Standalone logo backfill (Mongo + R2 only; no Redis/thscore needed).
# Idempotent — safe to re-run anytime to retry failed downloads.
logo-sync:
	go run ./cmd/logo-sync

build:
	go build ./...

# Static linux binaries for the VPS (api + logo-sync).
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags='-s -w' -o $(BIN_DIR)/livescore-api ./cmd/api
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags='-s -w' -o $(BIN_DIR)/livescore-logo-sync ./cmd/logo-sync

# Upload to .new then rename: overwriting a running binary in place fails
# with ETXTBSY on Linux, while rename is atomic and always safe.
deploy: build-linux
	scp $(BIN_DIR)/livescore-api $(BIN_DIR)/livescore-logo-sync $(DEPLOY_HOST):$(DEPLOY_DIR)/.incoming/
	ssh $(DEPLOY_HOST) 'mv $(DEPLOY_DIR)/.incoming/livescore-api $(DEPLOY_DIR)/livescore-api \
		&& mv $(DEPLOY_DIR)/.incoming/livescore-logo-sync $(DEPLOY_DIR)/livescore-logo-sync \
		&& sudo systemctl restart livescore'

test:
	go test ./...

vet:
	go vet ./...

redis:
	docker compose up -d redis
