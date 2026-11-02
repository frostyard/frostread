// The built app's list and its history (docs/plans/0002-the-standalone-
// desktop-flatpak.md, step 3.7's two findings): fast item opens must not
// spend more history calls than WebKit grants in 10 s, which took the whole
// window down with the error boundary, and a source's long list must keep
// the later pages it loaded across the change that an open makes. Both
// defects were open when step 3.7 merged; this file is their guard. Run
// with make app-e2e.
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { type App, type Canary, canary, HOSTILE_TITLE, launch } from "./harness";
import type { Element, Session } from "./webdriver";

/**
 * ELEMENT_KEY is the driver's element id key, as webdriver.ts holds it. It is
 * not exported there, and this file must hand a row to a script to scroll it
 * into view: the driver's own element calls cannot scroll a virtual list.
 */
const ELEMENT_KEY = "element-6066-11e4-a52e-4f735466cecf";

/** UNREAD_VIEW is the title of the Unread view, as the sidebar shows it. */
const UNREAD_VIEW = "Unread";

/** HOSTILE_SOURCE is readdev's hostile corpus feed, as the sidebar shows it. */
const HOSTILE_SOURCE = "Dev Hostile";

/**
 * UNREAD_LIST is the Unread view's list name: ListPane's listName takes it
 * from VIEW_TITLES.unread, and the listbox carries it as its aria-label.
 */
const UNREAD_LIST = "Unread";

/**
 * SOURCE_LIST is a source's list name: listName takes it from that source's
 * own title, which is the same text the sidebar shows for it.
 */
const SOURCE_LIST = "Dev Hostile";

/** NEWEST is the corpus's newest item, which is a source view's first row (the corpus is newest first). */
const NEWEST = "Hostile 70:";

/** OLDEST is the corpus's oldest item: the list's first page of 50 does not hold it, only its second page. */
const OLDEST = "Hostile 01:";

/** TARGET is the item the second test opens: it comes from the second page alone. */
const TARGET = "Hostile 10:";

/** NEXT is the item after TARGET in the source's list, which one press of j must open. */
const NEXT = "Hostile 09:";

/** PRESS_COUNT is how many presses the fast-open test sends: about 50 opens are 100 history calls. */
const PRESS_COUNT = 120;

/** SIDEBAR_TIMEOUT_MS bounds the wait for a view or a source in the sidebar. */
const SIDEBAR_TIMEOUT_MS = 30_000;

/** ROW_TIMEOUT_MS bounds the wait for one row of the list. */
const ROW_TIMEOUT_MS = 20_000;

/** TITLE_TIMEOUT_MS bounds the wait for the reader to show the item that was opened. */
const TITLE_TIMEOUT_MS = 20_000;

/** FAST_OPEN_TIMEOUT_MS bounds the wait for the reader's heading to settle after the presses. */
const FAST_OPEN_TIMEOUT_MS = 30_000;

/** HEADING_POLL_MS is how often the wait for a settled heading reads it again. */
const HEADING_POLL_MS = 100;

/** HEADING_QUIET_MS is how long the heading must stay the same before it counts as settled. */
const HEADING_QUIET_MS = 1000;

/**
 * HISTORY_SETTLE_MS is the shell's history window with a second to spare:
 * WebKit refuses more than 100 history calls in 10 s, and the shell may hold
 * the last route until that window ends. A settle is what this checks: the
 * address is quiet afterwards.
 */
const HISTORY_SETTLE_MS = 11_000;

/** HASH_QUIET_MS is how long the address must stay put after the history window: a late report shows. */
const HASH_QUIET_MS = 1000;

/** LAST_PAGE_TIMEOUT_MS bounds the scroll to the list's last page, which is 2 pages of 70 rows. */
const LAST_PAGE_TIMEOUT_MS = 30_000;

/** RELOAD_TIMEOUT_MS bounds the wait for the list's reload to start and to end after a write. */
const RELOAD_TIMEOUT_MS = 20_000;

/** sidebarButton finds a view or a source in the sidebar by its title. */
function sidebarButton(s: Session, title: string): Promise<Element> {
  return s.waitFor(
    `the ${title} source`,
    async () => {
      for (const el of await s.findAll('nav[aria-label="Views and sources"] button')) {
        if ((await s.text(el)).includes(title)) return el;
      }
      return undefined;
    },
    SIDEBAR_TIMEOUT_MS,
  );
}

/** settle waits ms: the bounded wait for a state that fires no event the test can await. */
const settle = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * press sends one keydown of key to the window. The app binds its keys on
 * window (web/src/components/App.tsx), and commandFor reads event.target, so
 * the event starts at the body, bubbles to the window, and its target is no
 * text field: the app acts on the key as it does on a held one.
 */
function press(s: Session, key: string): Promise<unknown> {
  return s.execute(
    `document.body.dispatchEvent(
  new KeyboardEvent("keydown", { key: arguments[0], bubbles: true, cancelable: true }),
);`,
    key,
  );
}

/** readerHeading is the reader's subject, trimmed, or "" while the reader shows no item. */
async function readerHeading(s: Session): Promise<string> {
  const text = await s.execute<string>(
    `const h = document.querySelector('section[aria-label="Reader"] h1');
     return h === null ? "" : h.innerText;`,
  );
  return text.trim();
}

/** pageText is the window's text, cut short: what a failing wait reports, and where the boundary's words show. */
function pageText(s: Session): Promise<string> {
  return s.execute<string>(`return document.body.innerText.slice(0, 600);`);
}

/** windowTitle is the window's title, which the shell writes as the route's title (web/src/desktop/shell.ts). */
function windowTitle(s: Session): Promise<string> {
  return s.execute<string>(`return document.title;`);
}

/** hash is the window's address fragment: the desktop's route. */
function hash(s: Session): Promise<string> {
  return s.execute<string>(`return document.location.hash;`);
}

/** showsRow reports whether the list has an element for a row that shows title. */
function showsRow(s: Session, title: string): Promise<boolean> {
  return s.execute<boolean>(
    `return [...document.querySelectorAll('[role="option"]')].some((row) => row.innerText.includes(arguments[0]));`,
    title,
  );
}

/**
 * listNamed waits until the listbox is named for the route the window now
 * shows: setRoute changes listRoute, and with it listName and the listbox's
 * aria-label, at once. So this says the route has changed, and it does not
 * say the rows have arrived: the list before it can still show until its own
 * load answers.
 */
async function listNamed(s: Session, name: string, timeoutMs: number): Promise<void> {
  await s.waitFor(
    `the list named ${name}`,
    () => s.find(`[role="listbox"][aria-label="${name}"]`).catch(() => undefined),
    timeoutMs,
  );
}

/**
 * reloadIdle waits until the list pane shows no Loading… paragraph: the reload
 * of an earlier open has ended, so the observer below sees the reload that
 * this test's own write starts.
 */
async function reloadIdle(s: Session, timeoutMs: number): Promise<void> {
  await s.waitFor(
    "the list's reload to end",
    () =>
      s.execute<boolean>(
        `const pane = document.querySelector('section[aria-label="Items"]');
         if (pane === null) return false;
         return ![...pane.querySelectorAll("p")].some((p) => p.innerText.trim() === "Loading…");`,
      ),
    timeoutMs,
  );
}

/**
 * watchReload records the shape of the next reload of the list. The pane shows
 * a Loading… paragraph while a non-empty list loads, and reload() sets that
 * flag before it fetches and clears it when its answer arrives
 * (web/src/components/ListPane.tsx, web/src/store.ts), so the paragraph's
 * arrival and departure are the reload's own start and end. The record waits
 * on window for reloadDone.
 */
async function watchReload(s: Session): Promise<void> {
  await s.execute(
    `const pane = document.querySelector('section[aria-label="Items"]');
     if (pane === null) throw new Error("the window shows no list pane");
     const loading = () => [...pane.querySelectorAll("p")].some((p) => p.innerText.trim() === "Loading…");
     const record = { done: false, seen: false };
     window.__listReload = record;
     const observer = new MutationObserver(() => {
       if (loading()) record.seen = true;
       else if (record.seen) {
         record.done = true;
         observer.disconnect();
       }
     });
     observer.observe(pane, { subtree: true, childList: true, characterData: true });`,
  );
}

/** reloadDone waits for the reload that watchReload watched to show its Loading… paragraph and drop it. */
async function reloadDone(s: Session, timeoutMs: number): Promise<void> {
  await s.waitFor(
    "the list's reload to finish",
    () => s.execute<boolean>(`return window.__listReload !== undefined && window.__listReload.done === true;`),
    timeoutMs,
  );
}

/** rowWith waits for the list to hold a row that shows title, and returns it. */
function rowWith(s: Session, title: string, timeoutMs: number): Promise<Element> {
  return s.waitFor(
    `the row for ${title}`,
    async () => {
      for (const row of await s.findAll('[role="option"]')) {
        if ((await s.text(row)).includes(title)) return row;
      }
      return undefined;
    },
    timeoutMs,
  );
}

/**
 * scrollBy moves the list's scroller by delta pixels. The scroller is the
 * listbox's parent (web/src/components/ListPane.tsx), and setting its
 * position from a script fires the scroll event that asks for the next page.
 * A position the scroller already has fires nothing, so the callers step away
 * and back.
 */
function scrollBy(s: Session, delta: number): Promise<unknown> {
  return s.execute(
    `const box = document.querySelector('[role="listbox"]');
     if (box === null) throw new Error("the list shows no listbox");
     const scroller = box.parentElement;
     if (scroller === null) throw new Error("the listbox has no scroller");
     scroller.scrollTop = Math.max(0, scroller.scrollTop + arguments[0]);`,
    delta,
  );
}

/** scrollToStart puts the list's scroller back at its top, where its first row is. */
async function scrollToStart(s: Session): Promise<void> {
  await s.execute(
    `const box = document.querySelector('[role="listbox"]');
     if (box === null) throw new Error("the list shows no listbox");
     const scroller = box.parentElement;
     if (scroller === null) throw new Error("the listbox has no scroller");
     scroller.scrollTop = 0;`,
  );
}

/** scrollToLast reads the list's end, which the scroller answers with its next page. */
async function scrollToLast(s: Session): Promise<void> {
  await scrollBy(s, -1000);
  await s.execute(
    `const box = document.querySelector('[role="listbox"]');
     if (box === null) throw new Error("the list shows no listbox");
     const scroller = box.parentElement;
     if (scroller === null) throw new Error("the listbox has no scroller");
     scroller.scrollTop = scroller.scrollHeight;`,
  );
}

/**
 * lastPage scrolls the list to its end until OLDEST is in the DOM. The list
 * is virtual: a row at the end has no element until the page that holds it
 * has arrived, so the scroll is repeated, and each one sees the rows it
 * reached.
 */
async function lastPage(s: Session, timeoutMs: number): Promise<void> {
  await s.waitFor(
    `the list's last page (${OLDEST})`,
    async () => {
      await scrollToLast(s);
      return showsRow(s, OLDEST);
    },
    timeoutMs,
  );
}

/**
 * rowInView is rowWith for a row the list may not have rendered: it steps up
 * a screen at each look, so a row above the rendered window comes into it,
 * and brings the row into the middle of the list before the test clicks it.
 */
function rowInView(s: Session, title: string, timeoutMs: number): Promise<Element> {
  return s.waitFor(
    `the row for ${title} in the list`,
    async () => {
      for (const row of await s.findAll('[role="option"]')) {
        if ((await s.text(row)).includes(title)) {
          await s.execute(`arguments[0].scrollIntoView({ block: "center" })`, { [ELEMENT_KEY]: row.id });
          return row;
        }
      }
      await scrollBy(s, -800);
      return undefined;
    },
    timeoutMs,
  );
}

/**
 * steadyHeading waits until the reader's heading names a corpus item and
 * stays the same for HEADING_QUIET_MS, and returns it. Opens that a person
 * makes faster than readd answers finish out of order: an earlier open's
 * item can arrive after a later one's, so the reader's last word is what the
 * test waits for, not the item it pressed last.
 */
async function steadyHeading(s: Session, timeoutMs: number): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  let since = Date.now();
  for (;;) {
    const heading = await readerHeading(s);
    if (heading !== last) {
      last = heading;
      since = Date.now();
    } else if (heading.includes(HOSTILE_TITLE) && Date.now() - since >= HEADING_QUIET_MS) {
      return heading;
    }
    if (Date.now() > deadline) {
      const shown = await pageText(s);
      throw new Error(`timed out after ${PRESS_COUNT} presses: the reader shows "${heading}": ${shown}`);
    }
    await settle(HEADING_POLL_MS);
  }
}

let app: App | undefined;
let trap: Canary | undefined;

beforeAll(async () => {
  // The canary is a second server on loopback; the corpus arrives with it,
  // and no test here opens a corpus page.
  trap = await canary();
  app = await launch({ hostile: { origin: trap.origin } });
});

afterAll(async () => {
  await app?.stop();
  await trap?.close();
});

describe("the app's list and its history", () => {
  // The long list goes first. It opens the corpus's rows and toggles one of
  // them read, and the fast-open test then wants the corpus's rows in the
  // list as well: this order keeps this test on a fresh app in a full run,
  // as it is when it runs alone.
  it("keeps a long list's later pages across a change", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    // A fresh list state: the unread view, then the source's view again, so
    // the list starts at its first page and the second page comes from the
    // scroll alone. Each click is followed by the wait for that route's own
    // list name, which says the route has changed and not that its rows have
    // arrived: the list before it can still show until its load answers, and
    // the row wait after the second click reads a row that the source's list
    // keeps. Nothing here needs the other test: this one runs alone.
    await s.click(await sidebarButton(s, UNREAD_VIEW));
    await listNamed(s, UNREAD_LIST, ROW_TIMEOUT_MS);
    await s.click(await sidebarButton(s, HOSTILE_SOURCE));
    await listNamed(s, SOURCE_LIST, ROW_TIMEOUT_MS);
    await rowWith(s, NEWEST, ROW_TIMEOUT_MS);
    await lastPage(s, LAST_PAGE_TIMEOUT_MS);

    const target = await rowInView(s, TARGET, ROW_TIMEOUT_MS);
    await s.click(target);
    await s.waitFor(
      `the reader to show ${TARGET}`,
      async () => (await readerHeading(s)).includes(TARGET),
      TITLE_TIMEOUT_MS,
    );

    // No fixed settle here: the test waits for the reload's own start and end.
    // The list pane shows a Loading… paragraph while a non-empty list loads,
    // and reload() sets that flag before it fetches and clears it when its
    // answer arrives, so an observer on the pane records both marks, and the
    // checks below read the reloaded list, not the one before it. One press of
    // m always writes: toggleRead sends setRead, and readd sends items.changed
    // for it, so the list reloads even when the open found TARGET read already
    // and wrote nothing.
    await reloadIdle(s, ROW_TIMEOUT_MS);
    await watchReload(s);
    await press(s, "m");
    await reloadDone(s, RELOAD_TIMEOUT_MS);

    // Before the fix the reload asked for the first page alone, rows 51 to 70
    // left the list, and the selection could not be found.
    const row = await rowWith(s, TARGET, ROW_TIMEOUT_MS);
    expect(await s.attribute(row, "aria-selected")).toBe("true");

    // One press of j opens the row after TARGET. A list of its first 50 rows
    // alone cannot find the selection, so j opens Hostile 70 instead, and the
    // wait below fails: that is the defect.
    await press(s, "j");
    await s.waitFor(
      `the reader to show ${NEXT}`,
      async () => (await readerHeading(s)).includes(NEXT),
      TITLE_TIMEOUT_MS,
    );
  });

  it("survives fast opens", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    // A source's view keeps its read rows, so the corpus's 70 items stay in
    // the list here, and the first page's first row is the newest item. The
    // scroller keeps the place the test before left it at, and a route to the
    // list the window already shows reloads the rows without moving it, so
    // the test goes back to the top, where the first row is, and names it
    // rather than taking whatever element the virtual list renders first.
    await s.click(await sidebarButton(s, HOSTILE_SOURCE));
    await scrollToStart(s);
    const first = await rowWith(s, NEWEST, ROW_TIMEOUT_MS);
    await s.click(first);
    await s.waitFor(
      `the reader to show ${NEWEST}`,
      async () => (await readerHeading(s)).includes(NEWEST),
      TITLE_TIMEOUT_MS,
    );

    // One press per script, so each is its own task and the app has a chance
    // to act on each, as a held key gives it. Each open reports the item's
    // route and then its title: 2 history calls, so about 50 opens reach
    // WebKit's limit of 100 calls in 10 s.
    for (let i = 0; i < PRESS_COUNT; i++) await press(s, "j");

    // Before the fix this wait fails: WebKit's pushState throws once the
    // window has had 100 calls, the throw comes from an effect in App, and
    // the root ErrorBoundary replaces the whole app with "Something went
    // wrong" and a Reload button, so the reader's heading is gone.
    const heading = await steadyHeading(s, FAST_OPEN_TIMEOUT_MS);
    expect(await pageText(s)).not.toContain("Something went wrong");
    expect(heading).toContain(HOSTILE_TITLE);
    expect(await hash(s)).toMatch(/^#\/item\/\d+$/);

    // The shell keeps a history budget: it may hold the last route until its
    // 10 s window ends, so the address is read after it, then watched for a
    // quiet second. Both are what this checks: one settled address.
    await settle(HISTORY_SETTLE_MS);
    const address = await hash(s);
    expect(address).toMatch(/^#\/item\/\d+$/);
    await settle(HASH_QUIET_MS);
    expect(await hash(s)).toBe(address);
    // The shell writes the route's title to the window even while the budget
    // holds the address. App reports the item's title there, and the reader's
    // heading shows that same item, so a title equal to the heading says the
    // shell's last report carried the reader's item: the address is an item's
    // route, and nothing reported after it.
    expect(await windowTitle(s)).toBe(await readerHeading(s));
  });
});
