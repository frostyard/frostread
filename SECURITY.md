# Security policy

Read is a lodge app: the `readd` daemon on lodge's app socket, its web
bundle, which lodge serves, and `readctl` on the private socket. Agents
reach it at `/mcp/read` with a scoped token. It holds the notes token in
`/var/lib/frostread/tokens/notes` (mode 0600, in a 0700 directory), and it
reaches notes only through notes' MCP on lodge's agent socket
([ADR-0004](docs/adr/0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
Untrusted article HTML is sanitized in the daemon, and the browser loads no
remote resource
([ADR-0005](docs/adr/0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
Every outbound request goes through the fetcher's address guard, which
refuses homelab, private, loopback, link-local and tailnet addresses
([ADR-0003](docs/adr/0003-the-fetcher-reaches-only-public-addresses.md)).
Treat any report that touches the tokens, the token's scope, the fetcher's
guard, the sanitizer, the content URLs' secret, the sockets and their
admission, or the desktop shell's capabilities with care.

## Supported versions

Read is pre-release. Fixes go to `main` and ship in the next release. Only
the latest `vX.Y.Z` release gets fixes, and only its `frostyard-readd`
`.deb`, which lodge's host installs; snosi's sysext `readd` carries it. There
are no backports to older releases. The desktop Flatpak is not published
yet, so it has no supported version; a build from `main` is not a release.

## Reporting a vulnerability

Report it privately, through GitHub's private vulnerability reporting for
this repository: <https://github.com/frostyard/frostread/security>
("Report a vulnerability").

Do not open a public issue for a vulnerability. Never post tokens, host
names, tailnet names, feeds, articles or notes.

## What to include

- the version: `readctl status` prints `readd <version>`, and the
  manifest's `version` is in `lodgectl apps`;
- where it runs: an installed `.deb`, the sysext `readd`, a
  `make lodge-package` stage, or the desktop Flatpak;
- the impact, and what you expected instead;
- the steps to reproduce it, with made-up data (`alice@example.com`,
  `example.org`, a feed at `https://example.org/feed.xml`).
