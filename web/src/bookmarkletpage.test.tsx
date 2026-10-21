// The page that hands out the bookmarklet (ADR-0006): the steps for iOS
// Safari, the code with the frame's origin, and a link that can be dragged.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { BookmarkletPage } from "./components/BookmarkletPage.js";
import { StoreProvider } from "./components/context.js";
import { createReadStore } from "./store.js";

/** page renders the page for a made-up lodge origin. */
function page() {
  const store = createReadStore();
  render(
    <StoreProvider store={store}>
      <BookmarkletPage origin="https://lodge.example" />
    </StoreProvider>,
  );
  return store;
}

describe("the bookmarklet page", () => {
  it("says how to install it in iOS Safari", () => {
    page();
    expect(screen.getByRole("heading", { name: "Save from your phone" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "iPhone and iPad (Safari)" })).toBeTruthy();
    expect(screen.getByText("Tap Copy above to copy the bookmarklet.")).toBeTruthy();
    expect(screen.getByText(/tap the Share button/)).toBeTruthy();
    expect(screen.getByText(/Clear its address, paste the copied text, and tap Done\./)).toBeTruthy();
    expect(screen.getByText(/Read shows the page's title and address\. Tap Save\./)).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Desktop browsers" })).toBeTruthy();
    expect(screen.getByText(/The bookmarklet holds no password or token\./)).toBeTruthy();
  });

  it("shows the code with the frame's origin, and a link that carries it", () => {
    page();
    const code = screen.getByLabelText("Bookmarklet code") as HTMLTextAreaElement;
    expect(code.value.startsWith("javascript:")).toBe(true);
    expect(code.value).toContain("https://lodge.example/read/save?url=");
    expect(code.value).not.toContain("\n");
    const link = screen.getByRole("link", { name: "Save to Read" });
    expect(link.getAttribute("href")).toBe(code.value);
  });

  it("says to drag the link when it is clicked inside the app", () => {
    page();
    fireEvent.click(screen.getByRole("link", { name: "Save to Read" }));
    expect(screen.getByRole("status").textContent).toContain("Drag this link to your bookmarks bar.");
  });

  it("goes back to the unread view", async () => {
    const store = page();
    fireEvent.click(screen.getByRole("button", { name: "Back to Read" }));
    await waitFor(() => expect(store.getState().route).toEqual({ kind: "view", view: "unread" }));
  });
});
