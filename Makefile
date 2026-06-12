SHELL := /bin/bash
DATABASE_URL ?= postgres://rmm:rmm-dev-only@localhost:5432/rmm?sslmode=disable

.PHONY: build build-web test test-integration lint vet proto migrate-up migrate-down dev-stack dev-stack-down dev e2e clean agent-binaries agent-packages

build:
	cd server && go build ./...
	cd agent && CGO_ENABLED=0 go build ./...
	cd shared && go build ./...

build-web:
	cd web && npm ci && npm run build

test:
	cd server && go test ./...
	cd agent && go test ./...
	cd shared && go test ./...

# Integration tests (RLS tenant-isolation probes, full API flows) need a
# real Postgres; they reset the schema of the target database.
test-integration: dev-stack
	cd server && RMM_TEST_DATABASE_URL="$(DATABASE_URL)" go test ./internal/api/ -run TestAPIIntegration -v

vet:
	cd server && go vet ./...
	cd agent && go vet ./...
	cd shared && go vet ./...

lint: vet
	@command -v staticcheck >/dev/null && \
		(cd server && staticcheck ./... && cd ../agent && staticcheck ./...) || \
		echo "staticcheck not installed; skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"

proto:
	cd proto && buf lint && buf generate

migrate-up:
	migrate -path server/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path server/migrations -database "$(DATABASE_URL)" down 1

dev-stack:
	docker compose up -d --wait

dev-stack-down:
	docker compose down

# dev starts the backend (port 8080) and the Vite HMR dev server (port
# 5173) together. Open http://localhost:5173 in your browser; API
# requests are proxied to the backend automatically by vite.config.ts.
# Requires dev-stack to be running first (make dev-stack).
dev: dev-stack
	@trap 'kill 0' EXIT; \
	RMM_DATABASE_URL="$(DATABASE_URL)" \
	RMM_APP_ROLE=rmm_app \
	RMM_MASTER_KEY=$$(grep RMM_MASTER_KEY .env 2>/dev/null | cut -d= -f2 || echo "0000000000000000000000000000000000000000000000000000000000000000") \
	RMM_COOKIE_SECURE=false \
	go run ./server/cmd/rmmserver & \
	cd web && npm run dev & \
	wait

e2e: dev-stack
	@echo "e2e harness lands in M2 (agent enrollment round-trip)"

# Cross-compile static agent binaries (linux amd64/arm64, windows amd64).
agent-binaries:
	agent/packaging/build.sh --bin-only

# Build the agent binaries and package them as .deb and .rpm (needs nfpm:
# go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest). Artifacts land
# in agent/packaging/dist/.
agent-packages:
	agent/packaging/build.sh

clean:
	rm -rf bin/ agent/packaging/dist/
