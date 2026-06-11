SHELL := /bin/bash
DATABASE_URL ?= postgres://rmm:rmm-dev-only@localhost:5432/rmm?sslmode=disable

.PHONY: build test lint vet proto migrate-up migrate-down dev-stack dev-stack-down e2e clean

build:
	cd server && go build ./...
	cd agent && CGO_ENABLED=0 go build ./...
	cd shared && go build ./...

test:
	cd server && go test ./...
	cd agent && go test ./...
	cd shared && go test ./...

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

clean:
	rm -rf bin/
