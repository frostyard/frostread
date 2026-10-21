// Shift+A asks before a mark-all that changes many items: the same confirm
// sheet the list's button opens (docs/specs/api.md, items.markAllRead).
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { App } from "./components/App.js";
import { StoreProvider } from "./components/context.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

describe("Shift+A", () => {
  it("asks first, and marks the items after the answer", async () => {
    const store = createReadStore();
    await store.getState().connect(readAPI(createMockTransport()));
    const before = store.getState().counts.views.unread;
    expect(before).toBeGreaterThan(20);
    render(
      <StoreProvider store={store}>
        <App />
      </StoreProvider>,
    );
    fireEvent.keyDown(window, { key: "A", shiftKey: true });
    expect(await screen.findByText(`Mark ${before} items as read?`)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Mark as read" }));
    await waitFor(() => expect(store.getState().counts.views.unread).toBe(0));
    expect(screen.queryByText(`Mark ${before} items as read?`)).toBeNull();
  });
});
