# 0005 — Articles are extracted, sanitized, and kept as sent

- **Status:** Proposed
- **Date:** 2026-10-21

## Context

Every article frostread shows is untrusted HTML from the public web, and so
is every image in it. Lodge serves an app's untrusted bytes only from the
`lodge-content` node, under a sandbox policy whose content CSP allows
images from `data:` alone
([lodge ADR-0010](https://github.com/frostyard/lodge/blob/main/docs/adr/0010-untrusted-bytes-come-from-a-second-node.md)).
Frostmail already solves the same problem for mail: sanitize in the daemon,
serve from lodge-content, and load no remote resource, so the browser never
tells a sender that the owner read the message. Frostnotes keeps blob
contents by hash. The suite audit of 2026-10-21 recommended the two
patterns for the reader.

A reader must also keep what the source said. A feed item changes and its
page goes away; the owner's copy is the only record of what they read.

## Decision

Three steps, in the daemon, for every item:

1. **Extract.** `internal/extract` runs a readability port
   (`codeberg.org/readeck/go-readability/v2`) over the page, or uses the
   feed's own content when it is a full article. The result is a title, a
   byline, a site name, a date, HTML and text.
2. **Sanitize.** `internal/sanitize`, on `bluemonday`, keeps headings,
   paragraphs, lists, blockquotes, pre and code, tables, figures and
   captions, `img` with `src`, `alt`, `width` and `height`, and `a` with
   `href`. It drops styles, classes, ids, iframes, forms, `object`,
   `embed`, `<base>`, `<meta>` and SVG or MathML content. A link keeps
   only `http`, `https` and `mailto`, gains `target="_blank"` and
   `rel="noopener noreferrer"`, and an image keeps only `http` and
   `https`.
3. **Inline the images.** `internal/extract` fetches each `<img src>`
   through the guarded fetcher (at most 60 images, 40 MiB together), keeps
   only a sniffed `image/png`, `image/jpeg`, `image/gif`, `image/webp` or
   `image/avif` (never SVG), stores it in `internal/blobs`, and rewrites
   the `src` to the internal marker `blob:<hash>`. An image that cannot be
   fetched loses its `src` and keeps its alt text in a `<span>`.

The content handler renders the article as one HTML page: a small inline
style (system fonts, a 42 rem column, light and dark by
`prefers-color-scheme`, images at most the column's width), the title,
byline and the sanitized content, with every `blob:<hash>` replaced by a
`data:<mime>;base64,…` URI. Lodge's content CSP allows only `img-src
data:`, so nothing else could load. The page is served at
`https://CONTENT/read/item/<id>/<mac>` under a MAC, never a guessable id
([ADR-0003's sibling rule, lodge-http.md's content routes](https://github.com/frostyard/lodge/blob/main/docs/specs/lodge-http.md#content-routes)).

**The original is kept as sent.** The bytes the feed or the page returned
are hashed and written to the blob store when the item is fetched, and the
item records the hash (`original_hash`). The extraction stores its blobs
beside it. A re-extraction never needs the network for the original.

## Consequences

- The browser loads no remote resource for an article: no page's
  scripts, no tracker's pixel, no remote font. The reading of an article
  is invisible to its publisher after the fetch.
- Images cost disk. One item's images are capped at 40 MiB, and identical
  images across items share one blob.
- A page whose images are on a private host shows alt text, not the
  image, because the fetcher refuses inside addresses
  ([ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md)).
- An extraction can fail. The item then shows its feed text, its state is
  `failed`, and the error is stored for the owner to see. A saved link
  whose page cannot be extracted still exists, with its URL and title.
- Keeping the original costs one copy of each fetched body. It is what
  lets save-as-note write the article as the source sent it, and it is
  what makes a re-extraction possible after a fix
  ([ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
- The note frostread writes gets a link to the original's images, because
  notes cannot read frostread's blobs.

## Alternatives considered

- **Show the article in the app's frame, with lodge's `remoteImages`:**
  the article would load remote images with the app's policy, which tells
  each publisher when it is read, and would need a wider CSP. Rejected.
- **Sanitize in the browser:** the untrusted HTML would reach the trusted
  origin first, which the contract forbids. Rejected.
- **Keep only the extraction, not the original:** smaller, but the source's
  words are gone, and save-as-note has nothing faithful to write.
  Rejected.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md#content)
- Builds on: [ADR-0003](0003-the-fetcher-reaches-only-public-addresses.md),
  [lodge ADR-0010](https://github.com/frostyard/lodge/blob/main/docs/adr/0010-untrusted-bytes-come-from-a-second-node.md)
- Built in: [roadmap — Phase 2](../plans/0001-roadmap.md)
