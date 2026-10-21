package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// The blob sweep's queries (docs/adr/0007-*). Every column that names a
// blob must be in ReferencedBlobs and BlobReferenced.

// ReferencedBlobs returns the set of blob hashes some row names now: every
// item's original, and every image of an extraction.
func (d *DB) ReferencedBlobs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT original_hash FROM items WHERE original_hash <> ''
		UNION SELECT hash FROM item_blobs`)
	if err != nil {
		return nil, fmt.Errorf("list referenced blobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	set := map[string]struct{}{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("list referenced blobs: %w", err)
		}
		set[h] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list referenced blobs: %w", err)
	}
	return set, nil
}

// BlobReferenced reports whether some row names the blob now.
func (d *DB) BlobReferenced(ctx context.Context, hash string) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM items WHERE original_hash = ?1 AND ?1 <> '')
		OR EXISTS (SELECT 1 FROM item_blobs WHERE hash = ?1)`, hash).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check blob %s: %w", hash, err)
	}
	return n != 0, nil
}

// BlobOrphans returns when the sweep first found each blob unreferenced.
func (d *DB) BlobOrphans(ctx context.Context) (map[string]time.Time, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT hash, since FROM blob_orphans`)
	if err != nil {
		return nil, fmt.Errorf("list blob orphans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]time.Time{}
	for rows.Next() {
		var h string
		var since sql.NullString
		if err := rows.Scan(&h, &since); err != nil {
			return nil, fmt.Errorf("list blob orphans: %w", err)
		}
		out[h] = parseTime(since)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list blob orphans: %w", err)
	}
	return out, nil
}

// RecordBlobOrphans records since for each hash that has no row yet, and
// keeps the earlier time of one that has. It also deletes the rows of the
// hashes in stale: blobs back in use, or with no file.
func (d *DB) RecordBlobOrphans(ctx context.Context, found []string, since time.Time, stale []string) error {
	if len(found) == 0 && len(stale) == 0 {
		return nil
	}
	return d.Tx(ctx, func(tx *sql.Tx) error {
		for _, h := range found {
			if _, err := tx.ExecContext(ctx, `INSERT INTO blob_orphans (hash, since) VALUES (?, ?)
				ON CONFLICT (hash) DO NOTHING`, h, timeString(since)); err != nil {
				return fmt.Errorf("record blob orphan: %w", err)
			}
		}
		for _, h := range stale {
			if _, err := tx.ExecContext(ctx, `DELETE FROM blob_orphans WHERE hash = ?`, h); err != nil {
				return fmt.Errorf("clear blob orphan: %w", err)
			}
		}
		return nil
	})
}

// ForgetBlob deletes a removed blob's rows: its blobs row, and its
// blob_orphans row. It keeps the blobs row when an item_blobs row names the
// hash again: an extraction may have stored the same bytes after the
// removal, and its image needs the row. The writes are serialized, so the
// check and the delete are one step.
func (d *DB) ForgetBlob(ctx context.Context, hash string) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE hash = ?1
			AND NOT EXISTS (SELECT 1 FROM item_blobs WHERE hash = ?1)`, hash); err != nil {
			return fmt.Errorf("forget blob %s: %w", hash, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM blob_orphans WHERE hash = ?`, hash); err != nil {
			return fmt.Errorf("forget blob %s: %w", hash, err)
		}
		return nil
	})
}
