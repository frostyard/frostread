.PHONY: help build fmt vet lint lint-version-check test race cross tidy-check verify check ci contract-update web-build web-check web-fmt lodge-package dev-seed dev-lodge clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath
# TAGS adds Go build tags, such as devfetch for the development escape
# (make dev-lodge). A release build leaves it empty.
TAGS ?=
TAGFLAGS := $(if $(TAGS),-tags '$(TAGS)')
# golangci-lint's release, read from mise.toml (frostyard/core ADR-0043).
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16

build: ## Build readd, readctl and the development commands into build/
	CGO_ENABLED=0 go build $(TAGFLAGS) $(GOFLAGS) -ldflags '$(LDFLAGS)' -o build/ ./cmd/...

fmt: ## Format Go code
	gofmt -w .

vet: ## Vet Go code
	go vet ./...

lint-version-check:
	@v=$$(golangci-lint version 2>/dev/null | sed -n 's/.*version \([0-9.]*\).*/\1/p'); \
	[ "$$v" = "$(GOLANGCI_LINT_VERSION)" ] || echo "warning: golangci-lint $$v, mise.toml pins $(GOLANGCI_LINT_VERSION) (mise install)"

lint: lint-version-check ## Run the pinned golangci-lint
	golangci-lint run

test: ## Run the Go tests, the app contract's socket rules among them
	go test ./...

race: ## Run the Go tests under the race detector
	go test -race ./...

cross: ## Build for linux/amd64 and linux/arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o /dev/null ./cmd/...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o /dev/null ./cmd/...

tidy-check: ## Fail if go.mod or go.sum is not tidy
	go mod tidy -diff

verify: tidy-check vet lint test dev-check ## The gate without formatting: tidy, vet, lint, tests and the dev tags
	test -z "$$(gofmt -l .)"

# The development tags are vetted and tested too, so they do not rot:
# devfetch (the fetcher's loopback escape) and devfeeds (cmd/readdev).
dev-check:
	go vet -tags 'devfetch devfeeds' ./...
	go test -tags 'devfetch devfeeds' ./cmd/readdev/ ./internal/fetch/

# The daemon's answers are the web app's contract: internal/readapi's
# contract test writes them to web/src/rpc/testdata, and the web app's
# Vitest test checks them against its types and components. `make check`
# fails when a golden is stale; this rewrites them.
contract-update: ## Rewrite web/src/rpc/testdata from the daemon's seeded answers
	go test ./internal/readapi -run TestContract -update

check: fmt verify ## Format, then verify

ci: verify race cross web-check ## verify, the race detector, cross-builds and the web bundle (nsl)

# The web bundle checks and builds in an nsl machine with Node (lodge's
# dev/nsl/provision.sh), never on the host, for lodge's /app/read/.
NSL_MACHINE ?= lodge
IN_NSL ?= nsl run -m $(NSL_MACHINE) sh -lc
FROZEN = $(if $(wildcard web/pnpm-lock.yaml),--frozen-lockfile,)

web-check: ## Lint (Biome), typecheck and test web/ (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run check'

web-fmt: ## Format web/ and organize its imports with Biome (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run fmt'

web-build: ## Build the web bundle for lodge's /app/read/ into build/web (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run build'

# lodge-package stages the app under LODGE_DESTDIR in lodge's install
# layout, with packaging/lodge/stage.sh. It replaces only this app's own
# files, so LODGE_DESTDIR may be a lodge root other apps share; it refuses
# an empty one, or /.
LODGE_DESTDIR ?= build/lodge-root
lodge-package: build ## Stage readd, readctl, the manifest, units and web build for lodge under LODGE_DESTDIR
	packaging/lodge/stage.sh "$(LODGE_DESTDIR)" "$(VERSION)" build

# dev-seed prints the OPML for the development feeds cmd/readdev serves.
dev-seed: ## Print the development OPML (the devfeeds build tag)
	go run -tags devfeeds ./cmd/readdev -opml

# dev-lodge starts lodge's development build on loopback with Read and
# Notes, generated data only, so save-as-note works end to end: readd is
# built with -tags devfetch, and the development feeds with -tags devfeeds.
# Another checkout is read, never changed; see scripts/dev-lodge.sh.
dev-lodge: ## Start lodge, Read and Notes on loopback with generated data
	scripts/dev-lodge.sh

clean: ## Remove build output
	rm -rf build
