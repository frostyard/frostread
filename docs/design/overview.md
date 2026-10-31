# Overview

Living document. Rationale:
[ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md)
(fetching), [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)
(notes), [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
(articles) and [ADR-0007](../adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)
(the blob sweep). Contracts: [specs/api.md](../specs/api.md).

## Overview

frostread is lodge's `read` app: a tailnet-only feed reader and read-later
list. It polls sources and fetches pages from the public web; lodge owns
every listener, and the browser loads nothing remote.

Planned, not built:
[ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
(Accepted) adds a desktop Flatpak, `org.frostyard.Frostread`: a Tauri
shell over this web bundle, with its own readd in a desktop mode, local by
default, and lodge as a connection the user chooses.
[Plan 0002](../plans/0002-the-standalone-desktop-flatpak.md) builds it;
this document changes with each of its phases.

```
 feeds, pages, images ──▶ internal/fetch (kit/netguard) ──▶ poller, extract
                                                             │
                                                   store (SQLite) + blobs
                                                             │
   browser ──https──▶ lodge ──/app/read/api──▶ readd engine ──┤
                        │  ──/content/item/…──▶ content page ◀┘
                        └──/mcp/read──▶ mcpserver ──▶ engine
   readctl ──unix──▶ readd private socket ──▶ engine
   notes ◀──agent socket◀── internal/notes ◀── engine (save as note)
```

## Design

**The daemon.** `readd`, on lodge's kit **v0.3.1**, loads its
configuration, opens the store and the
blob directory, starts the poller and the extraction workers, and serves
two Unix sockets: lodge's app socket, and its private socket for `readctl`.
It holds a `flock` on its state directory, so one readd owns one directory.

**The desktop's readd.** `readd -desktop` is the Flatpak's own
([ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md),
[specs/api.md](../specs/api.md#desktop-mode)). It takes the same lock,
then reads `app.json` before it opens the database: named lodge, it
releases the lock and exits 0, because the window then uses lodge's readd
and this computer keeps nothing. In local mode it serves only its private
socket: no lodge socket, no content route, no MCP and no notes client, and
no TCP port. `daemon.shutdown` lets the window stop it in order, and
`items.exportMarkdown` gives the reader a Markdown file instead of a
note.

**The store.** SQLite on local disk, WAL, foreign keys on, migrations by
`user_version`:

- `sources`: one subscription, its conditional-GET validators, its next
  poll time and its last error;
- `items`: one article or saved link. `source_id` is NULL for a saved link,
  which has `saved = 1`. `read_at`, `starred`, `archived` and `note_path`
  are the reader's own state;
- `blobs` and `item_blobs`: what an item's extraction stored;
- `items_fts`: FTS5 over title, author and text, an external-content table
  with triggers, as frostnotes' `notes_fts` is;
- `settings`: the secret that the content MACs use, made on first start.

**The fetcher.** `internal/fetch` is the only HTTP client. It checks the
name, then every dialed address after DNS, follows at most 5 redirects
with both checks repeated, ignores environment proxies, caps sizes and
time, and sends a fixed `User-Agent`
([ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md)).

**Sources and polling.** `internal/poller` runs one goroutine. Every minute
it takes the sources whose `next_poll_at` has passed, at most 4 at a time,
fetches each with its `ETag` and `Last-Modified`, parses it with
`internal/feed`, and inserts the items it does not have, by GUID. It
schedules the next poll: the configured interval with ±10% jitter after a
success, and a doubling backoff after an error, up to 24 hours, with a
`Retry-After` honoured. The error is stored on the source and shown on it.

**Extraction.** Every new item is queued for extraction. An item whose feed
text is under 600 characters is fetched from its page; one with more is
already a full article, so its feed content is used and its page is never
fetched. Two workers take the queue, and each also sweeps the store every
30 seconds, so an item whose enqueue was dropped is not lost. Extraction
runs readability, sanitizes, fetches the images into the blob store and
rewrites their sources to `blob:<hash>`
([ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
The original body is stored as sent. A feed item's original is its HTML as
the feed gave it, the summary and then the content, before it is
absolutized or sanitized; the poll writes it to the blob store before the
item's row names its hash. A fetched page's original is the page's body,
written before `SetOriginal` names it, and it takes the place of the feed
item's original on the row; the sweep removes the feed item's blob after
the grace period, when no other row names the same bytes.

**The engine.** `internal/engine` is the app's logic over the store,
fetcher, feed parser, extractor and notes client. Every change sends a
notification that the RPC server forwards to the connections that called
`events.subscribe`, as the kit's event envelope whose event is
`items.changed`. `internal/readapi` turns the engine's errors into the
contract's codes.

**The API.** `api/` and `internal/rpcserver` serve JSON-RPC 2.0 on
`kit/jsonrpc`: `/api` for a browser session, the private socket for
`readctl`, with the caller each admits.

**MCP.** `internal/mcpserver` serves the MCP SDK over `/mcp`, wrapped in
`kit/mcpsession`, whose `CheckScope` admits exactly `read` or
`read;write`. Each session gets its own MCP server built from its scope,
and the engine checks the scope again on every write.

**Save as a note.** `internal/notes` calls notes' `create_note` through
lodge's agent socket with frostread's own scoped token
([ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
The note is Markdown, with frontmatter (including the `read_item` marker),
and a rooted link back to the article. When the reader opens an item with a
note, and again when the owner clicks "Open in Notes", the app asks
`items.noteStatus`: a moved note is found by its marker and opened at its
new path, a missing one turns the button back into "Save as note", and an
unreachable notes keeps the link with the error shown
([specs/api.md](../specs/api.md#note-status)).

**Content.** `internal/content` renders one article's page: inline
styles, `data:` image URIs, and no remote resource. Lodge's route serves it
at `/content/item/<id>/<mac>` (the public `CONTENT/read/item/<id>/<mac>`),
only from lodge-content; the MAC is `HMAC-SHA256` of `item:<id>` under the
store's secret, base64url, 22 characters, and the id travels in the path
after it. The desktop's window asks `items.render` for the same page,
whose only difference is a CSP meta first in its head, and which keeps its
encoded answer under 12 MiB by leaving images out
([ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md),
[specs/api.md](../specs/api.md#rendering-for-the-desktop)). The desktop's
reader shows it in `<iframe srcdoc sandbox="allow-popups">`, with no `src`,
no `allow-scripts` and no `allow-same-origin`, and renders it again when
the scheme changes. It says how many images were left out, and on
`tooLarge` it shows the excerpt, "This article is too large to show in the
app" and a link to the original
([plan 0002, step 3.3](../plans/0002-the-standalone-desktop-flatpak.md#phase-3s-steps)).

**The web bundle.** `web/` is on `@frostyard/lodge-client`
**client/v0.3.2** and `@frostyard/lodge-ui` **ui/v0.6.0**: a source column,
an item list and a reader, with
`j`/`k`, `s` and `m`, and the narrow layout on a phone.

**Saving from another page.** `/save?url=<encoded>&title=<encoded>` is the
page a bookmarklet opens: it shows the page's title and address as text,
with the address's host, and saves only when the owner taps Save, through
`later.save` on the app's own lodge session. The bookmarklet only
navigates this tab; it holds no token. `/bookmarklet`, which the sidebar
opens, hands the bookmarklet out with its code and the install steps for
iOS Safari and a desktop browser. A saved title is cleaned and cut to 300
runes, and the address is checked with `fetch.Check` before it is stored
([ADR-0006](../adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md),
[specs/api.md](../specs/api.md#web-routes)).

## Operational notes

- **State:** `/var/lib/frostread` (`STATE_DIRECTORY`), with `frostread.db`,
  `blobs/`, and `tokens/notes` (0600).
- **Configuration:** `/etc/frostread/frostread.json` (`READ_CONFIG`).
  Absent is valid: defaults only.
- **Backup:** stop the daemon, then snapshot the state directory, or
  snapshot it live; SQLite's WAL is consistent with its own snapshots. The
  database must not live on a network share.
- **Failure modes.** A feed that fails stores its error and backs off; the
  app is up. A missing notes token disables save-as-note alone. A content
  secret that is missing is made again, which invalidates old content
  URLs only (the app rebuilds them), and does not touch the items.
- **Logs:** ids, a URL's host, status and duration. Never a token, a note's
  text or an article's text.
- **Disk:** a daily sweep removes the blobs no row names
  ([ADR-0007](../adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)).
  It marks the hashes the database names, scans `blobs/`, and records in
  `blob_orphans` when it first found each unnamed file. A file goes when
  that record and its modification time are both past the grace period
  (`blob_grace_days`, 7), after a second check under the store's lock; `Put`
  touches a blob that exists. It runs 10 minutes after start, then every
  24 hours (`blob_sweep`), and `readctl sweep [-dry-run]` runs it now.
- **Feed items polled before 2026-10-30** whose row still names their
  feed original (not a fetched page's) have an `original_hash` with no
  blob: the poll recorded the hash of the feed item's HTML and did not
  store the bytes. An item whose page was fetched names the page's body,
  which was stored, and has no gap. A later poll stores them when the feed still sends that
  item with the same bytes, because their SHA-256 then proves they are the
  bytes first sent. An item that has left its feed, or whose feed text has
  changed, keeps a hash with no blob: no re-fetch can give the bytes as
  they were sent, so none is stored under that hash. Such an item still
  shows its stored, sanitized text and its extraction.
- **Restoring an old database:** it may name blobs the sweep removed since
  the snapshot. A restore inside the grace period is safe; an older one
  needs the `blobs/` directory from the same snapshot, or its items show
  a missing image or an original that is gone.

## References

- Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md),
  [ADR-0002](../adr/0002-agent-portable-instruction-surface.md),
  [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md),
  [ADR-0006](../adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md),
  [ADR-0007](../adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md);
  planned: [ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
  (the desktop Flatpak)
- Contracts: [specs/api.md](../specs/api.md)
- Built in: [roadmap — Phases 1 to 8](../plans/0001-roadmap.md); the
  desktop in [plan 0002](../plans/0002-the-standalone-desktop-flatpak.md)
