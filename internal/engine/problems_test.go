package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/problemtest"
	"github.com/frostyard/frostread/internal/store"
)

// The engine's problem records (docs/specs/problem-records.md). The std
// package caps a process at 10 records in 60 seconds, and this package's
// tests are 1 process: together they write 3 records, 1 for each code.

// firstPost is setup's item whose article comes from its page.
func firstPost(t *testing.T, e *Engine) int64 {
	t.Helper()
	if _, err := e.AddSource(t.Context(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	items, _, err := e.ListItems(t.Context(), store.ListOptions{View: store.ViewUnread, Limit: 50})
	if err != nil || len(items) != 2 || items[1].Title != "First post" {
		t.Fatalf("items = %+v, %v", items, err)
	}
	return items[1].ID
}

// A blob store that cannot keep the page's original is readd's fault:
// extract.failed.
func TestExtractRecordsABlobStoreFault(t *testing.T) {
	j := problemtest.Listen(t)
	e, _, _, _ := setup(t)
	e.o.Problems = j.Recorder(t)
	id := firstPost(t, e)
	// The store's parent is a regular file: no Put can make its folder,
	// whoever runs the test.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.o.Blobs = blobs.New(filepath.Join(file, "blobs"))
	if err := e.ExtractItem(t.Context(), id); err == nil {
		t.Fatal("an extraction whose original cannot be stored succeeded")
	}
	f := j.Expect(t, "error", CodeExtractFailed)
	if !strings.Contains(f["MESSAGE"], "blobs: create store") || strings.Contains(f["MESSAGE"], "tomato") {
		t.Fatalf("message = %q, want the blob store's error and no article text", f["MESSAGE"])
	}
	j.None(t)
}

// A page with no article, a fetch error and an error status are the page's
// or the network's faults: no record.
func TestExtractRecordsNoPageFault(t *testing.T) {
	j := problemtest.Listen(t)
	for _, tc := range []struct {
		name string
		page answer
	}{
		{"no article", answer{body: "<html><body><p>Hi.</p></body></html>"}},
		{"fetch error", answer{err: errors.New("connection refused")}},
		{"not found", answer{status: 404}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, n, _, _ := setup(t)
			e.o.Problems = j.Recorder(t)
			id := firstPost(t, e)
			n.pages["https://example.com/first"] = tc.page
			if err := e.ExtractItem(t.Context(), id); err == nil {
				t.Fatal("the extraction succeeded")
			}
			d, err := e.Item(t.Context(), id)
			if err != nil || d.ExtractState != store.ExtractFailed {
				t.Fatalf("item = %+v, %v; want a failed extraction", d, err)
			}
			j.None(t)
		})
	}
}

// An article whose page alone is over items.render's bound records
// render.too_large.
func TestRenderRecordsTooLarge(t *testing.T) {
	j := problemtest.Listen(t)
	e, _, _, _ := setup(t)
	e.o.Problems = j.Recorder(t)
	id := firstPost(t, e)
	e.page.Limit = 64
	if _, err := e.Render(t.Context(), id, ""); !errors.Is(err, content.ErrTooLarge) {
		t.Fatalf("render = %v, want ErrTooLarge", err)
	}
	f := j.Expect(t, "error", CodeRenderTooLarge)
	if !strings.Contains(f["MESSAGE"], content.ErrTooLarge.Error()) {
		t.Fatalf("message = %q", f["MESSAGE"])
	}
	// A page under the bound records nothing.
	e.page.Limit = 0
	if _, err := e.Render(t.Context(), id, ""); err != nil {
		t.Fatal(err)
	}
	j.None(t)
}

// A scheduled sweep that fails records blobs.sweep_failed.
func TestRunBlobSweepRecordsAFailure(t *testing.T) {
	j := problemtest.Listen(t)
	h := newSweepEnv(t, config.Config{})
	h.e.o.Problems = j.Recorder(t)
	// A closed database fails the sweep's mark.
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.e.sweepFirst, h.e.sweepEvery = time.Nanosecond, time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	ended := make(chan struct{})
	go func() { h.e.RunBlobSweep(ctx); close(ended) }()
	f := j.Expect(t, "error", CodeSweepFailed)
	cancel()
	<-ended
	if !strings.Contains(f["MESSAGE"], "sweep blobs") {
		t.Fatalf("message = %q", f["MESSAGE"])
	}
	j.None(t)
}
