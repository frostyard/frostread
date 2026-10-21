//go:build devfeeds

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/opml"
)

// The three feeds parse, and their articles answer with a page and an
// image, so the live check has something to read.
func TestDevelopmentFeeds(t *testing.T) {
	srv := httptest.NewServer(handler("http://127.0.0.1:8088"))
	defer srv.Close()
	for _, path := range []string{"/rss.xml", "/atom.xml", "/feed.json"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, %v", path, res.StatusCode, err)
		}
		f, err := feed.Parse(body, srv.URL+path)
		if err != nil || len(f.Items) != 2 {
			t.Fatalf("%s = %v, %v; want 2 items", path, f, err)
		}
	}
	res, err := http.Get(srv.URL + "/articles/1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "<article>") || !strings.Contains(string(body), "/img/tomato.png") {
		t.Fatalf("an article page:\n%s", body)
	}
	res, err = http.Get(srv.URL + "/img/tomato.png")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("the image's type = %q", got)
	}
	// The OPML names three feeds that this server serves.
	feeds, err := opml.Import(strings.NewReader(opmlDocument(srv.URL)))
	if err != nil || len(feeds) != 3 {
		t.Fatalf("opml = %v, %v", feeds, err)
	}
	for _, f := range feeds {
		res, err := http.Get(f.XMLURL)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", f.XMLURL, res.StatusCode)
		}
	}
}
