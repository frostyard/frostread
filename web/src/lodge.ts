// The shell connection, and what the app asks of it: open a note's route,
// open an article outside, read the article's content URL. Outside lodge
// (vite dev) there is no shell, and the article's text is not available.
import type { Lodge } from "@frostyard/lodge-client";
import type { Shell } from "./shell.js";

/** LodgeConnection is the part of lodge-client's Lodge this app uses. */
export type LodgeConnection = Pick<Lodge, "open" | "contentURL" | "route">;

let current: LodgeConnection | null = null;

/** setLodge keeps the connection main.tsx made, or null outside lodge. */
export function setLodge(shell: LodgeConnection | null): void {
  current = shell;
}

/** lodge is the shell connection, or null outside lodge. */
export function lodge(): LodgeConnection | null {
  return current;
}

/** reportRoute tells the shell the frame's route, for its address bar and history. */
export function reportRoute(path: string, title: string, replace = false): void {
  current?.route(path, title, { replace });
}

/** openRoute opens a lodge route: a note, which the API named. */
export function openRoute(route: string): void {
  const shell = lodge();
  if (shell !== null) shell.open(route);
  else window.open(route, "_blank", "noopener");
}

/** openOutside opens an article or a link in a new tab, with no window handle back. */
export function openOutside(url: string): void {
  window.open(url, "_blank", "noopener");
}

/**
 * contentSrc is an item's article as lodge's content URL, or null outside
 * lodge and for an item the daemon has no content route for. contentPath is
 * relative to the content root, such as item/3/<mac>, which is what
 * lodge-client's contentURL takes: CONTENT/<app>/<path>. scheme, when given,
 * is the shell's theme, which the content page follows.
 */
export function contentSrc(contentPath: string | undefined, scheme?: "light" | "dark"): string | null {
  const shell = lodge();
  if (shell === null || contentPath === undefined || contentPath === "" || contentPath.startsWith("/")) return null;
  const url = shell.contentURL(contentPath);
  return scheme === undefined ? url : `${url}?scheme=${scheme}`;
}

/** lodgeShell is the lodge frame's Shell, over the functions above. */
export const lodgeShell: Shell = { savePages: true, reportRoute, openRoute, openOutside, contentSrc };
