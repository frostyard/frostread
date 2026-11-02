// The store against the mock transport (docs/specs/api.md): a view loads
// its list, j and k move, and opening an item marks it read.

import type { Transport } from "@frostyard/lodge-client/rpc";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readAPI } from "./rpc/api.js";
import { watchChanges } from "./rpc/events.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore, markAllPromptFor } from "./store.js";

afterEach(() => {
  vi.restoreAllMocks();
});

/** connected is a store over a fresh mock. */
async function connected() {
  const store = createReadStore();
  await store.getState().connect(readAPI(createMockTransport()));
  return store;
}

describe("the store", () => {
  it("loads the unread list when it connects", async () => {
    const store = await connected();
    const state = store.getState();
    expect(state.items.length).toBeGreaterThan(0);
    expect(state.items.every((item) => !item.read)).toBe(true);
    expect(state.sources).toHaveLength(3);
    expect(state.counts.views.unread).toBe(state.items.length);
  });

  it("loads a view's list when the view is chosen", async () => {
    const store = await connected();
    await store.getState().setRoute({ kind: "view", view: "later" });
    const state = store.getState();
    expect(state.listRoute).toEqual({ kind: "view", view: "later" });
    expect(state.items.length).toBeGreaterThan(0);
    expect(state.items.every((item) => item.saved)).toBe(true);
  });

  it("moves the selection with j and k (selectOffset), and opens each item", async () => {
    const store = await connected();
    const items = store.getState().items;
    expect(items.length).toBeGreaterThan(2);
    await store.getState().selectOffset(1);
    expect(store.getState().selectedId).toBe(items[0]?.id);
    await store.getState().selectOffset(1);
    expect(store.getState().selectedId).toBe(items[1]?.id);
    expect(store.getState().route).toEqual({ kind: "item", id: items[1]?.id });
    await store.getState().selectOffset(-1);
    expect(store.getState().selectedId).toBe(items[0]?.id);
  });

  it("marks an item read when it is opened", async () => {
    const store = await connected();
    const unread = store.getState().items.find((item) => !item.read);
    expect(unread).toBeDefined();
    if (unread === undefined) return;
    await store.getState().open(unread.id);
    expect(store.getState().item?.read).toBe(true);
    expect(store.getState().items.find((item) => item.id === unread.id)?.read).toBe(true);
    const api = store.getState().api;
    expect(api).not.toBeNull();
    if (api === null) return;
    expect((await api.getItem(unread.id)).read).toBe(true);
  });

  it("keeps the list behind an item, and goes back to it", async () => {
    const store = await connected();
    const first = store.getState().items[0];
    expect(first).toBeDefined();
    if (first === undefined) return;
    await store.getState().open(first.id);
    expect(store.getState().route).toEqual({ kind: "item", id: first.id });
    store.getState().closeItem();
    expect(store.getState().route).toEqual({ kind: "view", view: "unread" });
    expect(store.getState().selectedId).toBeNull();
  });

  it("searches with items.search, and clears back to the list", async () => {
    const store = await connected();
    await store.getState().search("feeds");
    const found = store.getState().items;
    expect(found.length).toBeGreaterThan(0);
    expect(found.every((item) => `${item.title} ${item.excerpt}`.toLowerCase().includes("feeds"))).toBe(true);
    await store.getState().search("");
    expect(store.getState().query).toBe("");
    expect(store.getState().items.every((item) => !item.read)).toBe(true);
  });

  it("saves a link and opens it", async () => {
    const store = await connected();
    await store.getState().saveLater("https://example.com/read-this");
    expect(store.getState().item?.saved).toBe(true);
    expect(store.getState().item?.url).toBe("https://example.com/read-this");
  });
});

describe("a long list", () => {
  it("keeps the rows past the first page, the selection and j's place across a change", async () => {
    const store = createReadStore();
    const transport = createMockTransport({ bulk: 120 });
    // The entries' own subscription (main.tsx and desktop/start.tsx): an
    // event is what makes the store reload the list.
    const unwatch = watchChanges(transport, () => void store.getState().changed());
    try {
      await store.getState().connect(readAPI(transport));
      await store.getState().setRoute({ kind: "source", id: 1 });
      while (store.getState().next !== null && store.getState().items.length < 100) {
        await store.getState().loadMore();
      }
      const loaded = store.getState().items;
      expect(loaded.length).toBeGreaterThanOrEqual(100);
      const opened = loaded[60];
      const after = loaded[61];
      expect(opened).toBeDefined();
      expect(after).toBeDefined();
      if (opened === undefined || after === undefined) return;

      const api = store.getState().api;
      expect(api).not.toBeNull();
      if (api === null) return;
      const spy = vi.spyOn(api, "listItems");
      const asked = spy.mock.calls.length;

      // The open marks the row read, and readd reports the change after it.
      await store.getState().open(opened.id);
      await vi.waitFor(() => expect(spy.mock.calls.length).toBeGreaterThan(asked));
      await vi.waitFor(() => expect(store.getState().loading).toBe(false));

      // The rows the pages added are still here, so the open row keeps its
      // place and j goes on from it.
      const state = store.getState();
      expect(state.items.length).toBeGreaterThanOrEqual(loaded.length);
      expect(state.selectedId).toBe(opened.id);
      expect(state.items[60]?.id).toBe(opened.id);

      await store.getState().selectOffset(1);
      expect(store.getState().selectedId).toBe(after.id);
      expect(store.getState().route).toEqual({ kind: "item", id: after.id });
    } finally {
      unwatch();
    }
  });

  it("ends with one consistent list when a loadMore and a change overlap", async () => {
    const store = createReadStore();
    const mock = createMockTransport({ bulk: 120 });
    // The loadMore's answer is held, so it comes back after the reload's:
    // an answer for the older list must not append to the newer one.
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const transport: Transport = {
      call: async <T>(method: string, params: unknown): Promise<T> => {
        // A list call with a cursor is loadMore's next page.
        if (method === "items.list" && (params as { cursor?: string }).cursor !== undefined) await held;
        return mock.call<T>(method, params);
      },
      onEvent: (handler) => mock.onEvent(handler),
      onClose: (handler) => mock.onClose(handler),
    };

    await store.getState().connect(readAPI(transport));
    await store.getState().setRoute({ kind: "source", id: 1 });
    const loaded = store.getState().items.length;
    expect(loaded).toBe(50);

    const more = store.getState().loadMore();
    const change = store.getState().changed();
    await change;
    // The newer list is in place, and then the older list's page comes back.
    release();
    await more;
    await vi.waitFor(() => expect(store.getState().loading).toBe(false));

    const state = store.getState();
    // The reloaded list is the one the change fetched, at the length it
    // started with: the stale page appended nothing to it.
    expect(state.items.length).toBe(loaded);
    const ids = state.items.map((item) => item.id);
    expect(new Set(ids).size).toBe(ids.length);
    const api = state.api;
    expect(api).not.toBeNull();
    if (api === null) return;
    const page = await api.listItems({ view: "all", sourceId: 1, limit: ids.length });
    expect(ids).toEqual(page.items.map((item) => item.id));
  });

  it("keeps the reader on the item opened last when the answers come back out of order", async () => {
    const store = createReadStore();
    const mock = createMockTransport();
    // The first items.get answer is held, so it comes back after the second
    // one: a fast run of opens answers out of order.
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let holding = true;
    const transport: Transport = {
      call: async <T>(method: string, params: unknown): Promise<T> => {
        if (method === "items.get" && holding) {
          holding = false;
          await held;
        }
        return mock.call<T>(method, params);
      },
      onEvent: (handler) => mock.onEvent(handler),
      onClose: (handler) => mock.onClose(handler),
    };

    await store.getState().connect(readAPI(transport));
    const rows = store.getState().items;
    const first = rows[0];
    const second = rows[1];
    expect(first).toBeDefined();
    expect(second).toBeDefined();
    if (first === undefined || second === undefined) return;

    const older = store.getState().open(first.id);
    await store.getState().open(second.id);
    expect(store.getState().item?.id).toBe(second.id);

    // The older answer arrives last, and the reader keeps the newer item.
    release();
    await older;
    const state = store.getState();
    expect(state.item?.id).toBe(second.id);
    expect(state.selectedId).toBe(second.id);
    expect(state.itemLoading).toBe(false);
    // Opening an item marks it read, even when its answer is late.
    const api = state.api;
    expect(api).not.toBeNull();
    if (api === null) return;
    expect((await api.getItem(first.id)).read).toBe(true);
  });

  it("stops the reader's spinner when the person leaves an item that is still loading", async () => {
    const store = createReadStore();
    const mock = createMockTransport();
    // The item's answer is held, so the person can leave it before it comes.
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let holding = true;
    const transport: Transport = {
      call: async <T>(method: string, params: unknown): Promise<T> => {
        if (method === "items.get" && holding) {
          holding = false;
          await held;
        }
        return mock.call<T>(method, params);
      },
      onEvent: (handler) => mock.onEvent(handler),
      onClose: (handler) => mock.onClose(handler),
    };

    await store.getState().connect(readAPI(transport));
    const row = store.getState().items[0];
    expect(row).toBeDefined();
    if (row === undefined) return;

    const opening = store.getState().open(row.id);
    expect(store.getState().itemLoading).toBe(true);
    await store.getState().setRoute({ kind: "view", view: "starred" });
    // The stale answer must not bring back a spinner that nothing clears.
    release();
    await opening;
    const state = store.getState();
    expect(state.itemLoading).toBe(false);
    expect(state.item).toBeNull();
  });

  it("asks for a page of at least 50 rows when the list is short", async () => {
    const store = createReadStore();
    await store.getState().connect(readAPI(createMockTransport({ bulk: 120 })));
    await store.getState().setRoute({ kind: "source", id: 1 });
    // A reload of a short list still asks for a plain page, not for the few
    // rows it holds.
    store.setState({ items: store.getState().items.slice(0, 3), next: null });
    const api = store.getState().api;
    expect(api).not.toBeNull();
    if (api === null) return;
    const spy = vi.spyOn(api, "listItems");
    await store.getState().reload();
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0]?.[0]?.limit).toBe(50);
    expect(store.getState().items.length).toBe(50);
  });
});

describe("a permalink into the reader", () => {
  it("opens an item outside the current view, with the All list behind it", async () => {
    const store = await connected();
    // An archived article is not in the Unread list the store starts with.
    const api = store.getState().api;
    expect(api).not.toBeNull();
    if (api === null) return;
    const archived = (await api.listItems({ view: "archive", limit: 5 })).items[0];
    expect(archived).toBeDefined();
    if (archived === undefined) return;
    expect(store.getState().items.some((item) => item.id === archived.id)).toBe(false);

    await store.getState().setRoute({ kind: "item", id: archived.id });

    const state = store.getState();
    expect(state.route).toEqual({ kind: "item", id: archived.id });
    expect(state.listRoute).toEqual({ kind: "view", view: "all" });
    expect(state.items.some((item) => item.id === archived.id)).toBe(true);
    expect(state.item?.id).toBe(archived.id);
  });
});

describe("mark all read", () => {
  it("asks first over 20 items, and marks them after the answer", async () => {
    const store = await connected();
    const before = store.getState().counts.views.unread;
    expect(before).toBeGreaterThan(20);
    await store.getState().askMarkAll();
    expect(store.getState().markAllPrompt).toBe(`Mark ${before} items as read?`);

    store.getState().cancelMarkAll();
    expect(store.getState().markAllPrompt).toBeNull();
    expect(store.getState().counts.views.unread).toBe(before);

    await store.getState().askMarkAll();
    await store.getState().confirmMarkAll();
    expect(store.getState().markAllPrompt).toBeNull();
    expect(store.getState().counts.views.unread).toBe(0);
  });

  it("does not ask for a few, and never runs a mark of a source's own", async () => {
    const store = await connected();
    store.setState({ counts: { ...store.getState().counts, views: { ...store.getState().counts.views, unread: 5 } } });
    await store.getState().askMarkAll();
    expect(store.getState().markAllPrompt).toBeNull();
    expect(store.getState().counts.views.unread).toBe(0);
  });

  it("has one question for the whole count, and one where it cannot know", () => {
    const counts = {
      views: { unread: 37, starred: 3, later: 0, archive: 0, all: 40 },
      sources: [{ sourceId: 1, unread: 21 }],
    };
    expect(markAllPromptFor({ kind: "view", view: "unread" }, counts)).toBe("Mark 37 items as read?");
    expect(markAllPromptFor({ kind: "view", view: "all" }, counts)).toBe("Mark 37 items as read?");
    expect(markAllPromptFor({ kind: "source", id: 1 }, counts)).toBe("Mark 21 items as read?");
    expect(markAllPromptFor({ kind: "view", view: "starred" }, counts)).toBe(
      "Mark every unread item in this view as read?",
    );
    expect(
      markAllPromptFor({ kind: "view", view: "unread" }, { ...counts, views: { ...counts.views, unread: 21 } }),
    ).toBe("Mark 21 items as read?");
    expect(
      markAllPromptFor({ kind: "view", view: "unread" }, { ...counts, views: { ...counts.views, unread: 5 } }),
    ).toBeNull();
  });
});

describe("the save and bookmarklet pages", () => {
  it("take the whole frame without opening an item or changing the list", async () => {
    const store = await connected();
    const first = store.getState().items[0];
    expect(first).toBeDefined();
    if (first === undefined) return;
    await store.getState().open(first.id);
    const listRoute = store.getState().listRoute;

    await store.getState().setRoute({ kind: "bookmarklet" });
    expect(store.getState().route).toEqual({ kind: "bookmarklet" });
    expect(store.getState().listRoute).toEqual(listRoute);
    expect(store.getState().selectedId).toBeNull();
    expect(store.getState().item).toBeNull();
    expect(markAllPromptFor(store.getState().route, store.getState().counts)).toBeNull();

    await store.getState().setRoute({
      kind: "save",
      url: "https://example.com/from-my-phone",
      title: "A page from my phone",
      path: "/save?url=https%3A%2F%2Fexample.com%2Ffrom-my-phone&title=A%20page%20from%20my%20phone",
    });
    expect(store.getState().route.kind).toBe("save");
    expect(store.getState().listRoute).toEqual(listRoute);
    expect(store.getState().selectedId).toBeNull();
    expect(markAllPromptFor(store.getState().route, store.getState().counts)).toBeNull();
  });

  it("saves a page with its title, from the save page", async () => {
    const store = await connected();
    const before = store.getState().counts.views.later;
    const saved = await store.getState().saveFromPage("https://example.com/from-my-phone", "A page from my phone");
    expect(saved.saved).toBe(true);
    expect(saved.title).toBe("A page from my phone");
    expect(store.getState().counts.views.later).toBe(before + 1);
    expect(store.getState().item).toBeNull();
  });
});
