// The store against the mock transport (docs/specs/api.md): a view loads
// its list, j and k move, and opening an item marks it read.
import { describe, expect, it } from "vitest";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

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
