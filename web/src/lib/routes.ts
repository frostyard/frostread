// The app's routes (docs/specs/api.md, Web routes): the views the sidebar
// names, a source, one item, the save page the bookmarklet opens, and the
// page that hands out the bookmarklet. lodge-client keeps the frame's path;
// the shell's canonical URL is /read plus this path.
import { type Source, VIEW_TITLES, VIEWS, type View } from "../rpc/api.js";

export type Route =
  | { kind: "view"; view: View }
  | { kind: "source"; id: number }
  | { kind: "item"; id: number }
  /** save is /save?url=…&title=…, which a bookmarklet navigates to; path is
   * the route exactly as it arrived, so reporting it adds no history entry. */
  | { kind: "save"; url: string; title: string; path: string }
  | { kind: "bookmarklet" };

/** UNREAD is the route of "/": the unread view. */
export const UNREAD: Route = { kind: "view", view: "unread" };

/** BOOKMARKLET is the route of the page that hands out the bookmarklet. */
export const BOOKMARKLET: Route = { kind: "bookmarklet" };

/** parseRoute reads a frame path; anything it does not know is the unread view. */
export function parseRoute(path: string): Route {
  // The route as it arrived, without a fragment: its path and its query.
  const end = path.indexOf("#");
  const rest = end < 0 ? path : path.slice(0, end);
  const mark = rest.indexOf("?");
  const clean = mark < 0 ? rest : rest.slice(0, mark);
  const query = mark < 0 ? "" : rest.slice(mark + 1);
  if (clean === "/save") {
    const params = new URLSearchParams(query);
    return { kind: "save", url: params.get("url") ?? "", title: params.get("title") ?? "", path: rest };
  }
  if (clean === "/bookmarklet") return BOOKMARKLET;
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

/** formatRoute writes a frame path; the unread view is "/". A save route is
 * written as it arrived, so the shell records no new entry for it. */
export function formatRoute(route: Route): string {
  switch (route.kind) {
    case "view":
      return route.view === "unread" ? "/" : `/view/${route.view}`;
    case "source":
      return `/source/${route.id}`;
    case "item":
      return `/item/${route.id}`;
    case "save":
      return route.path;
    case "bookmarklet":
      return "/bookmarklet";
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
    case "save":
      return "Save to Read";
    case "bookmarklet":
      return "Save from your phone";
  }
}

/** noteRoute is the route that opens a written note, from saveAsNote's path (base-relative). */
export function noteRoute(notePath: string, vaultId: number): string {
  const segments = notePath.split("/").filter((segment) => segment !== "");
  return `/notes/${vaultId}/${segments.map(encodeURIComponent).join("/")}`;
}
