// Package engine is readd's logic over the store, the fetcher, the feed
// parser, the extractor and notes (docs/design/overview.md). The API, MCP
// and the poller call it; every write checks the caller's scope.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/opml"
	"github.com/frostyard/frostread/internal/sanitize"
	"github.com/frostyard/frostread/internal/store"
)

// NoteSaver writes an article into the notes vault; notes.Client is one.
type NoteSaver interface {
	Save(ctx context.Context, a notes.Article) (path string, err error)
}

// Options configure an Engine.
type Options struct {
	Store     *store.DB
	Blobs     *blobs.Store
	Fetch     fetch.Fetcher
	Extractor *extract.Extractor
	Config    config.Config
	// Interval is a source's poll interval; the configuration's when 0.
	Interval time.Duration
	Version  string
	// Notes writes save-as-note; nil when the feature is not configured.
	Notes NoteSaver
	// SetNotesToken writes a new notes token and rebuilds Notes; nil
	// keeps the token that is set.
	SetNotesToken func(token string) error
	// ContentPath is an item's content path relative to the content root,
	// such as item/3/<mac>, for lodge-client's contentURL; nil makes every
	// detail's path empty.
	ContentPath func(id int64) string
	// OnChange receives the views a change affects, for items.changed.
	OnChange func(views []string)
	// Now is the clock, for tests.
	Now func() time.Time
	// Rand is a source of [0, 1) for the polls' jitter, for tests; it is
	// math/rand/v2's when nil.
	Rand func() float64
	Log  *slog.Logger
}

// Engine is readd's logic.
type Engine struct {
	o   Options
	log *slog.Logger
	now func() time.Time

	mu    sync.Mutex
	notes NoteSaver

	queue  chan int64
	busy   sync.Map // item id -> struct{}, extraction in flight
	notify func(views []string)
}

// New returns an Engine. Its extraction queue feeds one RunExtraction.
func New(o Options) *Engine {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	e := &Engine{o: o, log: o.Log, now: o.Now, notes: o.Notes, queue: make(chan int64, 512)}
	e.notify = o.OnChange
	return e
}

// Status is the daemon's state for the web app.
type Status struct {
	Version string `json:"version"`
	Notes   Note   `json:"notes"`
}

// Note says whether save-as-note is ready.
type Note struct {
	Configured bool   `json:"configured"`
	VaultID    int64  `json:"vaultId"`
	Folder     string `json:"folder"`
}

// Status implements status.
func (e *Engine) Status(context.Context) Status {
	e.mu.Lock()
	configured := e.notes != nil
	e.mu.Unlock()
	return Status{Version: e.o.Version, Notes: Note{
		Configured: configured && e.o.Config.NotesVault != 0,
		VaultID:    e.o.Config.NotesVault,
		Folder:     e.o.Config.NotesFolder,
	}}
}

// SetNotesToken writes the notes token and uses it at once.
func (e *Engine) SetNotesToken(_ context.Context, token string) error {
	if e.o.SetNotesToken == nil {
		return fmt.Errorf("save as a note: %w", ErrUnavailable)
	}
	if err := e.o.SetNotesToken(token); err != nil {
		return err
	}
	return nil
}

// SetNotes replaces the notes client, as setting a token does.
func (e *Engine) SetNotes(n NoteSaver) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notes = n
}

// Sources lists every source, by title, with its unread count.
func (e *Engine) Sources(ctx context.Context) ([]store.Source, error) {
	return e.o.Store.ListSources(ctx)
}

// Source returns one source.
func (e *Engine) Source(ctx context.Context, id int64) (store.Source, error) {
	return e.o.Store.Source(ctx, id)
}

// AddSource subscribes to a feed, or to the feed a page names, and polls
// it once.
func (e *Engine) AddSource(ctx context.Context, rawurl string) (store.Source, error) {
	src, err := e.subscribe(ctx, rawurl, "")
	if err != nil {
		return store.Source{}, err
	}
	if err := e.pollSource(ctx, src); err != nil {
		e.log.Warn("the first poll failed", "source", src.ID, "host", host(src.URL), "err", err)
	}
	if s, err := e.o.Store.Source(ctx, src.ID); err == nil {
		src = s
	}
	e.change("unread", "all")
	return src, nil
}

// subscribe finds the feed rawurl names, or the one the page rawurl names
// points at, and records it. title is the owner's, when an import gives
// one.
func (e *Engine) subscribe(ctx context.Context, rawurl, title string) (store.Source, error) {
	if err := fetch.Check(rawurl); err != nil {
		return store.Source{}, err
	}
	res, err := e.o.Fetch.Get(ctx, rawurl, fetch.Options{
		Accept:   "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml, text/html;q=0.8, */*;q=0.5",
		MaxBytes: fetch.MaxFeed,
	})
	if err != nil {
		return store.Source{}, err
	}
	if res.Status >= 400 {
		return store.Source{}, fmt.Errorf("%s answers %d", rawurl, res.Status)
	}
	feedURL, f, err := e.parseFeed(ctx, res.Body, res.URL)
	if err != nil {
		return store.Source{}, err
	}
	if title == "" {
		title = f.Title
	}
	if title == "" {
		title = host(feedURL)
	}
	next := e.now().Add(-time.Second) // due at once
	src, err := e.o.Store.AddSource(ctx, store.Source{URL: feedURL, Title: title,
		SiteURL: f.SiteURL, ETag: res.ETag, LastModified: res.LastModified}, next)
	if err != nil {
		return store.Source{}, err
	}
	return src, nil
}

// parseFeed reads body as a feed; when it is a page, it finds the feed the
// page names and fetches that.
func (e *Engine) parseFeed(ctx context.Context, body []byte, pageURL string) (string, feed.Feed, error) {
	if f, err := feed.Parse(body, pageURL); err == nil {
		return pageURL, f, nil
	}
	links := feed.Discover(body, pageURL)
	if len(links) == 0 {
		return "", feed.Feed{}, fmt.Errorf("%s names no feed: %w", host(pageURL), feed.ErrNotAFeed)
	}
	res, err := e.o.Fetch.Get(ctx, links[0], fetch.Options{MaxBytes: fetch.MaxFeed, Accept: "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml"})
	if err != nil {
		return "", feed.Feed{}, err
	}
	if res.Status >= 400 {
		return "", feed.Feed{}, fmt.Errorf("%s answers %d", host(links[0]), res.Status)
	}
	f, err := feed.Parse(res.Body, res.URL)
	if err != nil {
		return "", feed.Feed{}, err
	}
	return res.URL, f, nil
}

// RemoveSource unsubscribes, and its items go with it.
func (e *Engine) RemoveSource(ctx context.Context, id int64) error {
	if err := e.o.Store.RemoveSource(ctx, id); err != nil {
		return err
	}
	e.change("unread", "all")
	return nil
}

// RenameSource sets the owner's title for a source.
func (e *Engine) RenameSource(ctx context.Context, id int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("a source needs a title")
	}
	if err := e.o.Store.RenameSource(ctx, id, title); err != nil {
		return err
	}
	e.change("unread")
	return nil
}

// ImportOPML adds every feed an OPML document names, and polls each once
// in the background. A feed that cannot be added is skipped.
func (e *Engine) ImportOPML(ctx context.Context, document string) (added, skipped int, err error) {
	feeds, err := opml.Import(strings.NewReader(document))
	if err != nil {
		return 0, 0, err
	}
	for _, f := range feeds {
		if _, err := e.subscribe(ctx, f.XMLURL, f.Title); err != nil {
			e.log.Warn("an opml feed was skipped", "host", host(f.XMLURL), "err", err)
			skipped++
			continue
		}
		added++
	}
	e.change("unread", "all")
	return added, skipped, nil
}

// ExportOPML returns every source as an OPML document.
func (e *Engine) ExportOPML(ctx context.Context) (string, error) {
	sources, err := e.o.Store.ListSources(ctx)
	if err != nil {
		return "", err
	}
	feeds := make([]opml.Feed, 0, len(sources))
	for _, s := range sources {
		feeds = append(feeds, opml.Feed{Title: s.Title, XMLURL: s.URL, HTMLURL: s.SiteURL})
	}
	b, err := opml.Export(feeds)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Summary is one item in a list.
type Summary struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	SourceTitle string    `json:"sourceTitle,omitzero"`
	Author      string    `json:"author,omitzero"`
	Published   time.Time `json:"published"`
	Excerpt     string    `json:"excerpt,omitzero"`
	Read        bool      `json:"read"`
	Starred     bool      `json:"starred"`
	Archived    bool      `json:"archived"`
	Saved       bool      `json:"saved"`
	NotePath    string    `json:"notePath,omitzero"`
	Snippet     string    `json:"snippet,omitzero"`
}

// Detail is one item in the reader.
type Detail struct {
	Summary
	URL          string `json:"url"`
	ContentPath  string `json:"contentPath,omitzero"`
	ExtractState string `json:"extractState"`
	ExtractError string `json:"extractError,omitzero"`
	OriginalHash string `json:"originalHash,omitzero"`
}

// ListItems returns a page of summaries, and the next page's cursor.
func (e *Engine) ListItems(ctx context.Context, o store.ListOptions) ([]Summary, string, error) {
	items, next, err := e.o.Store.ListItems(ctx, o)
	if err != nil {
		return nil, "", err
	}
	sums, _, err := e.summaries(ctx, items)
	return sums, next, err
}

// Unread returns unread summaries: one source's or all, published since a
// time when it is not zero, newest first.
func (e *Engine) Unread(ctx context.Context, sourceID *int64, since time.Time, limit int) ([]Summary, error) {
	limit, err := clampLimit(limit)
	if err != nil {
		return nil, err
	}
	items, err := e.o.Store.Unread(ctx, sourceID, since, limit)
	if err != nil {
		return nil, err
	}
	sums, _, err := e.summaries(ctx, items)
	return sums, err
}

// Item returns one item's detail.
func (e *Engine) Item(ctx context.Context, id int64) (Detail, error) {
	it, err := e.o.Store.Item(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	sums, _, err := e.summaries(ctx, []store.Item{it})
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Summary: sums[0], URL: it.URL, ExtractState: it.ExtractState,
		ExtractError: it.ExtractError, OriginalHash: it.OriginalHash}
	if e.o.ContentPath != nil {
		d.ContentPath = e.o.ContentPath(it.ID)
	}
	return d, nil
}

// Body returns an item's extracted text, or its feed's text when the
// extraction has not run: what an agent reads.
func (e *Engine) Body(ctx context.Context, id int64) (string, error) {
	it, err := e.o.Store.Item(ctx, id)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(it.Text)
	if text == "" {
		text = sanitize.PlainText(it.SummaryHTML + " " + it.ContentHTML)
	}
	// An item stored before the extractor stripped the heading keeps it;
	// the title is above the text here too.
	return extract.StripTitle(text, it.Title), nil
}

// Search returns the items that match query, best match first.
func (e *Engine) Search(ctx context.Context, query string, limit int) ([]Summary, error) {
	limit, err := clampLimit(limit)
	if err != nil {
		return nil, err
	}
	items, err := e.o.Store.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	sums, _, err := e.summaries(ctx, items)
	return sums, err
}

// summaries turns items into summaries, naming each item's source.
func (e *Engine) summaries(ctx context.Context, items []store.Item) ([]Summary, string, error) {
	sources, err := e.o.Store.ListSources(ctx)
	if err != nil {
		return nil, "", err
	}
	names := make(map[int64]string, len(sources))
	for _, s := range sources {
		names[s.ID] = s.Title
	}
	out := make([]Summary, 0, len(items))
	for _, it := range items {
		out = append(out, Summary{
			ID: it.ID, Title: it.Title, SourceTitle: sourceName(names, it.SourceID),
			Author: it.Author, Published: it.PublishedAt.UTC(), Excerpt: excerpt(it),
			Read: it.ReadAt != nil, Starred: it.Starred, Archived: it.Archived, Saved: it.Saved,
			NotePath: it.NotePath, Snippet: it.Snippet,
		})
	}
	return out, "", nil
}

func sourceName(names map[int64]string, id *int64) string {
	if id == nil {
		return ""
	}
	return names[*id]
}

// excerpt is an item's summary as text, at most 300 characters, and
// without a leading copy of the title: the list shows the title itself.
func excerpt(it store.Item) string {
	text := it.Text
	if strings.TrimSpace(text) == "" {
		text = sanitize.PlainText(it.SummaryHTML)
	}
	text = extract.StripTitle(text, it.Title)
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 300 {
		cut := string(r[:300])
		if i := strings.LastIndexByte(cut, ' '); i > 200 {
			cut = cut[:i]
		}
		return cut + "…"
	}
	return text
}

// SetRead marks items read or unread.
func (e *Engine) SetRead(ctx context.Context, sc Scope, ids []int64, read bool) error {
	if !sc.Write {
		return ErrReadOnly
	}
	if len(ids) == 0 {
		return errors.New("no item ids")
	}
	if err := e.o.Store.SetRead(ctx, ids, read); err != nil {
		return err
	}
	e.change("unread", "all")
	return nil
}

// MarkAllRead marks read every unread item in a view or a source.
func (e *Engine) MarkAllRead(ctx context.Context, sc Scope, view string, sourceID *int64, before time.Time) error {
	if !sc.Write {
		return ErrReadOnly
	}
	if err := e.o.Store.MarkAllRead(ctx, view, sourceID, before); err != nil {
		return err
	}
	e.change("unread", "all")
	return nil
}

// SetStarred changes an item's star.
func (e *Engine) SetStarred(ctx context.Context, sc Scope, id int64, starred bool) error {
	if !sc.Write {
		return ErrReadOnly
	}
	if err := e.o.Store.SetStarred(ctx, id, starred); err != nil {
		return err
	}
	e.change("starred", "all")
	return nil
}

// SetArchived changes an item's archive state.
func (e *Engine) SetArchived(ctx context.Context, sc Scope, id int64, archived bool) error {
	if !sc.Write {
		return ErrReadOnly
	}
	if err := e.o.Store.SetArchived(ctx, id, archived); err != nil {
		return err
	}
	e.change("archive", "unread", "starred", "later", "all")
	return nil
}

// SaveURL saves a link to read later. Saving it twice returns the same
// item.
func (e *Engine) SaveURL(ctx context.Context, sc Scope, rawurl string) (store.Item, error) {
	if !sc.Write {
		return store.Item{}, ErrReadOnly
	}
	if err := fetch.Check(rawurl); err != nil {
		return store.Item{}, err
	}
	u, _ := url.Parse(rawurl)
	it, err := e.o.Store.SaveLink(ctx, rawurl, u.Hostname())
	if err != nil {
		return store.Item{}, err
	}
	e.Enqueue(it.ID)
	e.change("later", "all")
	return it, nil
}

// SaveAsNote writes an item into notes, and records where.
func (e *Engine) SaveAsNote(ctx context.Context, id int64) (path, route string, err error) {
	e.mu.Lock()
	s := e.notes
	e.mu.Unlock()
	if s == nil || e.o.Config.NotesVault == 0 {
		return "", "", fmt.Errorf("save as a note: %w", ErrUnavailable)
	}
	it, err := e.o.Store.Item(ctx, id)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(it.ContentHTML) == "" || sanitize.PlainText(it.ContentHTML) == "" {
		if it.ExtractState == store.ExtractFailed {
			return "", "", fmt.Errorf("the article could not be extracted: %s", it.ExtractError)
		}
		return "", "", errors.New("the article is still being extracted")
	}
	site := ""
	if it.SourceID != nil {
		if src, err := e.o.Store.Source(ctx, *it.SourceID); err == nil {
			site = src.Title
		}
	}
	path, err = s.Save(ctx, notes.Article{ID: it.ID, URL: it.URL, Title: it.Title,
		Author: it.Author, Site: site, Content: it.ContentHTML})
	if err != nil {
		return "", "", err
	}
	if err := e.o.Store.SetNotePath(ctx, id, path); err != nil {
		return "", "", err
	}
	e.change("all")
	return path, notes.Route(e.o.Config.NotesVault, path), nil
}

// Counts is the sidebar's counts.
type Counts struct {
	Views   map[string]int      `json:"views"`
	Sources []store.SourceCount `json:"sources"`
}

// Counts returns the views' and the sources' counts.
func (e *Engine) Counts(ctx context.Context) (Counts, error) {
	views, sources, err := e.o.Store.Counts(ctx)
	if err != nil {
		return Counts{}, err
	}
	if sources == nil {
		sources = []store.SourceCount{}
	}
	return Counts{Views: views, Sources: sources}, nil
}

// ClampLimit bounds a search or list limit: 50 when 0, at most 200.
func ClampLimit(limit int) (int, error) { return clampLimit(limit) }

func clampLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return 50, nil
	case limit < 1 || limit > 200:
		return 0, fmt.Errorf("limit must be 1 to 200")
	}
	return limit, nil
}

// change tells the API which views a change affects.
func (e *Engine) change(views ...string) {
	if e.notify != nil {
		e.notify(views)
	}
}

// host is a URL's host, for a log: never the whole URL.
func host(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
