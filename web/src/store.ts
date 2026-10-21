// The app's state, as one zustand store (createReadStore): the list, the
// open item, the counts, the query, and every action the panes and the keys
// ask for. It holds no DOM, so the tests drive it directly.
import { createStore, type StoreApi } from "zustand/vanilla";
import { type Route, noteRoute as routeOfNote, UNREAD } from "./lib/routes.js";
import type { Counts, ItemDetail, ItemSummary, NoteStatus, ReadAPI, Source, Status, View } from "./rpc/api.js";

/** EMPTY_COUNTS is the counts before the first answer. */
const EMPTY_COUNTS: Counts = { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] };

export interface ReadState {
  api: ReadAPI | null;
  status: Status | null;
  /** error is the last failure, which a Sheet shows. */
  error: string | null;
  /** route is what the frame shows; listRoute is the view or source behind it. */
  route: Route;
  listRoute: Route;
  counts: Counts;
  sources: Source[];
  items: ItemSummary[];
  /** next is the cursor of the following page, or null. */
  next: string | null;
  loading: boolean;
  query: string;
  selectedId: number | null;
  item: ItemDetail | null;
  itemLoading: boolean;
  /** noteRoutes are the routes saveAsNote answered in this session. */
  noteRoutes: Record<number, string>;
  /** noteStates are what notes last said about each item's note. */
  noteStates: Record<number, NoteStatus>;
  /** scheme is the shell's theme, which the article's frame follows. */
  scheme: "light" | "dark";
  /** markAllPrompt asks before many items change at once; null does not. */
  markAllPrompt: string | null;
  busy: boolean;
}

export interface ReadActions {
  connect(api: ReadAPI): Promise<void>;
  setRoute(route: Route): Promise<void>;
  open(id: number): Promise<void>;
  closeItem(): void;
  load(): Promise<void>;
  loadMore(): Promise<void>;
  search(query: string): Promise<void>;
  selectOffset(delta: number): Promise<void>;
  markRead(id: number, read: boolean): Promise<void>;
  toggleRead(): Promise<void>;
  toggleStar(): Promise<void>;
  archive(): Promise<void>;
  markAllRead(): Promise<void>;
  askMarkAll(): Promise<void>;
  confirmMarkAll(): Promise<void>;
  cancelMarkAll(): void;
  refresh(id?: number): Promise<void>;
  addSource(url: string): Promise<void>;
  renameSource(id: number, title: string): Promise<void>;
  removeSource(id: number): Promise<void>;
  importOPML(xml: string): Promise<number>;
  exportOPML(): Promise<string>;
  saveLater(url: string): Promise<void>;
  saveAsNote(id: number): Promise<void>;
  /** checkNote asks notes where an item's note is, and keeps note_path
   * true: a missing note clears it, a moved one records its path, and an
   * unreachable notes keeps it. It answers the status, or null. */
  checkNote(id: number): Promise<NoteStatus | null>;
  noteRoute(id: number): string | null;
  setScheme(scheme: "light" | "dark"): void;
  changed(): Promise<void>;
  clearError(): void;
  loadStatus(): Promise<void>;
  loadCounts(): Promise<void>;
  loadSources(): Promise<void>;
}

export type ReadStore = ReadState & ReadActions;

/** MARK_ALL_CONFIRM is how many items a mark-all may change without asking. */
export const MARK_ALL_CONFIRM = 20;

/**
 * unreadToMark is how many items a mark-all would mark read in a route, or
 * null when the counts the app has do not say (a starred, later or archive
 * view). The server marks every unread item in the view or the source.
 */
export function unreadToMark(route: Route, counts: Counts): number | null {
  switch (route.kind) {
    case "source":
      return counts.sources.find((row) => row.sourceId === route.id)?.unread ?? 0;
    case "view":
      switch (route.view) {
        case "unread":
        case "all":
          return counts.views.unread;
        default:
          return null;
      }
    case "item":
      return counts.views.unread;
  }
}

/** markAllPromptFor is the question a mark-all asks first, or null. */
export function markAllPromptFor(route: Route, counts: Counts): string | null {
  const n = unreadToMark(route, counts);
  if (n !== null && n <= MARK_ALL_CONFIRM) return null;
  if (n === null) return "Mark every unread item in this view as read?";
  return `Mark ${n} items as read?`;
}

/** message is an error as the UI shows it. */
function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** createReadStore returns a store with no connection; connect() starts it. */
export function createReadStore(): StoreApi<ReadStore> {
  return createStore<ReadStore>()((set, get) => ({
    api: null,
    status: null,
    error: null,
    route: UNREAD,
    listRoute: UNREAD,
    counts: EMPTY_COUNTS,
    sources: [],
    items: [],
    next: null,
    loading: false,
    query: "",
    selectedId: null,
    item: null,
    itemLoading: false,
    noteRoutes: {},
    noteStates: {},
    scheme: "light",
    markAllPrompt: null,
    busy: false,

    async connect(api) {
      // Live updates are main.tsx's watchChanges: it subscribes to
      // items.changed now, and again after each reconnect.
      set({ api });
      await Promise.all([get().loadStatus(), get().loadCounts(), get().loadSources(), get().load()]);
    },

    async setRoute(route) {
      if (route.kind === "item") {
        // A permalink may name an item outside the current list: the All
        // list is what stands behind it, whatever view was showing.
        const all: Route = { kind: "view", view: "all" };
        set({ route, listRoute: all, selectedId: route.id, item: null, query: "", error: null });
        await get().load();
        await get().open(route.id);
        return;
      }
      set({ route, listRoute: route, selectedId: null, item: null, query: "" });
      await get().load();
    },

    async open(id) {
      const { api } = get();
      if (api === null) return;
      set({ selectedId: id, route: { kind: "item", id }, itemLoading: true, error: null });
      try {
        const detail = await api.getItem(id);
        set({ item: detail, itemLoading: false });
        // A note the item names may have moved or gone since it was saved.
        if (detail.notePath !== undefined && detail.notePath !== "") void get().checkNote(id);
        // Opening an item marks it read (docs/specs/api.md, and the brief).
        if (!detail.read) await get().markRead(id, true);
      } catch (err) {
        set({ itemLoading: false, error: message(err) });
      }
    },

    closeItem() {
      set({ route: get().listRoute, selectedId: null, item: null, itemLoading: false });
    },

    async load() {
      const { api, listRoute, query } = get();
      if (api === null || query !== "") return;
      set({ loading: true });
      try {
        const page = await api.listItems(listParams(listRoute));
        set({ items: page.items, next: page.next ?? null, loading: false, error: null });
      } catch (err) {
        set({ loading: false, error: message(err) });
      }
    },

    async loadMore() {
      const { api, listRoute, next, loading } = get();
      if (api === null || next === null || loading) return;
      set({ loading: true });
      try {
        const page = await api.listItems({ ...listParams(listRoute), cursor: next });
        set((state) => ({ items: [...state.items, ...page.items], next: page.next ?? null, loading: false }));
      } catch (err) {
        set({ loading: false, error: message(err) });
      }
    },

    async search(query) {
      const { api } = get();
      set({ query });
      if (api === null) return;
      const trimmed = query.trim();
      if (trimmed === "") {
        await get().load();
        return;
      }
      set({ loading: true });
      try {
        const items = await api.search(trimmed);
        set({ items, next: null, loading: false, error: null });
      } catch (err) {
        set({ loading: false, error: message(err) });
      }
    },

    async selectOffset(delta) {
      const { items, selectedId } = get();
      if (items.length === 0) return;
      const at = items.findIndex((item) => item.id === selectedId);
      const target = at < 0 ? (delta > 0 ? 0 : items.length - 1) : Math.min(Math.max(at + delta, 0), items.length - 1);
      const item = items[target];
      if (item === undefined) return;
      await get().open(item.id);
      if (get().next !== null && target >= items.length - 3) void get().loadMore();
    },

    async markRead(id, read) {
      const { api } = get();
      if (api === null) return;
      set((state) => ({
        item: state.item !== null && state.item.id === id ? { ...state.item, read } : state.item,
        items: state.items.map((item) => (item.id === id ? { ...item, read } : item)),
      }));
      try {
        await api.setRead([id], read);
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async toggleRead() {
      const { item } = get();
      if (item === null) return;
      await get().markRead(item.id, !item.read);
    },

    async toggleStar() {
      const { api, item } = get();
      if (api === null || item === null) return;
      const starred = !item.starred;
      set((state) => ({
        item: state.item === null ? null : { ...state.item, starred },
        items: state.items.map((row) => (row.id === item.id ? { ...row, starred } : row)),
      }));
      try {
        await api.setStarred(item.id, starred);
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async archive() {
      const { api, item } = get();
      if (api === null || item === null) return;
      try {
        await api.setArchived(item.id, true);
        set((state) => ({
          item: state.item === null ? null : { ...state.item, archived: true },
          items: state.items.filter((row) => row.id !== item.id),
        }));
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async markAllRead() {
      const { api, listRoute } = get();
      if (api === null) return;
      try {
        await api.markAllRead(listParams(listRoute));
        await get().changed();
      } catch (err) {
        set({ error: message(err) });
      }
    },

    askMarkAll() {
      const prompt = markAllPromptFor(get().listRoute, get().counts);
      if (prompt === null) {
        return get().markAllRead();
      }
      set({ markAllPrompt: prompt });
      return Promise.resolve();
    },

    async confirmMarkAll() {
      set({ markAllPrompt: null });
      await get().markAllRead();
    },

    cancelMarkAll() {
      set({ markAllPrompt: null });
    },

    // The source sheet shows a failure in place, so these reject rather
    // than open the error sheet.
    async refresh(id) {
      const { api } = get();
      if (api === null) return;
      await api.refresh(id);
      await Promise.all([get().loadSources(), get().loadCounts(), get().load()]);
    },

    async renameSource(id, title) {
      const { api } = get();
      if (api === null) return;
      await api.renameSource(id, title);
      await Promise.all([get().loadSources(), get().load()]);
    },

    async removeSource(id) {
      const { api, listRoute } = get();
      if (api === null) return;
      await api.removeSource(id);
      // The source's page has gone with it: show the unread view.
      if (listRoute.kind === "source" && listRoute.id === id) {
        await get().setRoute(UNREAD);
      }
      await Promise.all([get().loadSources(), get().loadCounts(), get().load()]);
    },

    async addSource(url) {
      const { api } = get();
      if (api === null) return;
      // The sheet shows a failure in place; a second error sheet would
      // cover it.
      await api.addSource(url);
      await Promise.all([get().loadSources(), get().loadCounts()]);
    },

    async importOPML(xml) {
      const { api } = get();
      if (api === null) return 0;
      try {
        const result = await api.importOPML(xml);
        await Promise.all([get().loadSources(), get().loadCounts(), get().load()]);
        return result.added;
      } catch (err) {
        set({ error: message(err) });
        return 0;
      }
    },

    async exportOPML() {
      const { api } = get();
      if (api === null) return "";
      try {
        return await api.exportOPML();
      } catch (err) {
        set({ error: message(err) });
        return "";
      }
    },

    async saveLater(url) {
      const { api } = get();
      if (api === null) return;
      // The sheet shows a failure in place; a second error sheet would
      // cover it.
      const saved = await api.saveLater(url);
      await Promise.all([get().loadCounts(), get().loadSources()]);
      await get().open(saved.id);
    },

    async saveAsNote(id) {
      const { api } = get();
      if (api === null) return;
      set({ busy: true });
      try {
        const note = await api.saveAsNote(id);
        set((state) => ({
          busy: false,
          noteRoutes: { ...state.noteRoutes, [id]: note.route },
          noteStates: { ...state.noteStates, [id]: { state: "present", path: note.path, route: note.route } },
          item: state.item !== null && state.item.id === id ? { ...state.item, notePath: note.path } : state.item,
        }));
      } catch (err) {
        set({ busy: false, error: message(err) });
      }
    },

    async checkNote(id) {
      const { api } = get();
      if (api === null) return null;
      try {
        const st = await api.noteStatus(id);
        set((state) => {
          let item = state.item;
          if (item !== null && item.id === id) {
            const next = { ...item };
            if (st.state === "missing") delete next.notePath;
            else if (st.path !== undefined) next.notePath = st.path;
            item = next;
          }
          return { noteStates: { ...state.noteStates, [id]: st }, item };
        });
        return st;
      } catch (err) {
        const st: NoteStatus = { state: "unreachable", error: message(err) };
        set((state) => ({ noteStates: { ...state.noteStates, [id]: st } }));
        return st;
      }
    },

    noteRoute(id) {
      const { item, noteRoutes, status } = get();
      const known = noteRoutes[id];
      if (known !== undefined) return known;
      if (item === null || item.id !== id || item.notePath === undefined || item.notePath === "" || status === null) {
        return null;
      }
      if (status.notes.vaultId === 0) return null;
      return routeOfNote(item.notePath, status.notes.vaultId);
    },

    setScheme(scheme) {
      set({ scheme });
    },

    async changed() {
      const { query } = get();
      await Promise.all([
        get().loadCounts(),
        get().loadSources(),
        query.trim() === "" ? get().load() : get().search(query),
      ]);
    },

    clearError() {
      set({ error: null });
    },

    async loadStatus() {
      const { api } = get();
      if (api === null) return;
      try {
        set({ status: await api.status() });
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async loadCounts() {
      const { api } = get();
      if (api === null) return;
      try {
        set({ counts: await api.counts() });
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async loadSources() {
      const { api } = get();
      if (api === null) return;
      try {
        set({ sources: await api.listSources() });
      } catch (err) {
        set({ error: message(err) });
      }
    },
  }));
}

/** listParams is the list a route asks for: a source's items are its "all". */
function listParams(route: Route): { view: View; sourceId?: number } {
  switch (route.kind) {
    case "source":
      return { view: "all", sourceId: route.id };
    case "view":
      return { view: route.view };
    case "item":
      return { view: "all" };
  }
}
