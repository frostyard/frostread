package engine

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/store"
)

const day = 24 * time.Hour

// clock is the engine's and the store's time, which a test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuf is a log's buffer that several goroutines may write.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sweepDoneHandler tells a test when a sweep logs its totals. It never
// blocks: a schedule of 1ns keeps sweeping after the test has read its
// signals, and a blocked send would keep RunBlobSweep from seeing its
// context end.
type sweepDoneHandler struct {
	slog.Handler
	done chan struct{}
}

func (h sweepDoneHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.Handler.Handle(ctx, r)
	if r.Message == "blob sweep done" {
		select {
		case h.done <- struct{}{}:
		default:
		}
	}
	return err
}

// sweepEnv is an engine over a real store and a real blob store. Its
// clock starts at the real time, because a blob's modification time is a
// real file time.
type sweepEnv struct {
	t     *testing.T
	e     *Engine
	db    *store.DB
	blobs *blobs.Store
	clock *clock
	log   *syncBuf
	raw   *sql.DB
	dir   string // the blob store's directory
	src   int64
	n     int
}

func newSweepEnv(t *testing.T, cfg config.Config) *sweepEnv {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "read.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	c := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	db.Now = c.Now
	b := blobs.New(filepath.Join(dir, "blobs"))
	logs := &syncBuf{}
	env := &sweepEnv{t: t, db: db, blobs: b, clock: c, log: logs, raw: raw, dir: filepath.Join(dir, "blobs")}
	if cfg.BlobSweep == "" {
		cfg.BlobSweep = config.SweepOn
	}
	if cfg.BlobGraceDays == 0 {
		cfg.BlobGraceDays = 7
	}
	env.e = New(Options{Store: db, Blobs: b, Config: cfg, Now: c.Now,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	env.src = env.source()
	return env
}

func (h *sweepEnv) source() int64 {
	h.t.Helper()
	h.n++
	s, err := h.db.AddSource(h.t.Context(), store.Source{
		URL: fmt.Sprintf("https://example.com/feed%d.xml", h.n), Title: "Example"}, h.clock.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	return s.ID
}

// put stores content as a blob, and returns its hash.
func (h *sweepEnv) put(content string) string {
	h.t.Helper()
	id, err := h.blobs.Put(h.t.Context(), strings.NewReader(content), "text/plain")
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

// age makes a blob's file older than the clock by d.
func (h *sweepEnv) age(id string, d time.Duration) {
	h.t.Helper()
	p := filepath.Join(h.dir, "sha256", id[:2], id)
	mod := h.clock.Now().Add(-d)
	if err := os.Chtimes(p, mod, mod); err != nil {
		h.t.Fatal(err)
	}
}

// recordSince records that a blob has been unreferenced since d ago.
func (h *sweepEnv) recordSince(id string, d time.Duration) {
	h.t.Helper()
	if err := h.db.RecordBlobOrphans(h.t.Context(), []string{id}, h.clock.Now().Add(-d), nil); err != nil {
		h.t.Fatal(err)
	}
}

// old makes a blob that no row names yet, old enough to go: its file and
// its record are 30 days old.
func (h *sweepEnv) old(content string) string {
	h.t.Helper()
	id := h.put(content)
	h.age(id, 30*day)
	h.recordSince(id, 30*day)
	return id
}

// item adds an item to the source, with an original, and returns its id.
func (h *sweepEnv) item(src int64, guid, original string) int64 {
	h.t.Helper()
	ids, err := h.db.AddItems(h.t.Context(), src, []store.NewItem{{GUID: guid, Title: guid, OriginalHash: original}})
	if err != nil || len(ids) != 1 {
		h.t.Fatalf("add item %s = %v, %v", guid, ids, err)
	}
	return ids[0]
}

// extracted records an extraction that names the images.
func (h *sweepEnv) extracted(id int64, images ...string) {
	h.t.Helper()
	var bs []store.Blob
	for _, im := range images {
		bs = append(bs, store.Blob{Hash: im, Mime: "image/png", Size: 1})
	}
	if err := h.db.SetExtraction(h.t.Context(), id, store.Extraction{State: store.ExtractDone, Images: bs}); err != nil {
		h.t.Fatal(err)
	}
}

// exists reports whether a blob's file is there.
func (h *sweepEnv) exists(id string) bool {
	_, err := h.blobs.Size(id)
	return err == nil
}

func (h *sweepEnv) files() []string {
	h.t.Helper()
	var ids []string
	if err := h.blobs.List(func(id string, _ int64, _ time.Time) error { ids = append(ids, id); return nil }); err != nil {
		h.t.Fatal(err)
	}
	slices.Sort(ids)
	return ids
}

func (h *sweepEnv) orphans() map[string]time.Time {
	h.t.Helper()
	m, err := h.db.BlobOrphans(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// blobRows lists the hashes in the blobs table.
func (h *sweepEnv) blobRows() []string {
	h.t.Helper()
	rows, err := h.raw.QueryContext(h.t.Context(), `SELECT hash FROM blobs ORDER BY hash`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (h *sweepEnv) sweep(dryRun bool) SweepResult {
	h.t.Helper()
	res, err := h.e.SweepBlobs(h.t.Context(), dryRun)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// Every column that names a blob keeps it: an image, an original, an item
// with a note, a pending item, a starred and an archived item, a saved
// link. Each blob is old and has an old record, so only a reference keeps
// it, and the sweep deletes the record of a blob back in use.
func TestSweepKeepsBlobsInUse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(h *sweepEnv) []string
	}{
		{"an image of an extraction", func(h *sweepEnv) []string {
			img := h.old("image")
			h.extracted(h.item(h.src, "a", ""), img)
			return []string{img}
		}},
		{"an original of a done item", func(h *sweepEnv) []string {
			orig := h.old("original")
			h.extracted(h.item(h.src, "a", orig))
			return []string{orig}
		}},
		{"an item with a saved note", func(h *sweepEnv) []string {
			orig, img := h.old("original"), h.old("image")
			id := h.item(h.src, "a", orig)
			h.extracted(id, img)
			if err := h.db.SetNotePath(t.Context(), id, "Reading/a.md"); err != nil {
				t.Fatal(err)
			}
			return []string{orig, img}
		}},
		{"the original of a pending item", func(h *sweepEnv) []string {
			orig := h.old("original")
			h.item(h.src, "a", orig) // extract_state stays pending
			return []string{orig}
		}},
		{"a starred and an archived item", func(h *sweepEnv) []string {
			o1, o2, img := h.old("o1"), h.old("o2"), h.old("image")
			a, b := h.item(h.src, "a", o1), h.item(h.src, "b", o2)
			h.extracted(a, img)
			if err := h.db.SetStarred(t.Context(), a, true); err != nil {
				t.Fatal(err)
			}
			if err := h.db.SetArchived(t.Context(), b, true); err != nil {
				t.Fatal(err)
			}
			return []string{o1, o2, img}
		}},
		{"a saved link", func(h *sweepEnv) []string {
			orig, img := h.old("original"), h.old("image")
			it, err := h.db.SaveLink(t.Context(), "https://example.org/read-later", "example.org")
			if err != nil {
				t.Fatal(err)
			}
			if err := h.db.SetOriginal(t.Context(), it.ID, orig); err != nil {
				t.Fatal(err)
			}
			h.extracted(it.ID, img)
			return []string{orig, img}
		}},
		{"an image two items share", func(h *sweepEnv) []string {
			img := h.old("image")
			h.extracted(h.item(h.src, "a", ""), img)
			h.extracted(h.item(h.src, "b", ""), img)
			return []string{img}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSweepEnv(t, config.Config{})
			ids := tc.setup(h)
			res := h.sweep(false)
			if res.Removed != 0 || res.RemovedBytes != 0 || res.Waiting != 0 || res.InUse != len(ids) || res.Scanned != len(ids) {
				t.Fatalf("result = %+v, want %d in use and nothing else", res, len(ids))
			}
			for _, id := range ids {
				if !h.exists(id) {
					t.Errorf("blob %s went", id[:8])
				}
			}
			if got := h.orphans(); len(got) != 0 {
				t.Errorf("orphan rows = %v, want none for blobs in use", got)
			}
		})
	}
}

// An orphan whose record and whose file are past the grace period goes,
// with its blobs row and its record; an original has no blobs row.
func TestSweepRemovesOrphansPastGrace(t *testing.T) {
	h := newSweepEnv(t, config.Config{})
	original := h.old("an original, as sent")
	image := h.old("an image")
	// The image had a blobs row, from an item that went with its source.
	gone := h.source()
	h.extracted(h.item(gone, "g", ""), image)
	if err := h.db.RemoveSource(t.Context(), gone); err != nil {
		t.Fatal(err)
	}
	kept := h.put("kept")
	h.extracted(h.item(h.src, "k", kept))
	if rows := h.blobRows(); len(rows) != 1 || rows[0] != image {
		t.Fatalf("blobs rows before = %v", rows)
	}

	res := h.sweep(false)
	want := SweepResult{Scanned: 3, InUse: 1, Removed: 2, RemovedBytes: int64(len("an original, as sent") + len("an image"))}
	if res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}
	if got := h.files(); len(got) != 1 || got[0] != kept {
		t.Fatalf("files = %v, want only the blob in use", got)
	}
	if rows := h.blobRows(); len(rows) != 0 {
		t.Errorf("blobs rows = %v, want none", rows)
	}
	if got := h.orphans(); len(got) != 0 {
		t.Errorf("orphan rows = %v, want none", got)
	}
	logs := h.log.String()
	for _, id := range []string{original, image} {
		if !strings.Contains(logs, "removed an orphaned blob") || !strings.Contains(logs, "hash="+id) {
			t.Errorf("the log lacks the removal of %s:\n%s", id[:8], logs)
		}
	}
	if strings.Contains(logs, "an original, as sent") || strings.Contains(logs, "would remove") {
		t.Errorf("the log has content or a dry run's line:\n%s", logs)
	}
	if !strings.Contains(logs, "blob sweep done") {
		t.Errorf("the log lacks the totals:\n%s", logs)
	}
	// A second sweep finds nothing to do.
	if res := h.sweep(false); res.Removed != 0 || res.Scanned != 1 || res.InUse != 1 {
		t.Fatalf("second sweep = %+v", res)
	}
}

// An orphan inside the grace period stays, whichever of the two times
// is recent.
func TestSweepKeepsOrphansInsideGrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(h *sweepEnv, id string)
		since func(h *sweepEnv) time.Time // the record after the sweep
	}{
		{"first seen now", func(h *sweepEnv, id string) { h.age(id, 30*day) },
			func(h *sweepEnv) time.Time { return h.clock.Now() }},
		{"an old record and a recent file", func(h *sweepEnv, id string) {
			h.age(id, day)
			h.recordSince(id, 30*day)
		}, func(h *sweepEnv) time.Time { return h.clock.Now().Add(-30 * day) }},
		{"an old file and a recent record", func(h *sweepEnv, id string) {
			h.age(id, 30*day)
			h.recordSince(id, day)
		}, func(h *sweepEnv) time.Time { return h.clock.Now().Add(-day) }},
		{"a record that is just inside", func(h *sweepEnv, id string) {
			h.age(id, 30*day)
			h.recordSince(id, 7*day-time.Minute)
		}, func(h *sweepEnv) time.Time { return h.clock.Now().Add(-7*day + time.Minute) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSweepEnv(t, config.Config{})
			id := h.put("an orphan")
			tc.setup(h, id)
			res := h.sweep(false)
			if res.Removed != 0 || res.Waiting != 1 || res.Scanned != 1 || res.InUse != 0 {
				t.Fatalf("result = %+v, want 1 waiting", res)
			}
			if !h.exists(id) {
				t.Fatal("the orphan went")
			}
			got := h.orphans()
			if len(got) != 1 || !got[id].Equal(tc.since(h)) {
				t.Fatalf("orphan rows = %v, want %s at %v", got, id[:8], tc.since(h))
			}
		})
	}
}

// The grace period is the configuration's.
func TestSweepGraceFollowsTheConfiguration(t *testing.T) {
	h := newSweepEnv(t, config.Config{BlobGraceDays: 30})
	id := h.put("an orphan")
	h.age(id, 60*day)
	h.recordSince(id, 20*day)
	if res := h.sweep(false); res.Removed != 0 || res.Waiting != 1 {
		t.Fatalf("20 days into a 30-day grace = %+v", res)
	}
	h.clock.Advance(11 * day)
	h.age(id, 60*day)
	if res := h.sweep(false); res.Removed != 1 {
		t.Fatalf("31 days into a 30-day grace = %+v", res)
	}
}

// Removing a source leaves its blobs as orphans: the first sweep records
// them, one inside the grace period keeps them, and one after it removes
// them. A blob another item names stays.
func TestSweepAfterASourceIsRemoved(t *testing.T) {
	h := newSweepEnv(t, config.Config{})
	doomed := h.source()
	original, image := h.put("the original"), h.put("an image")
	shared := h.put("an image both items name")
	a := h.item(doomed, "a", original)
	h.extracted(a, image, shared)
	b := h.item(h.src, "b", "")
	h.extracted(b, shared)
	for _, id := range []string{original, image, shared} {
		h.age(id, 30*day)
	}
	if res := h.sweep(false); res.InUse != 3 || res.Removed != 0 {
		t.Fatalf("before the removal = %+v", res)
	}
	if err := h.e.RemoveSource(t.Context(), doomed); err != nil {
		t.Fatal(err)
	}

	first := h.clock.Now()
	res := h.sweep(false)
	if res.Waiting != 2 || res.InUse != 1 || res.Removed != 0 || res.Scanned != 3 {
		t.Fatalf("first sweep after the removal = %+v", res)
	}
	got := h.orphans()
	if len(got) != 2 || !got[original].Equal(first) || !got[image].Equal(first) {
		t.Fatalf("orphan rows = %v, want original and image at %v", got, first)
	}

	h.clock.Advance(6 * day) // inside the grace period
	if res := h.sweep(false); res.Waiting != 2 || res.Removed != 0 {
		t.Fatalf("6 days later = %+v", res)
	}
	h.clock.Advance(2 * day) // 8 days: past it, and the files are older than the cutoff
	for _, id := range []string{original, image, shared} {
		h.age(id, 30*day)
	}
	res = h.sweep(false)
	if res.Removed != 2 || res.InUse != 1 || res.Waiting != 0 || res.RemovedBytes != int64(len("the original")+len("an image")) {
		t.Fatalf("8 days later = %+v", res)
	}
	if got := h.files(); len(got) != 1 || got[0] != shared {
		t.Fatalf("files = %v, want only the shared image", got)
	}
	if rows := h.blobRows(); len(rows) != 1 || rows[0] != shared {
		t.Fatalf("blobs rows = %v, want the shared image's", rows)
	}
	if got := h.orphans(); len(got) != 0 {
		t.Fatalf("orphan rows = %v", got)
	}
}

// A dry run removes nothing and writes nothing, and says what a real sweep
// would remove now.
func TestSweepDryRun(t *testing.T) {
	h := newSweepEnv(t, config.Config{})
	goes := h.old("goes")                 // old file, old record
	fresh := h.put("fresh")               // no record: new
	used := h.old("used")                 // a record, but a row names it now
	h.extracted(h.item(h.src, "a", used)) //
	noFile := h.put("no file")            // a record whose file is gone
	h.recordSince(noFile, 30*day)         //
	if _, _, err := h.blobs.RemoveIf(noFile, func(time.Time) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	h.age(fresh, 30*day)
	files, rows := h.files(), h.orphans()

	res := h.sweep(true)
	want := SweepResult{DryRun: true, Scanned: 3, InUse: 1, Waiting: 1, Removed: 1, RemovedBytes: int64(len("goes"))}
	if res != want {
		t.Fatalf("dry run = %+v, want %+v", res, want)
	}
	if got := h.files(); !slices.Equal(got, files) {
		t.Fatalf("files changed: %v, was %v", got, files)
	}
	got := h.orphans()
	if len(got) != len(rows) {
		t.Fatalf("orphan rows changed: %v, was %v", got, rows)
	}
	for id, since := range rows {
		if !got[id].Equal(since) {
			t.Fatalf("orphan rows changed: %v, was %v", got, rows)
		}
	}
	if _, ok := got[fresh]; ok {
		t.Fatal("a dry run started a grace period")
	}
	logs := h.log.String()
	if !strings.Contains(logs, "would remove an orphaned blob") || !strings.Contains(logs, "hash="+goes) ||
		strings.Contains(logs, "removed an orphaned blob") {
		t.Fatalf("the log of a dry run:\n%s", logs)
	}

	// The real sweep does what the dry run said, and cleans the records.
	real := h.sweep(false)
	if real.Removed != 1 || real.RemovedBytes != res.RemovedBytes || real.Waiting != 1 || real.InUse != 1 || real.DryRun {
		t.Fatalf("real sweep = %+v", real)
	}
	if h.exists(goes) || !h.exists(fresh) || !h.exists(used) {
		t.Fatalf("files after the real sweep: %v", h.files())
	}
	if got := h.orphans(); len(got) != 1 || got[fresh].IsZero() {
		t.Fatalf("orphan rows after the real sweep = %v, want only the new orphan's", got)
	}
}

// A save that lands while the sweep runs, after it recorded the orphans
// and before it removes them, keeps its blob.
func TestSweepKeepsABlobASaveWantsAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(h *sweepEnv, id string)
		want SweepResult
	}{
		{"a Put, then its reference", func(h *sweepEnv, id string) {
			h.put("an old orphan")
			h.extracted(h.item(h.src, "new", ""), id)
		}, SweepResult{Scanned: 1, Waiting: 1}},
		{"a Put alone, before its reference", func(h *sweepEnv, _ string) {
			h.put("an old orphan")
		}, SweepResult{Scanned: 1, Waiting: 1}},
		{"a reference alone, under an old file", func(h *sweepEnv, id string) {
			h.extracted(h.item(h.src, "new", ""), id)
		}, SweepResult{Scanned: 1, InUse: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSweepEnv(t, config.Config{})
			id := h.old("an old orphan")
			h.e.sweepHook = func() { tc.act(h, id) }
			res := h.sweep(false)
			if res != tc.want {
				t.Fatalf("result = %+v, want %+v", res, tc.want)
			}
			if !h.exists(id) {
				t.Fatal("the blob went")
			}
			if tc.want.InUse == 1 && len(h.blobRows()) != 1 {
				t.Fatalf("blobs rows = %v, want the image's", h.blobRows())
			}
		})
	}
}

// A sweep beside many saves never removes a blob a row names. After both
// end, every hash that a row names has its file and its blobs row; the
// blobs nobody wanted again are gone. Run it with the race detector.
func TestSweepBesideSaves(t *testing.T) {
	const wanted, dead, sweeps = 60, 20, 8
	h := newSweepEnv(t, config.Config{})
	items := make([]int64, wanted)
	contents := make([]string, wanted)
	var deadIDs []string
	for i := range wanted {
		contents[i] = fmt.Sprintf("old blob %d", i)
		h.old(contents[i])
		items[i] = h.item(h.src, fmt.Sprintf("i%d", i), "")
	}
	for i := range dead {
		deadIDs = append(deadIDs, h.old(fmt.Sprintf("dead blob %d", i)))
	}
	// The sweeps and the saves run on a clock that moves with real time
	// only, so a blob a Put touched is never old.
	started := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, wanted+sweeps)
	wg.Go(func() {
		close(started)
		for i := range wanted {
			id, err := h.blobs.Put(t.Context(), strings.NewReader(contents[i]), "text/plain")
			if err != nil {
				errs <- err
				return
			}
			err = h.db.SetExtraction(t.Context(), items[i], store.Extraction{State: store.ExtractDone,
				Images: []store.Blob{{Hash: id, Mime: "image/png", Size: 1}}})
			if err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		<-started
		for range sweeps {
			if _, err := h.e.SweepBlobs(t.Context(), false); err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	for i, item := range items {
		blobsOfItem, err := h.db.ItemBlobs(t.Context(), item)
		if err != nil || len(blobsOfItem) != 1 {
			t.Fatalf("item %d blobs = %v, %v; want its image", i, blobsOfItem, err)
		}
		if id := blobs.ID([]byte(contents[i])); !h.exists(id) {
			t.Fatalf("the image of item %d went, and its row names it", i)
		}
	}
	for _, id := range deadIDs {
		if h.exists(id) {
			t.Fatalf("dead blob %s stayed", id[:8])
		}
	}
	// Every file left is named by a row, and every blobs row has its file.
	referenced, err := h.db.ReferencedBlobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range h.files() {
		if _, ok := referenced[id]; !ok {
			t.Errorf("an unreferenced file stayed: %s", id[:8])
		}
	}
	for _, id := range h.blobRows() {
		if !h.exists(id) {
			t.Errorf("the blobs row of %s has no file", id[:8])
		}
	}
}

// Only the mark, the scan or the record failing is the sweep's error.
func TestSweepErrors(t *testing.T) {
	h := newSweepEnv(t, config.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.e.SweepBlobs(ctx, false); err == nil {
		t.Fatal("a sweep with a cancelled context succeeded")
	}
}

// The mode off has no schedule.
func TestRunBlobSweepOff(t *testing.T) {
	h := newSweepEnv(t, config.Config{BlobSweep: config.SweepOff})
	id := h.old("an old orphan")
	h.e.sweepFirst = time.Nanosecond
	h.e.RunBlobSweep(t.Context()) // returns at once; blocking would end at the test's timeout
	if !h.exists(id) {
		t.Fatal("the blob went with the sweep off")
	}
}

// The schedule sweeps, and again after each interval, until its context
// ends; dry-run only logs.
func TestRunBlobSweep(t *testing.T) {
	for _, mode := range []string{config.SweepOn, config.SweepDryRun} {
		t.Run(mode, func(t *testing.T) {
			h := newSweepEnv(t, config.Config{BlobSweep: mode})
			id := h.old("an old orphan")
			done := make(chan struct{}, 8)
			inner := h.e.log
			h.e.log = slog.New(sweepDoneHandler{Handler: inner.Handler(), done: done})
			h.e.sweepFirst, h.e.sweepEvery = time.Nanosecond, time.Nanosecond
			ctx, cancel := context.WithCancel(t.Context())
			ended := make(chan struct{})
			go func() { h.e.RunBlobSweep(ctx); close(ended) }()
			<-done // the first sweep
			<-done // the second, one interval later
			cancel()
			<-ended
			logs := h.log.String()
			if mode == config.SweepOn {
				if h.exists(id) || !strings.Contains(logs, "removed an orphaned blob") {
					t.Fatalf("the scheduled sweep did not remove the orphan:\n%s", logs)
				}
				return
			}
			if !h.exists(id) || !strings.Contains(logs, "would remove an orphaned blob") ||
				strings.Contains(logs, "removed an orphaned blob") {
				t.Fatalf("the scheduled dry run:\n%s", logs)
			}
		})
	}
}

func TestSweepSchedule(t *testing.T) {
	if BlobSweepDelay != 10*time.Minute || BlobSweepEvery != 24*time.Hour {
		t.Fatalf("schedule = %v then every %v", BlobSweepDelay, BlobSweepEvery)
	}
}
