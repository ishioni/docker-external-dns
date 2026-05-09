BINARY := docker-external-dns
CMD     := ./cmd/docker-external-dns

# Load .env if it exists — lets you set UNIFI_HOST, UNIFI_API_KEY, etc. once
# and use all targets without re-typing credentials.
ifneq (,$(wildcard .env))
  include .env
  export
endif

.PHONY: build run vet test clean docker-build docker-run

## build: compile the binary to ./docker-external-dns
build:
	go build -o $(BINARY) $(CMD)

## run: run the agent against your real UniFi controller (reads .env)
run:
	go run $(CMD)

## docker-run: build image and run via docker compose (production)
docker-run:
	docker compose up --build

## vet: run go vet
vet:
	go vet ./...

## test: run unit tests
test:
	go test ./...

## docker-build: build the Docker image
docker-build:
	docker build -t $(BINARY) .

## clean: remove the compiled binary
clean:
	rm -f $(BINARY)

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
