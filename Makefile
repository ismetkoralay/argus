.PHONY: run build test lint up down tidy

run:
	go run ./cmd/service

build:
	CGO_ENABLED=0 go build -o bin/service ./cmd/service

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

up:
	docker compose up --build -d

down:
	docker compose down -v
