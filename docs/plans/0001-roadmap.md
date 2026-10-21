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
- The founding documents, written on 2026-10-21 (ADR-0001 to ADR-0005 Accepted, Brian,
  2026-10-21):
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

## Phase 5 — The web app (done 2026-10-21)

- The bundle on `@frostyard/lodge-ui`: a source column, an item list, a
  reader, unread, starred and archived views, search, save-as-note, and
  the narrow layout on a phone.
- `j`/`k`, `s` and `m`, the 30 s keepalive, and `items.changed` driving
  the refresh.
- **Done when:** the owner reads a feed on a phone through lodge, marks it
  read with `m`, and saves it to notes.
- **Deployed 2026-10-21** on lodge's host, for the owner's daily use, at
  `b01ee81`, with the steps in the README's "Install on lodge's host". The
  notes token is `vault=2;write=Reading/` on the owner's real vault. The
  live check there: the Read tab in lodge, one test feed polled and
  extracted, and Save as note wrote a note under `Reading/` that synced to
  the owner's devices; the test note was then deleted.
- **Done when is met** (OWNER-REPORTED, Brian, 2026-10-21): the owner
  checked the reader on his phone through lodge and reported it
  "perfect".

## Phase 6 — The live check on lodge's development build (built 2026-10-21)

- `cmd/readdev`, under the `devfeeds` tag: three generated feeds (RSS,
  Atom and JSON Feed) and their article pages on `127.0.0.1`, and the
  OPML that names them.
- `internal/fetch`'s `devfetch` escape, `READ_DEV_ALLOW`, documented as
  development-only in [specs/api.md](../specs/api.md#development-only).
- **Done when:** lodge's `dev/e2e.sh` root runs `readd` with the tag-built
  binaries, `readctl import` adds the three feeds, and an article's
  content page renders with its inlined image.

## Phase 7 — Save from the phone (in progress 2026-10-21)

- `later.save`'s optional `title`, cleaned and cut to 300 runes, and the
  engine test that an address inside the network is never stored
  ([specs/api.md](../specs/api.md): the methods, and the web routes).
- The save page at `/save?url=<encoded>&title=<encoded>`, which saves only
  on the owner's tap, and the `/bookmarklet` page, whose code the bundle
  builds from the frame's own origin.
- The sidebar's "Save from your phone", and the install steps for iOS
  Safari and a desktop browser
  ([ADR-0006](../adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md)).
- **Done when:** the owner installs the bookmarklet in iOS Safari and
  saves one page to read later through it.

## Phase 8 — The blob sweep (built 2026-10-21)

- `blobs.Store`'s read-write lock, `List` and `RemoveIf`, and `Put`
  touching a blob that exists
  ([ADR-0007](../adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)).
- Migration 2, `blob_orphans`, and the store's mark and record queries.
- `Engine.SweepBlobs` (mark, scan, record, remove, and a dry run) and
  `RunBlobSweep`, 10 minutes after start and then every 24 hours.
- `blobs.sweep`, for caller `local` only, and `readctl sweep [-dry-run]`;
  the configuration's `blob_sweep` and `blob_grace_days`
  ([specs/api.md](../specs/api.md)).
- **Done when:** a source's removal leaves its blobs for the grace
  period, the sweep after it removes them, a blob another item names stays,
  and a save that runs beside a sweep keeps its blob, under the race
  detector.

## Later / ideas

- Summaries through `kit/inference`, at background priority.
- Highlights and notes on an article.
- A share target: it needs a service worker, which lodge refuses
  (ADR-0008), so it stays out until Web Push is asked for. The bookmarklet
  is what saves from a phone instead
  ([ADR-0006](../adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md)).

## Open questions

- **Full-text search's rank in a view:** search covers every item today.
  Whether it should follow the current view is a UI question, decided by
  Phase 5.

## References

- Implements: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md)
- Rationale: [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md),
  [ADR-0006](../adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md),
  [ADR-0007](../adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)

### Phase 6 evidence, 2026-10-21

The whole live check ran on loopback with generated data only, through
`make dev-lodge` (`scripts/dev-lodge.sh`), on this machine. Its root is
written `<root>` here.

**What ran**

```sh
make -C ../frostnotes web-build     # nsl: notes' web build
make web-build                      # nsl: Read's web build
ROOT=<root> make dev-lodge          # lodge's dev/e2e.sh, notesd, readd, readdev
LODGE_ROOT=<root> ../lodge/build/lodgectl status
LODGE_ROOT=<root> ../lodge/build/lodgectl apps
build/readctl --socket <root>/run/frostread/readctl.sock status
build/readctl --socket <root>/run/frostread/readctl.sock sources
build/readctl --socket <root>/run/frostread/readctl.sock search tomato
build/readctl --socket <root>/run/frostread/readctl.sock note 1
curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8480/{,/read/,/app/read/,/notes/1/}
curl -s http://127.0.0.1:8481/read/item/1/<mac>   # the content page
```

The content page's path came from `items.get` over readd's private socket,
as a session would receive it.

**What was seen**

- `lodgectl status`: `app notes up`, `app read up`, the agent socket
  listening, 0 sessions.
- `lodgectl apps`: notes `up` with `content,device,mcp,remoteImages`; read
  `up` with `content,contentLinks,mcp`.
- `readctl status`: `readd 759d3b0-dirty`, `save as a note: yes, vault 1,
  folder Reading/`.
- `readctl sources`: three sources, each 2 unread: the generated RSS, Atom
  and JSON feeds. `readctl search tomato` found the extracted articles.
- `readctl note 1` printed
  `Reading/2026-10-21 Keeping a reading list that works.md` and its route
  `/notes/1/Reading/2026-10-21%20Keeping%20a%20reading%20list%20that%20works.md`.
  The file was in `<root>/vault/Reading/`, with the frontmatter (`source:
  http://127.0.0.1:8088/articles/5`, `saved`, `author`, `site`), the title,
  the line `[Open in Read](/read/item/1)`, the article's Markdown, and the
  image as `[image: A made-up tomato](http://127.0.0.1:8088/img/tomato.png)`.
- The four lodge pages answered 200, with the shell's title `Lodge` and the
  read app's `Read`. The content page answered 200 with one
  `data:image/png;base64` image and no `blob:` marker.
- No `lgm_` token appeared in the script's output, in `<root>/logs` or in
  the vault.

**What did not work at first, and was fixed before this run**

- notesd in the frostnotes checkout's `build/` was stale, from before that
  checkout's lodge support, and refused `-lodge`. `scripts/dev-lodge.sh`
  now runs `make -C ../frostnotes build` on every start.
- The script's closing text had a command in backticks inside an unquoted
  here-document, so it ran during printing. It says the same in plain words
  now.
- The development shell is plain HTTP, so the configuration has no
  `lodge_url` (it must be an https origin). The note's link is the rooted
  `/read/item/1`, which notes follows in lodge. That is by design, and it
  is what the note above holds.

### MCP over the agent socket, 2026-10-21

`make dev-lodge` ran on loopback, with the development build and the
generated vault. Tokens were issued for app `read`, each with `--agent`
naming the current user, and were passed to the check through the
environment; none was printed or committed.

```sh
LODGE_ROOT=<root> ../lodge/build/lodgectl token issue frostread-live-read  --app read --scope read       --agent <user>
LODGE_ROOT=<root> ../lodge/build/lodgectl token issue frostread-live-write --app read --scope read\;write --agent <user>
LODGE_ROOT=<root> ../lodge/build/lodgectl token issue frostread-live-bad   --app read --scope write      --agent <user>
LODGE_ROOT=<root> READ_LIVE_TOKEN_READ=… READ_LIVE_TOKEN_WRITE=… READ_LIVE_TOKEN_BAD=… \
  READ_LIVE_SOCKET=<root>/run/frostread/readctl.sock \
  go test -tags live ./internal/live -run 'TestAgentSocket|TestBadScopeIs403' -v
```

Seen:

- The read token's tools were `list_sources`, `list_unread`, `read_item`
  and `search`. `save_url` was not listed, and calling it was refused.
- `list_sources` answered 3 sources, and `list_unread` 6 items. The search
  for "heron" found "An autumn walk along the river", and `read_item` on it
  returned 991 characters.
- The token whose scope was `write` was refused at `initialize`: 403, "the
  token's scope does not parse: scope \"write\" is not read or read;write".
- The write token saved the development article at
  `http://127.0.0.1:8088/articles/4` as item 7, marked one item read and
  starred another. The counts a web session reads (`counts`) went from
  `all 6, later 0, starred 0, unread 6` to `all 7, later 1, starred 1,
  unread 6`: the saved link is a new unread item, and one was marked read.
- `lodgectl token revoke frostread-live-write` answered "revoked
  frostread-live-write". The next `initialize` with that token failed with
  "Unauthorized".

The content page's theme, by `curl` on its content URL:

| Query | The page's `<html>` |
| --- | --- |
| none | `<html lang="en">` |
| `?scheme=dark` | `<html lang="en" data-scheme="dark">` |
| `?scheme=light` | `<html lang="en" data-scheme="light">` |
