// Command readd is the Read daemon, lodge's read app
// (https://github.com/frostyard/lodge/blob/main/docs/specs/app-contract.md).
// It serves lodge on its app socket, the one systemd passes or
// /run/lodge/apps/read.sock under LODGE_ROOT, and its own clients on a
// private socket with --socket. It polls feeds from the public web through
// the guarded fetcher, serves articles' content pages to lodge-content,
// and MCP to agents.
//
// On a desktop, -desktop is the Flatpak's own readd: only its private
// socket, its own state directory, a lock, and the mode file that says
// whether the window uses lodge instead
// (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md).
//
// readd writes problem records, its panics and 5 coded internal errors, to
// the local journal, and sends nothing (docs/specs/problem-records.md).
//
//	readd [--socket PATH] [-desktop] [-log FILE] [-debug]
//	readd -build
//	readd -version
//	readd -desktop -mode
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/frostyard/lodge/kit/jsonrpc"
	"github.com/frostyard/lodge/kit/lodgeapp"
	"github.com/frostyard/lodge/kit/mcpsession"
	"github.com/frostyard/std/problems"

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

// flatpakInfo is where a Flatpak instance describes itself: -build and
// status.build read its app-commit (ADR-0008, Which engine runs in each
// mode).
const flatpakInfo = "/.flatpak-info"

var version = "dev"

func main() {
	// A release build records its problems; any other build's recorder is
	// inert (docs/specs/problem-records.md).
	rec := problems.New(AppID, releaseVersion(version), problems.ComponentDaemon)
	// The first defer, so it runs last: a panic of main's goroutine is
	// recorded after everything else unwinds. os.Exit below skips it, and
	// there is no panic then.
	defer rec.RecoverPanic()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], options{problems: rec, crashOutput: true}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "readd:", err)
		os.Exit(1)
	}
}

// options is what a test replaces in run; a zero value is production's.
// Nothing else in run exists for a test.
type options struct {
	// getenv reads the environment; os.Getenv when nil.
	getenv func(string) string
	// out receives lines from -build and -mode; os.Stdout when nil.
	out io.Writer
	// fetcher fetches the web; the guarded client when nil.
	fetcher fetch.Fetcher
	// flatpakInfo is the .flatpak-info file -build and status.build read;
	// flatpakInfo when empty.
	flatpakInfo string
	// listening is called with the private socket's path once readd accepts
	// connections on it, so a test needs no sleep.
	listening func(socket string)
	// problems records readd's panics and internal faults; nil records
	// nothing.
	problems *problems.Recorder
	// crashOutput makes run call problems.CrashOutput once readd holds the
	// state directory's lock. Only main sets it: CrashOutput changes the
	// whole process, a test's too.
	crashOutput bool
}

func run(ctx context.Context, args []string, o options) error {
	if o.getenv == nil {
		o.getenv = os.Getenv
	}
	if o.out == nil {
		o.out = os.Stdout
	}
	if v := o.getenv("READ_FLATPAK_INFO"); v != "" {
		o.flatpakInfo = v
	} else if o.flatpakInfo == "" {
		o.flatpakInfo = flatpakInfo
	}
	fs := flag.NewFlagSet("readd", flag.ContinueOnError)
	socket := fs.String("socket", "", "serve local clients on this private socket; it wins over the one the mode derives")
	desktop := fs.Bool("desktop", false, "the desktop mode: only the private socket, its own state directory and "+
		"the lock, and no lodge socket, content route, MCP or notes client")
	logPath := fs.String("log", "", "after readd holds the state directory's lock, move FILE to FILE.1 when it is "+
		"larger than 1 MiB, and append readd's output to FILE")
	showBuild := fs.Bool("build", false, "print the Flatpak instance's build, and exit")
	showMode := fs.Bool("mode", false, "print this computer's mode: local or lodge, and exit")
	showVersion := fs.Bool("version", false, "print readd's version, and exit")
	debug := fs.Bool("debug", false, "log at debug level")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(o.out, version)
		return nil
	}
	if *showBuild {
		build, err := config.Build(o.flatpakInfo)
		if err != nil {
			return err
		}
		fmt.Fprintln(o.out, build) // an empty line off the Flatpak
		return nil
	}
	if *showMode {
		if !*desktop {
			return errors.New("-mode requires -desktop")
		}
		modePath, err := config.ModePath(o.getenv)
		if err != nil {
			return err
		}
		mode, err := config.ReadMode(modePath)
		if err != nil {
			return err
		}
		if mode.Lodge != "" {
			fmt.Fprintln(o.out, "lodge")
		} else {
			fmt.Fprintln(o.out, "local")
		}
		return nil
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	rec := o.problems
	if err := rec.Err(); err != nil {
		log.Debug("problem records are off", "err", err)
	}

	// The desktop's paths are the XDG ones; lodge's are the host's, as
	// systemd passes them.
	cfgPath, state := "", ""
	if *desktop {
		var err error
		if cfgPath, err = config.DesktopConfigPath(o.getenv); err != nil {
			return err
		}
		if state, err = config.DesktopStateDir(o.getenv); err != nil {
			return err
		}
	} else {
		state = config.StateDir()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	interval, err := cfg.Interval()
	if err != nil {
		return err
	}
	build, err := config.Build(o.flatpakInfo)
	if err != nil {
		log.Warn("the Flatpak's build could not be read", "err", err)
	}

	// One readd per state directory, whatever its socket, and the mode is
	// checked under that lock: a desktop that uses lodge opens no database
	// at all (ADR-0008, Which engine runs in each mode).
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	lock, err := lockStateDir(state)
	if err != nil {
		// The Flatpak's launcher starts a second readd on purpose, and
		// expects this error; on lodge's host it is a fault.
		if errors.Is(err, errLockHeld) && !*desktop {
			rec.Error(CodeLockHeld, err)
		}
		return err
	}
	defer func() { _ = lock.Close() }()
	// -log keeps readd's output in a file. It runs here, after the lock:
	// only the readd that holds the state directory rotates that log.
	if *logPath != "" {
		if err := logTo(*logPath); err != nil {
			return fmt.Errorf("-log %s: %w", *logPath, err)
		}
	}
	// The crash file comes after the lock: -build, -mode, -version and a
	// second readd make none.
	if o.crashOutput && rec.Err() == nil {
		if err := rec.CrashOutput(); err != nil {
			log.Warn("the crash file could not be set up", "err", err)
		}
	}
	if *desktop {
		modePath, err := config.ModePath(o.getenv)
		if err != nil {
			return err
		}
		mode, err := config.ReadMode(modePath)
		if err != nil {
			return err
		}
		if mode.Lodge != "" {
			log.Info("this computer's reader is lodge's; readd stops here", "lodge", mode.Lodge)
			return nil
		}
	}

	db, err := store.Open(ctx, filepath.Join(state, "frostread.db"))
	if err != nil {
		// A stopped start is no fault of the schema.
		if errors.Is(err, store.ErrMigrate) && ctx.Err() == nil {
			rec.Error(CodeMigrateFailed, err)
		}
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
	f := o.fetcher
	if f == nil {
		f = fetch.New(version)
	}
	x := &extract.Extractor{Fetch: f, Blobs: b}
	srv := rpcserver.New(rpcserver.Options{Name: "readd " + version, Logger: log, Panics: rec})

	// Lodge's content route renders articles for lodge-content; a desktop
	// window asks items.render for the same page, and serves no route.
	var page *content.Handler
	if !*desktop {
		page = &content.Handler{Renderer: content.Renderer{Store: db, Blobs: b}, Secret: secret}
	}

	opts := engine.Options{
		Store: db, Blobs: b, Fetch: f, Extractor: x, Config: cfg, Interval: interval,
		Version: version, Build: build, OnChange: srv.Broadcast, Log: log, Problems: rec,
	}
	var eng *engine.Engine
	var env lodgeapp.Env
	if *desktop {
		// Notes is optional, and a desktop has no agent socket: there is
		// no save-as-note and no token to set (ADR-0008, Notes is optional).
		if cfg.LodgeURL != "" || cfg.NotesVault != 0 || cfg.NotesFolder != config.DefaultFolder {
			log.Info("lodge_url, notes_vault and notes_folder have no effect on this computer")
		}
		eng = engine.New(opts)
	} else {
		if env, err = lodgeapp.LoadEnv(); err != nil {
			return err
		}
		// The notes token arrives from the CLI after the daemon starts, so
		// the client is rebuilt when it does.
		opts.Notes = noteClient(env, cfg, state, log)
		opts.SetNotesToken = func(token string) error {
			if err := config.SetToken(state, token); err != nil {
				return err
			}
			eng.SetNotes(noteClient(env, cfg, state, log))
			log.Info("the notes token is set; save-as-note is ready")
			return nil
		}
		opts.ContentPath = page.Path
		eng = engine.New(opts)
		if cfg.NotesVault == 0 {
			log.Warn("no notes_vault in the configuration: save-as-note is off")
		}
		if cfg.LodgeURL == "" {
			log.Warn("no lodge_url in the configuration: links frostread writes into notes are relative")
		}
	}

	// The poller, the extraction workers and the blob sweep stop before the
	// database closes and the lock is released: a stopped readd has
	// finished the write it was in (ADR-0008, Leaving local mode stops
	// readd).
	runCtx, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stop(); workers.Wait() }()
	pl := &poller.Poller{Poll: eng.PollDue, Log: log.With("part", "poller")}
	// Each worker records its own panic: RecoverPanic covers 1 goroutine.
	workers.Go(func() {
		defer rec.RecoverPanic()
		pl.Run(runCtx)
	})
	workers.Go(func() {
		defer rec.RecoverPanic()
		eng.RunExtraction(runCtx, engine.ExtractWorkers)
	})
	workers.Go(func() {
		defer rec.RecoverPanic()
		eng.RunBlobSweep(runCtx)
	})

	ra := readapi.New(eng, log)
	if *desktop {
		ra.Stop = stop // daemon.shutdown answers, and then readd stops
	}

	privatePath := *socket // --socket wins over the socket the mode derives
	if privatePath == "" && *desktop {
		if privatePath, err = config.DesktopSocket(o.getenv); err != nil {
			return err
		}
	}
	var private net.Listener
	if privatePath != "" {
		if private, err = jsonrpc.ListenWith(privatePath, jsonrpc.ListenOptions{Name: "readd", Logger: log}); err != nil {
			return err
		}
	}

	if *desktop {
		if private == nil {
			return errors.New("desktop mode serves its private socket; none is named")
		}
		router, err := newRouter(&server{rpc: srv, read: ra})
		if err != nil {
			_ = private.Close()
			return err
		}
		if o.listening != nil {
			o.listening(privatePath)
		}
		log.Info("serving the desktop's private socket", "socket", privatePath, "version", version,
			"build", build, "state", state)
		err = srv.Serve(runCtx, private, router)
		log.Info("readd stopped")
		return err
	}

	mcp := mcpsession.New(mcpserver.Handler(mcpserver.Options{Engine: eng, Version: version, Log: log}),
		mcpsession.Options{CheckScope: mcpserver.CheckScope, Logger: log})
	defer func() { _ = mcp.Close() }()
	app, err := newApp(log, &server{rpc: srv, read: ra, content: page, mcp: mcp, unread: db.UnreadCount,
		problems: rec})
	if err != nil {
		if private != nil {
			_ = private.Close()
		}
		return err
	}
	ln, err := env.Listen(ID)
	if err != nil {
		if private != nil {
			_ = private.Close()
		}
		return fmt.Errorf("lodge socket: %w", err)
	}
	errs := make(chan error, 2)
	go func() { errs <- app.lodge.Serve(runCtx, ln) }()
	served := 1
	if private != nil {
		go func() { errs <- app.rpc.Serve(runCtx, private, app.router) }()
		served++
	}
	log.Info("serving lodge's "+ID+" app", "socket", ln.Addr().String(), "version", version)
	var first error
	for range served {
		if err := <-errs; err != nil && first == nil {
			first = err
			stop() // one socket failed: stop the other
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
	// unread is the badge on lodge's rail: the Unread view's count
	// (lodge ADR-0020). Nil sends no badge.
	unread func(ctx context.Context) (int, error)
	// problems records a panic of the content route and of MCP; nil
	// records nothing.
	problems *problems.Recorder
}

// app is the daemon's API, and lodge's socket server for it.
type app struct {
	rpc    *rpcserver.Server
	router *api.Router
	lodge  *lodgeapp.Server
}

// newRouter builds the API's routes: the read domain, connection setup, the
// changes' events, and the daemon's own life.
func newRouter(s *server) (*api.Router, error) {
	return api.NewRouter(api.Services{RPC: s.rpc, Read: s.read, Events: s.rpc, Daemon: s.read})
}

// newApp builds the API and the server lodge reaches it through: GET
// /health, GET /api, a browser session's JSON-RPC, /content/ pages for
// lodge-content, and /mcp for agents.
func newApp(log *slog.Logger, s *server) (*app, error) {
	router, err := newRouter(s)
	if err != nil {
		return nil, err
	}
	// The content route and MCP record their panics; a route the server
	// does not have stays nil.
	var page, mcp http.Handler
	if s.content != nil {
		page = recovering(s.content, s.problems)
	}
	if s.mcp != nil {
		mcp = recovering(s.mcp, s.problems)
	}
	var badge *badgeCounter
	if s.unread != nil {
		badge = &badgeCounter{unread: s.unread, log: log}
	}
	return &app{rpc: s.rpc, router: router, lodge: &lodgeapp.Server{
		Health: func(ctx context.Context) lodgeapp.Health {
			h := lodgeapp.Health{OK: true, Version: version, Protocol: api.Protocol}
			if badge != nil {
				h.Badge = badge.badge(ctx)
			}
			return h
		},
		API: func(ctx context.Context, nc net.Conn, _ lodgeapp.Caller) {
			s.rpc.ServeConn(ctx, nc, router, api.CallerSession)
		},
		Content: page,
		MCP:     mcp,
		Logger:  log,
	}}, nil
}

// badgeCounter reads the count lodge draws in its rail (lodge ADR-0020). It
// runs one count at a time, and a count never holds /health past the 1 s
// lodge's poll allows: the query may take longer than its context bounds it
// (the driver's PRAGMAs and the busy timeout run first).
type badgeCounter struct {
	unread func(ctx context.Context) (int, error)
	log    *slog.Logger
	busy   atomic.Bool
}

// badgeResult is a count, or the error that stopped it.
type badgeResult struct {
	n   int
	err error
}

// badge returns the Unread view's count, or 0 when the count failed, took
// longer than 1 s, or still runs from an earlier call. A 0 badge never makes
// the app degraded.
func (b *badgeCounter) badge(ctx context.Context) int {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	// A count that outlives this call is interrupted where the driver can.
	defer cancel()
	if !b.busy.CompareAndSwap(false, true) {
		return 0
	}
	// The buffer lets a count that ends after the timeout go.
	done := make(chan badgeResult, 1)
	go func() {
		defer b.busy.Store(false)
		n, err := b.unread(ctx)
		done <- badgeResult{n: n, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			b.log.Warn("unread count failed", "err", res.err)
			return 0
		}
		return res.n
	case <-ctx.Done():
		b.log.Warn("unread count timed out")
		return 0
	}
}
