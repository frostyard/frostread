// The Tauri transport against a fake IPC: a line goes out and its answer
// comes back, an event reaches its handler, a connection's end fails what
// waits on it, and the next call connects again.
import type { EventEnvelope, Transport } from "@frostyard/lodge-client/rpc";
import { CodeNotConnected, RPCError } from "@frostyard/lodge-client/rpc";
import { describe, expect, it, vi } from "vitest";
import { ITEMS_CHANGED, watchChanges } from "../events.js";
import { type BridgeChannel, type BridgeIPC, type BridgeMessage, connectTauri } from "./tauri.js";

/** Request is one line the page sent, as the shell reads it. */
interface Request {
  jsonrpc: string;
  id: number;
  method: string;
  params: unknown;
}

/** FakeBridge is the shell's two commands and its channel, in memory. */
class FakeBridge implements BridgeIPC {
  /** The lines the page sent, in order. */
  readonly sent: string[] = [];
  /** How many times the page connected. */
  connects = 0;
  /** Whether the next connect fails, as a reader that is not running does. */
  refuses = false;
  /** Whether a greeting is answered at once, as readd does. */
  greets = true;
  private listener: ((message: BridgeMessage) => void) | undefined;

  invoke<T>(command: string, args?: Record<string, unknown>): Promise<T> {
    if (command === "readd_connect") {
      this.connects += 1;
      if (this.refuses) return Promise.reject(new Error("connect to readd: no such file"));
      return Promise.resolve(undefined as T);
    }
    if (command !== "readd_send") return Promise.reject(new Error(`unknown command ${command}`));
    this.sent.push(String(args?.line));
    // readd answers a greeting at once, as it does on the socket.
    const last = this.sent.length - 1;
    if (this.greets && this.request(last).method === "rpc.hello") {
      this.answer(last, { protocol: 1, client: "read-test" });
    }
    return Promise.resolve(undefined as T);
  }

  channel(onMessage: (message: BridgeMessage) => void): BridgeChannel {
    this.listener = onMessage;
    return { onmessage: onMessage };
  }

  /** methods are the methods the page called, in order. */
  methods(): string[] {
    return this.sent.map((line) => (JSON.parse(line) as Request).method);
  }

  /** request is the n-th line the page sent. */
  request(index: number): Request {
    return JSON.parse(this.sent[index] ?? "{}") as Request;
  }

  /** indexOf is the n-th line the page sent for one method. */
  indexOf(method: string): number {
    return this.methods().indexOf(method);
  }

  /** answer answers the n-th line the page sent. */
  answer(index: number, result: unknown): void {
    const id = this.request(index).id;
    this.receive({ kind: "line", line: JSON.stringify({ jsonrpc: "2.0", id, result }) });
  }

  /** receive hands one message to the page, as the shell's channel does. */
  receive(message: BridgeMessage): void {
    this.listener?.(message);
  }

  /** line hands the page one more line from readd. */
  line(body: unknown): void {
    this.receive({ kind: "line", line: JSON.stringify(body) });
  }
}

/** Connected is a transport with one call out, so its session is up. */
interface Connected {
  bridge: FakeBridge;
  transport: Transport;
  call: Promise<unknown>;
}

/** connected starts a transport and makes its first call after the greeting. */
async function connected(): Promise<Connected> {
  const bridge = new FakeBridge();
  const transport = connectTauri(bridge);
  const call = transport.call<unknown>("status", {});
  await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello", "status"]));
  return { bridge, transport, call };
}

describe("connectTauri", () => {
  it("says hello, then sends a call as one line and answers it", async () => {
    const bridge = new FakeBridge();
    const transport = connectTauri(bridge);
    const call = transport.call<{ build: string }>("status", {});

    await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello", "status"]));
    expect(bridge.connects).toBe(1);
    expect(bridge.request(0).params).toEqual({ protocol: 1, client: "read-web" });
    expect(bridge.request(1)).toEqual({
      jsonrpc: "2.0",
      id: 2,
      method: "status",
      params: {},
    });

    bridge.answer(1, { build: "abc" });
    await expect(call).resolves.toEqual({ build: "abc" });
  });

  it("hands an event to onEvent", async () => {
    const { bridge, transport, call } = await connected();
    const events: EventEnvelope[] = [];
    transport.onEvent((event) => events.push(event));

    const data = { views: ["unread"] };
    bridge.line({ jsonrpc: "2.0", method: "event", params: { event: ITEMS_CHANGED, data } });

    expect(events).toEqual([{ event: ITEMS_CHANGED, data }]);
    bridge.answer(bridge.indexOf("status"), {});
    await call;
  });

  it("sends no call before the greeting is answered", async () => {
    const bridge = new FakeBridge();
    bridge.greets = false;
    const transport = connectTauri(bridge);
    const status = transport.call("status", {});
    const counts = transport.call("counts", {});

    await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello"]));
    bridge.answer(bridge.indexOf("rpc.hello"), { protocol: 1, client: "read-test" });
    await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello", "status", "counts"]));
    expect(bridge.connects).toBe(1);

    bridge.answer(bridge.indexOf("status"), {});
    bridge.answer(bridge.indexOf("counts"), { unread: 0 });
    await expect(status).resolves.toEqual({});
    await expect(counts).resolves.toEqual({ unread: 0 });
  });

  it("fails the pending call and tells onClose when the connection ends", async () => {
    const { bridge, transport, call } = await connected();
    const closes: string[] = [];
    transport.onClose((reason) => closes.push(reason));

    bridge.receive({ kind: "closed", error: "readd closed the connection" });

    await expect(call).rejects.toMatchObject({ code: CodeNotConnected });
    expect(closes).toEqual(["readd closed the connection"]);
  });

  it("connects again for the next call", async () => {
    const { bridge, transport, call } = await connected();
    bridge.answer(bridge.indexOf("status"), {});
    await call;

    bridge.receive({ kind: "closed", error: "readd closed the connection" });
    const next = transport.call<{ unread: number }>("counts", {});
    await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello", "status", "rpc.hello", "counts"]));
    expect(bridge.connects).toBe(2);

    bridge.answer(bridge.indexOf("counts"), { unread: 7 });
    await expect(next).resolves.toEqual({ unread: 7 });
  });

  it("refuses the call, not connected, when readd does not answer", async () => {
    const bridge = new FakeBridge();
    bridge.refuses = true;
    const transport = connectTauri(bridge);

    const failure = await transport.call("status", {}).catch((err: unknown) => err);
    expect(failure).toBeInstanceOf(RPCError);
    expect((failure as RPCError).code).toBe(CodeNotConnected);
    expect((failure as RPCError).message).toContain("no such file");
    expect(bridge.connects).toBe(1);
    expect(bridge.sent).toEqual([]);
  });
});

describe("watchChanges over the Tauri transport", () => {
  it("subscribes again after a connection ends", async () => {
    const bridge = new FakeBridge();
    const transport = connectTauri(bridge);
    let changes = 0;
    const stop = watchChanges(transport, () => changes++);

    await vi.waitFor(() => expect(bridge.methods()).toEqual(["rpc.hello", "events.subscribe"]));
    bridge.answer(bridge.indexOf("events.subscribe"), { subscribed: true });

    bridge.receive({ kind: "closed", error: "readd closed the connection" });
    await vi.waitFor(() =>
      expect(bridge.methods()).toEqual(["rpc.hello", "events.subscribe", "rpc.hello", "events.subscribe"]),
    );
    expect(bridge.connects).toBe(2);
    expect(changes).toBe(0);
    stop();
  });
});
