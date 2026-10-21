# 0004 — Save as a note goes through notes' MCP on the agent socket

- **Status:** Accepted (Brian, 2026-10-21)
- **Date:** 2026-10-21

## Context

The owner wants a reader button that saves an article as a note
(2026-10-21). Notes owns the vault: frostread must not write a vault's
files, and must not hold a vault path. The suite audit of 2026-10-21
recommended going through Porter, which already holds a scoped notes token.
The owner asked for a direct path if the reader needs one: waiting for a
chat turn to save an article is a poor fit for a button.

Lodge's contract already allows it. The agent socket
([lodge ADR-0014](https://github.com/frostyard/lodge/blob/main/docs/adr/0014-agents-on-lodges-host-use-a-local-agent-socket.md))
admits any system user that lodge's configuration names as an agent, and
notes' engine enforces the token's scope
([frostnotes ADR-0028](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0028-an-agents-scope-can-limit-writes-to-folders.md)).
Porter is the precedent: an app on the agent socket. Frostnotes'
[rooted link rule](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0029-inside-lodge-a-rooted-link-no-file-answers-opens-through-lodge.md)
lets the note open the article through lodge.

## Decision

frostread is an agent of notes on lodge's agent socket, with its own token
scoped to one folder.

- **The owner issues the token:**
  `lodgectl token issue frostread-notes --app notes --scope 'vault=<id>;write=Reading/' --agent frostread`.
- **`readctl notes-token` reads it from stdin** and writes it to
  `STATE/tokens/notes`, mode 0600, as Porter's `token set` does.
- **`internal/notes` calls notes' MCP tool `create_note`** with an MCP
  client session, through `lodgeapp.AgentClient(token)` at
  `lodgeapp.AgentURL("notes")`, as Porter's tools do.
- **The note's path is `Reading/<YYYY-MM-DD> <title>.md`**, with the title
  made safe and the folder from the configuration. When `create_note`
  says the path exists, the next free suffix is tried, ` 2` to ` 9`.
- **The note opens with `# <title>`**, a headed frontmatter block
  (`source`, `saved`, `author`, `site`), and a line
  `[Open in Read](/read/item/<id>)`, or
  `<lodge_url>/read/item/<id>` when the configuration has `lodge_url`.
- **The item records `note_path`**, so the reader shows the button as done.
- **The token is optional.** Without it, `saveAsNote` answers
  `unavailable` and the button is disabled. Nothing else in the app needs
  it.

This needs no change to lodge's contract or to lodge's code. The agent
socket admits any user lodge names in `agents`, notes' engine enforces the
folder scope, and the kit carries the client.

## Consequences

- The note is attributed to frostread's token in notes' history, so the
  owner can see where it came from and revert it.
- The reader can write only under `Reading/`. A mis-set token fails in
  notes, not in frostread, which reports notes' error.
- The owner can revoke it in lodge with `lodgectl token revoke`, and the
  reader loses only save-as-note.
- frostread's system user joins group `lodge-agents`, and lodge's
  configuration must name `frostread` in `agents`. The packaging comments
  say why.
- Images cannot be copied into the note, because notes cannot fetch them
  and frostread does not hold the vault's files. Each image becomes a link
  to its original URL, `[image: alt](url)`. That URL is the article's, so
  the note keeps a reference the reader cannot inline.
- Porter could have done it, but a chat round trip for a button is slower
  and needs the model. Rejected for the button, kept for conversation.

## Alternatives considered

- **(a) Through Porter:** no new token and no new agent, but saving a
  button's article becomes a model turn, with its latency and cost, and the
  note is attributed to Porter. Rejected for the button.
- **(c) The browser opens notes with the text in a URL:** the text is
  untrusted and long, the URL exposes it in history, and notes has no route
  to receive it. Rejected.
- **(d) Reading and writing notes' files directly:** breaks "each app runs
  as its own user and cannot read another app's state" (lodge's contract).
  Rejected.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md#save-as-a-note)
- Builds on: [ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md),
  [lodge ADR-0014](https://github.com/frostyard/lodge/blob/main/docs/adr/0014-agents-on-lodges-host-use-a-local-agent-socket.md),
  [frostnotes ADR-0028](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0028-an-agents-scope-can-limit-writes-to-folders.md),
  [frostnotes ADR-0029](https://github.com/frostyard/frostnotes/blob/main/docs/adr/0029-inside-lodge-a-rooted-link-no-file-answers-opens-through-lodge.md)
- Built in: [roadmap — Phase 4](../plans/0001-roadmap.md)
