// readd's API, typed by hand from docs/specs/api.md (JSON-RPC API), until
// the app adopts a generator. Every method here is a session method: the
// browser never calls notes.setToken, which is the private socket's.
import type { Transport } from "./transport.js";

/** View is one of the sidebar's views (docs/specs/api.md, Views). */
export type View = "unread" | "starred" | "later" | "archive" | "all";

/** VIEWS lists the views in the sidebar's order. */
export const VIEWS: readonly View[] = ["unread", "starred", "later", "archive", "all"];

/** VIEW_TITLES names each view as the sidebar shows it. */
export const VIEW_TITLES: Record<View, string> = {
  unread: "Unread",
  starred: "Starred",
  later: "Read later",
  archive: "Archive",
  all: "All",
};

/** Source is a subscribed feed (docs/specs/api.md, Types). */
export interface Source {
  id: number;
  url: string;
  siteUrl: string;
  title: string;
  unread: number;
  errorCount: number;
  lastError: string;
  polledAt: string;
  nextPollAt: string;
}

/** ItemSummary is an item in a list or a search result. */
export interface ItemSummary {
  id: number;
  title: string;
  sourceTitle: string;
  author: string;
  published: string;
  excerpt: string;
  read: boolean;
  starred: boolean;
  archived: boolean;
  saved: boolean;
  notePath: string;
  /** Search results only: the match, its words between **. */
  snippet?: string;
}

/** ExtractState says whether the article's text is ready. */
export type ExtractState = "pending" | "done" | "failed";

/** ItemDetail is a summary plus what the reader shows. */
export interface ItemDetail extends ItemSummary {
  url: string;
  /** /content/item/<id>/<mac>, for lodge-client's contentURL. */
  contentPath: string;
  extractState: ExtractState;
  extractError: string;
  originalHash: string;
}

/** Status is the daemon's version and whether save-as-note is ready. */
export interface Status {
  version: string;
  notes: { configured: boolean; vaultId: number; folder: string };
}

/** Counts is the sidebar's counts. */
export interface Counts {
  views: Record<View, number>;
  sources: { sourceId: number; unread: number }[];
}

/** Page is one page of a list; next is the cursor of the following page. */
export interface Page {
  items: ItemSummary[];
  next?: string;
}

/** ListParams asks for one page of a view or a source. */
export interface ListParams {
  view: View;
  sourceId?: number;
  cursor?: string;
  limit?: number;
}

/** Note is what saveAsNote wrote, and the route that opens it in notes. */
export interface Note {
  path: string;
  route: string;
}

/** ImportResult is what opml.import added or skipped. */
export interface ImportResult {
  added: number;
  skipped: number;
}

/** ItemsChanged is readd's notification: the views a change affects. */
export interface ItemsChanged {
  views: string[];
}

/** ErrorCode values of docs/specs/api.md, Errors. */
export const Code = {
  notFound: 1001,
  conflict: 1002,
  protocolMismatch: 1003,
  notReady: 1004,
  unavailable: 1005,
} as const;

/** ReadAPI is readd's methods, typed. */
export interface ReadAPI {
  status(): Promise<Status>;
  counts(): Promise<Counts>;
  listSources(): Promise<Source[]>;
  addSource(url: string): Promise<Source>;
  removeSource(id: number): Promise<void>;
  renameSource(id: number, title: string): Promise<void>;
  refresh(id?: number): Promise<void>;
  importOPML(xml: string): Promise<ImportResult>;
  exportOPML(): Promise<string>;
  listItems(params: ListParams): Promise<Page>;
  getItem(id: number): Promise<ItemDetail>;
  search(query: string, limit?: number): Promise<ItemSummary[]>;
  setRead(ids: number[], read: boolean): Promise<void>;
  markAllRead(params: { view?: View; sourceId?: number; before?: string }): Promise<void>;
  setStarred(id: number, starred: boolean): Promise<void>;
  setArchived(id: number, archived: boolean): Promise<void>;
  saveAsNote(id: number): Promise<Note>;
  saveLater(url: string): Promise<ItemSummary>;
}

/** readAPI returns the typed methods over a transport. */
export function readAPI(transport: Transport): ReadAPI {
  const call = <T>(method: string, params: unknown) => transport.call<T>(method, params);
  return {
    status: () => call<Status>("status", {}),
    counts: () => call<Counts>("counts", {}),
    listSources: () => call<{ sources: Source[] }>("sources.list", {}).then((r) => r.sources ?? []),
    addSource: (url) => call<{ source: Source }>("sources.add", { url }).then((r) => r.source),
    removeSource: async (id) => {
      await call("sources.remove", { id });
    },
    renameSource: async (id, title) => {
      await call("sources.rename", { id, title });
    },
    refresh: async (id) => {
      await call("sources.refresh", id === undefined ? {} : { id });
    },
    importOPML: (xml) => call<ImportResult>("opml.import", { xml }),
    exportOPML: () => call<{ xml: string }>("opml.export", {}).then((r) => r.xml),
    listItems: (params) => call<Page>("items.list", listParams(params)),
    getItem: (id) => call<ItemDetail>("items.get", { id }),
    search: (query, limit) =>
      call<{ items: ItemSummary[] }>("items.search", limit === undefined ? { query } : { query, limit }).then(
        (r) => r.items ?? [],
      ),
    setRead: async (ids, read) => {
      await call("items.setRead", { ids, read });
    },
    markAllRead: async (params) => {
      const sent: Record<string, unknown> = {};
      if (params.view !== undefined) sent.view = params.view;
      if (params.sourceId !== undefined) sent.sourceId = params.sourceId;
      if (params.before !== undefined) sent.before = params.before;
      await call("items.markAllRead", sent);
    },
    setStarred: async (id, starred) => {
      await call("items.setStarred", { id, starred });
    },
    setArchived: async (id, archived) => {
      await call("items.setArchived", { id, archived });
    },
    saveAsNote: (id) => call<Note>("items.saveAsNote", { id }),
    saveLater: (url) => call<{ item: ItemSummary }>("later.save", { url }).then((r) => r.item),
  };
}

/** listParams drops the members a call does not set. */
function listParams(params: ListParams): Record<string, unknown> {
  const sent: Record<string, unknown> = { view: params.view };
  if (params.sourceId !== undefined) sent.sourceId = params.sourceId;
  if (params.cursor !== undefined) sent.cursor = params.cursor;
  if (params.limit !== undefined) sent.limit = params.limit;
  return sent;
}
