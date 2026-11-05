package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/std/problems"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/problemtest"
	"github.com/frostyard/frostread/internal/store"
)

// The engine's problem records (docs/specs/problem-records.md). The std
// package caps a process at 10 records in 60 seconds, and this package's
// tests are 1 process: together they write 3 records, 1 for each code. The
// panic tests write theirs from child processes.

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

// panicFetch is a fetcher with a bug: every request panics.
type panicFetch struct{}

func (panicFetch) Get(context.Context, string, fetch.Options) (*fetch.Response, error) {
	panic("a fetcher with a bug")
}

// The child process of TestWorkerPanicsAreRecorded: its mode, and the
// journal socket its recorder writes to.
const (
	panicChildMode   = "FROSTREAD_TEST_PANIC_CHILD"
	panicChildSocket = "FROSTREAD_TEST_PANIC_SOCKET"
)

// A panic in a goroutine the engine starts, an extraction worker or a poll,
// writes 1 panic record before the process ends. The panic ends the
// process, so each case runs in a child process: this test binary again.
func TestWorkerPanicsAreRecorded(t *testing.T) {
	if mode := os.Getenv(panicChildMode); mode != "" {
		panicChild(t, mode)
		return
	}
	for _, mode := range []string{"extract", "poll"} {
		t.Run(mode, func(t *testing.T) {
			j := problemtest.Listen(t)
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWorkerPanicsAreRecorded$")
			cmd.Env = append(os.Environ(), panicChildMode+"="+mode, panicChildSocket+"="+j.Path)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() == 0 || !strings.Contains(string(out), "a fetcher with a bug") {
				t.Fatalf("the child = %v, want the panic's non-zero exit:\n%s", err, out)
			}
			f := j.Expect(t, "panic", "")
			if f["MESSAGE"] != "a fetcher with a bug" {
				t.Fatalf("message = %q", f["MESSAGE"])
			}
			if !strings.Contains(f["FROSTYARD_FRAMES"], "github.com/frostyard/frostread/internal/engine.panicFetch.Get") {
				t.Fatalf("frames = %q, want the fetcher's", f["FROSTYARD_FRAMES"])
			}
			j.None(t)
		})
	}
}

// panicChild runs an engine whose fetcher panics, in mode extract (an
// extraction worker) or poll (a poll PollDue starts). The panic ends this
// process; returning is a failure.
func panicChild(t *testing.T, mode string) {
	e, _, _, _ := setup(t)
	rec := problems.New(problemtest.AppID, problemtest.Version, problems.ComponentDaemon,
		problems.WithSocket(os.Getenv(panicChildSocket)))
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	id := firstPost(t, e)
	e.o.Problems = rec
	e.o.Fetch = panicFetch{}
	switch mode {
	case "extract":
		e.Enqueue(id)
		e.RunExtraction(t.Context(), 1)
	case "poll":
		// A day later, the source is due.
		later := e.now().Add(24 * time.Hour)
		e.now = func() time.Time { return later }
		if _, err := e.PollDue(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("mode %s: the engine did not panic", mode)
}
