// The save page, which the bookmarklet navigates to (ADR-0006, docs/specs/api.md,
// Web routes). It shows the page's title and address as text, and saves only
// when the owner taps Save: any site can open this route, so the confirm is
// what keeps a page from planting an item on its own.
import { useState } from "react";
import { UNREAD } from "../lib/routes.js";
import type { ItemSummary } from "../rpc/api.js";
import { useRead } from "./context.js";

/** hostOf names an address's host, or null when it does not parse. */
function hostOf(url: string): string | null {
  try {
    const host = new URL(url).hostname;
    return host === "" ? null : host;
  } catch {
    return null;
  }
}

/** SavePage asks before it saves, and then says where the item went. */
export function SavePage(props: { url: string; title: string }) {
  const saveFromPage = useRead((state) => state.saveFromPage);
  const setRoute = useRead((state) => state.setRoute);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [saved, setSaved] = useState<ItemSummary | null>(null);

  const cancel = () => void setRoute(UNREAD);

  if (props.url === "") {
    return (
      <section aria-label="Save to Read" className="flex h-full flex-col overflow-y-auto bg-window text-primary">
        <div className="mx-auto flex w-full max-w-xl flex-col gap-3 px-[var(--pad-x)] py-6">
          <h1 className="text-empty font-semibold">Save to Read</h1>
          <p className="text-reader-body">No page to save.</p>
          <div>
            <button type="button" onClick={cancel} className={secondary}>
              Back to Read
            </button>
          </div>
        </div>
      </section>
    );
  }

  const host = hostOf(props.url);

  const save = async () => {
    if (busy) return;
    setBusy(true);
    setFailure(null);
    try {
      setSaved(await saveFromPage(props.url, props.title));
    } catch (err) {
      // The button stays, so the owner can try again; the daemon's sentence
      // says why, such as an address inside the network.
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (saved !== null) {
    return (
      <section aria-label="Save to Read" className="flex h-full flex-col overflow-y-auto bg-window text-primary">
        <div className="mx-auto flex w-full max-w-xl flex-col gap-3 px-[var(--pad-x)] py-6">
          <h1 className="text-empty font-semibold">Saved</h1>
          <p className="text-reader-body font-medium">{saved.title}</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={() => void setRoute({ kind: "item", id: saved.id })} className={primary}>
              Open in Read
            </button>
            <button type="button" onClick={() => void setRoute({ kind: "view", view: "later" })} className={secondary}>
              Read later
            </button>
          </div>
          <p className="text-reader-meta text-secondary">Use your browser's Back button to return to the page.</p>
        </div>
      </section>
    );
  }

  return (
    <section aria-label="Save to Read" className="flex h-full flex-col overflow-y-auto bg-window text-primary">
      <div className="mx-auto flex w-full max-w-xl flex-col gap-3 px-[var(--pad-x)] py-6">
        <h1 className="text-empty font-semibold">Save to Read</h1>
        {props.title !== "" && <p className="text-reader-body font-medium break-words">{props.title}</p>}
        {host !== null && <p className="text-reader-meta text-secondary">{host}</p>}
        <p className="text-reader-meta break-all text-secondary">{props.url}</p>
        {failure !== null && (
          <p role="alert" className="text-sidebar-row text-flag-1">
            {failure}
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={() => void save()} disabled={busy} className={primary}>
            {busy ? "Saving…" : "Save"}
          </button>
          <button type="button" onClick={cancel} className={secondary}>
            Cancel
          </button>
        </div>
      </div>
    </section>
  );
}

/** primary is the page's main button, as a sheet's confirm is styled, but
 * 44 px high: the page is mostly used on a phone. */
const primary =
  "h-11 min-w-24 rounded-md bg-accent px-4 text-sidebar-row font-medium text-accent-contrast hover:opacity-90 disabled:opacity-40";

/** secondary is the page's other button, as a sheet's cancel is styled. */
const secondary =
  "h-11 min-w-24 rounded-md border border-separator px-4 text-sidebar-row text-primary hover:bg-selection-inactive";
