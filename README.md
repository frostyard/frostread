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
kit **v0.3.1**, and the bundle on `@frostyard/lodge-client`
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
   `lodge-apps` and in group `lodge-agents` (the agent socket), and user
   `lodge`, with lodge's own line byte for byte, because readd's
   `lodgeapp.LoadEnv` looks that user up.

   ```sh
   sudo systemd-sysusers /usr/lib/sysusers.d/frostread.conf
   id frostread          # expect lodge-apps and lodge-agents
   sudo systemctl daemon-reload
   sudo systemctl enable --now frostread.socket frostread.service
   ```

   **On an upgrade**, do not run these commands as they are: follow
   [Update](#update), which stops frostread before the copy.

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
   (earlier builds), so the script lists only the paths that exist. Every
   other path must exist, or `tar` fails.

   ```sh
   sudo sh -c 'umask 077
     a=/root/frostread-OLDVERSION-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
     bins=$(ls -d /usr/bin/readd /usr/bin/readctl \
       /usr/local/bin/readd /usr/local/bin/readctl 2>/dev/null)
     tar -czpf "$a" /var/lib/frostread /etc/frostread \
       /usr/share/lodge/apps/read $bins \
       /usr/lib/systemd/system/frostread.socket \
       /usr/lib/systemd/system/frostread.service \
       /usr/lib/sysusers.d/frostread.conf && echo "$a"'
   ```

   Continue only when `tar` exits 0 and prints the archive name. Check
   that the archive holds the state:

   ```sh
   sudo tar -tzf ARCHIVE var/lib/frostread >/dev/null
   ```

3. **Pull and build.** Pull the new commit, then repeat step 1's build
   commands (`make web-build` in nsl, then `make lodge-package`):

   ```sh
   git pull --ff-only origin main
   ```

4. **Copy, reload and start.** Repeat step 1's `rm` and `cp` lines, and
   its one-time removal of `/usr/local/bin/readd` and `readctl` if the
   host still has them. Then:

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
   that path; the next update then needs the one-time removal again. Or extract the installed files from the rollback archive,
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

The sysext `readd` is built and published for Snosi bootc images (Floe).
[snosi#1169](https://github.com/frostyard/snosi/pull/1169) added the
first two suite sysexts (notesd, maild).
[snosi#1173](https://github.com/frostyard/snosi/pull/1173) added lodge,
readd and porterd.
[snosi#1175](https://github.com/frostyard/snosi/pull/1175) pinned readd
0.2.0 and porterd 0.1.1 (lodge stays 0.1.0). Updex downloads the image
from `https://repository.frostyard.org/ext/readd/` and checks its
signature. lodge's own host stays hand-installed (core Plan 0010 Phase 4
is deferred, 2026-10-23), so the steps above stay valid for that host.
The full steps for an image are on
[the site page](https://frostyard.org/docs/sysexts/readd/).

Back up the state before you update. The units are upheld
(`Upholds=`), so a plain `systemctl stop` does not hold: mask them. The
state is not in the sysext. It holds the notes token, so keep the
archive root-only:

```sh
sudo systemctl mask --now frostread.socket frostread.service
sudo sh -c 'umask 077; tar -czpf /root/frostread-state-$(date -u +%Y%m%dT%H%MZ).tar.gz /var/lib/frostread'
sudo systemctl unmask frostread.socket frostread.service
sudo systemctl start frostread.socket frostread.service
```

Update on an image:

```sh
sudo updex features update --component=readd
sudo systemctl restart frostread.socket frostread.service
sudo lodgectl reload
```

With Updex v2.1.3 and later
([ADR-0019](https://github.com/frostyard/updex/blob/main/docs/adr/0019-apply-merged-sysusers-and-tmpfiles-on-a-live-refresh.md)),
the update is a live refresh. The new files merge at once,
`systemd-sysusers` and `systemd-tmpfiles --create` run, and systemd
reloads its unit files. Updex does not restart units, so the running
readd keeps its old binary until you restart it. No reboot is necessary.
`lodgectl reload` makes lodge read the new manifest.

Reboot only when the update waits for the next boot: you passed
`--no-refresh`, the automatic update timer installed it, or the output
says `refresh skipped`. With an Updex older than v2.1.3
(`updex --version` shows it), run `sudo systemd-sysusers`,
`sudo systemd-tmpfiles --create` and
`sudo systemctl daemon-reload` first, or reboot.

Check the version:

```sh
sudo -u frostread readctl status
sudo lodgectl apps
```

`readctl status` prints `readd <version>`. `lodgectl apps` lists `read`
up at the new version. lodge polls every 15 seconds, so if it shows
`down` at once, run it again after 15 seconds.

Roll back:

```sh
sudo updex features rollback readd
```

The rollback takes effect at the next boot. Add `--now` to apply it at
once, then restart and reload as above. A rollback does not undo a
database migration. If the journal of the older readd
(`journalctl -u frostread.service -n 50`) shows
`database schema N is newer than this readd`, restore the state from
the backup. Replace `ARCHIVE` with its path:

```sh
sudo systemctl mask --now frostread.socket frostread.service
sudo rm -rf /var/lib/frostread
sudo tar -xzpf ARCHIVE -C / var/lib/frostread
sudo systemctl unmask frostread.socket frostread.service
sudo systemctl start frostread.socket frostread.service
```

The restore loses what changed after the backup: new items, reads and
saves.

## Release

A release is a tag, and `make bump` is the only way to make one
([core ADR-0012](https://github.com/frostyard/core/blob/main/docs/adr/0012-svu-versioning-and-rolling-dev-prerelease.md)).
It checks the pinned `svu`, that you are on `main` at `origin/main` with a
clean tree, and that `make check` passes, then tags `svu next` and pushes
the tag. The tag starts
[`.github/workflows/release.yml`](.github/workflows/release.yml): it builds
`frostyard-readd_X.Y.Z_amd64.deb` with `make deb`, attests its provenance, and
attaches it to the GitHub release.

`make deb` builds the same package on a developer's machine, after the web
bundle: `make web-build` (nsl), then `make deb`. It writes
`dist/frostyard-readd_0.0.0-<commit>-snapshot_amd64.deb`, and `make deb-check`
(`packaging/deb-check.py`) reads the archive's exact member names. It fails unless
the control fields are right (`Depends: systemd`, no maintainer scripts), and
the package holds only root-owned, non-writable directories and regular files (no links)
under `/usr`, with the binaries (mode 0755), the units, the sysusers.d entry and the web bundle's `index.html`, and a manifest whose `version` is the package's
version. `internal/packagecheck` feeds it bad packages, and each must fail.
CI builds a snapshot on every pull request (job `deb`).

Verify a downloaded package:

```sh
gh attestation verify frostyard-readd_X.Y.Z_amd64.deb --repo frostyard/frostread
```

Snosi pins each release's `.deb` in a reviewed checksum PR, then builds
and publishes the sysext `readd`
([snosi#1173](https://github.com/frostyard/snosi/pull/1173)). See
[After the sysexts](#after-the-sysexts).

## Docs

- [docs/README.md](docs/README.md): the index.
- [docs/design/overview.md](docs/design/overview.md): the parts and how
  they fit.
- [docs/specs/api.md](docs/specs/api.md): the API, `readctl`, the config
  and the state layout.
- [AGENTS.md](AGENTS.md): the rules for every agent that works here.
