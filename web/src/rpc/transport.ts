// The line-level contract between the typed API (api.ts) and a connection
// to readd. JSON-RPC multiplexing happens here, in the browser
// (docs/specs/api.md, JSON-RPC API). Copied from frostmail's
// app/src/rpc/transport.ts at commit 4328cb7 (2026-10-21), with readd's
// notification type in place of mail's.
import type { ItemsChanged } from "./api.js";

export interface Transport {
  call<T>(method: string, params: unknown): Promise<T>;
  /** Subscribe to readd's notifications; returns an unsubscribe function. */
  onEvent(handler: (event: ItemsChanged) => void): () => void;
  /** Called once when the connection ends. */
  onClose(handler: (reason: string) => void): () => void;
}

/** A JSON-RPC error returned by readd; code is one of the API's ErrorCode values. */
export class RPCError extends Error {
  readonly code: number;
  readonly data: unknown;

  constructor(code: number, message: string, data?: unknown) {
    super(message);
    this.name = "RPCError";
    this.code = code;
    this.data = data;
  }
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (reason: Error) => void;
}

interface Incoming {
  id?: number | string | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

/** EVENT_METHOD is the kit's notification method: its params wrap the event (docs/specs/api.md). */
export const EVENT_METHOD = "event";

/** ITEMS_CHANGED is the event readd sends after any change. */
export const ITEMS_CHANGED = "items.changed";

/** EventEnvelope is the params of an event notification. */
interface EventEnvelope {
  seq?: number;
  event: string;
  data?: unknown;
}

/**
 * JsonRpcSession implements Transport over any line channel: the caller
 * supplies how to send a line and feeds received lines to receive().
 */
export class JsonRpcSession implements Transport {
  private nextId = 0;
  private readonly pending = new Map<number, Pending>();
  private readonly eventHandlers = new Set<(event: ItemsChanged) => void>();
  private readonly closeHandlers = new Set<(reason: string) => void>();
  private closedReason: string | undefined;
  private readonly sendLine: (line: string) => Promise<void>;

  constructor(sendLine: (line: string) => Promise<void>) {
    this.sendLine = sendLine;
  }

  call<T>(method: string, params: unknown): Promise<T> {
    if (this.closedReason !== undefined) {
      return Promise.reject(new Error(`readd connection closed: ${this.closedReason}`));
    }
    const id = ++this.nextId;
    const line = JSON.stringify({ jsonrpc: "2.0", id, method, params: params ?? {} });
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      this.sendLine(line).catch((err: unknown) => {
        this.pending.delete(id);
        reject(err instanceof Error ? err : new Error(String(err)));
      });
    });
  }

  onEvent(handler: (event: ItemsChanged) => void): () => void {
    this.eventHandlers.add(handler);
    return () => this.eventHandlers.delete(handler);
  }

  onClose(handler: (reason: string) => void): () => void {
    if (this.closedReason !== undefined) {
      handler(this.closedReason);
      return () => {};
    }
    this.closeHandlers.add(handler);
    return () => this.closeHandlers.delete(handler);
  }

  /** Feed one line received from readd. */
  receive(line: string): void {
    let msg: Incoming;
    try {
      msg = JSON.parse(line) as Incoming;
    } catch {
      this.close(`malformed message from readd: ${line.slice(0, 120)}`);
      return;
    }
    if (msg.id === undefined || msg.id === null) {
      if (msg.method === EVENT_METHOD && msg.params) {
        const envelope = msg.params as EventEnvelope;
        if (envelope.event !== ITEMS_CHANGED) return;
        const data = envelope.data as ItemsChanged | undefined;
        const event: ItemsChanged = { views: Array.isArray(data?.views) ? data.views : [] };
        for (const h of this.eventHandlers) h(event);
      }
      return;
    }
    if (typeof msg.id !== "number") return;
    const p = this.pending.get(msg.id);
    if (!p) return;
    this.pending.delete(msg.id);
    if (msg.error) p.reject(new RPCError(msg.error.code, msg.error.message, msg.error.data));
    else p.resolve(msg.result ?? null);
  }

  /** Fail pending calls and notify close handlers; idempotent. */
  close(reason: string): void {
    if (this.closedReason !== undefined) return;
    this.closedReason = reason;
    for (const p of this.pending.values()) p.reject(new Error(`readd connection closed: ${reason}`));
    this.pending.clear();
    for (const h of this.closeHandlers) h(reason);
    this.closeHandlers.clear();
  }
}
