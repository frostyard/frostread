// The app: the sidebar, the list and the reader, with a splitter between
// them; one pane at a time below 600px (lodge-ui's useNarrow); the keys;
// and the error Sheet.
import { Sheet, Splitter, useNarrow } from "@frostyard/lodge-ui/react";
import { useEffect, useRef, useState } from "react";
import { commandFor } from "../lib/keys.js";
import { formatRoute, routeTitle } from "../lib/routes.js";
import { openOutside, reportRoute } from "../lodge.js";
import { useRead, useReadStore } from "./context.js";
import { ListPane } from "./ListPane.js";
import { Reader } from "./Reader.js";
import { Sidebar } from "./Sidebar.js";

/** MIN and MAX bound a pane's width in pixels while it is dragged. */
const MIN = 180;
const MAX = 420;

/** App is the whole frame. */
export function App() {
  const store = useReadStore();
  const route = useRead((state) => state.route);
  const sources = useRead((state) => state.sources);
  const item = useRead((state) => state.item);
  const selectedId = useRead((state) => state.selectedId);
  const error = useRead((state) => state.error);
  const clearError = useRead((state) => state.clearError);
  const narrow = useNarrow();

  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(224);
  const [listWidth, setListWidth] = useState(320);
  const drag = useRef(0);
  const search = useRef<HTMLInputElement>(null);

  // Report the route to the shell's address bar and history (the brief:
  // report on every change). A title that arrives later replaces it.
  const reported = useRef({ path: "", title: "" });
  useEffect(() => {
    const path = formatRoute(route);
    const title = routeTitle(route, sources, item?.title ?? "Read");
    if (path === reported.current.path && title === reported.current.title) return;
    reportRoute(path, title, path === reported.current.path);
    reported.current = { path, title };
  }, [route, sources, item]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const command = commandFor(event);
      if (command === null) return;
      const state = store.getState();
      switch (command) {
        case "next":
          event.preventDefault();
          void state.selectOffset(1);
          break;
        case "previous":
          event.preventDefault();
          void state.selectOffset(-1);
          break;
        case "star":
          event.preventDefault();
          void state.toggleStar();
          break;
        case "toggleRead":
          event.preventDefault();
          void state.toggleRead();
          break;
        case "archive":
          event.preventDefault();
          void state.archive();
          break;
        case "openOriginal":
          if (state.item !== null) {
            event.preventDefault();
            openOutside(state.item.url);
          }
          break;
        case "focusSearch":
          event.preventDefault();
          search.current?.focus();
          break;
        case "markAllRead":
          event.preventDefault();
          void state.markAllRead();
          break;
        case "escape":
          if (sidebarOpen) {
            event.preventDefault();
            setSidebarOpen(false);
          } else if (narrow && state.selectedId !== null) {
            event.preventDefault();
            state.closeItem();
          } else if (state.query !== "") {
            event.preventDefault();
            void state.search("");
          }
          break;
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [store, narrow, sidebarOpen]);

  const errorSheet =
    error === null ? null : (
      <Sheet title="Something went wrong" onClose={clearError}>
        <p className="text-reader-body">{error}</p>
      </Sheet>
    );

  if (narrow) {
    return (
      <div className="flex h-full flex-col bg-window text-primary">
        {sidebarOpen ? (
          <Sidebar onNavigate={() => setSidebarOpen(false)} onBack={() => setSidebarOpen(false)} />
        ) : selectedId !== null ? (
          <Reader onBack={() => store.getState().closeItem()} />
        ) : (
          <ListPane searchRef={search} onViews={() => setSidebarOpen(true)} />
        )}
        {errorSheet}
      </div>
    );
  }

  return (
    <div className="flex h-full bg-window text-primary">
      <div className="h-full shrink-0" style={{ width: sidebarWidth }}>
        <Sidebar />
      </div>
      <Splitter
        orientation="vertical"
        label="Sidebar width"
        value={percent(sidebarWidth)}
        onStart={() => {
          drag.current = sidebarWidth;
        }}
        onMove={(delta) => setSidebarWidth(bound(drag.current + delta))}
      />
      <div className="h-full shrink-0" style={{ width: listWidth }}>
        <ListPane searchRef={search} />
      </div>
      <Splitter
        orientation="vertical"
        label="List width"
        value={percent(listWidth)}
        onStart={() => {
          drag.current = listWidth;
        }}
        onMove={(delta) => setListWidth(bound(drag.current + delta))}
      />
      <div className="h-full min-w-0 flex-1">
        <Reader />
      </div>
      {errorSheet}
    </div>
  );
}

/** bound keeps a pane's width between MIN and MAX. */
function bound(width: number): number {
  return Math.min(Math.max(width, MIN), MAX);
}

/** percent is a width as the splitter's aria-valuenow, of the frame's width. */
function percent(width: number): number {
  const total = typeof window === "undefined" ? 0 : window.innerWidth;
  return total === 0 ? 0 : Math.round((width / total) * 100);
}
