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
   the app's own directory replaced whole.

   ```sh
   make web-build                             # in nsl
   make lodge-package                         # stages build/lodge-root
   sudo rm -rf /usr/share/lodge/apps/read     # only Read's own directory
   sudo cp -r build/lodge-root/usr/. /usr/    # not -a: the files must be root's
   ```

2. **Create the user and start the socket.** The sysusers entry makes
   `frostread`, with its home at `/var/lib/frostread`, in group
   `lodge-apps` and in group `lodge-agents` (the agent socket).

   ```sh
   sudo systemd-sysusers /usr/lib/sysusers.d/frostread.conf
   id frostread          # expect lodge-apps and lodge-agents
   sudo systemctl daemon-reload
   sudo systemctl enable --now frostread.socket frostread.service
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

## Update

frostread has no release. An update moves the host to a newer commit of
`main`. While frostread is stopped, the Read tab does not work, the
poller does not run, and lodge shows the app as down.

1. **Note the old version, and stop frostread.** Stop the socket too:
   it starts the service again on the next connection.

   ```sh
   sudo -u frostread readctl status     # prints: readd <version>
   sudo systemctl stop frostread.socket frostread.service
   ```

2. **Take a rollback archive.** Keep it root-only: the notes token is a
   secret. Name it with the old version and a UTC time. The binaries are
   in `/usr/bin` (builds from frostread#20 on) or in `/usr/local/bin`
   (earlier builds); `--ignore-failed-read` skips the path that does not
   exist.

   ```sh
   sudo sh -c 'umask 077; tar --ignore-failed-read -czpf \
     /root/frostread-OLDVERSION-$(date -u +%Y%m%dT%H%M%SZ).tar.gz \
     /var/lib/frostread /etc/frostread /usr/share/lodge/apps/read \
     /usr/bin/readd /usr/bin/readctl \
     /usr/local/bin/readd /usr/local/bin/readctl \
     /usr/lib/systemd/system/frostread.socket \
     /usr/lib/systemd/system/frostread.service \
     /usr/lib/sysusers.d/frostread.conf'
   ```

3. **Pull and build.** Pull the new commit, then repeat step 1's build
   commands (`make web-build` in nsl, then `make lodge-package`):

   ```sh
   git pull --ff-only origin main
   ```

4. **Copy, reload and start.** Repeat step 1's `rm` and `cp` lines.
   Then:

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl start frostread.socket frostread.service
   sudo lodgectl reload
   ```

5. **Check.** `readctl status` prints the new version, and `read` is up
   in lodge's list, with the new version:

   ```sh
   sudo -u frostread readctl status
   sudo lodgectl apps
   ```

## Roll back

Roll back to the commit you ran before.

1. **Stop frostread**, as in step 1 of "Update".
2. **Put the earlier build back.** Check out the earlier commit, build
   it, and repeat step 1's `rm` and `cp` lines. A build from before
   frostread#20 brings back `/usr/local/bin/readd`, and its unit names
   that path. Or extract the installed files from the rollback archive,
   after you remove `/usr/share/lodge/apps/read`. This leaves the state
   and the configuration as they are:

   ```sh
   sudo tar -xzpf ARCHIVE -C / --exclude=var/lib/frostread --exclude=etc/frostread
   ```

3. **Reload and start**, as in step 4 of "Update":

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl start frostread.socket frostread.service
   sudo lodgectl reload
   ```

4. **Read the journal:**

   ```sh
   sudo journalctl -u frostread -n 50
   ```

   If it says `database schema N is newer than this readd`, the newer
   readd migrated the database, and the older readd refuses it. Stop
   frostread, restore `/var/lib/frostread` from the archive, and start
   again:

   ```sh
   sudo systemctl stop frostread.socket frostread.service
   sudo rm -rf /var/lib/frostread
   sudo tar -xzpf ARCHIVE -C / var/lib/frostread
   sudo systemctl start frostread.socket frostread.service
   ```

   The restore loses what changed after the archive: new items, reads
   and saves. The archive also holds the blobs and the notes token.
5. **Check**, as in step 5 of "Update".

## After the sysexts

This is planned, not built. In core's
[Plan 0010](https://github.com/frostyard/core/blob/main/docs/plans/0010-suite-packaging.md),
Phase 3 ships readd as the sysext `readd` for Snosi images (Floe), not
for this host. lodge's host stays hand-installed, as above (Phase 4 is
deferred, 2026-10-23). On a Snosi image the planned commands are
`sudo updex features update --component=readd` and a reboot to update,
and `sudo updex features rollback readd`, which takes effect at the next
boot. A sysext rollback does not undo a database migration, so keep a
backup of `/var/lib/frostread` there too.

## Docs

- [docs/README.md](docs/README.md): the index.
- [docs/design/overview.md](docs/design/overview.md): the parts and how
  they fit.
- [docs/specs/api.md](docs/specs/api.md): the API, `readctl`, the config
  and the state layout.
- [AGENTS.md](AGENTS.md): the rules for every agent that works here.
