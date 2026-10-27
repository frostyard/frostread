# 0008 — The desktop Flatpak carries its own readd; lodge is a connection the user chooses

- **Status:** Proposed
- **Date:** 2026-10-27

## Context

On 2026-10-27 the owner settled a product rule for the suite, and core's
[ADR-0062](https://github.com/frostyard/core/blob/main/docs/adr/0062-make-desktop-suite-apps-standalone-flatpaks-with-opt-in-lodge.md)
(Accepted, 2026-10-27) records it: each desktop suite app works on its own
as a Flatpak. The Flatpak carries the app's UI, its daemon and its
helpers. A fresh install needs no host daemon, no lodge, no Tailscale, no
sysext and no other suite app. lodge is a mode the user opts into, per
app: **sync** (the bundled daemon keeps its data and reconciles it with
the daemon behind lodge) or **connection** (the app uses the daemon behind
lodge as its backend). Data never moves silently, and a desktop runs one
engine per data set. ADR-0062's table leaves 4 things open for frostread:
the desktop shell, the isolation of untrusted article content without
lodge-content, the lodge mode, and save as note as an optional link to
Notes. It also asks frostread to set its application ID. This ADR settles
them. It does not reopen the rule.

frostread today is lodge's `read` app and nothing else:

- `readd` refuses to start without lodge's app socket (`env.Listen` in
  `cmd/readd`). It serves lodge on that socket (`/api`, `/content/`,
  `/mcp`) and `readctl` on a private socket, which the kit's
  `jsonrpc.ListenWith` admits only for the same user (`SO_PEERCRED`).
- The web bundle in `web/` is a lodge-client bundle on `@frostyard/lodge-ui`.
  It reaches readd only through lodge's `/app/read/api`.
- An article is shown in an `<iframe>` whose `src` is its content page on
  lodge-content, a second origin, under lodge's sandbox policy, whose CSP
  allows images from `data:` alone
  ([ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
  The frame's `sandbox` is `allow-popups allow-popups-to-escape-sandbox`.
- Save as note calls notes' MCP on lodge's agent socket, with a token
  scoped to one folder
  ([ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
  On a desktop there is no agent socket.
- State is `/var/lib/frostread` (`STATE_DIRECTORY`); the configuration is
  `/etc/frostread/frostread.json` (`READ_CONFIG`). The only secrets are the
  content MAC's key, in the database, and the notes token, in
  `STATE/tokens/notes`.
- The kit's JSON-RPC lines are at most `jsonrpc.MaxMessageSize`, 16 MiB,
  and lodge proxies the same lines. An item's images may total 40 MiB.

The suite has solved most of this twice:

- frostnotes
  [ADR-0031](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0031-the-desktop-app-ships-as-a-flatpak-with-its-own-notesd.md)
  (Accepted, and tested on a real Snow desktop on 2026-10-26): a Tauri v2
  shell over the same React UI as the web build, with a transport per
  host; notesd in its own sandbox instance, started through
  `flatpak-spawn`; one notesd per data directory by `flock`; the socket in
  `$XDG_RUNTIME_DIR/app/<app-id>/`, the one directory all of an app's
  instances share; the Background portal through a PyGObject helper;
  offline Go, cargo and pnpm sources.
- frostmail
  [ADR-0046](https://github.com/frostyard/frostmail/blob/main/docs/adr/0046-the-desktop-flatpak-carries-its-own-maild.md)
  (Proposed, merged 2026-10-27): the same layout, plus 2 modes chosen in
  `app.json`, the mode checked by the daemon under its lock, an ordered
  stop when the user leaves local mode, a forward-only replacement of the
  daemon after an update, and the SaveFile portal for files.
- frostmail [ADR-0005](https://github.com/frostyard/frostmail/blob/main/docs/adr/0005-html-mail-rendering.md),
  [ADR-0014](https://github.com/frostyard/frostmail/blob/main/docs/adr/0014-images-embedded-in-the-reader-frame.md)
  and [ADR-0036](https://github.com/frostyard/frostmail/blob/main/docs/adr/0036-reader-links-open-as-new-window-requests.md)
  show untrusted HTML in WebKitGTK without a second origin: sanitized in
  the daemon, in a `srcdoc` frame without `allow-scripts`, under an app CSP
  with no network source, images as `data:` URLs (a custom scheme does
  not load inside a child frame on WebKitGTK), and links as new-window
  requests that the shell opens in the browser. Its hostile suite found no
  script and no request.
- lodge's [lodge-http.md](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#native-clients)
  admits a native client, such as frostmail's bridge: it opens
  `wss://LODGE/app/<id>/api` with `Origin: https://LODGE`, and its
  identity is the node's Tailscale WhoIs.

What a reader is shapes the lodge mode:

- Its data is the owner's subscriptions, the items the feeds sent, the
  extracted articles with their originals and images, and the owner's
  state on them: read, starred, archived, saved, a note's path.
- Two pollers of one feed are not a correctness fault, as two mail
  engines are, but each fetches every page and image again and extracts
  them on its own. Items have no identity shared between two stores: a
  saved link is the owner's by URL, and a feed item is its source's by GUID.
- On lodge, the owner reads on the phone; the desktop would add the same
  list on a larger screen.

## Decision

### The app, its ID and its shell

- **The Flatpak is `org.frostyard.Frostread`**, named "Frostread" in the
  desktop's app grid, beside Frostmail and Frostnotes (core
  [ADR-0016](https://github.com/frostyard/core/blob/main/docs/adr/0016-reverse-dns-org-frostyard-identifiers.md)).
  lodge keeps the app id `read` and the tab "Read".
- **The shell is Tauri v2 over the existing web bundle**, as frostnotes'
  is. `web/src-tauri/` holds the Rust shell; `web/` stays one pnpm project
  with 2 entries:
  - the lodge entry, `main.tsx`, unchanged;
  - a desktop entry, `desktop.tsx`, with a transport over Tauri's bridge,
    the system's color scheme for the theme, the window's own routes, and
    links through the opener plugin.

  The components (source column, item list, reader, sheets) are shared.
  The desktop leaves out what only lodge can serve: the save page, the
  bookmarklet and its sidebar entry.
- **`bridge.rs` moves JSON-RPC lines and never parses them,** as
  frostnotes' and frostmail's do: to readd's socket in local mode, and to
  lodge's `/app/read/api` in lodge mode. The webview fetches nothing from
  the network; only readd and the Rust layer do.

### Two modes, chosen by the user

- **Local, the default.** The Flatpak runs its own readd, and the window
  talks to it as caller `local` over its private socket. Feeds poll,
  articles extract, search works, and links save to read later, all
  locally. A fresh install opens on the empty unread view with "Add a
  feed", "Import OPML" and "Save a link"; "Connect to lodge…" is a
  secondary action. Nothing about lodge, Tailscale or Notes is asked.
- **Connected to lodge, by explicit choice.** The window uses lodge's
  readd as a native client: `wss://LODGE/app/read/api`, with `Origin:
  https://LODGE`, as caller `session`, with lodge's TLS certificate and a
  ping every 30 s, as frostmail's bridge does. This is a **connection, not
  a sync**: the desktop holds no copy, and lodge's readd is the only
  engine. Read on the desktop then shows the same sources, items, states
  and saved links as the phone.
- **The choice is `app.json`** in the app's config directory: `{"lodge":
  "https://lodge.example.ts.net"}` is lodge mode; no file, or no `lodge`,
  is local. Only the Server sheet writes it, and only when the user saves
  a change. lodge's address is `https`, or `http` on loopback for lodge's
  development build.
- **A lost connection is shown, never replaced.** When lodge is
  unreachable in lodge mode, the window says so and retries; it never
  falls back to the local store, which would change the backend silently.

Why a connection and not a sync:

- Sync needs a protocol that frostread does not have: item identity across
  2 stores, rules for read, starred, archived and saved state, and
  sources added or removed on either side. Notes' hub protocol does not
  fit items, whose content the daemon makes, not the owner.
- In a sync, either both daemons fetch every page and image, with 2
  extractions that can differ, or one ships articles, originals (10 MiB
  each) and images (40 MiB per item) to the other.
- A connection reuses what exists: the web bundle is already a lodge
  client, lodge admits native clients, and frostmail's bridge is the model.
- The cost is offline reading in lodge mode: the desktop shows nothing of
  lodge's while lodge is unreachable. Local mode always works offline for
  what it has fetched. A sync can come later, in its own ADR, if offline
  reading of lodge's list is wanted.

### Which engine runs in each mode

| Mode | Engine | What runs on the desktop |
| --- | --- | --- |
| Local | the Flatpak's own readd | the window, and readd in its own sandbox instance, through logout, and from login when the Background portal allows it |
| Lodge | lodge's readd | the window only; no readd |

- **Never a host readd.** The Flatpak neither starts nor connects to a
  readd on the host. A host readd is lodge's, under `/var/lib/frostread`,
  and the Flatpak cannot see that directory.
- **One readd per data directory.** readd takes an exclusive `flock` on
  `<state>/readd.lock` before it opens the database, and holds it until it
  exits. A second readd on the same directory exits at once with "another
  readd uses <dir>", whatever its socket. This holds on lodge's host too.
- **The mode is checked under the lock.** In desktop mode, readd reads
  `app.json` (it reads it, never writes it) after it holds the lock and
  before it opens the database. In lodge mode it releases the lock, logs
  one line and exits 0. The launcher checks the mode too, to skip a
  needless start, but readd's check is the one that counts.
- **Leaving local mode stops readd, in frostmail's order.** When the user
  saves lodge in the Server sheet, the app:
  1. writes `app.json` with lodge (temporary file, synced, renamed);
  2. calls `daemon.shutdown` (new; caller `local` only) when a readd
     answers. readd stops its poller and workers, finishes the write it is
     in, releases its lock and exits;
  3. takes the data lock itself, waiting up to 30 s and repeating step 2
     each second, then releases it;
  4. if the lock is still held after 30 s, writes local back to
     `app.json`, connects to the local readd again, and shows "This
     computer's reader did not stop. Frostread still uses it." The mode
     does not change half-way.

  Saving local again writes `app.json`, then starts readd through the
  launcher.
- **Every start in lodge mode repeats steps 2 and 3.** Before the window
  opens its lodge connection, and each time the launcher runs in lodge
  mode, it calls `daemon.shutdown` when a local readd answers, and takes
  and releases the data lock, waiting up to 30 s. This ends a readd that
  a crash left running after step 1, or that started from a stale
  autostart and has not yet reached its mode check. When the lock stays
  held, the window does not connect to lodge: it shows "This computer's
  reader did not stop" with a retry, and the mode stays lodge in
  `app.json`. So no readd runs while the window uses lodge, whatever
  happened between the steps.
- **The Server sheet says what each change does** before the user saves,
  from local facts only, so it reads the same while lodge is unreachable:
  - to lodge: "This computer's reader stops polling. Its sources and
    saved articles stay on this computer, not synced, until you switch
    back. Nothing moves to lodge.";
  - to local, when a database exists: "This computer's reader starts
    again with the sources and articles it had. Nothing moves from
    lodge.";
  - to local, when none exists: "This computer starts an empty reader.
    Your list on lodge stays there. To bring your subscriptions, export
    OPML from Read in lodge and import it here."
- **An update replaces readd, only forward,** as frostmail ADR-0046 does
  for maild. `status` gains an optional `build`: in the Flatpak, the
  `app-commit` of the instance's `/.flatpak-info`. The launcher starts
  readd with `flatpak-spawn --latest-version` and asks the latest
  deployment's commit the same way (`readd -build`). When the running
  readd's `build` is not the latest, the launcher calls `daemon.shutdown`
  and starts the latest. Migrations run only in the readd that holds the
  lock.
- **The replacement happens at the launcher's next run,** not at the
  update itself: the next window opened from the app grid or by
  `flatpak run`, or the next login. Until then the old readd and any old
  window keep working together. A window that is open during the
  replacement loses its connection, reconnects to the new readd, and
  calls only methods that the new one keeps, because methods change only
  by addition within protocol 1. Replacing readd as soon as an update
  lands, through the Flatpak portal's update monitor, is a later idea
  (frostnotes plan 0008's G3).

### readd's desktop mode

- **`readd -desktop`** opens no lodge socket, serves no `/content/` and no
  MCP, and builds no notes client. It serves only its private socket,
  which admits only the user's own processes, as today. It listens on no
  TCP port: the rule that only lodge listens on the network holds, because
  nothing listens on the network on the desktop.
- **Paths.** In desktop mode, the state directory is
  `$XDG_DATA_HOME/frostread`, the configuration
  `$XDG_CONFIG_HOME/frostread/frostread.json` (optional, as today), and
  the socket
  `$XDG_RUNTIME_DIR/app/org.frostyard.Frostread/frostread/readd.sock` when
  `FLATPAK_ID` is `org.frostyard.Frostread`. `READ_SOCKET` still wins.
  `internal/config` and the shell's `paths.rs` both derive the socket,
  each with tests. `readctl` finds the same socket, so `flatpak run
  --command=readctl org.frostyard.Frostread status` works.
- **The configuration.** `poll_interval`, `blob_sweep` and
  `blob_grace_days` apply as on lodge. `lodge_url`, `notes_folder` and
  `notes_vault` have no effect in desktop mode; readd logs that once when
  they are set.
- **`-log PATH`** appends readd's log to a file, and readd rotates it past
  1 MiB once it holds the lock, as notesd does.
- **The fetcher is unchanged.** Every request still goes through
  `internal/fetch` on `kit/netguard`
  ([ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md)). On a
  desktop too, it refuses private, loopback, link-local and tailnet
  addresses, so a feed on the user's own network cannot be read. The
  `devfetch` and `devfeeds` tags stay out of every Flatpak build.

### Untrusted article content without lodge-content

ADR-0005's 3 steps in the daemon do not change: extract, sanitize with the
same policy, inline the images, and keep the original as sent. What
changes is where the page is shown.

- **The daemon renders the page for the window.** A new method,
  `items.render {id, scheme}`, answers `{html, omittedImages}`: the same
  document that `internal/content` serves to lodge-content, from the same
  code, with a `<meta http-equiv="Content-Security-Policy"
  content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">`
  first in its head. Callers `local` and `session` may call it, so both
  modes show articles the same way.
- **The answer stays under the transport's limit.** The limit is the
  whole JSON-RPC answer as it goes on the wire: the envelope, and the
  HTML after JSON escaping, which can grow text (a quote or a backslash
  takes 2 bytes, a control character 6). The kit refuses a line of 16 MiB
  or more (`jsonrpc.MaxMessageSize`), so readd keeps the encoded answer
  under 12 MiB:
  1. It renders the page with no image embedded, and measures the
     encoded answer. When that alone is 12 MiB or more, it answers
     `tooLarge` (a new application error in `schema/rpc/errors.yaml`,
     after `unavailable`), and the reader shows the excerpt with
     "This article is too large to show in the app" and a link to the
     original's URL.
  2. Otherwise it embeds the images as `data:` URIs in document order.
     An image is embedded only when the encoded size of its `data:` URI
     (base64, 4 bytes for each 3, plus its prefix) still fits, so the
     same image twice counts twice. An image that does not fit loses its
     `src` and keeps its alt text, as an image that could not be fetched
     does, and `omittedImages` counts it; the reader says how many were
     left out.
  3. It measures the final encoded answer again before it sends it, and
     falls back to step 1's page when it is still 12 MiB or more.

  The blobs stay in the store, so nothing is lost. Tests cover the
  boundary: text heavy with quotes and backslashes, markup near the
  limit, the same image many times, and an answer of exactly the limit.
- **The window shows it in `<iframe srcdoc sandbox="allow-popups">`:** no
  `allow-scripts` and no `allow-same-origin`, so the article runs no
  script and has an opaque origin, apart from the app's. The frame fills
  the reader and scrolls itself, so the parent never needs to reach into
  it.
- **The app's CSP has no network source** (`tauri.conf.json`): `default-src
  'self'`, `img-src 'self' data:`, `style-src 'self' 'unsafe-inline'`,
  `connect-src` only Tauri's IPC, `frame-src 'self'`, `form-action 'none'`
  and `object-src 'none'`. A `srcdoc` frame inherits it, on top of its
  own.
- **Links open in the browser.** The sanitizer already gives every link
  `target="_blank"`, so a click is a new-window request. The main window's
  `on_new_window` opens `http`, `https` and `mailto` with the opener
  plugin, which in the Flatpak is the OpenURI portal, and always answers
  Deny. `on_navigation` admits only the app itself.

The 3 layers stand on their own: the daemon's sanitizer, the frame's
sandbox, and a CSP with no network source. A hostile corpus in WebKitGTK
tests them, as frostmail's suite does.

### Notes is optional

- **Read never needs Notes.** No part of local mode reads, writes or waits
  for Notes.
- **In local mode there is no save as note.** The desktop has no agent
  socket, and readd builds no notes client, so `status.notes.configured`
  is false and the reader hides the button. The reader offers **Export as
  Markdown…** instead: readd's `items.exportMarkdown {id}` (caller
  `local`) answers
  `{filename, markdown}` with the same conversion and frontmatter as a
  note (`internal/notes`, which owns html-to-markdown), less the
  `read_item` marker, and the shell writes it where the user picks through
  the FileChooser portal's `SaveFile`. A file the user saves into a vault
  folder is the user's file; Read holds no vault path and no Notes link.
- **In lodge mode, save as note works as on lodge.** lodge's readd saves
  through notes' MCP on the agent socket
  ([ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md),
  unchanged). "Open in Notes" opens `https://LODGE<route>` in the browser,
  because the desktop has no lodge shell to open it in.
- **A direct link to a desktop Frostnotes is not built.** It needs a
  scoped interface in frostnotes, a frostnotes ADR, and a permission the
  user grants (core ADR-0062: a connection the user permits, only to the
  daemon's interface). It stays a later idea.

### The Flatpak's layout and lifecycle

These follow frostnotes ADR-0031 and frostmail ADR-0046; only the names
differ.

- **Build.** `packaging/flatpak/org.frostyard.Frostread.json`, on
  `org.gnome.Platform//51`, offline. A `readd` module builds `readd` and
  `readctl` with the Go SDK extension and `CGO_ENABLED=0`, from a
  generated `go-sources.json` (frostnotes' `tools/flatpakgo`). A
  `frostread` module builds the shell from `cargo-sources.json` and
  `node-sources.json`. `make flatpak-sources` writes all 3, in the nsl
  machine where it needs Node or Rust, and CI fails on any difference.
- **Install layout.** `/app/bin/frostread` is a POSIX sh launcher (the
  manifest's `command`); `/app/bin/readd` and `/app/bin/readctl` are the Go
  binaries; `/app/bin/frostread-request-background` is the Background
  helper; `/app/libexec/frostread-app` is the Tauri binary.
- **The launcher.**
  - `frostread --background` (local mode) runs `exec readd -desktop -log
    $XDG_STATE_HOME/frostread/readd.log`.
  - `frostread [ARGS]` in local mode: when `readctl status` fails, it runs
    `flatpak-spawn --latest-version frostread --background`, waits about
    10 s for `readctl status`, then ends `flatpak-spawn` with `SIGKILL`,
    so the window's sandbox does not wait on readd. Without the Flatpak
    portal it starts no readd of its own, and the window says that the
    reader is not running. It then asks the Background portal for
    `background` and `autostart` with `frostread --background`, through
    the PyGObject helper, and `exec`s the app.
  - In lodge mode it starts no readd and asks for no background.
- **Background.** readd keeps polling after the last window closes, until
  logout, and from login when the user allows it. A refusal stops only
  the start at login. The app has no switch for it yet; `flatpak kill
  org.frostyard.Frostread` stops it.
- **Permissions** (`finish-args`): `--socket=wayland`,
  `--socket=fallback-x11`, `--share=ipc`, `--device=dri` and
  `--share=network`. No filesystem, no Secret Service and no
  notifications. OPML import uses the web view's file input, which GTK
  routes through the FileChooser portal in a sandbox; OPML export and
  Export as Markdown use `SaveFile`; links use OpenURI. The Background,
  FileChooser, OpenURI and Flatpak portals need no finish-arg.

### Where the data lives

In local mode everything is the app's own, under
`~/.var/app/org.frostyard.Frostread/`, which `flatpak uninstall
--delete-data` removes:

| What | Where |
| --- | --- |
| `frostread.db`, `blobs/`, `readd.lock` | `data/frostread/` (readd's state directory) |
| `frostread.json` (optional), `app.json` (the mode) | `config/frostread/` |
| `readd.log` | `.local/state/frostread/` |
| the socket | `$XDG_RUNTIME_DIR/app/org.frostyard.Frostread/frostread/` |

- **No secret leaves the database.** The content MAC's key stays in the
  database's `settings`, as today, and serves no route on the desktop.
  Local mode has no notes token. Lodge mode holds no token: lodge knows
  the desktop by its Tailscale identity. So the Flatpak needs no Secret
  Service and keeps no secret file.
- **lodge's state is untouched.** The desktop never reads or writes
  `/var/lib/frostread` or lodge's configuration.

### Moving from lodge: optional and explicit

- An owner who reads only through lodge today needs no move: lodge mode
  shows the same list.
- An owner who wants a local reader brings the subscriptions with OPML:
  "Export OPML" in Read in lodge, then "Import OPML" in the desktop app.
  The feeds then poll locally. Items, read state, stars, saved links and
  archived items do not move; lodge keeps them.
- The app never copies anything from lodge by itself, and never changes
  lodge's data.

### What this changes in the earlier decisions

- **[ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md):**
  unchanged on lodge. On the desktop, save as note exists only in lodge
  mode, through lodge's readd.
- **[ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md):**
  the daemon's steps are unchanged. "Shown only from lodge-content" now
  has a desktop counterpart: `items.render` in a sandboxed `srcdoc` frame
  under a CSP with no network source.
- **AGENTS.md's invariants** gain the desktop's form of the content rule,
  and "readd serves Unix sockets only" gains "and on a desktop, only its
  private socket", when this ADR is accepted.

## Consequences

- A fresh desktop installs Read and uses it with nothing else of
  frostyard's. lodge's `read` app, its packaging and its sysext are
  unchanged.
- `web/` gains a desktop entry, a Tauri transport and a Rust shell, and
  the repository gains a Rust toolchain in the nsl machine, cargo and
  pnpm sources for the Flatpak, and a `flatpak.yml` workflow that
  `internal/workflowcheck` must accept.
- readd gains `-desktop`, `-log`, `-build`, the lock, the mode check under
  it, and the methods `daemon.shutdown`, `items.render` and
  `items.exportMarkdown`, the error `tooLarge`, and `status`'s `build`. `schema/rpc`,
  [specs/api.md](../specs/api.md) and the contract goldens follow.
- The socket path, like the state directory, has 2 sources of truth:
  `internal/config` and `paths.rs`, each with tests.
- An article whose embedded images pass 12 MiB shows some images as alt
  text on the desktop, where lodge-content would show them all.
- An article's page, its images as base64 inside it, is sent again over
  the socket on each open. That costs memory and time for a large
  article, bounded by the 12 MiB answer. A very large article shows only
  its excerpt and a link on the desktop.
- A desktop in local mode and lodge's readd each poll the same feeds when
  the owner subscribes in both. Each keeps its own list and state; the app
  does not merge them.
- In lodge mode nothing reads while lodge is unreachable, and save as note
  needs lodge's notes token, as on the phone.
- On the desktop in local mode there is no save as note, only a Markdown
  file the user places. Notes does not record it as Read's.
- readd polls in the background until logout in local mode, and at login
  when allowed. That uses the network with no window open.
- The portal paths (`flatpak-spawn`, Background, FileChooser, SaveFile,
  OpenURI) have no automated test against a real portal. Each change to
  them needs a check on a real desktop, and the release needs the
  clean-install gate of [plan 0002](../plans/0002-the-standalone-desktop-flatpak.md).
- No agent reaches the desktop's readd: it serves no MCP. Agents reach
  Read only through lodge's `/mcp/read`, as before.

## Alternatives considered

- **lodge sync:** offline reading of lodge's list, at the cost of a new
  protocol for item identity and state, and either 2 fetchers per feed or
  articles and blobs shipped between stores. Rejected for now; a later ADR
  can add it beside the connection.
- **Both modes from the start:** 2 lodge paths to build and test before
  the first release, for a gain only in offline reading. Rejected.
- **A web view on lodge's own page, `https://LODGE/read/`:** no local mode,
  and the remote shell's code would run in the app's window. Rejected.
- **Show lodge-content's page in the frame in lodge mode:** the webview
  would load a remote document, and the CSP would need a network source.
  `items.render` over the API keeps one path and no network in the webview.
- **Serve the article from a custom scheme with a sandbox header,** as
  lodge-content does: a second origin and no size limit, but frostmail
  found that a custom scheme does not load inside a child frame on
  WebKitGTK (ADR-0014). Rejected until a spike shows that a document,
  not only an image, loads.
- **`allow-same-origin` on the frame,** as frostmail grants it for sizing:
  the reader's frame scrolls itself and needs no sizing, so an opaque
  origin costs nothing.
- **readd in the window's sandbox:** it dies with the window, and with a
  duplicate window (frostnotes' first design). Rejected.
- **A host readd beside the Flatpak:** a second install, which the rule
  forbids, and a Flatpak cannot install a user unit. Rejected.
- **Save as note to a local Frostnotes through its socket:** notesd's
  local socket gives full access, with no scope, and reaching it needs a
  permission into another app's runtime directory. Rejected until
  frostnotes offers a scoped interface.
- **Write notes into a vault folder Read holds:** Read would hold a vault
  path and write Notes' files, which ADR-0004 rejects. Rejected; the
  user's own SaveFile choice is not a link.
- **Move lodge's store to the desktop on first start:** it moves data
  without a decision. Rejected.
- **Let the fetcher reach the user's own network on a desktop:** it would
  split ADR-0003's invariant by host. Rejected; it needs its own ADR if
  asked for.

## References

- Shapes: [plan 0002](../plans/0002-the-standalone-desktop-flatpak.md), [design/overview.md](../design/overview.md), [specs/api.md](../specs/api.md) (the new methods, the desktop's paths, when built), `packaging/flatpak/` (to come), `web/src-tauri/` (to come)
- Amends in part: [ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md) (save as note on the desktop), [ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md) (where the page is shown)
- Builds on: [ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md), [ADR-0007](0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md) (the sweep, unchanged on the desktop)
- Core: [ADR-0062](https://github.com/frostyard/core/blob/main/docs/adr/0062-make-desktop-suite-apps-standalone-flatpaks-with-opt-in-lodge.md), [ADR-0058](https://github.com/frostyard/core/blob/main/docs/adr/0058-ship-suite-desktop-apps-as-flatpaks-and-services-as-sysexts.md), [ADR-0016](https://github.com/frostyard/core/blob/main/docs/adr/0016-reverse-dns-org-frostyard-identifiers.md), [plan 0010](https://github.com/frostyard/core/blob/main/docs/plans/0010-suite-packaging.md)
- Models: frostnotes [ADR-0031](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0031-the-desktop-app-ships-as-a-flatpak-with-its-own-notesd.md); frostmail [ADR-0046](https://github.com/frostyard/frostmail/blob/main/docs/adr/0046-the-desktop-flatpak-carries-its-own-maild.md), [ADR-0005](https://github.com/frostyard/frostmail/blob/main/docs/adr/0005-html-mail-rendering.md), [ADR-0014](https://github.com/frostyard/frostmail/blob/main/docs/adr/0014-images-embedded-in-the-reader-frame.md), [ADR-0036](https://github.com/frostyard/frostmail/blob/main/docs/adr/0036-reader-links-open-as-new-window-requests.md)
- lodge: [lodge-http.md, native clients](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#native-clients)
