package content

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/store"
)

func setup(t *testing.T) (*Handler, *store.DB, int64) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	h := &Handler{Store: db, Blobs: b, Secret: []byte(strings.Repeat("s", 32))}
	it, err := db.SaveLink(t.Context(), "https://example.com/an-article", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := b.Put(t.Context(), strings.NewReader("png bytes"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	err = db.SetExtraction(t.Context(), it.ID, store.Extraction{
		Title: "An <article>", Author: "Ann", Text: "text",
		ContentHTML: `<p>A paragraph.</p><p><img src="blob:` + hash + `" alt="A tomato"></p>`,
		State:       store.ExtractDone, Images: []store.Blob{{Hash: hash, Mime: "image/png", Size: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, db, it.ID
}

func TestPage(t *testing.T) {
	h, _, id := setup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/content/" + h.Path(id))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{"data:image/png;base64,", "<p>A paragraph.</p>", "prefers-color-scheme"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "blob:") {
		t.Fatalf("page keeps a blob marker:\n%s", page)
	}
	if strings.Contains(page, "<script") {
		t.Fatalf("page has a script:\n%s", page)
	}
}

// An item read before its extraction shows its feed summary, sanitized.
func TestPendingItemShowsSanitizedSummary(t *testing.T) {
	h, db, _ := setup(t)
	src, err := db.AddSource(t.Context(), store.Source{URL: "https://example.org/feed.xml", Title: "Example"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := db.AddItems(t.Context(), src.ID, []store.NewItem{{GUID: "1",
		Title: "A pending article", URL: "https://example.org/pending",
		SummaryHTML: `<script>alert(1)</script><p>the summary</p><iframe src="https://evil.example/"></iframe>`}})
	if err != nil || len(ids) != 1 {
		t.Fatalf("items = %v, %v", ids, err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/content/" + h.Path(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(body), "<script") || strings.Contains(string(body), "<iframe") {
		t.Fatalf("the page keeps hostile HTML:\n%s", body)
	}
	if !strings.Contains(string(body), "the summary") {
		t.Fatalf("the page lacks the summary:\n%s", body)
	}
}

func TestWrongMACIs404(t *testing.T) {
	h, _, id := setup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	good := "/content/" + h.Path(id)
	for _, path := range []string{
		strings.TrimSuffix(good, good[len(good)-4:]) + "aaaa", // a wrong MAC
		"/content/item/99/" + good[len(good)-22:],             // another item
		"/content/item/x/" + good[len(good)-22:],
		"/content/item/1", // no MAC at all
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, res.StatusCode)
		}
	}
}

// The path is relative to the content root, as lodge-client expects: no
// leading slash, and no "content/" segment, which this handler answers
// under.
func TestPathIsRelativeToTheContentRoot(t *testing.T) {
	h, _, id := setup(t)
	path := h.Path(id)
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "content/") || !strings.HasPrefix(path, "item/") {
		t.Fatalf("Path = %q, want item/<id>/<mac>", path)
	}
	// The handler answers /content/ and the path.
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/content/" + path)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/content/%s = %d, want 200", path, res.StatusCode)
	}
}

// The page follows the scheme the web app passes, and the reader's own
// preference when it passes none.
func TestScheme(t *testing.T) {
	h, _, id := setup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"", `<html lang="en">`},
		{"?scheme=dark", `<html lang="en" data-scheme="dark">`},
		{"?scheme=light", `<html lang="en" data-scheme="light">`},
		{"?scheme=sepia", `<html lang="en">`},
	} {
		res, err := http.Get(srv.URL + "/content/" + h.Path(id) + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !strings.Contains(string(body), tc.want) {
			t.Fatalf("%q: the page does not start %s", tc.query, tc.want)
		}
	}
}

func TestMAC(t *testing.T) {
	h := &Handler{Secret: []byte("secret")}
	a, b := h.mac(1), h.mac(2)
	if len(a) != 22 || a == b {
		t.Fatalf("macs = %q, %q", a, b)
	}
	if h.mac(1) != a {
		t.Fatal("the mac is not stable")
	}
	other := &Handler{Secret: []byte("another secret")}
	if other.mac(1) == a {
		t.Fatal("two secrets make one mac")
	}
}

func TestMissingItemIs404(t *testing.T) {
	h, _, _ := setup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/content/" + h.Path(999))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown item = %d", res.StatusCode)
	}
}

// The article's date is the daemon's local day, which is the day the
// reader's header shows.
func TestDateIsTheDaemonsLocal(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	h := &Handler{Store: db, Blobs: b, Secret: []byte("secret"), Location: time.FixedZone("ahead", 2*60*60)}
	srv := httptest.NewServer(h)
	defer srv.Close()
	src, err := db.AddSource(t.Context(), store.Source{URL: "https://example.com/feed.xml", Title: "Example"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := db.AddItems(t.Context(), src.ID, []store.NewItem{{
		GUID: "1", Title: "An evening article", URL: "https://example.com/a",
		Published:   time.Date(2026, 10, 13, 22, 30, 0, 0, time.UTC),
		SummaryHTML: "<p>Words.</p>",
	}})
	if err != nil || len(ids) != 1 {
		t.Fatalf("items = %v, %v", ids, err)
	}
	res, err := http.Get(srv.URL + "/content/" + h.Path(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	// 22:30 UTC is 00:30 the next day in a zone two hours ahead.
	if !strings.Contains(string(body), "14 October 2026") {
		t.Fatalf("the page's date is not the local day:\n%s", body)
	}
	if strings.Contains(string(body), "13 October 2026") {
		t.Fatalf("the page keeps the UTC day:\n%s", body)
	}
}
