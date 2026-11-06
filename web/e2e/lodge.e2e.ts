// The desktop lodge connection (docs/plans/0002-the-standalone-desktop-
// flatpak.md, Phase 6, part 2). Run with make app-e2e-lodge; it skips in
// plain make app-e2e, where READ_E2E_LODGE is not set.
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { type App, freePort, launch, ROOT, SOURCES, TOMATOES, until } from "./harness";
import type { Element, Session } from "./webdriver";

const origin = process.env.READ_E2E_LODGE ?? "";
const lodgeRoot = process.env.READ_E2E_LODGE_ROOT ?? "";
const MARKER = "The garden in November";

function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM";
  }
}

async function pageText(s: Session): Promise<string> {
  return s.execute<string>("return document.body.innerText");
}

async function findButton(s: Session, text: string, timeoutMs = 30_000): Promise<Element> {
  return s.waitFor(
    `the ${text} button`,
    async () => {
      for (const el of await s.findAll("button")) {
        if ((await s.text(el)).trim() === text) return el;
      }
      return undefined;
    },
    timeoutMs,
  );
}

async function findNavButton(s: Session, text: string, timeoutMs = 30_000): Promise<Element> {
  return s.waitFor(
    `the ${text} sidebar button`,
    async () => {
      for (const el of await s.findAll('nav[aria-label="Views and sources"] button')) {
        if ((await s.text(el)).includes(text)) return el;
      }
      return undefined;
    },
    timeoutMs,
  );
}

async function waitSources(s: Session, timeoutMs = 30_000): Promise<void> {
  await s.waitFor(
    "the five local sources",
    async () => {
      const text = await s.execute<string>(
        'const nav=document.querySelector(\'nav[aria-label="Views and sources"]\'); return nav ? nav.innerText : ""',
      );
      return SOURCES.every((title) => text.includes(title));
    },
    timeoutMs,
  );
}

async function openServer(s: Session, buttonText: string): Promise<void> {
  await s.click(await findNavButton(s, buttonText));
  await s.waitFor("the Server sheet", () => s.find('input[name="reader"]').catch(() => undefined), 30_000);
}

async function setRadio(s: Session, value: "lodge" | "local"): Promise<void> {
  const radio = await s.find(`input[name="reader"][value="${value}"]`);
  await s.click(radio);
}

async function setLodgeAddress(s: Session, address: string): Promise<void> {
  await s.execute(
    `const input=document.querySelector('input[placeholder="https://lodge.example.ts.net"]');
     if (input === null) throw new Error("the lodge address input is missing");
     const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value")?.set;
     if (setter) setter.call(input, arguments[0]);
     else input.value=arguments[0];
     input.dispatchEvent(new Event("input", {bubbles:true}));`,
    address,
  );
}

async function showTomatoes(s: Session): Promise<void> {
  const shown = await s.execute<boolean>(
    `const h=document.querySelector('section[aria-label="Reader"] h1');
     return h !== null && h.innerText.includes(arguments[0]);`,
    TOMATOES,
  );
  if (!shown) {
    await s.click(await findNavButton(s, "Dev Journal (RSS)"));
    const row = await s.waitFor(
      "the tomatoes article",
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
  await s.waitFor("the tomatoes reader", () => s.find('section[aria-label="Reader"]').catch(() => undefined), 30_000);
}

async function markerVisible(s: Session, visible: boolean): Promise<boolean> {
  await s.click(await findNavButton(s, "Read later"));
  return s
    .waitFor(
      "the Read later list",
      async () => {
        const rows = await s.findAll('[role="option"]');
        const texts = await Promise.all(rows.map((el) => s.text(el)));
        const found = texts.some((text) => text.includes(MARKER));
        return visible ? found : !found;
      },
      30_000,
    )
    .then(() => true);
}

function markdownFiles(dir: string): string[] {
  if (!existsSync(dir)) return [];
  const found: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) found.push(...markdownFiles(path));
    else if (entry.name.endsWith(".md")) found.push(path);
  }
  return found;
}

describe.skipIf(!origin)("the desktop lodge connection", () => {
  let app: App | undefined;

  beforeAll(async () => {
    app = await launch({ launcher: true });

    // One saved link exists only in lodge's readd. It is the marker that
    // distinguishes lodge data from the local harness data.
    execFileSync(
      join(ROOT, "build", "e2e", "readctl"),
      ["--socket", `${lodgeRoot}/run/frostread/readctl.sock`, "save", "http://127.0.0.1:8088/articles/6"],
      {
        env: app.env,
        timeout: 30_000,
      },
    );
  });

  afterAll(async () => {
    await app?.stop();
  });

  it("a. starts in local mode with the five harness sources", async () => {
    if (!app) throw new Error("not started");
    await waitSources(app.session);
    await markerVisible(app.session, false);
  });

  it("b. switches to lodge, stops readd and shows lodge data", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    const localReadd = app.readdPids().find(processAlive);
    expect(localReadd).toBeGreaterThan(0);

    await openServer(s, "Connect to lodge…");
    await setRadio(s, "lodge");
    await setLodgeAddress(s, origin);
    await s.waitFor(
      "the lodge change text",
      async () =>
        (await pageText(s)).includes(
          "This computer's reader stops polling. Its sources and saved articles stay on this computer, not synced, until you switch back. Nothing moves to lodge.",
        ),
      30_000,
    );
    await s.click(await findButton(s, "Save"));

    await until("the local readd to exit", () => localReadd === undefined || !processAlive(localReadd), 45_000);
    expect(existsSync(join(app.dir, "data", "frostread", "frostread.db"))).toBe(true);

    const mode = JSON.parse(readFileSync(join(app.dir, "config", "frostread", "app.json"), "utf8"));
    expect(mode.lodge).toBe(origin);

    await s.waitFor(
      "the Lodge sidebar button",
      async () => (await pageText(s)).includes("Lodge: 127.0.0.1:8480"),
      45_000,
    );
    await markerVisible(s, true);
  });

  it("c. saves as a note and opens the new Notes page", async () => {
    if (!app) throw new Error("not started");
    const running = app;
    const s = running.session;
    await showTomatoes(s);

    await s.click(await findButton(s, "Save as note"));
    await s.waitFor(
      "Open in Notes",
      async () => {
        for (const el of await s.findAll("button")) {
          if ((await s.text(el)).trim() === "Open in Notes") return el;
        }
        return undefined;
      },
      45_000,
    );
    await s.click(await findButton(s, "Open in Notes"));

    await s.waitFor(
      "the stand-in opener to receive the Notes route",
      () => running.opened().some((url) => url.startsWith(`${origin}/notes/`)),
      30_000,
    );
    const opened = running.opened().find((url) => url.startsWith(`${origin}/notes/`));
    expect(opened).toBeDefined();

    await until("a Reading markdown note", () => markdownFiles(join(lodgeRoot, "vault", "Reading")).length > 0, 30_000);
  });

  it("d. shows stopped-lodge and local text, and cancels", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    const port = await freePort();
    const stopped = `http://127.0.0.1:${port}`;

    await openServer(s, "Lodge: 127.0.0.1:8480");
    await setLodgeAddress(s, stopped);
    await s.click(await findButton(s, "Save"));

    await s.waitFor(
      "the stopped lodge banner",
      async () => (await pageText(s)).includes(`Frostread cannot reach lodge at ${stopped}.`),
      45_000,
    );
    await s.waitFor(
      "no local source in lodge mode",
      async () => !(await pageText(s)).includes("Dev Journal (RSS)"),
      30_000,
    );

    await openServer(s, "Lodge:");
    await setRadio(s, "local");
    await s.waitFor(
      "the return-to-local text",
      async () =>
        (await pageText(s)).includes(
          "This computer's reader starts again with the sources and articles it had. Nothing moves from lodge.",
        ),
      30_000,
    );
    await s.click(await findButton(s, "Cancel"));
  });

  it("e. switches back to local and restores the five sources", async () => {
    if (!app) throw new Error("not started");
    const running = app;
    const s = running.session;
    await openServer(s, "Lodge:");
    await setRadio(s, "local");
    await s.click(await findButton(s, "Save"));

    await waitSources(s, 45_000);
    await until("the stand-in to start readd", () => running.readdPids().some(processAlive), 45_000);
    await markerVisible(s, false);
  });

  it("f. rolls lodge back to local when readd does not stop", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    const pid = app.readdPids().find(processAlive);
    expect(pid).toBeGreaterThan(0);
    if (pid === undefined) throw new Error("no running local readd");
    process.kill(pid, "SIGSTOP");

    try {
      await openServer(s, "Connect to lodge…");
      await setRadio(s, "lodge");
      await setLodgeAddress(s, origin);
      await s.click(await findButton(s, "Save"));

      await s.waitFor(
        "the readd did not stop alert",
        async () => (await pageText(s)).includes("This computer's reader did not stop. Frostread still uses it."),
        45_000,
      );
      const mode = JSON.parse(readFileSync(join(app.dir, "config", "frostread", "app.json"), "utf8"));
      expect(mode.lodge).toBeUndefined();
    } finally {
      process.kill(pid, "SIGCONT");
      const cancel = await s.findAll("button").then(async (buttons) => {
        for (const el of buttons) if ((await s.text(el)).trim() === "Cancel") return el;
        return undefined;
      });
      if (cancel) await s.click(cancel);
    }

    await waitSources(s);
  });

  it("g. restarts lodge mode after app.json is renamed and stops local readd", async () => {
    if (!app) throw new Error("not started");
    const s = app.session;
    await waitSources(s);
    const localReadd = app.readdPids().find(processAlive);
    expect(localReadd).toBeGreaterThan(0);

    // A crash just after step 1 of the ordered stop (ADR-0008) leaves
    // exactly this: app.json renamed to lodge, and this computer's readd
    // still running. The next start must end that readd before the
    // window connects to lodge.
    const modePath = join(app.dir, "config", "frostread", "app.json");
    const temp = join(app.dir, "config", "frostread", ".app.json.crash-test");
    writeFileSync(temp, JSON.stringify({ lodge: origin }));
    renameSync(temp, modePath);

    await app.restart();
    await until("the crash-left readd to exit", () => localReadd === undefined || !processAlive(localReadd), 45_000);

    const mode = JSON.parse(readFileSync(modePath, "utf8"));
    expect(mode.lodge).toBe(origin);
    await markerVisible(app.session, true);
  });
});
