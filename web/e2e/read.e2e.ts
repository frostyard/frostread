// The built app's reading path (docs/plans/0002-the-standalone-desktop-
// flatpak.md, step 3.6): readdev's generated feeds reach the window through
// a readd in its desktop mode, an article shows in the sandboxed frame with
// its image as a data: URI, and a click on the article's link reaches the
// system's opener, which the harness stands in for. Run with make app-e2e.
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { type App, launch, SOURCES, TOMATOES } from "./harness";
import type { Element, Session } from "./webdriver";

/** LINK is the archive link at the end of readdev's first article. */
const LINK = "https://example.org/journal/1";

/**
 * showTomatoes leaves the reader on readdev's first article and returns the
 * frame it shows there. Opening an item marks it read, so the unread list
 * drops it: the second test finds the article in the reader, with no row
 * left to click.
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

let app: App | undefined;

beforeAll(async () => {
  app = await launch();
});

afterAll(async () => {
  await app?.stop();
});

describe("the app on readdev's feeds", () => {
  it("lists readdev's feeds in the sidebar", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    await s.waitFor(
      "the sidebar's sources",
      async () => {
        const text = await s.execute<string>(
          'const nav = document.querySelector(\'nav[aria-label="Views and sources"]\'); return nav ? nav.innerText : ""',
        );
        return SOURCES.every((title) => text.includes(title));
      },
      30_000,
    );
  });

  it("shows an article in the frame with its image as a data: URI", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    const frame = await showTomatoes(s);
    expect(await s.attribute(frame, "sandbox")).toBe("allow-popups");

    // The frame grants neither script nor same origin, so the parent page
    // cannot read it: the driver's own element calls enter it instead.
    await s.frame(frame);
    try {
      const img = await s.waitFor("the article's image", () => s.find("img"), 30_000);
      expect(await s.attribute(img, "src")).toMatch(/^data:image\/png;base64,/);
    } finally {
      await s.parentFrame();
    }
  });

  it("shows Markdown export and no Save as note in local mode", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    await showTomatoes(s);
    const buttons = await s.findAll('section[aria-label="Reader"] button');
    const labels = await Promise.all(buttons.map((el) => s.text(el)));
    expect(labels).toContain("Export as Markdown…");
    expect(labels).not.toContain("Save as note");
  });

  it("sends a click on the article's link to the stand-in opener", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    const opened = app.opened;
    const frame = await showTomatoes(s);
    await s.frame(frame);
    const link = await s.waitFor("the article's archive link", () => s.find(`a[href="${LINK}"]`), 30_000);
    // The sanitizer gives every link target="_blank", so the click is a
    // new-window request: the shell hands it to the system's opener.
    expect(await s.attribute(link, "target")).toBe("_blank");
    await s.click(link);
    await s.parentFrame();
    await s.waitFor("the opener to receive the link", () => opened().includes(LINK), 20_000);
    expect(await s.windows()).toHaveLength(1);
    expect(opened()).toEqual([LINK]);
  });
});
