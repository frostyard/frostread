// A minimal W3C WebDriver client for driving the built app through
// WebKitWebDriver (plan 0002, step 3.6). Only the endpoints the app's
// end-to-end tests use. The article shows in a sandboxed frame, which the
// parent page cannot reach, so the tests find and read elements inside it
// with the driver's own element calls, never with a script.

const ELEMENT = "element-6066-11e4-a52e-4f735466cecf";

/**
 * CALL_TIMEOUT_MS bounds one WebDriver request. A driver that stalls answers
 * nothing, and a test must fail on it rather than hang: the harness's own
 * waits and the hook's timeout stay the outer bound.
 */
const CALL_TIMEOUT_MS = 30_000;

/** START_TIMEOUT_MS bounds the request that makes a session: the driver starts the app first. */
export const START_TIMEOUT_MS = 120_000;

/** Element is a handle to an element in the session's page. */
export interface Element {
  readonly id: string;
}

interface Reply<T> {
  value: T & { error?: string; message?: string };
}

/** WebDriverError is an error reply from the driver. */
export class WebDriverError extends Error {
  /** code is the driver's error code, such as no such element. */
  readonly code: string;

  constructor(code: string, message: string) {
    super(`${code}: ${message}`);
    this.code = code;
  }
}

async function call<T>(
  base: string,
  method: string,
  path: string,
  body?: unknown,
  timeoutMs = CALL_TIMEOUT_MS,
): Promise<T> {
  const signal = AbortSignal.timeout(timeoutMs);
  const init: RequestInit = { method, signal };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  try {
    const res = await fetch(base + path, init);
    const reply = (await res.json()) as Reply<T>;
    if (!res.ok || reply.value?.error) {
      throw new WebDriverError(reply.value?.error ?? String(res.status), reply.value?.message ?? res.statusText);
    }
    return reply.value;
  } catch (err) {
    // The only thing that aborts the signal is its own timeout: name it as
    // a timeout of this request, so a stalled driver is clear in the log.
    if (signal.aborted && !(err instanceof WebDriverError)) {
      throw new WebDriverError("timeout", `${method} ${path} gave no answer within ${timeoutMs} ms`);
    }
    throw err;
  }
}

/** Session is one WebDriver session: one running app. */
export class Session {
  /** id is the session's own id, as the driver gave it. */
  readonly id: string;
  private readonly base: string;

  private constructor(base: string, id: string) {
    this.base = base;
    this.id = id;
  }

  /** start creates a session; capabilities go into alwaysMatch. */
  static async start(driverURL: string, capabilities: Record<string, unknown>): Promise<Session> {
    const v = await call<{ sessionId: string }>(
      driverURL,
      "POST",
      "/session",
      { capabilities: { alwaysMatch: capabilities } },
      START_TIMEOUT_MS,
    );
    return new Session(driverURL, v.sessionId);
  }

  private do<T>(method: string, path: string, body?: unknown): Promise<T> {
    return call<T>(this.base, method, `/session/${this.id}${path}`, body);
  }

  /** quit ends the session and closes the app. */
  async quit(): Promise<void> {
    await this.do("DELETE", "");
  }

  /** execute runs a synchronous script in the page and returns its result. */
  execute<T>(script: string, ...args: unknown[]): Promise<T> {
    return this.do<T>("POST", "/execute/sync", { script, args });
  }

  /** find returns the first element matching a CSS selector. */
  async find(css: string): Promise<Element> {
    const v = await this.do<Record<string, string>>("POST", "/element", { using: "css selector", value: css });
    return { id: v[ELEMENT] ?? "" };
  }

  /** findAll returns every element matching a CSS selector. */
  async findAll(css: string): Promise<Element[]> {
    const v = await this.do<Record<string, string>[]>("POST", "/elements", { using: "css selector", value: css });
    return v.map((e) => ({ id: e[ELEMENT] ?? "" }));
  }

  /** click clicks an element. */
  async click(el: Element): Promise<void> {
    await this.do("POST", `/element/${el.id}/click`, {});
  }

  /**
   * text returns an element's innerText. WebKitWebDriver's own element text
   * drops text inside overflow-hidden, ellipsized elements (Tailwind's
   * truncate), which the app uses for every label.
   */
  text(el: Element): Promise<string> {
    return this.execute<string>("return arguments[0].innerText", { [ELEMENT]: el.id });
  }

  /**
   * attribute returns an element's attribute as the page has it, or null
   * when it has none. It needs no script, so it reads inside the sandboxed
   * frame, where the parent page may not run one.
   */
  async attribute(el: Element, name: string): Promise<string | null> {
    return this.do<string | null>("GET", `/element/${el.id}/attribute/${name}`);
  }

  /** windows returns the handles of the session's windows. */
  windows(): Promise<string[]> {
    return this.do<string[]>("GET", "/window/handles");
  }

  /** frame makes an iframe's document current, so find and click reach inside it. */
  async frame(el: Element): Promise<void> {
    await this.do("POST", "/frame", { id: { [ELEMENT]: el.id } });
  }

  /** parentFrame makes the current frame's parent current again. */
  async parentFrame(): Promise<void> {
    await this.do("POST", "/frame/parent", {});
  }

  /** waitFor polls fn until it returns a truthy value or timeoutMs passes. */
  async waitFor<T>(what: string, fn: () => T | Promise<T>, timeoutMs = 10_000): Promise<NonNullable<T>> {
    const deadline = Date.now() + timeoutMs;
    let last: unknown;
    for (;;) {
      try {
        const v = await fn();
        if (v) return v as NonNullable<T>;
      } catch (err) {
        last = err;
      }
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}${last ? `: ${String(last)}` : ""}`);
      await new Promise((r) => setTimeout(r, 100));
    }
  }
}
