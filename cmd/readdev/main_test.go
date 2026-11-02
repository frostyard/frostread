//go:build devfeeds

package main

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/opml"
)

// The three feeds parse, and their articles answer with a page and a real
// image, so the live check has something to read.
func TestDevelopmentFeeds(t *testing.T) {
	srv := httptest.NewServer(handler("http://127.0.0.1:8088", nil))
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
	// Each article ends with a link off the site, which the app's
	// end-to-end test clicks (plan 0002, step 3.6).
	if !strings.Contains(string(body), `href="https://example.org/journal/1"`) {
		t.Fatalf("article 1 does not link to the archive:\n%s", body)
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
	// The OPML names five feeds that this server serves.
	feeds, err := opml.Import(strings.NewReader(opmlDocument(srv.URL, nil)))
	if err != nil || len(feeds) != 5 {
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

// Two development feeds fail their polls, so the owner sees a source with
// an error marker and its message: one answers 500, and one 429 with a
// Retry-After. Each answers its feed once first, so subscribing works.
func TestFailingFeeds(t *testing.T) {
	srv := httptest.NewServer(handler("http://127.0.0.1:8088", nil))
	defer srv.Close()
	for _, tc := range []struct {
		path   string
		status int
		retry  string
	}{
		{"/broken.xml", http.StatusInternalServerError, ""},
		{"/busy.xml", http.StatusTooManyRequests, "120"},
	} {
		first, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(first.Body)
		_ = first.Body.Close()
		if first.StatusCode != http.StatusOK {
			t.Fatalf("%s's first answer = %d", tc.path, first.StatusCode)
		}
		if f, err := feed.Parse(body, srv.URL+tc.path); err != nil || len(f.Items) != 1 {
			t.Fatalf("%s's feed = %v, %v; want 1 item", tc.path, f, err)
		}
		second, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		_ = second.Body.Close()
		if second.StatusCode != tc.status {
			t.Fatalf("%s's second answer = %d, want %d", tc.path, second.StatusCode, tc.status)
		}
		if got := second.Header.Get("Retry-After"); got != tc.retry {
			t.Fatalf("%s's Retry-After = %q, want %q", tc.path, got, tc.retry)
		}
	}
}

// Each article has its own words, so a search for one finds only that one;
// only the tomato article mentions tomatoes.
func TestArticlesAreDistinct(t *testing.T) {
	srv := httptest.NewServer(handler("http://127.0.0.1:8088", nil))
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

// hostileDir writes a made-up corpus of three pages into a fresh temporary
// directory, and returns its path and the pages: name and bytes.
func hostileDir(t *testing.T) (string, []struct{ name, body string }) {
	t.Helper()
	pages := []struct{ name, body string }{
		{"01-alpha-words.html", `<p>alpha</p><script>document.body.append(document.createElement("x-ran"));fetch("http://canary.test/01-alpha-words")</script>`},
		{"02-beta-script.html", `<a href="javascript:document.body.append(document.createElement('x-ran'))">beta</a><img src="http://canary.test/02-beta-script.png">`},
		{"03-gamma-frame.html", `<iframe srcdoc="<p>gamma</p>"></iframe>`},
	}
	dir := t.TempDir()
	for _, p := range pages {
		if err := os.WriteFile(filepath.Join(dir, p.name), []byte(p.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, pages
}

// The hostile corpus is one feed item per page, with a title that names the
// file, and a link to the page that readd extracts from.
func TestHostileFeed(t *testing.T) {
	dir, pages := hostileDir(t)
	hostile, err := loadHostile(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:8088"
	srv := httptest.NewServer(handler(base, hostile))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/hostile.xml")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("/hostile.xml = %d, %v", res.StatusCode, err)
	}
	f, err := feed.Parse(body, srv.URL+"/hostile.xml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Dev Hostile" || len(f.Items) != len(pages) {
		t.Fatalf("the hostile feed = %q with %d items, want Dev Hostile with %d", f.Title, len(f.Items), len(pages))
	}
	for i, tc := range []struct{ title, stem string }{
		{"Hostile 01: alpha words", "01-alpha-words"},
		{"Hostile 02: beta script", "02-beta-script"},
		{"Hostile 03: gamma frame", "03-gamma-frame"},
	} {
		item := f.Items[i]
		if item.Title != tc.title {
			t.Errorf("item %d's title = %q, want %q", i+1, item.Title, tc.title)
		}
		want := base + "/hostile/" + tc.stem
		if item.URL != want || item.GUID != want {
			t.Errorf("item %d = %q, %q; want %q in both", i+1, item.URL, item.GUID, want)
		}
		// Each item is later than the one before it, and later than every
		// development article.
		if !item.Published.After(time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)) {
			t.Errorf("item %d is published %v", i+1, item.Published)
		}
		if i > 0 && !item.Published.After(f.Items[i-1].Published) {
			t.Errorf("item %d is not later than item %d", i+1, i)
		}
	}
}

// A page answers with the file's own bytes, and then a paragraph of made-up
// text, so a readability extraction has words to find.
func TestHostilePages(t *testing.T) {
	dir, pages := hostileDir(t)
	hostile, err := loadHostile(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler("http://127.0.0.1:8088", hostile))
	defer srv.Close()

	for _, page := range pages {
		stem := strings.TrimSuffix(page.name, ".html")
		res, err := http.Get(srv.URL + "/hostile/" + stem)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("/hostile/%s = %d", stem, res.StatusCode)
		}
		if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("/hostile/%s's type = %q", stem, got)
		}
		// The item's title first, so the extraction keeps it, then the
		// file's bytes as they are.
		head := "<title>Hostile " + strings.Replace(strings.ReplaceAll(stem, "-", " "), " ", ": ", 1) + "</title>\n"
		if !strings.HasPrefix(string(body), head+page.body) {
			t.Errorf("/hostile/%s does not start with its title and the file's bytes:\n%s", stem, body)
		}
		if added := len(body) - len(head) - len(page.body); added < 600 {
			t.Errorf("/hostile/%s adds %d bytes of text, want about 700", stem, added)
		}
	}

	// No name answers unless the corpus holds it, and no path leaves the
	// directory.
	for _, path := range []string{"/hostile/99-nope", "/hostile/01-alpha-words.html", "/hostile/../99-nope", "/hostile/"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, res.StatusCode)
		}
	}
}

// Without a corpus neither the hostile feed nor a page exists, so the
// development server is the five feeds alone.
func TestNoHostileCorpus(t *testing.T) {
	base := "http://127.0.0.1:8088"
	srv := httptest.NewServer(handler(base, nil))
	defer srv.Close()

	for _, path := range []string{"/hostile.xml", "/hostile/01-alpha-words"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, res.StatusCode)
		}
	}
	feeds, err := opml.Import(strings.NewReader(opmlDocument(base, nil)))
	if err != nil || len(feeds) != 5 {
		t.Fatalf("opml without a corpus = %v, %v; want 5 feeds", feeds, err)
	}
}

// With a corpus the OPML names Dev Hostile too, and the feed it names is
// the hostile feed this server serves.
func TestHostileOPML(t *testing.T) {
	dir, _ := hostileDir(t)
	hostile, err := loadHostile(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:8088"
	srv := httptest.NewServer(handler(base, hostile))
	defer srv.Close()

	feeds, err := opml.Import(strings.NewReader(opmlDocument(base, hostile)))
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 6 {
		t.Fatalf("opml with a corpus = %d feeds, want 6", len(feeds))
	}
	last := feeds[len(feeds)-1]
	if last.Title != "Dev Hostile" || last.XMLURL != base+"/hostile.xml" {
		t.Fatalf("the last feed = %+v, want Dev Hostile at %s/hostile.xml", last, base)
	}
	res, err := http.Get(srv.URL + "/hostile.xml")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d", last.XMLURL, res.StatusCode)
	}
}

// An unreadable corpus directory is an error at start, not an empty feed.
func TestHostileDirMissing(t *testing.T) {
	if _, err := loadHostile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing corpus directory loaded with no error")
	}
}
