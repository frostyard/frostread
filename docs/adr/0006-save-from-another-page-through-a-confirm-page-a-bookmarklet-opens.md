# 0006 — Save from another page through a confirm page a bookmarklet opens

- **Status:** Proposed
- **Date:** 2026-10-21

## Context

Brian wants to save a page he is reading on his phone to Read's read-later
list, with two taps and no copy and paste. The page is in iOS Safari, and
Read runs behind lodge on the tailnet. On 2026-10-21 he chose a
bookmarklet over an iOS Shortcut.

The browser gives a page opened from another site little room:

- A share target would receive the page's address. It needs a web app
  manifest and a service worker, and lodge refuses service workers
  ([lodge ADR-0008](https://github.com/frostyard/lodge/blob/main/docs/adr/0008-lodge-installs-as-one-home-screen-app.md)).
- Lodge's browser checks refuse a cross-site request to an app route or to
  `/app/<id>/api`: `Origin` must be lodge's and `Sec-Fetch-Site` must be
  `same-origin` or `none`
  ([lodge-http.md](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#browser-checks)).
  A `fetch` from the visited page would therefore be refused, and CORS
  would refuse its answer too.
- One exception passes from any site: a top-level navigation to a shell
  page (`GET`, `Sec-Fetch-Mode: navigate`, `Sec-Fetch-Dest: document`).
  The shell then opens the app's frame at that route, its query included
  ([shell.md](https://github.com/frostyard/lodge/blob/main/docs/specs/shell.md#addresses-and-history)).

So the only door a page can open by itself is a navigation to a page Read
serves in the shell. That page can ask the daemon to save the link,
because it runs in lodge's origin with the owner's session.

## Decision

- **A bookmarklet navigates the top-level window** to
  `https://LODGE/read/save?url=<encoded>&title=<encoded>`, with the page's
  address and its title when it has one. It is one line of JavaScript,
  holds no token, and never calls an API. The web app builds it from the
  frame's own origin, so no host is written into the bundle.
- **The save page confirms.** `/save` shows the page's title and address
  as text, never as HTML, with the address's host. It saves only when the
  owner taps Save, through `later.save` on the app's own lodge session.
  Cancel, and Escape, return to the unread view.
- **The daemon checks the address.** `SaveURL` calls `fetch.Check`
  ([ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md)) before
  it stores the link, so a `javascript:` URL, a `file:` URL and an address
  inside the owner's network are refused, whoever sent them.
- **The title is other people's words, so it is data.** It is cleaned
  (control characters go, each run of white space, a line break included,
  becomes one space, it is trimmed) and cut to 300 runes. An empty title becomes the address's host
  name, as before.
- **`later.save` gains an optional `title`.** Its other callers, MCP's
  `save_url` and `readctl save`, pass no title.
- **`/bookmarklet` hands the bookmarklet out**: the link to drag, its code
  to copy, and the install steps for iOS Safari and for a desktop browser.
  The sidebar opens it.

## Consequences

What gets easier:

- The owner saves a page on his phone with two taps, from any site, with no
  token on the phone and nothing new to sign in to.
- The visited page never talks to lodge or to Read: it only navigates.
- Every save passes the same URL guard as a feed, so the bookmarklet
  cannot widen what the daemon fetches.

What gets harder, or is a cost we accept:

- The save takes two taps: the bookmarklet opens the page, and Save
  confirms. This is the price of admitting a navigation from any site.
- The tab leaves the page it was on. The browser's Back returns to it, and
  its state may be as the site left it, or not.
- The route carries the address and the title, so a very long one can
  reach the shell's limit of 2,048 characters
  ([lodge-client's `MAX_ROUTE`](https://github.com/frostyard/lodge/blob/main/client/src/messages.ts)).
  The bookmarklet leaves the title out when the query would pass 1,900
  characters, and so the save keeps the address and loses the title. A
  route over 2,048 characters is not reported to the shell at all; the
  shell keeps the address it opened.
- Any site can navigate the browser to the save page. The confirm is
  therefore required and must stay: without it, any page could plant an
  item in the owner's list.
- A page's title is attacker-controlled text. It is shown as text and
  stored as data, and it reaches notes' frontmatter only through the
  existing save-as-note path.

## Alternatives considered

- **An iOS Shortcut with a token:** it works from the share sheet, but it
  needs a token on the phone that can write to Read, a Shortcut to install,
  and a rotation story when the token leaks. The bookmarklet holds no
  secret, and the owner's lodge session is the authority.
- **A share target:** it needs a web app manifest and a service worker,
  which lodge refuses ([lodge ADR-0008](https://github.com/frostyard/lodge/blob/main/docs/adr/0008-lodge-installs-as-one-home-screen-app.md)).
- **A bookmarklet that calls lodge's API with `fetch`:** lodge's browser
  checks refuse a cross-site request (`Origin`, `Sec-Fetch-Site`), CORS
  would refuse the answer, and it would still need a token in the code.
- **Saving on load, with no confirm:** simplest for the owner, but any page
  could then add items to the list by navigating to the route, so it is
  refused.
- **A pop-up window:** iOS Safari may block a window a bookmarklet opens, so
  the bookmarklet navigates the current tab instead.

## References

- Shapes: [specs/api.md](../specs/api.md#web-routes) (the routes, the
  bookmarklet's code, and `later.save`'s `title`),
  [design/overview.md](../design/overview.md#design) (the web bundle),
  [roadmap — Phase 7](../plans/0001-roadmap.md#phase-7--save-from-the-phone-in-progress-2026-10-21)
- Builds on: [ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md)
  (the fetcher reaches only public addresses), and
  [ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md)
  (untrusted text stays data, and is shown as text)
- Follows lodge's [lodge-http.md](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#browser-checks)
  and [shell.md](https://github.com/frostyard/lodge/blob/main/docs/specs/shell.md#addresses-and-history),
  and its [ADR-0008](https://github.com/frostyard/lodge/blob/main/docs/adr/0008-lodge-installs-as-one-home-screen-app.md)
