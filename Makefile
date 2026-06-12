SHELL := /bin/bash
DATABASE_URL ?= postgres://rmm:rmm-dev-only@localhost:5432/rmm?sslmode=disable

.PHONY: build test test-integration lint vet proto migrate-up migrate-down dev-stack dev-stack-down e2e clean agent-binaries agent-packages

build:
	cd server && go build ./...
	cd agent && CGO_ENABLED=0 go build ./...
	cd shared && go build ./...

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

e2e: dev-stack
	@echo "e2e harness lands in M2 (agent enrollment round-trip)"

# Cross-compile static agent binaries for linux amd64/arm64.
agent-binaries:
	agent/packaging/build.sh --bin-only

# Build the agent binaries and package them as .deb and .rpm (needs nfpm:
# go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest). Artifacts land
# in agent/packaging/dist/.
agent-packages:
	agent/packaging/build.sh

clean:
	rm -rf bin/ agent/packaging/dist/
