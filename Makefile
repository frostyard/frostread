.PHONY: help build fmt vet lint lint-version-check test race cross vulncheck tidy-check verify check ci contract-update web-build web-check web-fmt app-check app-test app-build app-e2e flatpak-smoke lodge-package dev-seed dev-lodge svu-version-check bump deb deb-check clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath
# TAGS adds Go build tags, such as devfetch for the development escape
# (make dev-lodge). A release build leaves it empty.
TAGS ?=
TAGFLAGS := $(if $(TAGS),-tags '$(TAGS)')
# golangci-lint's release, read from mise.toml (frostyard/core ADR-0043).
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
# svu's release, read from mise.toml too; make bump checks it.
SVU_VERSION := $(strip $(shell sed -n 's/^svu = "\(.*\)"/\1/p' mise.toml))
SVU ?= svu

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16

build: ## Build readd, readctl and the development commands into build/
	CGO_ENABLED=0 go build $(TAGFLAGS) $(GOFLAGS) -ldflags '$(LDFLAGS)' -o build/ ./cmd/...

fmt: ## Format Go code
	gofmt -w .

vet: ## Vet Go code
	go vet ./...

lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (install with: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (install with: mise install)"; exit 1; \
	fi

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

# govulncheck's release, pinned here and not in go.mod (it needs the network).
GOVULNCHECK_VERSION := v1.9.0

vulncheck: ## Report reachable vulnerabilities (govulncheck; needs the network)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: flatpak-sources
flatpak-sources: ## Regenerate the Flatpak's offline Go, cargo and pnpm sources
	scripts/flatpak-sources.sh

ci: verify race cross web-check app-check app-build ## verify, the race detector, cross-builds, the web bundle and the app (nsl)

# The web bundle checks and builds in an nsl machine with Node, never on
# the host, for lodge's /app/read/: frostnotes' machine (its
# dev/nsl/provision.sh), as no lodge machine exists (plan 0002, step 3.0).
NSL_MACHINE ?= frostnotes
# The desktop app's shell needs Rust and WebKitGTK's development files too:
# frostnotes' nsl machine has both (plan 0002, step 3.0).
APP_NSL_MACHINE ?= frostnotes
IN_NSL ?= nsl run -m $(NSL_MACHINE) sh -lc
IN_APP_NSL ?= nsl run -m $(APP_NSL_MACHINE) sh -lc
# The shell's build output stays in the machine, never in the checkout.
APP_TARGET_DIR ?= /var/tmp/frostread/target
FROZEN = $(if $(wildcard web/pnpm-lock.yaml),--frozen-lockfile,)

web-check: ## Lint (Biome), typecheck and test web/ (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run check'

web-fmt: ## Format web/ and organize its imports with Biome (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run fmt'

web-build: ## Build the web bundle for lodge's /app/read/ into build/web (nsl)
	$(IN_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run build'

# The desktop app (plan 0002, Phase 3): the shell in web/src-tauri over the
# same web project's desktop entry. Its Rust build needs build/desktop, so
# the bundle comes first. The release binary lands in build/frostread-app.
app-check: ## Lint (Biome), typecheck and test web/, then fmt, clippy and test the shell (nsl)
	$(IN_APP_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run check && pnpm run build:desktop'
	$(IN_APP_NSL) 'export CARGO_TARGET_DIR=$(APP_TARGET_DIR); cd web/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && dbus-run-session -- env READ_REQUIRE_SESSION_BUS=1 cargo test'

app-test: ## Build the desktop bundle and run the shell's Rust tests (nsl)
	$(IN_APP_NSL) 'cd web && pnpm install $(FROZEN) --silent && pnpm run build:desktop'
	$(IN_APP_NSL) 'export CARGO_TARGET_DIR=$(APP_TARGET_DIR); cd web/src-tauri && dbus-run-session -- env READ_REQUIRE_SESSION_BUS=1 cargo test'

app-build: ## Build the release app binary into build/frostread-app (nsl)
	$(IN_APP_NSL) 'export CARGO_TARGET_DIR=$(APP_TARGET_DIR); cd web && pnpm install $(FROZEN) --silent && pnpm tauri build && mkdir -p ../build && cp "$$CARGO_TARGET_DIR/release/frostread" ../build/frostread-app'

flatpak-smoke: ## Run the Flatpak's smoke test on the host with host-built readd and readctl
	rm -rf build/flatpak-smoke
	mkdir -p build/flatpak-smoke
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o build/flatpak-smoke/ ./cmd/readd ./cmd/readctl
	cp packaging/flatpak/frostread.sh build/flatpak-smoke/frostread
	chmod 755 build/flatpak-smoke/frostread
	FROSTREAD_BIN_DIR="$$(pwd)/build/flatpak-smoke" sh packaging/flatpak/smoke-test.sh

# app-e2e drives the built app in WebKitGTK against readd's desktop mode on
# readdev's generated feeds, headless in the nsl machine (plan 0002, step
# 3.6). The Go binaries are static, so the machine runs the host's build;
# readd needs the devfetch tag and readdev the devfeeds one. The leftover
# check runs in the same shell as Vitest, so a failed test run still reports
# a process the run left, and Vitest's own failure stays the target's. It
# skips a zombie (state Z), because that process has exited and only waits
# for PID 1 to reap it.
app-e2e: app-build ## Drive the built app in WebKitGTK against readd and readdev, headless (nsl); F= picks tests
	CGO_ENABLED=0 go build -tags 'devfetch devfeeds' $(GOFLAGS) -o build/e2e/ ./cmd/readd ./cmd/readctl ./cmd/readdev
	$(IN_APP_NSL) 'cd web && pnpm install $(FROZEN) --silent && env -u WAYLAND_DISPLAY GDK_BACKEND=x11 xvfb-run -a -s "-screen 0 1280x800x24" pnpm exec vitest run -c vitest.e2e.config.ts $(F); rc=$$?; if pgrep -af -r D,I,R,S,T,t "[b]uild/e2e/|[f]rostread-app|[W]ebKitWebDriver"; then echo "app-e2e: a process of the run is left" >&2; rc=1; fi; exit $$rc'

.PHONY: screenshots
screenshots: app-build ## Take the 1280x800 Flatpak metainfo screenshots with made-up feeds (nsl)
	CGO_ENABLED=0 go build -tags 'devfetch devfeeds' $(GOFLAGS) -o build/e2e/ ./cmd/readd ./cmd/readctl ./cmd/readdev
	CGO_ENABLED=0 go build -tags devfeeds $(GOFLAGS) -o build/e2e/screenshots-server ./dev/screenshots
	$(IN_APP_NSL) 'if curl -fs -o /dev/null http://127.0.0.1:8099/; then echo "screenshots: port 8099 is in use" >&2; exit 1; fi; cd web && pnpm install $(FROZEN) --silent && env -u WAYLAND_DISPLAY GDK_BACKEND=x11 xvfb-run -a -s "-screen 0 1280x800x24" pnpm exec vitest run -c vitest.screenshots.config.ts; rc=$$?; if pgrep -af -r D,I,R,S,T,t "[b]uild/e2e/|[f]rostread-app|[W]ebKitWebDriver|[s]creenshots-server"; then echo "screenshots: a process of the run is left" >&2; rc=1; fi; exit $$rc'

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

# make deb builds the release package (frostyard/core ADR-0011, ADR-0012,
# ADR-0058): the daemon's binaries into build/deb/in, the tree staged from
# scratch into build/deb/root by packaging/lodge/stage.sh, and nFPM's
# dist/frostyard-readd_<DEB_VERSION>_amd64.deb. A snapshot version starts with 0.0.0-
# (ADR-0012); a release passes DEB_VERSION=X.Y.Z, the tag without its v.
DEB_PKG := frostyard-readd
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DEB_VERSION ?= 0.0.0-$(COMMIT)-snapshot
DEB_FILE = dist/$(DEB_PKG)_$(DEB_VERSION)_amd64.deb

deb: ## Build dist/frostyard-readd_<DEB_VERSION>_amd64.deb with nFPM, after the web build
	@test -f build/web/index.html || { echo "no web build in build/web: run make web-build first"; exit 1; }
	rm -rf build/deb
	mkdir -p build/deb/in dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X main.version=$(DEB_VERSION)' -o build/deb/in/ ./cmd/readd ./cmd/readctl
	cp -R build/web build/deb/in/web
	packaging/lodge/stage.sh build/deb/root "$(DEB_VERSION)" build/deb/in
	chmod -R u=rwX,go=rX build/deb/root
	DEB_VERSION="$(DEB_VERSION)" nfpm package -p deb -f packaging/nfpm.yaml -t dist/
	$(MAKE) --no-print-directory deb-check DEB_VERSION="$(DEB_VERSION)"

deb-check: ## Check the .deb make deb built: its fields, its paths (only /usr), its owners and modes, its payload
	packaging/deb-check.py "$(DEB_FILE)" $(DEB_PKG) "$(DEB_VERSION)" \
		--exec /usr/bin/readd --exec /usr/bin/readctl \
		--file /usr/lib/systemd/system/frostread.socket \
		--file /usr/lib/systemd/system/frostread.service \
		--file /usr/lib/sysusers.d/frostread.conf \
		--file /usr/share/lodge/apps/read/web/index.html \
		--manifest /usr/share/lodge/apps/read/app.json

svu-version-check:
	@test -n "$(SVU_VERSION)" || { echo "mise.toml pins no svu"; exit 1; }
	@if ! command -v "$(SVU)" >/dev/null 2>&1; then \
		echo "svu $(SVU_VERSION) is required for make bump (not installed; run: mise install)"; \
		exit 1; \
	fi; \
	installed="$$($(SVU) --version 2>&1 | sed -n 's/^GitVersion:[[:space:]]*//p' | head -1)"; \
	if [ "$$installed" != "$(SVU_VERSION)" ]; then \
		echo "expected svu $(SVU_VERSION), found $${installed:-unknown} (run: mise install)"; \
		exit 1; \
	fi

# bump is the only sanctioned way to tag a release (ADR-0012): on main, equal
# to origin/main, with a clean tree and a passing gate, it tags svu's next
# version and pushes the tag, which starts the Release workflow.
bump: svu-version-check ## Tag the next version with svu and push the tag (main, clean, up to date, gate passing)
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "make bump runs on main"; exit 1; }
	@git fetch origin main
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || { echo "HEAD is not origin/main"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "the working tree is not clean"; exit 1; }
	$(MAKE) check
	@test -z "$$(git status --porcelain)" || { echo "the gate changed the tree (formatting?); commit it first"; exit 1; }
	@set -eu; \
	version=$$($(SVU) next); \
	printf '%s\n' "$$version" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+' || { echo "svu next gave '$$version', not vX.Y.Z"; exit 1; }; \
	git tag -a "$$version" -m "Version $$version"; \
	echo "Tagged $$version; pushing it to origin..."; \
	git push origin "refs/tags/$$version"

clean: ## Remove build output
	rm -rf build dist
