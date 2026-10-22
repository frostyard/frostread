# 0007 — A daily mark-and-scan sweep removes orphaned blobs after a grace period

- **Status:** Accepted (Brian, 2026-10-22)
- **Date:** 2026-10-21

## Context

Read keeps each article's original body and each image it inlines as a
blob: one file under `STATE/blobs/sha256/`, named by its SHA-256
([ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md)).
The same bytes stored twice are one file, so one blob can serve many
items. Nothing removes a blob today, so the store only grows.

The facts a removal must respect:

- **Two columns name a blob.** `items.original_hash` names an item's body
  as sent, and `item_blobs.hash` names each image of an item's
  extraction. An original has no row in the `blobs` table; an image has
  one.
- **References go away by cascade.** Removing a source deletes its items,
  and their `item_blobs` rows, through SQLite's `ON DELETE CASCADE`. No Go
  code sees those rows go. A later retention rule, or a re-extraction,
  would be one more path.
- **A fetch writes the file before its reference.** An extraction calls
  `blobs.Store.Put` for the body and each image, and only then records
  them with `SetOriginal` and `SetExtraction`. A crash, a cancel or a
  failed extraction between the two leaves a file that no row names.
- **`Put` of an existing blob is a no-op today:** it finds the file and
  returns. So a blob that lost its last reference can be wanted again by
  an extraction that is running now.
- **A saved note holds no blob.** Save-as-note turns each image into a
  link to its original address, because notes cannot fetch an image Read
  holds ([ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)).
  The note's item keeps its own blobs while it exists.
- **Only `readd` owns the state directory.** `readctl` reaches the daemon
  through its private socket, as caller `local`, and never opens the
  files itself.
- The store is one person's reading: thousands to tens of thousands of
  files, not millions.

## Decision

**A mark-and-scan sweep in `readd` removes a blob only after no row has
named it for a grace period, 7 days by default.** It keeps no reference
count.

1. **Mark.** The sweep reads the set of hashes the database names:
   every non-empty `items.original_hash`, and every `item_blobs.hash`. An
   item with a saved note, a starred item and an archived item are items,
   so their blobs are in the set.
2. **Scan.** It lists the blob files under `blobs/sha256/`, and skips a
   `Put`'s temporary `.put-*` file.
3. **Record.** A new table, `blob_orphans (hash, since)`, records when
   the sweep first found each file outside the set. A file back in the
   set loses its row; a row whose file is gone is deleted.
4. **Remove.** A file goes only when two times are past the grace
   period: its `since`, and its modification time. `Put` of a blob that
   exists now sets the file's modification time to now. So a blob that a
   pending fetch has just written, or wants again, waits a whole grace
   period from that `Put`, and its reference lands long before.
5. **Check again under the lock.** `blobs.Store` gains a read-write
   lock. `Put` holds it shared while it finds, touches or publishes a
   file; a removal holds it alone. Under it, the removal reads the file's
   modification time again, and asks the database again whether a row
   names the hash. Either one keeps the file. So a `Put` and a removal of
   one blob never interleave, whichever comes first.
6. **Log each removal** with the hash, the size and the `since` time,
   and the sweep's totals at its end. A blob is bytes by hash: no title,
   no address and no article text reaches the log.

The removal deletes the file, then the hash's `blob_orphans` row, and
its `blobs` row unless an `item_blobs` row names the hash again by then:
a save may publish the same bytes anew right after the removal, and its
image needs that row. The check and the delete are one transaction. One
sweep runs at a time.

**A dry run removes nothing and writes nothing.** It logs each blob a
real sweep would remove now, "would remove", and returns the same totals.
It does not record a new orphan's `since`, so a dry run does not start
anyone's grace period.

**It runs on a schedule in `readd`, and on demand from `readctl`.**

- `readd` sweeps 10 minutes after it starts, then every 24 hours.
- `readctl sweep [-dry-run]` asks the daemon to sweep now, through a new
  method, `blobs.sweep`, that only the caller `local` may call. It prints
  the totals.
- The configuration's `blob_sweep` is `on` (the default), `dry-run`
  (the schedule only logs), or `off` (no schedule; `readctl sweep` still
  works). `blob_grace_days` is the grace period, 7 by default, from 1 to
  365.

## Consequences

- **Disk comes back** after a source goes: its blobs leave between 7 and
  8 days later, unless another item names them.
- **The sweep is correct by construction.** It derives what is in use
  from the database at the time it runs, so no delete path (a cascade, a
  later retention rule, a hand edit) has to remember a count. It also
  finds files no row ever named: an original or an image whose extraction
  failed after its `Put`.
- **A sweep costs a directory walk and one query,** once a day. At one
  person's scale that is milliseconds to seconds, and it runs beside the
  polls without blocking them; it holds the blob lock alone only for one
  removal at a time.
- **Restoring an old database brings back references to files the sweep
  may have removed** since. The grace period covers a restore inside it;
  an older restore needs the blobs from the same snapshot. The operational
  notes say so.
- **A new obligation:** any new place that names a blob must join the
  mark's query, or the sweep removes its blobs after the grace period.
  The sweep's test names every reference column, so a new one is a
  deliberate change.
- **`Put` writes a little more:** touching an existing file is one
  `utimes` call.
- **A schema change:** migration 2 adds `blob_orphans`.

## Alternatives considered

- **A reference count on `blobs`:** every path that adds or drops a
  reference must change the count in the same transaction. The cascades
  drop references where no Go code runs, so the count needs triggers;
  originals have no `blobs` row at all; and a count that drifts, after a
  crash between a `Put` and its row or a bug in one path, removes a blob
  in use or keeps one forever, and nothing finds out. A mark-and-scan is
  slower per run, and that does not matter at this scale.
- **Remove an item's blobs when the item goes:** blobs are shared
  between items, and a cascade has no hook in Go; it would also miss the
  files no row ever named.
- **A grace period by the file's modification time alone:** a file
  written months ago that lost its reference today would go at the next
  sweep, with no time to notice a mistake. `since` measures the time
  without a reference, which is what the grace period is for.
- **The sweep in `readctl`, on the files directly:** only `readd` owns
  the state directory and the lock that `Put` takes; a second process
  could remove a blob between a running `Put` and its row.
- **A systemd timer:** it would still have to ask `readd`, so it adds a
  unit and a second schedule to keep in step for nothing a goroutine does
  not do.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/api.md](../specs/api.md), the
  [roadmap, Phase 8](../plans/0001-roadmap.md#phase-8--the-blob-sweep-built-2026-10-21)
- Builds on: [ADR-0005](0005-articles-are-extracted-sanitized-and-kept-as-sent.md),
  [ADR-0004](0004-save-as-note-goes-through-notes-mcp-on-the-agent-socket.md)
