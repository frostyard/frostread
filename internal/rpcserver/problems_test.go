package rpcserver

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/problemtest"
)

// panicDaemon's daemon.shutdown panics: a handler with a bug.
type panicDaemon struct{}

func (panicDaemon) Shutdown(context.Context) error { panic("a handler with a bug") }

// A handler that panics writes 1 panic record, and the client still gets
// the kit's internal answer (docs/specs/problem-records.md). The package's
// tests write this 1 record: the std package caps a process at 10 in 60
// seconds.
func TestPanicIsRecorded(t *testing.T) {
	j := problemtest.Listen(t)
	srv := New(Options{Name: "rpcserver test", Logger: slog.New(slog.DiscardHandler), Panics: j.Recorder(t)})
	router, err := api.NewRouter(api.Services{RPC: srv, Daemon: panicDaemon{}})
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
	j.None(t)

	out := call(t, nc, r, ping("1", "daemon.shutdown"))
	if out.Error == nil || out.Error.Code != int(api.CodeInternal) {
		t.Fatalf("a panicking handler = %+v, want internal", out)
	}
	f := j.Expect(t, "panic", "")
	if f["MESSAGE"] != "a handler with a bug" {
		t.Fatalf("message = %q", f["MESSAGE"])
	}
	if !strings.Contains(f["FROSTYARD_FRAMES"], "github.com/frostyard/frostread/internal/rpcserver.panicDaemon.Shutdown") {
		t.Fatalf("frames = %q, want the handler's", f["FROSTYARD_FRAMES"])
	}
	j.None(t)

	// The connection still serves.
	if out := call(t, nc, r, ping("2", "rpc.ping")); out.Error != nil {
		t.Fatalf("rpc.ping after the panic = %+v", out)
	}
}
