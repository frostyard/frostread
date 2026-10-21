// One source, managed: its title, its addresses, its poll times and its
// error, with Refresh now and Unsubscribe (docs/specs/api.md,
// sources.rename, sources.refresh and sources.remove).
import { Sheet } from "@frostyard/lodge-ui/react";
import { useState } from "react";
import { headerDate } from "../lib/format.js";
import type { Source } from "../rpc/api.js";
import { useRead } from "./context.js";

/** SourceSheet manages one source. */
export function SourceSheet(props: { source: Source; onClose: () => void }) {
  const renameSource = useRead((state) => state.renameSource);
  const removeSource = useRead((state) => state.removeSource);
  const refresh = useRead((state) => state.refresh);
  const [title, setTitle] = useState(props.source.title);
  const [busy, setBusy] = useState<"rename" | "refresh" | "remove" | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  const run = async (what: "rename" | "refresh" | "remove", action: () => Promise<void>) => {
    setBusy(what);
    setFailure(null);
    try {
      await action();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const source = props.source;
  const rename = () => {
    const next = title.trim();
    if (next === "" || next === source.title) return;
    void run("rename", () => renameSource(source.id, next));
  };
  const unsubscribe = () =>
    void run("remove", async () => {
      await removeSource(source.id);
      props.onClose();
    });
  const polled = headerDate(source.polledAt ?? "") || "Never";
  const next = headerDate(source.nextPollAt ?? "") || "Not scheduled";
  const site = source.siteUrl ?? "";

  return (
    <Sheet title={source.title} onClose={props.onClose}>
      <div className="flex flex-col gap-4 text-reader-body text-primary">
        <label className="flex flex-col gap-1 text-sidebar-row text-secondary">
          Title
          <div className="flex gap-2">
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  rename();
                }
              }}
              aria-label="Source title"
              className="h-8 min-w-0 flex-1 rounded-md border border-separator bg-window px-2 text-primary outline-none focus-visible:border-accent"
            />
            <button
              type="button"
              aria-label="Rename"
              onClick={rename}
              disabled={busy !== null || title.trim() === "" || title.trim() === source.title}
              className="h-8 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive disabled:opacity-40"
            >
              {busy === "rename" ? "Renaming…" : "Rename"}
            </button>
          </div>
        </label>

        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sidebar-row">
          <dt className="text-secondary">Feed</dt>
          <dd className="min-w-0 truncate">
            <a href={source.url} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
              {source.url}
            </a>
          </dd>
          <dt className="text-secondary">Site</dt>
          <dd className="min-w-0 truncate">
            {site === "" ? (
              <span className="text-tertiary">None</span>
            ) : (
              <a href={site} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
                {site}
              </a>
            )}
          </dd>
          <dt className="text-secondary">Last poll</dt>
          <dd>{polled}</dd>
          <dt className="text-secondary">Next poll</dt>
          <dd>{next}</dd>
          <dt className="text-secondary">Failed polls</dt>
          <dd>{source.errorCount ?? 0}</dd>
        </dl>

        {(source.lastError ?? "") !== "" && (
          <p role="alert" className="text-sidebar-row text-flag-1">
            Last error: {source.lastError}
          </p>
        )}
        {failure !== null && (
          <p role="alert" className="text-sidebar-row text-flag-1">
            {failure}
          </p>
        )}

        {confirming ? (
          <div className="flex flex-col gap-2 rounded-md border border-separator p-3">
            {/* sources.remove deletes the source and every item of it, read
                or not (store.RemoveSource's cascade), not only the unread
                ones. */}
            <p className="text-sidebar-row text-primary">Unsubscribe from {source.title}? All of its items go too.</p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirming(false)}
                className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive"
              >
                Keep
              </button>
              <button
                type="button"
                onClick={unsubscribe}
                disabled={busy !== null}
                className="h-7 rounded-md bg-flag-1 px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90 disabled:opacity-40"
              >
                {busy === "remove" ? "Unsubscribing…" : "Unsubscribe"}
              </button>
            </div>
          </div>
        ) : (
          <div className="flex flex-wrap justify-end gap-2">
            <button
              type="button"
              onClick={() => void run("refresh", () => refresh(source.id))}
              disabled={busy !== null}
              className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive disabled:opacity-40"
            >
              {busy === "refresh" ? "Refreshing…" : "Refresh now"}
            </button>
            <button
              type="button"
              onClick={() => setConfirming(true)}
              disabled={busy !== null}
              className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-flag-1 hover:bg-selection-inactive disabled:opacity-40"
            >
              Unsubscribe
            </button>
            <button
              type="button"
              onClick={props.onClose}
              className="h-7 rounded-md bg-accent px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90"
            >
              Close
            </button>
          </div>
        )}
      </div>
    </Sheet>
  );
}
