// The lodge transport: readd's API over lodge's /app/read/api WebSocket,
// one JSON-RPC message per text frame (docs/specs/api.md, JSON-RPC API).
// The connection starts with rpc.hello, as the scaffold's contract says;
// lodge closes a session after 90 s without a frame from the browser, so
// this sends rpc.ping every 30 s while the page is visible, and the socket
// opens only while it is. Adapted from frostmail's
// app/src/rpc/transport/lodge.ts at commit 4328cb7 (2026-10-21).
import { JsonRpcSession, type Transport } from "./transport.js";

/** PROTOCOL is the API major version readd speaks. */
export const PROTOCOL = 1;

/** CLIENT names this build in rpc.hello, for readd's logs. */
export const CLIENT = "read-web";

/** PING_MS is how often a visible page pings readd through lodge. */
export const PING_MS = 30_000;

/** whenVisible resolves once the page is visible. */
export function whenVisible(doc: Document = document): Promise<void> {
  if (doc.visibilityState === "visible") return Promise.resolve();
  return new Promise((resolve) => {
    const on = () => {
      if (doc.visibilityState !== "visible") return;
      doc.removeEventListener("visibilitychange", on);
      resolve();
    };
    doc.addEventListener("visibilitychange", on);
  });
}

/** connectLodgeAPI opens readd's API at url (lodge.apiURL()) once the page is visible, and readies it. */
export async function connectLodgeAPI(url: string, doc: Document = document): Promise<Transport> {
  await whenVisible(doc);
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    const session = new JsonRpcSession(async (line) => ws.send(line));
    let open = false;
    let timer: ReturnType<typeof setInterval> | undefined;
    const stop = () => {
      clearInterval(timer);
      doc.removeEventListener("visibilitychange", onVisibility);
    };
    const ping = () => {
      if (doc.visibilityState === "visible") void session.call("rpc.ping", {}).catch(() => {});
    };
    const onVisibility = () => {
      if (doc.visibilityState === "visible") ping();
    };
    ws.onmessage = (e) => {
      const line = String(e.data).replace(/\n$/, "");
      if (line !== "") session.receive(line);
    };
    ws.onopen = () => {
      open = true;
      // readd answers every method but rpc.hello with notReady until it
      // has succeeded, so the connection is ready only after this.
      session.call("rpc.hello", { protocol: PROTOCOL, client: CLIENT }).then(
        () => {
          timer = setInterval(ping, PING_MS);
          doc.addEventListener("visibilitychange", onVisibility);
          resolve(session);
        },
        (err: unknown) => {
          ws.close();
          reject(err instanceof Error ? err : new Error(String(err)));
        },
      );
    };
    ws.onclose = (e) => {
      stop();
      if (!open) reject(new Error(`cannot reach readd through lodge (${e.code})`));
      session.close(`lodge closed the connection (${e.code})`);
    };
  });
}
