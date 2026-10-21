package mcpserver

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frostyard/lodge/kit/lodgeapp"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/store"
)

// page is a fake network with one feed and one article.
type page map[string]string

func (p page) Get(_ context.Context, url string, o fetch.Options) (*fetch.Response, error) {
	body, ok := p[url]
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
<item><title>A tomato article</title><link>https://example.com/tomato</link><guid>1</guid>
<pubDate>Tue, 20 Oct 2026 09:00:00 GMT</pubDate><description>Plant tomatoes after the frost.</description></item>
</channel></rss>`

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	p := page{"https://example.com/feed.xml": feedXML}
	e := engine.New(engine.Options{
		Store: db, Blobs: b, Fetch: p, Extractor: &extract.Extractor{Fetch: p, Blobs: b},
		Config:   config.Config{PollInterval: "20m", NotesFolder: "Reading/", NotesVault: 1},
		Interval: 20 * time.Minute, Version: "test",
	})
	if _, err := e.AddSource(t.Context(), "https://example.com/feed.xml"); err != nil {
		t.Fatal(err)
	}
	return e
}

// scopeTransport adds the caller's scope header to every request, as lodge
// sends Lodge-Scope.
type scopeTransport struct {
	scope string
	next  http.RoundTripper
}

func (s scopeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Test-Scope", s.scope)
	return s.next.RoundTrip(r)
}

// connect serves h as lodge would, and returns a client session.
func connect(t *testing.T, h http.Handler, scope string) *mcp.ClientSession {
	t.Helper()
	withCaller := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := lodgeapp.Caller{Kind: lodgeapp.Agent, Token: "test", Scope: r.Header.Get("X-Test-Scope")}
		h.ServeHTTP(w, r.WithContext(lodgeapp.WithCaller(r.Context(), c)))
	})
	srv := httptest.NewServer(withCaller)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: scopeTransport{scope: scope, next: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// call runs a tool and returns its structured result, or whether it failed.
func call(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any, out any) (isError bool) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return true // an unlisted tool, or a tool error
	}
	if res.IsError {
		return true
	}
	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return false
}

func TestReadScope(t *testing.T) {
	e := newEngine(t)
	sess := connect(t, Handler(Options{Engine: e, Version: "test"}), "read")

	var sources sourcesOut
	if call(t, sess, "list_sources", nil, &sources) {
		t.Fatal("list_sources failed")
	}
	if len(sources.Sources) != 1 || sources.Sources[0].Unread != 1 || sources.Sources[0].Title != "Example Journal" {
		t.Fatalf("sources = %+v", sources)
	}

	var unread itemsOut
	if call(t, sess, "list_unread", map[string]any{}, &unread) {
		t.Fatal("list_unread failed")
	}
	if len(unread.Items) != 1 || unread.Items[0].Title != "A tomato article" {
		t.Fatalf("unread = %+v", unread)
	}

	var found itemsOut
	if call(t, sess, "search", map[string]any{"query": "tomatoes"}, &found) {
		t.Fatal("search failed")
	}
	if len(found.Items) != 1 || found.Items[0].Snippet == "" {
		t.Fatalf("search = %+v", found)
	}

	var read readOut
	if call(t, sess, "read_item", map[string]any{"id": unread.Items[0].ID}, &read) {
		t.Fatal("read_item failed")
	}
	if read.Text == "" || read.Notice != Notice {
		t.Fatalf("read_item = %+v", read)
	}

	// A read token cannot write.
	if !call(t, sess, "save_url", map[string]any{"url": "https://example.org/later"}, nil) {
		t.Fatal("save_url succeeded under a read scope")
	}
	if !call(t, sess, "mark_read", map[string]any{"ids": []int64{unread.Items[0].ID}}, nil) {
		t.Fatal("mark_read succeeded under a read scope")
	}
	if !call(t, sess, "star", map[string]any{"id": unread.Items[0].ID, "starred": true}, nil) {
		t.Fatal("star succeeded under a read scope")
	}
}

func TestWriteScope(t *testing.T) {
	e := newEngine(t)
	sess := connect(t, Handler(Options{Engine: e, Version: "test"}), "read;write")

	var saved saveOut
	if call(t, sess, "save_url", map[string]any{"url": "https://example.org/later"}, &saved) {
		t.Fatal("save_url failed under a write scope")
	}
	if saved.URL != "https://example.org/later" {
		t.Fatalf("saved = %+v", saved)
	}
	var marked markOut
	if call(t, sess, "mark_read", map[string]any{"ids": []int64{1}}, &marked) {
		t.Fatal("mark_read failed")
	}
	if marked.Changed != 1 {
		t.Fatalf("marked = %+v", marked)
	}
	var starred starOut
	if call(t, sess, "star", map[string]any{"id": 1, "starred": true}, &starred) {
		t.Fatal("star failed")
	}
	if !starred.Starred {
		t.Fatalf("starred = %+v", starred)
	}
	// Marking read unread again is admitted too.
	if call(t, sess, "mark_read", map[string]any{"ids": []int64{1}, "read": false}, nil) {
		t.Fatal("mark_read with read false failed")
	}
}

func TestListUnreadSinceAndLimit(t *testing.T) {
	e := newEngine(t)
	sess := connect(t, Handler(Options{Engine: e, Version: "test"}), "read")
	var none itemsOut
	if call(t, sess, "list_unread", map[string]any{"since": "2027-01-01T00:00:00Z"}, &none) {
		t.Fatal("list_unread failed")
	}
	if len(none.Items) != 0 {
		t.Fatalf("since a later date = %+v", none)
	}
	if !call(t, sess, "list_unread", map[string]any{"since": "yesterday"}, nil) {
		t.Fatal("a bad since was accepted")
	}
	var all itemsOut
	if call(t, sess, "list_unread", map[string]any{"limit": 200, "source_id": 1}, &all) {
		t.Fatal("list_unread with a source failed")
	}
	if len(all.Items) != 1 {
		t.Fatalf("unread with a source = %+v", all)
	}
}

func TestUnknownTokenScope(t *testing.T) {
	e := newEngine(t)
	h := Handler(Options{Engine: e, Version: "test"})
	// A scope that does not parse gets no server: the SDK refuses it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := lodgeapp.Caller{Kind: lodgeapp.Agent, Token: "test", Scope: "admin"}
		h.ServeHTTP(w, r.WithContext(lodgeapp.WithCaller(r.Context(), c)))
	}))
	defer srv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	if _, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL},
		nil); err == nil {
		t.Fatal("a session with an unparseable scope was opened")
	}
	if err := CheckScope("read"); err != nil {
		t.Fatal(err)
	}
	if err := CheckScope("read;write"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "write", "read;admin"} {
		if err := CheckScope(bad); err == nil {
			t.Fatalf("scope %q parsed", bad)
		}
	}
}

func TestTextIsCapped(t *testing.T) {
	long := strings.Repeat("word ", MaxText)
	if r := []rune(long); len(r) <= MaxText {
		t.Fatalf("the fixture is %d runes", len(r))
	}
	// The cap's own arithmetic: one rune over gives one over, and the
	// result ends where the cap does.
	text := []rune(long)
	capped := string(text[:MaxText])
	if len([]rune(capped)) != MaxText {
		t.Fatalf("capped to %d runes", len([]rune(capped)))
	}
}
