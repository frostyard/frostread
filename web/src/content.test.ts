// The article's content URL, as lodge-client builds it (lodge-client.md:
// contentOrigin + "/" + app + "/" + path; the path has no leading slash).
// The daemon sends contentPath relative to the content root, and the app's
// content handler answers /content/<contentPath> (lodge-http.md, content
// routes). A path with a "content/" segment or a leading slash would build
// CONTENT/read/content/…, which answers 404.
import { afterEach, describe, expect, it } from "vitest";
import { contentSrc, setLodge } from "./lodge.js";
import done from "./rpc/testdata/items.get.done.json";

afterEach(() => {
  setLodge(null);
});

describe("an article's content URL", () => {
  it("is CONTENT/read/<contentPath>, with the path the daemon sent", () => {
    const paths: string[] = [];
    setLodge({
      open: () => {},
      route: () => {},
      contentURL: (path: string) => {
        paths.push(path);
        return `http://content.example/read/${path}`;
      },
    });
    const src = contentSrc("item/3/anExampleMac");
    expect(src).toBe("http://content.example/read/item/3/anExampleMac");
    expect(paths).toEqual(["item/3/anExampleMac"]);
    // The shell's theme goes to the content page as ?scheme=.
    expect(contentSrc("item/3/anExampleMac", "dark")).toBe(
      "http://content.example/read/item/3/anExampleMac?scheme=dark",
    );
    expect(contentSrc("item/3/anExampleMac", "light")).toBe(
      "http://content.example/read/item/3/anExampleMac?scheme=light",
    );
  });

  it("holds a golden's contentPath, which has no leading slash or content/ segment", () => {
    expect(done.contentPath).toBeDefined();
    const path = done.contentPath ?? "";
    expect(path.startsWith("/")).toBe(false);
    expect(path.startsWith("content/")).toBe(false);
    expect(path.startsWith("item/")).toBe(true);
  });

  it("is null without a shell, and for a path the client would refuse", () => {
    expect(contentSrc("item/3/mac")).toBeNull();
    setLodge({ open: () => {}, route: () => {}, contentURL: (path) => path });
    expect(contentSrc(undefined)).toBeNull();
    expect(contentSrc("")).toBeNull();
    expect(contentSrc("/content/item/3/mac")).toBeNull();
  });

  it("the client refuses a leading slash, so the app never sends one", () => {
    // lodge-client's contentURL throws on a leading slash.
    const contentURL = (path: string): string => {
      if (path.startsWith("/")) throw new TypeError("a content path has no leading /");
      return `http://content.example/read/${path}`;
    };
    expect(() => contentURL("/item/3/mac")).toThrow(TypeError);
    expect(contentURL("item/3/mac")).toBe("http://content.example/read/item/3/mac");
  });
});
