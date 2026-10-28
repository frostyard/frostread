package rpcserver

// The id guard's tests: a request whose id is too long to answer under
// items.render's bound is refused before the kit dispatches it
// (docs/specs/api.md, Rendering for the desktop).

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/content"
)

// dial serves connection setup on a private socket, readies one connection
// with rpc.hello, and returns it and a reader of its answers.
func dial(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	srv := New(Options{Name: "rpcserver test", Logger: slog.New(slog.DiscardHandler)})
	router, err := api.NewRouter(api.Services{RPC: srv, Events: srv})
	if err != nil {
		t.Fatal(err)
	}
	run := rpctest.Serve(t, func(ctx context.Context, ln net.Listener) error { return srv.Serve(ctx, ln, router) })
	nc, err := net.Dial("unix", run.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	r := bufio.NewReader(nc)
	if out := call(t, nc, r, `{"jsonrpc":"2.0","id":0,"method":"rpc.hello","params":{"protocol":1,"client":"test"}}`); out.Error != nil {
		t.Fatalf("rpc.hello = %+v", out)
	}
	return nc, r
}

// reply is one answer's id and error code.
type reply struct {
	ID    jsontext.Value `json:"id"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// call writes one request line, and returns the answer.
func call(t *testing.T, nc net.Conn, r *bufio.Reader, line string) reply {
	t.Helper()
	if _, err := io.WriteString(nc, line+"\n"); err != nil {
		t.Fatal(err)
	}
	answer, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("%.20s…: %v", line, err)
	}
	var out reply
	if err := json.Unmarshal(answer, &out); err != nil {
		t.Fatalf("%.20s…: the answer is not JSON: %v", line, err)
	}
	return out
}

// ping is a request of method with an id.
func ping(id, method string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `"}`
}

// longest is the longest string id the envelope's allowance holds, in its
// quotes, and one is a byte longer.
func longest(id int) string { return `"` + strings.Repeat("i", id-2) + `"` }

// An id of exactly the allowance is served, and its answer carries it.
func TestAnIDAtTheBoundIsServed(t *testing.T) {
	nc, r := dial(t)
	id := longest(content.MaxRequestID)
	if got := len(id); got != content.MaxRequestID {
		t.Fatalf("the test's id is %d bytes, not %d", got, content.MaxRequestID)
	}
	out := call(t, nc, r, ping(id, "rpc.ping"))
	if out.Error != nil {
		t.Fatalf("an id of %d bytes answered %d", len(id), out.Error.Code)
	}
	if string(out.ID) != id {
		t.Fatalf("the answer's id is %d bytes, want %d", len(out.ID), len(id))
	}
}

// A longer id is refused as invalidRequest with id null, as a string and as
// a number, and the connection lives on.
func TestALongerIDIsRefused(t *testing.T) {
	nc, r := dial(t)
	for _, id := range []string{
		longest(content.MaxRequestID + 1),
		strings.Repeat("1", content.MaxRequestID+1), // a number of the same length
	} {
		out := call(t, nc, r, ping(id, "rpc.ping"))
		if out.Error == nil || out.Error.Code != int(api.CodeInvalidRequest) {
			t.Fatalf("an id of %d bytes = %+v, want invalidRequest", len(id), out)
		}
		if string(out.ID) != "null" {
			t.Fatalf("an id of %d bytes answered id %s, want null", len(id), out.ID)
		}
		// The connection is still usable, and a short id is answered.
		if next := call(t, nc, r, ping("2", "rpc.ping")); next.Error != nil || string(next.ID) != "2" {
			t.Fatalf("the call after a refusal = %+v", next)
		}
	}
}

// A line that is not JSON still reaches the kit, which answers parseError.
func TestALineThatIsNotJson(t *testing.T) {
	nc, r := dial(t)
	if out := call(t, nc, r, `not json`); out.Error == nil || out.Error.Code != int(api.CodeParseError) {
		t.Fatalf("a line that is not JSON = %+v, want parseError", out)
	}
	if out := call(t, nc, r, ping("3", "rpc.ping")); out.Error != nil || string(out.ID) != "3" {
		t.Fatalf("the call after a parse error = %+v", out)
	}
}

// scripted is a net.Conn whose Read answers r, and nothing else.
type scripted struct {
	net.Conn
	r io.Reader
}

func (s scripted) Read(p []byte) (int, error) { return s.r.Read(p) }

// A guard conn gives the same bytes back, however small the reader's
// buffer is, and leaves a line with a short id as it came.
func TestGuardConnReadsInSmallPieces(t *testing.T) {
	first, second := ping("1", "rpc.ping"), ping("2", "rpc.ping")
	script := first + "\n" + second + "\n"
	c := newGuardConn(scripted{r: strings.NewReader(script)}, slog.New(slog.DiscardHandler))
	var got strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := c.Read(buf)
		got.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != script {
		t.Fatalf("the guard's bytes are\n%q\nwant\n%q", got.String(), script)
	}
}

// A line with a longer id comes out as an id-less request, and the lines
// around it are unchanged.
func TestGuardConnReplacesALongerID(t *testing.T) {
	first := ping("1", "rpc.ping")
	long := longest(content.MaxRequestID + 1)
	second := ping("null", "rpc.ping")
	c := newGuardConn(scripted{r: strings.NewReader(first + "\n" + ping(long, "rpc.ping") + "\n" + second + "\n")},
		slog.New(slog.DiscardHandler))
	all, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	want := first + "\n" + idless + "\n" + second + "\n"
	if string(all) != want {
		t.Fatalf("the guard's bytes are\n%q\nwant\n%q", all, want)
	}
}
