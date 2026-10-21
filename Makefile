SHELL := /bin/bash
BIN := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/axmishell/mikvacepanel/internal/version.Version=$(VERSION) \
	-X github.com/axmishell/mikvacepanel/internal/version.Commit=$(COMMIT) \
	-X github.com/axmishell/mikvacepanel/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all web server node client build test fmt vet clean docker docker-node dev

all: build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

server:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/mikvace-server ./cmd/server

node:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/mikvace-node ./cmd/node

client:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/mikvace-client ./cmd/client

# The server embeds web/dist, so the frontend must be built first.
build: web server node client

test:
	go test ./...

fmt:
	gofmt -w cmd internal web/embed.go

vet:
	go vet ./...

dev:
	go run ./cmd/server -addr :8080 -data ./data

docker:
	docker build -t mikvacepanel/panel:latest \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) .

docker-node:
	docker build -f Dockerfile.node -t ghcr.io/axmishell/mikvacepanel-node:main \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) .

clean:
	rm -rf $(BIN) web/dist
