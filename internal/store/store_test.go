package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// open returns a store in a temp dir, and its clock's time.
func open(t *testing.T) (*DB, time.Time) {
	t.Helper()
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return now }
	return d, now
}

func TestSchemaAndSecret(t *testing.T) {
	d, _ := open(t)
	v, err := d.SchemaVersion(t.Context())
	if err != nil || v != 3 {
		t.Fatalf("schema version = %d, %v; want 3", v, err)
	}
	a, err := d.Secret(t.Context())
	if err != nil || len(a) != 32 {
		t.Fatalf("secret = %d bytes, %v; want 32", len(a), err)
	}
	b, err := d.Secret(t.Context())
	if err != nil || string(a) != string(b) {
		t.Fatalf("the second secret differs: %v", err)
	}
}

func TestSourcesAndItems(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	src, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddSource(ctx, Source{URL: src.URL}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("adding the same source = %v, want a conflict", err)
	}
	older := now.Add(-2 * time.Hour)
	added, err := d.AddItems(ctx, src.ID, []NewItem{
		{GUID: "a", URL: "https://example.com/a", Title: "First post", Published: now.Add(-24 * time.Hour), Text: "the first article"},
		{GUID: "b", URL: "https://example.com/b", Title: "Second post", Published: older, Text: "the second article"},
	})
	if err != nil || len(added) != 2 {
		t.Fatalf("add items = %v, %v; want 2", added, err)
	}
	// A re-poll adds nothing: the GUIDs are stored.
	again, err := d.AddItems(ctx, src.ID, []NewItem{{GUID: "a", URL: "https://example.com/a"}, {GUID: "c", Title: "Third"}})
	if err != nil || len(again) != 1 {
		t.Fatalf("re-poll = %v, %v; want 1 new", again, err)
	}
	list, next, err := d.ListItems(ctx, ListOptions{View: ViewUnread, Limit: 1})
	if err != nil || len(list) != 1 || next == "" {
		t.Fatalf("first page = %d items, next %q, %v", len(list), next, err)
	}
	page2, next2, err := d.ListItems(ctx, ListOptions{View: ViewUnread, Limit: 1, Cursor: next})
	if err != nil || len(page2) != 1 || page2[0].ID == list[0].ID || next2 == "" {
		t.Fatalf("second page = %+v, next %q, %v", page2, next2, err)
	}
	if _, _, err := d.ListItems(ctx, ListOptions{View: ViewUnread, Cursor: "not-a-cursor", Limit: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a bad cursor = %v, want ErrNotFound", err)
	}
	if _, _, err := d.ListItems(ctx, ListOptions{View: "nope", Limit: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a bad view = %v, want ErrNotFound", err)
	}
}

func TestSavedLinksAreIdempotent(t *testing.T) {
	d, _ := open(t)
	ctx := t.Context()
	first, err := d.SaveLink(ctx, "https://example.org/read-later", "example.org")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.SaveLink(ctx, "https://example.org/read-later", "example.org")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("saving twice = %d and %d", first.ID, second.ID)
	}
	if again, err := d.SaveLink(ctx, first.URL, "example.org"); err != nil || again.ID != first.ID {
		t.Fatalf("third save = %v, %v", again, err)
	}
	views, _, err := d.Counts(ctx)
	if err != nil || views[ViewLater] != 1 {
		t.Fatalf("later = %d, %v; want 2", views[ViewLater], err)
	}
}

func TestMarksAndCounts(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	src, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := d.AddItems(ctx, src.ID, []NewItem{
		{GUID: "a", Title: "One", Published: now.Add(-time.Hour)},
		{GUID: "b", Title: "Two", Published: now.Add(-2 * time.Hour)},
		{GUID: "c", Title: "Three", Published: now.Add(-3 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetRead(ctx, ids[:2], true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetStarred(ctx, ids[2], true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetArchived(ctx, ids[0], true); err != nil {
		t.Fatal(err)
	}
	views, sources, err := d.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if views[ViewUnread] != 1 || views[ViewStarred] != 1 || views[ViewArchive] != 1 || views[ViewAll] != 3 {
		t.Fatalf("counts = %+v", views)
	}
	if len(sources) != 1 || sources[0].Unread != 1 {
		t.Fatalf("source counts = %+v", sources)
	}
	if err := d.MarkAllRead(ctx, ViewAll, nil, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if views, _, _ = d.Counts(ctx); views[ViewUnread] != 0 {
		t.Fatalf("unread after mark all = %d", views[ViewUnread])
	}
	if err := d.SetStarred(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("starring no item = %v, want ErrNotFound", err)
	}
}

// UnreadCount counts the Unread view alone: a read item and an archived
// item do not count.
func TestUnreadCount(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	if n, err := d.UnreadCount(ctx); err != nil || n != 0 {
		t.Fatalf("unread on an empty store = %d, %v; want 0", n, err)
	}
	src, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := d.AddItems(ctx, src.ID, []NewItem{
		{GUID: "a", Title: "One", Published: now.Add(-time.Hour)},
		{GUID: "b", Title: "Two", Published: now.Add(-2 * time.Hour)},
		{GUID: "c", Title: "Three", Published: now.Add(-3 * time.Hour)},
	})
	if err != nil || len(ids) != 3 {
		t.Fatalf("add items = %v, %v; want 3", ids, err)
	}
	if n, err := d.UnreadCount(ctx); err != nil || n != 3 {
		t.Fatalf("unread = %d, %v; want 3", n, err)
	}
	if err := d.SetRead(ctx, ids[:1], true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetArchived(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}
	if n, err := d.UnreadCount(ctx); err != nil || n != 1 {
		t.Fatalf("unread after a read and an archived item = %d, %v; want 1", n, err)
	}
}

func TestSearchRanksAndSnippets(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	src, _ := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	_, err := d.AddItems(ctx, src.ID, []NewItem{
		{GUID: "a", Title: "Gardening in spring", Author: "Ann", Text: "plant tomatoes after the last frost"},
		{GUID: "b", Title: "Tomatoes", Text: "plant tomatoes in a sunny bed"},
		{GUID: "c", Title: "Bicycles", Text: "the chain and the cog"},
	})
	if err != nil {
		t.Fatal(err)
	}
	found, err := d.Search(ctx, "tomatoes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Title != "Tomatoes" {
		t.Fatalf("search = %+v", found)
	}
	if found[0].Snippet == "" {
		t.Fatal("the best match has no snippet")
	}
	if found, err := d.Search(ctx, `"the last frost"`, 10); err != nil || len(found) != 1 {
		t.Fatalf("phrase search = %d, %v; want 2", len(found), err)
	}
	if found, err := d.Search(ctx, "plant", 10); err != nil || len(found) != 2 {
		t.Fatalf("prefix search = %d, %v; want 2", len(found), err)
	}
	if found, err := d.Search(ctx, " ", 10); err != nil || len(found) != 0 {
		t.Fatalf("empty search = %d, %v", len(found), err)
	}
}

func TestRemoveSourceCascades(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	src, _ := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if _, err := d.AddItems(ctx, src.ID, []NewItem{{GUID: "a", Title: "Gardens"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveLink(ctx, "https://example.org/kept", "example.org"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if views, _, _ := d.Counts(ctx); views[ViewAll] != 1 {
		t.Fatalf("items after removal = %d, want the saved link alone", views[ViewAll])
	}
	if found, err := d.Search(ctx, "gardens", 10); err != nil || len(found) != 0 {
		t.Fatalf("the removed item's search row survived: %d, %v", len(found), err)
	}
}

func TestExtractionAndBlobs(t *testing.T) {
	d, _ := open(t)
	ctx := t.Context()
	it, err := d.SaveLink(ctx, "https://example.org/an-article", "example.org")
	if err != nil {
		t.Fatal(err)
	}
	err = d.SetExtraction(ctx, it.ID, Extraction{
		Title: "An article", Author: "Ann", Text: "the article's text",
		ContentHTML: `<p>the article</p><img src="blob:abc">`, State: ExtractDone,
		Images: []Blob{{Hash: "abc", Mime: "image/png", Size: 12}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Item(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "An article" || got.ExtractState != ExtractDone || got.Text == "" {
		t.Fatalf("item after extraction = %+v", got)
	}
	blobs, err := d.ItemBlobs(ctx, it.ID)
	if err != nil || blobs["abc"].Mime != "image/png" || blobs["abc"].Size != 12 {
		t.Fatalf("blobs = %+v, %v", blobs, err)
	}
	if ids, err := d.PendingExtraction(ctx, 10); err != nil || len(ids) != 0 {
		t.Fatalf("pending = %v, %v; want none", ids, err)
	}
}

// A saved page's title stays when the extraction finds no title of its own:
// the item is called what the owner saved it as (items.SetExtraction).
func TestExtractionKeepsASavedTitle(t *testing.T) {
	d, _ := open(t)
	ctx := t.Context()
	it, err := d.SaveLink(ctx, "https://example.org/a-titled-page", "The page as I saved it")
	if err != nil {
		t.Fatal(err)
	}
	err = d.SetExtraction(ctx, it.ID, Extraction{
		Title: "", ContentHTML: `<p>the article</p>`, Text: "the article", State: ExtractDone,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Item(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The page as I saved it" {
		t.Fatalf("title after an extraction with none = %q, want the saved title", got.Title)
	}
}

func TestPolling(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	src, _ := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml"}, now.Add(-time.Second))
	due, err := d.DueSources(ctx, now, 4)
	if err != nil || len(due) != 1 || due[0].ID != src.ID {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if err := d.Polled(ctx, src.ID, `"etag"`, "then", now.Add(time.Hour), 2, "boom"); err != nil {
		t.Fatal(err)
	}
	got, err := d.Source(ctx, src.ID)
	if err != nil || got.ETag != `"etag"` || got.ErrorCount != 2 || got.LastError != "boom" {
		t.Fatalf("source after poll = %+v, %v", got, err)
	}
	if due, err := d.DueSources(ctx, now, 4); err != nil || len(due) != 0 {
		t.Fatalf("due after poll = %+v, %v; want none", due, err)
	}
}

// A folder is one level, on the source: it is set, cleared, and it filters
// both the list and mark all read.
func TestSourceFolders(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	garden, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example Garden", Folder: "Garden"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.Source(ctx, garden.ID); err != nil || got.Folder != "Garden" {
		t.Fatalf("the new source's folder = %q, %v; want Garden", got.Folder, err)
	}
	news, err := d.AddSource(ctx, Source{URL: "https://example.org/feed.xml", Title: "Example News", Folder: "News"}, now)
	if err != nil {
		t.Fatal(err)
	}
	loose, err := d.AddSource(ctx, Source{URL: "https://example.net/feed.xml", Title: "Example Notes"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Source(ctx, loose.ID); got.Folder != "" {
		t.Fatalf("a source with no folder = %q, want none", got.Folder)
	}
	if _, err := d.AddItems(ctx, garden.ID, []NewItem{
		{GUID: "g1", Title: "Tomatoes", Published: now.Add(-time.Hour)},
		{GUID: "g2", Title: "Beans", Published: now.Add(-2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddItems(ctx, news.ID, []NewItem{
		{GUID: "n1", Title: "Bicycles", Published: now.Add(-3 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddItems(ctx, loose.ID, []NewItem{
		{GUID: "l1", Title: "Kettles", Published: now.Add(-4 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveLink(ctx, "https://example.org/later", "example.org"); err != nil {
		t.Fatal(err)
	}

	page, _, err := d.ListItems(ctx, ListOptions{View: ViewUnread, Folder: "Garden", Limit: 50})
	if err != nil || len(page) != 2 {
		t.Fatalf("the garden's items = %d, %v; want 2", len(page), err)
	}
	if page, _, err := d.ListItems(ctx, ListOptions{View: ViewUnread, Folder: "Nowhere", Limit: 50}); err != nil || len(page) != 0 {
		t.Fatalf("a folder with no source = %d items, %v; want none", len(page), err)
	}
	// A saved link has no source, so no folder holds it.
	if page, _, err := d.ListItems(ctx, ListOptions{View: ViewLater, Folder: "Garden", Limit: 50}); err != nil || len(page) != 0 {
		t.Fatalf("a saved link in a folder = %d items, %v; want none", len(page), err)
	}

	// Marking a folder read leaves the other folder's items, the source
	// outside every folder, and the saved links unread.
	if err := d.MarkAllRead(ctx, ViewAll, nil, "Garden", time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Three unread items are left: the news feed's, the loose source's and
	// the saved link, which no folder holds.
	if views, _, err := d.Counts(ctx); err != nil || views[ViewUnread] != 3 || views[ViewLater] != 1 {
		t.Fatalf("counts after the folder's mark all = %+v, %v; want the other items and the saved link", views, err)
	}
	for _, c := range []struct {
		name string
		o    ListOptions
	}{
		{"the other folder", ListOptions{View: ViewAll, Folder: "News", Limit: 50}},
		{"the source outside every folder", ListOptions{View: ViewAll, SourceID: &loose.ID, Limit: 50}},
	} {
		page, _, err := d.ListItems(ctx, c.o)
		if err != nil || len(page) != 1 || page[0].ReadAt != nil {
			t.Fatalf("%s was marked read: %+v, %v", c.name, page, err)
		}
	}
	// The garden's own items are read.
	if page, _, err := d.ListItems(ctx, ListOptions{View: ViewAll, Folder: "Garden", Limit: 50}); err != nil || len(page) != 2 {
		t.Fatalf("the garden's items = %d, %v; want 2", len(page), err)
	} else if page[0].ReadAt == nil || page[1].ReadAt == nil {
		t.Fatalf("the garden's items are unread: %+v", page)
	}

	// The folder changes, and it clears.
	if err := d.SetSourceFolder(ctx, garden.ID, "Growing"); err != nil {
		t.Fatal(err)
	}
	if page, _, err := d.ListItems(ctx, ListOptions{View: ViewAll, Folder: "Growing", Limit: 50}); err != nil || len(page) != 2 {
		t.Fatalf("the new folder's items = %d, %v; want 2", len(page), err)
	}
	if err := d.SetSourceFolder(ctx, garden.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Source(ctx, garden.ID); got.Folder != "" {
		t.Fatalf("a cleared folder = %q, want none", got.Folder)
	}
	if err := d.SetSourceFolder(ctx, 9999, "Garden"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a folder on no source = %v, want ErrNotFound", err)
	}
}

// A store a version 2 daemon left gains the folder column, and keeps its
// sources and items: the migration an owner's database goes through.
func TestUpgradeFromVersion2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.db")
	d, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return now }
	src, err := d.AddSource(t.Context(), Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddItems(t.Context(), src.ID, []NewItem{{GUID: "a", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Take the store back to version 2, as the folder column's migration
	// had not run.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE sources DROP COLUMN folder`, `PRAGMA user_version = 2`} {
		if _, err := raw.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	up, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = up.Close() })
	if v, err := up.SchemaVersion(t.Context()); err != nil || v != 3 {
		t.Fatalf("schema version after the upgrade = %d, %v; want 3", v, err)
	}
	got, err := up.Source(t.Context(), src.ID)
	if err != nil || got.Title != "Example" || got.Folder != "" {
		t.Fatalf("the source after the upgrade = %+v, %v; want Example in no folder", got, err)
	}
	if views, _, err := up.Counts(t.Context()); err != nil || views[ViewAll] != 1 {
		t.Fatalf("items after the upgrade = %+v, %v; want 1", views, err)
	}
	if err := up.SetSourceFolder(t.Context(), src.ID, "Garden"); err != nil {
		t.Fatal(err)
	}
}
