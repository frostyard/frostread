# frostyard/frostread

frostread is a tailnet-only feed reader and read-later list. It runs behind
[lodge](https://github.com/frostyard/lodge), frostyard's tailnet-only web
shell, as app `read`: the `readd` daemon on lodge's app socket, and a web
bundle lodge serves at `/app/read/`. It polls RSS, Atom and JSON Feed
sources, saves links to read later, extracts and sanitizes each article,
and keeps the original bytes as sent. Agents reach it through `/mcp/read`
with a scoped token. It is the third first-party app beside frostmail and
frostnotes. Start at [docs/README.md](docs/README.md). The architecture is
in [docs/design/overview.md](docs/design/overview.md), and the contract with
lodge is lodge's
[app-contract.md](https://github.com/frostyard/lodge/blob/main/docs/specs/app-contract.md).

This file (`AGENTS.md`) is the CANONICAL agent instructions. `CLAUDE.md`,
`GEMINI.md` and `.github/copilot-instructions.md` are symlinks to it, and
`.claude/skills` symlinks to `.agents/skills/`
([ADR-0002](docs/adr/0002-agent-portable-instruction-surface.md)). Edit only
the canonical paths, and keep content tool-agnostic.

## Status

Pre-release, private, 2026-10-21. Built: the daemon and its store, the
guarded fetcher, feeds and OPML, extraction and the blob store, polling,
search, the JSON-RPC API, `readctl`, MCP with scoped tokens, save-as-note,
the content pages, the units, and the development feeds. Next: the web app
([roadmap](docs/plans/0001-roadmap.md), Phase 5). The founding documents are
Proposed (2026-10-21).
Lodge, frostnotes and frostmail are sibling checkouts at `../lodge`,
`../frostnotes` and `../frostmail` when present. Before writing frostread's
counterpart of something those repositories do, read their file.

## Map

- `cmd/readd`: the daemon, with the app contract's socket rules as tests
  (kittest). It serves lodge's app socket, its private socket for `readctl`,
  the content pages on `/content/`, and MCP on `/mcp`.
- `cmd/readctl`: the CLI, over the private socket.
- `api/`, `internal/rpcserver`: the JSON-RPC API on `kit/jsonrpc`.
  `schema/rpc` is the contract, and `api/*.go` follow it by hand.
- `internal/store`: the SQLite database: sources, items, blobs, full-text
  search and settings. Migrations by `user_version`.
- `internal/blobs`: the content-addressed file store under
  `STATE/blobs/sha256/`.
- `internal/fetch`: the only HTTP client, built on lodge's `kit/netguard`.
- `internal/feed`: feeds, on `gofeed`, and feed discovery.
- `internal/opml`: OPML import and export.
- `internal/sanitize`: the article policy, on `bluemonday`.
- `internal/extract`: readability extraction, on
  `codeberg.org/readeck/go-readability/v2`, and the images it inlines.
- `internal/engine`: the app's logic over the packages above, the polls
  themselves, and the extraction queue.
- `internal/poller`: the poll loop's timing alone.
- `internal/readapi`: the JSON-RPC contract over the engine, and the codes
  its errors become.
- `internal/content`: the `lodgeapp.Server` content handler.
- `internal/mcpserver`: MCP, on the MCP SDK, with the token's scope.
- `internal/notes`: save-as-note, through notes' MCP on lodge's agent
  socket.
- `internal/config`: `/etc/frostread/frostread.json` and the state
  directory.
- `scripts/dev-lodge.sh`: one command, `make dev-lodge`, that starts
  lodge's development build on loopback with Read and Notes, generated
  feeds, a generated vault and Read's scoped notes token. It reads the
  `../lodge` and `../frostnotes` checkouts and changes neither.
- `web/`: the web bundle on `@frostyard/lodge-client`.
- `packaging/lodge`: the manifest, the socket and service units, the system
  user, and `stage.sh`.

## Gates

- `make check` formats, then runs `make verify`: tidy (`go mod tidy -diff`),
  vet, gofmt, the pinned golangci-lint (`mise install`) and the tests. Run
  it before claiming any change complete, docs included.
- `make race` runs the tests under the race detector, and `make cross`
  builds linux/amd64 and linux/arm64. `make ci` adds the web bundle's
  checks.
- Never run pnpm, node or cargo on the host. `web/` builds in the nsl
  machine `lodge` (`make web-build`), as lodge's apps do.
- Fix a lint finding rather than loosening `.golangci.yml`.
- Tests run unfiltered (`go test ./...`). No test reaches the network: it
  uses `httptest`, and never sleeps to synchronize.

## Invariants (decided; binding from the first line of code)

- **Only lodge listens on the network.** `readd` serves Unix sockets only:
  lodge's app socket, and its own private socket for `readctl`.
- **The fetcher never reaches homelab, private, loopback, link-local or
  tailnet addresses.** Every outbound request goes through
  `internal/fetch`, built on lodge's `kit/netguard`, which checks each
  resolved address after DNS on every dial, redirects included
  ([ADR-0003](docs/adr/0003-the-fetcher-reaches-only-public-addresses.md)).
- **Untrusted HTML is sanitized in the daemon** and shown only from
  lodge-content, with images inlined as `data:` URIs; the browser never
  loads a remote resource
  ([ADR-0005](docs/adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
- **Content URLs are MACs under a secret the daemon keeps,** never
  guessable ids (lodge's
  [lodge-http.md](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#content-routes),
  content routes).
- **Agents reach frostread only through `/mcp/read` with a token,** and the
  engine enforces the token's scope, not only the tool list.
- **frostread writes notes only through notes' `/mcp/notes`** on lodge's
  agent socket, with a token scoped to one folder
  ([ADR-0004](docs/adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
- **Items are kept as sent:** the original bytes are stored beside the
  extraction.
- **Other people's words are data.** Text from a feed or a page reaches an
  agent marked as data, never as instructions.
- **Never log a token, a note's text or an article's text.** Log ids, a
  URL's host, the status and the duration.

## Code conventions

- Go: pure Go (`CGO_ENABLED=0`); `ctx` first; errors are lowercase and
  wrapped with `%w`; `slog` for logs; `encoding/json/v2`; no mutable package
  state; modern idioms (`t.Context()`, `t.Chdir`, `wg.Go`, `omitzero`,
  `b.Loop`). Tests use temp dirs, table tests, and never sleep to
  synchronize.
- One package wraps each heavy dependency: `gofeed` only in
  `internal/feed`, readability only in `internal/extract`, `bluemonday`
  only in `internal/sanitize`, html-to-markdown only in
  `internal/notes`, and the MCP SDK only in `internal/mcpserver` and
  `internal/notes`.
- API changes start in `schema/rpc`, and `api/` follows it by hand until
  the app adopts a generator.

## Development-only code

Two build tags serve the live check on lodge's development build, and
neither is in a release build:

- **`devfetch`**: the fetcher admits `127.0.0.1` alone, for the host:ports
  `READ_DEV_ALLOW` names (such as `127.0.0.1:8088`). Every other address
  stays refused. A test proves with `go tool nm` that a release binary
  lacks the code.
- **`devfeeds`**: `cmd/readdev` serves a generated RSS feed, Atom feed and
  JSON Feed with six made-up articles on `127.0.0.1:8088`, and `-opml`
  prints the OPML that names them. `make dev-seed` prints it.

They are documented in [docs/specs/api.md](docs/specs/api.md#development-only),
and `make check` vets and tests both tags. `make dev-lodge` runs the whole
live check on loopback; its evidence is in the
[roadmap](docs/plans/0001-roadmap.md#phase-6--the-live-check-on-lodges-development-build-built-2026-10-21).

## Repository boundary

- Never commit `build/`, `node_modules`, tokens, provider credentials, a
  note's text, an article's text, or private host names. Tests and docs use
  made-up data: `alice@example.com`, `example.com`, `example.org`.
- No non-documentation IP address outside loopback in code or docs, except
  the development-only loopback example above.
- Use conventional commit subjects.

## Documentation rules

Docs live in `docs/` in four categories (frostyard/core ADR-0025). **Every
new doc starts from its category's `TEMPLATE.md`** and follows its
structure:

- `docs/adr/`: why we decided. Immutable once Accepted; a reversal is a new
  ADR that marks the old one Superseded. The founding ADRs are Proposed
  (2026-10-21) and may be edited in place until they are accepted.
- `docs/design/`: how it fits together. Living, and updated to match
  reality.
- `docs/specs/`: exact contracts. They change only alongside implementing
  code.
- `docs/plans/`: the order of work. Phases with "Done when" outcomes.

Cross-linking is mandatory in both directions. An ADR links the design docs
and specs it shapes. A design doc links its ADRs, its specs and the roadmap
phase that builds it. A spec links its ADRs and its design doc. A plan
links what each phase implements. A new doc gets a line in
[docs/README.md](docs/README.md). Convert relative dates to absolute dates.
