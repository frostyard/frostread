// Run-time checks of readd's wire results against the types in api.ts
// (docs/specs/api.md). The golden files in testdata/ come from the Go
// daemon itself, through internal/readapi's contract test, so a field that
// one side renames or omits fails here before a browser sees it.
import type {
  Counts,
  ExtractState,
  ImportResult,
  ItemDetail,
  ItemsChanged,
  Note,
  Page,
  Source,
  Status,
  View,
} from "./api.js";
import { VIEWS } from "./api.js";

/** Problems is what a check found; empty means the value matches. */
export type Problems = string[];

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/** object checks that v is an object, and that it holds no other member. */
function object(v: unknown, at: string, allowed: readonly string[], p: Problems): v is Record<string, unknown> {
  if (!isObject(v)) {
    p.push(`${at}: not an object`);
    return false;
  }
  for (const key of Object.keys(v)) {
    if (!allowed.includes(key)) p.push(`${at}.${key}: the daemon does not send this member`);
  }
  return true;
}

/** field checks one member, which must be absent when it is optional. */
function field(
  v: Record<string, unknown>,
  key: string,
  at: string,
  p: Problems,
  check: (value: unknown) => boolean,
  optional = false,
): void {
  const value = v[key];
  if (value === undefined) {
    if (!optional) p.push(`${at}.${key}: missing`);
    return;
  }
  if (!check(value)) p.push(`${at}.${key}: ${JSON.stringify(value)} is the wrong type`);
}

const isString = (v: unknown): v is string => typeof v === "string";
const isNumber = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
const isBoolean = (v: unknown): v is boolean => typeof v === "boolean";
const isView = (v: unknown): v is View => typeof v === "string" && (VIEWS as readonly string[]).includes(v);
const isExtractState = (v: unknown): v is ExtractState => v === "pending" || v === "done" || v === "failed";

/** Source checks one source. */
export function validateSource(v: unknown, at = "source"): Problems {
  const p: Problems = [];
  if (
    !object(v, at, ["id", "url", "siteUrl", "title", "unread", "errorCount", "lastError", "polledAt", "nextPollAt"], p)
  ) {
    return p;
  }
  field(v, "id", at, p, isNumber);
  field(v, "url", at, p, isString);
  field(v, "siteUrl", at, p, isString, true);
  field(v, "title", at, p, isString);
  field(v, "unread", at, p, isNumber);
  field(v, "errorCount", at, p, isNumber, true);
  field(v, "lastError", at, p, isString, true);
  field(v, "polledAt", at, p, isString, true);
  field(v, "nextPollAt", at, p, isString, true);
  return p;
}

/** validateSources checks sources.list's result. */
export function validateSources(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "sources.list", ["sources"], p)) return p;
  checkList(v.sources, "sources.list.sources", p, validateSource);
  return p;
}

/** validateSourceResult checks sources.add's result. */
export function validateSourceResult(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "sources.add", ["source"], p)) return p;
  p.push(...validateSource(v.source, "sources.add.source"));
  return p;
}

/** summaryFields checks the members every summary has, a detail's too. */
function summaryFields(v: unknown, at: string, p: Problems): void {
  if (!isObject(v)) {
    p.push(`${at}: not an object`);
    return;
  }
  field(v, "id", at, p, isNumber);
  field(v, "title", at, p, isString);
  field(v, "sourceTitle", at, p, isString, true);
  field(v, "author", at, p, isString, true);
  field(v, "published", at, p, isString);
  field(v, "excerpt", at, p, isString, true);
  field(v, "read", at, p, isBoolean);
  field(v, "starred", at, p, isBoolean);
  field(v, "archived", at, p, isBoolean);
  field(v, "saved", at, p, isBoolean);
  field(v, "notePath", at, p, isString, true);
  field(v, "snippet", at, p, isString, true);
}

/** validateItemSummary checks one row. */
export function validateItemSummary(v: unknown, at = "item"): Problems {
  const p: Problems = [];
  if (
    !object(
      v,
      at,
      [
        "id",
        "title",
        "sourceTitle",
        "author",
        "published",
        "excerpt",
        "read",
        "starred",
        "archived",
        "saved",
        "notePath",
        "snippet",
      ],
      p,
    )
  ) {
    return p;
  }
  summaryFields(v, at, p);
  return p;
}

/** validatePage checks items.list's and items.search's result. */
export function validatePage(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "page", ["items", "next"], p)) return p;
  checkList(v.items, "page.items", p, validateItemSummary);
  field(v, "next", "page", p, isString, true);
  return p;
}

/** validateItemDetail checks items.get's result. */
export function validateItemDetail(v: unknown, at = "item"): Problems {
  const p: Problems = [];
  if (
    !object(
      v,
      at,
      [
        "id",
        "title",
        "sourceTitle",
        "author",
        "published",
        "excerpt",
        "read",
        "starred",
        "archived",
        "saved",
        "notePath",
        "snippet",
        "url",
        "contentPath",
        "extractState",
        "extractError",
        "originalHash",
      ],
      p,
    )
  ) {
    return p;
  }
  summaryFields(v, at, p);
  field(v, "url", at, p, isString);
  field(v, "contentPath", at, p, isString, true);
  field(v, "extractState", at, p, isExtractState);
  field(v, "extractError", at, p, isString, true);
  field(v, "originalHash", at, p, isString, true);
  return p;
}

/** validateStatus checks status. */
export function validateStatus(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "status", ["version", "notes"], p)) return p;
  field(v, "version", "status", p, isString);
  const notes = v.notes;
  if (!object(notes, "status.notes", ["configured", "vaultId", "folder"], p)) return p;
  field(notes, "configured", "status.notes", p, isBoolean);
  field(notes, "vaultId", "status.notes", p, isNumber);
  field(notes, "folder", "status.notes", p, isString);
  return p;
}

/** validateCounts checks counts. */
export function validateCounts(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "counts", ["views", "sources"], p)) return p;
  if (!object(v.views, "counts.views", VIEWS, p)) return p;
  for (const view of VIEWS) field(v.views, view, "counts.views", p, isNumber);
  checkList(v.sources, "counts.sources", p, (row, at) => {
    const q: Problems = [];
    if (!object(row, at, ["sourceId", "unread"], q)) return q;
    field(row, "sourceId", at, q, isNumber);
    field(row, "unread", at, q, isNumber);
    return q;
  });
  return p;
}

/** validateNoteStatus checks items.noteStatus's result. */
export function validateNoteStatus(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "noteStatus", ["state", "path", "route", "error"], p)) return p;
  field(
    v,
    "state",
    "noteStatus",
    p,
    (state) => state === "present" || state === "moved" || state === "missing" || state === "unreachable",
  );
  field(v, "path", "noteStatus", p, isString, true);
  field(v, "route", "noteStatus", p, isString, true);
  field(v, "error", "noteStatus", p, isString, true);
  return p;
}

/** validateNote checks items.saveAsNote's result. */
export function validateNote(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "note", ["path", "route"], p)) return p;
  field(v, "path", "note", p, isString);
  field(v, "route", "note", p, isString);
  return p;
}

/** validateImport checks opml.import's result. */
export function validateImport(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "opml.import", ["added", "skipped"], p)) return p;
  field(v, "added", "opml.import", p, isNumber);
  field(v, "skipped", "opml.import", p, isNumber);
  return p;
}

/** validateOPML checks opml.export's result. */
export function validateOPML(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "opml.export", ["xml"], p)) return p;
  field(v, "xml", "opml.export", p, isString);
  return p;
}

/** validateItemResult checks later.save's result. */
export function validateItemResult(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "later.save", ["item"], p)) return p;
  p.push(...validateItemSummary(v.item, "later.save.item"));
  return p;
}

/** validateSubscribe checks events.subscribe's result. */
export function validateSubscribe(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "events.subscribe", ["subscribed"], p)) return p;
  field(v, "subscribed", "events.subscribe", p, isBoolean);
  return p;
}

/** validateHello checks rpc.hello's result. */
export function validateHello(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "rpc.hello", ["protocol", "server", "caller"], p)) return p;
  field(v, "protocol", "rpc.hello", p, isNumber);
  field(v, "server", "rpc.hello", p, isString);
  field(v, "caller", "rpc.hello", p, (c) => c === "local" || c === "session" || c === "agent");
  return p;
}

/** validateNull checks a method whose result is null, such as a write. */
export function validateNull(v: unknown): Problems {
  return v === null ? [] : ["the result is not null"];
}

/** validateChanged checks items.changed's params. */
export function validateChanged(v: unknown): Problems {
  const p: Problems = [];
  if (!object(v, "items.changed", ["views"], p)) return p;
  checkList(v.views, "items.changed.views", p, (view, at) =>
    isView(view) ? [] : [`${at}: ${String(view)} is not a view`],
  );
  return p;
}

/** checkList checks every member of a list. */
function checkList(v: unknown, at: string, p: Problems, check: (value: unknown, at: string) => Problems): void {
  if (!Array.isArray(v)) {
    p.push(`${at}: not a list`);
    return;
  }
  v.forEach((value, i) => {
    p.push(...check(value, `${at}[${i}]`));
  });
}

/** checked returns raw as T, or fails the test with the problems found. */
export function checked<T>(raw: unknown, validate: (v: unknown) => Problems): T {
  const problems = validate(raw);
  if (problems.length > 0) throw new Error(problems.join("\n"));
  return raw as T;
}

/** The types a checked golden is held to at compile time. */
export type GoldenTypes = {
  status: Status;
  counts: Counts;
  source: Source;
  page: Page;
  detail: ItemDetail;
  note: Note;
  imported: ImportResult;
  changed: ItemsChanged;
};
