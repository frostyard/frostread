// The desktop reader's article frame (ADR-0008): items.render's page in a
// srcdoc frame with no script and no same-origin grant. The lodge reader
// keeps lodge-content's URL, and never asks the daemon to render a page.
import type { Transport } from "@frostyard/lodge-client/rpc";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StoreApi } from "zustand/vanilla";
import { StoreProvider } from "./components/context.js";
import { Reader } from "./components/Reader.js";
import { createDesktopShell } from "./desktop/shell.js";
import { setLodge } from "./lodge.js";
import { type ItemRender, readAPI } from "./rpc/api.js";
import { createMockTransport, MockError } from "./rpc/mock.js";
import { ShellProvider } from "./shell.js";
import { createReadStore, type ReadStore } from "./store.js";

afterEach(() => {
  setLodge(null);
  vi.restoreAllMocks();
});

/** RenderStub answers items.render for a test, in place of the mock's own. */
type RenderStub = (params: unknown) => Promise<ItemRender>;

/** schemeOf reads the scheme of an items.render call's params. */
function schemeOf(params: unknown): unknown {
  return (params as { scheme?: unknown }).scheme;
}

/** openStore is a store on the mock, its items.render the stub, with item 1
 * open, whose extractState is "done". */
async function openStore(renderItem: RenderStub): Promise<StoreApi<ReadStore>> {
  const mock = createMockTransport();
  const transport: Transport = {
    call: async <T,>(method: string, params: unknown): Promise<T> => {
      if (method === "items.render") return (await renderItem(params)) as T;
      return mock.call<T>(method, params);
    },
    onEvent: (handler) => mock.onEvent(handler),
    onClose: (handler) => mock.onClose(handler),
  };
  const store = createReadStore();
  await store.getState().connect(readAPI(transport));
  await store.getState().open(1);
  expect(store.getState().item?.extractState).toBe("done");
  return store;
}

/** show renders the reader in the desktop's shell. */
function show(store: StoreApi<ReadStore>) {
  return render(
    <ShellProvider shell={createDesktopShell(window)}>
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>
    </ShellProvider>,
  );
}

/** findFrame waits for the article's frame. */
async function findFrame(): Promise<HTMLIFrameElement> {
  return waitFor(() => {
    const frame = document.querySelector("iframe");
    if (frame === null) throw new Error("no frame");
    return frame;
  });
}

const PAGE = '<!doctype html><html lang="en"><head><title>One</title></head><body><p>one</p></body></html>';

describe("the desktop reader's article frame", () => {
  it("shows items.render's page in a srcdoc frame with sandbox allow-popups alone", async () => {
    const renderItem = vi.fn<RenderStub>(async () => ({ html: PAGE, omittedImages: 0 }));
    show(await openStore(renderItem));
    const frame = await findFrame();
    expect(renderItem).toHaveBeenCalledOnce();
    expect(frame.getAttribute("srcdoc")).toBe(PAGE);
    expect(frame.getAttribute("sandbox")).toBe("allow-popups");
    expect(frame.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(frame.hasAttribute("src")).toBe(false);
    // The frame's whole markup holds neither token, in no attribute.
    expect(frame.outerHTML).not.toContain("allow-scripts");
    expect(frame.outerHTML).not.toContain("allow-same-origin");
  });

  it("renders the page again when the scheme changes", async () => {
    const renderItem = vi.fn<RenderStub>(async (params) => ({
      html: `<!doctype html><html data-scheme="${String(schemeOf(params))}"><body><p>page</p></body></html>`,
      omittedImages: 0,
    }));
    const store = await openStore(renderItem);
    show(store);
    await waitFor(() => expect(renderItem).toHaveBeenCalledTimes(1));
    expect(renderItem.mock.calls[0]?.[0]).toEqual({ id: 1, scheme: "light" });
    expect((await findFrame()).getAttribute("srcdoc")).toContain('data-scheme="light"');

    act(() => store.getState().setScheme("dark"));
    await waitFor(() => expect(renderItem).toHaveBeenCalledTimes(2));
    expect(renderItem.mock.calls[1]?.[0]).toEqual({ id: 1, scheme: "dark" });
    await waitFor(() =>
      expect(document.querySelector("iframe")?.getAttribute("srcdoc")).toContain('data-scheme="dark"'),
    );
  });

  it("says how many images were left out", async () => {
    for (const [omitted, text] of [
      [3, "3 images were left out"],
      [1, "1 image was left out"],
      [0, null],
    ] as const) {
      const renderItem = vi.fn<RenderStub>(async () => ({ html: PAGE, omittedImages: omitted }));
      const view = show(await openStore(renderItem));
      if (text === null) {
        await findFrame();
        expect(screen.queryByRole("status")).toBeNull();
      } else {
        const bar = await screen.findByRole("status");
        expect(bar.textContent).toContain(text);
      }
      view.unmount();
    }
  });

  it("shows the excerpt and the original's link when the article is too large", async () => {
    const renderItem = vi.fn<RenderStub>(async () => {
      throw new MockError(1006, "too large");
    });
    const store = await openStore(renderItem);
    show(store);
    expect(await screen.findByText("This article is too large to show in the app.")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    const item = store.getState().item;
    expect(screen.getByText(item?.excerpt ?? "missing")).toBeTruthy();
    const link = screen.getByRole("link", { name: "Open the original" });
    expect(link.getAttribute("href")).toBe(item?.url);
    expect(link.getAttribute("target")).toBe("_blank");
  });

  it("shows another error with the excerpt, and no frame", async () => {
    const renderItem = vi.fn<RenderStub>(async () => {
      throw new MockError(1001, "no such item");
    });
    show(await openStore(renderItem));
    expect(await screen.findByText("no such item")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("drops a page that answers after the scheme changed", async () => {
    const OLD = "<!doctype html><html><body><p>old</p></body></html>";
    const NEW = "<!doctype html><html><body><p>new</p></body></html>";
    let release: () => void = () => {};
    const held = new Promise<ItemRender>((resolve) => {
      release = () => resolve({ html: OLD, omittedImages: 0 });
    });
    const renderItem = vi.fn<RenderStub>(async (params) =>
      schemeOf(params) === "light" ? held : { html: NEW, omittedImages: 0 },
    );
    const store = await openStore(renderItem);
    show(store);
    await waitFor(() => expect(renderItem).toHaveBeenCalledTimes(1));

    act(() => store.getState().setScheme("dark"));
    await waitFor(() => expect(document.querySelector("iframe")?.getAttribute("srcdoc")).toBe(NEW));

    // The first answer comes now, too late: it must not replace the page.
    await act(async () => {
      release();
    });
    expect(document.querySelector("iframe")?.getAttribute("srcdoc")).toBe(NEW);
  });

  it("never shows the last item's page under the next item", async () => {
    const ONE = "<!doctype html><html><body><p>one</p></body></html>";
    let release: () => void = () => {};
    const held = new Promise<ItemRender>((resolve) => {
      release = () => resolve({ html: "<!doctype html><html><body><p>two</p></body></html>", omittedImages: 0 });
    });
    const renderItem = vi.fn<RenderStub>(async (params) =>
      (params as { id: number }).id === 1 ? { html: ONE, omittedImages: 0 } : held,
    );
    const store = await openStore(renderItem);
    show(store);
    expect((await findFrame()).getAttribute("srcdoc")).toBe(ONE);

    // Item 2 is "done" in the mock too; its page has not come yet.
    await act(async () => {
      await store.getState().open(2);
    });
    expect(store.getState().item?.extractState).toBe("done");
    await waitFor(() => expect(renderItem).toHaveBeenCalledTimes(2));
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.getByText("Loading…")).toBeTruthy();

    await act(async () => {
      release();
    });
    await waitFor(() => expect(document.querySelector("iframe")?.getAttribute("srcdoc")).toContain("two"));
  });

  it("keeps the lodge reader's content frame and never asks the daemon to render", async () => {
    const renderItem = vi.fn<RenderStub>(async () => ({ html: PAGE, omittedImages: 0 }));
    setLodge({ open: () => {}, contentURL: (path) => `data:text/html,${encodeURIComponent(path)}`, route: () => {} });
    const store = await openStore(renderItem);
    const item = store.getState().item;
    // The daemon's contentPath is relative to the content root, which is
    // what lodge-client's contentURL takes (docs/specs/api.md).
    store.setState({ item: item === null ? null : { ...item, contentPath: "item/1/mock-mac" } });

    // No ShellProvider: the app's shell is lodge's.
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    const frame = await findFrame();
    expect(frame.getAttribute("src")).toBe(
      `data:text/html,${encodeURIComponent("item/1/mock-mac")}?scheme=${store.getState().scheme}`,
    );
    expect(frame.getAttribute("sandbox")).toBe("allow-popups allow-popups-to-escape-sandbox");
    expect(frame.hasAttribute("srcdoc")).toBe(false);
    expect(renderItem).not.toHaveBeenCalled();
  });
});
