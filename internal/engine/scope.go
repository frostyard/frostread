package engine

import (
	"errors"
	"fmt"

	"github.com/frostyard/frostread/internal/store"
)

// Scope is an MCP token's right over frostread: read alone, or read and
// write. The grammar is exactly "read" or "read;write"
// (docs/specs/api.md, MCP).
type Scope struct {
	// Write admits the write tools and the engine's write methods.
	Write bool
}

// ParseScope reads a token's Lodge-Scope. Anything but the two the
// contract names is an error, and the kit answers 403 at initialize.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "read":
		return Scope{}, nil
	case "read;write":
		return Scope{Write: true}, nil
	}
	return Scope{}, fmt.Errorf("scope %q is not read or read;write", s)
}

// String is the scope as a token's scope string.
func (s Scope) String() string {
	if s.Write {
		return "read;write"
	}
	return "read"
}

var (
	// ErrReadOnly is a write that a read-only scope does not admit.
	ErrReadOnly = errors.New("the token's scope is read only")
	// ErrNotFound is an item or a source that does not exist.
	ErrNotFound = store.ErrNotFound
	// ErrConflict is a source that is already subscribed.
	ErrConflict = store.ErrConflict
	// ErrUnavailable is a feature that is not configured, such as
	// save-as-note without a notes token.
	ErrUnavailable = errors.New("not configured")
)
