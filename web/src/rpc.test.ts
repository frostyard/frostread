// The transport's two jobs (docs/specs/api.md, Notifications): it answers
// calls by id, and it unwraps the kit's event envelope, whose event is
// items.changed.
import { describe, expect, it } from "vitest";
import { JsonRpcSession, RPCError } from "./rpc/transport.js";

/** session is a session over a fake channel, and the lines it sent. */
function session() {
  const sent: string[] = [];
  const s = new JsonRpcSession(async (line) => {
    sent.push(line);
  });
  return { s, sent };
}

describe("JsonRpcSession", () => {
  it("sends a call, and answers it by id", async () => {
    const { s, sent } = session();
    const answer = s.call<{ views: number }>("counts", {});
    const line = JSON.parse(sent[0] ?? "{}") as { id: number; method: string };
    expect(line.method).toBe("counts");
    s.receive(JSON.stringify({ jsonrpc: "2.0", id: line.id, result: { views: 3 } }));
    await expect(answer).resolves.toEqual({ views: 3 });
  });

  it("rejects with an RPCError", async () => {
    const { s, sent } = session();
    const answer = s.call("items.get", { id: 4 });
    const line = JSON.parse(sent[0] ?? "{}") as { id: number };
    s.receive(JSON.stringify({ jsonrpc: "2.0", id: line.id, error: { code: 1001, message: "no such item" } }));
    await expect(answer).rejects.toBeInstanceOf(RPCError);
    await answer.catch((err: RPCError) => expect(err.code).toBe(1001));
  });

  it("unwraps items.changed from the event envelope", () => {
    const { s } = session();
    const heard: string[][] = [];
    s.onEvent((event) => heard.push(event.views));
    s.receive(JSON.stringify({ jsonrpc: "2.0", method: "event", params: { seq: 1, event: "tick", data: {} } }));
    expect(heard).toEqual([]);
    s.receive(
      JSON.stringify({
        jsonrpc: "2.0",
        method: "event",
        params: { seq: 2, event: "items.changed", data: { views: ["unread", "later"] } },
      }),
    );
    expect(heard).toEqual([["unread", "later"]]);
  });
});
