# Overview

Living document. Rationale:
[ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md)
(fetching), [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)
(notes) and [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
(articles). Contracts: [specs/api.md](../specs/api.md).

## Overview

frostread is lodge's `read` app: a tailnet-only feed reader and read-later
list. It polls sources and fetches pages from the public web; lodge owns
every listener, and the browser loads nothing remote.

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

**The daemon.** `readd`, on lodge's kit **v0.3.0**, loads its
configuration, opens the store and the
blob directory, starts the poller and the extraction workers, and serves
two Unix sockets: lodge's app socket, and its private socket for `readctl`.

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
The original body is stored as sent.

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

**Content.** `internal/content` serves
`/content/item/<id>/<mac>` (the public `CONTENT/read/item/<id>/<mac>`) as one
HTML page with inline styles and `data:` image
URIs, only from lodge-content. The MAC is `HMAC-SHA256` of `item:<id>`
under the store's secret, base64url, 22 characters, and the id travels in
the path after it.

**The web bundle.** `web/` is on `@frostyard/lodge-client`
**client/v0.3.0** and `@frostyard/lodge-ui` **ui/v0.1.0**: a source column,
an item list and a reader, with
`j`/`k`, `s` and `m`, and the narrow layout on a phone.

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
- **Disk:** fetched bodies and images are bounded per item, but the store
  grows with reading. Removing a source deletes its items by cascade, and
  their rows with it; the blobs they named stay until a later sweep.

## References

- Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md),
  [ADR-0002](../adr/0002-agent-portable-instruction-surface.md),
  [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
- Contracts: [specs/api.md](../specs/api.md)
- Built in: [roadmap — Phases 1 to 6](../plans/0001-roadmap.md)
