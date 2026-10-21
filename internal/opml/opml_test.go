package opml

import (
	"errors"
	"strings"
	"testing"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Subscriptions</title></head>
  <body>
    <outline text="Technology" title="Technology">
      <outline type="rss" text="Example Journal" title="Example Journal"
               xmlUrl="https://example.com/feed.xml" htmlUrl="https://example.com/"/>
      <outline type="rss" text="Example Atoms" xmlUrl="https://example.org/atom.xml"/>
    </outline>
    <outline type="rss" text="Example Feeds" xmlUrl="https://example.net/feed.json"/>
  </body>
</opml>
`

func TestImportFlattensFolders(t *testing.T) {
	feeds, err := Import(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 3 {
		t.Fatalf("feeds = %+v, want 3", feeds)
	}
	if feeds[0].Title != "Example Journal" || feeds[0].HTMLURL != "https://example.com/" {
		t.Fatalf("first feed = %+v", feeds[0])
	}
	if feeds[1].XMLURL != "https://example.org/atom.xml" {
		t.Fatalf("second feed = %+v", feeds[1])
	}
}

func TestExportRoundTrip(t *testing.T) {
	in := []Feed{
		{Title: "Example Feeds", XMLURL: "https://example.net/feed.json"},
		{Title: "Example Journal", XMLURL: "https://example.com/feed.xml", HTMLURL: "https://example.com/"},
	}
	data, err := Export(in)
	if err != nil {
		t.Fatal(err)
	}
	feeds, err := Import(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("import the export: %v\n%s", err, data)
	}
	if len(feeds) != 2 {
		t.Fatalf("round trip = %+v", feeds)
	}
	if feeds[0].Title != "Example Feeds" || feeds[1].HTMLURL != "https://example.com/" {
		t.Fatalf("round trip order or fields = %+v", feeds)
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	if _, err := Import(strings.NewReader("not xml")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage = %v, want ErrInvalid", err)
	}
	if _, err := Import(strings.NewReader(`<opml version="2.0"><body><outline text="A folder"/></body></opml>`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an opml with no feed = %v, want ErrInvalid", err)
	}
}
