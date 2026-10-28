# Plan 0002: the standalone desktop Flatpak

This plan makes `org.frostyard.Frostread` a Flatpak that works on its own:
the desktop shell, readd and readctl in one package, a local reader by
default, and lodge as a connection the user chooses
([ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)).
It is frostread's part of the suite's standalone Flatpak program, which
Odrade coordinates under core's
[ADR-0062](https://github.com/frostyard/core/blob/main/docs/adr/0062-make-desktop-suite-apps-standalone-flatpaks-with-opt-in-lodge.md)
and [plan 0010](https://github.com/frostyard/core/blob/main/docs/plans/0010-suite-packaging.md):
its step 4 (Read) and, for Read, step 6 (the clean-install gate). It
follows frostnotes'
[ADR-0031](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0031-the-desktop-app-ships-as-a-flatpak-with-its-own-notesd.md)
and frostmail's
[plan 0013](https://github.com/frostyard/frostmail/blob/main/docs/plans/0013-m6-the-standalone-desktop-flatpak.md).
The [roadmap](0001-roadmap.md)'s lodge app and its packaging are not in
this plan; nothing here changes them.

The engine exists. This plan adds a desktop mode to readd, a shell, the
Flatpak, the lodge connection, and the release gate. It adds no reader
features. Tests use generated feeds (`cmd/readdev`) and made-up data, and
no test reaches the network, as AGENTS.md requires.

Status (2026-10-28): **Phase 2 is in review:** Phase 1 is done (ADR-0008
records Brian's 3 answers of 2026-10-27 and is Accepted; core's plan 0010
Read row waits for Lucilla), and readd's desktop mode is built and in
review. Nothing of the shell, the Flatpak or the release is built.

## Who helps

- **Brian:** accepts ADR-0008; answers the open questions; adds the
  Flatpak jobs to the repository's required checks, if he wants them
  required; pushes the candidate and release tags; makes the GHCR package
  `frostread` public.
- **Lucilla** (package delivery): plan 0010's Read row in core; the staged
  catalogue that serves a candidate's digest (Phase 9); the promotion and
  the listing in `flatpak-index/apps.json` (Phase 11).
- **Duncan** (nsl and test machines): an nsl machine with Node, Rust and
  WebKitGTK for `web/src-tauri` (frostnotes' `frostnotes` machine, or a
  new one from its `dev/nsl/provision.sh`); a disposable Snow VM with real
  portals for Phases 5, 6, 8 and 10, with snapshots, and a route to
  lodge's development build.

## Phase 1 — Decide (planner, small)

- [ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
  and this plan, indexed in [docs/README.md](../README.md), with the
  pointers in [design/overview.md](../design/overview.md) and the
  [roadmap](0001-roadmap.md).
- Brian's answers to the open questions, recorded in ADR-0008 before it
  is accepted.
- On acceptance: AGENTS.md's invariants take the desktop's form of the
  content rule and of "readd serves Unix sockets only"; link-only changes
  to the Status lines of ADR-0004 and ADR-0005: "Accepted; amended in
  part by 0008".
- Core's plan 0010 gains Read's row and its checks (Lucilla).
- **Done when:** Brian accepts ADR-0008, and plan 0010's Read row is
  merged in core.

## Phase 2 — readd's desktop mode (planner, medium)

Go only. [specs/api.md](../specs/api.md) and `schema/rpc` first, then the
code, then `make contract-update`.

- `readd -desktop`: no lodge socket, no `/content/`, no MCP, no notes
  client; only the private socket.
- `internal/config`: in desktop mode, the state directory
  `$XDG_DATA_HOME/frostread`, the configuration
  `$XDG_CONFIG_HOME/frostread/frostread.json`, and the socket under
  `$XDG_RUNTIME_DIR/app/org.frostyard.Frostread/` when `FLATPAK_ID` says
  so; `READ_SOCKET` still wins; `readctl` finds the same socket. Unit
  tests for each.
- The `flock` on `<state>/readd.lock`; the mode check of `app.json` under
  it; `-log` with rotation past 1 MiB once the lock is held; `-build`; and
  the one log line for the notes fields that have no effect.
- `daemon.shutdown` (caller `local`), `status`'s `build`,
  `items.render {id, scheme}` (callers `local` and `session`) with the CSP
  `meta`, the 12 MiB bound on the encoded answer and the `tooLarge`
  error, and `items.exportMarkdown {id}`
  (caller `local`) in `internal/notes`.
- `internal/content` and `items.render` share one renderer; a golden test
  holds both.
- **Done when:** `make check` and `make race` pass, and tests show:
  `readd -desktop` with a temporary XDG tree serves `readctl status` on the
  derived socket and polls an `httptest` feed; a second readd on the same
  directory exits with "another readd uses"; with `app.json` naming
  lodge, readd exits 0 without opening the database; `daemon.shutdown`
  releases the lock; `items.render` of an item with 20 MiB of images
  answers with an encoded line under 12 MiB and the right
  `omittedImages`, also for text heavy with quotes and backslashes and
  for one image repeated many times; a page whose text alone passes the
  bound answers `tooLarge`; and no listener but the private socket is
  open.

## Phase 3 — The desktop shell (planner, large)

All UI and Rust work runs in the nsl machine.

- `web/src-tauri/`: Tauri v2, the identifier `org.frostyard.Frostread`,
  `bridge.rs` (lines to readd's socket), `paths.rs`, the CSP of ADR-0008,
  `on_navigation`, `on_new_window` with the opener plugin, and single
  instance.
- `web/src/desktop.tsx` and its HTML entry: the Tauri transport, the
  system's color scheme, in-window routes, links through the shell; the
  reader's `srcdoc` frame from `items.render`; OPML export through the
  shell (`SaveFile` in a Flatpak, Tauri's dialog elsewhere); no save page
  and no bookmarklet. The lodge entry does not change.
- `make` targets in the style of frostnotes': `app-check` (Biome, tsc,
  Vitest, `cargo test`) and `app-build`, in nsl. `make ci` gains them.
- A hostile corpus and an e2e suite in WebKitGTK (WebKitWebDriver, as
  frostmail's): the built app against `readd -desktop` built with the
  `devfetch` tag and `cmd/readdev`'s feeds.
- **Done when:** in the nsl machine, the built app lists readdev's feeds
  and shows an article with its inlined image; the hostile corpus runs no
  script and makes no network request from the frame; a click on an
  article's link reaches a stand-in opener on the app's `PATH`; and `make
  ci` passes.

## Phase 4 — The Flatpak in CI (planner, medium)

- `packaging/flatpak/org.frostyard.Frostread.json` with the `readd` and
  `frostread` modules, the 3 sources files, `make flatpak-sources`
  (frostnotes' `scripts/flatpak-sources.sh` and `tools/flatpakgo`), and
  the generators' hashed requirements.
- The install layout, the launcher (`--background`, `flatpak-spawn
  --latest-version`, the `SIGKILL`, the build check), and
  `frostread-request-background`.
- The desktop file, the metainfo ("Feeds and a reading list on your own
  computer", with no word that lodge is needed), the icons, and the
  `finish-args` of ADR-0008.
- `.github/workflows/flatpak.yml`: the sources job and the build-and-test
  job on pull requests and main, SHA-pinned with least privilege;
  `internal/workflowcheck` covers it.
- The smoke test in the build, without portals: `frostread --background`
  at the Flatpak's socket; a second readd exits; `readctl` imports an
  OPML file and lists its sources (no fetch: the build has no network);
  the launcher with a stand-in `flatpak-spawn` ends it, and readd lives
  on; with `app.json` naming lodge, `--background` exits 0; a readd whose
  `build` differs from the stand-in latest commit is replaced at the
  launcher's next run, and one that matches is kept; the log turns over.
- **Done when:** both jobs pass on a pull request, and the bundle they
  upload runs `flatpak run --command=readctl org.frostyard.Frostread
  --help`.

## Phase 5 — The local reader on a real desktop (planner, with Duncan)

- On Duncan's Snow VM, with a CI bundle and real portals: the start
  through `flatpak-spawn`, the Background request, the file input through
  the FileChooser portal, `SaveFile`, and links through OpenURI.
- Fixes the check finds, each with a test where one can run in CI.
- [design/overview.md](../design/overview.md) gains the desktop as built.
- **Done when,** on the VM: the window and readd run in 2 instances
  (`flatpak ps`), readd under `flatpak-portal`; a public feed added in the
  window polls and its article shows with images; readd keeps polling
  with the window closed; an OPML file imports from `~/Downloads` and
  exports to `~/Documents`; a link opens in the browser; and at the next
  login readd starts when the Background portal allowed it.

## Phase 6 — The lodge connection (planner, medium)

- `bridge.rs`'s remote mode: `wss://LODGE/app/read/api`, `Origin:
  https://LODGE`, rustls with the web's roots, and a ping every 30 s.
- The Server sheet: its texts, from local facts only; the ordered stop
  (write `app.json`, `daemon.shutdown`, take the lock, or write local back
  after 30 s); the same stop on every start in lodge mode, before the
  window connects; saving local starts readd through the launcher; the
  unreachable state, which never falls back to local.
- In lodge mode: save as note when lodge's `status` says it is ready, and
  "Open in Notes" in the browser.
- An e2e test in the nsl machine against lodge's development build (`make
  dev-lodge`): local, then lodge (readd stops, the local data stays, the
  window shows lodge's feeds), then local again (the same local sources
  return); the sheet's texts with lodge stopped; a readd that will not
  stop leaves the mode local, with the error; and a crash injected just
  after `app.json` is renamed to lodge leaves a local readd that the next
  start ends before it connects to lodge.
- **Done when:** the e2e test passes, and on the VM a switch to lodge
  leaves no readd in `flatpak ps`, and a login with the autostart still
  registered starts none.

## Phase 7 — Notes on the desktop (planner, small)

Brian chose ADR-0008's proposal for open question 3 (2026-10-27).

- Local mode: "Export as Markdown…" in the reader, through
  `items.exportMarkdown` and `SaveFile`; no save as note.
- Lodge mode: save as note and "Open in Notes", as Phase 6 built them.
- **Done when,** on the VM: in local mode an export lands where the user
  picked it, with the article's text and frontmatter, and no Notes is
  installed; in lodge mode a save writes a note in lodge's vault under
  `Reading/`.

## Phase 8 — Moving from lodge (planner, small)

Brian chose ADR-0008's proposal for open question 2 (2026-10-27).

- The steps in [design/overview.md](../design/overview.md): stay on lodge
  with the connection, or export OPML from Read in lodge and import it
  in the desktop app.
- **Done when,** on the VM, with lodge's development build holding
  generated sources, items, stars and saved links: the export and the
  import give the desktop the same sources, lodge's data is unchanged
  afterwards, and nothing moved before the user acted.

## Phase 9 — A release candidate (Brian and Lucilla, small)

The gate tests a candidate, and the release promotes that same digest, as
core ADR-0062 asks.

- `flatpak.yml` learns tags: `vX.Y.Z-rc.N` pushes and attests
  `ghcr.io/frostyard/frostread` with its version and `sha-` tags and does
  not move `latest`; a promotion step later copies a tested digest to
  `vX.Y.Z` and `latest` and builds nothing. The publish job checks out
  nothing and runs no repository code.
- `release.yml`, which builds lodge's `.deb` on `v*`, treats an `-rc.N`
  tag as a pre-release, or skips it; `make bump` learns the candidate
  tag.
- Lucilla names the staged catalogue that lists a candidate's digest.
- Brian pushes 2 candidate tags: a **baseline**, `vX.Y.Z-rc.1`, for the
  update check only, and the **release candidate**, `vX.Y.Z-rc.2`, which
  the gate tests and the release promotes. A fix found by the gate makes
  a new release candidate, and the gate runs again on it.
- **Done when:** the VM installs each candidate from the staged
  catalogue, and `flatpak info` shows its attested digest.

## Phase 10 — The clean-install gate (planner, with Duncan)

On fresh Snow VMs with real portals. Items 1 to 4, 6 and 7 run on a fresh
install of the **release candidate** of Phase 9; item 5 runs on a fresh
install of the **baseline**, updated to that same release candidate. Each
item is recorded below with both digests, the date, and the VM's
versions (GNOME, xdg-desktop-portal, Flatpak).

1. **Nothing else is there:** `flatpak list` shows no frostyard app;
   `command -v readd readctl lodge tailscale` finds nothing; no
   `frostread*` or `lodge*` unit; no `~/.var/app/org.frostyard.Frostread`.
2. **Install** from the staged catalogue; the app grid shows Frostread
   with its icon and summary.
3. **The core local work, with no terminal:** add 2 public feeds and 1
   page whose feed is discovered; read an article with images; mark read,
   star, archive; save a link to read later; search; import and export
   OPML; export an article as Markdown (if Phase 7 builds it). Nothing
   asks for lodge, Notes or an account.
4. **Restart and login:** close and reopen the window; readd keeps
   polling while it is closed. Log out and in: readd starts at login
   (Background allowed), and new items arrived. With the background
   permission off, readd starts with the first window only.
5. **Update without loss:** on the baseline, with feeds, stars and saved
   links, and a window open, `flatpak update` to the release candidate.
   The old window keeps working. Then open a new window from the app
   grid: the launcher replaces the old readd, `readctl status`'s `build`
   is the release candidate's commit, and the old window reconnects to
   it. The old readd is never started again; the database migrates;
   every source, item and state is there.
6. **Opt into lodge** (lodge's development build, reachable from the VM,
   with Read and Notes): read lodge's list, mark an item read and see it
   read in lodge's web app, save a link, and save as note.
7. **Disconnects and mode changes:** stop lodge in lodge mode: the window
   says so and shows no local data in its place. Switch to local and back
   to lodge: no data moves or disappears, no readd runs in lodge mode,
   and the sheet's texts match what happened.

- **Done when:** all 7 pass for one release candidate's digest.

### Record

No run yet.

## Phase 11 — Release (Brian and Lucilla, small)

- Brian runs the promotion: `vX.Y.Z` and `latest` move to the release
  candidate's digest that passed Phase 10, never the baseline's.
- Brian makes `ghcr.io/frostyard/frostread` public.
- Lucilla lists `org.frostyard.Frostread` in `flatpak-index/apps.json`.
- **Done when:** `flatpak install frostyard org.frostyard.Frostread` on a
  fresh VM installs the digest that passed Phase 10, and plan 0010's Read
  row records it.

## Later / ideas

- A setting for polling in the background, beside the portal's.
- lodge sync, if offline reading of lodge's list is wanted (its own ADR).
- A direct, scoped link to a desktop Frostnotes, after a frostnotes ADR.
- A share target or a browser extension that saves a page to the desktop
  app.
- Images past the budget shown on demand, one at a time.

## Open questions

Brian answered all 3 on 2026-10-27, each with ADR-0008's proposal.

1. **The lodge mode.** ADR-0008 proposes a connection: the desktop uses
   lodge's readd and holds no copy. A sync would keep a local copy and
   reconcile it, for offline reading, at the cost of a new protocol and 2
   fetchers per feed. Options: connection (proposed); sync; connection
   now and sync later.
   **Answer:** connection (Brian, 2026-10-27).
2. **Moving from lodge to local.** ADR-0008 proposes OPML only: the
   subscriptions move, and items, states and saved links stay on lodge.
   The other option is a full copy too: `readctl export` on lodge's host
   and `readctl import` into the Flatpak (database and blobs, with a new
   content key), as frostmail's `mailctl import`, which adds about one
   medium phase.
   **Answer:** OPML only (Brian, 2026-10-27).
3. **Notes on the standalone desktop.** ADR-0008 proposes "Export as
   Markdown…" to a file the user picks, and save as note only in lodge
   mode. Options: the export (proposed); no note feature in local mode;
   the export now and a scoped link to a desktop Frostnotes later (needs
   frostnotes work).
   **Answer:** the export (Brian, 2026-10-27).

## References

- Implements: [ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
- Context: [design/overview.md](../design/overview.md), [specs/api.md](../specs/api.md), [ADR-0003](../adr/0003-the-fetcher-reaches-only-public-addresses.md), [ADR-0004](../adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md), [ADR-0005](../adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md), [roadmap](0001-roadmap.md)
- Core: [ADR-0062](https://github.com/frostyard/core/blob/main/docs/adr/0062-make-desktop-suite-apps-standalone-flatpaks-with-opt-in-lodge.md), [plan 0010](https://github.com/frostyard/core/blob/main/docs/plans/0010-suite-packaging.md)
- Models: frostnotes [ADR-0031](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0031-the-desktop-app-ships-as-a-flatpak-with-its-own-notesd.md); frostmail [ADR-0046](https://github.com/frostyard/frostmail/blob/main/docs/adr/0046-the-desktop-flatpak-carries-its-own-maild.md) and [plan 0013](https://github.com/frostyard/frostmail/blob/main/docs/plans/0013-m6-the-standalone-desktop-flatpak.md)
- The remote: [frostyard/flatpak-index](https://github.com/frostyard/flatpak-index)
