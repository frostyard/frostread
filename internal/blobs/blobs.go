// Package blobs stores immutable bodies by their SHA-256, under
// <dir>/sha256/<first two hex digits>/<rest> (docs/specs/api.md, State).
// An article's original bytes and each of its images are one blob; the
// same bytes stored twice are one file.
package blobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrInvalidID rejects an id that is not 64 lowercase hex digits.
	ErrInvalidID = errors.New("blobs: invalid id")
	// ErrTooLarge rejects content over the store's cap.
	ErrTooLarge = errors.New("blobs: content is too large")
)

// Store is a directory of immutable blobs. It is safe for concurrent use.
type Store struct {
	dir string
	// MaxBytes caps one Put; 0 means no cap.
	MaxBytes int64
}

// New returns a Store rooted at dir. It creates no file until a Put.
func New(dir string) *Store { return &Store{dir: dir} }

// ID returns the lowercase hex SHA-256 of data.
func ID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(id string) (string, error) {
	if !validID(id) {
		return "", ErrInvalidID
	}
	return filepath.Join(s.dir, "sha256", id[:2], id), nil
}

// Put streams r into a temporary file, then atomically publishes the blob
// as its SHA-256. An existing blob is kept, and its content is not read
// again. Content over MaxBytes is an error, and nothing is published.
func (s *Store) Put(ctx context.Context, r io.Reader, mime string) (string, error) {
	_ = mime // the MIME type lives in the store's blobs table
	root := filepath.Join(s.dir, "sha256")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("blobs: create store: %w", err)
	}
	tmp, err := os.CreateTemp(root, ".put-*")
	if err != nil {
		return "", fmt.Errorf("blobs: create temp: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	var src io.Reader = r
	if s.MaxBytes > 0 {
		src = io.LimitReader(r, s.MaxBytes+1)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		return "", fmt.Errorf("blobs: read content: %w", err)
	}
	if s.MaxBytes > 0 && n > s.MaxBytes {
		return "", fmt.Errorf("blobs: content over %d bytes: %w", s.MaxBytes, ErrTooLarge)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := hex.EncodeToString(h.Sum(nil))
	final, err := s.path(id)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(final); err == nil {
		return id, nil // already stored
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("blobs: stat blob: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("blobs: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("blobs: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("blobs: close temp: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", fmt.Errorf("blobs: create shard: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", fmt.Errorf("blobs: rename into place: %w", err)
	}
	dir, err := os.Open(filepath.Dir(final))
	if err != nil {
		return "", fmt.Errorf("blobs: open shard: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return "", fmt.Errorf("blobs: sync shard: %w", err)
	}
	return id, nil
}

// Open opens a blob. A missing blob returns an error matching
// fs.ErrNotExist; a malformed id returns ErrInvalidID.
func (s *Store) Open(id string) (io.ReadCloser, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("blobs: open %s: %w", id, err)
	}
	return f, nil
}

// Get reads a blob's content.
func (s *Store) Get(id string) ([]byte, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("blobs: read %s: %w", id, err)
	}
	return b, nil
}

// Size returns a blob's size in bytes.
func (s *Store) Size(id string) (int64, error) {
	p, err := s.path(id)
	if err != nil {
		return 0, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return 0, fmt.Errorf("blobs: stat %s: %w", id, err)
	}
	return fi.Size(), nil
}

// CleanupTemp removes temporary files a stopped Put left behind.
func (s *Store) CleanupTemp() (int, error) {
	root := filepath.Join(s.dir, "sha256")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("blobs: list temps: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".put-") || !e.Type().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(root, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}
