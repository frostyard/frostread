// The desktop's server choice (ADR-0008): local facts explain a mode change,
// and a save changes app.json through Tauri instead of reaching lodge.
import { Sheet } from "@frostyard/lodge-ui/react";
import { useEffect, useState } from "react";
import type { ServerControl, ServerInfo } from "../shell.js";

/** normalizeAddress trims the address and removes one trailing slash. */
export function normalizeAddress(address: string): string {
  const trimmed = address.trim();
  return trimmed.endsWith("/") ? trimmed.slice(0, -1) : trimmed;
}

/** serverChangeText says the effect of the selected mode from local facts. */
export function serverChangeText(info: ServerInfo, lodgeMode: boolean, lodgeAddress: string): string {
  const current = normalizeAddress(info.lodge);
  const next = normalizeAddress(lodgeAddress);
  if (!lodgeMode) {
    if (current === "") return "Frostread uses this computer's reader.";
    return info.localData
      ? "This computer's reader starts again with the sources and articles it had. Nothing moves from lodge."
      : "This computer starts an empty reader. Your list on lodge stays there. To bring your subscriptions, export OPML from Read in lodge and import it here.";
  }
  if (current === "") {
    return "This computer's reader stops polling. Its sources and saved articles stay on this computer, not synced, until you switch back. Nothing moves to lodge.";
  }
  if (next === current) return `Frostread uses the lodge at ${current}.`;
  return `Frostread uses the lodge at ${next} instead. Nothing moves between the two.`;
}

export function ServerSheet(props: { server: ServerControl; onClose: () => void }) {
  const [loading, setLoading] = useState(true);
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [getFailure, setGetFailure] = useState<string | null>(null);
  const [lodgeMode, setLodgeMode] = useState(false);
  const [address, setAddress] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveFailure, setSaveFailure] = useState<string | null>(null);
  const [reloadOnClose, setReloadOnClose] = useState(false);

  useEffect(() => {
    let live = true;
    props.server
      .get()
      .then((answer) => {
        if (!live) return;
        setInfo(answer);
        setLodgeMode(answer.lodge !== "");
        setAddress(answer.lodge);
        setLoading(false);
      })
      .catch((err) => {
        if (!live) return;
        setGetFailure(err instanceof Error ? err.message : String(err));
        setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [props.server]);

  const close = () => {
    props.onClose();
    if (reloadOnClose) props.server.reload();
  };

  const current = info;
  const nextAddress = normalizeAddress(address);
  const unchanged =
    current !== null &&
    ((lodgeMode && normalizeAddress(current.lodge) !== "" && nextAddress === normalizeAddress(current.lodge)) ||
      (!lodgeMode && normalizeAddress(current.lodge) === ""));
  const disabled = saving || unchanged || (lodgeMode && nextAddress === "");

  const save = async () => {
    if (disabled) return;
    setSaving(true);
    setSaveFailure(null);
    try {
      await props.server.save(lodgeMode ? nextAddress : "");
      props.server.reload();
      props.onClose();
    } catch (err) {
      setSaveFailure(err instanceof Error ? err.message : String(err));
      try {
        const answer = await props.server.get();
        setInfo(answer);
        setReloadOnClose(normalizeAddress(answer.lodge) !== normalizeAddress(props.server.lodge));
      } catch {
        // The save error remains in place; get's own failure needs no
        // second error sheet.
      } finally {
        setSaving(false);
      }
    }
  };

  return (
    <Sheet title="Server" onClose={close}>
      {loading ? (
        <p className="text-reader-body text-secondary">Loading…</p>
      ) : getFailure !== null ? (
        <p role="alert" className="text-sidebar-row text-flag-1">
          {getFailure}
        </p>
      ) : info !== null ? (
        <div className="flex flex-col gap-3">
          <div role="radiogroup" aria-label="Reader" className="flex flex-col gap-2">
            <p className="text-sidebar-row text-secondary">Reader</p>
            <label className="flex items-center gap-2 text-reader-body text-primary">
              <input
                type="radio"
                name="reader"
                value="local"
                checked={!lodgeMode}
                onChange={() => setLodgeMode(false)}
              />
              This computer
            </label>
            <label className="flex items-center gap-2 text-reader-body text-primary">
              <input type="radio" name="reader" value="lodge" checked={lodgeMode} onChange={() => setLodgeMode(true)} />
              Lodge
            </label>
          </div>

          <label className="flex flex-col gap-1 text-sidebar-row text-secondary">
            Lodge's address
            <input
              value={address}
              disabled={!lodgeMode}
              onChange={(event) => setAddress(event.target.value)}
              placeholder="https://lodge.example.ts.net"
              className="h-8 rounded-md border border-separator bg-window px-2 text-primary outline-none focus-visible:border-accent disabled:opacity-40"
            />
          </label>

          <p aria-live="polite" className="text-sidebar-row text-primary">
            {serverChangeText(info, lodgeMode, address)}
          </p>

          {saving && <p className="text-sidebar-row text-secondary">This can take up to 30 seconds.</p>}
          {saveFailure !== null && (
            <p role="alert" className="text-sidebar-row text-flag-1">
              {saveFailure}
            </p>
          )}

          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={close}
              className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => void save()}
              disabled={disabled}
              className="h-7 rounded-md bg-accent px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90 disabled:opacity-40"
            >
              {saving ? "Saving…" : "Save"}
            </button>
          </div>
        </div>
      ) : null}
    </Sheet>
  );
}
