import { describe, expect, it } from "vitest";
import { importMessage } from "./importmessage.js";

describe("importMessage", () => {
  it.each([
    [{ added: 1, skipped: 0 }, "Imported 1 feed."],
    [{ added: 2, skipped: 0 }, "Imported 2 feeds."],
    [{ added: 0, skipped: 2 }, "No feeds imported. Skipped 2: already a source, or an address Read cannot reach."],
    [{ added: 1, skipped: 1 }, "Imported 1 feed. Skipped 1: already a source, or an address Read cannot reach."],
    [{ added: 2, skipped: 1 }, "Imported 2 feeds. Skipped 1: already a source, or an address Read cannot reach."],
    [{ added: 0, skipped: 0 }, "No feeds imported. The file names no feeds."],
  ])("describes %j", (result, message) => {
    expect(importMessage(result)).toBe(message);
  });
});
