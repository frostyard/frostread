package rpcserver

// The desktop's keep-alive: in lodge mode the shell sends the notification
// below every 30 s, so that lodge keeps the window's session, and parses
// nothing that comes back (docs/specs/api.md, The window in lodge mode).
// readd must answer it with nothing, or its answer would reach the page as
// a line no call of the page's asked for.

import (
	"io"
	"testing"
)

// bridgePing is the line web/src-tauri/src/bridge.rs sends (PING).
const bridgePing = `{"jsonrpc":"2.0","method":"rpc.ping"}`

// The bridge's ping gets no answer on a ready connection, and the
// connection's next answer is the next call's.
func TestTheBridgesPingIsNotAnswered(t *testing.T) {
	nc, r := dial(t)
	for _, id := range []string{"7", "8"} {
		if _, err := io.WriteString(nc, bridgePing+"\n"); err != nil {
			t.Fatal(err)
		}
		if out := call(t, nc, r, ping(id, "rpc.ping")); out.Error != nil || string(out.ID) != id {
			t.Fatalf("the answer after the bridge's ping = %+v, want id %s", out, id)
		}
	}
}
