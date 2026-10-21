package blobs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPutAndOpen(t *testing.T) {
	s := New(t.TempDir())
	id, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if id != ID([]byte("hello")) {
		t.Fatalf("id = %s, want the SHA-256 of the content", id)
	}
	b, err := s.Get(id)
	if err != nil || string(b) != "hello" {
		t.Fatalf("get = %q, %v", b, err)
	}
	// Storing the same bytes again keeps one file.
	again, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain")
	if err != nil || again != id {
		t.Fatalf("second put = %s, %v", again, err)
	}
	if _, err := s.Open("../../etc/passwd"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("a bad id = %v, want ErrInvalidID", err)
	}
	if _, err := s.Open(ID([]byte("missing"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing blob = %v, want fs.ErrNotExist", err)
	}
}

func TestPutCap(t *testing.T) {
	s := New(t.TempDir())
	s.MaxBytes = 4
	if _, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an oversized put = %v, want ErrTooLarge", err)
	}
	id := ID([]byte("hello"))
	if _, err := s.Size(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the oversized blob was published: %v", err)
	}
	if n, err := s.CleanupTemp(); err != nil || n != 0 {
		t.Fatalf("cleanup = %d, %v; want none: a put removes its own temp file", n, err)
	}
}

func TestCancelledPutLeavesNothing(t *testing.T) {
	s := New(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Put(ctx, strings.NewReader("hello"), "text/plain"); err == nil {
		t.Fatal("a cancelled put succeeded")
	}
}

// CleanupTemp removes what a stopped process left behind.
func TestCleanupTemp(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(s.dir, "sha256", ".put-crashed")
	if err := os.WriteFile(left, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CleanupTemp(); err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v; want the one leftover", n, err)
	}
	if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the leftover survived: %v", err)
	}
}

// old sets a blob's modification time.
func old(t *testing.T, s *Store, id string, mod time.Time) {
	t.Helper()
	p, err := s.path(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestPutTouchesAnExistingBlob(t *testing.T) {
	s := New(t.TempDir())
	id, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-48 * time.Hour)
	old(t, s, id, then)
	before := time.Now().Add(-time.Second)
	if _, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.path(id)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.ModTime().Before(before) {
		t.Fatalf("mod time = %v after a second put, want now", fi.ModTime())
	}
}

func TestList(t *testing.T) {
	s := New(t.TempDir())
	if err := s.List(func(string, int64, time.Time) error { t.Fatal("an empty store listed a blob"); return nil }); err != nil {
		t.Fatalf("list of a store with no directory = %v", err)
	}
	want := map[string]int64{}
	for _, body := range []string{"one", "two", "three"} {
		id, err := s.Put(t.Context(), strings.NewReader(body), "text/plain")
		if err != nil {
			t.Fatal(err)
		}
		want[id] = int64(len(body))
	}
	root := filepath.Join(s.dir, "sha256")
	// A stopped Put's temp file, a name that is not an id, a name in the
	// wrong shard, a directory and a stray file in the root.
	good, _ := s.path(ID([]byte("one")))
	wrongShard := filepath.Join(root, "zz", strings.Repeat("a", 64))
	for _, p := range []string{
		filepath.Join(root, ".put-123"),
		filepath.Join(root, "ab", ".put-456"),
		filepath.Join(root, "ab", "not-an-id"),
		filepath.Join(root, "ab", strings.ToUpper(strings.Repeat("a", 64))),
		filepath.Join(root, "ab", strings.Repeat("a", 64)),
		wrongShard,
		filepath.Join(root, "stray"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(good), strings.Repeat("b", 64)), 0o700); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	err := s.List(func(id string, size int64, mod time.Time) error {
		if mod.IsZero() {
			t.Errorf("blob %s has no mod time", id)
		}
		got[id] = size
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The blob "aaaa…" sits in shard ab, which is not its shard: skipped.
	if len(got) != len(want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
	for id, size := range want {
		if got[id] != size {
			t.Fatalf("list = %v, want %v", got, want)
		}
	}
	stop := errors.New("stop")
	if err := s.List(func(string, int64, time.Time) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("list with a failing callback = %v", err)
	}
}

func TestRemoveIf(t *testing.T) {
	s := New(t.TempDir())
	id, err := s.Put(t.Context(), strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-time.Hour).Truncate(time.Second)
	old(t, s, id, then)

	removed, _, err := s.RemoveIf(id, func(time.Time) (bool, error) { return false, nil })
	if err != nil || removed {
		t.Fatalf("a no = %v, %v", removed, err)
	}
	boom := errors.New("boom")
	if removed, _, err := s.RemoveIf(id, func(time.Time) (bool, error) { return true, boom }); !errors.Is(err, boom) || removed {
		t.Fatalf("an error = %v, %v; want the file kept", removed, err)
	}
	if _, err := s.Size(id); err != nil {
		t.Fatalf("the blob went: %v", err)
	}
	var seen time.Time
	removed, size, err := s.RemoveIf(id, func(mod time.Time) (bool, error) { seen = mod; return true, nil })
	if err != nil || !removed || size != 5 {
		t.Fatalf("a yes = %v, %d, %v", removed, size, err)
	}
	if !seen.Equal(then) {
		t.Fatalf("callback saw mod time %v, want %v", seen, then)
	}
	if _, err := s.Size(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the blob stayed: %v", err)
	}
	called := false
	removed, _, err = s.RemoveIf(id, func(time.Time) (bool, error) { called = true; return true, nil })
	if err != nil || removed || called {
		t.Fatalf("a missing blob = %v, %v, called %v", removed, err, called)
	}
	if _, _, err := s.RemoveIf("nope", func(time.Time) (bool, error) { return true, nil }); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("a bad id = %v", err)
	}
}
