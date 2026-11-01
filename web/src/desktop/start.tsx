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
import { ShellProvider } from "../shell.js";
import { createReadStore } from "../store.js";
import { createDesktopShell, desktopRoute } from "./shell.js";

/** DesktopOptions say where the app starts: root is required, the rest default for the real window. */
export interface DesktopOptions {
  root: HTMLElement;
  win?: Window;
  transport?: Transport;
}

/** Desktop is a started desktop app: its store, and the call that stops it. */
export interface Desktop {
  store: ReturnType<typeof createReadStore>;
  stop(): void;
}

/** startDesktop renders the app into root and connects it. stop unmounts it and removes every listener. */
export async function startDesktop(options: DesktopOptions): Promise<Desktop> {
  const win = options.win ?? window;
  const store = createReadStore();

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
  root.render(
    <StrictMode>
      <ShellProvider shell={createDesktopShell(win)}>
        <StoreProvider store={store}>
          <ErrorBoundary>
            <App />
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
  const unwatch = watchChanges(transport, () => void store.getState().changed());
  await store.getState().connect(readAPI(transport));
  await store.getState().setRoute(store.getState().route);

  return {
    store,
    stop() {
      win.removeEventListener("popstate", onPop);
      query.removeEventListener("change", onScheme);
      unwatch();
      unscheme();
      unfollow();
      root.unmount();
    },
  };
}
