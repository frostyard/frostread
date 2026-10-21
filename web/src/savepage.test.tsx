// The save page the bookmarklet opens (ADR-0006, docs/specs/api.md, Web
// routes): it shows the page's title and address as text, and saves only
// when the owner taps Save.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { SavePage } from "./components/SavePage.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

const URL = "https://example.com/a-page";
const TITLE = "A <b>bold</b> page";

/** page renders the Save page for an address and a title. */
async function page(url: string, title: string, api = readAPI(createMockTransport())) {
  const store = createReadStore();
  await store.getState().connect(api);
  render(
    <StoreProvider store={store}>
      <SavePage url={url} title={title} />
    </StoreProvider>,
  );
  return store;
}

describe("the save page", () => {
  it("shows the title and the address as text, and the address's host", async () => {
    await page(URL, TITLE);
    expect(screen.getByText(TITLE)).toBeTruthy();
    expect(screen.getByText(URL)).toBeTruthy();
    expect(screen.getByText("example.com")).toBeTruthy();
    // A title with markup stays text: React never makes an element of it.
    expect(document.querySelector("b")).toBeNull();
  });

  it("saves nothing until Save is tapped, and then shows Saved", async () => {
    const api = readAPI(createMockTransport());
    const saveLater = vi.fn((url: string, title?: string) => api.saveLater(url, title));
    await page(URL, "A page from my phone", { ...api, saveLater });
    expect(saveLater).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Saved")).toBeTruthy();
    expect(saveLater).toHaveBeenCalledOnce();
    expect(saveLater).toHaveBeenCalledWith(URL, "A page from my phone");

    // The item's title is the page's title, and both ways on are offered.
    expect(screen.getByText("A page from my phone")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open in Read" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Read later" })).toBeTruthy();
    expect(screen.getByText(/Use your browser's Back button to return to the page\./)).toBeTruthy();
  });

  it("shows a refusal in an alert, and keeps the Save button", async () => {
    const api = readAPI(createMockTransport());
    const saveLater = async () => {
      throw new Error("Read does not fetch addresses inside your network (192.0.2.1).");
    };
    await page("http://192.0.2.1/page", "", { ...api, saveLater });

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("192.0.2.1");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    expect(screen.queryByText("Saved")).toBeNull();
  });

  it("says Saving… while the daemon works, and takes no second tap", async () => {
    let answer: () => void = () => {};
    const pending = new Promise<void>((resolve) => {
      answer = resolve;
    });
    const saveLater = vi.fn(async () => {
      await pending;
      return {
        id: 1,
        title: "A page",
        published: "2026-10-21T12:00:00Z",
        read: false,
        starred: false,
        archived: false,
        saved: true,
      };
    });
    await page(URL, "A page", { ...readAPI(createMockTransport()), saveLater });

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const busy = screen.getByRole("button", { name: "Saving…" }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);
    fireEvent.click(busy);
    expect(saveLater).toHaveBeenCalledOnce();
    answer();
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
  });

  it("says there is no page to save, and offers only the way back", async () => {
    const store = await page("", "");
    expect(screen.getByText("No page to save.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Back to Read" }));
    await waitFor(() => expect(store.getState().route).toEqual({ kind: "view", view: "unread" }));
  });

  it("goes back to the unread view on Cancel, and saves nothing", async () => {
    const api = readAPI(createMockTransport());
    const saveLater = vi.fn(api.saveLater);
    const store = await page(URL, "A page", { ...api, saveLater });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(store.getState().route).toEqual({ kind: "view", view: "unread" }));
    expect(saveLater).not.toHaveBeenCalled();
  });
});
