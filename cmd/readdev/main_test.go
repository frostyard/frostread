//go:build devfeeds

package main

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/opml"
)

// The three feeds parse, and their articles answer with a page and a real
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
	if !strings.Contains(string(body), "<article>") || !strings.Contains(string(body), "/img/1.png") {
		t.Fatalf("an article page:\n%s", body)
	}
	// The image is a real, visible picture: a 480 by 270 PNG.
	pic, err := http.Get(srv.URL + "/img/1.png")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(pic.Body)
	_ = pic.Body.Close()
	if got := pic.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("the image's type = %q", got)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode the image: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 480 || b.Dy() != 270 {
		t.Fatalf("the image is %dx%d, want 480x270", b.Dx(), b.Dy())
	}
	if len(data) < 1000 {
		t.Fatalf("the image is %d bytes: not a drawn picture", len(data))
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

// Each article has its own words, so a search for one finds only that one;
// only the tomato article mentions tomatoes.
func TestArticlesAreDistinct(t *testing.T) {
	srv := httptest.NewServer(handler("http://127.0.0.1:8088"))
	defer srv.Close()
	texts := make([]string, len(articles))
	tomatoes := 0
	for i := range articles {
		res, err := http.Get(srv.URL + "/articles/" + itoa(i+1))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		texts[i] = string(body)
		if strings.Contains(strings.ToLower(texts[i]), "tomato") {
			tomatoes++
		}
	}
	if tomatoes != 1 {
		t.Fatalf("%d articles mention tomatoes, want 1", tomatoes)
	}
	// Every page differs, and each has a word of its own that no other has.
	words := []string{"steady drink", "heron", "saddle", "yeast", "reading list", "quince"}
	for i := range texts {
		if !strings.Contains(texts[i], words[i]) {
			t.Fatalf("article %d does not hold %q", i+1, words[i])
		}
		for j := i + 1; j < len(texts); j++ {
			if texts[i] == texts[j] {
				t.Fatalf("articles %d and %d are the same page", i+1, j+1)
			}
			if strings.Contains(texts[j], words[i]) {
				t.Fatalf("article %d holds article %d's word %q", j+1, i+1, words[i])
			}
		}
	}
}

// itoa is a small integer's decimal form, for a URL.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
