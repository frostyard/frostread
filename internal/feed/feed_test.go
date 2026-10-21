package feed

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		title   string
		site    string
		items   int
		firstID string
		first   string
		author  string
	}{
		{name: "rss.xml", base: "https://example.com/feed.xml", title: "Example Journal",
			site: "https://example.com/", items: 2, firstID: "urn:uuid:1", first: "https://example.com/first"},
		{name: "atom.xml", base: "https://example.org/atom.xml", title: "Example Atoms",
			site: "https://example.org/", items: 1, firstID: "tag:example.org,2026:1",
			first: "https://example.org/atom-entry", author: "Ann Author"},
		{name: "feed.json", base: "https://example.net/feed.json", title: "Example Feeds",
			site: "https://example.net/", items: 1, firstID: "2", first: "https://example.net/json-entry",
			author: "Jo Writer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Parse(read(t, tc.name), tc.base)
			if err != nil {
				t.Fatal(err)
			}
			if f.Title != tc.title || f.SiteURL != tc.site || len(f.Items) != tc.items {
				t.Fatalf("feed = %+v, want %q, %q, %d items", f, tc.title, tc.site, tc.items)
			}
			it := f.Items[0]
			if it.GUID != tc.firstID || it.URL != tc.first || it.Author != tc.author {
				t.Fatalf("first item = %+v", it)
			}
			if it.Title == "" {
				t.Fatal("the first item has no title")
			}
		})
	}
}

func TestParseResolvesRelativeLinks(t *testing.T) {
	f, err := Parse(read(t, "rss.xml"), "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Items[1].URL; got != "https://example.com/second" {
		t.Fatalf("relative link = %q", got)
	}
}

func TestGUIDFallbacks(t *testing.T) {
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><title>No ids</title>
	<item><title>Only a title</title><description>text</description></item>
	<item><link>https://example.org/linked</link><title>Linked</title></item>
	</channel></rss>`
	f, err := Parse([]byte(rss), "https://example.org/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 2 {
		t.Fatalf("items = %+v", f.Items)
	}
	if f.Items[0].GUID == "" || f.Items[0].GUID == f.Items[1].GUID {
		t.Fatalf("fallback guids = %q, %q", f.Items[0].GUID, f.Items[1].GUID)
	}
	if f.Items[1].GUID != "https://example.org/linked" {
		t.Fatalf("a link is not the guid: %q", f.Items[1].GUID)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("<html><body>not a feed</body></html>"), "https://example.com/"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage = %v, want ErrInvalid", err)
	}
	if _, err := Parse([]byte(`{"version":"https://jsonfeed.org/version/1.1","title":"Empty","items":[]}`),
		"https://example.com/feed.json"); err != nil {
		t.Fatalf("an empty feed = %v, want no error", err)
	}
}

func TestDiscover(t *testing.T) {
	got := Discover(read(t, "page.html"), "https://example.com/blog/post")
	want := []string{"https://example.com/feed.xml", "https://example.org/atom.xml"}
	if len(got) != len(want) {
		t.Fatalf("discovered = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discovered = %v, want %v", got, want)
		}
	}
	if got := Discover([]byte("<html><head></head><body>nothing</body></html>"), "https://example.com/"); len(got) != 0 {
		t.Fatalf("a page without a feed = %v", got)
	}
}

func TestPublishedDate(t *testing.T) {
	f, err := Parse(read(t, "atom.xml"), "https://example.org/atom.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if !f.Items[0].Published.Equal(want) {
		t.Fatalf("published = %v, want %v", f.Items[0].Published, want)
	}
}
