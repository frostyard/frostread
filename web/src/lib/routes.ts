// The app's routes (docs/specs/api.md and the brief): the views the sidebar
// names, a source, and one item. lodge-client keeps the frame's path; the
// shell's canonical URL is /read plus this path.
import { type Source, VIEW_TITLES, VIEWS, type View } from "../rpc/api.js";

export type Route = { kind: "view"; view: View } | { kind: "source"; id: number } | { kind: "item"; id: number };

/** UNREAD is the route of "/": the unread view. */
export const UNREAD: Route = { kind: "view", view: "unread" };

/** parseRoute reads a frame path; anything it does not know is the unread view. */
export function parseRoute(path: string): Route {
  const clean = path.split(/[?#]/, 1)[0] ?? "";
  if (clean === "" || clean === "/") return UNREAD;
  const item = /^\/item\/(\d+)$/.exec(clean);
  if (item?.[1] !== undefined) return { kind: "item", id: Number(item[1]) };
  const source = /^\/source\/(\d+)$/.exec(clean);
  if (source?.[1] !== undefined) return { kind: "source", id: Number(source[1]) };
  const view = /^\/view\/([a-z]+)$/.exec(clean);
  if (view?.[1] !== undefined && (VIEWS as readonly string[]).includes(view[1])) {
    return { kind: "view", view: view[1] as View };
  }
  return UNREAD;
}

/** formatRoute writes a frame path; the unread view is "/". */
export function formatRoute(route: Route): string {
  switch (route.kind) {
    case "view":
      return route.view === "unread" ? "/" : `/view/${route.view}`;
    case "source":
      return `/source/${route.id}`;
    case "item":
      return `/item/${route.id}`;
  }
}

/** routeTitle names a route for the shell's address bar. */
export function routeTitle(route: Route, sources: Source[], itemTitle = "Read"): string {
  switch (route.kind) {
    case "view":
      return VIEW_TITLES[route.view];
    case "source":
      return sources.find((source) => source.id === route.id)?.title ?? "Source";
    case "item":
      return itemTitle;
  }
}

/** noteRoute is the route that opens a written note, from saveAsNote's path (base-relative). */
export function noteRoute(notePath: string, vaultId: number): string {
  const segments = notePath.split("/").filter((segment) => segment !== "");
  return `/notes/${vaultId}/${segments.map(encodeURIComponent).join("/")}`;
}
