package content

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
	res, err := http.Get(srv.URL + h.Path(id))
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

func TestWrongMACIs404(t *testing.T) {
	h, _, id := setup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	good := h.Path(id)
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
	res, err := http.Get(srv.URL + h.Path(999))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown item = %d", res.StatusCode)
	}
}
