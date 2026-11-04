// The desktop app's start (ADR-0008): the standalone Flatpak's window, with
// its own readd. The lodge entry is main.tsx.
import "./app.css";
import type { Transport } from "@frostyard/lodge-client/rpc";
import type { SaveFile } from "./desktop/shell.js";
import { startDesktop } from "./desktop/start.js";
import { downloadFile } from "./lib/download.js";

const root = document.getElementById("root");
if (root === null) throw new Error("read-web: #root is missing");

/**
 * connectDesktop chooses the shell's bridge and file saver: Tauri invokes
 * save_file, while a Vite browser keeps the existing download. Both imports
 * are lazy, so the mock and the Tauri shell stay out of each other's path.
 */
async function connectDesktop(): Promise<{ transport: Transport; saveFile: SaveFile }> {
  const core = await import("@tauri-apps/api/core");
  if (!core.isTauri()) {
    const mock = await import("./rpc/mock.js");
    return {
      transport: mock.createMockTransport(),
      saveFile: async (name, text, type) => {
        downloadFile(name, text, type);
        return true;
      },
    };
  }
  const { connectTauri } = await import("./rpc/transport/tauri.js");
  return {
    transport: connectTauri(),
    saveFile: (name, text) => core.invoke<boolean>("save_file", { name, text }),
  };
}

const desktop = await connectDesktop();
await startDesktop({ root, transport: desktop.transport, saveFile: desktop.saveFile });
