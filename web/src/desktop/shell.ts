// The desktop's Shell (ADR-0008): the window's own address and history, with
// the route in the URL fragment, and none of lodge's pages.
import { parseRoute, type Route, UNREAD } from "../lib/routes.js";
import type { Shell } from "../shell.js";

/**
 * COALESCE_MS is how soon after a push a later report replaces instead of
 * pushing. One item open reports twice (a push for the item's route, then a
 * replace when its title arrives), and a held-down `j` opens one item after
 * another: with this delay a run of opens leaves one history entry, which is
 * also what Back should undo.
 */
export const COALESCE_MS = 1_000;

/**
 * BUDGET_WINDOW_MS is the window WebKit counts the history calls in. A call
 * past its limit in this window throws a SecurityError, and the throw comes
 * from an effect, so React's root ErrorBoundary replaces the whole app.
 */
export const BUDGET_WINDOW_MS = 10_000;

/**
 * BUDGET is how many history calls this shell makes in one window. WebKit
 * refuses past 100 calls, so the app takes half of what it allows: a few
 * other calls in the window still fit.
 */
export const BUDGET = 50;

/**
 * desktopRoute reads the URL fragment as a route. The desktop has no save
 * page and no bookmarklet page, so those routes are the unread view.
 */
export function desktopRoute(hash: string): Route {
  const route = parseRoute(hash.startsWith("#") ? hash.slice(1) : hash);
  return route.kind === "save" || route.kind === "bookmarklet" ? UNREAD : route;
}

/** SaveFile saves text at a place the user picks: true when saved, false when the user cancels. */
export type SaveFile = (name: string, text: string, type: string) => Promise<boolean>;

/** noSaveFile is the shell's saver when its start names none: it refuses, so an export never looks saved. */
const noSaveFile: SaveFile = () => Promise.reject(new Error("this window cannot save files"));

/**
 * createDesktopShell is the Shell of the desktop's window. `now` is the clock
 * the history budget and the coalescing read, so a test can drive it with
 * fake timers. The default is the window's performance clock, not the wall
 * clock: a clock jump (an NTP correction, a restored laptop) must not retire
 * the calls in the budget window early and let the app spend them twice.
 */
export function createDesktopShell(
  win: Window = window,
  now: () => number = () => win.performance.now(),
  saveFile: SaveFile = noSaveFile,
): Shell {
  // The first report is the start route, which is already the window's
  // address: it replaces, so a start adds no history entry.
  let first = true;
  // written holds the times of this shell's history calls in the last
  // BUDGET_WINDOW_MS, oldest first. Only the calls it made count: another
  // caller in the window is beyond this shell's budget.
  const written: number[] = [];
  // lastPush is when this shell last pushed, or -Infinity before it pushed:
  // a report less than COALESCE_MS after it replaces instead.
  let lastPush = Number.NEGATIVE_INFINITY;
  // held is the route that waits for the window to end, or null. Its push
  // flag says whether any report it holds asked for a push: one is enough,
  // because the held route is the newest one.
  let held: { hash: string; push: boolean } | null = null;
  let timer: number | undefined;
  // warned says the window refused a call: a fast run of opens would
  // otherwise log the same failure once per open.
  let warned = false;

  /** forget drops the call times that have left the window by time t. */
  function forget(t: number): void {
    for (;;) {
      const oldest = written[0];
      if (oldest === undefined || t - oldest < BUDGET_WINDOW_MS) return;
      written.shift();
    }
  }

  /** dropHeld forgets the held route and its timer: a newer write, or the
   * route Back brought, has taken its place. */
  function dropHeld(): void {
    held = null;
    if (timer === undefined) return;
    win.clearTimeout(timer);
    timer = undefined;
  }

  /** flush writes the held route once the window has room for it. */
  function flush(): void {
    const route = held;
    if (route === null) return;
    held = null;
    const t = now();
    forget(t);
    if (written.length >= BUDGET) {
      // The timer fired with no room left (a timer that runs early, or
      // another caller's calls): hold again for the next window.
      hold(route.hash, route.push);
      return;
    }
    write(route.hash, route.push, t);
  }

  /** hold keeps a route for the end of the window, and sets the one timer. */
  function hold(hash: string, push: boolean): void {
    held = { hash, push: push || held?.push === true };
    if (timer !== undefined) return;
    // The oldest call is the first to leave the window, and the held route
    // is written then. A timer that runs late costs only a late address.
    const t = now();
    const oldest = written[0];
    const wait = oldest === undefined ? BUDGET_WINDOW_MS : Math.max(0, BUDGET_WINDOW_MS - (t - oldest));
    timer = win.setTimeout(() => {
      timer = undefined;
      flush();
    }, wait);
  }

  /** write makes one history call, which the caller has room for in the budget. */
  function write(hash: string, push: boolean, t: number): void {
    written.push(t);
    if (push) lastPush = t;
    try {
      if (push) win.history.pushState(null, "", hash);
      else win.history.replaceState(null, "", hash);
    } catch (err) {
      // A refused call loses the entry, but it never reaches the app: this
      // runs inside an effect, and a throw there shows the error page.
      if (warned) return;
      warned = true;
      // The route and the title carry an article's words, so the log names
      // the failure only.
      console.warn("the window refused a history change", err instanceof Error ? err.name : typeof err);
    }
  }

  return {
    savePages: false,
    reportRoute(path, title, replace) {
      // The title needs no history entry, so it is set even when the
      // window refuses the route.
      win.document.title = title;
      const hash = `#${path}`;
      const replacing = replace || first;
      first = false;
      // A route that came back from popstate is the current fragment: it
      // changes nothing, so the forward entries stay. A held route is older
      // than this one, and its timer would move the address away from where
      // Back or Forward went.
      if (!replacing && win.location.hash === hash) {
        dropHeld();
        // Back ends the coalescing period: the next route must push, so the
        // forward entry it replaces goes away. A replace here would overwrite
        // the entry Back chose and leave the old Forward entry in place.
        lastPush = Number.NEGATIVE_INFINITY;
        return;
      }
      const t = now();
      forget(t);
      // The coalesce rule alone cannot see a push that is held: lastPush is
      // only set by a call this shell made. A held report that asked for a
      // push still wanted a new entry, so this route takes that entry.
      const push = (!replacing && t - lastPush >= COALESCE_MS) || held?.push === true;
      if (written.length >= BUDGET) {
        // One more call in this window would throw: keep the newest route,
        // and write it when the window has room.
        hold(hash, push);
        return;
      }
      // A timer that ran late left room for this report, and this route is
      // newer than the held one: writing the held route when the timer fires
      // would put the address back.
      dropHeld();
      write(hash, push, t);
    },
    // The desktop has no lodge and no Notes (ADR-0008, "Notes is optional"),
    // so there is no route to open.
    openRoute() {},
    // Step 3.2's shell opens this with the opener plugin.
    openOutside(url) {
      win.open(url, "_blank", "noopener");
    },
    saveFile,
    // The desktop has no content route (ADR-0008): the reader shows the
    // article from items.render in a sandboxed srcdoc frame instead.
    renderArticles: true,
    contentSrc() {
      return null;
    },
  };
}
