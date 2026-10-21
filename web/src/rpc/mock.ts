// A mock of readd's API for the tests, and for `vite dev` with no lodge
// (docs/specs/api.md, JSON-RPC API). It holds 3 sources, 30 items and 2
// saved links in memory, answers every session method, and sends
// items.changed after a write, as readd does.
import type { EventEnvelope, Transport } from "@frostyard/lodge-client/rpc";
import type { Counts, ItemDetail, ItemSummary, ItemsChanged, Source, View } from "./api.js";

const DAY = 86_400_000;
/** NOW is the clock the mock's dates count back from, so the tests are stable. */
const NOW = Date.parse("2026-10-20T12:00:00Z");

const SOURCE_TITLES = ["Frostyard Blog", "Lobsters", "The Verge"] as const;
const AUTHORED = [true, false, true] as const;

interface MockItem extends ItemDetail {
  sourceId: number;
  sourceTitle: string;
  author: string;
  excerpt: string;
  notePath: string;
  contentPath: string;
  extractError: string;
  originalHash: string;
}

/** MockSource holds the values the mock keeps; sourceOut drops the empty
 * ones, as the daemon's omitzero does. */
interface MockSource extends Source {
  siteUrl: string;
  errorCount: number;
  lastError: string;
  polledAt: string;
  nextPollAt: string;
}

/** sourceOut is a source as readd sends it: no zero field. */
function sourceOut(source: MockSource): Source {
  return {
    id: source.id,
    url: source.url,
    title: source.title,
    unread: source.unread,
    ...(source.siteUrl === "" ? {} : { siteUrl: source.siteUrl }),
    ...(source.errorCount === 0 ? {} : { errorCount: source.errorCount }),
    ...(source.lastError === "" ? {} : { lastError: source.lastError }),
    ...(source.polledAt === "" ? {} : { polledAt: source.polledAt }),
    ...(source.nextPollAt === "" ? {} : { nextPollAt: source.nextPollAt }),
  };
}

/** createMockTransport returns a Transport over the mock's data. */
export function createMockTransport(): Transport {
  const sources: MockSource[] = SOURCE_TITLES.map((title, i) => ({
    id: i + 1,
    url: `https://feed${i + 1}.example/feed.xml`,
    siteUrl: `https://site${i + 1}.example/`,
    title,
    unread: 0,
    errorCount: i === 2 ? 1 : 0,
    lastError: i === 2 ? "the feed answered 503" : "",
    polledAt: new Date(NOW - (i + 1) * 3_600_000).toISOString(),
    nextPollAt: new Date(NOW + 20 * 60_000).toISOString(),
  }));

  const items: MockItem[] = [];
  for (let id = 1; id <= 30; id++) {
    const source = sources[id % 3];
    if (source === undefined) continue;
    items.push(mockItem(id, source, AUTHORED[id % 3] === true));
  }
  // Two saved links: the owner's, with no source.
  for (const [id, title] of [
    [31, "Why feeds are still good"],
    [32, "A guide to slow reading"],
  ] as const) {
    items.push({
      ...mockItem(id, null, true),
      title,
      sourceTitle: "",
      saved: true,
      read: false,
      url: `https://read.example/${id}`,
    });
  }

  const events = new Set<(event: EventEnvelope) => void>();
  const closes = new Set<(reason: string) => void>();

  /** notify sends items.changed after the response, as readd does: the
   * kit's event envelope, its data a Changed. */
  const notify = (...views: string[]) =>
    queueMicrotask(() => {
      const changed: ItemsChanged = { views };
      for (const handler of events) handler({ event: "items.changed", data: changed });
    });

  const byId = (id: number) => items.find((item) => item.id === id);
  /** summary is a row as readd sends it: no zero field. */
  const summary = (item: MockItem): ItemSummary => ({
    id: item.id,
    title: item.title,
    published: item.published,
    read: item.read,
    starred: item.starred,
    archived: item.archived,
    saved: item.saved,
    ...(item.sourceTitle === "" ? {} : { sourceTitle: item.sourceTitle }),
    ...(item.author === "" ? {} : { author: item.author }),
    ...(item.excerpt === "" ? {} : { excerpt: item.excerpt }),
    ...(item.notePath === "" ? {} : { notePath: item.notePath }),
    ...(item.snippet === undefined ? {} : { snippet: item.snippet }),
  });
  /** detail is an item as items.get sends it: a summary, and no zero field. */
  const detail = (item: MockItem): ItemDetail => ({
    ...summary(item),
    url: item.url,
    extractState: item.extractState,
    ...(item.contentPath === "" ? {} : { contentPath: item.contentPath }),
    ...(item.extractError === "" ? {} : { extractError: item.extractError }),
    ...(item.originalHash === "" ? {} : { originalHash: item.originalHash }),
  });
  const inView = (item: MockItem, view: View): boolean => {
    switch (view) {
      case "unread":
        return !item.read && !item.archived;
      case "starred":
        return item.starred && !item.archived;
      case "later":
        return item.saved && !item.archived;
      case "archive":
        return item.archived;
      case "all":
        return true;
    }
  };
  const counts = (): Counts => {
    const views = { unread: 0, starred: 0, later: 0, archive: 0, all: 0 };
    for (const item of items) for (const view of Object.keys(views) as View[]) if (inView(item, view)) views[view]++;
    return {
      views,
      sources: sources.map((source) => ({
        sourceId: source.id,
        unread: items.filter((item) => item.sourceId === source.id && inView(item, "unread")).length,
      })),
    };
  };

  async function call<T>(method: string, raw: unknown): Promise<T> {
    const params = (raw ?? {}) as Record<string, unknown>;
    switch (method) {
      case "rpc.hello":
        return { protocol: 1, server: "readd mock", caller: "session" } as T;
      case "rpc.ping":
        return {} as T;
      case "events.subscribe":
        return { subscribed: true } as T;
      case "status":
        return {
          version: "mock",
          notes: { configured: true, vaultId: 1, folder: "Reading/" },
        } as T;
      case "counts":
        return counts() as T;
      case "sources.list":
        return {
          sources: [...sources].sort((a, b) => a.title.localeCompare(b.title)).map(sourceOut),
        } as T;
      case "sources.add": {
        const url = String(params.url ?? "");
        const existing = sources.find((source) => source.url === url);
        if (existing !== undefined) return { source: sourceOut(existing) } as T;
        const source: MockSource = {
          id: sources.length + 1,
          url,
          siteUrl: url,
          title: "New source",
          unread: 0,
          errorCount: 0,
          lastError: "",
          polledAt: new Date(NOW).toISOString(),
          nextPollAt: new Date(NOW + 20 * 60_000).toISOString(),
        };
        sources.push(source);
        notify("unread", "all");
        return { source: sourceOut(source) } as T;
      }
      case "sources.remove": {
        const id = Number(params.id);
        const at = sources.findIndex((source) => source.id === id);
        if (at >= 0) sources.splice(at, 1);
        for (let i = items.length - 1; i >= 0; i--) if (items[i]?.sourceId === id) items.splice(i, 1);
        notify("unread", "all");
        return {} as T;
      }
      case "sources.rename": {
        const id = Number(params.id);
        const source = sources.find((s) => s.id === id);
        if (source !== undefined) source.title = String(params.title ?? "");
        return {} as T;
      }
      case "sources.refresh":
        notify("unread", "all");
        return {} as T;
      case "opml.import": {
        const xml = String(params.xml ?? "");
        const urls = [...xml.matchAll(/xmlUrl="([^"]+)"/g)].map((m) => m[1] ?? "");
        const skipped = urls.filter((url) => sources.some((source) => source.url === url)).length;
        notify("unread", "all");
        return { added: urls.length - skipped, skipped } as T;
      }
      case "opml.export":
        return { xml: opml(sources.map(sourceOut)) } as T;
      case "items.list": {
        const view = (params.view ?? "all") as View;
        const sourceId = params.sourceId === undefined ? undefined : Number(params.sourceId);
        const limit = params.limit === undefined ? 50 : Number(params.limit);
        const all = items
          .filter((item) => inView(item, view) && (sourceId === undefined || item.sourceId === sourceId))
          .sort((a, b) => b.published.localeCompare(a.published));
        const start = params.cursor === undefined ? 0 : all.findIndex((item) => String(item.id) === params.cursor) + 1;
        const page = all.slice(start, start + limit);
        const next = start + limit < all.length ? String(page[page.length - 1]?.id ?? "") : undefined;
        return (next === undefined ? { items: page.map(summary) } : { items: page.map(summary), next }) as T;
      }
      case "items.get": {
        const item = byId(Number(params.id));
        if (item === undefined) throw new MockError(1001, "no such item");
        return detail(item) as T;
      }
      case "items.search": {
        const query = String(params.query ?? "").toLowerCase();
        const limit = params.limit === undefined ? 50 : Number(params.limit);
        const found = items
          .filter((item) => `${item.title} ${item.excerpt} ${item.author}`.toLowerCase().includes(query))
          .slice(0, limit)
          .map((item) => ({ ...summary(item), snippet: snippet(item.excerpt, query) }));
        return { items: found } as T;
      }
      case "items.setRead": {
        const ids = Array.isArray(params.ids) ? params.ids.map(Number) : [];
        const read = params.read === true;
        for (const id of ids) {
          const item = byId(id);
          if (item !== undefined) item.read = read;
        }
        notify("unread", "all");
        return {} as T;
      }
      case "items.markAllRead": {
        const view = (params.view ?? "all") as View;
        const sourceId = params.sourceId === undefined ? undefined : Number(params.sourceId);
        for (const item of items) {
          if (inView(item, view) && (sourceId === undefined || item.sourceId === sourceId)) item.read = true;
        }
        notify("unread", "all");
        return {} as T;
      }
      case "items.setStarred": {
        const item = byId(Number(params.id));
        if (item !== undefined) item.starred = params.starred === true;
        notify("starred", "all");
        return {} as T;
      }
      case "items.setArchived": {
        const item = byId(Number(params.id));
        if (item !== undefined) item.archived = params.archived === true;
        notify("archive", "unread", "starred", "all");
        return {} as T;
      }
      case "items.saveAsNote": {
        const item = byId(Number(params.id));
        if (item === undefined) throw new MockError(1001, "no such item");
        const file = `${item.title.replaceAll("/", "-")}.md`;
        item.notePath = `Reading/${file}`;
        notify("all");
        return { path: item.notePath, route: `/notes/1/Reading/${encodeURIComponent(file)}` } as T;
      }
      case "items.noteStatus": {
        const item = byId(Number(params.id));
        if (item === undefined) throw new MockError(1001, "no such item");
        if (item.notePath === "") return { state: "missing" } as T;
        const route = `/notes/1/${item.notePath
          .split("/")
          .map((segment) => encodeURIComponent(segment))
          .join("/")}`;
        return { state: "present", path: item.notePath, route } as T;
      }
      case "later.save": {
        const url = String(params.url ?? "");
        const existing = items.find((item) => item.saved && item.url === url);
        if (existing !== undefined) return { item: summary(existing) } as T;
        const source = sources[0];
        if (source === undefined) throw new MockError(1005, "no source");
        const item: MockItem = {
          ...mockItem(items.length + 1, null, true),
          title: hostOf(url),
          url,
          saved: true,
          read: false,
        };
        items.push(item);
        notify("later", "all");
        return { item: summary(item) } as T;
      }
      default:
        throw new MockError(-32601, `methodNotFound: ${method}`);
    }
  }

  return {
    call,
    onEvent(handler) {
      events.add(handler);
      return () => events.delete(handler);
    },
    onClose(handler) {
      closes.add(handler);
      return () => closes.delete(handler);
    },
  };
}

/** MockError is the mock's JSON-RPC error. */
export class MockError extends Error {
  readonly code: number;

  constructor(code: number, message: string) {
    super(message);
    this.name = "MockError";
    this.code = code;
  }
}

/** mockItem generates one item of a source, or a saved link when source is null. */
function mockItem(id: number, source: MockSource | null, authored: boolean): MockItem {
  const title = `Item ${id}: ${["Slow web", "RSS forever", "A quieter inbox", "Reading at night"][id % 4]}`;
  const excerpt =
    `The ${id}th piece about reading: feeds, saving things for later, and ` +
    "the small pleasure of a page that has nothing to sell.";
  const published = new Date(NOW - id * 5 * 3_600_000 - (id % 3) * DAY).toISOString();
  return {
    id,
    title,
    sourceTitle: source?.title ?? "",
    sourceId: source?.id ?? 0,
    author: authored ? `Author ${id}` : "",
    published,
    excerpt,
    read: id % 5 === 0,
    starred: id % 11 === 0,
    archived: id % 17 === 0,
    saved: source === null,
    notePath: "",
    url: `https://site${(id % 3) + 1}.example/articles/${id}`,
    contentPath: `/content/item/${id}/mock${id}mac`,
    extractState: id % 7 === 0 ? "failed" : id % 5 === 0 ? "pending" : "done",
    extractError: id % 7 === 0 ? "the page answered 403" : "",
    originalHash: `hash${id}`,
  };
}

/** snippet marks the query's first match in text the way the API does. */
function snippet(text: string, query: string): string {
  const at = text.toLowerCase().indexOf(query);
  if (query === "" || at < 0) return text;
  return `${text.slice(0, at)}**${text.slice(at, at + query.length)}**${text.slice(at + query.length)}`;
}

/** hostOf names a URL's host, for a saved link's title. */
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/** opml writes the sources as an OPML 2.0 document. */
function opml(sources: Source[]): string {
  const outlines = sources
    .map((source) => `    <outline type="rss" text="${source.title}" xmlUrl="${source.url}"/>`)
    .join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>\n<opml version="2.0">\n  <head><title>Read</title></head>\n  <body>\n${outlines}\n  </body>\n</opml>\n`;
}
