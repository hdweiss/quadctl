# Task runner (PLAN.md 6.6). Deliberately thin: `go build ./...`, `go vet ./...` and
# `go test ./...` are still the build system, and anything here that only spells one of them
# differently would be noise. What earns a target is a command with arguments worth not
# retyping - the lint and release tools, the coverage dance, the local cross-build.
#
# Both tools are go.mod tool dependencies, so `go tool <name>` is the pinned version and there
# is nothing to install first.

BIN        := quadctl
DIST       := dist
COVERAGE   := coverage.out

# Stamped into a `make build` binary so a locally built quadctl --version says which commit it
# is, the way a released one does. GoReleaser sets the same three for real releases.
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse HEAD 2>/dev/null)
DATE       := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.DEFAULT_GOAL := help

## help: list the targets
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | awk -F': ' '{printf "  \033[1m%-14s\033[0m %s\n", $$1, $$2}'

## build: build ./quadctl with version information stamped in
build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) .

## test: run the tests with the race detector, as CI does
test:
	go test -race ./...

## cover: run the tests and open the per-function coverage summary
cover:
	go test -coverprofile=$(COVERAGE) ./...
	go tool cover -func=$(COVERAGE)
	@echo
	@echo "HTML report: go tool cover -html=$(COVERAGE)"

## vet: go vet
vet:
	go vet ./...

## lint: golangci-lint, at the version pinned in go.mod
lint:
	go tool golangci-lint run

## fmt: rewrite anything gofmt disagrees with
fmt:
	gofmt -w -l .

## check: everything CI runs, in CI's order
check: build vet lint test

## golden: regenerate internal/command/testdata/commands.golden
golden:
	go test ./internal/command/ -run TestGenerateCommandsGolden -update

## snapshot: cross-build every release archive into ./dist without publishing
snapshot:
	go tool goreleaser release --snapshot --clean --skip=publish

## release-check: validate .goreleaser.yaml
release-check:
	go tool goreleaser check

## clean: remove build output
clean:
	rm -rf $(DIST) $(BIN) $(COVERAGE)

.PHONY: help build test cover vet lint fmt check golden snapshot release-check clean
