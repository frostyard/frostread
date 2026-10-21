package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The views an item list takes (docs/specs/api.md, Views).
const (
	ViewUnread  = "unread"
	ViewStarred = "starred"
	ViewLater   = "later"
	ViewArchive = "archive"
	ViewAll     = "all"
)

// ValidView reports whether view is one the contract defines.
func ValidView(view string) bool {
	switch view {
	case ViewUnread, ViewStarred, ViewLater, ViewArchive, ViewAll:
		return true
	}
	return false
}

// ExtractState is an item's extraction state.
const (
	ExtractPending = "pending"
	ExtractDone    = "done"
	ExtractFailed  = "failed"
)

// Item is one article, or one saved link.
type Item struct {
	ID           int64
	SourceID     *int64
	GUID         string
	URL          string
	Title        string
	Author       string
	PublishedAt  time.Time
	FetchedAt    time.Time
	SummaryHTML  string
	ContentHTML  string
	Text         string
	OriginalHash string
	ExtractState string
	ExtractError string
	ReadAt       *time.Time
	Starred      bool
	Archived     bool
	Saved        bool
	NotePath     string
	CreatedAt    time.Time
	// Snippet is a search result's matching words, between **.
	Snippet string
}

// NewItem is an item a feed gave, before it is stored.
type NewItem struct {
	GUID         string
	URL          string
	Title        string
	Author       string
	Published    time.Time
	SummaryHTML  string
	ContentHTML  string
	Text         string
	OriginalHash string
}

const itemColumns = `id, source_id, guid, url, title, author, published_at, fetched_at,
	summary_html, content_html, text, original_hash, extract_state, extract_error,
	read_at, starred, archived, saved, note_path, created_at`

// scanItem reads the itemColumns, and then any extra columns.
func scanItem(row interface{ Scan(...any) error }, it *Item, extra ...any) error {
	var source sql.NullInt64
	var read, notePath sql.NullString
	var published, fetched, created sql.NullString
	var starred, archived, saved int
	dest := []any{&it.ID, &source, &it.GUID, &it.URL, &it.Title, &it.Author, &published, &fetched,
		&it.SummaryHTML, &it.ContentHTML, &it.Text, &it.OriginalHash, &it.ExtractState, &it.ExtractError,
		&read, &starred, &archived, &saved, &notePath, &created}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return err
	}
	if source.Valid {
		id := source.Int64
		it.SourceID = &id
	}
	if t := parseTime(read); !t.IsZero() {
		it.ReadAt = &t
	}
	if notePath.Valid && notePath.String != "" {
		it.NotePath = notePath.String
	}
	it.PublishedAt = parseTime(published)
	it.FetchedAt = parseTime(fetched)
	it.CreatedAt = parseTime(created)
	it.Starred, it.Archived, it.Saved = starred != 0, archived != 0, saved != 0
	return nil
}

// Item returns one item by id.
func (d *DB) Item(ctx context.Context, id int64) (Item, error) {
	var it Item
	err := scanItem(d.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE id = ?`, id), &it)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("get item %d: %w", id, err)
	}
	return it, nil
}

// AddItems inserts the items a source has that it did not have, and
// returns the new items' ids. An item is the same as one already stored
// when its guid is, so a re-poll changes nothing.
func (d *DB) AddItems(ctx context.Context, sourceID int64, items []NewItem) ([]int64, error) {
	var added []int64
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		now := timeString(d.Now())
		for _, it := range items {
			published := it.Published
			if published.IsZero() {
				published = d.Now()
			}
			var id int64
			err := tx.QueryRowContext(ctx, `INSERT INTO items
				(source_id, guid, url, title, author, published_at, fetched_at, summary_html,
				 content_html, text, original_hash, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING RETURNING id`,
				sourceID, it.GUID, it.URL, it.Title, it.Author, timeString(published), now,
				it.SummaryHTML, it.ContentHTML, it.Text, it.OriginalHash, now).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				continue // already stored
			}
			if err != nil {
				return fmt.Errorf("insert item: %w", err)
			}
			added = append(added, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// SaveLink returns the saved link for url, making it when it is new.
func (d *DB) SaveLink(ctx context.Context, url, title string) (Item, error) {
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		now := d.Now()
		_, err := tx.ExecContext(ctx, `INSERT INTO items
			(source_id, guid, url, title, published_at, fetched_at, saved, extract_state, created_at)
			VALUES (NULL, '', ?, ?, ?, ?, 1, 'pending', ?)
			ON CONFLICT DO NOTHING`,
			url, title, timeString(now), timeString(now), timeString(now))
		return err
	})
	if err != nil {
		return Item{}, fmt.Errorf("save link: %w", err)
	}
	var it Item
	err = scanItem(d.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE saved = 1 AND url = ?`, url), &it)
	if err != nil {
		return Item{}, fmt.Errorf("save link: %w", err)
	}
	return it, nil
}

// ListOptions selects a page of items.
type ListOptions struct {
	// View is one of the contract's views.
	View string
	// SourceID limits the page to one source when set.
	SourceID *int64
	// Cursor is the previous page's next cursor; "" starts at the newest.
	Cursor string
	// Limit is the page's size.
	Limit int
}

// ListItems returns a page of items, newest first, and the next page's
// cursor, which is empty on the last page.
func (d *DB) ListItems(ctx context.Context, o ListOptions) ([]Item, string, error) {
	if !ValidView(o.View) {
		return nil, "", fmt.Errorf("view %q is not a view: %w", o.View, ErrNotFound)
	}
	where, args := viewClause(o.View)
	if o.SourceID != nil {
		where += " AND source_id = ?"
		args = append(args, *o.SourceID)
	}
	if o.Cursor != "" {
		published, id, err := parseCursor(o.Cursor)
		if err != nil {
			return nil, "", err
		}
		where += " AND (published_at < ? OR (published_at = ? AND id < ?))"
		args = append(args, published, published, id)
	}
	args = append(args, o.Limit+1)
	rows, err := d.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items WHERE `+where+
		` ORDER BY published_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, "", fmt.Errorf("list items: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list items: %w", err)
	}
	next := ""
	if len(out) > o.Limit {
		out = out[:o.Limit]
		last := out[len(out)-1]
		next = makeCursor(last.PublishedAt, last.ID)
	}
	return out, next, nil
}

// Unread returns the unread items of one source or all, published since a
// time when it is not zero, newest first.
func (d *DB) Unread(ctx context.Context, sourceID *int64, since time.Time, limit int) ([]Item, error) {
	where := "read_at IS NULL AND archived = 0"
	var args []any
	if sourceID != nil {
		where += " AND source_id = ?"
		args = append(args, *sourceID)
	}
	if !since.IsZero() {
		where += " AND published_at >= ?"
		args = append(args, timeString(since))
	}
	args = append(args, limit)
	rows, err := d.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items WHERE `+where+
		` ORDER BY published_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list unread: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, fmt.Errorf("list unread: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// viewClause is a view's WHERE clause, and its arguments.
func viewClause(view string) (string, []any) {
	switch view {
	case ViewUnread:
		return "read_at IS NULL AND archived = 0", nil
	case ViewStarred:
		return "starred = 1 AND archived = 0", nil
	case ViewLater:
		return "saved = 1 AND archived = 0", nil
	case ViewArchive:
		return "archived = 1", nil
	default:
		return "1 = 1", nil
	}
}

// makeCursor names an item for the next page: opaque, and a URL segment.
func makeCursor(published time.Time, id int64) string {
	raw := published.UTC().Format(TimeFormat) + "|" + fmt.Sprint(id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// parseCursor reads a cursor makeCursor wrote. A cursor this daemon did
// not issue is an error wrapping ErrNotFound.
func parseCursor(c string) (string, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", 0, fmt.Errorf("not a cursor: %w", ErrNotFound)
	}
	published, idText, ok := strings.Cut(string(raw), "|")
	if !ok || published == "" {
		return "", 0, fmt.Errorf("not a cursor: %w", ErrNotFound)
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("not a cursor: %w", ErrNotFound)
	}
	return published, id, nil
}

// SetRead marks items read, or unread.
func (d *DB) SetRead(ctx context.Context, ids []int64, read bool) error {
	var at any
	if read {
		at = timeString(d.Now())
	}
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET read_at = ? WHERE id = ?`, at, id); err != nil {
				return fmt.Errorf("mark item %d read: %w", id, err)
			}
		}
		return nil
	})
	return err
}

// MarkAllRead marks read every unread item in a view, or in a source when
// one is given, published before before.
func (d *DB) MarkAllRead(ctx context.Context, view string, sourceID *int64, before time.Time) error {
	if view == "" {
		view = ViewAll
	}
	if !ValidView(view) {
		return fmt.Errorf("view %q is not a view: %w", view, ErrNotFound)
	}
	where, args := viewClause(view)
	where = "(" + where + ") AND read_at IS NULL"
	if sourceID != nil {
		where += " AND source_id = ?"
		args = append(args, *sourceID)
	}
	if !before.IsZero() {
		where += " AND published_at < ?"
		args = append(args, timeString(before))
	}
	args = append([]any{timeString(d.Now())}, args...)
	_, err := d.db.ExecContext(ctx, `UPDATE items SET read_at = ? WHERE `+where, args...)
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

// SetStarred changes an item's star.
func (d *DB) SetStarred(ctx context.Context, id int64, starred bool) error {
	return d.execOne(ctx, `UPDATE items SET starred = ? WHERE id = ?`, "star an item", starred, id)
}

// SetArchived changes an item's archive state.
func (d *DB) SetArchived(ctx context.Context, id int64, archived bool) error {
	return d.execOne(ctx, `UPDATE items SET archived = ? WHERE id = ?`, "archive an item", archived, id)
}

// SetNotePath records the note save-as-a-note wrote.
func (d *DB) SetNotePath(ctx context.Context, id int64, path string) error {
	return d.execOne(ctx, `UPDATE items SET note_path = ? WHERE id = ?`, "record a note", path, id)
}

// Blob is one stored image: its hash, its MIME type and its size.
type Blob struct {
	Hash string
	Mime string
	Size int64
}

// Extraction is what an extraction made of an item.
type Extraction struct {
	// Title, Author and Text replace the item's when not empty.
	Title, Author, Text string
	// Published replaces the item's published time when it is not zero.
	Published time.Time
	// ContentHTML is the sanitized article, with blob: markers.
	ContentHTML string
	// State is ExtractDone or ExtractFailed; Error says why it failed.
	State string
	Error string
	// Images are the blobs the article inlines.
	Images []Blob
}

// SetExtraction records an extraction's result, its images and its state,
// in one transaction.
func (d *DB) SetExtraction(ctx context.Context, id int64, e Extraction) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		now := timeString(d.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE items SET
			title = CASE WHEN ? = '' THEN title ELSE ? END,
			author = CASE WHEN ? = '' THEN author ELSE ? END,
			content_html = ?, text = CASE WHEN ? = '' THEN text ELSE ? END,
			published_at = CASE WHEN ? IS NULL THEN published_at ELSE ? END,
			extract_state = ?, extract_error = ? WHERE id = ?`,
			e.Title, e.Title, e.Author, e.Author, e.ContentHTML, e.Text, e.Text,
			timeString(e.Published), timeString(e.Published), e.State, e.Error, id); err != nil {
			return fmt.Errorf("store extraction: %w", err)
		}
		for _, b := range e.Images {
			if _, err := tx.ExecContext(ctx, `INSERT INTO blobs (hash, mime, size, created_at)
				VALUES (?, ?, ?, ?) ON CONFLICT (hash) DO NOTHING`, b.Hash, b.Mime, b.Size, now); err != nil {
				return fmt.Errorf("store blob %s: %w", b.Hash, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_blobs (item_id, hash)
				VALUES (?, ?) ON CONFLICT DO NOTHING`, id, b.Hash); err != nil {
				return fmt.Errorf("link blob %s: %w", b.Hash, err)
			}
		}
		return nil
	})
}

// SetOriginal records the hash of an item's body as sent.
func (d *DB) SetOriginal(ctx context.Context, id int64, hash string) error {
	return d.execOne(ctx, `UPDATE items SET original_hash = ? WHERE id = ?`, "record an original", hash, id)
}

// ItemBlobs returns the blobs an item's extraction stored, by hash.
func (d *DB) ItemBlobs(ctx context.Context, id int64) (map[string]Blob, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT blobs.hash, blobs.mime, blobs.size
		FROM item_blobs JOIN blobs ON blobs.hash = item_blobs.hash WHERE item_blobs.item_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("list item blobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Blob{}
	for rows.Next() {
		var b Blob
		if err := rows.Scan(&b.Hash, &b.Mime, &b.Size); err != nil {
			return nil, fmt.Errorf("list item blobs: %w", err)
		}
		out[b.Hash] = b
	}
	return out, rows.Err()
}

// PendingExtraction returns the ids of items whose extraction has not run,
// oldest first.
func (d *DB) PendingExtraction(ctx context.Context, limit int) ([]int64, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id FROM items
		WHERE extract_state = 'pending' ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending extraction: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list pending extraction: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SourceCount is one source's unread count.
type SourceCount struct {
	SourceID int64
	Unread   int
}

// Counts returns every view's item count and each source's unread count.
func (d *DB) Counts(ctx context.Context) (map[string]int, []SourceCount, error) {
	views := map[string]int{}
	for _, v := range []string{ViewUnread, ViewStarred, ViewLater, ViewArchive, ViewAll} {
		where, args := viewClause(v)
		var n int
		if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE `+where, args...).Scan(&n); err != nil {
			return nil, nil, fmt.Errorf("count %s: %w", v, err)
		}
		views[v] = n
	}
	rows, err := d.db.QueryContext(ctx, `SELECT source_id, count(*) FROM items
		WHERE source_id IS NOT NULL AND read_at IS NULL AND archived = 0 GROUP BY source_id`)
	if err != nil {
		return nil, nil, fmt.Errorf("count sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SourceCount{}
	for rows.Next() {
		var c SourceCount
		if err := rows.Scan(&c.SourceID, &c.Unread); err != nil {
			return nil, nil, fmt.Errorf("count sources: %w", err)
		}
		out = append(out, c)
	}
	return views, out, rows.Err()
}
