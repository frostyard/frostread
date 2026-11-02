// The desktop entry on the mock transport, with no lodge: the sources, the
// system's scheme, the fragment's routes, and no save pages.
import type { Transport } from "@frostyard/lodge-client/rpc";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BUDGET, BUDGET_WINDOW_MS, createDesktopShell, desktopRoute } from "./desktop/shell.js";
import { type Desktop, startDesktop } from "./desktop/start.js";
import { createMockTransport } from "./rpc/mock.js";

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

  it("keeps a view chosen while the transport opens", async () => {
    // The first list answer is held, so connect() is pending while the app
    // already takes clicks.
    const mock = createMockTransport();
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let first = true;
    const transport: Transport = {
      call: async <T,>(method: string, params: unknown): Promise<T> => {
        if (method === "items.list" && first) {
          first = false;
          await held;
        }
        return mock.call<T>(method, params);
      },
      onEvent: (handler) => mock.onEvent(handler),
      onClose: (handler) => mock.onClose(handler),
    };
    const before = window.history.length;
    const pending = startDesktop({ root, transport });
    fireEvent.click(await screen.findByRole("button", { name: /^Starred/ }));
    await waitFor(() => expect(window.location.hash).toBe("#/view/starred"));
    release();
    started = await pending;
    const store = started.store;
    expect(store.getState().route).toEqual({ kind: "view", view: "starred" });
    await waitFor(() => {
      const { items } = store.getState();
      expect(items.length).toBeGreaterThan(0);
      expect(items.every((item) => item.starred)).toBe(true);
    });
    expect(window.location.hash).toBe("#/view/starred");
    expect(window.history.length).toBe(before + 1);
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

/** ShellCall is one history call the fake window recorded. */
interface ShellCall {
  kind: "push" | "replace";
  hash: string;
  at: number;
}

/**
 * fakeShellWindow stands in for the window the shell writes to: the title, the
 * fragment, and a history that records its calls and throws a SecurityError
 * past `refuseAfter` calls in total, as WebKit does past its limit. Its timers
 * are the test's fake ones, so the budget's timer runs on the fake clock.
 */
function fakeShellWindow(options: { refuseAfter?: number } = {}) {
  const calls: ShellCall[] = [];
  const location = { hash: "" };
  const refuseAfter = options.refuseAfter ?? 100;
  let tried = 0;
  const record = (kind: "push" | "replace", hash: string) => {
    tried++;
    if (tried > refuseAfter) {
      throw new DOMException("Attempt to use history.pushState() more than 100 times per 10 seconds", "SecurityError");
    }
    calls.push({ kind, hash, at: Date.now() });
    location.hash = hash;
  };
  const win = {
    document: { title: "" },
    location,
    history: {
      pushState: (_state: unknown, _title: string, hash: string) => record("push", hash),
      replaceState: (_state: unknown, _title: string, hash: string) => record("replace", hash),
    },
    setTimeout: (fn: () => void, ms?: number) => setTimeout(fn, ms),
    clearTimeout: (id: number) => clearTimeout(id),
  };
  return { win: win as unknown as Window, calls, location };
}

describe("the desktop shell's history budget", () => {
  // The budget reads a clock, so it needs timers and Date on a clock the test
  // moves by hand.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("makes no more history calls than the budget in any 10 s, and ends at the last route", () => {
    const fake = fakeShellWindow();
    const shell = createDesktopShell(fake.win, () => Date.now());
    // About 50 item opens with no pause, each 10 ms after the one before.
    expect(() => {
      for (let i = 1; i <= 200; i++) {
        shell.reportRoute(`/item/${i}`, `Item ${i}`, false);
        vi.advanceTimersByTime(10);
      }
    }).not.toThrow();
    // No window of 10 s holds more calls than the budget allows.
    for (const call of fake.calls) {
      const inWindow = fake.calls.filter((other) => other.at >= call.at && other.at < call.at + BUDGET_WINDOW_MS);
      expect(inWindow.length).toBeLessThanOrEqual(BUDGET);
    }
    // Once the held route's timer runs, the address is the last route.
    vi.advanceTimersByTime(BUDGET_WINDOW_MS);
    expect(fake.location.hash).toBe("#/item/200");
    expect(fake.win.document.title).toBe("Item 200");
  });

  it("writes one entry for two routes within a second, and one each further apart", () => {
    const near = fakeShellWindow();
    const shell = createDesktopShell(near.win, () => Date.now());
    shell.reportRoute("/", "Unread", true);
    shell.reportRoute("/item/1", "Item 1", false);
    vi.advanceTimersByTime(100);
    shell.reportRoute("/item/2", "Item 2", false);
    // One push made the entry, and the report 100 ms later replaced it.
    expect(near.calls.map((call) => call.kind)).toEqual(["replace", "push", "replace"]);

    const far = fakeShellWindow();
    const other = createDesktopShell(far.win, () => Date.now());
    other.reportRoute("/", "Unread", true);
    other.reportRoute("/item/1", "Item 1", false);
    vi.advanceTimersByTime(2_000);
    other.reportRoute("/item/2", "Item 2", false);
    expect(far.calls.map((call) => call.kind)).toEqual(["replace", "push", "push"]);
  });

  it("writes the newest route when a late timer leaves room for it", () => {
    const fake = fakeShellWindow();
    const shell = createDesktopShell(fake.win, () => Date.now());
    // Fill the budget in one instant, so the report after it is held.
    for (let i = 1; i <= BUDGET; i++) shell.reportRoute(`/item/${i}`, `Item ${i}`, false);
    expect(fake.calls.length).toBe(BUDGET);
    shell.reportRoute("/view/starred", "Starred", false);
    expect(fake.calls.length).toBe(BUDGET);
    expect(fake.location.hash).not.toBe("#/view/starred");

    // The clock passes the oldest call's window while the held route's timer
    // still waits (a timer that runs late), so the next report has room.
    vi.setSystemTime(Date.now() + BUDGET_WINDOW_MS + 1);
    shell.reportRoute("/view/later", "Read later", false);
    expect(fake.calls.length).toBe(BUDGET + 1);
    expect(fake.location.hash).toBe("#/view/later");

    // The timer runs now: the older held route must not put the address back.
    vi.runAllTimers();
    expect(fake.location.hash).toBe("#/view/later");
    expect(fake.win.document.title).toBe("Read later");
    expect(fake.calls.filter((call) => call.hash === "#/view/starred")).toEqual([]);
  });

  it("keeps the address Back went to when a route is held", () => {
    const fake = fakeShellWindow();
    const shell = createDesktopShell(fake.win, () => Date.now());
    for (let i = 1; i <= BUDGET; i++) shell.reportRoute(`/item/${i}`, `Item ${i}`, false);
    expect(fake.calls.length).toBe(BUDGET);
    shell.reportRoute("/view/starred", "Starred", false);
    expect(fake.calls.length).toBe(BUDGET);

    // Back moves the fragment, and the app reports the route it brought.
    fake.location.hash = "#/view/all";
    shell.reportRoute("/view/all", "All", false);
    expect(fake.calls.length).toBe(BUDGET);

    // The held route's timer must not move the address away from Back's.
    vi.runAllTimers();
    expect(fake.location.hash).toBe("#/view/all");
    expect(fake.calls.length).toBe(BUDGET);
    expect(fake.calls.filter((call) => call.hash === "#/view/starred")).toEqual([]);
  });

  it("sets the title and warns once when the window refuses every call", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const fake = fakeShellWindow({ refuseAfter: 0 });
    const shell = createDesktopShell(fake.win, () => Date.now());
    expect(() => {
      for (let i = 1; i <= 60; i++) {
        shell.reportRoute(`/item/${i}`, `Item ${i}`, false);
        vi.advanceTimersByTime(500);
      }
    }).not.toThrow();
    expect(fake.win.document.title).toBe("Item 60");
    expect(warn).toHaveBeenCalledTimes(1);
    // The log holds no route and no title: both carry an article's words.
    const logged = warn.mock.calls.flat().map(String).join(" ");
    expect(logged).not.toContain("/item/");
    expect(logged).not.toContain("Item 60");
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
