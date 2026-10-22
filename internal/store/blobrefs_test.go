package store

import (
	"strings"
	"testing"
	"time"
)

func hash(c string) string { return strings.Repeat(c, 64) }

func TestReferencedBlobs(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	if set, err := d.ReferencedBlobs(ctx); err != nil || len(set) != 0 {
		t.Fatalf("empty store = %v, %v", set, err)
	}
	src, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := d.AddItems(ctx, src.ID, []NewItem{
		{GUID: "a", Title: "One", OriginalHash: hash("a")},
		{GUID: "b", Title: "Two"}, // no original: the empty hash is no reference
		{GUID: "c", Title: "Three"},
	})
	if err != nil || len(ids) != 3 {
		t.Fatalf("add items = %v, %v", ids, err)
	}
	if err := d.SetExtraction(ctx, ids[1], Extraction{State: ExtractDone,
		Images: []Blob{{Hash: hash("b"), Mime: "image/png", Size: 1}, {Hash: hash("a"), Mime: "image/png", Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	set, err := d.ReferencedBlobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 2 {
		t.Fatalf("referenced = %v, want hashes a and b only", set)
	}
	for _, h := range []string{hash("a"), hash("b")} {
		if _, ok := set[h]; !ok {
			t.Errorf("%s is not referenced", h)
		}
		if used, err := d.BlobReferenced(ctx, h); err != nil || !used {
			t.Errorf("BlobReferenced(%s) = %v, %v", h, used, err)
		}
	}
	if used, err := d.BlobReferenced(ctx, hash("c")); err != nil || used {
		t.Errorf("BlobReferenced(c) = %v, %v", used, err)
	}
	if used, err := d.BlobReferenced(ctx, ""); err != nil || used {
		t.Errorf("the empty hash is referenced: %v, %v", used, err)
	}
	// Removing the source cascades both columns away.
	if err := d.RemoveSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if set, err := d.ReferencedBlobs(ctx); err != nil || len(set) != 0 {
		t.Fatalf("after the source went = %v, %v", set, err)
	}
}

func TestBlobOrphans(t *testing.T) {
	d, now := open(t)
	ctx := t.Context()
	first := now.Add(-48 * time.Hour)
	if err := d.RecordBlobOrphans(ctx, []string{hash("a"), hash("b")}, first, nil); err != nil {
		t.Fatal(err)
	}
	// A second record keeps the first time.
	if err := d.RecordBlobOrphans(ctx, []string{hash("a"), hash("c")}, now, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := d.BlobOrphans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || !rows[hash("a")].Equal(first) || !rows[hash("b")].Equal(first) || !rows[hash("c")].Equal(now) {
		t.Fatalf("rows = %v", rows)
	}
	if err := d.RecordBlobOrphans(ctx, nil, now, []string{hash("a"), hash("zz")}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := d.BlobOrphans(ctx); len(rows) != 2 {
		t.Fatalf("after clearing a = %v", rows)
	}
	// ForgetBlob deletes both rows of a removed blob, and keeps the
	// blobs row of a hash an image names again.
	src, err := d.AddSource(ctx, Source{URL: "https://example.com/feed.xml", Title: "Example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := d.AddItems(ctx, src.ID, []NewItem{{GUID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetExtraction(ctx, ids[0], Extraction{State: ExtractDone,
		Images: []Blob{{Hash: hash("b"), Mime: "image/png", Size: 1}, {Hash: hash("c"), Mime: "image/png", Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.ExecContext(ctx, `DELETE FROM item_blobs WHERE hash = ?`, hash("c")); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{hash("b"), hash("c")} {
		if err := d.ForgetBlob(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM blobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blobs rows = %d, %v; want only b, which an item names", n, err)
	}
	if rows, _ := d.BlobOrphans(ctx); len(rows) != 0 {
		t.Fatalf("orphan rows = %v, want none", rows)
	}
}
