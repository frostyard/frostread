<!-- readme-kind: service -->
# Read

Read is a tailnet-only feed reader and read-later list. It polls RSS, Atom
and JSON Feed sources, saves links to read later, and shows each article as
its sanitized text. It runs behind
[lodge](https://github.com/frostyard/lodge), frostyard's tailnet-only web
shell, as app `read`, for the people on the tailnet.

**Status:** pre-release.

**Documentation:** [docs/README.md](docs/README.md)

## What it does

- **Sources.** RSS, Atom and JSON Feed, by URL or from a page's
  `<link rel="alternate">`, imported and exported as OPML. Each source
  polls every 20 minutes or so, with conditional GET and a per-source
  backoff ([fetching](docs/specs/api.md#fetching)).
- **Read later.** Save a link from the app, from `readctl save <url>`, or
  from an agent with a `read;write` token
  ([methods](docs/specs/api.md#methods)).
- **From your phone.** A bookmarklet opens Read's save page with the
  page's address and title, and Read saves only when you tap Save, so it
  works in iOS Safari and needs no token
  ([ADR-0006](docs/adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md)).
- **Articles.** The main content is extracted and sanitized, and its images
  are fetched into a content-addressed store and inlined, so the browser
  loads nothing remote. The original bytes are kept as sent
  ([ADR-0005](docs/adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
- **Reading.** Unread, starred and archived views, full-text search, and
  permalinks at `/read/item/<id>`
  ([web routes](docs/specs/api.md#web-routes)).
- **Save as a note.** A reader button writes the article into notes under
  `Reading/`, through notes' MCP on lodge's agent socket
  ([ADR-0004](docs/adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
- **Agents.** Any agent reaches it at `/mcp/read` with a scoped token, and
  the engine enforces the token's scope
  ([MCP](docs/specs/api.md#mcp)).
- **A small state.** A daily mark-and-scan sweep deletes the blobs that no
  item uses, after a grace period
  ([ADR-0007](docs/adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)).

## Deploy

Read runs on lodge's host, beside lodge and notes: it is lodge's `read`
app, installed from a release's `frostyard-readd` `.deb` as
`/usr/bin/readd` under the system user `frostread`, and lodge serves its web
bundle at `/app/read/`. A change reaches it as a `vX.Y.Z` tag that
`make bump` pushes: the tag's workflow builds, attests and publishes the
`.deb`, and the host installs it by hand. The runbooks are in
[packaging/README.md](packaging/README.md):
[install on lodge's host](packaging/README.md#install-on-lodges-host),
[update](packaging/README.md#update),
[roll back](packaging/README.md#roll-back),
[after the sysexts](packaging/README.md#after-the-sysexts) and
[release](packaging/README.md#release).

## Interface

| Socket or route | What it does |
| --- | --- |
| `/run/lodge/apps/read.sock` | lodge's app socket: `/health`, `/api`, `/content/` and `/mcp` |
| `/run/frostread/readctl.sock` | `readctl`, the CLI: readd's private socket |
| `/app/read/` | the web app, which lodge serves |
| `CONTENT/read/item/<id>/<mac>` | an article's content page: a MAC under the daemon's secret is the only permission |
| `/mcp/read` | MCP, with a scoped token |

It runs as user `frostread`, in groups `lodge-apps` and `lodge-agents`, and
listens on no network. Its state is `/var/lib/frostread`, and its
configuration is `/etc/frostread/frostread.json`. The
[API spec](docs/specs/api.md) is the contract: the JSON-RPC methods, the
MCP tools and scope, the content routes, `readctl` and the state layout.

## Problem records

readd and the desktop app record their panics, and readd 5 coded internal
errors, in the local systemd journal: a migration that fails, a second
readd on lodge's host, an extraction or a render that fails in readd, and a
blob sweep that fails. frostread sends nothing over a network for them.
Only chairlift, when its user turns reporting on, sends reports from a
host. Only a release build records. See
[docs/specs/problem-records.md](docs/specs/problem-records.md).

## Develop

```sh
mise install     # the pinned golangci-lint, svu and nFPM
make check       # format, then tidy, vet, lint and the Go tests
make ci          # verify, the race detector, the cross builds, the web bundle and the app (nsl)
make dev-lodge   # lodge's development build on loopback, with Read and Notes
make web-build   # the web bundle, in an nsl machine
```

Go 1.27 builds on the host; the web bundle and the desktop app build in the
nsl machine, never on the host. `make help` lists every target, and
[AGENTS.md](AGENTS.md#gates) has the gates in full.

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs and plans.
- [SECURITY.md](SECURITY.md): report a vulnerability privately.
- [LICENSE](LICENSE): MIT.
