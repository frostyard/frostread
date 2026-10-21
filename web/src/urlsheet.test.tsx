// The Add-a-source and Save-a-link sheets show the daemon's work, close on
// success, and keep their failure in the sheet instead of a second one.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { UrlSheet } from "./components/UrlSheet.js";
import { readAPI } from "./rpc/api.js";
import { createMockTransport } from "./rpc/mock.js";
import { createReadStore } from "./store.js";

/** sheet renders the form with a submit that the test controls. */
function sheet(onSubmit: (url: string) => Promise<void>, onClose: () => void) {
  render(
    <UrlSheet
      title="Add a source"
      label="Feed or site address"
      placeholder="https://example.com/feed.xml"
      submitLabel="Add"
      busyLabel="Adding…"
      onSubmit={onSubmit}
      onClose={onClose}
    />,
  );
  fireEvent.change(screen.getByLabelText(/Feed or site address/), {
    target: { value: "https://example.com/feed.xml" },
  });
}

describe("the address sheet", () => {
  it("shows Adding…, then the failure, and stays open", async () => {
    let reject: (err: Error) => void = () => {};
    const pending = new Promise<void>((_, r) => {
      reject = r;
    });
    const onClose = vi.fn();
    sheet(() => pending, onClose);

    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    const busy = screen.getByRole("button", { name: "Adding…" }) as HTMLButtonElement;
    expect(busy.disabled).toBe(true);

    reject(new Error("Read does not fetch addresses inside your network (192.0.2.1)."));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("192.0.2.1");
    expect(onClose).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole("button", { name: "Add" })).toBeTruthy());
  });

  it("closes the sheet when the daemon answers", async () => {
    const onClose = vi.fn();
    sheet(async () => {}, onClose);
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
  });
});

describe("the store's address actions", () => {
  it("pass a failure to the sheet and leave the error sheet alone", async () => {
    const store = createReadStore();
    const api = readAPI(createMockTransport());
    const failing = {
      ...api,
      addSource: async () => {
        throw new Error("Read does not fetch addresses inside your network (192.0.2.1).");
      },
      saveLater: async () => {
        throw new Error("Read does not fetch addresses inside your network (192.0.2.1).");
      },
    };
    await store.getState().connect(failing);

    await expect(store.getState().addSource("http://192.0.2.1/feed.xml")).rejects.toThrow("192.0.2.1");
    await expect(store.getState().saveLater("http://192.0.2.1/page")).rejects.toThrow("192.0.2.1");
    expect(store.getState().error).toBeNull();
  });
});
