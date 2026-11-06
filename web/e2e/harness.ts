// The end-to-end harness for the built app (docs/plans/0002-the-standalone-
// desktop-flatpak.md, steps 3.6 and 3.7): readdev's generated feeds, a readd
// in its desktop mode on a temporary XDG tree, and the release app driven
// through WebKitWebDriver under xvfb-run. The hostile corpus is one more
// feed, pointed at a canary server that records every request. make app-e2e
// builds the binaries and runs it in the nsl machine.
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import { createServer as createNetServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

import { Session, WebDriverError } from "./webdriver";

/** ROOT is the repository; BUILD holds the app, and E2E the Go binaries. */
export const ROOT = resolve(import.meta.dirname, "../..");
export const BUILD = join(ROOT, "build");
/** E2E is where make app-e2e builds readd, readctl and readdev. */
export const E2E = join(BUILD, "e2e");
/** APP is the release app binary that make app-build writes. */
export const APP = join(BUILD, "frostread-app");

/** SOURCES are the titles readdev's OPML names, as the sidebar shows them. */
export const SOURCES = [
  "Dev Journal (RSS)",
  "Dev Atoms (Atom)",
  "Dev Feeds (JSON)",
  "Dev Broken (500)",
  "Dev Busy (429)",
];

/** TOMATOES is the title of readdev's first article: a summary to extract. */
export const TOMATOES = "Planting tomatoes after the last frost";

/** HOSTILE is the hostile corpus: markup that must reach no network (web/e2e/hostile/README.md). */
export const HOSTILE = join(ROOT, "web", "e2e", "hostile");

/** HOSTILE_TITLE is the start of every hostile item's title, as readdev and the sidebar give it. */
export const HOSTILE_TITLE = "Hostile ";

/**
 * HOSTILE_TEXT is the start of the paragraph readdev serves after every
 * corpus page, and it must match cmd/readdev/hostile.go's hostileText: a page
 * read through readd carries these words in its frame, which is how the
 * suite knows the frame holds the page rather than something else. Change the
 * two together.
 */
export const HOSTILE_TEXT = "The workshop keeps its own hours";

/** SEED_TIMEOUT_MS bounds each of the seed's 3 calls, well below readctl's own 2 minutes. */
const SEED_TIMEOUT_MS = 30_000;

/** EXTRACT_TIMEOUT_MS bounds the wait for the three development extractions. */
const EXTRACT_TIMEOUT_MS = 30_000;

/** HOSTILE_EXTRACT_TIMEOUT_MS bounds the same wait with the corpus: three, and one for each of its files. */
const HOSTILE_EXTRACT_TIMEOUT_MS = 150_000;

/** Canary records every HTTP request made to it, as METHOD URL. */
export interface Canary {
  /** origin is the canary's address, which no corpus page may reach. */
  origin: string;
  /** hits is what the canary recorded, oldest first, such as "GET /07-javascript-href". */
  hits: string[];
  /** close stops the server. */
  close: () => Promise<void>;
}

/** canary starts a recording HTTP server on a free loopback port, as frostmail's suite does. */
export function canary(): Promise<Canary> {
  const hits: string[] = [];
  const server = createHTTPServer((req, res) => {
    hits.push(`${req.method} ${req.url ?? ""}`);
    res.writeHead(204);
    res.end();
  });
  return new Promise((done) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      done({ origin: `http://127.0.0.1:${port}`, hits, close: () => new Promise((r) => server.close(() => r())) });
    });
  });
}

/** hostileTitle is readdev's item title for a corpus file: "Hostile 07: javascript href". */
export function hostileTitle(name: string): string {
  const [number, ...words] = name.replace(/\.html$/, "").split("-");
  return `${HOSTILE_TITLE}${number ?? ""}: ${words.join(" ")}`;
}

/** appEntry reads the app's built entry script from build/desktop/desktop.html. */
function appEntry(): string {
  const page = join(BUILD, "desktop", "desktop.html");
  const found = /<script type="module" crossorigin src="([^"]+)"/.exec(readFileSync(page, "utf8"));
  if (!found?.[1]) throw new Error(`no entry script in ${page}`);
  return found[1];
}

/**
 * hostileFiles is the corpus, sorted, with its placeholders filled in: the
 * canary's URLs point at origin, and {{APP_ENTRY}} is the app's own entry
 * script, so a page that runs its script tries to mount the real app.
 */
export function hostileFiles(origin: string): { name: string; html: string }[] {
  const entry = appEntry();
  return readdirSync(HOSTILE)
    .filter((name) => name.endsWith(".html"))
    .sort()
    .map((name) => ({
      name,
      html: readFileSync(join(HOSTILE, name), "utf8")
        .replaceAll("http://canary.test", origin)
        .replaceAll("//canary.test", origin.replace("http:", ""))
        .replaceAll("{{APP_ENTRY}}", entry),
    }));
}

/** tempDir makes a private directory for one test run. */
export function tempDir(): string {
  return mkdtempSync(join(tmpdir(), "frostread-e2e-"));
}

/** freePort is a free TCP port on loopback. */
export function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const s = createNetServer();
    s.on("error", fail);
    s.listen(0, "127.0.0.1", () => {
      const addr = s.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      s.close(() => done(port));
    });
  });
}

/** until waits for ok, failing after timeoutMs. */
export async function until(what: string, ok: () => boolean | Promise<boolean>, timeoutMs = 20_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!(await ok())) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** reachable reports whether an HTTP endpoint answers with success. */
async function reachable(url: string): Promise<boolean> {
  try {
    return (await fetch(url, { signal: AbortSignal.timeout(2_000) })).ok;
  } catch {
    return false;
  }
}

/** Opener stands in for the launchers the app shell opens links with. */
export interface Opener {
  /** env puts the stand-ins first on the app's PATH; pass it to launch. */
  env: Record<string, string>;
  /** opened returns the URLs the app asked to open, oldest first. */
  opened: () => string[];
}

/**
 * fakeOpener writes xdg-open and gio stand-ins into dir/bin that record
 * their last argument: the shell hands a clicked link to the system's
 * opener, and these are what the machine has while the test runs.
 */
export function fakeOpener(dir: string): Opener {
  const stand = join(dir, "bin");
  const log = join(dir, "opened");
  mkdirSync(stand, { recursive: true });
  for (const name of ["xdg-open", "gio"]) {
    writeFileSync(join(stand, name), `#!/bin/sh\nfor a; do last=$a; done\nprintf '%s\\n' "$last" >> '${log}'\n`, {
      mode: 0o755,
    });
  }
  return {
    env: { PATH: `${stand}:${process.env.PATH ?? ""}` },
    opened: () => (existsSync(log) ? readFileSync(log, "utf8").split(/\r?\n/).filter(Boolean) : []),
  };
}

/** Running is a child of the run and what it has written. */
interface Running {
  proc: ChildProcess;
  /** output is everything the child wrote to stdout and stderr so far. */
  output: () => string;
}

/**
 * start starts a child and collects its output. readd's own log is what
 * the seed waits on, so its output is kept as well as shown.
 */
function start(command: string, args: string[], env: NodeJS.ProcessEnv, what: string): Running {
  let text = "";
  const proc = spawn(command, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  for (const stream of [proc.stdout, proc.stderr]) {
    stream?.on("data", (chunk: Buffer) => {
      text += chunk.toString();
      process.stderr.write(chunk);
    });
  }
  proc.on("error", (err) => process.stderr.write(`${what} failed: ${String(err)}\n`));
  return { proc, output: () => text };
}

/** end sends SIGTERM to a child and waits for it, failing after 10 s. */
function end(what: string, proc: ChildProcess): Promise<void> {
  return new Promise<void>((done, fail) => {
    if (proc.exitCode !== null || proc.signalCode !== null) return done();
    const timer = setTimeout(() => fail(new Error(`${what} still ran 10 s after SIGTERM`)), 10_000);
    proc.once("exit", () => {
      clearTimeout(timer);
      done();
    });
    proc.kill("SIGTERM");
  });
}

/** pidAlive reports whether pid names a live process. */
function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM";
  }
}

/** endPids ends stand-in readd processes with a bounded SIGTERM wait. */
async function endPids(pids: number[]): Promise<string[]> {
  const problems: string[] = [];
  const unique = [...new Set(pids)].filter(pidAlive);
  for (const pid of unique) {
    try {
      process.kill(pid, "SIGTERM");
    } catch (err) {
      problems.push(`could not stop readd ${pid}: ${String(err)}`);
    }
  }
  const deadline = Date.now() + 10_000;
  while (unique.some(pidAlive) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  for (const pid of unique) {
    if (!pidAlive(pid)) continue;
    try {
      process.kill(pid, "SIGKILL");
    } catch (err) {
      problems.push(`could not kill readd ${pid}: ${String(err)}`);
    }
  }
  return problems;
}

/** App is the app under test: its session, the stand-in opener's record, and how to end it all. */
export interface App {
  /** session is the WebDriver session on the app's window. */
  session: Session;
  /** env is the environment of the app and its stand-ins. */
  env: NodeJS.ProcessEnv;
  /** opened returns the URLs the app asked the system to open, oldest first. */
  opened: () => string[];
  /** readdPids returns the harness readd and every readd the stand-in launcher started. */
  readdPids: () => number[];
  /** dir is the run's temporary directory; stop deletes it. */
  dir: string;
  /** restart quits the WebDriver session and starts a new one on the same driver. */
  restart: () => Promise<void>;
  /** stop quits the session, ends every process of the run, and deletes dir. */
  stop: () => Promise<void>;
}

/**
 * LaunchOptions add to a launch. Without them the run is the development
 * feeds alone, as step 3.6 made it.
 */
export interface LaunchOptions {
  /** launcher puts a stand-in frostread on the app's PATH and starts readd from it. */
  launcher?: boolean;
  /**
   * hostile starts readdev with the corpus as a sixth feed, Dev Hostile: its
   * pages are written into the run's directory with their canary URLs pointed
   * at origin.
   */
  hostile?: { origin: string };
  /** server replaces readdev for a made-up feed set, such as the screenshots run. */
  server?: {
    addr: string;
    allow?: string;
    command: string;
    args: string[];
    opmlArgs: string[];
    name: string;
    extractions: number;
  };
  /** environment adds variables to readd, the driver and the app. */
  environment?: NodeJS.ProcessEnv;
}

/**
 * launch starts readdev on a free port, a readd in its desktop mode on a
 * fresh XDG tree, seeds it with readdev's OPML and one poll, and starts the
 * app in a WebKitWebDriver session with the stand-ins on its PATH. A failed
 * launch stops everything it started before it throws.
 *
 * Every wait of a launch is bounded, and the worst case of all of them, in
 * order, is 375 s without the hostile corpus and 495 s with it: readdev's
 * answer 30, readd's socket 30, the seed's 3 calls 3 × 30, the extraction 30
 * (150 with the corpus), the driver's answer 45, and the session's start 120
 * (345 or 465 in all), plus the cleanup of a failed launch that has no
 * session yet: 3 processes at 10 s each, 30 more. That is below the 600 s
 * hookTimeout of vitest.e2e.config.ts, which leaves room for the polls'
 * small overruns, so a launch that stalls fails the hook in time and the
 * hook's own failure still stops the run. A change to a wait here must keep
 * this sum below hookTimeout.
 */
export async function launch(opts: LaunchOptions = {}): Promise<App> {
  const dir = tempDir();
  const socket = join(dir, "run", "readd.sock");
  for (const sub of ["data", "config", "cache", "run"]) mkdirSync(join(dir, sub), { mode: 0o700, recursive: true });
  const opener = fakeOpener(dir);
  const pidFile = join(dir, "readd-pids");
  const readdPidsStarted = new Set<number>();
  if (opts.launcher) {
    const stand = join(dir, "bin");
    const log = join(dir, "readd.log");
    writeFileSync(
      join(stand, "frostread"),
      `#!/bin/sh
set -eu
readctl='${join(E2E, "readctl")}'
readd='${join(E2E, "readd")}'
status() { "$readctl" status >/dev/null 2>&1; }
if status; then exit 0; fi
setsid "$readd" -desktop >> '${log}' 2>&1 &
pid=$!
printf '%s\n' "$pid" >> '${pidFile}'
i=0
while [ "$i" -lt 100 ]; do
  if status; then exit 0; fi
  i=$((i + 1))
  sleep 0.1
done
exit 1
`,
      { mode: 0o755 },
    );
  }
  const base = { ...process.env };
  const procs: { what: string; proc: ChildProcess }[] = [];
  let session: Session | undefined;
  let stopped = false;

  const stop = async () => {
    if (stopped) return;
    stopped = true;
    const problems: string[] = [];
    try {
      // The quit has the WebDriver client's 30 s bound, so a stalled driver
      // cannot keep stop from ending the processes below.
      await session?.quit().catch((err: unknown) => {
        // A session the driver has already dropped is the state stop wants.
        if (err instanceof WebDriverError && (err.code === "no such session" || err.code === "invalid session id")) {
          return;
        }
        problems.push(`the session did not quit: ${String(err)}`);
      });
    } finally {
      // In a finally, so the processes end even when the quit failed.
      // The driver goes first, so it cannot start another app after readd.
      const started = existsSync(pidFile)
        ? readFileSync(pidFile, "utf8")
            .split(/\r?\n/)
            .filter(Boolean)
            .map((pid) => Number(pid))
            .filter((pid) => Number.isInteger(pid) && pid > 0)
        : [];
      problems.push(...(await endPids([...readdPidsStarted, ...started])));
      for (const { what, proc } of [...procs].reverse()) {
        await end(what, proc).catch((err: unknown) => {
          problems.push(err instanceof Error ? err.message : String(err));
        });
      }
      rmSync(dir, { force: true, maxRetries: 10, recursive: true });
    }
    if (problems.length > 0) throw new Error(problems.join("; "));
  };

  try {
    const port = opts.server ? 0 : await freePort();
    const addr = opts.server?.addr ?? `127.0.0.1:${port}`;
    // The canary is a second server on loopback, and it must not be the one
    // readd's development escape admits: READ_DEV_ALLOW names readdev alone.
    if (opts.hostile && new URL(opts.hostile.origin).host === addr) {
      throw new Error(`the canary (${opts.hostile.origin}) is readdev's own address`);
    }
    // With the corpus, its pages go into the run's own directory, pointed at
    // the canary, and readdev serves them as Dev Hostile. readdev reads the
    // directory at start and never changes it.
    const readdevArgs = opts.server?.args ?? ["-addr", addr];
    const opmlArgs = opts.server?.opmlArgs ?? ["-addr", addr, "-opml"];
    let extractions = opts.server?.extractions ?? 3;
    if (opts.hostile) {
      const files = hostileFiles(opts.hostile.origin);
      const hostileDir = join(dir, "hostile");
      mkdirSync(hostileDir, { recursive: true });
      for (const file of files) writeFileSync(join(hostileDir, file.name), file.html);
      readdevArgs.push("-hostile", hostileDir);
      opmlArgs.push("-hostile", hostileDir);
      extractions += files.length;
    }
    const feedCommand = opts.server?.command ?? join(E2E, "readdev");
    const feedName = opts.server?.name ?? "readdev";
    const readdev = start(feedCommand, readdevArgs, base, feedName);
    procs.push({ proc: readdev.proc, what: feedName });
    await until(`${feedName} on ${addr}`, () => reachable(`http://${addr}/`), 30_000);

    // READ_SOCKET names readd's private socket to readctl and to the app
    // (web/src-tauri/src/paths.rs); the XDG homes are the run's own.
    const appEnv: NodeJS.ProcessEnv = {
      ...base,
      ...opts.environment,
      READ_SOCKET: socket,
      XDG_CACHE_HOME: join(dir, "cache"),
      XDG_CONFIG_HOME: join(dir, "config"),
      XDG_DATA_HOME: join(dir, "data"),
    };
    const allow = opts.server?.allow ?? addr;
    const readd = start(join(E2E, "readd"), ["-desktop"], { ...appEnv, READ_DEV_ALLOW: allow }, "readd");
    procs.push({ proc: readd.proc, what: "readd" });
    if (readd.proc.pid !== undefined) readdPidsStarted.add(readd.proc.pid);
    await until("readd's desktop socket", () => existsSync(socket), 30_000);

    // The seed: readctl import subscribes readdev's five feeds, and readctl
    // poll polls them all now, so the items do not wait for the poller. Each
    // call is bounded well below readctl's own 2 minutes, so a stalled one
    // throws here, and launch's catch stops the run.
    const opml = join(dir, "dev.opml");
    const seed = { killSignal: "SIGKILL", timeout: SEED_TIMEOUT_MS } as const;
    writeFileSync(opml, execFileSync(feedCommand, opmlArgs, { ...seed, env: base }));
    execFileSync(join(E2E, "readctl"), ["import", opml], { ...seed, env: appEnv });
    execFileSync(join(E2E, "readctl"), ["poll"], { ...seed, env: appEnv });
    // Three development items arrive as a summary alone, and readd extracts
    // each from its page; a hostile item arrives as markup to read, so readd
    // extracts it from its page too, unless the page gives it no article and
    // it says so. The reader shows nothing for an item until then.
    await until(
      `readd to extract ${extractions} articles`,
      () => {
        const lines = readd.output().split("\n");
        const extracted = lines.filter((line) => line.includes("msg=extracted")).length;
        const failed = lines.filter((line) => line.includes("did not extract")).length;
        return extracted + failed >= extractions;
      },
      opts.hostile ? HOSTILE_EXTRACT_TIMEOUT_MS : EXTRACT_TIMEOUT_MS,
    );

    // WebKitWebDriver starts the app itself, and Tauri allows automation
    // when TAURI_WEBVIEW_AUTOMATION is set. The driver's environment is the
    // app's, and READ_SOCKET and the stand-in opener come with it.
    const driverPort = await freePort();
    const driverURL = `http://127.0.0.1:${driverPort}`;
    const driver = start(
      "WebKitWebDriver",
      [`--port=${driverPort}`],
      { ...appEnv, ...opener.env, TAURI_WEBVIEW_AUTOMATION: "true" },
      "WebKitWebDriver",
    );
    procs.push({ proc: driver.proc, what: "WebKitWebDriver" });
    await until("the WebDriver server", () => reachable(`${driverURL}/status`), 45_000);
    const capabilities = {
      "webkitgtk:browserOptions": { args: ["--automation"], binary: APP },
    };
    session = await Session.start(driverURL, capabilities);
    const appEnvWithStandIns = { ...appEnv, ...opener.env, TAURI_WEBVIEW_AUTOMATION: "true" };
    const readdPids = (): number[] => {
      const started = existsSync(pidFile)
        ? readFileSync(pidFile, "utf8")
            .split(/\r?\n/)
            .filter(Boolean)
            .map((pid) => Number(pid))
            .filter((pid) => Number.isInteger(pid) && pid > 0)
        : [];
      return [...new Set([...readdPidsStarted, ...started])];
    };
    const app: App = {
      dir,
      env: appEnvWithStandIns,
      opened: opener.opened,
      readdPids,
      session,
      async restart() {
        await app.session.quit();
        session = await Session.start(driverURL, capabilities);
        app.session = session;
      },
      stop,
    };
    return app;
  } catch (err) {
    await stop().catch(() => {});
    throw err;
  }
}
