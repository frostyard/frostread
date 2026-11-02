// The built app's hostile corpus (docs/plans/0002-the-standalone-desktop-
// flatpak.md, step 3.7): every page of web/e2e/hostile points at a canary
// server, and none of them may make a request, run a script or run an event
// handler, either raw in the reader's frame (the sandbox and the app's CSP
// alone) or through readd as readdev's Dev Hostile feed, where the sanitizer
// comes first. A person's click on an http, https or mailto link may reach
// the system's opener, which the harness stands in for; nothing else may,
// and no click may make a window. Run with make app-e2e.
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import {
  type App,
  type Canary,
  canary,
  HOSTILE_TEXT,
  HOSTILE_TITLE,
  hostileFiles,
  hostileTitle,
  launch,
  TOMATOES,
} from "./harness";
import { type Element, type Session, WebDriverError } from "./webdriver";

/**
 * QUIET_MS is how long a corpus page gets to try something. An absence
 * cannot be awaited, so the test waits a bounded quiet time, as frostmail's
 * suite does.
 */
const QUIET_MS = 300;

/** SETTLE_MS is the quiet time before the canary and the opener are read: a late request still counts. */
const SETTLE_MS = 2000;

/** FRAME_TIMEOUT_MS bounds the wait for the reader's frame after an item opens. */
const FRAME_TIMEOUT_MS = 20_000;

/** TITLE_TIMEOUT_MS bounds the wait for the reader to show the item that was clicked. */
const TITLE_TIMEOUT_MS = 20_000;

/**
 * MARKER_TIMEOUT_MS bounds the wait for the evidence that the frame holds the
 * page: a corpus page's own marker, or readdev's paragraph.
 */
const MARKER_TIMEOUT_MS = 5_000;

/** MARKER_DEPTH is how far down a page's nested frames the test looks for a marker. */
const MARKER_DEPTH = 3;

/**
 * CLICK_REFUSALS are the driver's errors that a click on a page's link may
 * answer with, and that the page is not to blame for: a link the frame does
 * not lay out, one another element covers, one its own page has already
 * replaced, or one the driver no longer holds. Every other error fails the
 * test.
 */
const CLICK_REFUSALS = new Set([
  "element not interactable",
  "element click intercepted",
  "stale element reference",
  "no such element",
]);

/** driverCode is an error's code, as the driver spells it. */
function driverCode(err: unknown): string | undefined {
  if (!(err instanceof WebDriverError)) return undefined;
  return err.code.toLowerCase().replaceAll("_", " ");
}

/**
 * rowsGone reports whether err says the row's element has left the page. The
 * list reloads after each read and after each change, so a row the test has
 * looked at can be gone before it reads the row's text or clicks it: the test
 * looks again rather than failing on the row that replaced it.
 */
function rowsGone(err: unknown): boolean {
  const code = driverCode(err);
  return code === "stale element reference" || code === "no such element";
}

/** OPEN_ALL_DEADLINE_MS bounds the whole of the second test's loop, well below
 * the 300 s testTimeout of vitest.e2e.config.ts, so a corpus that stops
 * opening fails with the titles that are left.
 */
const OPEN_ALL_DEADLINE_MS = 240_000;

/** UNREAD_VIEW is the title of the Unread view, as the sidebar shows it. */
const UNREAD_VIEW = "Unread";

/**
 * UNREAD_LIST is the Unread view's list name: ListPane's listName takes it
 * from VIEW_TITLES.unread, and the listbox carries it as its aria-label.
 */
const UNREAD_LIST = "Unread";

/** LIST_TIMEOUT_MS bounds the wait for a named list, which a new route loads. */
const LIST_TIMEOUT_MS = 30_000;

/** JOURNAL_SOURCE is the title of readdev's RSS feed, which holds the tomatoes article. */
const JOURNAL_SOURCE = "Dev Journal (RSS)";

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
    30_000,
  );
}

/** settle waits ms: a page's own attempts start while it loads, and some finish after it. */
const settle = (ms: number) => new Promise((r) => setTimeout(r, ms));

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

let app: App | undefined;
let trap: Canary | undefined;

beforeAll(async () => {
  // The canary is a second server on loopback. Its host:port is never in
  // READ_DEV_ALLOW, so readd's own fetches never reach it either.
  trap = await canary();
  app = await launch({ hostile: { origin: trap.origin } });
});

afterAll(async () => {
  await app?.stop();
  await trap?.close();
});

/**
 * showTomatoes leaves the reader on readdev's first article and returns the
 * frame it shows there. The reader renders a corpus page in that same frame,
 * so the raw path needs the app to show one first.
 */
async function showTomatoes(s: Session): Promise<Element> {
  const shown = await s.execute<boolean>(
    `const h = document.querySelector('section[aria-label="Reader"] h1');
     return h !== null && h.innerText.includes(arguments[0]);`,
    TOMATOES,
  );
  if (!shown) {
    const row = await s.waitFor(
      "the tomatoes article in the list",
      async () => {
        for (const el of await s.findAll('[role="option"]')) {
          if ((await s.text(el)).includes(TOMATOES)) return el;
        }
        return undefined;
      },
      30_000,
    );
    await s.click(row);
  }
  return s.waitFor(
    "the article's frame",
    () => s.find('section[aria-label="Reader"] iframe').catch(() => undefined),
    30_000,
  );
}

/**
 * Want is the evidence that the reader's frame holds the page the test asked
 * for: page is a corpus page's own marker, in the raw path, and text is
 * readdev's paragraph and the item's title in the
 * page's own heading, in the path through readd.
 */
type Want = { page: string } | { text: string; title: string };

/**
 * expectPage waits for the evidence that the frame's document is the page
 * the test put there, and fails with the page's name when it does not come.
 */
async function expectPage(s: Session, want: Want): Promise<void> {
  if ("page" in want) {
    const marker = await s.waitFor(
      `the marker of page ${want.page}`,
      async () => {
        const [found] = await s.findAll(`[data-hostile-page="${want.page}"]`);
        return found;
      },
      MARKER_TIMEOUT_MS,
    );
    expect(await s.attribute(marker, "data-hostile-page")).toBe(want.page);
    return;
  }
  // The rendered page's own heading names its item, so a frame that shows
  // another item's page fails here, not only in the parent's heading.
  const heading = await s.waitFor(
    `the heading of ${want.title} in the frame`,
    async () => {
      const [h1] = await s.findAll("header h1");
      return h1;
    },
    MARKER_TIMEOUT_MS,
  );
  expect(await s.elementText(heading)).toBe(want.title);
  await s.waitFor(
    "readdev's paragraph in the frame",
    async () => {
      for (const p of await s.findAll("p")) {
        if ((await s.elementText(p)).includes(want.text)) return true;
      }
      return false;
    },
    MARKER_TIMEOUT_MS,
  );
}

/**
 * expectNoMarker fails when an x-ran marker shows in the frame or in a frame
 * inside it, depth levels deep: a page may nest a document that runs a
 * script, and that document marks itself the same way. A nested frame the
 * driver cannot enter answers "no such frame", and holds no document, so it
 * is skipped; every other error is the page's.
 */
async function expectNoMarker(s: Session, depth: number): Promise<void> {
  expect(await s.findAll("x-ran")).toEqual([]);
  if (depth === 0) return;
  for (const inner of await s.findAll("iframe")) {
    try {
      await s.frame(inner);
    } catch (err) {
      if (driverCode(err) === "no such frame") continue;
      throw err;
    }
    try {
      await expectNoMarker(s, depth - 1);
    } finally {
      await s.parentFrame();
    }
  }
}

/**
 * checkFrame is what a corpus page must pass in the reader: the frame's
 * sandbox and no source, the page's own evidence that the frame holds it, no
 * marker that a script or an event handler ran, and its links, each clicked
 * once and remembered. A click the sandbox refuses, or an element the page
 * does not lay out, is not a failure of the page: the click is what a person
 * would do.
 */
async function checkFrame(s: Session, clicked: Set<string>, want: Want): Promise<void> {
  const frame = await s.find('section[aria-label="Reader"] iframe');
  expect(await s.attribute(frame, "sandbox")).toBe("allow-popups");
  // WebKitWebDriver answers "" for an attribute the element lacks, so the
  // parent page, which may run a script, says whether the frame has a src.
  expect(
    await s.execute<boolean>(
      `return document.querySelector('section[aria-label="Reader"] iframe').hasAttribute("src")`,
    ),
  ).toBe(false);
  await s.frame(frame);
  try {
    await expectPage(s, want);
    await expectNoMarker(s, MARKER_DEPTH);
    for (const link of await s.findAll("a")) {
      const href = await s.attribute(link, "href");
      try {
        await s.click(link);
      } catch (err) {
        const code = driverCode(err);
        if (code !== undefined && CLICK_REFUSALS.has(code)) continue;
        throw err;
      }
      // Only a click that happened asks the shell to open a link.
      if (href !== null) clicked.add(href);
    }
    // Again after the clicks: a handler that a pointer fires (onmouseover)
    // marks the page as surely as one that a load fires.
    await expectNoMarker(s, MARKER_DEPTH);
  } finally {
    await s.parentFrame();
  }
  // No click may make a window: the shell denies every one, and hands an
  // http, https or mailto link to the system's opener instead.
  expect(await s.windows()).toHaveLength(1);
}

/** asked is the hrefs the test clicked that an opener may receive: http, https and mailto. */
function asked(clicked: Set<string>): Set<string> {
  return new Set([...clicked].filter((href) => /^(https?|mailto):/i.test(href)));
}

/** openerProblems is every URL the opener got that no clicked http, https or mailto link asked for. */
function openerProblems(opened: string[], clicked: Set<string>): string[] {
  const links = asked(clicked);
  return opened.filter((url) => !links.has(url));
}

describe("the hostile corpus", () => {
  it("runs nothing and reaches nothing raw in the reader's frame", async () => {
    if (!app || !trap) throw new Error("not started");
    const s = app.session;
    const files = hostileFiles(trap.origin);
    expect(files.length).toBeGreaterThanOrEqual(55);
    const openedBefore = app.opened().length;
    const clicked = new Set<string>();
    // The corpus is newer than the development articles, so the Unread
    // list shows it first: the article's own source lists it in view.
    await s.click(await sidebarButton(s, JOURNAL_SOURCE));
    await showTomatoes(s);
    for (const file of files) {
      // The page and its own marker, so a frame that shows anything else
      // fails the test: the marker is the evidence that the page arrived.
      const page = file.name.replace(/\.html$/, "");
      const html = `${file.html}\n<p data-hostile-page="${page}">hostile page ${page}</p>`;
      // The frame the reader already shows, with the corpus page in place of
      // the article: the sandbox and the CSP are all that stand between the
      // page and the network.
      await s.execute(
        `const frame = document.querySelector('section[aria-label="Reader"] iframe');
         if (frame === null) throw new Error("the reader shows no frame");
         frame.srcdoc = arguments[0];`,
        html,
      );
      await settle(QUIET_MS);
      await checkFrame(s, clicked, { page });
    }
    await settle(SETTLE_MS);
    const opened = app.opened().slice(openedBefore);
    // Every request the canary saw counts, the seed's and the extraction's
    // as well: a corpus page that reached it is a failure.
    expect(trap.hits).toEqual([]);
    expect(openerProblems(opened, clicked)).toEqual([]);
    console.log(
      `hostile raw: ${files.length} pages, ${clicked.size} links clicked, ${opened.length} opener URLs, ${trap.hits.length} canary hits`,
    );
  });

  it("runs nothing and reaches nothing through readd's sanitizer", async () => {
    if (!app || !trap) throw new Error("not started");
    const s = app.session;
    const files = hostileFiles(trap.origin);
    const titles = files.map((file) => hostileTitle(file.name));
    const openedBefore = app.opened().length;
    // The Unread view drops a row once it is read, so the corpus's next
    // unread items come up into the list's first page, and into view, with no
    // scroll. A source's view keeps its read rows, and its long list has a
    // test of its own: web/e2e/lists.e2e.ts.
    await s.click(await sidebarButton(s, UNREAD_VIEW));
    // The route is the Unread view now. Its rows can still be on their way:
    // the list before it can show until its own load answers, so the loop
    // below skips every row that is no corpus item, and rowsGone covers a row
    // that leaves between the look and the act.
    await listNamed(s, UNREAD_LIST, LIST_TIMEOUT_MS);

    const done = new Set<string>();
    const clicked = new Set<string>();
    const unrendered: string[] = [];
    const deadline = Date.now() + OPEN_ALL_DEADLINE_MS;
    while (done.size < titles.length) {
      const left = titles.filter((title) => !done.has(title));
      if (Date.now() > deadline) {
        throw new Error(`timed out with ${left.length} items left: ${left.join(", ")}`);
      }
      let row: Element | undefined;
      let title: string | undefined;
      for (const candidate of await s.findAll('[role="option"]')) {
        let text = "";
        try {
          text = await s.text(candidate);
        } catch (err) {
          // The list reloads after each read and each change, so this row can
          // leave between the look and the read: the next one is the row.
          if (rowsGone(err)) continue;
          throw err;
        }
        if (!text.includes(HOSTILE_TITLE)) continue;
        const match = titles.find((known) => !done.has(known) && text.includes(known));
        if (match === undefined) continue;
        row = candidate;
        title = match;
        break;
      }
      if (row === undefined || title === undefined) {
        // The list reloads after each read: look again.
        await settle(200);
        continue;
      }
      const wanted = title;
      try {
        await s.click(row);
      } catch (err) {
        // The same reload, now between the look and the click: this item is
        // not done, and the loop looks again for a row it can open.
        if (!rowsGone(err)) throw err;
        continue;
      }
      let heading = "";
      await s
        .waitFor(
          `the reader to show ${wanted}`,
          async () => {
            heading = await s.execute<string>(
              `const h = document.querySelector('section[aria-label="Reader"] h1');
               return h === null ? "" : h.innerText;`,
            );
            return heading.includes(wanted);
          },
          TITLE_TIMEOUT_MS,
        )
        .catch(async (err: unknown) => {
          const reader = await s
            .execute<string>(
              `const r = document.querySelector('section[aria-label="Reader"]');
               return r === null ? "(no reader) " + document.body.innerText.slice(0, 600) : r.innerText.slice(0, 300);`,
            )
            .catch(() => "(unknown)");
          throw new Error(`${String(err)}; the reader shows "${heading}": ${reader}`);
        });
      // A page with no frame is recorded, not waited on forever: the test
      // fails on it after the loop, with its title.
      const frame = await s
        .waitFor(
          `the frame of ${wanted}`,
          () => s.find('section[aria-label="Reader"] iframe').catch(() => undefined),
          FRAME_TIMEOUT_MS,
        )
        .catch(() => undefined);
      if (frame === undefined) unrendered.push(wanted);
      else {
        // The rendered page gets the same quiet time as a raw one. This wait
        // is the page's quiet time only, not a pace for the history: the shell
        // keeps a history budget, and web/e2e/lists.e2e.ts tests fast opens.
        await settle(QUIET_MS);
        await checkFrame(s, clicked, { text: HOSTILE_TEXT, title: wanted });
      }
      done.add(wanted);
    }
    await settle(SETTLE_MS);
    const opened = app.opened().slice(openedBefore);
    expect(done.size).toBe(titles.length);
    // Every corpus page carries readdev's made-up paragraph, so each one
    // extracts and shows in the frame: a page with no frame checked nothing.
    expect(unrendered).toEqual([]);
    // Every request the canary saw counts, the seed's and the extraction's
    // as well.
    expect(trap.hits).toEqual([]);
    expect(openerProblems(opened, clicked)).toEqual([]);
    console.log(
      `hostile through readd: ${done.size} items, ${unrendered.length} without a frame, ${clicked.size} links clicked, ${opened.length} opener URLs, ${trap.hits.length} canary hits`,
    );
  });
});
