// The store against the mock transport (docs/specs/api.md): a view loads
// its list, j and k move, and opening an item marks it read.
import { describe, expect, it } from "vitest";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore, markAllPromptFor } from "./store.js";

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
