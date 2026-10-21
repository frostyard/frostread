// The routes the shell sends and the app reports (docs/specs/api.md, and
// the brief's list).

import { frameRoute } from "@frostyard/lodge-client";
import { describe, expect, it } from "vitest";
import { formatRoute, noteRoute, parseRoute } from "./lib/routes.js";

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
