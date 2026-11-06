// The desktop's quiet disconnected state (ADR-0008): the app never falls
// back to local data while a lodge connection is unreachable.

import { useRead } from "../components/context.js";
import type { ServerControl } from "../shell.js";

export function ConnectionBanner(props: { server: ServerControl }) {
  const offline = useRead((state) => state.offline);
  const reconnect = useRead((state) => state.reconnect);
  if (offline === null) return null;
  return (
    <div
      role="alert"
      className="border-b border-separator bg-window px-[var(--pad-x)] py-2 text-sidebar-row text-primary"
    >
      <p>
        {props.server.lodge === ""
          ? "Frostread cannot reach this computer's reader."
          : `Frostread cannot reach lodge at ${props.server.lodge}.`}
      </p>
      <p>{offline}</p>
      <p className="text-secondary">It tries again every 10 seconds.</p>
      <button
        type="button"
        onClick={() => void reconnect()}
        className="mt-1 h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive"
      >
        Try again
      </button>
    </div>
  );
}
