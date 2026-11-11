# Spec: Problem records

readd and the desktop shell write problem records, their panics and a few
coded internal errors, to the local systemd journal, with frostyard/std's
Go package `problems` and its Rust crate `frostyard-problems`. frostread
sends nothing over a network for them: the record stays in the host's
journal, and only chairlift, when its user turns reporting on, is a sender
([core ADR-0069](https://github.com/frostyard/core/blob/main/docs/adr/0069-report-problems-local-first-opt-in-with-one-sender-per-host.md)).
This spec says which records frostread writes and when. The record's own
format, the `MESSAGE_ID`, the `FROSTYARD_*` fields and their patterns are
core's [problem report spec](https://github.com/frostyard/core/blob/main/docs/specs/problem-report.md);
the writer's behavior (the rate cap, the panic hook, the crash file) is
std's [problems package spec](https://github.com/frostyard/std/blob/main/docs/specs/problems-package.md).
Its consumers are the local readers of the journal (chairlift's Problems
page) and frostread's tests.

## Interface

| Field | Value |
| --- | --- |
| `FROSTYARD_APP_ID` | `org.frostyard.Frostread` |
| `FROSTYARD_COMPONENT` | `daemon` for readd (lodge's host and the Flatpak); `gui` for the Tauri shell |
| `FROSTYARD_RUNTIME` | `go` for readd; `rust` for the shell |
| `FROSTYARD_APP_VERSION` | the release version, `X.Y.Z` or `X.Y.Z-rc.N`, without the `v` |
| `FROSTYARD_CLASS` | `panic`, or `error` with 1 of the codes below |
| `MESSAGE` | the error's text, or the panic's value |

`readctl` records nothing.

### Codes (class `error`, readd)

| Code | Condition |
| --- | --- |
| `store.migrate_failed` | `store.Open` fails with an error that wraps `store.ErrMigrate`: any error of the schema's migration, a database newer than this readd included, while the start's context is not cancelled. |
| `lock.held` | Another process holds the state directory's lock (`EWOULDBLOCK` on `readd.lock`), on lodge's host only (readd without `-desktop`). |
| `extract.failed` | In an extraction, `Extractor.Inline` or `Extractor.Extract` returns an error that is not `extract.ErrNoArticle`, or the blob store cannot keep the page's original, while the extraction's context is not cancelled. |
| `render.too_large` | `items.render` answers `content.ErrTooLarge`: the article's page alone, with no image, is at or over the answer's bound. |
| `blobs.sweep_failed` | The scheduled blob sweep fails (its mark, scan or record), while readd's context is not cancelled. `readctl sweep` reports its own failure and records nothing. |

The `MESSAGE` of `extract.failed` and `render.too_large` names the item's
id before the error's text.

### Panics (class `panic`)

| Where | How |
| --- | --- |
| readd's `main` goroutine | `defer o.problems.RecoverPanic()`, the first defer of `main` |
| the poller and the blob sweep | `defer rec.RecoverPanic()`, the first statement of their `workers.Go` goroutines in `run` |
| each extraction worker, and each poll that `PollDue` or `Refresh` starts | `defer e.o.Problems.RecoverPanic()`, the first statement of each goroutine the engine starts (`engine.Options.Problems`) |
| the accept loops of lodge's socket and the private socket, and the badge's count | `defer rec.RecoverPanic()` in each goroutine `run` and `badgeCounter` start |
| each JSON-RPC request, on both sockets | `rpcserver.Options.Panics` wraps the router; the kit then recovers the panic again and answers internal, as before |
| the content route and MCP (lodge's host) | a handler that defers `rec.RecoverPanic()` around `ServeHTTP`; net/http then handles the panic as before |
| any other goroutine (such as the kit's connection goroutines outside a request), and a fatal error | `rec.CrashOutput()`: the Go runtime writes the crash to a file, and the next start of readd turns it into 1 record |
| the shell | `rec.install_panic_hook(env!("CARGO_MANIFEST_DIR"))` in `main.rs`, before `tauri::Builder`; the record's `FROSTYARD_LOCATION` is `src/<file>.rs:<line>` for a panic in the shell's own crate |

### The crash file

`main` gives `run` the options of `mainOptions`: the build's recorder and
`crashOutput`. readd calls `CrashOutput` only on an active recorder and
only after it holds the state directory's lock, so a build that is not a
release, `-build`, `-mode`, `-version` and a second readd make no crash
file.
Its folder is `$STATE_DIRECTORY/frostyard-problems/org.frostyard.Frostread`
on lodge's host (`/var/lib/frostread/...`), and
`$XDG_STATE_HOME/frostyard-problems/org.frostyard.Frostread` in the
Flatpak, after std's rules.

### Version stamping

```sh
readd -version          # prints readd's version, and exits
sh scripts/release-version.sh   # 1.2.3 at tag v1.2.3 or v1.2.3-rc.N; nothing on another commit
```

- The release `.deb` stamps readd with the tag's version: `release.yml`
  runs `make deb DEB_VERSION=X.Y.Z`. A snapshot `.deb`
  (`0.0.0-<commit>-snapshot`) records nothing. `make build` stamps
  `git describe`, such as `v0.5.0` at a tag; readd removes 1 leading `v`.
- The Flatpak's CI job writes `packaging/flatpak/release-version` with
  `scripts/release-version.sh` before the build. It prints `X.Y.Z` for both
  `vX.Y.Z` and `vX.Y.Z-rc.N`. The job first adds the workspace to git's
  global `safe.directory`, because it runs as root in a container on a
  workspace another user owns. The tag run also stamps the metainfo with
  `packaging/flatpak/stamp-metainfo.sh X.Y.Z`. The `readd` module builds
  with `-X main.version=` that file's version, or `dev` when it is empty or
  absent. The `frostread` module runs `packaging/flatpak/tauri-build.sh`,
  which reads `/app/bin/readd -version` and, for a release version, builds
  the shell with `pnpm tauri build --config '{"version":"X.Y.Z"}'`.

## Rules

- frostread opens no network connection for a problem record; a record is
  1 datagram to the local journal socket.
- A version that is not a release semver (`dev`, `flatpak`,
  `v0.4.0-3-gabc1234`, `0.0.0-abc1234-snapshot`, and the shell's unstamped
  `0.0.0`) makes the recorder inert: it writes nothing. readd logs 1 debug
  line with the reason.
- A network fault records nothing: a fetch error, an HTTP status of 400 or
  more, a refused address, a feed that fails its poll.
- An authentication fault records nothing: a bad token, a refused scope.
- A data fault records nothing: a page with no article, an item with no
  address, a bad OPML file, a bad request.
- `lock.held` is not recorded in `-desktop` mode: the Flatpak's launcher
  starts a second readd on purpose, and expects "another readd uses".
- No `MESSAGE` holds an article's text, a token or a note's text.
- The std package caps a process at 10 records in 60 seconds; the test
  packages that read records keep under that cap.

## References

- Rationale: [core ADR-0069](https://github.com/frostyard/core/blob/main/docs/adr/0069-report-problems-local-first-opt-in-with-one-sender-per-host.md)
- Record format: [core problem-report.md](https://github.com/frostyard/core/blob/main/docs/specs/problem-report.md)
- Writer: [std problems-package.md](https://github.com/frostyard/std/blob/main/docs/specs/problems-package.md)
- Context: [design/overview.md](../design/overview.md#problem-records)
- The desktop: [ADR-0008](../adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
