// The desktop's connection control (ADR-0008): Tauri reads and writes
// app.json, and reloads the window after a mode change.
import type { ServerControl, ServerInfo } from "../shell.js";

/** ServerInvoke is the Tauri invoke function the control calls. */
export type ServerInvoke = <T>(command: string, args?: Record<string, unknown>) => Promise<T>;

/** tauriServer calls the two server commands and reloads the real window. */
export function tauriServer(invoke: ServerInvoke, win: Window, lodge: string, known: boolean): ServerControl {
  return {
    lodge,
    known,
    get: () => invoke<ServerInfo>("server_get"),
    save: (next) => invoke<void>("server_save", { lodge: next }),
    reload: () => win.location.reload(),
  };
}

/** mockServer keeps one in-memory mode for the Vite desktop browser. */
export function mockServer(lodge = "", localData = true, known = true): ServerControl {
  let current = lodge;
  return {
    lodge,
    known,
    async get() {
      return { lodge: current, localData };
    },
    async save(next) {
      current = next;
    },
    reload() {},
  };
}
