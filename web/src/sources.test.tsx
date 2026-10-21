// Managing a source from the sidebar: the "…" button, the sheet's fields,
// rename, refresh now, and unsubscribe with its confirm
// (docs/specs/api.md, sources.rename, sources.refresh and sources.remove).
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { Sidebar } from "./components/Sidebar.js";
import { SourceSheet } from "./components/SourceSheet.js";
import type { Source } from "./rpc/api.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

/** connected is a store over a fresh mock, and its API. */
async function connected() {
  const store = createReadStore();
  const api = readAPI(createMockTransport());
  await store.getState().connect(api);
  const source = store.getState().sources[0];
  expect(source).toBeDefined();
  if (source === undefined) throw new Error("no source");
  return { store, api, source };
}

/** renderSheet renders one source's sheet. */
function renderSheet(source: Source, onClose: () => void, store: Awaited<ReturnType<typeof connected>>["store"]) {
  render(
    <StoreProvider store={store}>
      <SourceSheet source={source} onClose={onClose} />
    </StoreProvider>,
  );
}

describe("the source sheet", () => {
  it("shows the source's title, addresses, polls and error", async () => {
    const { store, source } = await connected();
    const failing = { ...source, errorCount: 2, lastError: "the feed answered 503" };
    renderSheet(failing, () => {}, store);
    expect((screen.getByLabelText("Source title") as HTMLInputElement).value).toBe(source.title);
    expect(screen.getByRole("link", { name: source.url }).getAttribute("href")).toBe(source.url);
    expect(screen.getByText(String(failing.errorCount))).toBeTruthy();
    expect(screen.getByText(/Last error: the feed answered 503/)).toBeTruthy();
    expect(screen.getByText("Last poll")).toBeTruthy();
    expect(screen.getByText("Next poll")).toBeTruthy();
  });

  it("renames the source", async () => {
    const { store, source } = await connected();
    const renamed = vi.spyOn(store.getState(), "renameSource");
    renderSheet(source, () => {}, store);
    fireEvent.change(screen.getByLabelText("Source title"), { target: { value: "Renamed feed" } });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(renamed).toHaveBeenCalledWith(source.id, "Renamed feed"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Rename" })).toBeTruthy());
  });

  it("polls now", async () => {
    const { store, api, source } = await connected();
    const refresh = vi.spyOn(api, "refresh");
    renderSheet(source, () => {}, store);
    fireEvent.click(screen.getByRole("button", { name: "Refresh now" }));
    await waitFor(() => expect(refresh).toHaveBeenCalledWith(source.id));
  });

  it("asks before unsubscribing, and says what goes", async () => {
    const { store, api, source } = await connected();
    const remove = vi.spyOn(api, "removeSource");
    const onClose = vi.fn();
    renderSheet(source, onClose, store);

    fireEvent.click(screen.getByRole("button", { name: "Unsubscribe" }));
    expect(screen.getByText(`Unsubscribe from ${source.title}? All of its items go too.`)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Keep" }));
    expect(screen.queryByText(/All of its items go too/)).toBeNull();
    expect(remove).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Unsubscribe" }));
    fireEvent.click(screen.getByRole("button", { name: "Unsubscribe" }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith(source.id));
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
    expect(store.getState().sources.some((row) => row.id === source.id)).toBe(false);
  });
});

describe("the sidebar's manage button", () => {
  it("opens the sheet for a source", async () => {
    const { store, source } = await connected();
    render(
      <StoreProvider store={store}>
        <Sidebar />
      </StoreProvider>,
    );
    const manage = screen.getByRole("button", { name: `Manage ${source.title}` });
    fireEvent.click(manage);
    expect((screen.getByLabelText("Source title") as HTMLInputElement).value).toBe(source.title);
  });
});
