package store

import (
	"context"
	"fmt"
	"strings"
)

// searchColumns is the itemColumns, qualified for the join with the index.
const searchColumns = `items.id, items.source_id, items.guid, items.url, items.title, items.author,
	items.published_at, items.fetched_at, items.summary_html, items.content_html, items.text,
	items.original_hash, items.extract_state, items.extract_error, items.read_at, items.starred,
	items.archived, items.saved, items.note_path, items.created_at`

// Search returns the items matching query, best match first, each with a
// snippet of the matching words between **. A word matches as a prefix, a
// phrase in double quotes matches as written, and every word or phrase
// must match. Case and accents are ignored.
func (d *DB) Search(ctx context.Context, query string, limit int) ([]Item, error) {
	match, err := matchExpression(query)
	if err != nil {
		return nil, err
	}
	if match == "" {
		return []Item{}, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT `+searchColumns+`,
		snippet(items_fts, -1, '**', '**', '…', 12)
		FROM items_fts JOIN items ON items.id = items_fts.rowid
		WHERE items_fts MATCH ? ORDER BY bm25(items_fts), items.published_at DESC LIMIT ?`,
		match, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it, &it.Snippet); err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// matchExpression turns a reader's query into an FTS5 MATCH expression:
// each word becomes a prefix term, a double-quoted phrase stays a phrase,
// and all of them must match. A query with no word is empty.
func matchExpression(query string) (string, error) {
	var terms []string
	for _, raw := range strings.Fields(query) {
		if phrase, ok := strings.CutPrefix(raw, `"`); ok {
			phrase = strings.TrimSuffix(phrase, `"`)
			if phrase == "" {
				continue
			}
			terms = append(terms, quoteFTSTerm(phrase))
			continue
		}
		// A leading minus would negate; a reader means the word.
		word := strings.TrimLeft(raw, "-")
		if word == "" {
			continue
		}
		terms = append(terms, quoteFTSTerm(word)+"*")
	}
	return strings.Join(terms, " "), nil
}

// quoteFTSTerm quotes a term, its double quotes doubled as FTS5 wants.
func quoteFTSTerm(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}
