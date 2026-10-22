package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/rpcserver"
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

// sweeper answers blobs.sweep, and nothing else.
type sweeper struct {
	api.ReadService
	dryRuns chan bool
	res     api.SweepResult
}

func (s *sweeper) SweepBlobs(_ context.Context, dryRun bool) (api.SweepResult, error) {
	s.dryRuns <- dryRun
	res := s.res
	res.DryRun = dryRun
	return res, nil
}

// sweep runs readctl sweep against a daemon that answers as s does.
func sweep(t *testing.T, s *sweeper, args ...string) (string, error) {
	t.Helper()
	srv := rpcserver.New(rpcserver.Options{Name: "readd test", Logger: slog.New(slog.DiscardHandler)})
	router, err := api.NewRouter(api.Services{RPC: srv, Read: s, Events: srv})
	if err != nil {
		t.Fatal(err)
	}
	run1 := rpctest.Serve(t, func(ctx context.Context, ln net.Listener) error { return srv.Serve(ctx, ln, router) })
	var out, errOut bytes.Buffer
	err = run(t.Context(), append([]string{"--socket", run1.Socket, "sweep"}, args...), strings.NewReader(""), &out, &errOut)
	return out.String(), err
}

func TestSweep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		dryRun bool
		want   []string
	}{
		{"real", nil, false, []string{"scanned 9 blobs", "in use 4", "waiting 3", "removed 2, 2048 bytes"}},
		{"dry run", []string{"-dry-run"}, true, []string{"dry run: nothing was removed or written", "would remove 2, 2048 bytes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &sweeper{dryRuns: make(chan bool, 1), res: api.SweepResult{
				Scanned: 9, InUse: 4, Waiting: 3, Removed: 2, RemovedBytes: 2048}}
			out, err := sweep(t, s, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if got := <-s.dryRuns; got != tc.dryRun {
				t.Fatalf("dryRun sent = %v, want %v", got, tc.dryRun)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestSweepRefusesAnArgument(t *testing.T) {
	s := &sweeper{dryRuns: make(chan bool, 1)}
	if _, err := sweep(t, s, "now"); err == nil || !strings.Contains(err.Error(), "usage: readctl sweep") {
		t.Fatalf("sweep now = %v", err)
	}
	if _, err := sweep(t, s, "-nope"); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if len(s.dryRuns) != 0 {
		t.Fatal("a refused command reached the daemon")
	}
}

func TestSweepIsKnown(t *testing.T) {
	if !known("sweep") || !strings.Contains(usage, "sweep [-dry-run]") {
		t.Fatal("sweep is not in the command list")
	}
}
