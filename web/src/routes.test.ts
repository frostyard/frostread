// The routes the shell sends and the app reports (docs/specs/api.md, and
// the brief's list).

import { frameRoute } from "@frostyard/lodge-client";
import { describe, expect, it } from "vitest";
import { formatRoute, noteRoute, parseRoute, routeTitle } from "./lib/routes.js";

describe("routes", () => {
  it("parses an item, a source and a view", () => {
    expect(parseRoute("/item/7")).toEqual({ kind: "item", id: 7 });
    expect(parseRoute("/source/3")).toEqual({ kind: "source", id: 3 });
    expect(parseRoute("/view/later")).toEqual({ kind: "view", view: "later" });
    expect(parseRoute("/item/7#top")).toEqual({ kind: "item", id: 7 });
  });

  it("takes / as the unread view, and anything it does not know", () => {
    for (const path of ["", "/", "/nonsense", "/view/nope", "/item/seven", "/source/"]) {
      expect(parseRoute(path), path).toEqual({ kind: "view", view: "unread" });
    }
  });

  it("writes the routes it parses", () => {
    for (const path of ["/item/7", "/source/3", "/view/later", "/"]) {
      expect(formatRoute(parseRoute(path)), path).toBe(path);
    }
  });

  it("names the route that opens a written note", () => {
    expect(noteRoute("Reading/Why feeds are still good.md", 1)).toBe(
      "/notes/1/Reading/Why%20feeds%20are%20still%20good.md",
    );
    expect(noteRoute("", 2)).toBe("/notes/2/");
  });
});

describe("a cold load's route", () => {
  // lodge-client frames an app at /app/<id><route> and gives the app its
  // own route from that address (lodge-client.md, Routes; frameRoute).
  it("comes from the frame's own address, not from a report", () => {
    const loc = (pathname: string) => ({ pathname, search: "", hash: "" });
    expect(parseRoute(frameRoute("read", loc("/app/read/item/3")))).toEqual({ kind: "item", id: 3 });
    expect(parseRoute(frameRoute("read", loc("/app/read/view/starred")))).toEqual({
      kind: "view",
      view: "starred",
    });
    expect(parseRoute(frameRoute("read", loc("/app/read/source/2")))).toEqual({ kind: "source", id: 2 });
    expect(parseRoute(frameRoute("read", loc("/app/read/")))).toEqual({ kind: "view", view: "unread" });
    expect(parseRoute(frameRoute("read", loc("/app/read")))).toEqual({ kind: "view", view: "unread" });
  });
});

describe("the save page the bookmarklet opens", () => {
  const path = "/save?url=https%3A%2F%2Fexample.com%2Fa-page&title=A%20page";

  it("parses its address and title, and writes the path it arrived at", () => {
    const route = parseRoute(path);
    expect(route).toEqual({
      kind: "save",
      url: "https://example.com/a-page",
      title: "A page",
      path,
    });
    expect(formatRoute(route)).toBe(path);
  });

  it("takes a save page with no title, and one with no address", () => {
    expect(parseRoute("/save?url=https%3A%2F%2Fexample.com%2Fa-page")).toEqual({
      kind: "save",
      url: "https://example.com/a-page",
      title: "",
      path: "/save?url=https%3A%2F%2Fexample.com%2Fa-page",
    });
    expect(parseRoute("/save")).toEqual({ kind: "save", url: "", title: "", path: "/save" });
    expect(parseRoute("/save?title=A%20page")).toMatchObject({ kind: "save", url: "", title: "A page" });
  });

  it("decodes a + and a %20 in the query, and keeps them in the path it reports", () => {
    const route = parseRoute("/save?url=https%3A%2F%2Fexample.com%2Fa+b&title=A+B%20page");
    expect(route).toMatchObject({ kind: "save", url: "https://example.com/a b", title: "A B page" });
    expect(formatRoute(route)).toBe("/save?url=https%3A%2F%2Fexample.com%2Fa+b&title=A+B%20page");
  });

  it("round trips a save path exactly, so the shell records no new entry", () => {
    for (const arrived of ["/save", path, "/save?url=a+b", "/save?url=%2F%3F%23&title="]) {
      expect(formatRoute(parseRoute(arrived)), arrived).toBe(arrived);
    }
  });

  it("names the save page and the bookmarklet page for the shell", () => {
    expect(routeTitle(parseRoute(path), [])).toBe("Save to Read");
    expect(routeTitle(parseRoute("/bookmarklet"), [])).toBe("Save from your phone");
  });
});

describe("the bookmarklet page", () => {
  it("parses /bookmarklet and writes it back", () => {
    expect(parseRoute("/bookmarklet")).toEqual({ kind: "bookmarklet" });
    expect(formatRoute({ kind: "bookmarklet" })).toBe("/bookmarklet");
  });

  it("stays strict about anything else", () => {
    for (const other of ["/bookmarklets", "/bookmarklet/x", "/save/x"]) {
      expect(parseRoute(other), other).toEqual({ kind: "view", view: "unread" });
    }
  });
});
