package gnomesession

// The package's own tests: the pieces that only it can see, such as the
// address parser and the setup's bound. The tests of what a caller sees are in
// gnomesession_test.go, against a private dbus-daemon.

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// bounded is the longest a test waits for an outcome before it fails: no test
// sleeps to synchronize, and a wait that ends is a fault.
const bounded = 30 * time.Second

// silentBus is a Unix socket that accepts 1 connection and never answers, as
// a bus that a stopped machine left behind does.
type silentBus struct {
	address  string
	ln       net.Listener
	accepted chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	conn     net.Conn
}

func newSilentBus(t *testing.T) *silentBus {
	t.Helper()
	// t.TempDir's path can pass the 108 bytes of a Unix socket address.
	dir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	b := &silentBus{address: "unix:path=" + socket, ln: ln,
		accepted: make(chan struct{}), done: make(chan struct{})}
	go b.accept()
	t.Cleanup(b.close)
	return b
}

// accept takes the 1 connection the watcher makes, and holds it open.
func (b *silentBus) accept() {
	defer close(b.done)
	conn, err := b.ln.Accept()
	if err != nil {
		return
	}
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()
	close(b.accepted)
}

func (b *silentBus) close() {
	_ = b.ln.Close()
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		_ = b.conn.Close()
	}
}

// watchIn runs watch on its own goroutine, so that a watch that never returns
// fails the test instead of hanging it.
func watchIn(t *testing.T, ctx context.Context, address string, timeout time.Duration) <-chan error {
	t.Helper()
	errs := make(chan error, 1)
	go func() {
		_, err := watch(ctx, address, timeout)
		errs <- err
	}()
	return errs
}

// A peer that accepts and never answers ends the setup at its bound: readd
// holds its state directory's lock while it waits, so the wait must end.
func TestWatchBoundsAPeerThatNeverAnswers(t *testing.T) {
	bus := newSilentBus(t)
	errs := watchIn(t, t.Context(), bus.address, 200*time.Millisecond)
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("watch reached no bus, and reported no error")
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("watch = %v, want the setup's deadline", err)
		}
	case <-time.After(bounded):
		t.Fatal("watch did not return: the setup is unbounded")
	}
}

// A caller that stops reaches the setup: the dialect's deadline is set from
// the past, and a blocked authentication read returns.
func TestWatchEndsWhenTheContextEndsDuringSetup(t *testing.T) {
	bus := newSilentBus(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := watchIn(t, ctx, bus.address, time.Minute)
	select {
	case <-bus.accepted:
	case err := <-errs:
		t.Fatalf("watch returned before the connection was made: %v", err)
	case <-time.After(bounded):
		t.Fatal("the watcher did not connect")
	}
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("watch = %v, want the caller's context", err)
		}
	case <-time.After(bounded):
		t.Fatal("watch outlived its context: the setup does not take it")
	}
}

// An address that cannot be reached is an error that holds no part of the
// address: a D-Bus address can name a path or a token, and the caller logs the
// error.
func TestWatchReportsNoAddress(t *testing.T) {
	const (
		secretPath = "frostread-secret-bus-path-7f3a"
		secretHost = "frostread-secret-host-7f3a"
	)
	for _, tc := range []struct {
		name    string
		address string
		secret  string
		want    error
	}{
		{"a socket nothing serves", "unix:path=" + filepath.Join(t.TempDir(), secretPath), secretPath, nil},
		{"another transport", "tcp:host=" + secretHost + ",port=1", secretHost, ErrUnsupportedAddress},
		{"a Unix entry with no socket", "unix:tmpdir=/tmp", "", ErrUnsupportedAddress},
		{"nothing at all", "", "", ErrUnsupportedAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Watch(t.Context(), tc.address)
			if err == nil {
				t.Fatal("Watch reached no bus, and reported no error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Watch = %v, want %v", err, tc.want)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("the error carries the address: %v", err)
			}
			if strings.Contains(err.Error(), "unix:") || strings.Contains(err.Error(), "tcp:") {
				t.Fatalf("the error carries the address: %v", err)
			}
		})
	}
}

// unixPath reads a D-Bus address's Unix socket, and no other.
func TestUnixPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		address string
		path    string
		ok      bool
	}{
		{"path", "unix:path=/run/user/1000/bus", "/run/user/1000/bus", true},
		{"abstract", "unix:abstract=/tmp/dbus-abc", "@/tmp/dbus-abc", true},
		{"guid ignored", "unix:path=/run/bus,guid=2e3f", "/run/bus", true},
		{"guid before path", "unix:guid=2e3f,path=/run/bus", "/run/bus", true},
		{"escaped", "unix:path=/run/user/a%20b/bus", "/run/user/a b/bus", true},
		{"a bad escape stays", "unix:path=/run/%zz", "/run/%zz", true},
		{"the first Unix entry wins", "unix:path=/run/one;unix:path=/run/two", "/run/one", true},
		{"a Unix entry after another", "tcp:host=example.org,port=1;unix:path=/run/bus", "/run/bus", true},
		{"no socket in the entry", "unix:tmpdir=/tmp", "", false},
		{"another transport", "tcp:host=example.org,port=1", "", false},
		{"autolaunch", "autolaunch:", "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := unixPath(tc.address)
			if !tc.ok {
				if !errors.Is(err, ErrUnsupportedAddress) {
					t.Fatalf("unixPath(%q) = %q, %v, want ErrUnsupportedAddress", tc.address, path, err)
				}
				return
			}
			if err != nil || path != tc.path {
				t.Fatalf("unixPath(%q) = %q, %v, want %q", tc.address, path, err, tc.path)
			}
		})
	}
}

// unescape leaves a value with no escapes as it is.
func TestUnescape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/run/user/1000/bus", "/run/user/1000/bus"},
		{"/run/a%2Fb", "/run/a/b"},
		{"%41%42", "AB"},
		{"100%", "100%"},
		{"%4", "%4"},
	} {
		if got := unescape(tc.in); got != tc.want {
			t.Errorf("unescape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
