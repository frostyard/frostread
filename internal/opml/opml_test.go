package opml

import (
	"errors"
	"slices"
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

// nested has a folder in a folder, a folder whose only name is its text,
// and a feed at the top level.
const nested = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <body>
    <outline text="Technology" title="Technology">
      <outline text="Programming" title="Programming">
        <outline type="rss" text="Example Journal" xmlUrl="https://example.com/feed.xml"/>
      </outline>
      <outline type="rss" text="Example Atoms" xmlUrl="https://example.org/atom.xml"/>
    </outline>
    <outline text="Gardening">
      <outline type="rss" text="Example Feeds" xmlUrl="https://example.net/feed.json"/>
    </outline>
    <outline type="rss" text="Top Level" xmlUrl="https://example.com/top.xml"/>
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
	// The outer folder names its feeds; the feed at the top level is in
	// no folder.
	if feeds[0].Folder != "Technology" || feeds[1].Folder != "Technology" || feeds[2].Folder != "" {
		t.Fatalf("folders = %q, %q, %q; want Technology, Technology, none",
			feeds[0].Folder, feeds[1].Folder, feeds[2].Folder)
	}
}

// A feed's folder is its outermost folder's name, so a folder in a folder
// adds nothing, and a folder with no title is named by its text.
func TestImportKeepsTheOutermostFolder(t *testing.T) {
	feeds, err := Import(strings.NewReader(nested))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ title, folder string }{
		{"Example Journal", "Technology"},
		{"Example Atoms", "Technology"},
		{"Example Feeds", "Gardening"},
		{"Top Level", ""},
	}
	if len(feeds) != len(want) {
		t.Fatalf("feeds = %+v, want %d", feeds, len(want))
	}
	for i, w := range want {
		if feeds[i].Title != w.title || feeds[i].Folder != w.folder {
			t.Fatalf("feed %d = %q in %q; want %q in %q", i, feeds[i].Title, feeds[i].Folder, w.title, w.folder)
		}
	}
}

// An outermost folder with no name is still the outermost folder: a deeper
// folder does not name its feeds, so they are in no folder.
func TestImportUnnamedOuterFolder(t *testing.T) {
	doc := `<opml version="2.0"><body><outline text="">` +
		`<outline text="Inner"><outline type="rss" text="Example" xmlUrl="https://example.com/feed.xml"/></outline>` +
		`</outline></body></opml>`
	feeds, err := Import(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 || feeds[0].Folder != "" {
		t.Fatalf("feeds = %+v; want 1 feed in no folder", feeds)
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
	if !strings.Contains(string(data), `<opml version="2.0">`) {
		t.Fatalf("export root is not OPML 2.0:\n%s", data)
	}
	if !strings.Contains(string(data), `<head>`) || !strings.Contains(string(data), `<title>Frostread subscriptions</title>`) {
		t.Fatalf("export has no Frostread subscriptions head:\n%s", data)
	}
	if feeds[0].Folder != "" || feeds[1].Folder != "" {
		t.Fatalf("round trip folders = %+v", feeds)
	}
}

// The export writes the folders first, by name, and then the feeds that
// are in none; an import of it gives the same feeds and folders back.
func TestExportWritesFoldersAndRoundTrips(t *testing.T) {
	in := []Feed{
		{Title: "Example Journal", XMLURL: "https://example.com/feed.xml", HTMLURL: "https://example.com/", Folder: "News"},
		{Title: "Example Atoms", XMLURL: "https://example.org/atom.xml", Folder: "Garden"},
		{Title: "Example Feeds", XMLURL: "https://example.net/feed.json", Folder: "Garden"},
		{Title: "Top Level", XMLURL: "https://example.com/top.xml"},
		// Two spellings are two folders, as the store's filter sees them.
		{Title: "Example Seeds", XMLURL: "https://example.org/seeds.xml", Folder: "garden"},
	}
	data, err := Export(in)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, `<opml version="2.0">`) {
		t.Fatalf("folder export root is not OPML 2.0:\n%s", doc)
	}
	if !strings.Contains(doc, `<head>`) || !strings.Contains(doc, `<title>Frostread subscriptions</title>`) {
		t.Fatalf("folder export has no Frostread subscriptions head:\n%s", doc)
	}
	garden := strings.Index(doc, `<outline text="Garden" title="Garden">`)
	news := strings.Index(doc, `<outline text="News" title="News">`)
	top := strings.Index(doc, `xmlUrl="https://example.com/top.xml"`)
	lower := strings.Index(doc, `<outline text="garden" title="garden">`)
	if garden < 0 || lower < 0 || news < 0 || top < 0 || garden >= lower || lower >= news || news >= top {
		t.Fatalf("folders are not first, by name:\n%s", doc)
	}
	if strings.Count(doc, `type="rss"`) != len(in) {
		t.Fatalf("a folder outline carries a type:\n%s", doc)
	}

	feeds, err := Import(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("import the export: %v\n%s", err, doc)
	}
	got := slices.Clone(feeds)
	slices.SortFunc(got, func(a, b Feed) int { return strings.Compare(a.XMLURL, b.XMLURL) })
	want := slices.Clone(in)
	slices.SortFunc(want, func(a, b Feed) int { return strings.Compare(a.XMLURL, b.XMLURL) })
	if !slices.EqualFunc(got, want, func(a, b Feed) bool {
		return a.Title == b.Title && a.XMLURL == b.XMLURL && a.HTMLURL == b.HTMLURL && a.Folder == b.Folder
	}) {
		t.Fatalf("round trip = %+v; want %+v", got, want)
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
