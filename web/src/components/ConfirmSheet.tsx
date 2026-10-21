// The one-question sheet: a change that affects many things asks first.
import { Sheet } from "@frostyard/lodge-ui/react";

/** ConfirmSheet asks its question, and acts only on its confirm button. */
export function ConfirmSheet(props: {
  title: string;
  message: string;
  confirmLabel: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Sheet title={props.title} onClose={props.onClose}>
      <p className="text-reader-body text-primary">{props.message}</p>
      <div className="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onClick={props.onClose}
          className="h-7 rounded-md border border-separator px-3 text-sidebar-row text-primary hover:bg-selection-inactive"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={props.onConfirm}
          className="h-7 rounded-md bg-accent px-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90"
        >
          {props.confirmLabel}
        </button>
      </div>
    </Sheet>
  );
}
