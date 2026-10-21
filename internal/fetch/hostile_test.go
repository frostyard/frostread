// The end-to-end hostile check: a feed and an article page served by an
// httptest server, fetched through the real client (its dialer pointed at
// the test server), parsed, extracted, stored, and served by the content
// handler. Nothing hostile may reach the store's HTML or the page.
package fetch_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/store"
)

// hostile is the markup every path must lose.
const hostile = `<script>alert("SCRIPT_SENTINEL")</script>
<style>body { color: red }</style>
<p onclick="alert('handler')">An event handler sits here.</p>
<a href="javascript:alert('js')">a javascript link</a>
<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">a data link</a>
<iframe src="https://evil.example/frame">IFRAME_SENTINEL</iframe>
<base href="https://evil.example/">
<meta http-equiv="refresh" content="0;url=https://evil.example/">
<svg onload="alert('svg')"><circle r="1">SVG_SENTINEL</circle></svg>
<div style="background:url(javascript:alert(1))">STYLE_SENTINEL</div>
<form action="https://evil.example/post"><input name="x"></form>
<img src="http://example.com/pic.png" onerror="alert('img')" alt="A picture">`

// words is real article text, long enough for the inline path (600
// characters) and for readability (220).
var words = strings.Repeat("<p>Useful words about a made-up article, enough of them for the extraction to keep.</p>", 12)

// articlePage is the page the extractor reads.
var articlePage = `<!doctype html><html><head><title>A hostile page</title>` + hostile + `</head>
<body><article><h1>A hostile page</h1>` + words + `<p>` + hostile + `</p></article></body></html>`

// png is a 1 × 1 PNG, the article's image.
var png = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}

// feedXML is the feed: an inline article with hostile content, a short
// summary whose page holds the attack, and a summary left unextracted.
func feedXML() string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel><title>Hostile Feed</title><link>http://example.com/</link>
<item><title>The inline article</title><link>http://example.com/inline</link><guid>inline</guid>
<description>A short summary.</description>
<content:encoded><![CDATA[` + words + hostile + `]]></content:encoded></item>
<item><title>The extracted article</title><link>http://example.com/article</link><guid>page</guid>
<description><![CDATA[` + hostile + `]]></description></item>
<item><title>The pending article</title><link>http://example.com/pending</link><guid>pending</guid>
<description><![CDATA[` + hostile + ` and some summary words.]]></description></item>
</channel></rss>`
}

// server serves the feed, the article and its image, and records the paths
// asked for, so the test can prove the inline article's page was not
// fetched.
type server struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		switch r.URL.Path {
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, feedXML())
		case "/article":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, articlePage)
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) requested(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.paths {
		if p == path {
			return true
		}
	}
	return false
}

// world is the daemon over the test server, with the real client.
type world struct {
	srv     *server
	engine  *engine.Engine
	store   *store.DB
	content *content.Handler
}

func newWorld(t *testing.T) *world {
	t.Helper()
	srv := newServer(t)
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
	// The real client, its dialer pointed at the test server: the address
	// check still runs on the URL, and only the connection is redirected.
	client := fetch.New("test")
	fetch.SetDialerForTest(client, func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	})
	eng := engine.New(engine.Options{
		Store: db, Blobs: b, Fetch: client,
		Extractor: &extract.Extractor{Fetch: client, Blobs: b},
		Config:    config.Config{PollInterval: "20m", NotesFolder: "Reading/", NotesVault: 1},
		Interval:  20 * time.Minute, Version: "test",
		ContentPath: (&content.Handler{Store: db, Blobs: b, Secret: secret}).Path,
		Now:         func() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) },
	})
	page := &content.Handler{Store: db, Blobs: b, Secret: secret}
	return &world{srv: srv, engine: eng, store: db, content: page}
}

// item finds an item by the URL the feed gave it: the extraction may
// replace its title with the page's.
func (w *world) item(t *testing.T, url string) store.Item {
	t.Helper()
	items, _, err := w.store.ListItems(t.Context(), store.ListOptions{View: store.ViewAll, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.URL == url {
			return it
		}
	}
	var urls []string
	for _, it := range items {
		urls = append(urls, it.URL)
	}
	t.Fatalf("no item at %q; the store holds %q", url, urls)
	return store.Item{}
}

// TestHostileFeedAndArticle checks both extraction paths: the feed's own
// content, and the article's page.
func TestHostileFeedAndArticle(t *testing.T) {
	w := newWorld(t)
	if _, err := w.engine.AddSource(t.Context(), "http://example.com/feed.xml"); err != nil {
		t.Fatal(err)
	}
	// The inline article is extracted from the feed; the second from its
	// page; the third is left pending, so the page shows its summary.
	if err := w.engine.ExtractItem(t.Context(), w.item(t, "http://example.com/inline").ID); err != nil {
		t.Fatal(err)
	}
	if err := w.engine.ExtractItem(t.Context(), w.item(t, "http://example.com/article").ID); err != nil {
		t.Fatal(err)
	}
	if w.srv.requested("/inline") {
		t.Fatal("the inline article's page was fetched")
	}
	if !w.srv.requested("/article") {
		t.Fatal("the extracted article's page was not fetched")
	}

	for _, tc := range []struct {
		name  string
		url   string
		state string
	}{
		{"The inline article", "http://example.com/inline", store.ExtractDone},
		{"The extracted article", "http://example.com/article", store.ExtractDone},
		{"The pending article", "http://example.com/pending", store.ExtractPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := w.item(t, tc.url)
			if it.ExtractState != tc.state {
				t.Fatalf("state = %q, want %q", it.ExtractState, tc.state)
			}
			// Nothing hostile is stored, and no script's words are text.
			for _, html := range []string{it.SummaryHTML, it.ContentHTML} {
				assertClean(t, "stored HTML", html, "blob:", "data:image/", "http://", "https://")
			}
			for _, bad := range []string{"SCRIPT_SENTINEL", "color: red", "IFRAME_SENTINEL"} {
				if strings.Contains(it.Text, bad) {
					t.Fatalf("the text holds %q: %q", bad, it.Text)
				}
			}
			if !strings.Contains(it.Text, "Useful words") && tc.state == store.ExtractDone {
				t.Fatalf("the text lost the article: %q", it.Text)
			}
			page := w.page(t, it.ID)
			body := articleBody(page)
			assertClean(t, "the article", body, "data:image/")
			if tc.state == store.ExtractDone {
				// The article's image is stored and inlined, not dropped.
				if !strings.Contains(body, "data:image/png;base64,") {
					t.Fatalf("the article has no inlined image:\n%s", body)
				}
				if !strings.Contains(it.ContentHTML, "blob:") {
					t.Fatalf("the stored article has no blob image:\n%s", it.ContentHTML)
				}
			}
			// The page around it may hold its own style and meta; it holds
			// no script, no remote image and no script URL.
			lower := strings.ToLower(page)
			for _, bad := range []string{"<script", "javascript:", "data:text", "onerror", "onclick", "onload"} {
				if strings.Contains(lower, bad) {
					t.Fatalf("the content page keeps %q", bad)
				}
			}
			for _, m := range imageSrc.FindAllStringSubmatch(page, -1) {
				if !strings.HasPrefix(m[1], "data:image/") {
					t.Fatalf("the content page has an image from %q", m[1])
				}
			}
		})
	}
}

// page fetches an item's content page.
func (w *world) page(t *testing.T, id int64) string {
	t.Helper()
	srv := httptest.NewServer(w.content)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/content/" + w.content.Path(id))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the content page = %d", res.StatusCode)
	}
	var b strings.Builder
	if _, err := io.Copy(&b, res.Body); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

var imageSrc = regexp.MustCompile(`(?i)<img[^>]*\ssrc="([^"]*)"`)

// articleBody is the page's <article> element: the sanitized content.
func articleBody(page string) string {
	start := strings.Index(page, "<article>")
	end := strings.Index(page, "</article>")
	if start < 0 || end < start {
		return ""
	}
	return page[start:end]
}

// assertClean fails on anything hostile in HTML the daemon keeps or
// serves. An image's source must start with one of the prefixes the caller
// allows: a stored article names its blobs, and a content page has inlined
// them.
func assertClean(t *testing.T, what, html string, srcPrefixes ...string) {
	t.Helper()
	lower := strings.ToLower(html)
	for _, bad := range []string{
		"<script", "SCRIPT_SENTINEL", "<style", "color: red", "<iframe", "IFRAME_SENTINEL",
		"<base", "<meta", "http-equiv", "<form", "<input", "<object", "<embed", "<svg",
		"javascript:", "onerror", "onclick", "onload", "style=", "data:text", "<link",
	} {
		if strings.Contains(lower, bad) {
			t.Fatalf("%s keeps %q:\n%s", what, bad, html)
		}
	}
	for _, m := range imageSrc.FindAllStringSubmatch(html, -1) {
		src := m[1]
		ok := false
		for _, prefix := range srcPrefixes {
			if strings.HasPrefix(src, prefix) {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("%s has an image from %q, want %v:\n%s", what, src, srcPrefixes, html)
		}
	}
}
