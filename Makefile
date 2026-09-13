BINARY  := qoget
MODULE  := github.com/davidetoniatti/qoget
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install test lint fmt vet check clean

all: check build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/qoget

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/qoget

test:
	go test -race ./...

fmt:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }

vet:
	go vet ./...

check: fmt vet test

clean:
	rm -f $(BINARY)
	rm -rf dist
