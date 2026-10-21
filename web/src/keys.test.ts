// The keys (the brief's list): j and k, s, m, e, o, /, Shift+A and Escape,
// and none of them inside a text field.
import { describe, expect, it } from "vitest";
import { commandFor, inTextField } from "./lib/keys.js";

/** key is a keydown event with the modifiers a test sets. */
function key(name: string, init: { shiftKey?: boolean; ctrlKey?: boolean } = {}, target: EventTarget | null = null) {
  return {
    key: name,
    shiftKey: init.shiftKey ?? false,
    ctrlKey: init.ctrlKey ?? false,
    metaKey: false,
    altKey: false,
    target,
  } as unknown as KeyboardEvent;
}

describe("commandFor", () => {
  it("maps the app's keys", () => {
    for (const [name, command] of [
      ["j", "next"],
      ["k", "previous"],
      ["s", "star"],
      ["m", "toggleRead"],
      ["e", "archive"],
      ["o", "openOriginal"],
      ["/", "focusSearch"],
    ] as const) {
      expect(commandFor(key(name)), name).toBe(command);
    }
    expect(commandFor(key("A", { shiftKey: true }))).toBe("markAllRead");
    expect(commandFor(key("A"))).toBeNull();
    expect(commandFor(key("Escape"))).toBe("escape");
  });

  it("leaves the other keys and the shell's chord alone", () => {
    expect(commandFor(key("x"))).toBeNull();
    expect(commandFor(key("s", { ctrlKey: true }))).toBeNull();
    expect(commandFor(key("`", { ctrlKey: true }))).toBeNull();
  });

  it("keeps the keys out of a text field, but Escape", () => {
    const input = document.createElement("input");
    expect(inTextField(input)).toBe(true);
    expect(inTextField(document.body)).toBe(false);
    expect(commandFor(key("j", {}, input))).toBeNull();
    expect(commandFor(key("Escape", {}, input))).toBe("escape");
  });
});
