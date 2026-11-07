// Desktop Markdown export in local mode, and unchanged Save as note in
// lodge mode (web/src/components/Reader.tsx).
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { App } from "./components/App.js";
import { StoreProvider } from "./components/context.js";
import { mockServer } from "./desktop/server.js";
import { createDesktopShell, type SaveFile } from "./desktop/shell.js";
import { lodgeShell, setLodge } from "./lodge.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { ShellProvider } from "./shell.js";
import { createReadStore } from "./store.js";

async function openReader(saveFile: SaveFile, lodge: string) {
  const store = createReadStore();
  await store.getState().connect(readAPI(createMockTransport()));
  const item = store.getState().items.find((row) => !row.read);
  expect(item).toBeDefined();
  if (item === undefined) throw new Error("the mock has no unread item");
  await store.getState().open(item.id);

  const shell = createDesktopShell(window, () => window.performance.now(), saveFile, mockServer(lodge));
  render(
    <ShellProvider shell={shell}>
      <StoreProvider store={store}>
        <App />
      </StoreProvider>
    </ShellProvider>,
  );
  return { store, item };
}

async function openLodgeReader() {
  const store = createReadStore();
  await store.getState().connect(readAPI(createMockTransport()));
  const item = store.getState().items.find((row) => !row.read);
  expect(item).toBeDefined();
  if (item === undefined) throw new Error("the mock has no unread item");
  await store.getState().open(item.id);

  render(
    <ShellProvider shell={lodgeShell}>
      <StoreProvider store={store}>
        <App />
      </StoreProvider>
    </ShellProvider>,
  );
}

describe("Export as Markdown", () => {
  it("shows in local desktop mode and saves the mock's result", async () => {
    const saveFile = vi.fn(async () => true);
    const { store, item } = await openReader(saveFile, "");
    const api = store.getState().api;
    expect(api).not.toBeNull();
    const expected = await api?.exportMarkdown(item.id);

    expect(screen.queryByRole("button", { name: "Save as note" })).toBeNull();
    const exportButton = await screen.findByRole("button", { name: "Export as Markdown…" });
    fireEvent.click(exportButton);

    await waitFor(() => expect(saveFile).toHaveBeenCalledOnce());
    expect(saveFile).toHaveBeenCalledWith(expected?.filename, expected?.markdown, "text/markdown");
  });

  it("does no harm when the desktop save is cancelled", async () => {
    const saveFile = vi.fn(async () => false);
    const { store } = await openReader(saveFile, "");

    fireEvent.click(await screen.findByRole("button", { name: "Export as Markdown…" }));

    await waitFor(() => expect(saveFile).toHaveBeenCalledOnce());
    expect(store.getState().error).toBeNull();
  });

  it("shows an error when the desktop save rejects", async () => {
    const saveFile = vi.fn(async () => {
      throw new Error("cannot save Markdown");
    });
    const { store } = await openReader(saveFile, "");

    fireEvent.click(await screen.findByRole("button", { name: "Export as Markdown…" }));

    expect(await screen.findByText("cannot save Markdown")).toBeTruthy();
    expect(store.getState().error).toBe("cannot save Markdown");
  });

  it("keeps Save as note in desktop lodge mode", async () => {
    const saveFile = vi.fn(async () => true);
    await openReader(saveFile, "http://127.0.0.1:8480");

    expect(await screen.findByRole("button", { name: "Save as note" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Export as Markdown…" })).toBeNull();
  });

  it("keeps Save as note in the lodge shell", async () => {
    setLodge(null);
    await openLodgeReader();

    expect(await screen.findByRole("button", { name: "Save as note" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Export as Markdown…" })).toBeNull();
  });
});
