.PHONY: run build test vet redis logo-sync build-linux install deploy

# --- deploy (systemd on the VPS) ---------------------------------------
# On the server (repo cloned there):   make install
# From the dev machine over ssh:       make deploy DEPLOY_HOST=me@1.2.3.4
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

# Run ON the server after `git pull`: native build, swap binaries in via
# rename (overwriting a running binary in place fails with ETXTBSY; rename
# is atomic and always safe), then restart the service. Run as root or
# prefix with sudo.
install:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN_DIR)/livescore-api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN_DIR)/livescore-logo-sync ./cmd/logo-sync
	mv $(BIN_DIR)/livescore-api $(DEPLOY_DIR)/livescore-api
	mv $(BIN_DIR)/livescore-logo-sync $(DEPLOY_DIR)/livescore-logo-sync
	systemctl restart livescore
	systemctl --no-pager status livescore

# Same flow from the dev machine over ssh (needs $(DEPLOY_DIR)/.incoming/ on
# the server and a systemd unit already installed).
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
