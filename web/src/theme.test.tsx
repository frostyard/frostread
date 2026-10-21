// The article's frame follows the shell's theme: the content page's
// ?scheme= carries it, and a theme change reloads the frame (the app's own
// surface follows lodge's data-theme through lodge-ui).
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { StoreProvider } from "./components/context.js";
import { Reader } from "./components/Reader.js";
import { setLodge } from "./lodge.js";
import type { ItemDetail, Status } from "./rpc/api.js";
import { checked, validateItemDetail } from "./rpc/contract.js";
import doneJSON from "./rpc/testdata/items.get.done.json";
import { createReadStore } from "./store.js";

const done: ItemDetail = checked<ItemDetail>(doneJSON, validateItemDetail);

const status: Status = {
  version: "test",
  notes: { configured: false, vaultId: 0, folder: "Reading/" },
};

afterEach(() => {
  setLodge(null);
});

describe("the article frame's theme", () => {
  it("passes the scheme, and reloads the frame when the theme changes", () => {
    // A data URL keeps the test's frame off the network.
    setLodge({ open: () => {}, route: () => {}, contentURL: (path) => `data:text/html,${path}` });
    const store = createReadStore();
    store.setState({ item: done, status, selectedId: done.id, scheme: "dark" });
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    const frame = screen.getByTitle(done.title);
    expect(frame.getAttribute("src")).toBe(`data:text/html,${done.contentPath}?scheme=dark`);

    act(() => store.getState().setScheme("light"));
    expect(frame.getAttribute("src")).toBe(`data:text/html,${done.contentPath}?scheme=light`);
  });
});
