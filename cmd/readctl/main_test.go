package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A command readctl does not have is refused before it dials: no socket is
// needed.
func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{"--socket", filepath.Join(t.TempDir(), "none.sock"), "nope"}, strings.NewReader(""), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestNoCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{"--socket", filepath.Join(t.TempDir(), "none.sock")}, strings.NewReader(""), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "no command") {
		t.Fatalf("no command = %v", err)
	}
	if !strings.Contains(errOut.String(), "usage: readctl") {
		t.Fatalf("usage was not printed:\n%s", errOut.String())
	}
}

// The socket is --socket, then READ_SOCKET, then the default; a missing
// socket is a clear error, and not a token's.
func TestSocketOrder(t *testing.T) {
	dir := t.TempDir()
	flagSocket := filepath.Join(dir, "flag.sock")
	t.Setenv("READ_SOCKET", filepath.Join(dir, "env.sock"))
	var out, errOut bytes.Buffer
	err := run(t.Context(), []string{"--socket", flagSocket, "status"}, strings.NewReader(""), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "flag.sock") {
		t.Fatalf("--socket was not used: %v", err)
	}
	err = run(t.Context(), []string{"status"}, strings.NewReader(""), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "env.sock") {
		t.Fatalf("READ_SOCKET was not used: %v", err)
	}
}
