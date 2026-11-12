// The Server sheet, its exact local-facts text, save arguments and failure.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { StoreProvider } from "./components/context.js";
import { normalizeAddress, ServerSheet, serverChangeText } from "./components/ServerSheet.js";
import { Sidebar } from "./components/Sidebar.js";
import { mockServer, tauriServer } from "./desktop/server.js";
import { type ServerControl, ShellProvider } from "./shell.js";
import { createReadStore } from "./store.js";

function server(
  answer: { lodge: string; lastLodge?: string; localData: boolean },
  overrides: Partial<ServerControl> = {},
): ServerControl {
  return {
    lodge: answer.lodge,
    known: true,
    get: vi.fn(async () => ({ lastLodge: "", ...answer })),
    save: vi.fn(async () => {}),
    reload: vi.fn(),
    ...overrides,
  };
}

function renderSheet(control: ServerControl, onClose = vi.fn()) {
  render(<ServerSheet server={control} onClose={onClose} />);
  return onClose;
}

describe("the Server sheet", () => {
  it("uses the Tauri server commands and an in-memory browser server", async () => {
    const calls: Array<[string, Record<string, unknown> | undefined]> = [];
    async function invoke<T>(command: string, args?: Record<string, unknown>): Promise<T> {
      calls.push([command, args]);
      if (command === "server_get")
        return { lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true } as T;
      return undefined as T;
    }
    const location = { href: "tauri://localhost/desktop.html#/item/4", reload: vi.fn(), replace: vi.fn() };
    const win = { location } as unknown as Window;
    const control = tauriServer(invoke, win, "https://lodge.example.ts.net", true);
    await expect(control.get()).resolves.toEqual({
      lodge: "https://lodge.example.ts.net",
      lastLodge: "",
      localData: true,
    });
    await control.save("https://two.example");
    control.reload();
    expect(calls).toEqual([
      ["server_get", undefined],
      ["server_save", { lodge: "https://two.example" }],
    ]);
    // The reload loads the start address: the fragment's route belongs to the
    // reader the window leaves.
    expect(location.replace).toHaveBeenCalledExactlyOnceWith("tauri://localhost/desktop.html");
    expect(location.reload).not.toHaveBeenCalled();

    const mock = mockServer("", false);
    await expect(mock.get()).resolves.toEqual({ lodge: "", lastLodge: "", localData: false });
    await mock.save("https://lodge.example.ts.net");
    await expect(mock.get()).resolves.toEqual({
      lodge: "https://lodge.example.ts.net",
      lastLodge: "",
      localData: false,
    });
    // A switch to local keeps the address it leaves, as app.json does.
    await mock.save("");
    await expect(mock.get()).resolves.toEqual({
      lodge: "",
      lastLodge: "https://lodge.example.ts.net",
      localData: false,
    });
  });

  it("offers the last lodge address in local mode, and saves it after the Lodge radio", async () => {
    const local = server({ lodge: "", lastLodge: "https://lodge.example.ts.net", localData: true });
    renderSheet(local);
    await screen.findByRole("radiogroup", { name: "Reader" });

    const input = screen.getByLabelText("Lodge's address") as HTMLInputElement;
    expect(input.value).toBe("https://lodge.example.ts.net");
    expect(input.disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByLabelText("Lodge"));
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(local.save).toHaveBeenCalledWith("https://lodge.example.ts.net"));
  });

  it("shows Loading and the loading facts, then the exact change texts", async () => {
    let resolve: (answer: { lodge: string; lastLodge: string; localData: boolean }) => void = () => {};
    const pending = new Promise<{ lodge: string; lastLodge: string; localData: boolean }>((r) => {
      resolve = r;
    });
    renderSheet({ ...server({ lodge: "", lastLodge: "", localData: true }), get: vi.fn(() => pending) });
    expect(screen.getByText("Loading…")).toBeTruthy();
    resolve({ lodge: "", lastLodge: "", localData: true });
    await screen.findByRole("radiogroup", { name: "Reader" });

    expect(serverChangeText({ lodge: "", lastLodge: "", localData: true }, false, "")).toBe(
      "Frostread uses this computer's reader.",
    );
    expect(serverChangeText({ lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true }, false, "")).toBe(
      "This computer's reader starts again with the sources and articles it had. Nothing moves from lodge.",
    );
    expect(
      serverChangeText({ lodge: "https://lodge.example.ts.net", lastLodge: "", localData: false }, false, ""),
    ).toBe(
      "This computer starts an empty reader. Your list on lodge stays there. To bring your subscriptions, export OPML from Read in lodge and import it here.",
    );
    expect(serverChangeText({ lodge: "", lastLodge: "", localData: true }, true, "https://lodge.example.ts.net")).toBe(
      "This computer's reader stops polling. Its sources and saved articles stay on this computer, not synced, until you switch back. Nothing moves to lodge.",
    );
    expect(
      serverChangeText({ lodge: "https://one.example", lastLodge: "", localData: true }, true, "https://one.example/"),
    ).toBe("Frostread uses the lodge at https://one.example.");
    expect(
      serverChangeText({ lodge: "https://one.example", lastLodge: "", localData: true }, true, "https://two.example"),
    ).toBe("Frostread uses the lodge at https://two.example instead. Nothing moves between the two.");
  });

  it("shows get failure in place", async () => {
    renderSheet(
      server(
        { lodge: "", lastLodge: "", localData: true },
        {
          get: vi.fn(async () => {
            throw new Error("cannot read mode");
          }),
        },
      ),
    );
    expect((await screen.findByRole("alert")).textContent).toContain("cannot read mode");
  });

  it("compares addresses after trim and one trailing slash", () => {
    expect(normalizeAddress("  https://lodge.example.ts.net/  ")).toBe("https://lodge.example.ts.net");
  });

  it("disables Save for no change and an empty Lodge address", async () => {
    renderSheet(server({ lodge: "", lastLodge: "", localData: true }));
    await screen.findByRole("radiogroup", { name: "Reader" });
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByLabelText("Lodge"));
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Lodge's address"), { target: { value: "https://lodge.example.ts.net" } });
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);

    const lodge = server({ lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true });
    renderSheet(lodge);
    await screen.findAllByRole("radiogroup", { name: "Reader" });
    const saves = screen.getAllByRole("button", { name: "Save" });
    expect((saves.at(-1) as HTMLButtonElement).disabled).toBe(true);
  });

  it("saves local mode and a Lodge address, and reloads after success", async () => {
    const toLocal = server({ lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true });
    renderSheet(toLocal);
    await screen.findByRole("radiogroup", { name: "Reader" });
    fireEvent.click(screen.getByLabelText("This computer"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toLocal.save).toHaveBeenCalledWith(""));
    expect(toLocal.reload).toHaveBeenCalledOnce();

    const toLodge = server({ lodge: "", lastLodge: "", localData: true });
    renderSheet(toLodge);
    await screen.findAllByRole("radiogroup", { name: "Reader" });
    const lodgeRadios = screen.getAllByLabelText("Lodge");
    const lodgeRadio = lodgeRadios.at(-1);
    if (lodgeRadio === undefined) throw new Error("no Lodge radio");
    fireEvent.click(lodgeRadio);
    const inputs = screen.getAllByLabelText("Lodge's address");
    const input = inputs.at(-1);
    if (input === undefined) throw new Error("no Lodge address");
    fireEvent.change(input, { target: { value: "  https://lodge.example.ts.net/  " } });
    const saves = screen.getAllByRole("button", { name: "Save" });
    const save = saves.at(-1);
    if (save === undefined) throw new Error("no Save button");
    fireEvent.click(save);
    await waitFor(() => expect(toLodge.save).toHaveBeenCalledWith("https://lodge.example.ts.net"));
  });

  it("ignores Cancel, Escape and Close while saving, then reloads after the failure's local mode", async () => {
    let rejectSave: (err: Error) => void = () => {};
    const pending = new Promise<void>((_resolve, reject) => {
      rejectSave = reject;
    });
    let reads = 0;
    const control: ServerControl = {
      lodge: "https://lodge.example.ts.net",
      known: true,
      get: vi.fn(async () => {
        reads++;
        return reads === 1
          ? { lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true }
          : { lodge: "", lastLodge: "", localData: true };
      }),
      save: vi.fn(() => pending),
      reload: vi.fn(),
    };
    const onClose = vi.fn();
    renderSheet(control, onClose);
    await screen.findByRole("radiogroup", { name: "Reader" });
    fireEvent.click(screen.getByLabelText("This computer"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const cancel = screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement;
    expect(cancel.disabled).toBe(true);
    fireEvent.click(cancel);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).not.toHaveBeenCalled();
    expect(control.reload).not.toHaveBeenCalled();

    rejectSave(new Error("This computer's reader did not start"));
    expect((await screen.findByRole("alert")).textContent).toContain("This computer's reader did not start");
    await waitFor(() => expect(control.get).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(control.reload).toHaveBeenCalledOnce();
  });

  it("shows a save failure, calls get again, and reloads only if closing sees a changed mode", async () => {
    let reads = 0;
    const control: ServerControl = {
      lodge: "",
      known: true,
      get: vi.fn(async () => {
        reads++;
        return reads === 1
          ? { lodge: "", lastLodge: "", localData: true }
          : { lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true };
      }),
      save: vi.fn(async () => {
        throw new Error("the lock stayed held");
      }),
      reload: vi.fn(),
    };
    const onClose = vi.fn();
    renderSheet(control, onClose);
    await screen.findByRole("radiogroup", { name: "Reader" });
    fireEvent.click(screen.getByLabelText("Lodge"));
    fireEvent.change(screen.getByLabelText("Lodge's address"), { target: { value: "https://lodge.example.ts.net" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect((await screen.findByRole("alert")).textContent).toContain("the lock stayed held");
    await waitFor(() => expect(control.get).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(control.reload).toHaveBeenCalledOnce();
  });

  it("shows the server footer only with a server and its local or Lodge label", () => {
    const store = createReadStore();
    const local = server({ lodge: "", lastLodge: "", localData: true });
    const { rerender } = render(
      <ShellProvider
        shell={{
          savePages: false,
          exportsMarkdown: true,
          server: local,
          reportRoute: () => {},
          openRoute: () => {},
          openOutside: () => {},
          saveFile: async () => true,
          renderArticles: false,
          contentSrc: () => null,
        }}
      >
        <StoreProvider store={store}>
          <Sidebar />
        </StoreProvider>
      </ShellProvider>,
    );
    expect(screen.getByRole("button", { name: "Connect to lodge…" })).toBeTruthy();

    rerender(
      <ShellProvider
        shell={{
          savePages: false,
          exportsMarkdown: false,
          server: server({ lodge: "https://lodge.example.ts.net", lastLodge: "", localData: true }),
          reportRoute: () => {},
          openRoute: () => {},
          openOutside: () => {},
          saveFile: async () => true,
          renderArticles: false,
          contentSrc: () => null,
        }}
      >
        <StoreProvider store={store}>
          <Sidebar />
        </StoreProvider>
      </ShellProvider>,
    );
    expect(screen.getByRole("button", { name: "Lodge: lodge.example.ts.net" })).toBeTruthy();
  });

  it("opens the Server sheet from the sidebar button", async () => {
    const store = createReadStore();
    const control = server({ lodge: "", lastLodge: "", localData: true });
    render(
      <ShellProvider
        shell={{
          savePages: false,
          exportsMarkdown: true,
          server: control,
          reportRoute: () => {},
          openRoute: () => {},
          openOutside: () => {},
          saveFile: async () => true,
          renderArticles: false,
          contentSrc: () => null,
        }}
      >
        <StoreProvider store={store}>
          <Sidebar />
        </StoreProvider>
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Connect to lodge…" }));
    expect(await screen.findByText("Loading…")).toBeTruthy();
  });
});
