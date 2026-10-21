// Command readd is the Read daemon, lodge's read app
// (https://github.com/frostyard/lodge/blob/main/docs/specs/app-contract.md).
// It serves lodge on its app socket, the one systemd passes or
// /run/lodge/apps/read.sock under LODGE_ROOT, and its own clients on a
// private socket with --socket.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/frostyard/lodge/kit/jsonrpc"
	"github.com/frostyard/lodge/kit/lodgeapp"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/rpcserver"
)

// ID is the app's id, as its manifest says.
const ID = "read"

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "readd:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("readd", flag.ContinueOnError)
	socket := fs.String("socket", "", "also serve local clients on this private socket")
	debug := fs.Bool("debug", false, "log at debug level")
	if err := fs.Parse(args); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	app, err := newApp(log)
	if err != nil {
		return err
	}
	env, err := lodgeapp.LoadEnv()
	if err != nil {
		return err
	}
	var private net.Listener
	if *socket != "" {
		if private, err = jsonrpc.ListenWith(*socket, jsonrpc.ListenOptions{Name: "readd", Logger: log}); err != nil {
			return err
		}
	}
	ln, err := env.Listen(ID)
	if err != nil {
		if private != nil {
			_ = private.Close()
		}
		return fmt.Errorf("lodge socket: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	go func() { errs <- app.lodge.Serve(ctx, ln) }()
	served := 1
	if private != nil {
		go func() { errs <- app.rpc.Serve(ctx, private, app.router) }()
		served++
	}
	log.Info("serving lodge's "+ID+" app", "socket", ln.Addr().String(), "version", version)
	var first error
	for range served {
		if err := <-errs; err != nil && first == nil {
			first = err
			cancel() // one socket failed: stop the other
		}
	}
	return first
}

// app is the daemon's API, and lodge's socket server for it.
type app struct {
	rpc    *rpcserver.Server
	router *api.Router
	lodge  *lodgeapp.Server
}

// newApp builds the API and the server lodge reaches it through: GET
// /health, and GET /api, a browser session's JSON-RPC.
func newApp(log *slog.Logger) (*app, error) {
	rpc := rpcserver.New(rpcserver.Options{Name: "readd " + version, Logger: log})
	router, err := api.NewRouter(api.Services{RPC: rpc})
	if err != nil {
		return nil, err
	}
	return &app{rpc: rpc, router: router, lodge: &lodgeapp.Server{
		Health: func(context.Context) lodgeapp.Health {
			return lodgeapp.Health{OK: true, Version: version, Protocol: api.Protocol}
		},
		API: func(ctx context.Context, nc net.Conn, _ lodgeapp.Caller) {
			rpc.ServeConn(ctx, nc, router, api.CallerSession)
		},
		Logger: log,
	}}, nil
}
