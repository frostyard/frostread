// The desktop's transport (ADR-0008): JSON-RPC lines travel over the Rust
// shell's bridge (web/src-tauri/src/bridge.rs) to readd's private socket.
// The lines themselves are lodge-client's session, so the desktop and lodge
// speak one protocol; only the way a line travels differs.
import type { EventEnvelope, Transport } from "@frostyard/lodge-client/rpc";
import { CodeNotConnected, JsonRpcSession, RPCError } from "@frostyard/lodge-client/rpc";
import { Channel, invoke } from "@tauri-apps/api/core";
import { CLIENT, PROTOCOL } from "../api.js";

/** BridgeMessage is what the shell sends on its channel, in order. */
export type BridgeMessage = { kind: "line"; line: string } | { kind: "closed"; error: string };

/** BridgeChannel is the part of Tauri's Channel that the transport uses. */
export interface BridgeChannel {
  onmessage: ((message: BridgeMessage) => void) | null;
}

/** BridgeIPC is the shell's two commands and its channel; tests pass a fake. */
export interface BridgeIPC {
  /** invoke calls readd_connect or readd_send. */
  invoke<T>(command: string, args?: Record<string, unknown>): Promise<T>;
  /** channel makes one that hands each message to onMessage. */
  channel(onMessage: (message: BridgeMessage) => void): BridgeChannel;
}

/** tauriIPC is the shell's commands and channels, through Tauri's IPC. */
function tauriIPC(): BridgeIPC {
  return {
    invoke: <T>(command: string, args?: Record<string, unknown>): Promise<T> => invoke<T>(command, args),
    channel: (onMessage) => {
      const channel = new Channel<BridgeMessage>();
      channel.onmessage = onMessage;
      return channel;
    },
  };
}

/**
 * connectTauri is the Transport over the shell's bridge. Its event and close
 * handlers live over every connection: a connection's end tells them, and
 * the next call connects again, so the app sees one transport that mends
 * itself.
 */
export function connectTauri(ipc?: BridgeIPC): Transport {
  const bridge = ipc ?? tauriIPC();
  const eventHandlers = new Set<(event: EventEnvelope) => void>();
  const closeHandlers = new Set<(reason: string) => void>();
  let session: JsonRpcSession | undefined;
  let connecting: Promise<JsonRpcSession> | undefined;

  /** open makes one session, connects readd to it, and says hello. */
  const open = async (): Promise<JsonRpcSession> => {
    const next = new JsonRpcSession((line) => bridge.invoke("readd_send", { line }));
    next.onEvent((event) => {
      for (const handler of eventHandlers) handler(event);
    });
    next.onClose((reason) => {
      // The session is the connection: the next call connects again.
      if (session === next) session = undefined;
      for (const handler of closeHandlers) handler(reason);
    });
    const channel = bridge.channel((message) => {
      if (message.kind === "line") next.receive(message.line);
      else next.close(message.error);
    });
    await bridge.invoke("readd_connect", { onMessage: channel });
    // A new connection is a new session: readd answers every method but
    // rpc.hello with notReady until it has one (docs/specs/api.md). The
    // session is the caller's only after it, so no call goes before it.
    await next.call("rpc.hello", { protocol: PROTOCOL, client: CLIENT });
    session = next;
    return next;
  };

  /** connect runs one connect at a time, and answers the session it made. */
  const connect = (): Promise<JsonRpcSession> => {
    connecting ??= open().finally(() => {
      connecting = undefined;
    });
    return connecting;
  };

  return {
    async call<T>(method: string, params: unknown): Promise<T> {
      let up = session;
      if (up === undefined) {
        try {
          up = await connect();
        } catch (err) {
          // readd's own refusal of rpc.hello keeps its code, as lodge's
          // connection passes it; a socket that is not there is not connected.
          if (err instanceof RPCError) throw err;
          const reason = err instanceof Error ? err.message : String(err);
          throw new RPCError(CodeNotConnected, `readd is not reachable: ${reason}`);
        }
      }
      return up.call<T>(method, params);
    },
    onEvent(handler) {
      eventHandlers.add(handler);
      return () => eventHandlers.delete(handler);
    },
    onClose(handler) {
      closeHandlers.add(handler);
      return () => closeHandlers.delete(handler);
    },
  };
}
