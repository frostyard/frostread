// The desktop app's start (ADR-0008): the standalone Flatpak's window, with
// its own readd. The lodge entry is main.tsx.
import "./app.css";
import type { Transport } from "@frostyard/lodge-client/rpc";
import { startDesktop } from "./desktop/start.js";

const root = document.getElementById("root");
if (root === null) throw new Error("read-web: #root is missing");

/**
 * connectTransport is the shell's bridge when Tauri is there, and the mock
 * elsewhere, such as a browser on Vite's development server. Both imports
 * are lazy, so neither the mock nor the shell's code sits in the other's
 * path.
 */
async function connectTransport(): Promise<Transport> {
  const { isTauri } = await import("@tauri-apps/api/core");
  if (!isTauri()) return (await import("./rpc/mock.js")).createMockTransport();
  const { connectTauri } = await import("./rpc/transport/tauri.js");
  return connectTauri();
}

await startDesktop({ root, transport: await connectTransport() });
