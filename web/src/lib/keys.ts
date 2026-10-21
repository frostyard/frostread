// Keyboard shortcuts, as frostmail's keymap has them
// (app/src/lib/keymap.ts at commit 4328cb7): a binding matches only without
// Ctrl, Alt or Meta, and in a text field only Escape applies, so the field
// keeps every other key.

/** Command is what a key asks the app to do. */
export type Command =
  | "next"
  | "previous"
  | "star"
  | "toggleRead"
  | "archive"
  | "openOriginal"
  | "focusSearch"
  | "markAllRead"
  | "escape";

/** inTextField reports whether an element takes text, which keeps the app's keys out. */
export function inTextField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  return target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT";
}

/** commandFor is the command a key event asks for, or null. */
export function commandFor(event: KeyboardEvent): Command | null {
  if (event.ctrlKey || event.metaKey || event.altKey) return null;
  if (inTextField(event.target)) return event.key === "Escape" ? "escape" : null;
  switch (event.key) {
    case "j":
      return "next";
    case "k":
      return "previous";
    case "s":
      return "star";
    case "m":
      return "toggleRead";
    case "e":
      return "archive";
    case "o":
      return "openOriginal";
    case "/":
      return "focusSearch";
    case "A":
      return event.shiftKey ? "markAllRead" : null;
    case "Escape":
      return "escape";
    default:
      return null;
  }
}
