// docs/specs/api.md, opml.import: report both counts so skipped feeds do
// not disappear without an explanation (https://github.com/frostyard/frostread/issues/64).
import type { ImportResult } from "../rpc/api.js";

/** importMessage describes an import, including a file with no feeds. */
export function importMessage(result: ImportResult): string {
  const imported =
    result.added > 0 ? `Imported ${result.added} ${result.added === 1 ? "feed" : "feeds"}.` : "No feeds imported.";
  if (result.skipped > 0) {
    return `${imported} Skipped ${result.skipped}: already a source, or an address Read cannot reach.`;
  }
  return result.added > 0 ? imported : `${imported} The file names no feeds.`;
}
