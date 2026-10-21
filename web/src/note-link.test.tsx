// The "Open in Notes" button when the note moved, was deleted, or notes
// cannot be asked (docs/specs/api.md, items.noteStatus).
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { Reader } from "./components/Reader.js";
import { setLodge } from "./lodge.js";
import type { ItemDetail, NoteStatus } from "./rpc/api.js";
import { readAPI } from "./rpc/api.js";
import { checked, validateItemDetail } from "./rpc/contract.js";
import { createMockTransport } from "./rpc/mock.js";
import doneJSON from "./rpc/testdata/items.get.done.json";
import { createReadStore } from "./store.js";

const base: ItemDetail = checked<ItemDetail>(doneJSON, validateItemDetail);

/** item is the golden item with a note it saved earlier. */
function item(): ItemDetail {
  return { ...base, notePath: "Reading/2026-10-20 Note.md" };
}

/** store is connected over the mock, with noteStatus answering status. */
async function storeFor(status: () => Promise<NoteStatus>) {
  const store = createReadStore();
  const api = readAPI(createMockTransport());
  const detail = item();
  const stub = {
    ...api,
    getItem: async () => detail,
    noteStatus: status,
  };
  await store.getState().connect(stub);
  await store.getState().open(detail.id);
  return { store, stub };
}

afterEach(() => {
  setLodge(null);
});

describe("the note link", () => {
  it("opens the note when it is present", async () => {
    const open = vi.fn();
    setLodge({ open, contentURL: (path) => `data:text/html,${path}`, route: () => {} });
    const { store } = await storeFor(async () => ({
      state: "present",
      path: "Reading/2026-10-20 Note.md",
      route: "/notes/1/Reading/2026-10-20%20Note.md",
    }));
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    const button = await screen.findByRole("button", { name: "Open in Notes" });
    fireEvent.click(button);
    await waitFor(() => expect(open).toHaveBeenCalledWith("/notes/1/Reading/2026-10-20%20Note.md"));
  });

  it("says Save as note when the note is gone", async () => {
    const open = vi.fn();
    setLodge({ open, contentURL: (path) => `data:text/html,${path}`, route: () => {} });
    const { store } = await storeFor(async () => ({ state: "missing" }));
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    expect(await screen.findByRole("button", { name: "Save as note" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Open in Notes" })).toBeNull();
    expect(open).not.toHaveBeenCalled();
    // The server cleared note_path; the app did too.
    expect(store.getState().item?.notePath).toBeUndefined();
  });

  it("opens the new path when the note moved", async () => {
    const open = vi.fn();
    setLodge({ open, contentURL: (path) => `data:text/html,${path}`, route: () => {} });
    const { store } = await storeFor(async () => ({
      state: "moved",
      path: "Reading/2 - Note.md",
      route: "/notes/1/Reading/2%20-%20Note.md",
    }));
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Open in Notes" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith("/notes/1/Reading/2%20-%20Note.md"));
    expect(store.getState().item?.notePath).toBe("Reading/2 - Note.md");
  });

  it("keeps the link and says why when notes cannot be reached", async () => {
    const open = vi.fn();
    setLodge({ open, contentURL: (path) => `data:text/html,${path}`, route: () => {} });
    const { store } = await storeFor(async () => ({ state: "unreachable", error: "notes is down" }));
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    expect(await screen.findByText(/Notes could not be reached: notes is down/)).toBeTruthy();
    const button = screen.getByRole("button", { name: "Open in Notes" });
    fireEvent.click(button);
    await waitFor(() => expect(store.getState().noteStates[base.id]?.state).toBe("unreachable"));
    expect(open).not.toHaveBeenCalled();
    expect(store.getState().item?.notePath).toBe("Reading/2026-10-20 Note.md");
  });
});
