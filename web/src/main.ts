// Read's web bundle, in a frame of lodge's shell. It connects to the
// shell with lodge-client, then to readd's API at /app/read/api.
import { connectLodge } from "@frostyard/lodge-client";

const lodge = await connectLodge({ app: "read" });
const status = document.getElementById("status") as HTMLElement;

// Follow the shell's theme rather than prefers-color-scheme.
document.documentElement.dataset.theme = lodge.theme;
lodge.on("theme", ({ scheme }) => {
  document.documentElement.dataset.theme = scheme;
});

// Report the route the frame opened at, and follow the shell's navigation.
lodge.route(lodge.history.location.pathname, "", { replace: true });
lodge.on("navigate", ({ route }) => lodge.route(route, "", { replace: true }));

// The API: JSON-RPC 2.0, one message per WebSocket frame, rpc.hello first.
let ws: WebSocket | null = null;
let next = 0;

function send(method: string, params: object = {}): void {
  ws?.send(`${JSON.stringify({ jsonrpc: "2.0", id: ++next, method, params })}\n`);
}

function connect(): void {
  ws = new WebSocket(lodge.apiURL());
  ws.onopen = () => send("rpc.hello", { protocol: 1, client: "read-web" });
  ws.onmessage = (e) => {
    for (const line of String(e.data).split("\n")) {
      if (line === "") continue;
      const m = JSON.parse(line) as { result?: { server?: string }; error?: { message: string } };
      if (m.error) status.textContent = m.error.message;
      else if (m.result?.server) status.textContent = `Connected to ${m.result.server}`;
    }
  };
  ws.onclose = () => {
    ws = null;
    status.textContent = "Disconnected";
  };
}
connect();

// lodge closes a session after 90 s without a frame: send one every 30 s
// while the page is visible, and reconnect when it is visible again
// (lodge's app-contract.md, Sessions).
setInterval(() => {
  if (document.visibilityState === "visible" && ws?.readyState === WebSocket.OPEN) send("rpc.ping");
}, 30_000);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && ws === null) connect();
});
