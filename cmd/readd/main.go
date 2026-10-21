// Command readd is the Read daemon, lodge's read app
// (https://github.com/frostyard/lodge/blob/main/docs/specs/app-contract.md).
// It serves lodge on its app socket, the one systemd passes or
// /run/lodge/apps/read.sock under LODGE_ROOT, and its own clients on a
// private socket with --socket. It polls feeds from the public web through
// the guarded fetcher, serves articles' content pages to lodge-content,
// and MCP to agents.
//
//	readd [--socket PATH] [--debug]
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
	"path/filepath"
	"syscall"

	"github.com/frostyard/lodge/kit/jsonrpc"
	"github.com/frostyard/lodge/kit/lodgeapp"
	"github.com/frostyard/lodge/kit/mcpsession"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/mcpserver"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/poller"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/rpcserver"
	"github.com/frostyard/frostread/internal/store"
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

	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	interval, err := cfg.Interval()
	if err != nil {
		return err
	}
	env, err := lodgeapp.LoadEnv()
	if err != nil {
		return err
	}
	state := config.StateDir()
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, filepath.Join(state, "frostread.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	b := blobs.New(filepath.Join(state, "blobs"))
	if n, err := b.CleanupTemp(); err == nil && n > 0 {
		log.Info("removed temporary blobs a stopped readd left", "count", n)
	}
	secret, err := db.Secret(ctx)
	if err != nil {
		return err
	}
	f := fetch.New(version)
	x := &extract.Extractor{Fetch: f, Blobs: b}
	page := &content.Handler{Store: db, Blobs: b, Secret: secret}
	srv := rpcserver.New(rpcserver.Options{Name: "readd " + version, Logger: log})

	var eng *engine.Engine
	// The notes token arrives from the CLI after the daemon starts, so the
	// client is rebuilt when it does.
	setToken := func(token string) error {
		if err := config.SetToken(state, token); err != nil {
			return err
		}
		eng.SetNotes(noteClient(env, cfg, state, log))
		log.Info("the notes token is set; save-as-note is ready")
		return nil
	}
	eng = engine.New(engine.Options{
		Store: db, Blobs: b, Fetch: f, Extractor: x, Config: cfg, Interval: interval,
		Version: version, Notes: noteClient(env, cfg, state, log), ContentPath: page.Path,
		OnChange: srv.Broadcast, SetNotesToken: setToken, Log: log,
	})
	if cfg.NotesVault == 0 {
		log.Warn("no notes_vault in the configuration: save-as-note is off")
	}
	if cfg.LodgeURL == "" {
		log.Warn("no lodge_url in the configuration: links frostread writes into notes are relative")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pl := &poller.Poller{Poll: eng.PollDue, Log: log.With("part", "poller")}
	go pl.Run(ctx)
	go eng.RunExtraction(ctx, engine.ExtractWorkers)
	go eng.RunBlobSweep(ctx)

	mcp := mcpsession.New(mcpserver.Handler(mcpserver.Options{Engine: eng, Version: version, Log: log}),
		mcpsession.Options{CheckScope: mcpserver.CheckScope, Logger: log})
	defer func() { _ = mcp.Close() }()

	app, err := newApp(log, &server{rpc: srv, read: readapi.New(eng, log), content: page, mcp: mcp})
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

// noteClient builds save-as-note's client, or nil when it is not
// configured or has no token yet.
func noteClient(env lodgeapp.Env, cfg config.Config, state string, log *slog.Logger) engine.NoteSaver {
	if cfg.NotesVault == 0 {
		return nil
	}
	tok, err := config.Token(state)
	if err != nil {
		log.Error("the notes token could not be read", "err", err)
		return nil
	}
	if tok == "" {
		return nil
	}
	return &notes.Client{HTTP: env.AgentClient(tok), Endpoint: lodgeapp.AgentURL("notes"),
		Folder: cfg.NotesFolder, VaultID: cfg.NotesVault, LodgeURL: cfg.LodgeURL, Version: version}
}

// server is what the app's routes need from the daemon.
type server struct {
	rpc     *rpcserver.Server
	read    *readapi.Service
	content *content.Handler
	mcp     *mcpsession.Sessions
}

// app is the daemon's API, and lodge's socket server for it.
type app struct {
	rpc    *rpcserver.Server
	router *api.Router
	lodge  *lodgeapp.Server
}

// newApp builds the API and the server lodge reaches it through: GET
// /health, GET /api, a browser session's JSON-RPC, /content/ pages for
// lodge-content, and /mcp for agents.
func newApp(log *slog.Logger, s *server) (*app, error) {
	router, err := api.NewRouter(api.Services{RPC: s.rpc, Read: s.read, Events: s.rpc})
	if err != nil {
		return nil, err
	}
	return &app{rpc: s.rpc, router: router, lodge: &lodgeapp.Server{
		Health: func(context.Context) lodgeapp.Health {
			return lodgeapp.Health{OK: true, Version: version, Protocol: api.Protocol}
		},
		API: func(ctx context.Context, nc net.Conn, _ lodgeapp.Caller) {
			s.rpc.ServeConn(ctx, nc, router, api.CallerSession)
		},
		Content: s.content,
		MCP:     s.mcp,
		Logger:  log,
	}}, nil
}
