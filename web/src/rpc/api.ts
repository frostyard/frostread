// readd's API, typed by hand from docs/specs/api.md (JSON-RPC API), until
// the app adopts a generator. Every method here is a session method: the
// browser never calls notes.setToken, which is the private socket's.
import type { Transport } from "@frostyard/lodge-client/rpc";

/** PROTOCOL is the API major version readd speaks (rpc.hello). */
export const PROTOCOL = 1;

/** CLIENT names this build in rpc.hello, for readd's logs. */
export const CLIENT = "read-web";

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

/** Source is a subscribed feed (docs/specs/api.md, Types). The Go type's
 * omitzero fields are optional here: readd leaves a zero one out. */
export interface Source {
  id: number;
  url: string;
  /** The site's home, when the feed names it. */
  siteUrl?: string;
  title: string;
  unread: number;
  /** Consecutive failed polls; absent when it has not failed. */
  errorCount?: number;
  /** The last poll's error; absent when it did not fail. */
  lastError?: string;
  /** RFC 3339 UTC; absent before the first successful poll. */
  polledAt?: string;
  /** RFC 3339 UTC; absent when no poll is scheduled. */
  nextPollAt?: string;
}

/** ItemSummary is an item in a list or a search result. */
export interface ItemSummary {
  id: number;
  title: string;
  /** Its source's title; absent for a saved link. */
  sourceTitle?: string;
  /** Absent when the feed or the page names no author. */
  author?: string;
  /** RFC 3339 UTC. */
  published: string;
  /** Its summary as text; absent when there is none yet. */
  excerpt?: string;
  read: boolean;
  starred: boolean;
  archived: boolean;
  saved: boolean;
  /** The note save-as-note wrote; absent when it wrote none. */
  notePath?: string;
  /** Search results only: the match, its words between **. */
  snippet?: string;
}

/** ExtractState says whether the article's text is ready. */
export type ExtractState = "pending" | "done" | "failed";

/** ItemDetail is a summary plus what the reader shows. */
export interface ItemDetail extends ItemSummary {
  url: string;
  /** /content/item/<id>/<mac>, for lodge-client's contentURL; absent when
   * the daemon has no content route for it. */
  contentPath?: string;
  extractState: ExtractState;
  /** Why extraction failed; absent unless extractState is "failed". */
  extractError?: string;
  /** The SHA-256 of the body as sent; absent before the page is fetched. */
  originalHash?: string;
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

/** NoteState is where an item's note is (docs/specs/api.md, items.noteStatus). */
export type NoteState = "present" | "moved" | "missing" | "unreachable";

/** NoteStatus is what notes says about an item's note. */
export interface NoteStatus {
  state: NoteState;
  /** The note's path, when it is present or moved. */
  path?: string;
  /** The note's route in lodge, when it is present or moved. */
  route?: string;
  /** Why notes could not answer, when state is unreachable. */
  error?: string;
}

/** ItemsChanged is readd's notification: the views a change affects. */
export interface ItemsChanged {
  views: string[];
}

/** ItemRender is the article's page for the desktop's window, from
 * items.render (docs/specs/api.md, and ADR-0008). */
export interface ItemRender {
  /** The whole document, its CSP meta first in its head, its images inlined. */
  html: string;
  /** The images the answer's bound left out; their alt text stays. */
  omittedImages: number;
}

/** ErrorCode values of docs/specs/api.md, Errors. */
export const Code = {
  notFound: 1001,
  conflict: 1002,
  protocolMismatch: 1003,
  notReady: 1004,
  unavailable: 1005,
  tooLarge: 1006,
} as const;

/**
 * isTooLarge says whether an error is items.render's tooLarge: the article's
 * text alone is at or over the bound on an answer (ADR-0008). It reads the
 * error's code, which both lodge-client's RPCError and the mock's MockError
 * carry.
 */
export function isTooLarge(err: unknown): boolean {
  if (typeof err !== "object" || err === null) return false;
  const code = (err as { code?: unknown }).code;
  return typeof code === "number" && code === Code.tooLarge;
}

/** ReadAPI is readd's methods, typed. The subscription to items.changed is
 * watchChanges' (events.ts): it is made on each connection. */
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
  noteStatus(id: number): Promise<NoteStatus>;
  /** renderItem is the article's page for the desktop's frame, in a scheme. */
  renderItem(id: number, scheme?: "light" | "dark"): Promise<ItemRender>;
  /** saveLater saves a link to read later, with the page's title when there is one. */
  saveLater(url: string, title?: string): Promise<ItemSummary>;
}

/** readAPI returns the typed methods over a transport. */
export function readAPI(transport: Transport): ReadAPI {
  const call = <T>(method: string, params: unknown) => transport.call<T>(method, params);
  return {
    status: () => call<Status>("status", {}),
    counts: () => call<Counts>("counts", {}),
    listSources: () => call<{ sources: Source[] }>("sources.list", {}).then((r) => r.sources),
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
        (r) => r.items,
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
    noteStatus: (id) => call<NoteStatus>("items.noteStatus", { id }),
    renderItem: (id, scheme) => call<ItemRender>("items.render", scheme === undefined ? { id } : { id, scheme }),
    saveLater: (url, title) =>
      call<{ item: ItemSummary }>("later.save", title === undefined || title === "" ? { url } : { url, title }).then(
        (r) => r.item,
      ),
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
