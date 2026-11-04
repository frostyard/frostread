// Make the Flatpak metainfo screenshots with the real desktop app.
// make screenshots runs this suite in the nsl machine. It uses the app's
// temporary desktop state and made-up loopback feeds, never the lodge UI.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterAll, describe, expect, it } from "vitest";

import { type App, launch, ROOT } from "./harness";
import type { Element, Session } from "./webdriver";

const ADDR = "127.0.0.1:8099";
const HOST = "orchard.localhost:8099";
const BASE = `http://${HOST}`;
const OUT = join(ROOT, "docs", "images");
const SERVER = join(ROOT, "build", "e2e", "screenshots-server");

let light: App | undefined;
let dark: App | undefined;

async function viewport(s: Session): Promise<void> {
  for (let i = 0; i < 3; i++) {
    const size = await s.execute<number[]>("return [window.innerWidth, window.innerHeight]");
    const rect = await s.windowRect();
    await s.setWindowRect({
      x: 0,
      y: 0,
      width: rect.width + 1280 - (size[0] ?? 1280),
      height: rect.height + 800 - (size[1] ?? 800),
    });
  }
  const size = await s.execute<number[]>("return [window.innerWidth, window.innerHeight]");
  expect(size).toEqual([1280, 800]);
}

async function clickNamed(s: Session, css: string, name: string): Promise<Element> {
  return s.waitFor(
    `${name} button`,
    async () => {
      for (const el of await s.findAll(css)) {
        if ((await s.text(el)).includes(name)) return el;
      }
      return undefined;
    },
    30_000,
  );
}

interface ReaderImageState {
  count: number;
  incomplete: number;
  zeroWidth: number;
  dataSources: number;
  widths: number[];
  painted: number;
}

async function waitReaderImages(s: Session): Promise<void> {
  const imageState = (): Promise<ReaderImageState> =>
    s.execute<ReaderImageState>(
      `const images=[...document.images];
     const widths=images.map((image) => image.naturalWidth);
     return {
       count: images.length,
       incomplete: images.filter((image) => !image.complete).length,
       zeroWidth: widths.filter((width) => width <= 0).length,
       dataSources: images.filter((image) => (image.currentSrc || image.src).startsWith("data:image/png;base64,")).length,
       widths,
       painted: images.filter((image) => {
         const rect = image.getBoundingClientRect();
         return rect.width > 0 && rect.height > 0;
       }).length,
     };`,
    );
  let paintedRuns = 0;
  try {
    await s.waitFor(
      "every reader image to decode and paint",
      async () => {
        const state = await imageState();
        const ready =
          state.count > 0 &&
          state.incomplete === 0 &&
          state.zeroWidth === 0 &&
          state.dataSources === state.count &&
          state.painted === state.count;
        paintedRuns = ready ? paintedRuns + 1 : 0;
        return ready && paintedRuns >= 5 ? state : undefined;
      },
      30_000,
    );
  } catch (err) {
    const state = await imageState().catch(() => ({
      count: 0,
      incomplete: 0,
      zeroWidth: 0,
      dataSources: 0,
      widths: [],
      painted: 0,
    }));
    throw new Error(
      `reader image did not decode: count=${state.count}, incomplete=${state.incomplete}, zeroWidth=${state.zeroWidth}, dataSources=${state.dataSources}, painted=${state.painted}, widths=[${state.widths.join(",")}]`,
      { cause: err },
    );
  }
}

async function openArticle(s: Session, title: string): Promise<Element> {
  const row = await s.waitFor(
    title,
    async () => {
      for (const el of await s.findAll('[role="option"]')) {
        if ((await s.text(el)).includes(title)) return el;
      }
      return undefined;
    },
    30_000,
  );
  await s.click(row);
  await s.waitFor(
    "the open row to stay selected",
    async () => (await s.attribute(row, "aria-selected")) === "true",
    30_000,
  );
  const frame = await s.waitFor(
    "the article frame",
    () => s.find('section[aria-label="Reader"] iframe').catch(() => undefined),
    30_000,
  );
  await s.frame(frame);
  try {
    await s.waitFor("the article heading", () => s.find("h1").catch(() => undefined), 30_000);
    await s.waitFor("the article image", () => s.find("img").catch(() => undefined), 30_000);

    await waitReaderImages(s);

    await s.waitFor(
      "the article text",
      async () => {
        for (const el of await s.findAll("p")) {
          const text = await s.elementText(el);
          if (text.length >= 100) return el;
        }
        return undefined;
      },
      30_000,
    );
  } finally {
    await s.parentFrame();
  }
  await s.waitFor(
    "the reader's selected title",
    async () => {
      const heading = await s.execute<string>(
        'const h=document.querySelector(\'section[aria-label="Reader"] h1\'); return h ? h.innerText : ""',
      );
      return heading.includes(title);
    },
    30_000,
  );
  await s.waitFor(
    "the selected row remains visible",
    async () => {
      for (const el of await s.findAll('[role="option"]')) {
        if ((await s.text(el)).includes(title)) {
          return (await s.attribute(el, "aria-selected")) === "true";
        }
      }
      return false;
    },
    30_000,
  );

  // The item answer can reload the srcdoc when its scheme or detail settles.
  // Enter the final frame once more, wait for its image, then leave it in the
  // same state as the shot. This prevents a screenshot from taking an early
  // or transient frame that has not decoded the article image.
  const finalFrame = await s.waitFor(
    "the settled article frame",
    () => s.find('section[aria-label="Reader"] iframe').catch(() => undefined),
    30_000,
  );
  await s.frame(finalFrame);
  try {
    await waitReaderImages(s);
  } finally {
    await s.parentFrame();
  }
  return finalFrame;
}

async function openSaved(s: Session, title: string): Promise<void> {
  const row = await s.waitFor(
    title,
    async () => {
      for (const el of await s.findAll('[role="option"]')) {
        if ((await s.text(el)).includes(title)) return el;
      }
      return undefined;
    },
    30_000,
  );
  await s.click(row);
  await s.waitFor(
    "the saved row to stay selected",
    async () => (await s.attribute(row, "aria-selected")) === "true",
    30_000,
  );
  const frame = await s.waitFor(
    "the saved article frame",
    () => s.find('section[aria-label="Reader"] iframe').catch(() => undefined),
    30_000,
  );
  await s.frame(frame);
  try {
    await s.waitFor(
      "the saved article heading",
      async () => {
        const heading = await s.find("h1").catch(() => undefined);
        return heading && (await s.elementText(heading)).includes(title);
      },
      30_000,
    );
  } finally {
    await s.parentFrame();
  }
}

async function shot(s: Session, name: string): Promise<void> {
  await s.execute("document.activeElement && document.activeElement.blur(); window.getSelection().removeAllRanges();");
  await s.pointerMove(1270, 790);
  writeFileSync(join(OUT, name), await s.screenshot());
}

const server = {
  addr: ADDR,
  allow: HOST,
  command: SERVER,
  args: ["-addr", ADDR, "-base", BASE],
  opmlArgs: ["-addr", ADDR, "-base", BASE, "-opml"],
  name: "screenshots-server",
  extractions: 8,
};

afterAll(async () => {
  await dark?.stop();
  await light?.stop();
});

describe("the Flatpak metainfo screenshots", () => {
  it("takes the reading and later screenshots", async () => {
    mkdirSync(OUT, { recursive: true });
    light = await launch({ server });
    const s = light.session;
    await viewport(s);

    await s.waitFor(
      "the screenshot feed sources",
      async () => {
        const nav = await s.execute<string>(
          'const n=document.querySelector(\'nav[aria-label="Views and sources"]\'); return n ? n.innerText : ""',
        );
        return ["The Orchard Journal", "Weeknight Kitchen", "Trail Notes", "The Home Workshop"].every((title) =>
          nav.includes(title),
        );
      },
      30_000,
    );

    const all = await clickNamed(s, "nav button", "All");
    await s.click(all);
    await openArticle(s, "A quiet week in the seed garden");
    await shot(s, "reading.png");

    const socket = join(light.dir, "run", "readd.sock");
    const env = { ...process.env, READ_SOCKET: socket };
    for (const url of [
      `${BASE}/later/Garden-notes`,
      `${BASE}/later/Weekend-baking`,
      `${BASE}/later/Trail-map`,
      `${BASE}/later/Bench-project`,
    ]) {
      execFileSync(join(ROOT, "build", "e2e", "readctl"), ["save", url], {
        env,
        timeout: 30_000,
      });
    }

    const later = await clickNamed(s, "nav button", "Read later");
    await s.click(later);
    await s.waitFor(
      "four distinct saved links",
      async () => {
        const list = await s.findAll('[role="option"]');
        const texts = await Promise.all(list.slice(0, 4).map((el) => s.text(el)));
        return (
          ["Garden notes", "weekend loaf", "north ridge", "porch bench"].every((title) =>
            texts.some((text) => text.toLowerCase().includes(title.toLowerCase())),
          ) && new Set(texts).size >= 4
        );
      },
      30_000,
    );
    await openSaved(s, "Garden notes");
    await shot(s, "later.png");
  }, 300_000);

  it("takes the dark screenshot", async () => {
    await light?.stop();
    light = undefined;
    dark = await launch({
      server,
      environment: {
        GTK_THEME: "Adwaita:dark",
        GTK_APPLICATION_PREFER_DARK_THEME: "1",
        XDG_CURRENT_DESKTOP: "GNOME",
      },
    });
    const s = dark.session;
    await viewport(s);

    await s.waitFor(
      "the dark preference",
      async () => s.execute<boolean>('return window.matchMedia("(prefers-color-scheme: dark)").matches'),
      30_000,
    );

    await s.waitFor(
      "the dark screenshot sources",
      async () => {
        const nav = await s.execute<string>(
          'const n=document.querySelector(\'nav[aria-label="Views and sources"]\'); return n ? n.innerText : ""',
        );
        return nav.includes("The Orchard Journal");
      },
      30_000,
    );

    const source = await clickNamed(s, "nav button", "The Orchard Journal");
    await s.click(source);
    await openArticle(s, "Tomatoes after the last frost");
    await shot(s, "dark.png");
  }, 300_000);
});
