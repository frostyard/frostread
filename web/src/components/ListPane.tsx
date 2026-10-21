// The list pane: the search field, "Save a link", "Mark all read", and the
// virtualized list of items with keyset paging (docs/specs/api.md,
// items.list). Search asks items.search 200 ms after the last key, and
// Escape clears it.
import { IconButton } from "@frostyard/lodge-ui/react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { BookmarkPlus, CheckCheck, PanelLeft } from "lucide-react";
import { type RefObject, useEffect, useRef, useState } from "react";
import type { Route } from "../lib/routes.js";
import { VIEW_TITLES, type View } from "../rpc/api.js";
import { useRead } from "./context.js";
import { ItemRow, ROW_HEIGHT } from "./ItemRow.js";
import { UrlSheet } from "./UrlSheet.js";

/** SEARCH_MS is how long the search field waits after a key. */
const SEARCH_MS = 200;

/** ListPane is the middle pane, or the narrow window's list. */
export function ListPane(props: { searchRef: RefObject<HTMLInputElement | null>; onViews?: () => void }) {
  const items = useRead((state) => state.items);
  const next = useRead((state) => state.next);
  const loading = useRead((state) => state.loading);
  const query = useRead((state) => state.query);
  const selectedId = useRead((state) => state.selectedId);
  const listRoute = useRead((state) => state.listRoute);
  const sources = useRead((state) => state.sources);
  const open = useRead((state) => state.open);
  const search = useRead((state) => state.search);
  const markAllRead = useRead((state) => state.markAllRead);
  const loadMore = useRead((state) => state.loadMore);
  const saveLater = useRead((state) => state.saveLater);
  const [text, setText] = useState(query);
  const [saving, setSaving] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scroller = useRef<HTMLDivElement>(null);

  // Escape, or a new view, clears the field with the query.
  useEffect(() => {
    if (query === "") setText("");
  }, [query]);
  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    [],
  );

  const onSearch = (value: string) => {
    setText(value);
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = setTimeout(() => void search(value), SEARCH_MS);
  };

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 8,
  });

  const onScroll = () => {
    const element = scroller.current;
    if (element === null || next === null) return;
    if (element.scrollHeight - element.scrollTop - element.clientHeight < 400) void loadMore();
  };

  return (
    <section aria-label="Items" className="flex h-full flex-col bg-window">
      <div className="flex items-center gap-1 border-b border-separator px-[var(--pad-x)] py-1.5">
        {props.onViews !== undefined && <IconButton label="Views" icon={PanelLeft} onClick={props.onViews} />}
        <input
          ref={props.searchRef}
          type="search"
          value={text}
          aria-label="Search items"
          placeholder="Search"
          onChange={(event) => onSearch(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Escape") return;
            event.stopPropagation();
            setText("");
            void search("");
          }}
          className="h-7 min-w-0 flex-1 rounded-md border border-separator bg-window px-2 text-sidebar-row outline-none focus-visible:border-accent"
        />
        <IconButton label="Save a link" icon={BookmarkPlus} onClick={() => setSaving(true)} />
        <IconButton label="Mark all read" icon={CheckCheck} onClick={() => void markAllRead()} />
      </div>

      {items.length === 0 ? (
        <p className="flex flex-1 items-center justify-center px-4 text-center text-empty text-tertiary">
          {emptyText(query, listRoute, sources)}
        </p>
      ) : (
        <div ref={scroller} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
          <div
            role="listbox"
            aria-label={listName(listRoute, sources)}
            className="relative w-full"
            style={{ height: virtualizer.getTotalSize() }}
          >
            {virtualizer.getVirtualItems().map((row) => {
              const item = items[row.index];
              if (item === undefined) return null;
              return (
                <div
                  key={item.id}
                  className="absolute inset-x-0 top-0"
                  style={{ height: row.size, transform: `translateY(${row.start}px)` }}
                >
                  <ItemRow item={item} selected={item.id === selectedId} onSelect={() => void open(item.id)} />
                </div>
              );
            })}
          </div>
        </div>
      )}

      {loading && items.length > 0 && (
        <p className="border-t border-separator px-[var(--pad-x)] py-1 text-sidebar-section text-tertiary">Loading…</p>
      )}

      {saving && (
        <UrlSheet
          title="Save a link"
          label="Address"
          placeholder="https://example.com/an-article"
          submitLabel="Save"
          onSubmit={saveLater}
          onClose={() => setSaving(false)}
        />
      )}
    </section>
  );
}

/** EMPTY_VIEW is each view's empty state (the brief's wording). */
const EMPTY_VIEW: Record<View, string> = {
  unread: "No unread items",
  later: "Nothing saved to read later",
  starred: "Nothing starred",
  archive: "Nothing archived",
  all: "No items",
};

/** emptyText is a view's empty state. */
function emptyText(query: string, route: Route, sources: { id: number; title: string }[]): string {
  if (query.trim() !== "") return `No matches for “${query.trim()}”`;
  switch (route.kind) {
    case "view":
      return EMPTY_VIEW[route.view];
    case "source": {
      const source = sources.find((row) => row.id === route.id);
      return source === undefined ? "No items in this source" : `No items in ${source.title}`;
    }
    case "item":
      return "No items";
  }
}

/** listName names the list for its listbox. */
function listName(route: Route, sources: { id: number; title: string }[]): string {
  if (route.kind === "view") return VIEW_TITLES[route.view];
  if (route.kind === "source") return sources.find((row) => row.id === route.id)?.title ?? "Source";
  return "Items";
}
