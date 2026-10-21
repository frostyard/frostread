package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Source is one subscription: a feed, its conditional-GET validators, when
// it polls next, and its last error.
type Source struct {
	ID           int64
	URL          string
	SiteURL      string
	Title        string
	ETag         string
	LastModified string
	PolledAt     time.Time
	NextPollAt   time.Time
	ErrorCount   int
	LastError    string
	CreatedAt    time.Time
	// Unread is the source's unread items, filled by ListSources.
	Unread int
}

const sourceColumns = `id, url, site_url, title, etag, last_modified, polled_at, next_poll_at,
	error_count, last_error, created_at`

// scanSource reads the sourceColumns.
func scanSource(row interface{ Scan(...any) error }, s *Source, extra ...any) error {
	var polled, next, created sql.NullString
	dest := []any{&s.ID, &s.URL, &s.SiteURL, &s.Title, &s.ETag, &s.LastModified, &polled, &next,
		&s.ErrorCount, &s.LastError, &created}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return err
	}
	s.PolledAt = parseTime(polled)
	s.NextPollAt = parseTime(next)
	s.CreatedAt = parseTime(created)
	return nil
}

// ListSources returns every source by title, each with its unread count.
func (d *DB) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+sourceColumns+`,
		(SELECT count(*) FROM items WHERE items.source_id = sources.id
			AND read_at IS NULL AND archived = 0)
		FROM sources ORDER BY title COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Source{}
	for rows.Next() {
		var s Source
		if err := scanSource(rows, &s, &s.Unread); err != nil {
			return nil, fmt.Errorf("list sources: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Source returns one source by id.
func (d *DB) Source(ctx context.Context, id int64) (Source, error) {
	var s Source
	err := scanSource(d.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = ?`, id), &s)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("get source %d: %w", id, err)
	}
	return s, nil
}

// SourceByURL returns the source with url, or ErrNotFound.
func (d *DB) SourceByURL(ctx context.Context, url string) (Source, error) {
	var s Source
	err := scanSource(d.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM sources WHERE url = ?`, url), &s)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("get source by url: %w", err)
	}
	return s, nil
}

// AddSource records a new source, scheduling its first poll at next. A URL
// that is already a source returns ErrConflict.
func (d *DB) AddSource(ctx context.Context, s Source, next time.Time) (Source, error) {
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO sources
			(url, site_url, title, etag, last_modified, next_poll_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			s.URL, s.SiteURL, s.Title, s.ETag, s.LastModified, timeString(next), timeString(d.Now()))
		if err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
		s.ID = id
		s.NextPollAt = next
		s.CreatedAt = d.Now()
		return nil
	})
	if err != nil {
		if isUnique(err) {
			return Source{}, fmt.Errorf("%s is already a source: %w", s.URL, ErrConflict)
		}
		return Source{}, err
	}
	return s, nil
}

// RenameSource sets a source's title.
func (d *DB) RenameSource(ctx context.Context, id int64, title string) error {
	return d.execOne(ctx, `UPDATE sources SET title = ? WHERE id = ?`, "rename source", title, id)
}

// RemoveSource deletes a source, and its items and their search rows with
// it. A saved link has no source, so it stays.
func (d *DB) RemoveSource(ctx context.Context, id int64) error {
	return d.execOne(ctx, `DELETE FROM sources WHERE id = ?`, "remove source", id)
}

// Polled records a poll's outcome on a source: its validators, when it
// polled, its next poll and its error.
func (d *DB) Polled(ctx context.Context, id int64, etag, lastModified string, next time.Time, errCount int, lastErr string) error {
	return d.execOne(ctx, `UPDATE sources SET etag = ?, last_modified = ?, polled_at = ?,
		next_poll_at = ?, error_count = ?, last_error = ? WHERE id = ?`,
		"record a poll", etag, lastModified, timeString(d.Now()), timeString(next), errCount, lastErr, id)
}

// DueSources returns at most limit sources whose next poll has come, the
// earliest first.
func (d *DB) DueSources(ctx context.Context, now time.Time, limit int) ([]Source, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+sourceColumns+` FROM sources
		WHERE next_poll_at <= ? ORDER BY next_poll_at, id LIMIT ?`, timeString(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Source{}
	for rows.Next() {
		var s Source
		if err := scanSource(rows, &s); err != nil {
			return nil, fmt.Errorf("list due sources: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// execOne runs a statement that must touch one row; none is ErrNotFound.
func (d *DB) execOne(ctx context.Context, q, what string, args ...any) error {
	res, err := d.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

// isUnique reports whether err is SQLite's uniqueness conflict.
func isUnique(err error) bool {
	var se interface{ Code() int }
	if errors.As(err, &se) {
		return se.Code() == 2067 || se.Code() == 1555 // SQLITE_CONSTRAINT_UNIQUE, _PRIMARYKEY
	}
	return false
}
