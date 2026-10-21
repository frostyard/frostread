// A small Sheet that asks for one address: adding a source, or saving a
// link to read later. lodge-ui's Sheet takes focus on mount, so the field
// takes it back on the next task.
import { Sheet } from "@frostyard/lodge-ui/react";
import { type FormEvent, useEffect, useRef, useState } from "react";

/** UrlSheet is a form in a modal frame. */
export function UrlSheet(props: {
  title: string;
  label: string;
  placeholder: string;
  submitLabel: string;
  /** busyLabel is the button's text while the daemon works. */
  busyLabel: string;
  onSubmit: (url: string) => Promise<void>;
  onClose: () => void;
}) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const timer = setTimeout(() => field.current?.focus(), 0);
    return () => clearTimeout(timer);
  }, []);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const url = value.trim();
    if (url === "" || busy) return;
    setBusy(true);
    setFailure(null);
    try {
      await props.onSubmit(url);
      props.onClose();
    } catch (err) {
      // The sheet says what went wrong and stays open, so the address can
      // be changed and tried again.
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet title={props.title} onClose={props.onClose}>
      <form onSubmit={(event) => void submit(event)} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1 text-sidebar-row text-secondary">
          {props.label}
          <input
            ref={field}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder={props.placeholder}
            className="h-8 rounded-md border border-separator bg-window px-2 text-primary outline-none focus-visible:border-accent"
          />
        </label>
        {failure !== null && (
          <p role="alert" className="text-sidebar-row text-flag-1">
            {failure}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={props.onClose}
            className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy || value.trim() === ""}
            className="h-7 rounded-md bg-accent px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90 disabled:opacity-40"
          >
            {busy ? props.busyLabel : props.submitLabel}
          </button>
        </div>
      </form>
    </Sheet>
  );
}
