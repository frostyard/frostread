// The desktop's unreachable banner and its quiet 10 second retry.
import { CodeNotConnected, RPCError, type Transport } from "@frostyard/lodge-client/rpc";
import { fireEvent, render, screen } from "@testing-library/react";
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
    get: async () => ({ lodge, localData: true }),
    save: async () => {},
    reload: () => {},
  };
}

describe("the connection banner", () => {
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

  it("retries reconnect every 10 seconds while offline", async () => {
    vi.useFakeTimers();
    let statusCalls = 0;
    const down = new RPCError(CodeNotConnected, "lodge refused");
    const transport: Transport = {
      call: async <T,>(method: string): Promise<T> => {
        if (method === "status") {
          statusCalls++;
          if (statusCalls === 1) throw down;
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

    await vi.advanceTimersByTimeAsync(10_000);
    expect(started.store.getState().offline).toBeNull();
    expect(statusCalls).toBeGreaterThanOrEqual(2);
  });
});
