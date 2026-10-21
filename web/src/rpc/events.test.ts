// readd's events over lodge-client's rpc subpath: watchChanges subscribes
// now and after every reconnect, and hands on only items.changed
// (@frostyard/lodge-client/rpc, docs/specs/api.md, Notifications).

import type { EventEnvelope, Transport } from "@frostyard/lodge-client/rpc";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ITEMS_CHANGED, watchChanges } from "./events.js";

/** fake is a Transport whose calls the test answers. */
function fake() {
  const calls: string[] = [];
  const answers: ("ok" | "down")[] = [];
  const events = new Set<(event: EventEnvelope) => void>();
  const closes = new Set<(reason: string) => void>();
  const transport: Transport = {
    call: <T>(method: string, _params: unknown): Promise<T> => {
      calls.push(method);
      const answer = answers.shift() ?? "ok";
      return answer === "ok" ? Promise.resolve({ subscribed: true } as T) : Promise.reject(new Error("not connected"));
    },
    onEvent: (handler) => {
      events.add(handler);
      return () => events.delete(handler);
    },
    onClose: (handler) => {
      closes.add(handler);
      return () => closes.delete(handler);
    },
  };
  return {
    transport,
    calls,
    answers,
    emit: (event: EventEnvelope) => {
      for (const handler of events) handler(event);
    },
    close: (reason: string) => {
      for (const handler of closes) handler(reason);
    },
  };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("watchChanges", () => {
  it("subscribes at once, and hands on items.changed only", () => {
    const f = fake();
    let changes = 0;
    const stop = watchChanges(f.transport, () => changes++);
    expect(f.calls).toEqual(["events.subscribe"]);

    f.emit({ event: "theme", data: {} });
    expect(changes).toBe(0);
    f.emit({ event: ITEMS_CHANGED, data: { views: ["unread"] } });
    expect(changes).toBe(1);

    stop();
    f.emit({ event: ITEMS_CHANGED, data: {} });
    expect(changes).toBe(1);
  });

  it("subscribes again after a drop, retrying until the socket is up", async () => {
    vi.useFakeTimers();
    const f = fake();
    let changes = 0;
    watchChanges(f.transport, () => changes++);
    expect(f.calls).toHaveLength(1);

    // The drop: the first subscribe fails, and the retry is asked again.
    f.answers.push("down", "down", "ok");
    f.close("the connection through lodge closed (1006)");
    expect(f.calls).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(f.calls).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(f.calls).toHaveLength(4);

    // The retries stop once a subscribe takes.
    await vi.advanceTimersByTimeAsync(10_000);
    expect(f.calls).toHaveLength(4);
    f.emit({ event: ITEMS_CHANGED, data: {} });
    expect(changes).toBe(1);
  });
});
