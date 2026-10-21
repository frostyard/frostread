# Spec: the Read app's interface

What frostread offers: its JSON-RPC API, its MCP tools and scope, its
content routes, `readctl`, the configuration file, the state layout, and
what the fetcher may do. Its consumers are the web bundle, `readctl`, lodge
(which proxies the sockets), notes (which receives a saved article through
its own MCP), and agents through `/mcp/read`. The key words MUST, MUST NOT,
SHOULD and MAY are used as in RFC 2119.

This spec binds from the first line of code: frostread is pre-release, so
it changes only alongside the code that implements it.

## Interface

### JSON-RPC API

JSON-RPC 2.0, one message per line, on lodge's `kit/jsonrpc`. The
connection starts with `rpc.hello` (protocol `1`), as the scaffold's
contract says. Two callers exist:

| Caller | Connection | Methods |
| --- | --- | --- |
| `session` | lodge's `/app/read/api`, one WebSocket per browser | every method but `notes.setToken` |
| `local` | `/run/frostread/readctl.sock` | every method, `notes.setToken` included |

A method a connection may not call answers `methodNotFound`, as does one
that does not exist.

#### Views

`view` is one of `unread`, `starred`, `later`, `archive` or `all`:

| View | Items |
| --- | --- |
| `unread` | `read_at` is NULL and not archived |
| `starred` | starred and not archived |
| `later` | saved and not archived |
| `archive` | archived, whatever else |
| `all` | every item |

An item is in a source's list and in the views by these rules. `all` is
what search returns too, unless a query says otherwise.

#### Types

`Source`:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | the source's id |
| `url` | string | the feed's URL |
| `siteUrl` | string | the site's home, when the feed names it |
| `title` | string | the title the owner or the feed gave it |
| `unread` | integer | its unread items |
| `errorCount` | integer | consecutive failed polls |
| `lastError` | string | the last poll's error, empty when none |
| `polledAt` | string | RFC 3339 UTC, the last successful poll |
| `nextPollAt` | string | RFC 3339 UTC, when it polls next |

`ItemSummary`:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | the item's id |
| `title` | string | its title |
| `sourceTitle` | string | its source's title; empty for a saved link |
| `author` | string | its author, when the feed or page names one |
| `published` | string | RFC 3339 UTC; the fetched time when the feed has none |
| `excerpt` | string | its summary as text, at most 300 characters |
| `read` | bool | `read_at` is set |
| `starred` | bool | |
| `archived` | bool | |
| `saved` | bool | it is a saved link |
| `notePath` | string | the note save-as-note wrote, empty when none |
| `snippet` | string | search results only: the match, its words between `**` |

`ItemDetail` is `ItemSummary` plus:

| Field | Type | Meaning |
| --- | --- | --- |
| `url` | string | the article's URL |
| `contentPath` | string | the article's path relative to the content root, `item/<id>/<mac>`, for lodge-client's `contentURL`, which builds `CONTENT/read/<contentPath>` |
| `extractState` | string | `pending`, `done` or `failed` |
| `extractError` | string | why extraction failed, empty when it did not |
| `originalHash` | string | the SHA-256 of the body as sent |

#### Methods

| Method | Params | Result | What it does |
| --- | --- | --- | --- |
| `status` | none | `{version, notes: {configured, vaultId, folder}}` | the daemon's version and whether save-as-note is ready |
| `sources.list` | none | `{sources: [Source]}` | every source, by title |
| `sources.add` | `{url}` | `{source: Source}` | subscribes: when `url` is a page, discovers its feed; polls it at once |
| `sources.remove` | `{id}` | `{}` | unsubscribes; its items go with it |
| `sources.rename` | `{id, title}` | `{}` | sets the owner's title |
| `sources.refresh` | `{id?}` | `{}` | polls now: one source, or all of them |
| `opml.import` | `{xml}` | `{added, skipped}` | adds every outline in an OPML document |
| `opml.export` | none | `{xml}` | every source as OPML 2.0 |
| `items.list` | `{view, sourceId?, cursor?, limit?}` | `{items: [ItemSummary], next?}` | newest first; `limit` 1 to 200, 50 when absent; `next` is the next page's cursor |
| `items.get` | `{id}` | `ItemDetail` | one item |
| `items.search` | `{query, limit?}` | `{items: [ItemSummary]}` | best match first, with `snippet` |
| `items.setRead` | `{ids, read}` | `{}` | marks items read or unread |
| `items.markAllRead` | `{view?, sourceId?, before?}` | `{}` | marks read every unread item in the view or the source, published before `before` |
| `items.setStarred` | `{id, starred}` | `{}` | |
| `items.setArchived` | `{id, archived}` | `{}` | |
| `items.saveAsNote` | `{id}` | `{path, route}` | writes the article into notes; `route` is `/notes/<vaultId>/<path segments, encoded>`, for lodge-client's `open` |
| `items.noteStatus` | `{id}` | `NoteStatus` | asks notes where the item's note is; `present`, `moved` (and records the new path), `missing` (and clears `note_path`) or `unreachable` (keeping it), within 5 seconds |
| `later.save` | `{url}` | `{item: ItemSummary}` | saves a link to read later; saving it twice returns the same item |
| `counts` | none | `{views: {unread, starred, later, archive, all}, sources: [{sourceId, unread}]}` | the sidebar's counts |
| `notes.setToken` | `{token}` | `{}` | caller `local` only: writes the notes token, 0600, and uses it at once |
| `events.subscribe` | none | `{subscribed}` | makes this connection receive `items.changed`, from the answer on |

Times are RFC 3339 in UTC, with `omitzero`. A cursor is opaque to the
client: it names the last item of the page it came with, and `items.list`
answers a cursor it did not issue with `invalidParams`.

Errors are the scaffold's codes: `invalidParams` (a bad view, limit, cursor
or URL), `notFound` (an unknown item or source), `conflict` (a URL that is
already a source), `unavailable` (save-as-note with no token or no vault,
or notes is not reachable) and `internal`.

`NoteStatus`:

| Field | Type | Meaning |
| --- | --- | --- |
| `state` | string | `present`, `moved`, `missing` or `unreachable` |
| `path` | string | the note's path, present or moved, or the old one when notes could not be asked |
| `route` | string | its route in lodge, present or moved |
| `error` | string | why notes could not answer, when the state is `unreachable` |

#### Notifications

After any change (an item arrives, is marked read, starred, archived or
saved, a source is added or removed, a note is written), every connection
that called `events.subscribe` receives the kit's event envelope, its
event named `items.changed`:

```json
{"jsonrpc":"2.0","method":"event","params":{"event":"items.changed","data":{"views":["unread","later"]}}}
```

`data.views` names the views the change affects. A client refetches the
counts and the list it shows. `readctl` does not subscribe, so it receives
no events. A notification may reach a client before the answer to its own
`events.subscribe`; a client that ignores one before it has subscribed
anyway loses nothing, because it refetches on connecting.

#### Note status

An item that saved a note keeps its path in `note_path`, and the reader
offers "Open in Notes". The note can move, or go, so `items.noteStatus`
asks notes, with the token's own scope, which reads the whole vault
([frostnotes ADR-0028](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0028-an-agents-scope-can-limit-writes-to-folders.md)):

- **The marker.** Every new note's frontmatter holds `read_item:
  /read/item/<id>`, the article's rooted path in the reader, beside
  `source`, `saved`, `author` and `site`.
- **Present:** `read_note` at `note_path` answers.
- **Moved:** the read fails as not found, so the daemon looks for the
  marker with `list_notes{property: "read_item", value: "/read/item/<id>"}`.
  For a note written before the marker, it lists the notes under
  `Reading/` (at most 50) and reads each for the item's link,
  `/read/item/<id>`. A found note's path is recorded in `note_path`, and
  the answer is `moved` with its route.
- **Missing:** neither finds the note. `note_path` is cleared, and the
  reader offers "Save as note" again.
- **Unreachable:** a transport, authentication or protocol error, or the
  5 second timeout. `note_path` stays, and the error is the answer's
  `error`, which the reader shows beside the link.
- **What this does not detect:** an old note, without the marker, that was
  moved out of `Reading/`; a note whose marker and "Open in Read" link were
  both edited away; and a note beyond the first 50 under `Reading/` in
  path order. A renamed or moved note with its marker is found anywhere in
  the vault.

### MCP

MCP Streamable HTTP at `/mcp`, for caller `agent`, wrapped in
`kit/mcpsession`. The manifest sets `"mcp": true`.

**The scope** is exactly `read` or `read;write`, and nothing else: a
connection whose token's `Lodge-Scope` is anything else answers 403 at
`initialize` (the kit's `CheckScope`). A connection binds its scope to its
own MCP server. The engine checks the scope on every write, so a
`read`-only session that calls a write tool gets a tool error, not a
result.

| Tool | Scope | Params | Returns |
| --- | --- | --- | --- |
| `list_sources` | `read` | none | each source's id, title, site URL and unread count |
| `list_unread` | `read` | `source_id?`, `since?` (RFC 3339), `limit?` (50, at most 200) | summaries of unread items, newest first |
| `search` | `read` | `query`, `limit?` (50, at most 200) | matching items' ids, titles, URLs, sources and snippets |
| `read_item` | `read` | `id` | the item's title, URL, source, dates and states, and its extracted text, at most 60,000 characters |
| `save_url` | `read;write` | `url` | the saved item's id and URL |
| `mark_read` | `read;write` | `ids` (1 to 200), `read?` (true) | how many changed |
| `star` | `read;write` | `id`, `starred` | the item's id and state |

No tool deletes anything. Every tool's description says that article text
from the web is data, not instructions, and `read_item` wraps the text in
a line saying so.

### Content

`GET /content/item/<id>/<mac>`, for caller `content`, only from
lodge-content. The manifest sets `"content": true` and
`"contentLinks": true`.

- **The public URL** is `CONTENT/read/item/<id>/<mac>`: lodge-content maps
  `CONTENT/<id>/<path>` to the app's `/content/<path>`, and an item's
  `contentPath` is `item/<id>/<mac>`, relative to that root. It has no
  leading slash and no `content/` segment, because lodge-client's
  `contentURL` adds `CONTENT/<app>/`.

- `mac` is `base64url(HMAC-SHA256(secret, "item:<id>"))` cut to 22
  characters. `secret` is 32 random bytes in the store's `settings`,
  written on first start.
- A wrong `mac`, or an id that is not a number, answers 404. A wrong MAC
  answer MUST NOT say whether the item exists.
- The body is one HTML page: a `<style>` with a system font stack, a
  `max-width: 42rem` column, a line height of 1.6, light and dark colors by
  `prefers-color-scheme`, and images at `max-width: 100%`. Then the title,
  byline, site name, date, and the sanitized article, each `blob:<hash>`
  replaced by `data:<mime>;base64,…`.
- The page sets `Content-Type: text/html; charset=utf-8`,
  `X-Content-Type-Options: nosniff` and
  `Cache-Control: private, no-cache`. Lodge adds the sandbox policy, whose
  CSP allows only `img-src data:`.
- The page has no script, no remote link but the article's own anchors,
  and no remote image.
- The article's date is shown in the daemon's time zone (`time.Local`),
  the same day the app's reader shows in the reader's zone.
- **The theme.** `?scheme=light` or `?scheme=dark` makes the page follow
  the shell's theme, which the web app passes whenever the theme changes
  (and a changed theme reloads the frame). Without the query, the page
  follows the reader's own `prefers-color-scheme`. The MAC covers the path
  alone, so the query changes nothing about what may be read.

### Fetching

`internal/fetch` is the only outbound HTTP client. It MUST:

- accept only `http` and `https` URLs, with no userinfo;
- refuse a name `netguard.Name` refuses, before DNS;
- refuse every dialed address `netguard` refuses, after DNS, with
  `netguard.ErrInside`, so a redirect or DNS rebinding inside is refused
  too;
- follow at most 5 redirects, checking each target's scheme and name;
- use no environment proxy;
- cap a body at its caller's `MaxBytes` (feed 10 MiB, page 10 MiB, image
  10 MiB, 40 MiB of images per item), and answer a larger body as an
  error;
- send a fixed `User-Agent`, `frostread/<version>`, no cookies and no
  credentials;
- send `If-None-Match` and `If-Modified-Since` when it has them, and
  answer a 304 as `NotModified`;
- give one request at most 30 seconds, redirects included.

### `readctl`

```
readctl [--socket PATH] COMMAND [ARGS]
```

`--socket`, or `READ_SOCKET`, or `/run/frostread/readctl.sock`. It speaks
the private socket's JSON-RPC as caller `local`.

| Command | Does |
| --- | --- |
| `save <url>` | saves a link to read later |
| `add <feed-url>` | subscribes, discovering the feed from a page |
| `sources` | lists sources with their unread counts |
| `note <item-id>` | saves the article as a note, and prints its path and its route in notes |
| `note-status <item-id>` | prints where the item's note is: `present`, `moved`, `missing` or `unreachable`, with its path or the error |
| `import <file.opml>` | imports the file's outlines; `-` reads stdin |
| `export` | writes the OPML to stdout |
| `poll [source-id]` | polls every source now, or one source |
| `search <query>` | searches titles, authors and text |
| `notes-token` | reads the notes token from stdin and sets it |
| `status` | prints the version and whether save-as-note is ready |

Exit status is 0 on success and 1 on an error, which it prints to stderr.

### Configuration

`/etc/frostread/frostread.json`, or `READ_CONFIG`. It is optional, and an
unknown member is an error.

```json
{
  "poll_interval": "20m",
  "lodge_url": "https://lodge.example.ts.net",
  "notes_folder": "Reading/",
  "notes_vault": 1
}
```

| Field | Type | Default | Constraints |
| --- | --- | --- | --- |
| `poll_interval` | string | `20m` | a Go duration from `15m` to `24h` |
| `lodge_url` | string | empty | an `https` origin, no path, for links in notes |
| `notes_folder` | string | `Reading/` | a vault path ending in `/`, relative, no `.` or `..` segment |
| `notes_vault` | integer | 0 | the notes vault id; save-as-note and "Open in Notes" need it |

### State

`/var/lib/frostread`, or `STATE_DIRECTORY` (systemd, the first entry when
it names several), mode 0700:

| Path | What |
| --- | --- |
| `frostread.db` | SQLite: sources, items, blobs, the search index, settings |
| `blobs/sha256/<first two hex digits>/<rest>` | content-addressed bodies and images, 0600 |
| `tokens/notes` | the notes MCP token, 0600 |

### Installation on lodge's host

The daemon installs as lodge's `read` app, beside lodge and notes, with
the steps [README.md, Install on lodge's host](../../README.md#install-on-lodges-host)
gives. What it needs:

| What | Where | Made by |
| --- | --- | --- |
| `readd`, `readctl` | `/usr/local/bin` | `make lodge-package`, then the staged `usr/` copied over the host's |
| manifest and web bundle | `/usr/share/lodge/apps/read/` | the same |
| `frostread.socket`, `frostread.service` | `/usr/lib/systemd/system/` | the same |
| the user `frostread`, in `lodge-apps` and `lodge-agents` | `sysusers.d` | `sudo systemd-sysusers /usr/lib/sysusers.d/frostread.conf` |
| the configuration | `/etc/frostread/frostread.json` | the owner, by hand |
| the notes token | `STATE/tokens/notes`, 0600 | `readctl notes-token`, from stdin |

- **Group `lodge-agents` and the `agents` list.** Save-as-note calls notes'
  MCP on lodge's agent socket ([ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md));
  the sysusers entry puts `frostread` in `lodge-agents`, and `frostread`
  must also be named in `agents` in `/etc/lodge/config.json`, followed by
  `sudo lodgectl reload`.
- **The token is issued for the agent socket:**
  `sudo lodgectl token issue frostread-notes --app notes --scope
  'vault=<id>;write=Reading/' --agent frostread`, piped to
  `sudo -u frostread readctl notes-token`. It is shown once and is never
  printed again.
- **`readctl` is same-user only:** it speaks readd's private socket, which
  admits only frostread's own processes, so every readctl step runs as
  `sudo -u frostread`.

## Rules

- `readd` MUST listen only on Unix sockets. It MUST NOT open a TCP
  listener, for any reason.
- Every outbound request MUST go through `internal/fetch`. No other package
  may import `net/http`'s client for a fetch of its own.
- A content URL MUST be unguessable without the store's secret, and a wrong
  MAC MUST answer 404 without disclosing whether the item exists.
- An unread item MUST keep its unread state through a re-poll: a poll
  updates a source's items only by adding new ones.
- A saved link MUST be idempotent by URL: saving it twice returns the same
  item, and a source's item and a saved link of the same URL stay two items,
  because one is a feed's and the other is the owner's.
- `sources.remove` MUST delete the source's items and their search rows in
  one transaction. It MUST NOT delete a saved link.
- A poll error MUST be stored on the source and shown in `Source.lastError`,
  and it MUST NOT stop the poller.
- A URL the fetcher refuses MUST answer `invalidParams` with one sentence
  naming the address, such as "Read does not fetch addresses inside your
  network (192.0.2.1)." The guard's detail, and the address that was
  refused, go to the log; the MCP `save_url` tool answers the same
  sentence.
- Extraction MUST NOT delay the item's arrival: an item is listed with
  `extractState: "pending"` before it is done, and every new item is
  queued for it. A feed item whose feed text is 600 characters or more is
  its own article: its images are fetched and inlined, and its page is
  never fetched. Any other item's page is fetched once, through the
  guarded client.
- `saveAsNote` MUST NOT write the vault's files, and MUST call only notes'
  MCP with frostread's token.
- No log line, and no error message, may contain a token, a note's text or
  an article's text.
- A release build MUST NOT admit a loopback or inside address.

### Development-only

For the live check on lodge's development build, a binary built with the
`devfetch` tag admits `127.0.0.1` for exactly the host:ports named in
`READ_DEV_ALLOW`, a comma-separated list such as `127.0.0.1:8088`. The
scheme and name checks, and every other dial, still run the guard. Every
other address stays refused, and every other name too. A release build has
no such code: the tag guards a file whose symbol a test proves absent from
a release build with `go tool nm`.

`cmd/readdev`, built with the `devfeeds` tag, serves on `127.0.0.1:8088` a
generated RSS feed, Atom feed and JSON Feed, two more that answer their
feed once and then fail their polls (500, and 429 with `Retry-After: 120`),
six made-up article pages with one image, and `/subscriptions.opml`;
`-opml` prints the OPML and stops. The OPML names all five feeds, so the
owner sees sources with an error marker and the message in a source's
sheet.
`testdata/dev/subscriptions.opml` is the same document, for a check that
does not want to run readdev first. `make dev-seed` prints it. The live check is:

```sh
go run -tags devfeeds ./cmd/readdev &
READ_DEV_ALLOW=127.0.0.1:8088 readd --root "$ROOT" --socket "$ROOT/run/frostread/readctl.sock" &
go run -tags devfeeds ./cmd/readdev -opml | readctl --socket … import -
```

Neither tag is in any release build or in `make build`, and `make check`
vets and tests both.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| `api/*.go` | follows this spec by hand until the app adopts a generator |
| `schema/rpc/*.yaml` | the JSON-RPC contract; `api/` follows it by hand |
| `readctl` | the private socket's methods |
| `web/src/rpc/testdata/*.json` | the daemon's answers: `internal/readapi`'s contract test writes them for a seeded store, and `make check` fails when one is stale. `make contract-update` rewrites them |
| `web/src/rpc/api.ts` | follows the Go types, `omitzero` included; the goldens' Vitest check fails when it does not |

## References

- Rationale: [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md),
  [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
- Context: [design/overview.md](../design/overview.md)
- Built in: [roadmap](../plans/0001-roadmap.md)
