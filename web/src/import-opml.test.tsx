// Import OPML reports every count, including skipped feeds (frostread#64).
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { Sidebar } from "./components/Sidebar.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

async function connected() {
  const store = createReadStore();
  const api = readAPI(createMockTransport());
  await store.getState().connect(api);
  render(
    <StoreProvider store={store}>
      <Sidebar />
    </StoreProvider>,
  );
  return { store, api };
}

function choose(xml: string) {
  const file = new File([xml], "feeds.opml", { type: "text/xml" });
  const input = screen.getByLabelText("OPML file") as HTMLInputElement;
  fireEvent.change(input, { target: { files: [file] } });
  expect(input.value).toBe("");
}

describe("the OPML import notice", () => {
  it.each([
    ["added only", 2, 0, "Imported 2 feeds."],
    ["skipped only", 0, 2, "No feeds imported. Skipped 2: already a source, or an address Read cannot reach."],
    ["both", 1, 1, "Imported 1 feed. Skipped 1: already a source, or an address Read cannot reach."],
    ["none", 0, 0, "No feeds imported. The file names no feeds."],
  ] as const)("shows %s and closes with OK", async (_name, added, skipped, message) => {
    const { store, api } = await connected();
    const source = store.getState().sources[0];
    if (source === undefined) throw new Error("no source");
    const imported = vi.spyOn(api, "importOPML");
    const outlines = [
      ...Array.from({ length: added }, (_, n) => `<outline xmlUrl="https://example.org/new-${n}.xml"/>`),
      ...Array.from({ length: skipped }, () => `<outline xmlUrl="${source.url}"/>`),
    ].join("");
    choose(`<opml><body>${outlines}</body></opml>`);
    const dialog = await screen.findByRole("dialog", { name: "Import OPML" });
    expect(within(dialog).getByText(message)).toBeTruthy();
    expect(await imported.mock.results[0]?.value).toEqual({ added, skipped });
    fireEvent.click(within(dialog).getByRole("button", { name: "OK" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("keeps failures in the error state without a success notice", async () => {
    const { store, api } = await connected();
    vi.spyOn(api, "importOPML").mockRejectedValue(new Error("Import failed"));
    choose("bad OPML");
    await waitFor(() => expect(store.getState().error).toBe("Import failed"));
    expect(screen.queryByRole("dialog", { name: "Import OPML" })).toBeNull();
  });
});
