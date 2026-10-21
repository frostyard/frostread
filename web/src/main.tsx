// Read's web bundle in lodge's shell: connect with lodge-client, follow the
// shell's theme, then open readd's API at /app/read/api and start the app.
// In vite dev there is no shell, so the app runs on the mock transport with
// the system's scheme (lodge's app-contract.md, The web build).
import { connectLodge } from "@frostyard/lodge-client";
import { applyScheme, followLodge } from "@frostyard/lodge-ui/theme";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./app.css";
import { App } from "./components/App.js";
import { StoreProvider } from "./components/context.js";
import { ErrorBoundary } from "./components/ErrorBoundary.js";
import { parseRoute } from "./lib/routes.js";
import { setLodge } from "./lodge.js";
import { readAPI } from "./rpc/api.js";
import { connectLodgeAPI } from "./rpc/lodge.js";
import { createReadStore } from "./store.js";

const host = document.getElementById("root");
if (host === null) throw new Error("read-web: #root is missing");

const store = createReadStore();
createRoot(host).render(
  <StrictMode>
    <StoreProvider store={store}>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </StoreProvider>
  </StrictMode>,
);

/** start connects the store to readd, and follows the shell's navigation. */
async function start(): Promise<void> {
  if (window.parent === window) {
    // No shell: the mock, and the system's colour scheme. The import is
    // lazy, so a lodge build carries no mock.
    applyScheme(document.documentElement, null);
    const { createMockTransport } = await import("./rpc/mock.js");
    const transport = createMockTransport();
    transport.onEvent(() => void store.getState().changed());
    await store.getState().connect(readAPI(transport));
    await store.getState().setRoute(parseRoute(location.pathname));
    return;
  }
  const lodge = await connectLodge({ app: "read" });
  setLodge(lodge);
  followLodge(lodge);
  lodge.on("navigate", ({ route }) => void store.getState().setRoute(parseRoute(route)));
  const transport = await connectLodgeAPI(lodge.apiURL());
  transport.onEvent(() => void store.getState().changed());
  await store.getState().connect(readAPI(transport));
  await store.getState().setRoute(parseRoute(lodge.history.location.pathname));
}

await start();
