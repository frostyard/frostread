// Save as note in the reader (docs/specs/api.md, items.saveAsNote): the
// button writes the item, becomes "Open in Notes", and that opens the route
// readd answered, through lodge.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { Reader } from "./components/Reader.js";
import { setLodge } from "./lodge.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

afterEach(() => {
  setLodge(null);
});

describe("Save as note", () => {
  it("saves the item, then opens it in Notes with the route readd answered", async () => {
    const open = vi.fn();
    // A data URL keeps the test's frame out of the network.
    setLodge({ open, contentURL: (path) => `data:text/html,${encodeURIComponent(path)}`, route: () => {} });

    const store = createReadStore();
    await store.getState().connect(readAPI(createMockTransport()));
    const unread = store.getState().items.find((item) => !item.read);
    expect(unread).toBeDefined();
    if (unread === undefined) return;
    await store.getState().open(unread.id);

    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );

    const save = await screen.findByRole("button", { name: "Save as note" });
    expect(save.hasAttribute("disabled")).toBe(false);
    fireEvent.click(save);

    const openNotes = await screen.findByRole("button", { name: "Open in Notes" });
    fireEvent.click(openNotes);

    // The link asks notes where the note is before it opens it.
    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    const route = open.mock.calls[0]?.[0] as string;
    expect(route.startsWith("/notes/1/Reading/")).toBe(true);
    expect(route.endsWith(".md")).toBe(true);
    expect(route).toBe(store.getState().noteRoute(unread.id));
    expect(store.getState().item?.notePath).not.toBe("");
  });
});
