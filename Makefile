.PHONY: run test lint build docker up

run:
	go run ./cmd/zato-entry

test:
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -o bin/zato-entry ./cmd/zato-entry

docker:
	docker build -t zato-entry .

# Запуск вместе с PostgreSQL (нужен файл .env, см. .env.example).
up:
	docker compose up --build
