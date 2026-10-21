// The bookmarklet's code (ADR-0006): one line of JavaScript that opens
// lodge's save page with the page's address and title, and the address it
// computes, which the code mirrors.

import { describe, expect, it } from "vitest";
import { bookmarkletCode, originOf, saveURL } from "./lib/bookmarklet.js";

const ORIGIN = "https://lodge.example";

describe("the bookmarklet's code", () => {
  it("is one line of JavaScript that holds the frame's origin", () => {
    const code = bookmarkletCode(ORIGIN);
    expect(code.startsWith("javascript:")).toBe(true);
    expect(code).not.toContain("\n");
    expect(code).not.toContain("\r");
    expect(code).toContain("https://lodge.example/read/save?url=");
  });

  it("computes the address saveURL computes", () => {
    const code = bookmarkletCode(ORIGIN);
    // Both build the same base, and the code appends the same two encoded
    // pieces in the same order.
    expect(code).toContain(`location.href='${saveURL(ORIGIN, "", "")}'+u+(t?'&title='+t:'')`);
    expect(code).toContain("u=encodeURIComponent(location.href)");
    expect(code).toContain("(document.title||'').slice(0,200)");
  });

  it("names no host of its own, and quotes the origin it is given", () => {
    expect(bookmarkletCode("https://read.example")).toContain("'https://read.example/read/save?url='");
    // A quote in a path is not part of the origin, so it cannot end the
    // literal: the origin is normalized first.
    expect(bookmarkletCode("https://lodge.example/'")).toContain("'https://lodge.example/read/save?url='");
  });

  it("refuses an origin that is not http or https with a host", () => {
    for (const bad of ["file:///tmp/page", "not an address", "", "https://", "ftp://lodge.example"]) {
      expect(() => bookmarkletCode(bad), bad).toThrow();
    }
  });

  it("writes an origin as a URL does", () => {
    expect(originOf("https://lodge.example/read/")).toBe("https://lodge.example");
    expect(originOf("http://127.0.0.1:8088")).toBe("http://127.0.0.1:8088");
  });
});

describe("the address the bookmarklet opens", () => {
  it("encodes the page's address and its title", () => {
    expect(saveURL(ORIGIN, "https://example.com/a page?x=1&y=2", "A page & more")).toBe(
      `${ORIGIN}/read/save?url=https%3A%2F%2Fexample.com%2Fa%20page%3Fx%3D1%26y%3D2&title=A%20page%20%26%20more`,
    );
  });

  it("cuts the title to 200 characters, and leaves it out when the query is too long", () => {
    const named = saveURL(ORIGIN, "https://example.com/", "é".repeat(300));
    expect(named.slice(named.indexOf("&title=") + "&title=".length)).toBe(encodeURIComponent("é".repeat(200)));

    const long = saveURL(ORIGIN, `https://example.com/${"a".repeat(2000)}`, "A title");
    expect(long).not.toContain("&title=");
    expect(long.startsWith(`${ORIGIN}/read/save?url=`)).toBe(true);
  });

  it("leaves out an empty title", () => {
    expect(saveURL(ORIGIN, "https://example.com/", "")).toBe(`${ORIGIN}/read/save?url=https%3A%2F%2Fexample.com%2F`);
  });
});
