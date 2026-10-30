package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/store"
)

// net is a fake network: URL -> answer.
type net struct {
	mu      sync.Mutex
	pages   map[string]answer
	fetched []string
}

type answer struct {
	status  int
	body    string
	err     error
	etag    string
	retryAf time.Duration
}

func (n *net) Get(_ context.Context, rawurl string, o fetch.Options) (*fetch.Response, error) {
	n.mu.Lock()
	n.fetched = append(n.fetched, rawurl)
	n.mu.Unlock()
	p, ok := n.pages[rawurl]
	if !ok {
		return nil, errors.New("no such page: " + rawurl)
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.status == 0 {
		p.status = 200
	}
	res := &fetch.Response{Status: p.status, URL: rawurl, Header: http.Header{}, RetryAfter: p.retryAf}
	if p.etag != "" {
		res.Header.Set("ETag", p.etag)
		res.ETag = p.etag
	}
	if p.status == http.StatusNotModified {
		res.NotModified = true
		return res, nil
	}
	if int64(len(p.body)) > o.MaxBytes {
		return nil, fetch.ErrTooLarge
	}
	res.Body = []byte(p.body)
	return res, nil
}

func (n *net) requests() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.fetched...)
}

// saver stands in for notes' MCP endpoint.
type saver struct {
	mu   sync.Mutex
	arts []notes.Article
	err  error
	// find is what Find answers; present at the path when State is empty.
	find notes.Status
}

func (s *saver) Find(_ context.Context, path string, _ int64) notes.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.find.State != "" {
		st := s.find
		if st.Path == "" {
			st.Path = path
		}
		return st
	}
	if path == "" {
		return notes.Status{State: notes.StateMissing}
	}
	return notes.Status{State: notes.StatePresent, Path: path}
}

func (s *saver) Save(_ context.Context, a notes.Article) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	s.arts = append(s.arts, a)
	return "Reading/2026-10-21 " + a.Title + ".md", nil
}

const longText = `Plant tomatoes after the last frost. A tomato plant needs sun, water and a little patience.
The soil should be warm and loose, and the plant should go in deep enough that its stem can root.
Water it well for the first week, then less often and more deeply, and the roots will follow the water down.
A mulch of straw keeps the soil's moisture even and the fruit off the ground.
Pinch out the side shoots of a cordon variety, and let a bush variety grow as it likes.
Feed them once a week with a tomato feed once the first flowers set.
Take off the lower leaves as the fruit ripens, so the plant's energy goes into the fruit.
A greenhouse gives an earlier crop, and a windy site needs a shelter or a sturdier stake.
Save the seed of the best fruit for next year, and the variety will suit the garden in time.`

// feedXML is a feed with one short entry and one with the full article.
func feedXML() string {
	full := strings.ReplaceAll(longText, "\n", " ")
	return `<?xml version="1.0"?><rss version="2.0"><channel>
	<title>Example Journal</title><link>https://example.com/</link>
	<item><title>First post</title><link>https://example.com/first</link><guid>1</guid>
	<pubDate>Tue, 20 Oct 2026 09:00:00 GMT</pubDate><description>A short summary.</description></item>
	<item><title>A full article</title><link>https://example.com/full</link><guid>2</guid>
	<pubDate>Tue, 20 Oct 2026 10:00:00 GMT</pubDate><description>` + full + `</description></item>
	</channel></rss>`
}

const pageHTML = `<!doctype html><html><head><title>The first page</title></head><body>
<article><h1>The first page</h1>` + longText + `
<p>Companion planting with basil is an old gardener's trick.</p>
<p><img src="/tomato.png" alt="A ripe tomato"></p></article></body></html>`

var png = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}

func setup(t *testing.T) (*Engine, *net, *saver, *[]string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	db.Now = func() time.Time { return now }
	b := blobs.New(filepath.Join(dir, "blobs"))
	n := &net{pages: map[string]answer{
		"https://example.com/":           {body: `<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head><body>a page</body></html>`},
		"https://example.com/feed.xml":   {body: feedXML(), etag: `"v1"`},
		"https://example.com/first":      {body: pageHTML},
		"https://example.com/tomato.png": {body: string(png)},
	}}
	s := &saver{}
	var changes []string
	var mu sync.Mutex
	e := New(Options{
		Store: db, Blobs: b, Fetch: n, Extractor: &extract.Extractor{Fetch: n, Blobs: b},
		Config:   config.Config{PollInterval: "20m", NotesFolder: "Reading/", NotesVault: 1},
		Interval: 20 * time.Minute, Version: "test", Notes: s,
		ContentPath: func(id int64) string { return "/content/item/" + itoa(id) + "/mac" },
		OnChange: func(views []string) {
			mu.Lock()
			changes = append(changes, strings.Join(views, ","))
			mu.Unlock()
		},
		Now: func() time.Time { return now },
	})
	return e, n, s, &changes
}

func itoa(n int64) string {
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

func TestAddSourceDiscoversAndPolls(t *testing.T) {
	e, n, _, changes := setup(t)
	src, err := e.AddSource(t.Context(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if src.URL != "https://example.com/feed.xml" || src.Title != "Example Journal" {
		t.Fatalf("source = %+v", src)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if items[0].Title != "A full article" || items[1].Title != "First post" {
		t.Fatalf("order = %q, %q", items[0].Title, items[1].Title)
	}
	if items[0].SourceTitle != "Example Journal" || items[0].Excerpt == "" {
		t.Fatalf("summary = %+v", items[0])
	}
	if got := n.requests(); len(got) < 2 {
		t.Fatalf("requests = %v", got)
	}
	if len(*changes) == 0 {
		t.Fatal("no change notification")
	}
}

func TestExtraction(t *testing.T) {
	e, n, _, _ := setup(t)
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if err := e.ExtractItem(t.Context(), it.ID); err != nil {
			t.Fatalf("%s: %v", it.Title, err)
		}
	}
	first, err := e.Item(t.Context(), items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ExtractState != store.ExtractDone || first.Title != "The first page" {
		t.Fatalf("extracted item = %+v", first)
	}
	if first.ContentPath == "" || first.OriginalHash == "" {
		t.Fatalf("detail = %+v", first)
	}
	if !strings.Contains(first.ContentPath, "mac") {
		t.Fatalf("content path = %q", first.ContentPath)
	}
	// The page's image was fetched and its original kept.
	sawImage, sawPage := false, false
	for _, u := range n.requests() {
		switch u {
		case "https://example.com/tomato.png":
			sawImage = true
		case "https://example.com/first":
			sawPage = true
		}
	}
	if !sawImage || !sawPage {
		t.Fatalf("requests = %v", n.requests())
	}
	// The full article came from the feed: no page was fetched for it.
	for _, u := range n.requests() {
		if u == "https://example.com/full" {
			t.Fatal("the full feed article was fetched from its page")
		}
	}
	full, err := e.Item(t.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.ExtractState != store.ExtractDone || full.Title != "A full article" {
		t.Fatalf("full item = %+v", full)
	}
	// ExtractItem again changes nothing.
	if err := e.ExtractItem(t.Context(), items[0].ID); err != nil {
		t.Fatalf("a second extraction = %v", err)
	}
}

// feedOriginals is each item's original in feedXML, by GUID: its summary,
// then its content, as the feed parser gives them.
func feedOriginals(t *testing.T) map[string][]byte {
	t.Helper()
	f, err := feed.Parse([]byte(feedXML()), "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, it := range f.Items {
		out[it.GUID] = []byte(it.SummaryHTML + it.ContentHTML)
	}
	return out
}

// assertOriginal checks that hash names a stored blob whose bytes are want.
func assertOriginal(t *testing.T, b *blobs.Store, name, hash string, want []byte) {
	t.Helper()
	if hash != blobs.ID(want) {
		t.Fatalf("%s: original hash = %q, want the hash of its bytes as sent, %q", name, hash, blobs.ID(want))
	}
	got, err := b.Get(hash)
	if err != nil {
		t.Fatalf("%s: original blob: %v", name, err)
	}
	if !bytes.Equal(got, want) || blobs.ID(got) != hash {
		t.Fatalf("%s: original blob = %q, want %q", name, got, want)
	}
}

// A feed item's original is stored when the item is, and stays when the
// feed's own content is its article (ADR-0005).
func TestFeedItemsKeepTheirOriginal(t *testing.T) {
	e, _, _, _ := setup(t)
	src, err := e.AddSource(t.Context(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	originals := feedOriginals(t)
	if len(originals) != 2 || len(originals["1"]) == 0 || len(originals["2"]) == 0 {
		t.Fatalf("feed originals = %q", originals)
	}
	stored, err := e.o.Store.KnownOriginals(t.Context(), src.ID, []string{"1", "2", "3"})
	if err != nil || len(stored) != 2 {
		t.Fatalf("known originals = %v, %v", stored, err)
	}
	for guid, want := range originals {
		assertOriginal(t, e.o.Blobs, "item "+guid, stored[guid], want)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if err := e.ExtractItem(t.Context(), it.ID); err != nil {
			t.Fatalf("%s: %v", it.Title, err)
		}
	}
	// items[0] is the full article: its extraction took the feed's content
	// and kept the feed's original.
	full, err := e.Item(t.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, e.o.Blobs, "the full article", full.OriginalHash, originals["2"])
	// items[1]'s page was fetched: its original is the page as sent.
	first, err := e.Item(t.Context(), items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, e.o.Blobs, "the fetched page", first.OriginalHash, []byte(pageHTML))
}

// A re-poll stores the original of an item polled before originals were
// stored, when the feed still sends the bytes its row names, and only then.
func TestPollRestoresAMissingOriginal(t *testing.T) {
	e, _, _, _ := setup(t)
	src, err := e.AddSource(t.Context(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	originals := feedOriginals(t)
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Make the rows of an older daemon: the full article (GUID 2) names its
	// bytes with no blob, and the first post (GUID 1) names bytes the feed
	// no longer sends.
	for _, b := range originals {
		if _, _, err := e.o.Blobs.RemoveIf(blobs.ID(b), func(time.Time) (bool, error) { return true, nil }); err != nil {
			t.Fatal(err)
		}
	}
	changed := blobs.ID([]byte("a summary the feed sent once"))
	if err := e.o.Store.SetOriginal(t.Context(), items[1].ID, changed); err != nil {
		t.Fatal(err)
	}
	if err := e.Refresh(t.Context(), &src.ID); err != nil {
		t.Fatal(err)
	}
	full, err := e.Item(t.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, e.o.Blobs, "the restored original", full.OriginalHash, originals["2"])
	// The first post's row still names bytes nobody has: nothing is stored
	// under its hash, and the bytes the feed sends now are not stored.
	for _, h := range []string{changed, blobs.ID(originals["1"])} {
		if _, err := e.o.Blobs.Size(h); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("blob %s = %v, want none", h[:8], err)
		}
	}
	first, err := e.Item(t.Context(), items[1].ID)
	if err != nil || first.OriginalHash != changed {
		t.Fatalf("first post's original = %q, %v; want %q kept", first.OriginalHash, err, changed)
	}
}

// A blob write that fails fails the poll and keeps no ETag, so the next
// poll offers the items again, and no item is stored without its original.
func TestPollFailsWhenAnOriginalCannotBeStored(t *testing.T) {
	e, _, _, _ := setup(t)
	e.o.Blobs.MaxBytes = 4
	src, err := e.AddSource(t.Context(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.o.Store.Source(t.Context(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastError == "" || got.ETag != "" {
		t.Fatalf("source = %+v, want a poll error and no ETag", got)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewAll, Limit: 50})
	if err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v; want none", items, err)
	}
	// The store recovers: the next poll stores the items and their originals.
	e.o.Blobs.MaxBytes = 0
	if err := e.Refresh(t.Context(), &src.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := e.o.Store.KnownOriginals(t.Context(), src.ID, []string{"1", "2"})
	if err != nil || len(stored) != 2 {
		t.Fatalf("known originals = %v, %v", stored, err)
	}
	for guid, want := range feedOriginals(t) {
		assertOriginal(t, e.o.Blobs, "item "+guid, stored[guid], want)
	}
}

func TestExtractionFailure(t *testing.T) {
	e, n, _, _ := setup(t)
	n.pages["https://example.com/first"] = answer{status: 500}
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err := e.ExtractItem(t.Context(), items[1].ID); err == nil {
		t.Fatal("extracting a failed page succeeded")
	}
	d, err := e.Item(t.Context(), items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.ExtractState != store.ExtractFailed || d.ExtractError == "" {
		t.Fatalf("failed item = %+v", d)
	}
}

func TestScopeGuardsWrites(t *testing.T) {
	e, _, _, _ := setup(t)
	read := Scope{}
	if _, err := e.SaveURL(t.Context(), read, "https://example.org/later", ""); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("save with a read scope = %v, want ErrReadOnly", err)
	}
	if err := e.SetRead(t.Context(), read, []int64{1}, true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("mark read with a read scope = %v", err)
	}
	if err := e.SetStarred(t.Context(), read, 1, true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("star with a read scope = %v", err)
	}
	if _, err := e.SaveURL(t.Context(), Scope{Write: true}, "http://127.0.0.1/inside", ""); err == nil {
		t.Fatal("an inside url was saved")
	}
}

// A saved page's URL is checked before it is stored: loopback, an address
// netguard refuses, a tailnet name, a homelab name and a scheme that is
// not http or https are all refused (ADR-0003).
func TestSaveURLRefusesAnAddressInsideTheNetwork(t *testing.T) {
	e, _, _, _ := setup(t)
	for _, rawurl := range []string{
		"http://127.0.0.1/inside",
		"http://192.0.2.1/page",
		"http://[::1]/page",
		"http://lodge.example.ts.net/page",
		"http://nas.local/page",
		"http://router/page",
		"javascript:alert(1)",
	} {
		if _, err := e.SaveURL(t.Context(), Scope{Write: true}, rawurl, ""); err == nil {
			t.Errorf("saving %q succeeded, want a refusal", rawurl)
		}
	}
	c, err := e.Counts(t.Context())
	if err != nil || c.Views[store.ViewLater] != 0 {
		t.Fatalf("later = %d, %v; want 0, so nothing refused was stored", c.Views[store.ViewLater], err)
	}
}

// A title from a page is other people's words: control characters go, runs
// of white space become one space, and it is cut to 300 runes. An empty
// title becomes the URL's host.
func TestSaveURLCleansTheTitle(t *testing.T) {
	e, _, _, _ := setup(t)
	write := Scope{Write: true}
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"plain", "A page about feeds", "A page about feeds"},
		{"white space", "  A \t page \n about   feeds ", "A page about feeds"},
		{"control characters", "A\x00 page\x07 about feeds", "A page about feeds"},
		{"line break", "A page\nfrom my phone", "A page from my phone"},
		{"empty", "", "example.org"},
		{"white space only", " \t\n ", "example.org"},
		{"cut", strings.Repeat("é", 301), strings.Repeat("é", 300)},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it, err := e.SaveURL(t.Context(), write, fmt.Sprintf("https://example.org/title-%d", i), c.title)
			if err != nil {
				t.Fatal(err)
			}
			if it.Title != c.want {
				t.Fatalf("title = %q, want %q", it.Title, c.want)
			}
		})
	}
}

func TestSaveURLIsIdempotent(t *testing.T) {
	e, _, _, _ := setup(t)
	write := Scope{Write: true}
	first, err := e.SaveURL(t.Context(), write, "https://example.org/read-later", "The page's title")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.SaveURL(t.Context(), write, "https://example.org/read-later", "Another title")
	if err != nil || first.ID != second.ID {
		t.Fatalf("second save = %+v, %v", second, err)
	}
	// A second save keeps the item's title: the owner's first save is what
	// the item is called.
	if second.Title != "The page's title" || first.Title != second.Title {
		t.Fatalf("title after a second save = %q, want %q", second.Title, first.Title)
	}
	c, err := e.Counts(t.Context())
	if err != nil || c.Views[store.ViewLater] != 1 {
		t.Fatalf("counts = %+v, %v", c, err)
	}
}

// items.noteStatus: a present note stays, a moved one records its path, a
// missing one clears the link, and notes out of reach keeps it.
func TestNoteStatus(t *testing.T) {
	e, _, s, _ := setup(t)
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err := e.ExtractItem(t.Context(), items[1].ID); err != nil {
		t.Fatal(err)
	}
	path, _, err := e.SaveAsNote(t.Context(), items[1].ID)
	if err != nil {
		t.Fatal(err)
	}

	st, err := e.NoteStatus(t.Context(), items[1].ID)
	if err != nil || st.State != notes.StatePresent || st.Path != path {
		t.Fatalf("present = %+v, %v", st, err)
	}

	s.mu.Lock()
	s.find = notes.Status{State: notes.StateMoved, Path: "Reading/1 - moved.md"}
	s.mu.Unlock()
	st, err = e.NoteStatus(t.Context(), items[1].ID)
	if err != nil || st.State != notes.StateMoved {
		t.Fatalf("moved = %+v, %v", st, err)
	}
	if got, _ := e.Item(t.Context(), items[1].ID); got.NotePath != "Reading/1 - moved.md" {
		t.Fatalf("the moved path was not recorded: %q", got.NotePath)
	}
	if route := e.NoteRoute(st.Path); route != "/notes/1/Reading/1%20-%20moved.md" {
		t.Fatalf("route = %q", route)
	}

	s.mu.Lock()
	s.find = notes.Status{State: notes.StateMissing}
	s.mu.Unlock()
	if st, err = e.NoteStatus(t.Context(), items[1].ID); err != nil || st.State != notes.StateMissing {
		t.Fatalf("missing = %+v, %v", st, err)
	}
	if got, _ := e.Item(t.Context(), items[1].ID); got.NotePath != "" {
		t.Fatalf("the missing note's path stayed: %q", got.NotePath)
	}

	// Saving again writes a new note, and notes out of reach keeps it.
	if _, _, err := e.SaveAsNote(t.Context(), items[1].ID); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.find = notes.Status{State: notes.StateUnreachable, Error: "notes is down"}
	s.mu.Unlock()
	st, err = e.NoteStatus(t.Context(), items[1].ID)
	if err != nil || st.State != notes.StateUnreachable || st.Error == "" {
		t.Fatalf("unreachable = %+v, %v", st, err)
	}
	if got, _ := e.Item(t.Context(), items[1].ID); got.NotePath == "" {
		t.Fatal("an unreachable notes dropped the link")
	}
}

func TestSaveAsNote(t *testing.T) {
	e, _, s, _ := setup(t)
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if _, _, err := e.SaveAsNote(t.Context(), items[1].ID); err == nil {
		t.Fatal("saving an unextracted item succeeded")
	}
	if err := e.ExtractItem(t.Context(), items[1].ID); err != nil {
		t.Fatal(err)
	}
	path, route, err := e.SaveAsNote(t.Context(), items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if path != "Reading/2026-10-21 The first page.md" || route != "/notes/1/Reading/2026-10-21%20The%20first%20page.md" {
		t.Fatalf("note = %q, %q", path, route)
	}
	if len(s.arts) != 1 || s.arts[0].Site != "Example Journal" || !strings.Contains(s.arts[0].Content, "blob:") {
		t.Fatalf("article passed to notes = %+v", s.arts)
	}
	if d, err := e.Item(t.Context(), items[1].ID); err != nil || d.NotePath != path {
		t.Fatalf("note path = %+v, %v", d, err)
	}
}

func TestSaveAsNoteUnconfigured(t *testing.T) {
	e, _, _, _ := setup(t)
	e.SetNotes(nil)
	if _, _, err := e.SaveAsNote(t.Context(), 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("save with no notes = %v, want ErrUnavailable", err)
	}
}

func TestPollBackoff(t *testing.T) {
	e, _, _, _ := setup(t)
	now := e.now()
	for _, tc := range []struct {
		failures   int
		retryAfter time.Duration
		want       time.Duration
	}{
		{failures: 0, want: 20 * time.Minute},
		{failures: 1, want: 40 * time.Minute},
		{failures: 2, want: 80 * time.Minute},
		{failures: 20, want: MaxBackoff},
		{failures: 1, retryAfter: 3 * time.Hour, want: 3 * time.Hour},
	} {
		got := e.nextPoll(now, tc.failures, tc.retryAfter).Sub(now)
		if tc.want == MaxBackoff {
			if got < MaxBackoff*9/10 || got > MaxBackoff {
				t.Fatalf("%d failures = %v, want about %v", tc.failures, got, MaxBackoff)
			}
			continue
		}
		if got < tc.want*9/10 || got > tc.want*11/10+time.Second {
			t.Fatalf("%d failures = %v, want about %v", tc.failures, got, tc.want)
		}
	}
}

func TestPollFailureAndRecovery(t *testing.T) {
	e, n, _, _ := setup(t)
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	n.pages["https://example.com/feed.xml"] = answer{err: errors.New("network down")}
	srcs, _ := e.Sources(t.Context())
	id := srcs[0].ID
	if err := e.Refresh(t.Context(), &id); err == nil {
		t.Fatal("a failed poll returned no error")
	}
	src, err := e.Source(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if src.ErrorCount != 1 || src.LastError == "" || !src.NextPollAt.After(e.now()) {
		t.Fatalf("source after a failure = %+v", src)
	}
	// The poller leaves it alone until its next time comes.
	if due, err := e.o.Store.DueSources(t.Context(), e.now(), 4); err != nil || len(due) != 0 {
		t.Fatalf("due = %+v, %v; want none", due, err)
	}
	// A conditional GET answers 304, which is a success.
	n.pages["https://example.com/feed.xml"] = answer{status: 304}
	if err := e.Refresh(t.Context(), &id); err != nil {
		t.Fatal(err)
	}
	src, _ = e.Source(t.Context(), id)
	if src.ErrorCount != 0 || src.LastError != "" {
		t.Fatalf("source after a 304 = %+v", src)
	}
}

func TestMarksAndSearch(t *testing.T) {
	e, _, _, _ := setup(t)
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	write := Scope{Write: true}
	if err := e.SetRead(t.Context(), write, []int64{items[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.SetStarred(t.Context(), write, items[1].ID, true); err != nil {
		t.Fatal(err)
	}
	c, err := e.Counts(t.Context())
	if err != nil || c.Views[store.ViewUnread] != 1 || c.Views[store.ViewStarred] != 1 {
		t.Fatalf("counts = %+v, %v", c, err)
	}
	found, err := e.Search(t.Context(), "tomatoes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("search found nothing")
	}
	// Search covers the extracted text too.
	if err := e.ExtractItem(t.Context(), items[1].ID); err != nil {
		t.Fatal(err)
	}
	found, err = e.Search(t.Context(), "basil", 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("search after extraction = %+v, %v", found, err)
	}
	if err := e.MarkAllRead(t.Context(), write, store.ViewAll, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.Counts(t.Context()); c.Views[store.ViewUnread] != 0 {
		t.Fatalf("unread after mark all = %d", c.Views[store.ViewUnread])
	}
}

func TestOPMLImportAndExport(t *testing.T) {
	e, n, _, _ := setup(t)
	n.pages["https://example.com/second.xml"] = answer{body: `<?xml version="1.0"?><rss version="2.0"><channel>
		<title>Second Feed</title><link>https://example.com/second</link>
		<item><title>An item</title><link>https://example.com/item</link><guid>9</guid></item></channel></rss>`}
	doc := `<?xml version="1.0"?><opml version="2.0"><body>
		<outline text="Example" title="Example" xmlUrl="https://example.com/feed.xml"/>
		<outline text="Second" title="Second" xmlUrl="https://example.com/second.xml"/>
		<outline text="Bad" xmlUrl="http://127.0.0.1/feed.xml"/>
	</body></opml>`
	added, skipped, err := e.ImportOPML(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || skipped != 1 {
		t.Fatalf("import = %d added, %d skipped", added, skipped)
	}
	out, err := e.ExportOPML(t.Context())
	if err != nil || !strings.Contains(out, "https://example.com/second.xml") {
		t.Fatalf("export = %q, %v", out, err)
	}
	srcs, _ := e.Sources(t.Context())
	if len(srcs) != 2 {
		t.Fatalf("sources = %+v", srcs)
	}
}

func TestStatus(t *testing.T) {
	e, _, _, _ := setup(t)
	s := e.Status(t.Context())
	if s.Version != "test" || !s.Notes.Configured || s.Notes.Folder != "Reading/" || s.Notes.VaultID != 1 {
		t.Fatalf("status = %+v", s)
	}
	if _, err := ParseScope("read;write"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "write", "read;write;delete", "read; write", "READ"} {
		if _, err := ParseScope(bad); err == nil {
			t.Fatalf("scope %q was accepted", bad)
		}
	}
}

// A feed that repeats its title in the summary does not put it in the
// excerpt: the list shows the title itself.
func TestExcerptDropsTheTitle(t *testing.T) {
	e, n, _, _ := setup(t)
	n.pages["https://example.com/repeat.xml"] = answer{body: `<?xml version="1.0"?><rss version="2.0"><channel>
	<title>Example Journal</title><link>https://example.com/</link>
	<item><title>The garden in November</title><link>https://example.com/november</link><guid>9</guid>
	<description>The garden in November The garden in November Pears by the wall, and the last of the quince.</description></item>
	</channel></rss>`}
	if _, err := e.AddSource(t.Context(), "https://example.com/repeat.xml"); err != nil {
		t.Fatal(err)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if strings.Contains(items[0].Excerpt, "The garden in November") {
		t.Fatalf("the excerpt repeats the title: %q", items[0].Excerpt)
	}
	if !strings.HasPrefix(items[0].Excerpt, "Pears by the wall") {
		t.Fatalf("excerpt = %q", items[0].Excerpt)
	}
}
