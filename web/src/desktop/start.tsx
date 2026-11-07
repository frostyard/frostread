// The desktop app's start (ADR-0008): the store, the system's colour scheme,
// the fragment's route, and the transport. It takes the window and the
// transport, so a test runs it with no shell and no lodge.
import type { Transport } from "@frostyard/lodge-client/rpc";
import { followLodge, type Scheme, type ThemeSource } from "@frostyard/lodge-ui/theme";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "../components/App.js";
import { StoreProvider } from "../components/context.js";
import { ErrorBoundary } from "../components/ErrorBoundary.js";
import { readAPI } from "../rpc/api.js";
import { watchChanges } from "../rpc/events.js";
import type { ServerControl } from "../shell.js";
import { ShellProvider } from "../shell.js";
import { createReadStore } from "../store.js";
import { ConnectionBanner } from "./ConnectionBanner.js";
import { createDesktopShell, desktopRoute, type SaveFile } from "./shell.js";

/** DESKTOP_RETRY_MS is the desktop's 10 second connection retry. */
export const DESKTOP_RETRY_MS = 10_000;

/** DesktopOptions say where the app starts: root is required, the rest default for the real window. */
export interface DesktopOptions {
  root: HTMLElement;
  win?: Window;
  transport?: Transport;
  saveFile?: SaveFile;
  server?: ServerControl;
}

/** Desktop is a started desktop app: its store, and the call that stops it. */
export interface Desktop {
  store: ReturnType<typeof createReadStore>;
  stop(): void;
}

/** startDesktop renders the app into root and connects it. stop unmounts it and removes every listener. */
export async function startDesktop(options: DesktopOptions): Promise<Desktop> {
  const win = options.win ?? window;
  const store = createReadStore({ quietDisconnects: options.server !== undefined });

  // The system's scheme is the theme source.
  const query = win.matchMedia("(prefers-color-scheme: dark)");
  const scheme = (): Scheme => (query.matches ? "dark" : "light");
  const listeners = new Set<(event: { scheme: Scheme }) => void>();
  const onScheme = () => {
    for (const fn of listeners) fn({ scheme: scheme() });
  };
  query.addEventListener("change", onScheme);
  const source: ThemeSource = {
    get theme() {
      return scheme();
    },
    on(_type, fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
  const unfollow = followLodge(source, win.document.documentElement);
  store.getState().setScheme(scheme());
  const unscheme = source.on("theme", ({ scheme: next }) => store.getState().setScheme(next));

  // The route the window starts at is its fragment. App reports its route
  // on mount, so the store must hold the fragment's route before the first
  // render: else App reports the default "/", and the start route is lost.
  // setRoute sets the route synchronously and loads nothing while there is
  // no API. The app takes navigation from the render on, so after connect
  // the store's CURRENT route loads, not the start route: a view the person
  // chose while the transport opened must stay.
  const startRoute = desktopRoute(win.location.hash);
  void store.getState().setRoute(startRoute);

  const root = createRoot(options.root);
  const shell = createDesktopShell(win, () => win.performance.now(), options.saveFile, options.server);
  root.render(
    <StrictMode>
      <ShellProvider shell={shell}>
        <StoreProvider store={store}>
          <ErrorBoundary>
            {options.server === undefined ? (
              <App />
            ) : (
              <div className="flex h-full flex-col">
                <ConnectionBanner server={options.server} />
                <div className="min-h-0 flex-1">
                  <App />
                </div>
              </div>
            )}
          </ErrorBoundary>
        </StoreProvider>
      </ShellProvider>
    </StrictMode>,
  );

  const onPop = () => void store.getState().setRoute(desktopRoute(win.location.hash));
  win.addEventListener("popstate", onPop);

  // The mock when the caller names no transport: a test, or a browser on
  // Vite's development server. desktop.tsx passes the shell's bridge when
  // it runs in Tauri. The import is lazy either way.
  const transport = options.transport ?? (await import("../rpc/mock.js")).createMockTransport();
  const unclose =
    options.server === undefined ? () => {} : transport.onClose((reason) => store.getState().setOffline(reason));
  const unwatch =
    options.server === undefined
      ? watchChanges(transport, () => void store.getState().changed())
      : // The watcher is the window's only retry schedule, every 10 s: a
        // subscribe that takes after a drop or a failure reloads everything
        // the window missed, and reconnect's status call clears the banner.
        watchChanges(
          transport,
          () => void store.getState().changed(),
          DESKTOP_RETRY_MS,
          () => void store.getState().reconnect(),
        );
  let stopModeCheck = () => {};
  let checkModeAfterConnect = () => {};
  if (options.server !== undefined && !options.server.known) {
    const server = options.server;
    let modeCheckStarted = false;
    const checkMode = () => {
      if (modeCheckStarted || store.getState().status === null) return;
      modeCheckStarted = true;
      stopModeCheck();
      // A start that could not read app.json keeps mode-specific actions off.
      // The first non-null status may follow a first connect or a reconnect:
      // read the mode once, reload once if known, and keep the safe mode on failure.
      void server
        .get()
        .then(() => server.reload())
        .catch(() => {});
    };
    stopModeCheck = store.subscribe((state, previous) => {
      if (state.status !== null && state.status !== previous.status) checkMode();
    });
    checkModeAfterConnect = checkMode;
  }

  await store.getState().connect(readAPI(transport));
  checkModeAfterConnect();

  await store.getState().setRoute(store.getState().route);

  return {
    store,
    stop() {
      win.removeEventListener("popstate", onPop);
      query.removeEventListener("change", onScheme);
      unwatch();
      unclose();
      stopModeCheck();
      unscheme();
      unfollow();
      root.unmount();
    },
  };
}
