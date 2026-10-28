package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

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
			return nil, fmt.Errorf("another readd uses %s", dir)
		}
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	}
	return f, nil
}
