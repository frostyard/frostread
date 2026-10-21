# Plan: the roadmap

What frostread builds, in the order it builds it, from lodge's scaffold to
a live check on lodge's development build. It delivers
[specs/api.md](../specs/api.md) as
[design/overview.md](../design/overview.md) describes.

Each phase ends with a demonstrable outcome. A phase's checkboxes are
ticked as its work lands.

## Phase 1 — Scaffold and documents (2026-10-21)

- Lodge's `new-lodge-app` scaffold: `readd`, its API, `web/`, the units
  and `stage.sh`, on lodge's kit.
- The repository surface: `AGENTS.md` with its symlinks, `README.md`,
  `LICENSE`, `mise.toml` and `mise.lock`, `.golangci.yml`, and the CI
  workflow.
- The founding documents, all Proposed on 2026-10-21:
  [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md),
  [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md), and this plan.
- **Done when:** `make check` passes on the scaffold alone.

## Phase 2 — The daemon (built 2026-10-21)

- `internal/store`: SQLite with WAL and foreign keys, migrations by
  `user_version`, the tables of
  [specs/api.md, State](../specs/api.md#state), FTS5 with triggers, keyset
  paging, counts, and the content secret.
- `internal/blobs`: content-addressed files, written by temp file and
  rename, fsync'd.
- `internal/fetch`: the only HTTP client, on `kit/netguard`
  ([ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md)).
- `internal/feed`: RSS, Atom and JSON Feed on `gofeed`, and feed
  discovery.
- `internal/opml`: import and export.
- `internal/sanitize`: the article policy on `bluemonday`, with a hostile
  corpus.
- `internal/extract`: readability, sanitizing, and the images
  ([ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
- `internal/poller`: due sources every minute, four at a time, conditional
  GET, backoff with jitter and `Retry-After`, and the extraction queue.
- `internal/engine`: the logic over them, with a change notification.
- **Done when:** two test feeds on an `httptest` server poll, extract,
  index and search in a test, and a `127.0.0.1` fetch is refused.

## Phase 3 — API, readctl, MCP (built 2026-10-21)

- `schema/rpc` and `api/`: every method of
  [specs/api.md](../specs/api.md#json-rpc-api).
- `internal/rpcserver`: the `items.changed` notification to sessions.
- `internal/content`: the MAC'd content page.
- `internal/mcpserver`: the seven tools, and the scope
  `read` or `read;write`, enforced in the engine.
- `cmd/readctl`: every command of
  [specs/api.md](../specs/api.md#readctl).
- **Done when:** `readctl add`, `save`, `poll`, `search` and `export`
  work against a running `readd`, and an MCP client with `read` cannot
  save a URL.

## Phase 4 — Save as a note (built 2026-10-21)

- `internal/notes`: HTML to Markdown, the path rules, the frontmatter, the
  rooted link, and `create_note` over lodge's agent socket
  ([ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
- `readctl notes-token` and `notes.setToken`.
- The item's `note_path`, and `items.saveAsNote`'s `route`.
- The packaging: group `lodge-agents` for `frostread`, and the comment that
  says why.
- **Done when:** a test's fake notes server receives one `create_note` with
  `Reading/<date> <title>.md`, and a second save of the same title gets
  ` 2`.

## Phase 5 — The web app

- The bundle on `@frostyard/lodge-ui`: a source column, an item list, a
  reader, unread, starred and archived views, search, save-as-note, and
  the narrow layout on a phone.
- `j`/`k`, `s` and `m`, the 30 s keepalive, and `items.changed` driving
  the refresh.
- **Done when:** the owner reads a feed on a phone through lodge, marks it
  read with `m`, and saves it to notes.

## Phase 6 — The live check on lodge's development build

- `cmd/readdev`, under the `devfeeds` tag: three generated feeds (RSS,
  Atom and JSON Feed) and their article pages on `127.0.0.1`, and the
  OPML that names them.
- `internal/fetch`'s `devfetch` escape, `READ_DEV_ALLOW`, documented as
  development-only in [specs/api.md](../specs/api.md#development-only).
- **Done when:** lodge's `dev/e2e.sh` root runs `readd` with the tag-built
  binaries, `readctl import` adds the three feeds, and an article's
  content page renders with its inlined image.

## Later / ideas

- Summaries through `kit/inference`, at background priority.
- Highlights and notes on an article.
- A share target: it needs a service worker, which lodge refuses
  (ADR-0008), so it stays out until Web Push is asked for.

## Open questions

- **A blob sweep:** items' blobs are never removed when an item or a
  source goes. It is harmless for now; a sweep needs a reference count or
  a mark-and-scan, decided by Phase 6 or later.
- **Full-text search's rank in a view:** search covers every item today.
  Whether it should follow the current view is a UI question, decided by
  Phase 5.

## References

- Implements: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md)
- Rationale: [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
