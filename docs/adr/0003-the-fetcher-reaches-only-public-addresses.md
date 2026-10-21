# 0003 — The fetcher reaches only public addresses

- **Status:** Accepted (Brian, 2026-10-21)
- **Date:** 2026-10-21

## Context

A reader fetches arbitrary feeds, pages and images on the server. frostread
runs in the owner's homelab, on the same host as lodge, and that host is on
the owner's tailnet. Every URL a subscription or a saved link names is
therefore a server-side request forgery target: a feed item can point at
`http://127.0.0.1:...`, at a TrueNAS address, at a printer on the house
LAN, or at another tailnet node, and frostread would fetch it with its own
rights and show the answer in the reader.

Lodge's kit carries the guard for exactly this
([lodge ADR-0017](https://github.com/frostyard/lodge/blob/main/docs/adr/0017-the-kit-guards-outbound-fetches-against-inside-addresses.md)).
Frostmail's one-click unsubscribe copied the same check before the kit
had it. The owner decided on 2026-10-21: v1 never fetches homelab, private,
loopback, link-local or tailnet addresses.

## Decision

Every outbound request in frostread goes through `internal/fetch`, which is
built on lodge's `kit/netguard`:

- **The check is per address, after DNS, on every dial.** A connection to
  an inside address is refused with `netguard.ErrInside`, so a redirect to
  one, and a host that resolves afresh to one (DNS rebinding), are refused
  too.
- **The name is checked first**, with `netguard.Name`, before any DNS
  lookup: a name that is `localhost` or ends in `.local`, `.internal`,
  `.home.arpa` or `.ts.net`, and an IP literal inside, is refused.
- **No environment proxy.** The transport ignores `HTTP_PROXY` and
  `HTTPS_PROXY`, which would route the request somewhere the check cannot
  see.
- **Only `http` and `https`**, and no userinfo in the URL.
- **At most 5 redirects**, and each target's scheme and name are checked
  again in `CheckRedirect` before it is followed.
- **Size caps:** a feed 10 MiB, a page 10 MiB, one image 10 MiB, and 40
  MiB of images per item. A body over its cap is an error, never a silent
  truncation.
- **A total time cap of 30 seconds** for one request, redirects included.
- **Conditional GET:** the source's `ETag` and `Last-Modified` are sent,
  and a 304 is not an error.
- **A fixed `User-Agent`, `frostread/<version>`**, no cookies, no
  credentials, and no other headers the caller could set.

`internal/fetch` is the only package that makes an outbound request;
`go vet` cannot check that, so review does.

## Consequences

- A feed or a page on the homelab cannot be read in v1. A TrueNAS-hosted
  blog, a router's status feed, and a Caddy index on the LAN are all
  refused. That is the intended trade: the reader is for the public web.
- Tests prove the refusal. A test `httptest` server on `127.0.0.1` is
  refused, a URL with userinfo, `file:` or `ftp:` is refused, a
  `localhost` or `x.ts.net` name is refused, and the size cap and the
  conditional GET are covered. The positive tests inject a test-only
  transport, which dials the `httptest` server; the guard stays on in
  production, and the injection is unexported.
- Images inlined from an article come through the same path. An article
  with an image on a private host shows the image's alt text, not the
  image.
- A development escape exists for lodge's development build alone: built
  with the `devfetch` tag, the fetcher admits `127.0.0.1` for hosts named
  in `READ_DEV_ALLOW`. A release build has no such code
  ([specs/api.md, Development-only](../specs/api.md#development-only)).
- The owner's TrueNAS snapshots of the state directory are the backup. The
  guard does not affect that.

## Alternatives considered

- **An allow-list of hosts:** safe, but it turns subscribing to any new
  feed into an administration task. Rejected for v1.
- **A fetching proxy outside the tailnet:** moves the problem to another
  host and another secret; the homelab would still be reachable from that
  host. Rejected.
- **Checks by name only:** a resolvable name can point inside at any
  moment, and a redirect can change the host. Rejected.
- **No guard, with a warning in the UI:** the failure mode is disclosure of
  the homelab's own services to whoever can subscribe. Rejected.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md#fetching),
  [ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
- Builds on: [lodge ADR-0017](https://github.com/frostyard/lodge/blob/main/docs/adr/0017-the-kit-guards-outbound-fetches-against-inside-addresses.md),
  [lodge ADR-0014](https://github.com/frostyard/lodge/blob/main/docs/adr/0014-agents-on-lodges-host-use-a-local-agent-socket.md)
- Built in: [roadmap — Phase 2](../plans/0001-roadmap.md)
