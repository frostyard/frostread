// The OPML export's download (docs/specs/api.md, opml.export): a Blob URL,
// named frostread.opml, clicked once.
import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadOPML } from "./lib/download.js";

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("downloadOPML", () => {
  it("builds a download of the export, named frostread.opml", () => {
    vi.useFakeTimers();
    const create = vi.fn((_blob: Blob) => "blob:frostread");
    const revoke = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: create });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoke });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    const link = downloadOPML('<?xml version="1.0"?><opml version="2.0"/>');

    expect(create).toHaveBeenCalledOnce();
    expect(create.mock.calls[0]?.[0].type).toBe("text/x-opml");
    expect(link.download).toBe("frostread.opml");
    expect(link.getAttribute("href")).toBe("blob:frostread");
    expect(link.rel).toBe("noopener");
    expect(click).toHaveBeenCalledOnce();

    vi.runAllTimers();
    expect(revoke).toHaveBeenCalledWith("blob:frostread");
  });
});
