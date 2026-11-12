// The desktop's unreachable banner and its quiet 10 second retry.
import { CodeNotConnected, RPCError, type Transport } from "@frostyard/lodge-client/rpc";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { ConnectionBanner } from "./desktop/ConnectionBanner.js";
import { type Desktop, startDesktop } from "./desktop/start.js";
import type { ServerControl } from "./shell.js";
import { createReadStore } from "./store.js";

const realMatchMedia = window.matchMedia;
let desktop: Desktop | null = null;
let root: HTMLElement;

beforeEach(() => {
  root = document.createElement("div");
  document.body.append(root);
  window.history.replaceState(null, "", "/");
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
});

afterEach(() => {
  desktop?.stop();
  desktop = null;
  root.remove();
  window.matchMedia = realMatchMedia;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function control(lodge: string): ServerControl {
  return {
    lodge,
    known: true,
    get: async () => ({ lodge, lastLodge: "", localData: true }),
    save: async () => {},
    reload: () => {},
  };
}

/** onlineTransport is one store connection that answers every start call. */
function onlineTransport(): Transport {
  return {
    call: async <T,>(method: string): Promise<T> => {
      if (method === "status") return { version: "1", notes: { configured: false, vaultId: 0, folder: "" } } as T;
      if (method === "counts")
        return { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] } as T;
      if (method === "sources.list") return { sources: [] } as T;
      if (method === "items.list") return { items: [] } as T;
      if (method === "events.subscribe") return { subscribed: true } as T;
      throw new Error(`unexpected ${method}`);
    },
    onEvent: () => () => {},
    onClose: () => () => {},
  };
}

describe("the connection banner", () => {
  it("reloads once after an unknown start mode becomes readable", async () => {
    const get = vi.fn(async () => ({ lodge: "", lastLodge: "", localData: true }));
    const reload = vi.fn();
    const server: ServerControl = { lodge: "", known: false, get, save: vi.fn(async () => {}), reload };

    desktop = await startDesktop({ root, transport: onlineTransport(), server });

    expect(get).toHaveBeenCalledOnce();
    expect(reload).toHaveBeenCalledOnce();
  });

  it("keeps unknown mode without reload when server_get rejects", async () => {
    const get = vi.fn(async () => {
      throw new Error("cannot read mode");
    });
    const reload = vi.fn();
    const server: ServerControl = { lodge: "", known: false, get, save: vi.fn(async () => {}), reload };

    desktop = await startDesktop({ root, transport: onlineTransport(), server });

    expect(get).toHaveBeenCalledOnce();
    expect(reload).not.toHaveBeenCalled();
  });

  it("reloads once when unknown mode becomes readable after a failed first connect", async () => {
    let up = false;
    const get = vi.fn(async () => ({ lodge: "", lastLodge: "", localData: true }));
    const reload = vi.fn();
    const server: ServerControl = { lodge: "", known: false, get, save: vi.fn(async () => {}), reload };
    const transport: Transport = {
      call: async <T,>(method: string): Promise<T> => {
        if (!up && method === "status") throw new RPCError(CodeNotConnected, "lodge refused");
        if (method === "status") return { version: "1", notes: { configured: false, vaultId: 0, folder: "" } } as T;
        if (method === "counts")
          return { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] } as T;
        if (method === "sources.list") return { sources: [] } as T;
        if (method === "items.list") return { items: [] } as T;
        if (method === "events.subscribe") return { subscribed: true } as T;
        throw new Error(`unexpected ${method}`);
      },
      onEvent: () => () => {},
      onClose: () => () => {},
    };

    desktop = await startDesktop({ root, transport, server });
    expect(desktop.store.getState().status).toBeNull();

    up = true;
    await desktop.store.getState().reconnect();

    await waitFor(() => expect(reload).toHaveBeenCalledOnce());
    expect(get).toHaveBeenCalledOnce();
  });

  it("does not reread a known mode at start", async () => {
    const get = vi.fn(async () => ({ lodge: "", lastLodge: "", localData: true }));
    const reload = vi.fn();
    const server: ServerControl = { lodge: "", known: true, get, save: vi.fn(async () => {}), reload };

    desktop = await startDesktop({ root, transport: onlineTransport(), server });

    expect(get).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  it("shows an idle close, reconnects after resubscribe, refreshes and hides", async () => {
    vi.useFakeTimers();
    const closes = new Set<(reason: string) => void>();
    const subscribeAnswers: ("ok" | "down")[] = ["ok", "ok"];
    let subscribeCalls = 0;
    let countsCalls = 0;
    const transport: Transport = {
      call: async <T,>(method: string): Promise<T> => {
        if (method === "events.subscribe") {
          subscribeCalls++;
          const answer = subscribeAnswers.shift() ?? "ok";
          if (answer === "down") throw new Error("not connected");
          return { subscribed: true } as T;
        }
        if (method === "status") return { version: "1", notes: { configured: false, vaultId: 0, folder: "" } } as T;
        if (method === "counts") {
          countsCalls++;
          return { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] } as T;
        }
        if (method === "sources.list") return { sources: [] } as T;
        if (method === "items.list") return { items: [] } as T;
        throw new Error(`unexpected ${method}`);
      },
      onEvent: () => () => {},
      onClose: (handler) => {
        closes.add(handler);
        return () => closes.delete(handler);
      },
    };

    const started = await startDesktop({ root, transport, server: control("https://lodge.example.ts.net") });
    desktop = started;
    const before = countsCalls;

    act(() => {
      for (const close of closes) close("the connection through lodge closed (1006)");
    });
    expect(started.store.getState().offline).toBe("the connection through lodge closed (1006)");

    expect(subscribeCalls).toBe(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(9_999);
    });
    expect(subscribeCalls).toBe(1);
    expect(countsCalls).toBe(before);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(subscribeCalls).toBe(2);
    expect(countsCalls).toBeGreaterThan(before);
    expect(started.store.getState().offline).toBeNull();
  });

  it("shows the Lodge and local texts and Try again reconnects", () => {
    const reconnect = vi.fn(async () => {});
    const store = createReadStore({ quietDisconnects: true });
    store.setState({ offline: "lodge refused", reconnect });
    const { rerender } = render(
      <StoreProvider store={store}>
        <ConnectionBanner server={control("https://lodge.example.ts.net")} />
      </StoreProvider>,
    );
    expect(screen.getByRole("alert").textContent).toContain(
      "Frostread cannot reach lodge at https://lodge.example.ts.net.",
    );
    expect(screen.getByText("lodge refused")).toBeTruthy();
    expect(screen.getByText("It tries again every 10 seconds.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(reconnect).toHaveBeenCalledOnce();

    rerender(
      <StoreProvider store={store}>
        <ConnectionBanner server={control("")} />
      </StoreProvider>,
    );
    expect(screen.getByRole("alert").textContent).toContain("Frostread cannot reach this computer's reader.");
  });

  it("retries every 10 seconds from a failed start, and clears the banner when one takes", async () => {
    vi.useFakeTimers();
    let up = false;
    let statusCalls = 0;
    const down = new RPCError(CodeNotConnected, "lodge refused");
    const transport: Transport = {
      call: async <T,>(method: string): Promise<T> => {
        if (!up) throw down;
        if (method === "status") {
          statusCalls++;
          return { version: "1", notes: { configured: false, vaultId: 0, folder: "" } } as T;
        }
        if (method === "counts")
          return { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] } as T;
        if (method === "sources.list") return { sources: [] } as T;
        if (method === "items.list") return { items: [] } as T;
        if (method === "events.subscribe") return { subscribed: true } as T;
        throw new Error(`unexpected ${method}`);
      },
      onEvent: () => () => {},
      onClose: () => () => {},
    };

    const started = await startDesktop({ root, transport, server: control("https://lodge.example.ts.net") });
    desktop = started;
    expect(started.store.getState().offline).toBe("lodge refused");

    up = true;
    await vi.advanceTimersByTimeAsync(9_999);
    expect(statusCalls).toBe(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(statusCalls).toBe(1);
    expect(started.store.getState().offline).toBeNull();
  });

  it("spaces its connection attempts 10 seconds apart after a close in the middle of an interval", async () => {
    vi.useFakeTimers();
    const closes = new Set<(reason: string) => void>();
    let up = true;
    const attempts: number[] = [];
    const transport: Transport = {
      call: async <T,>(method: string): Promise<T> => {
        if (!up) {
          attempts.push(Date.now());
          throw new RPCError(CodeNotConnected, "lodge closed");
        }
        if (method === "status") return { version: "1", notes: { configured: false, vaultId: 0, folder: "" } } as T;
        if (method === "counts")
          return { views: { unread: 0, starred: 0, later: 0, archive: 0, all: 0 }, sources: [] } as T;
        if (method === "sources.list") return { sources: [] } as T;
        if (method === "items.list") return { items: [] } as T;
        if (method === "events.subscribe") return { subscribed: true } as T;
        throw new Error(`unexpected ${method}`);
      },
      onEvent: () => () => {},
      onClose: (handler) => {
        closes.add(handler);
        return () => closes.delete(handler);
      },
    };

    desktop = await startDesktop({ root, transport, server: control("https://lodge.example.ts.net") });
    const start = Date.now();
    await vi.advanceTimersByTimeAsync(5_000);
    up = false;
    act(() => {
      for (const close of closes) close("the connection through lodge closed (1006)");
    });
    await vi.advanceTimersByTimeAsync(40_000);

    // Every attempt after the close is one subscribe, 10 s after the one
    // before it: no second timer runs on the start's own schedule.
    expect(attempts.map((t) => t - start)).toEqual([15_000, 25_000, 35_000, 45_000]);
  });
});
