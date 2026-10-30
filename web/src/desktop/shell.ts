// The desktop's Shell (ADR-0008): the window's own address and history, with
// the route in the URL fragment, and none of lodge's pages.
import { parseRoute, type Route, UNREAD } from "../lib/routes.js";
import type { Shell } from "../shell.js";

/**
 * desktopRoute reads the URL fragment as a route. The desktop has no save
 * page and no bookmarklet page, so those routes are the unread view.
 */
export function desktopRoute(hash: string): Route {
  const route = parseRoute(hash.startsWith("#") ? hash.slice(1) : hash);
  return route.kind === "save" || route.kind === "bookmarklet" ? UNREAD : route;
}

/** createDesktopShell is the Shell of the desktop's window. */
export function createDesktopShell(win: Window = window): Shell {
  // The first report is the start route, which is already the window's
  // address: it replaces, so a start adds no history entry.
  let first = true;
  return {
    savePages: false,
    reportRoute(path, title, replace) {
      win.document.title = title;
      const hash = `#${path}`;
      const replacing = replace || first;
      first = false;
      // A route that came back from popstate is the current fragment: it
      // changes nothing, so the forward entries stay.
      if (!replacing && win.location.hash === hash) return;
      if (replacing) win.history.replaceState(null, "", hash);
      else win.history.pushState(null, "", hash);
    },
    // The desktop has no lodge and no Notes (ADR-0008, "Notes is optional"),
    // so there is no route to open.
    openRoute() {},
    // Step 3.2's shell opens this with the opener plugin.
    openOutside(url) {
      win.open(url, "_blank", "noopener");
    },
    // Null until step 3.3: the article then shows in a srcdoc frame from items.render.
    contentSrc() {
      return null;
    },
  };
}
