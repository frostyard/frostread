package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frostyard/lodge/kit/kittest"
	"github.com/frostyard/lodge/kit/lodgeapp"
	"github.com/frostyard/lodge/kit/manifest"
	"github.com/frostyard/lodge/kit/mcpsession"
	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/mcpserver"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/rpcserver"
	"github.com/frostyard/frostread/internal/store"
)

// fakeNet answers one feed, so a test can subscribe without a network.
type fakeNet map[string]string

func (f fakeNet) Get(_ context.Context, url string, o fetch.Options) (*fetch.Response, error) {
	body, ok := f[url]
	if !ok {
		return nil, http.ErrNotSupported
	}
	if int64(len(body)) > o.MaxBytes {
		return nil, fetch.ErrTooLarge
	}
	return &fetch.Response{Status: 200, URL: url, Body: []byte(body)}, nil
}

const feedXML = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Example Journal</title><link>https://example.com/</link>
<item><title>First post</title><link>https://example.com/first</link><guid>1</guid>
<desc>An article.</desc></item></channel></rss>`

// start builds the daemon's routes over a store in a temp dir, and serves
// them as lodge does.
func start(t *testing.T) (*app, *engine.Engine, *kittest.Lodge) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	secret, err := db.Secret(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f := fakeNet{"https://example.com/feed.xml": feedXML}
	log := slog.New(slog.DiscardHandler)
	srv := rpcserver.New(rpcserver.Options{Name: "readd test", Logger: log})
	eng := engine.New(engine.Options{
		Store: db, Blobs: b, Fetch: f, Extractor: &extract.Extractor{Fetch: f, Blobs: b},
		Config: config.Config{PollInterval: "20m", NotesFolder: "Reading/"}, Interval: 20 * time.Minute,
		Version: "test", ContentPath: (&content.Handler{Store: db, Blobs: b, Secret: secret}).Path,
		OnChange: srv.Broadcast, Log: log,
	})
	page := &content.Handler{Store: db, Blobs: b, Secret: secret}
	mcp := mcpsession.New(mcpserver.Handler(mcpserver.Options{Engine: eng, Version: "test", Log: log}),
		mcpsession.Options{CheckScope: mcpserver.CheckScope, Logger: log})
	t.Cleanup(func() { _ = mcp.Close() })
	a, err := newApp(log, &server{rpc: srv, read: readapi.New(eng, log), content: page, mcp: mcp})
	if err != nil {
		t.Fatal(err)
	}
	return a, eng, kittest.Start(t, ID, a.lodge)
}

// installed reads the manifest the packaging installs.
func installed(t *testing.T) manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile("../../packaging/lodge/app.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != ID || m.Contract != manifest.Contract {
		t.Fatalf("app.json names %q, contract %d", m.ID, m.Contract)
	}
	return m
}

// The socket keeps the app contract's rules, as lodge sees it.
func TestSocketRules(t *testing.T) {
	_, _, l := start(t)
	kittest.CheckRules(t, l, installed(t))
}

// A browser session's /api speaks the contract, behind rpc.hello, and a
// change reaches a subscribed session as items.changed.
func TestAPI(t *testing.T) {
	_, eng, l := start(t)
	nc, err := l.DialAPI(t.Context(), kittest.Session())
	if err != nil {
		t.Fatal(err)
	}
	c := api.NewClient(nc)
	defer c.Close()
	if err := c.RPC().Ping(t.Context(), &api.RPCPingParams{}); !errors.Is(err, api.ErrNotReady) {
		t.Errorf("ping before hello = %v, want notReady", err)
	}
	h, err := c.RPC().Hello(t.Context(), &api.RPCHelloParams{Protocol: api.Protocol, Client: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Caller != api.CallerSession || h.Protocol != api.Protocol {
		t.Errorf("hello = %+v, want a session", h)
	}
	if err := c.RPC().Ping(t.Context(), &api.RPCPingParams{}); err != nil {
		t.Errorf("ping: %v", err)
	}
	if err := c.Events().Subscribe(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Subscribe answers before notifications reach us: add a source, which
	// changes the unread view.
	if _, err := eng.AddSource(t.Context(), "https://example.com/feed.xml"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-c.Notifications():
		if ev.Event != api.EventMethod {
			t.Fatalf("notification = %q, want %q", ev.Event, api.EventMethod)
		}
		var changed api.Changed
		if err := json.Unmarshal(ev.Data, &changed); err != nil {
			t.Fatal(err)
		}
		if len(changed.Views) == 0 {
			t.Fatal("the change names no view")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no items.changed arrived")
	}
	page, err := c.Read().ListItems(t.Context(), api.ItemsListParams{View: store.ViewUnread})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("items.list = %+v, %v", page, err)
	}
	counts, err := c.Read().Counts(t.Context())
	if err != nil || counts.Views[store.ViewUnread] != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// The private socket serves local clients, and notes.setToken is local
// only.
func TestPrivateSocket(t *testing.T) {
	a, _, _ := start(t)
	run := rpctest.Serve(t, func(ctx context.Context, ln net.Listener) error { return a.rpc.Serve(ctx, ln, a.router) })
	c, h, err := api.Dial(t.Context(), run.Socket, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if h.Caller != api.CallerLocal {
		t.Errorf("hello on the private socket = %+v, want local", h)
	}
	if _, err := c.Read().Sources(t.Context()); err != nil {
		t.Fatalf("sources.list on the private socket: %v", err)
	}
	if res, err := c.Read().SweepBlobs(t.Context(), true); err != nil || !res.DryRun {
		t.Fatalf("blobs.sweep on the private socket = %+v, %v", res, err)
	}
}

// A session may not set the notes token, nor sweep the blobs.
func TestNotesTokenIsLocalOnly(t *testing.T) {
	_, _, l := start(t)
	nc, err := l.DialAPI(t.Context(), kittest.Session())
	if err != nil {
		t.Fatal(err)
	}
	c := api.NewClient(nc)
	defer c.Close()
	if _, err := c.RPC().Hello(t.Context(), &api.RPCHelloParams{Protocol: api.Protocol, Client: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Read().SetNotesToken(t.Context(), "lgm_x"); !errors.Is(err, api.ErrMethodNotFound) {
		t.Fatalf("notes.setToken as a session = %v, want methodNotFound", err)
	}
	if _, err := c.Read().SweepBlobs(t.Context(), true); !errors.Is(err, api.ErrMethodNotFound) {
		t.Fatalf("blobs.sweep as a session = %v, want methodNotFound", err)
	}
}

// The content route answers only for its own MAC, as the contract wants.
func TestContentNeedsCaller(t *testing.T) {
	_, _, l := start(t)
	if code, _ := l.Get(t, "/content/item/1/mac", lodgeapp.Caller{}); code != http.StatusForbidden {
		t.Fatalf("content without a caller = %d", code)
	}
}
