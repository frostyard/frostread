// The app's state, as one zustand store (createReadStore): the list, the
// open item, the counts, the query, and every action the panes and the keys
// ask for. It holds no DOM, so the tests drive it directly.
import { createStore, type StoreApi } from "zustand/vanilla";
import { type Route, noteRoute as routeOfNote, UNREAD } from "./lib/routes.js";
import type { Counts, ItemDetail, ItemSummary, ReadAPI, Source, Status, View } from "./rpc/api.js";

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
  /** scheme is the shell's theme, which the article's frame follows. */
  scheme: "light" | "dark";
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
  refresh(id?: number): Promise<void>;
  addSource(url: string): Promise<void>;
  importOPML(xml: string): Promise<number>;
  exportOPML(): Promise<string>;
  saveLater(url: string): Promise<void>;
  saveAsNote(id: number): Promise<void>;
  noteRoute(id: number): string | null;
  setScheme(scheme: "light" | "dark"): void;
  changed(): Promise<void>;
  clearError(): void;
  loadStatus(): Promise<void>;
  loadCounts(): Promise<void>;
  loadSources(): Promise<void>;
}

export type ReadStore = ReadState & ReadActions;

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
    scheme: "light",
    busy: false,

    async connect(api) {
      set({ api });
      // Live updates: readd sends items.changed once a connection subscribes.
      await api.subscribe().catch((err: unknown) => {
        set({ error: message(err) });
      });
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

    async refresh(id) {
      const { api } = get();
      if (api === null) return;
      try {
        await api.refresh(id);
      } catch (err) {
        set({ error: message(err) });
      }
    },

    async addSource(url) {
      const { api } = get();
      if (api === null) return;
      try {
        await api.addSource(url);
        await Promise.all([get().loadSources(), get().loadCounts()]);
      } catch (err) {
        set({ error: message(err) });
      }
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
      try {
        const saved = await api.saveLater(url);
        await Promise.all([get().loadCounts(), get().loadSources()]);
        await get().open(saved.id);
      } catch (err) {
        set({ error: message(err) });
      }
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
          item: state.item !== null && state.item.id === id ? { ...state.item, notePath: note.path } : state.item,
        }));
      } catch (err) {
        set({ busy: false, error: message(err) });
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
