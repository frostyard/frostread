// The production Tauri file adapter (ADR-0008, step 3.4).
import type { SaveFile } from "./shell.js";

/** tauriSaveFile adapts the Tauri invoke function to the app's file shell. */
export function tauriSaveFile(invoke: <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>): SaveFile {
  return (name, text) => invoke<boolean>("save_file", { name, text });
}
