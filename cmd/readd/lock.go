package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// errLockHeld is wrapped by lockStateDir's error when another process
// holds the lock. On lodge's host readd records it as the problem
// lock.held; on the desktop it does not, because the launcher starts a
// second readd on purpose (docs/specs/problem-records.md).
var errLockHeld = errors.New("the state directory's lock is held")

// lockHeldError is the error of a lock another process holds: its text
// names the directory, and it wraps errLockHeld.
type lockHeldError struct{ dir string }

func (e *lockHeldError) Error() string { return "another readd uses " + e.dir }
func (e *lockHeldError) Unwrap() error { return errLockHeld }

// lockStateDir takes an exclusive lock on dir/readd.lock and returns the
// open file; the lock lasts until the file is closed or the process ends,
// even by a crash. One readd owns a state directory, whatever its socket: a
// second one fails here at once, before it touches the database
// (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md).
// This holds on lodge's host too.
func lockStateDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "readd.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &lockHeldError{dir: dir}
		}
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	}
	return f, nil
}
