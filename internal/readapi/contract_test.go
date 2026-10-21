// The contract between readd and its web app: every session method's wire
// result, for a seeded store, as the golden files web/src/rpc/testdata
// holds. The web app's Vitest contract test checks each file against its
// TypeScript types and the components; this test fails when a golden is
// stale. Regenerate them with `make contract-update`.
package readapi_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/lodge/kit/jsonrpc"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/store"
)

// update rewrites the golden files: go test ./internal/readapi -update.
var update = flag.Bool("update", false, "rewrite web/src/rpc/testdata from this seed")

// testdata is where the web app's contract test reads the goldens.
var testdata = filepath.Join("..", "..", "web", "src", "rpc", "testdata")

// net is a fake network: a URL's answer.
type net map[string]answer

type answer struct {
	status int
	body   string
	err    error
}

func (n net) Get(_ context.Context, rawurl string, o fetch.Options) (*fetch.Response, error) {
	a, ok := n[rawurl]
	if !ok {
		return nil, errors.New("no such page: " + rawurl)
	}
	if a.err != nil {
		return nil, a.err
	}
	if a.status == 0 {
		a.status = http.StatusOK
	}
	if int64(len(a.body)) > o.MaxBytes {
		return nil, fetch.ErrTooLarge
	}
	return &fetch.Response{Status: a.status, URL: rawurl, Body: []byte(a.body)}, nil
}

// saver stands in for notes' MCP endpoint, with a stable path.
type saver struct{ path string }

func (s saver) Save(context.Context, notes.Article) (string, error) { return s.path, nil }

// full is an article long enough that a feed carries it whole.
var full = strings.Repeat("Plant tomatoes after the last frost, water them well, and wait for the fruit. ", 12)

// feed is the seeded feed: one full article, one whose page fails, one
// archived, one with no words, and a short one.
var feed = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Example Journal</title><link>https://example.com/</link>
<item><title>Planting tomatoes after the last frost</title><link>https://example.com/tomatoes</link>
<guid>a</guid><author>Ann Author</author><pubDate>Tue, 20 Oct 2026 10:00:00 GMT</pubDate>
<description>Planting tomatoes after the last frost Planting tomatoes after the last frost ` + full + `</description></item>
<item><title>A page that fails</title><link>https://example.com/fails</link><guid>b</guid>
<pubDate>Tue, 20 Oct 2026 09:00:00 GMT</pubDate><description>A short summary.</description></item>
<item><title>An archived article</title><link>https://example.com/archived</link><guid>c</guid>
<pubDate>Tue, 20 Oct 2026 08:00:00 GMT</pubDate><description>Another short summary.</description></item>
<item><title>An item with no words</title><link>https://example.com/empty</link><guid>d</guid>
<pubDate>Tue, 20 Oct 2026 07:00:00 GMT</pubDate></item>
</channel></rss>`

// second is a feed added late, by opml.import and sources.add.
const second = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Second Feed</title><link>https://second.example/</link>
<item><title>Something new</title><link>https://second.example/1</link><guid>s1</guid>
<pubDate>Tue, 20 Oct 2026 06:00:00 GMT</pubDate><description>A few words about the new thing.</description></item>
</channel></rss>`

// third is the feed sources.add subscribes to, after opml.import has
// added the second.
const third = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Third Feed</title><link>https://third.example/</link>
<item><title>A third thing</title><link>https://third.example/1</link><guid>t1</guid>
<pubDate>Tue, 20 Oct 2026 05:00:00 GMT</pubDate><description>Words about the third thing.</description></item>
</channel></rss>`

// seeded is a ready daemon over a fixed store and clock.
type seeded struct {
	router *api.Router
	items  map[string]int64
	saved  int64
}

// seed builds the store, the engine and the router the cases call.
func seed(t *testing.T) *seeded {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	db.Now = func() time.Time { return now }
	b := blobs.New(filepath.Join(dir, "blobs"))
	n := net{
		"https://example.com/feed.xml":    {body: feed},
		"https://example.com/fails":       {status: http.StatusInternalServerError},
		"https://second.example/feed.xml": {body: second},
		"https://third.example/feed.xml":  {body: third},
	}
	eng := engine.New(engine.Options{
		Store: db, Blobs: b,
		Fetch: n, Extractor: &extract.Extractor{Fetch: n, Blobs: b},
		Config:   config.Config{PollInterval: "20m", NotesFolder: "Reading/", NotesVault: 1},
		Interval: 20 * time.Minute, Version: "contract",
		Notes:       saver{path: "Reading/2026-10-20 Planting tomatoes after the last frost.md"},
		ContentPath: func(id int64) string { return "/content/item/" + itoa(id) + "/contract-mac" },
		Now:         func() time.Time { return now },
		Rand:        func() float64 { return 0.5 },
	})
	ctx := t.Context()

	first, err := eng.AddSource(ctx, "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := db.AddSource(ctx, store.Source{URL: "https://broken.example/feed.xml", Title: "A Broken Feed"},
		now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Polled(ctx, broken.ID, "", "", now.Add(time.Hour), 2, "the feed answered 503"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddSource(ctx, store.Source{URL: "https://nos.example/feed.xml", Title: "No Site"},
		now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The full article extracts from the feed; the failing one from its
	// page; the archived one is starred and archived, and stays pending.
	items, _, err := eng.ListItems(ctx, store.ListOptions{View: store.ViewAll, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, it := range items {
		ids[it.Title] = it.ID
		switch it.Title {
		case "Planting tomatoes after the last frost", "A page that fails":
			if err := eng.ExtractItem(ctx, it.ID); err != nil {
				t.Logf("extraction of %q: %v", it.Title, err)
			}
		case "An archived article":
			if err := db.SetStarred(ctx, it.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := db.SetArchived(ctx, it.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	saved, err := eng.SaveURL(ctx, engine.Scope{Write: true}, "https://example.org/later-reading")
	if err != nil {
		t.Fatal(err)
	}
	_ = first

	router, err := api.NewRouter(api.Services{RPC: stubRPC{}, Read: readapi.New(eng), Events: stubEvents{}})
	if err != nil {
		t.Fatal(err)
	}
	return &seeded{router: router, items: ids, saved: saved.ID}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// stubRPC answers connection setup, as the daemon's rpcserver does.
type stubRPC struct{}

func (stubRPC) Hello(context.Context, *api.RPCHelloParams) (*api.Hello, error) {
	return &api.Hello{Protocol: 1, Server: "readd contract", Caller: api.CallerSession}, nil
}

func (stubRPC) Ping(context.Context, *api.RPCPingParams) error { return nil }

// stubEvents answers events.subscribe.
type stubEvents struct{}

func (stubEvents) Subscribe(context.Context, *api.EventsSubscribeParams) (*api.EventsSubscribeResult, error) {
	return &api.EventsSubscribeResult{Subscribed: true}, nil
}

// TestContract writes, or checks, every session method's golden.
func TestContract(t *testing.T) {
	s := seed(t)
	cases := []struct {
		name   string
		method string
		params string
	}{
		{"rpc.hello", "rpc.hello", `{"protocol":1,"client":"contract"}`},
		{"status", "status", `{}`},
		{"counts", "counts", `{}`},
		{"sources.list", "sources.list", `{}`},
		{"items.list.unread", "items.list", `{"view":"unread","limit":2}`},
		{"items.list.all", "items.list", `{"view":"all","limit":50}`},
		{"items.get.done", "items.get", `{"id":` + itoa(s.items["Planting tomatoes after the last frost"]) + `}`},
		{"items.get.failed", "items.get", `{"id":` + itoa(s.items["A page that fails"]) + `}`},
		{"items.get.saved", "items.get", `{"id":` + itoa(s.saved) + `}`},
		{"items.search", "items.search", `{"query":"tomatoes","limit":10}`},
		{"items.saveAsNote", "items.saveAsNote", `{"id":` + itoa(s.items["Planting tomatoes after the last frost"]) + `}`},
		{"later.save", "later.save", `{"url":"https://example.org/read-later"}`},
		{"events.subscribe", "events.subscribe", `{}`},
		{"opml.export", "opml.export", `{}`},
		{"opml.import", "opml.import", `{"xml":"<?xml version=\"1.0\"?><opml version=\"2.0\"><body><outline type=\"rss\" text=\"Second Feed\" xmlUrl=\"https://second.example/feed.xml\"/></body></opml>"}`},
		{"sources.add", "sources.add", `{"url":"https://third.example/feed.xml"}`},
		{"sources.rename", "sources.rename", `{"id":1,"title":"Example Journal, renamed"}`},
		{"sources.refresh", "sources.refresh", `{"id":1}`},
		{"items.setRead", "items.setRead", `{"ids":[` + itoa(s.items["Planting tomatoes after the last frost"]) + `],"read":true}`},
		{"items.setStarred", "items.setStarred", `{"id":` + itoa(s.items["An item with no words"]) + `,"starred":true}`},
		{"items.setArchived", "items.setArchived", `{"id":` + itoa(s.items["An item with no words"]) + `,"archived":false}`},
		{"items.markAllRead", "items.markAllRead", `{"view":"all"}`},
		{"sources.remove", "sources.remove", `{"id":3}`},
	}
	ctx := api.WithCaller(t.Context(), api.CallerSession)
	if *update {
		if err := os.MkdirAll(testdata, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := s.router.Dispatch(ctx, c.method, jsontext.Value(c.params))
			if err != nil {
				t.Fatalf("%s: %v", c.method, err)
			}
			var buf bytes.Buffer
			enc := jsontext.NewEncoder(&buf, jsontext.WithIndent("  "))
			if err := json.MarshalEncode(enc, res, json.Deterministic(true)); err != nil {
				t.Fatalf("%s: %v", c.method, err)
			}
			got := append(bytes.TrimRight(buf.Bytes(), "\n"), '\n')
			path := filepath.Join(testdata, c.name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s is missing; run make contract-update", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s is stale; run make contract-update\n got:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}

// A method a session may not call is not part of the contract: the web
// never calls notes.setToken, and the router refuses it.
func TestNotesTokenIsLocal(t *testing.T) {
	s := seed(t)
	ctx := api.WithCaller(t.Context(), api.CallerSession)
	if _, err := s.router.Dispatch(ctx, "notes.setToken", jsontext.Value(`{"token":"x"}`)); !errors.Is(err, api.ErrMethodNotFound) {
		t.Fatalf("notes.setToken as a session = %v, want methodNotFound", err)
	}
}

var _ jsonrpc.Dispatcher = (*api.Router)(nil)
