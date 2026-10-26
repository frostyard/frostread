# Read

frostread is a tailnet-only feed reader and read-later list. It runs behind
[lodge](https://github.com/frostyard/lodge), frostyard's tailnet-only web
shell, as app `read`.

- **Sources.** RSS, Atom and JSON Feed, by URL or from a page's
  `<link rel="alternate">`, imported and exported as OPML. Each source
  polls every 20 minutes or so, with conditional GET and a per-source
  backoff.
- **Read later.** Save a link from the app, from `readctl save <url>`, or
  from an agent with a `read;write` token.
- **From your phone.** A bookmarklet opens Read's save page with the
  page's address and title. Read shows both and saves only when you tap
  Save, so it works in iOS Safari and needs no token.
- **Articles.** The main content is extracted, sanitized, and its images
  are fetched into a content-addressed store and inlined, so the browser
  loads nothing remote. The original bytes are kept as sent.
- **Reading.** Unread, starred and archived views, full-text search, and
  permalinks at `/read/item/<id>`.
- **Save as a note.** A reader button writes the article into notes under
  `Reading/`, through notes' MCP on lodge's agent socket.
- **Agents.** Porter and any other agent reach it at `/mcp/read` with a
  scoped token.

Status: pre-release, 2026-10-22. The repository is public since
2026-10-23. The daemon, its API, `readctl`, MCP, save-as-note, the web
app, save from the phone and the blob sweep are built. It runs on lodge's
kit **v0.3.0**, and the bundle on `@frostyard/lodge-client`
**client/v0.3.2** and `@frostyard/lodge-ui` **ui/v0.6.0**. See the
[roadmap](docs/plans/0001-roadmap.md).

## Build

```sh
make check              # tidy, vet, gofmt, lint and tests
make build              # build/readd and build/readctl
make lodge-package      # stage them for lodge under build/lodge-root
make dev-lodge          # lodge's development build on loopback, with Read and Notes
```

`make dev-lodge` starts lodge's development build with frostread and
frostnotes in it, generated feeds and a generated vault, and frostread's
scoped notes token, so save-as-note works end to end. It reads `../lodge`
and `../frostnotes` (`LODGE_DIR`, `NOTES_DIR`) and changes neither. See
`scripts/dev-lodge.sh`.

The web bundle builds only in an nsl machine with Node, never on the host:

```sh
make web-build
```

## How it runs behind lodge

lodge owns every network listener. `readd` serves:

| Socket or route | What |
| --- | --- |
| `/run/lodge/apps/read.sock` | lodge's app socket: `/health`, `/api`, `/content/`, `/mcp` |
| `/run/frostread/readctl.sock` | `readctl`, the CLI |
| `https://CONTENT/read/<path>` | an article's content page, sandboxed |

It runs as user `frostread`, in groups `lodge-apps` and `lodge-agents`,
and listens on no network. Its state is `/var/lib/frostread`, and its
configuration is `/etc/frostread/frostread.json`
(`docs/specs/api.md`).

## Install on lodge's host

frostread runs as lodge's `read` app, beside lodge and notes, on the same
host. `LODGE` below is lodge's address, `lodge.<tailnet>.ts.net`.

1. **Build the bundle and stage the app.** The web bundle builds in an nsl
   machine. `lodge-package` stages `readd`, `readctl`, the manifest, the
   bundle, the units and the sysusers entry under `build/lodge-root`; it
   refuses a whole root, so copy the staged `usr/` over the host's, with
   the app's own directory replaced whole. The staging writes only under
   `usr/`: `readd` and `readctl` land in `/usr/bin`, and the unit's
   `ExecStart=` is `/usr/bin/readd`
   ([core ADR-0058](https://github.com/frostyard/core/blob/main/docs/adr/0058-ship-suite-desktop-apps-as-flatpaks-and-services-as-sysexts.md)).

   ```sh
   make web-build                             # in nsl
   make lodge-package                         # stages build/lodge-root
   sudo rm -rf /usr/share/lodge/apps/read     # only Read's own directory
   sudo cp -r build/lodge-root/usr/. /usr/    # not -a: the files must be root's
   ```

   **Once, on a host that installed an earlier version** (which put the
   binaries in `/usr/local/bin`): remove the old copies, because
   `/usr/local/bin` comes before `/usr/bin` on `PATH` and a stale
   `readctl` there would shadow the new one.

   ```sh
   sudo rm -f /usr/local/bin/readd /usr/local/bin/readctl
   command -v readctl                         # expect /usr/bin/readctl
   systemctl cat frostread.service            # expect ExecStart=/usr/bin/readd
   ```

   The steps below copy the unit to `/usr/lib/systemd/system/`, so a full
   copy of it in `/etc/systemd/system/frostread.service` would shadow the
   new one and still start `/usr/local/bin/readd`. These steps make only
   the drop-in `tz.conf` (step 2), which is safe; remove any full copy.

2. **Create the user and start the socket.** The sysusers entry makes
   `frostread`, with its home at `/var/lib/frostread`, in group
   `lodge-apps` and in group `lodge-agents` (the agent socket).

   ```sh
   sudo systemd-sysusers /usr/lib/sysusers.d/frostread.conf
   id frostread          # expect lodge-apps and lodge-agents
   sudo systemctl daemon-reload
   sudo systemctl enable --now frostread.socket frostread.service
   ```

   **On an upgrade**, `enable --now` does not restart a service that
   runs, so readd keeps its old binary until a restart. Restart it, then
   check the unit and the running version:

   ```sh
   sudo systemctl restart frostread.service
   systemctl show -p ExecStart frostread.service   # expect /usr/bin/readd
   sudo -u frostread /usr/bin/readctl status       # expect readd <the new version>
   sudo lodgectl reload                            # lodge rereads the manifest
   ```

   The service is enabled too, so the poller runs from boot, not only
   after lodge's first request. On a host whose clock is UTC, give readd
   the owner's zone, as notes' unit does: a drop-in
   `/etc/systemd/system/frostread.service.d/tz.conf` with
   `[Service]` and `Environment=TZ=America/New_York`. Saved notes are
   dated, and article pages show their day, in that zone.

3. **Write the configuration**, `/etc/frostread/frostread.json`, owned by
   root, 0644: no secret is in it. `notes_vault` is the id of the vault
   notes serves.

   ```json
   {
     "poll_interval": "20m",
     "lodge_url": "https://LODGE",
     "notes_folder": "Reading/",
     "notes_vault": 1
   }
   ```

4. **Let frostread reach notes.** Add `frostread` to `agents` in
   `/etc/lodge/config.json` (the list is at most 16 system users), then
   reload lodge, which also picks up Read's manifest:

   ```json
   { "agents": ["frostread"] }
   ```

   ```sh
   sudo lodgectl reload
   sudo lodgectl status     # the agent socket, and frostread among its agents
   ```

5. **Issue Read's notes token**, scoped to one folder, for the agent
   socket. It is shown once; pipe it straight into `readctl`, which writes
   it to `/var/lib/frostread/tokens/notes` (0600) and prints
   `the notes token is set`.

   ```sh
   sudo lodgectl token issue frostread-notes --app notes \
     --scope 'vault=1;write=Reading/' --agent frostread |
     sudo -u frostread readctl notes-token
   ```

   `readctl` speaks readd's private socket, which admits only frostread's
   own processes; every `readctl` step runs as `sudo -u frostread`.

6. **Subscribe to feeds** from an OPML file, and check the app:

   ```sh
   sudo -u frostread readctl import feeds.opml
   sudo -u frostread readctl status     # save as a note: yes, vault 1, folder Reading/
   sudo -u frostread readctl sources
   ```

   The web app is at `https://LODGE/read/`, and save-as-note writes into
   notes under `Reading/`.

## Docs

- [docs/README.md](docs/README.md): the index.
- [docs/design/overview.md](docs/design/overview.md): the parts and how
  they fit.
- [docs/specs/api.md](docs/specs/api.md): the API, `readctl`, the config
  and the state layout.
- [AGENTS.md](AGENTS.md): the rules for every agent that works here.
