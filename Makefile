.PHONY: help build fmt vet test check web-build lodge-package clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16

build: ## Build readd into build/
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o build/ ./cmd/...

fmt: ## Format Go code
	gofmt -w .

vet: ## Vet Go code
	go vet ./...

test: ## Run the Go tests, the app contract's socket rules among them
	go test ./...

check: fmt vet test ## Format, vet and test

# The web bundle builds in an nsl machine with Node (lodge's
# dev/nsl/provision.sh), never on the host, for lodge's /app/read/.
NSL_MACHINE ?= lodge
IN_NSL ?= nsl run -m $(NSL_MACHINE) sh -lc
FROZEN = $(if $(wildcard web/pnpm-lock.yaml),--frozen-lockfile,)

web-build: ## Build the web bundle for lodge's /app/read/ into build/web (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm exec tsc --noEmit && pnpm exec vite build --outDir ../build/web --emptyOutDir'

# lodge-package stages the app under LODGE_DESTDIR in lodge's install
# layout, with packaging/lodge/stage.sh. It replaces only this app's own
# files, so LODGE_DESTDIR may be a lodge root other apps share; it refuses
# an empty one, or /.
LODGE_DESTDIR ?= build/lodge-root
lodge-package: build ## Stage readd, its manifest, units and web build for lodge under LODGE_DESTDIR
	packaging/lodge/stage.sh "$(LODGE_DESTDIR)" "$(VERSION)" build

clean: ## Remove build output
	rm -rf build
