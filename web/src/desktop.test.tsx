// The desktop entry on the mock transport, with no lodge: the sources, the
// system's scheme, the fragment's routes, and no save pages.
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDesktopShell, desktopRoute } from "./desktop/shell.js";
import { type Desktop, startDesktop } from "./desktop/start.js";

let started: Desktop | null = null;
let root: HTMLElement;
const realMatchMedia = window.matchMedia;

beforeEach(() => {
  window.history.replaceState(null, "", "/");
  root = document.createElement("div");
  root.id = "root";
  document.body.append(root);
});

afterEach(() => {
  started?.stop();
  started = null;
  root.remove();
  window.matchMedia = realMatchMedia;
  delete document.documentElement.dataset.theme;
  window.history.replaceState(null, "", "/");
  vi.restoreAllMocks();
});

/** start runs the desktop entry in the page, on the mock. */
async function start(hash = ""): Promise<Desktop> {
  if (hash !== "") window.history.replaceState(null, "", hash);
  started = await startDesktop({ root });
  return started;
}

/** fakeScheme stubs matchMedia: the dark query follows dark, and fires its change listeners. */
function fakeScheme(dark: boolean): (next: boolean) => void {
  const listeners = new Set<() => void>();
  const state = { dark };
  window.matchMedia = ((query: string) => ({
    get matches() {
      return query.includes("prefers-color-scheme") && state.dark;
    },
    media: query,
    addEventListener: (_type: string, fn: () => void) => {
      if (query.includes("prefers-color-scheme")) listeners.add(fn);
    },
    removeEventListener: (_type: string, fn: () => void) => listeners.delete(fn),
  })) as unknown as typeof window.matchMedia;
  return (next) => {
    state.dark = next;
    for (const fn of [...listeners]) fn();
  };
}

describe("the desktop entry", () => {
  it("lists the mock's sources in the sidebar", async () => {
    const desktop = await start();
    await waitFor(() => expect(desktop.store.getState().sources.length).toBeGreaterThan(0));
    const first = desktop.store.getState().sources[0];
    expect(first).toBeDefined();
    const nav = screen.getByRole("navigation", { name: "Views and sources" });
    expect(nav.textContent).toContain(first?.title);
  });

  it("follows the system's scheme", async () => {
    const fire = fakeScheme(false);
    const desktop = await start();
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(desktop.store.getState().scheme).toBe("light");
    act(() => fire(true));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(desktop.store.getState().scheme).toBe("dark");
  });

  it("moves between routes and back", async () => {
    await start();
    await screen.findByRole("button", { name: /^Starred/ });
    const before = window.history.length;
    fireEvent.click(screen.getByRole("button", { name: /^Starred/ }));
    await waitFor(() => expect(window.location.hash).toBe("#/view/starred"));
    expect(window.history.length).toBe(before + 1);
    expect(document.title).toBe("Starred");
    act(() => window.history.back());
    await waitFor(() => expect(started?.store.getState().route).toEqual({ kind: "view", view: "unread" }));
    await waitFor(() => expect(document.title).toBe("Unread"));
    expect(window.location.hash).toBe("#/");
    // happy-dom's history.length drops on back(), so the entry count is
    // checked after forward(): going back added none.
    act(() => window.history.forward());
    await waitFor(() => expect(window.location.hash).toBe("#/view/starred"));
    await waitFor(() => expect(started?.store.getState().route).toEqual({ kind: "view", view: "starred" }));
    expect(window.history.length).toBe(before + 1);
  });

  it("starts at the fragment's item with no new history entry", async () => {
    const before = window.history.length;
    const desktop = await start("#/item/3");
    await waitFor(() => expect(desktop.store.getState().selectedId).toBe(3));
    await waitFor(() => expect(desktop.store.getState().item).not.toBeNull());
    const title = desktop.store.getState().item?.title ?? "";
    expect(title).not.toBe("");
    await waitFor(() => expect(document.title).toBe(title));
    expect(screen.getAllByText(title).length).toBeGreaterThan(0);
    expect(window.location.hash).toBe("#/item/3");
    expect(window.history.length).toBe(before);
  });

  it("starts at the unread view from a save route, with no new history entry", async () => {
    const before = window.history.length;
    await start("#/save?url=https://example.com/a");
    await screen.findByRole("button", { name: /^Starred/ });
    await waitFor(() => expect(window.location.hash).toBe("#/"));
    expect(window.history.length).toBe(before);
  });

  it("has no save page and no bookmarklet entry", async () => {
    await start("#/save?url=https://example.com/a");
    await screen.findByRole("button", { name: /^Starred/ });
    expect(started?.store.getState().route).toEqual({ kind: "view", view: "unread" });
    expect(screen.queryByRole("button", { name: "Save from your phone" })).toBeNull();
    expect(screen.queryByText("Save to Read")).toBeNull();
  });

  it("shows the unread view at the bookmarklet's route", async () => {
    await start("#/bookmarklet");
    await screen.findByRole("button", { name: /^Starred/ });
    expect(started?.store.getState().route).toEqual({ kind: "view", view: "unread" });
    expect(screen.queryByRole("button", { name: "Save from your phone" })).toBeNull();
  });
});

describe("the desktop shell", () => {
  it("has no article source and no route to open, and opens a link with noopener", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const shell = createDesktopShell(window);
    expect(shell.savePages).toBe(false);
    expect(shell.contentSrc("item/3/abc", "dark")).toBeNull();
    shell.openRoute("/notes/a");
    expect(open).not.toHaveBeenCalled();
    shell.openOutside("https://example.com/a");
    expect(open).toHaveBeenCalledWith("https://example.com/a", "_blank", "noopener");
  });

  it("reads a fragment as a route", () => {
    expect(desktopRoute("#/view/starred")).toEqual({ kind: "view", view: "starred" });
    expect(desktopRoute("#/item/4")).toEqual({ kind: "item", id: 4 });
    expect(desktopRoute("")).toEqual({ kind: "view", view: "unread" });
    expect(desktopRoute("#/bookmarklet")).toEqual({ kind: "view", view: "unread" });
  });
});
