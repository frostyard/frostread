// The sidebar: the views with their counts, then the sources with theirs,
// and the buttons that add, import, export and refresh. A source that
// failed its last poll shows a warning, and its error in the tooltip.
import { IconButton, Sheet } from "@frostyard/lodge-ui/react";
import { AlertTriangle, ArrowLeft, Download, Ellipsis, Plus, RefreshCw, Upload } from "lucide-react";
import { type ChangeEvent, useRef, useState } from "react";
import { importMessage } from "../lib/importmessage.js";
import { BOOKMARKLET, type Route } from "../lib/routes.js";
import { type Source, VIEW_TITLES, VIEWS } from "../rpc/api.js";
import { useShell } from "../shell.js";
import { useRead } from "./context.js";
import { ServerSheet } from "./ServerSheet.js";
import { SourceSheet } from "./SourceSheet.js";
import { UrlSheet } from "./UrlSheet.js";

/** hostOf shows the lodge host in the compact sidebar button. */
export function hostOf(address: string): string {
  try {
    return new URL(address).host;
  } catch {
    return address;
  }
}

/** Sidebar is the left pane, or the narrow window's first pane. */
export function Sidebar(props: { onNavigate?: () => void; onBack?: () => void }) {
  const shell = useShell();
  const sources = useRead((state) => state.sources);
  const counts = useRead((state) => state.counts);
  const listRoute = useRead((state) => state.listRoute);
  const setRoute = useRead((state) => state.setRoute);
  const refresh = useRead((state) => state.refresh);
  const addSource = useRead((state) => state.addSource);
  const importOPML = useRead((state) => state.importOPML);
  const exportOPML = useRead((state) => state.exportOPML);
  const setError = useRead((state) => state.setError);
  const [adding, setAdding] = useState(false);
  const [managingId, setManagingId] = useState<number | null>(null);
  const [serverOpen, setServerOpen] = useState(false);
  const [importNotice, setImportNotice] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);

  const go = (route: Route) => {
    void setRoute(route);
    props.onNavigate?.();
  };

  const importFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = event.target.files?.[0];
    event.target.value = ""; // the same file may be chosen again
    if (chosen === undefined) return;
    const result = await importOPML(await chosen.text());
    if (result !== null) setImportNotice(importMessage(result));
  };

  const exportAll = async () => {
    const xml = await exportOPML();
    if (xml === "") return;
    try {
      await shell.saveFile("frostread.opml", xml, "text/x-opml");
    } catch (error) {
      setError(error);
    }
  };

  const unreadOf = (id: number) => counts.sources.find((row) => row.sourceId === id)?.unread ?? 0;
  // The sheet reads the freshest copy, so a poll or a rename shows in it.
  const managing = managingId === null ? null : (sources.find((source) => source.id === managingId) ?? null);

  return (
    <nav aria-label="Views and sources" className="flex h-full flex-col overflow-y-auto bg-sidebar">
      <div className="flex items-center justify-end gap-0.5 px-[var(--pad-x)] pb-1 pt-2">
        {props.onBack !== undefined && <IconButton label="Back" icon={ArrowLeft} onClick={props.onBack} />}
        <IconButton label="Add a source" icon={Plus} onClick={() => setAdding(true)} />
        <IconButton label="Import OPML" icon={Upload} onClick={() => file.current?.click()} />
        <input
          ref={file}
          type="file"
          accept=".opml,.xml,text/xml,application/xml"
          aria-label="OPML file"
          className="hidden"
          onChange={(event) => void importFile(event)}
        />
        <IconButton label="Export OPML" icon={Download} onClick={() => void exportAll()} />
        <IconButton label="Refresh all" icon={RefreshCw} onClick={() => void refresh()} />
      </div>

      <ul className="px-1">
        {VIEWS.map((view) => (
          <li key={view}>
            <Row
              title={VIEW_TITLES[view]}
              count={counts.views[view]}
              current={listRoute.kind === "view" && listRoute.view === view}
              onClick={() => go({ kind: "view", view })}
            />
          </li>
        ))}
      </ul>

      <h2 className="px-[var(--pad-x)] pb-1 pt-4 text-sidebar-section uppercase tracking-wide text-tertiary">
        Sources
      </h2>
      <ul className="px-1 pb-4">
        {sources.map((source) => (
          <li key={source.id} className="group relative">
            <SourceRow
              source={source}
              unread={unreadOf(source.id)}
              current={listRoute.kind === "source" && listRoute.id === source.id}
              onClick={() => go({ kind: "source", id: source.id })}
            />
            {/* The manage button shows on hover and on keyboard focus,
                and always where the pointer is coarse: a phone has no
                hover. */}
            <span className="pointer-events-none absolute right-0.5 top-1/2 -translate-y-1/2 opacity-0 transition-opacity group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [@media(pointer:coarse)]:pointer-events-auto [@media(pointer:coarse)]:opacity-100">
              <IconButton label={`Manage ${source.title}`} icon={Ellipsis} onClick={() => setManagingId(source.id)} />
            </span>
          </li>
        ))}
      </ul>

      {(shell.savePages || shell.server !== undefined) && (
        <div className="mt-auto border-t border-separator px-1 py-2">
          {shell.savePages && (
            <button
              type="button"
              onClick={() => go(BOOKMARKLET)}
              className="flex h-7 w-full items-center gap-2 rounded-md px-2 text-left text-sidebar-row text-secondary hover:bg-selection-inactive"
            >
              <span className="min-w-0 flex-1 truncate">Save from your phone</span>
            </button>
          )}
          {shell.server !== undefined && (
            <button
              type="button"
              onClick={() => setServerOpen(true)}
              className="flex h-7 w-full items-center gap-2 rounded-md px-2 text-left text-sidebar-row text-secondary hover:bg-selection-inactive"
            >
              <span className="min-w-0 flex-1 truncate">
                {shell.server.lodge === "" ? "Connect to lodge…" : `Lodge: ${hostOf(shell.server.lodge)}`}
              </span>
            </button>
          )}
        </div>
      )}

      {serverOpen && shell.server !== undefined && (
        <ServerSheet server={shell.server} onClose={() => setServerOpen(false)} />
      )}

      {importNotice !== null && (
        <Sheet title="Import OPML" onClose={() => setImportNotice(null)}>
          <p className="text-reader-body text-primary">{importNotice}</p>
          <div className="mt-4 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setImportNotice(null)}
              className="h-7 rounded-md bg-accent px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90"
            >
              OK
            </button>
          </div>
        </Sheet>
      )}

      {managing !== null && <SourceSheet source={managing} onClose={() => setManagingId(null)} />}

      {adding && (
        <UrlSheet
          title="Add a source"
          label="Feed or site address"
          placeholder="https://example.com/feed.xml"
          submitLabel="Add"
          busyLabel="Adding…"
          onSubmit={addSource}
          onClose={() => setAdding(false)}
        />
      )}
    </nav>
  );
}

/** Row is one view: its name, and its count when it has one. */
function Row(props: { title: string; count: number; current: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-current={props.current ? "page" : undefined}
      onClick={props.onClick}
      className={`flex h-7 w-full items-center gap-2 rounded-md px-2 text-left text-sidebar-row ${
        props.current ? "bg-selection-sidebar text-primary" : "text-secondary hover:bg-selection-inactive"
      }`}
    >
      <span className="min-w-0 flex-1 truncate">{props.title}</span>
      {props.count > 0 && <span className="text-sidebar-section tabular-nums text-tertiary">{props.count}</span>}
    </button>
  );
}

/** SourceRow is one source: its title, its unread count, and its last error. */
export function SourceRow(props: { source: Source; unread: number; current: boolean; onClick: () => void }) {
  const lastError = props.source.lastError ?? "";
  return (
    <button
      type="button"
      aria-current={props.current ? "page" : undefined}
      onClick={props.onClick}
      title={lastError === "" ? props.source.url : lastError}
      className={`flex h-7 w-full items-center gap-2 rounded-md px-2 text-left text-sidebar-row ${
        props.current ? "bg-selection-sidebar text-primary" : "text-secondary hover:bg-selection-inactive"
      }`}
    >
      <span className="min-w-0 flex-1 truncate">{props.source.title}</span>
      {lastError !== "" && (
        <span role="img" className="shrink-0 text-flag-2" aria-label={`Last error: ${lastError}`}>
          <AlertTriangle size={12} aria-hidden="true" />
        </span>
      )}
      {props.unread > 0 && <span className="text-sidebar-section tabular-nums text-tertiary">{props.unread}</span>}
    </button>
  );
}
