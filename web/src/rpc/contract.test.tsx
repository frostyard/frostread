// readd's own answers, checked against the web app's types and the
// components that read them. The golden files come from the Go daemon
// (internal/readapi/contract_test.go, `make contract-update`), so this test
// fails when the two sides drift: a renamed member, a field that is absent
// when zero, or a component that assumes it is always there.
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StoreProvider } from "../components/context.js";
import { ErrorBoundary } from "../components/ErrorBoundary.js";
import { ItemRow } from "../components/ItemRow.js";
import { Reader } from "../components/Reader.js";
import { SourceRow } from "../components/Sidebar.js";
import { createReadStore } from "../store.js";
import type {
  Counts,
  ImportResult,
  ItemDetail,
  ItemRender,
  ItemSummary,
  Note,
  NoteStatus,
  Source,
  Status,
} from "./api.js";
import {
  checked,
  type Problems,
  validateCounts,
  validateHello,
  validateImport,
  validateItemDetail,
  validateItemResult,
  validateNote,
  validateNoteStatus,
  validateNull,
  validateOPML,
  validatePage,
  validateRender,
  validateSourceResult,
  validateSources,
  validateStatus,
  validateSubscribe,
} from "./contract.js";
import countsJSON from "./testdata/counts.json";
import subscribeJSON from "./testdata/events.subscribe.json";
import doneJSON from "./testdata/items.get.done.json";
import failedJSON from "./testdata/items.get.failed.json";
import savedJSON from "./testdata/items.get.saved.json";
import allJSON from "./testdata/items.list.all.json";
import unreadJSON from "./testdata/items.list.unread.json";
import noteStatusJSON from "./testdata/items.noteStatus.json";
import renderJSON from "./testdata/items.render.json";
import saveAsNoteJSON from "./testdata/items.saveAsNote.json";
import searchJSON from "./testdata/items.search.json";
import laterJSON from "./testdata/later.save.json";
import exportJSON from "./testdata/opml.export.json";
import importJSON from "./testdata/opml.import.json";
import helloJSON from "./testdata/rpc.hello.json";
import addJSON from "./testdata/sources.add.json";
import sourcesJSON from "./testdata/sources.list.json";
import statusJSON from "./testdata/status.json";

afterEach(() => {
  vi.restoreAllMocks();
});

/** The goldens, each held to its TypeScript type at run time. */
const status: Status = checked(statusJSON, validateStatus);
const sources: Source[] = checked<{ sources: Source[] }>(sourcesJSON, validateSources).sources;
const added: Source = checked<{ source: Source }>(addJSON, validateSourceResult).source;
const all: ItemSummary[] = checked<{ items: ItemSummary[] }>(allJSON, validatePage).items;
const unread: ItemSummary[] = checked<{ items: ItemSummary[] }>(unreadJSON, validatePage).items;
const found: ItemSummary[] = checked<{ items: ItemSummary[] }>(searchJSON, validatePage).items;
const done: ItemDetail = checked<ItemDetail>(doneJSON, validateItemDetail);
const failed: ItemDetail = checked<ItemDetail>(failedJSON, validateItemDetail);
const saved: ItemDetail = checked<ItemDetail>(savedJSON, validateItemDetail);
const later: ItemSummary = checked<{ item: ItemSummary }>(laterJSON, validateItemResult).item;
const note: Note = checked<Note>(saveAsNoteJSON, validateNote);
const rendered: ItemRender = checked<ItemRender>(renderJSON, validateRender);

describe("the daemon's answers match api.ts", () => {
  it("rpc.hello", () => {
    expect(validateHello(helloJSON)).toEqual([]);
  });

  it("status", () => {
    expect(status.notes.folder).toBe("Reading/");
  });

  it("counts", () => {
    const counts = checked<Counts>(countsJSON, validateCounts);
    expect(counts.views.unread).toBeGreaterThan(0);
  });

  it("sources.list: a source without errors has no lastError", () => {
    expect(sources.length).toBeGreaterThan(1);
    expect(sources.some((source) => source.lastError === undefined)).toBe(true);
    expect(sources.some((source) => source.lastError !== undefined)).toBe(true);
    expect(sources.some((source) => source.siteUrl === undefined)).toBe(true);
    expect(added.lastError).toBeUndefined();
  });

  it("items.list and items.search", () => {
    expect(all.length).toBeGreaterThan(0);
    expect(unread.length).toBeGreaterThan(0);
    expect(found.some((item) => item.snippet !== undefined)).toBe(true);
  });

  it("items.get: every state, and a saved link with no source", () => {
    expect(done.extractState).toBe("done");
    expect(done.excerpt).toBeDefined();
    expect(failed.extractState).toBe("failed");
    expect(failed.extractError).toBeDefined();
    expect(saved.saved).toBe(true);
    expect(saved.sourceTitle).toBeUndefined();
    expect(saved.author).toBeUndefined();
    expect(saved.excerpt).toBeUndefined();
    expect(saved.notePath).toBeUndefined();
    expect(saved.extractState).toBe("pending");
  });

  it("items.noteStatus: a note it found is present, with its route", () => {
    const status = checked<NoteStatus>(noteStatusJSON, validateNoteStatus);
    expect(status.state).toBe("present");
    expect(status.path?.endsWith(".md")).toBe(true);
    expect(status.route?.startsWith("/notes/1/")).toBe(true);
  });

  it("items.render: the desktop's page, its CSP first, no image left out", () => {
    expect(rendered.html.startsWith("<!doctype html>")).toBe(true);
    // The CSP meta is the head's first element, before any markup it governs.
    const head = rendered.html.slice(rendered.html.indexOf("<head>") + "<head>".length).trimStart();
    expect(
      head.startsWith(
        "<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; img-src data:; style-src 'unsafe-inline'\">",
      ),
    ).toBe(true);
    expect(rendered.omittedImages).toBe(0);
  });

  it("items.saveAsNote and later.save", () => {
    expect(note.path.endsWith(".md")).toBe(true);
    expect(note.route.startsWith("/notes/")).toBe(true);
    expect(later.id).toBeGreaterThan(0);
  });

  it("opml, events.subscribe and the writes whose result is null", () => {
    expect(checked<{ xml: string }>(exportJSON, validateOPML).xml).toContain("<opml");
    expect(checked<ImportResult>(importJSON, validateImport).skipped).toBeGreaterThanOrEqual(0);
    expect(validateSubscribe(subscribeJSON)).toEqual([]);
    expect(validateNull(null)).toEqual([]);
    expect(validateNull({})).not.toEqual([]);
  });

  it("a member the daemon does not send is a problem the checks find", () => {
    const extra = { ...status, unknownMember: true };
    expect(validateStatus(extra)).not.toEqual([]);
    const renamed = { ...(sources[0] ?? {}), polled_at: "2026-10-20T12:00:00Z" };
    expect(validateSources({ sources: [renamed] })).not.toEqual([]);
    const problems: Problems = validateItemDetail({ ...saved, notePath: "" });
    expect(problems.length).toBe(0); // an empty notePath is a string, so it is legal
  });
});

describe("the components read the daemon's answers", () => {
  it("renders every source, its warning only when a poll failed", () => {
    for (const source of sources) {
      const { unmount } = render(
        <SourceRow source={source} unread={source.unread} current={false} onClick={() => {}} />,
      );
      const failedPoll = source.lastError !== undefined && source.lastError !== "";
      expect(screen.queryByLabelText(/^Last error: /) !== null).toBe(failedPoll);
      if (!failedPoll) {
        // The tooltip falls back to the feed's address, never "undefined".
        expect(screen.getByRole("button").getAttribute("title")).toBe(source.url);
      }
      unmount();
    }
  });

  it("renders every item, with or without an excerpt or a source", () => {
    for (const item of [...all, ...unread, ...found, later]) {
      const { unmount } = render(<ItemRow item={item} selected={false} onSelect={() => {}} />);
      expect(screen.getByText(item.title)).toBeTruthy();
      unmount();
    }
    // A row with no excerpt shows nothing in its excerpt line, and does not
    // throw: the excerpt is absent from the daemon's answer.
    const { unmount } = render(<ItemRow item={saved} selected={false} onSelect={() => {}} />);
    expect(screen.getByText("Saved link", { exact: false })).toBeTruthy();
    unmount();
  });

  it("opens a saved link that has no note and no excerpt: no crash", () => {
    const store = createReadStore();
    store.setState({ item: saved, status, selectedId: saved.id });
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    expect(screen.getByRole("heading", { name: saved.title })).toBeTruthy();
    expect(screen.getByText("Extracting…")).toBeTruthy();
    // noteRoute is null for an item without a notePath, so the button is
    // Save as note, not Open in Notes.
    expect(screen.getByRole("button", { name: "Save as note" })).toBeTruthy();
  });

  it("opens a failed article with its error and its excerpt", () => {
    const store = createReadStore();
    store.setState({ item: failed, status, selectedId: failed.id });
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    expect(screen.getByText(failed.extractError ?? "missing")).toBeTruthy();
    expect(screen.getByText(failed.excerpt ?? "missing")).toBeTruthy();
  });

  it("opens a done article without a lodge shell as its text", () => {
    const store = createReadStore();
    store.setState({ item: done, status, selectedId: done.id });
    render(
      <StoreProvider store={store}>
        <Reader />
      </StoreProvider>,
    );
    expect(screen.getByRole("heading", { name: done.title })).toBeTruthy();
    expect(screen.getByText(done.excerpt ?? "missing")).toBeTruthy();
  });
});

describe("the error boundary", () => {
  it("shows the error and a Reload button instead of a blank page", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    function Boom(): never {
      throw new Error("the render failed");
    }
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert").textContent).toContain("the render failed");
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });
});
