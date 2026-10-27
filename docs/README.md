# Documentation

Docs are split by the question they answer (frostyard/core ADR-0025):

| Directory | Question | Contents |
| --- | --- | --- |
| [adr/](adr/) | **Why** did we choose this? | Architecture Decision Records: immutable once accepted, superseded, never edited |
| [design/](design/) | **How** does it fit together? | Living documents describing the current architecture |
| [specs/](specs/) | **What exactly** is the contract? | Precise, testable interface definitions |
| [plans/](plans/) | **When/in what order** do we build? | Roadmaps and phase plans, updated as work lands |

frostread is an app behind
[lodge](https://github.com/frostyard/lodge), under frostyard/core's
[ADR-0057](https://github.com/frostyard/core/blob/main/docs/adr/0057-run-first-party-apps-behind-lodge.md).
The contract it follows is lodge's
[app contract](https://github.com/frostyard/lodge/blob/main/docs/specs/app-contract.md).

## Index

### Decisions (ADRs)

An ADR marked (Proposed) waits for the owner; the others are Accepted.

- [0001 — Record architecture decisions](adr/0001-record-architecture-decisions.md)
- [0002 — Agent-portable instruction surface](adr/0002-agent-portable-instruction-surface.md)
- [0003 — The fetcher reaches only public addresses](adr/0003-the-fetcher-reaches-only-public-addresses.md)
- [0004 — Save as a note goes through notes' MCP on the agent socket](adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)
- [0005 — Articles are extracted, sanitized, and kept as sent](adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
- [0006 — Save from another page through a confirm page a bookmarklet opens](adr/0006-save-from-another-page-through-a-confirm-page-a-bookmarklet-opens.md)
- [0007 — A daily mark-and-scan sweep removes orphaned blobs after a grace period](adr/0007-a-daily-mark-and-scan-sweep-removes-orphaned-blobs-after-a-grace-period.md)
- [0008 — The desktop Flatpak carries its own readd; lodge is a connection the user chooses](adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md) (Proposed)

### Design

- [Overview](design/overview.md): the daemon, the store, the fetcher,
  polling, extraction, the engine, the API, MCP, save-as-note, content, and
  operations

### Specs

- [The Read app's interface](specs/api.md): the JSON-RPC methods, the MCP
  tools and scope, the content routes, the web routes, `readctl`, the
  configuration, the state layout, and what the fetcher may do

### Plans

- [0001: Roadmap](plans/0001-roadmap.md): the scaffold and docs, the
  daemon, the API and MCP, save-as-note, the web app, the live check, save
  from the phone, and the blob sweep
- [0002: The standalone desktop Flatpak](plans/0002-the-standalone-desktop-flatpak.md):
  readd's desktop mode, the Tauri shell, the Flatpak, the lodge
  connection, Notes as an option, the move from lodge, and the
  clean-install gate

## Conventions

- **New docs start from their category's `TEMPLATE.md`** (in each
  directory).
- A new decision gets a new ADR with the next number. If it reverses an old
  one, mark the old one `Superseded by NNNN` rather than editing it.
- Design docs are updated in place to always reflect reality.
- Specs change only alongside the code that implements them. The founding
  spec says when it binds.
- Cross-links between categories are mandatory in both directions. See the
  documentation rules in [AGENTS.md](../AGENTS.md) (`CLAUDE.md` and
  `GEMINI.md` are symlinks to it, ADR-0002).
- Adding a doc means adding it to the index above.
