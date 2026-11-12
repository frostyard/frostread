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

/** mockServer keeps one in-memory mode for the Vite desktop browser. It keeps
 * lastLodge as the shell does: a local save moves the address it leaves there,
 * and a lodge save clears it. */
export function mockServer(lodge = "", localData = true, known = true): ServerControl {
  let current = lodge;
  let last = "";
  return {
    lodge,
    known,
    async get() {
      return { lodge: current, lastLodge: last, localData };
    },
    async save(next) {
      if (next === "") {
        if (current !== "") last = current;
      } else {
        last = "";
      }
      current = next;
    },
    reload() {},
  };
}
