// The built app's hostile corpus (docs/plans/0002-the-standalone-desktop-
// flatpak.md, step 3.7): every page of web/e2e/hostile points at a canary
// server, and none of them may make a request, run a script or run an event
// handler, either raw in the reader's frame (the sandbox and the app's CSP
// alone) or through readd as readdev's Dev Hostile feed, where the sanitizer
// comes first. A person's click on an http, https or mailto link may reach
// the system's opener, which the harness stands in for; nothing else may,
// and no click may make a window. Run with make app-e2e.
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { type App, type Canary, canary, HOSTILE_TITLE, hostileFiles, hostileTitle, launch, TOMATOES } from "./harness";
import type { Element, Session } from "./webdriver";

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

/** OPEN_ALL_DEADLINE_MS bounds the whole of the second test's loop, well below
 * the 300 s testTimeout of vitest.e2e.config.ts, so a corpus that stops
 * opening fails with the titles that are left.
 */
const OPEN_ALL_DEADLINE_MS = 240_000;

/** UNREAD_VIEW is the title of the Unread view, as the sidebar shows it. */
const UNREAD_VIEW = "Unread";

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
 * checkFrame is what a corpus page must pass in the reader: the frame's
 * sandbox and no source, no marker that a script or an event handler ran,
 * and its links, each clicked once and remembered. A click the sandbox
 * refuses, or an element the page does not lay out, is not a failure of the
 * page: the click is what a person would do.
 */
async function checkFrame(s: Session, clicked: Set<string>): Promise<void> {
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
    expect(await s.findAll("x-ran")).toEqual([]);
    for (const link of await s.findAll("a")) {
      const href = await s.attribute(link, "href");
      if (href !== null) clicked.add(href);
      await s.click(link).catch(() => {});
    }
    // Again after the clicks: a handler that a pointer fires (onmouseover)
    // marks the page as surely as one that a load fires.
    expect(await s.findAll("x-ran")).toEqual([]);
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
    const hitsBefore = trap.hits.length;
    const clicked = new Set<string>();
    // The corpus is newer than the development articles, so the Unread
    // list shows it first: the article's own source lists it in view.
    await s.click(await sidebarButton(s, JOURNAL_SOURCE));
    await showTomatoes(s);
    for (const file of files) {
      // The frame the reader already shows, with the corpus page in place of
      // the article: the sandbox and the CSP are all that stand between the
      // page and the network.
      await s.execute(
        `const frame = document.querySelector('section[aria-label="Reader"] iframe');
         if (frame === null) throw new Error("the reader shows no frame");
         frame.srcdoc = arguments[0];`,
        file.html,
      );
      await settle(QUIET_MS);
      await checkFrame(s, clicked);
    }
    await settle(SETTLE_MS);
    const opened = app.opened().slice(openedBefore);
    expect(trap.hits.slice(hitsBefore)).toEqual([]);
    expect(openerProblems(opened, clicked)).toEqual([]);
    console.log(
      `hostile raw: ${files.length} pages, ${clicked.size} links clicked, ${opened.length} opener URLs, ${trap.hits.length - hitsBefore} canary hits`,
    );
  });

  it("runs nothing and reaches nothing through readd's sanitizer", async () => {
    if (!app || !trap) throw new Error("not started");
    const s = app.session;
    const files = hostileFiles(trap.origin);
    const titles = files.map((file) => hostileTitle(file.name));
    const openedBefore = app.opened().length;
    const hitsBefore = trap.hits.length;
    // The Unread view drops a row once it is read, so the corpus's next
    // unread items come up into the list's first page, and into view. A
    // source's view keeps its read rows, and its list reloads its first page
    // alone after each change, so its later pages are out of reach.
    await s.click(await sidebarButton(s, UNREAD_VIEW));

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
        const text = await s.text(candidate);
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
      await s.click(row);
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
        // The rendered page gets the same quiet time as a raw one. It also
        // paces the opens: each one is a history entry and a title, and
        // WebKit refuses a page more than 100 history calls in 10 s.
        await settle(QUIET_MS);
        await checkFrame(s, clicked);
      }
      done.add(wanted);
    }
    await settle(SETTLE_MS);
    const opened = app.opened().slice(openedBefore);
    expect(done.size).toBe(titles.length);
    // Every corpus page carries readdev's made-up paragraph, so each one
    // extracts and shows in the frame: a page with no frame checked nothing.
    expect(unrendered).toEqual([]);
    expect(trap.hits.slice(hitsBefore)).toEqual([]);
    expect(openerProblems(opened, clicked)).toEqual([]);
    console.log(
      `hostile through readd: ${done.size} items, ${unrendered.length} without a frame, ${clicked.size} links clicked, ${opened.length} opener URLs, ${trap.hits.length - hitsBefore} canary hits`,
    );
  });
});
