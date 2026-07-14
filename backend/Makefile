# Writing Agent V2 — Backend Makefile

.PHONY: build run dev test clean

# Go binary
GO=go

# Binary output
BIN=bin/server

build:
	$(GO) build -o $(BIN) ./cmd/server/

run:
	$(GO) run ./cmd/server/

dev:
	$(GO) run ./cmd/server/ &

test:
	$(GO) test ./... -v -count=1

clean:
	rm -rf bin/

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...
