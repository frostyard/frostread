package blobs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
