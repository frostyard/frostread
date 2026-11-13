// Package gnomesession watches the GNOME session on the session bus, so that
// the desktop's readd stops when the session ends
// (docs/adr/0009-readd-stops-when-the-gnome-session-ends.md).
//
// gnome-session owns org.gnome.SessionManager on the session bus for the life
// of the graphical session, so that name's owner leaving the bus is the
// session ending. godbus is imported only here and in the test helper
// internal/bustest.
//
// The package dials the Unix socket of the bus itself, so that the address it
// takes is the one it connects to: a default or autolaunched bus is never
// reached. The dial and the bus's setup are bounded by a context of their own,
// because a peer that accepts a connection and never answers would otherwise
// hold readd, and its state directory's lock, for ever. The socket never takes
// a deadline: the bound closes it, so no deadline can end a connection that a
// running session needs. No error this package returns carries the address or
// the socket path: a net error prints the path, and the caller logs the error.
package gnomesession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// Name is the session bus name gnome-session owns for the life of the
// graphical session.
const Name = "org.gnome.SessionManager"

// setupTimeout bounds the bus's setup: the dial, the authentication, the
// match rule and the owner lookup.
const setupTimeout = 10 * time.Second

var (
	// ErrNoSession says that the session bus has no owner of Name: this is
	// not a GNOME session, and readd cannot stop at logout.
	ErrNoSession = errors.New("gnomesession: no owner of " + Name)
	// ErrUnsupportedAddress says that the address names no Unix socket the
	// bus could be on. Its text holds no address (D-Bus addresses can hold
	// a path or a token).
	ErrUnsupportedAddress = errors.New("gnomesession: the bus address names no Unix socket")
	// errUnreachable is the fixed text of a bus that could not be reached
	// for a reason this machine cannot name.
	errUnreachable = errors.New("gnomesession: the bus could not be reached")
)

// The bus's own name and the signal that says a name's owner changed.
const (
	busName    = "org.freedesktop.DBus"
	changeName = "NameOwnerChanged"
	// hasNoOwner is the bus's error when a name has no owner.
	hasNoOwner = "org.freedesktop.DBus.Error.NameHasNoOwner"
)

// Watch connects to the session bus at address and waits for the owner of
// Name, gnome-session, to leave it. The channel it returns is closed when
// that owner leaves, or when the connection to the bus ends; it is never
// closed when ctx ends, so a caller that stops its own work sees nothing.
//
// Watch returns ErrNoSession when the bus has no owner of Name,
// ErrUnsupportedAddress when the address names no Unix socket, and a wrapped
// error when the bus could not be watched: 1 of setupTimeout, ctx, an errno
// or a bus error's name. The connection is shut down before any of them.
func Watch(ctx context.Context, address string) (<-chan struct{}, error) {
	return watch(ctx, address, setupTimeout)
}

// watch is Watch with the setup's bound given, so that a test can hold a peer
// that never answers to a short 1.
func watch(ctx context.Context, address string, timeout time.Duration) (<-chan struct{}, error) {
	path, err := unixPath(address)
	if err != nil {
		return nil, err
	}
	conn, signals, owner, err := connect(ctx, path, timeout)
	if err != nil {
		return nil, err
	}
	ended := make(chan struct{})
	go watchEnd(ctx, conn, signals, owner, ended)
	return ended, nil
}

// unixPath is the socket path of the bus at address: the value of the first
// Unix entry's path key, or its abstract key as "@name"
// (https://dbus.freedesktop.org/doc/dbus-specification.html#addresses).
// Another key of that entry, such as guid, is ignored, and any other
// transport is ErrUnsupportedAddress: a bus that must be started, such as
// the autolaunch one, is never reached.
func unixPath(address string) (string, error) {
	for _, entry := range strings.Split(address, ";") {
		transport, keys, ok := strings.Cut(entry, ":")
		if !ok || transport != "unix" {
			continue
		}
		var path string
		for _, key := range strings.Split(keys, ",") {
			name, value, ok := strings.Cut(key, "=")
			if !ok {
				continue
			}
			switch name {
			case "path":
				path = unescape(value)
			case "abstract":
				path = "@" + unescape(value)
			}
		}
		// The first Unix entry is the bus's: an entry that names no socket
		// reaches no bus.
		if path == "" {
			return "", ErrUnsupportedAddress
		}
		return path, nil
	}
	return "", ErrUnsupportedAddress
}

// unescape decodes the escapes of a D-Bus address: %XX is 1 byte, and an
// escape that is not 2 hex digits stays as it is.
func unescape(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	var path strings.Builder
	path.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+3 <= len(value) {
			if b, err := strconv.ParseUint(value[i+1:i+3], 16, 8); err == nil {
				path.WriteByte(byte(b))
				i += 2
				continue
			}
		}
		path.WriteByte(value[i])
	}
	return path.String()
}

// connect dials the bus at path and sets the watch up: authenticating, saying
// Hello, asking for Name's NameOwnerChanged signals, then asking who owns the
// name. The setup has a context of its own, timeout or the caller's end at the
// earliest, and that context closes the socket: a peer that never answers ends
// it, and godbus's blocked read returns.
func connect(ctx context.Context, path string, timeout time.Duration) (*dbus.Conn, chan *dbus.Signal, string, error) {
	setupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	raw, err := dialer.DialContext(setupCtx, "unix", path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("gnomesession: connect: %w", setupCause(ctx, setupCtx, err))
	}
	uc, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, nil, "", fmt.Errorf("gnomesession: connect: %w", errUnreachable)
	}
	// A socket takes no deadline: a deadline can fire after the setup
	// answered, and godbus's reader would then end a connection that a running
	// session needs. The setup's context closes the socket instead, and the
	// watch takes the connection back when the setup is done.
	stopClose := context.AfterFunc(setupCtx, func() { _ = uc.Close() })
	var conn *dbus.Conn
	setUp := false
	defer func() {
		// Every failure deregisters the close and shuts the socket down; the
		// watch that started closes its own connection.
		if setUp {
			return
		}
		stopClose()
		if conn != nil {
			_ = conn.Close()
			return
		}
		_ = uc.Close()
	}()
	if conn, err = dbus.ConnectUnix(uc); err != nil {
		return nil, nil, "", fmt.Errorf("gnomesession: connect: %w", setupCause(ctx, setupCtx, err))
	}
	// The match rule comes before the owner lookup, so that a change between
	// the 2 is delivered and never lost.
	if err := conn.AddMatchSignalContext(setupCtx, matchOptions()...); err != nil {
		return nil, nil, "", fmt.Errorf("gnomesession: watch %s: %w", Name, setupCause(ctx, setupCtx, err))
	}
	// The signals channel is registered after the rule, and before the
	// lookup: the bus delivers in order, and the buffer holds a burst of
	// changes.
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	owner, err := nameOwner(setupCtx, conn)
	if err != nil {
		if errors.Is(err, ErrNoSession) {
			return nil, nil, "", err
		}
		return nil, nil, "", fmt.Errorf("gnomesession: the owner of %s: %w", Name, setupCause(ctx, setupCtx, err))
	}
	if !stopClose() {
		// The setup's bound ended, and its close runs or has run: the
		// connection is gone, so this watch never starts.
		return nil, nil, "", fmt.Errorf("gnomesession: connect: %w", setupCause(ctx, setupCtx, nil))
	}
	setUp = true
	return conn, signals, owner, nil
}

// setupCause is a failed setup's cause, as far as it can be logged: the
// caller's own end, the setup's bound, or safe(ctx, err).
func setupCause(ctx, setupCtx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if setupCtx.Err() != nil {
		return os.ErrDeadlineExceeded
	}
	return safe(ctx, err)
}

// safe is the part of err that can be logged. A bus error's name and this
// machine's errno, a deadline and ctx's error are kept; the peer's words and a
// net error's own text are not, because they can hold the socket path or the
// address.
func safe(ctx context.Context, err error) error {
	var callErr dbus.Error
	if errors.As(err, &callErr) && callErr.Name != "" {
		return errors.New(callErr.Name)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return os.ErrDeadlineExceeded
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return errUnreachable
}

// matchOptions is the rule that sends this connection Name's own
// NameOwnerChanged signals.
func matchOptions() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender(busName),
		dbus.WithMatchInterface(busName),
		dbus.WithMatchMember(changeName),
		dbus.WithMatchArg(0, Name),
	}
}

// nameOwner is the unique name that owns Name now, or ErrNoSession.
func nameOwner(ctx context.Context, conn *dbus.Conn) (string, error) {
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, busName+".GetNameOwner", 0, Name).Store(&owner); err != nil {
		var callErr dbus.Error
		if errors.As(err, &callErr) && callErr.Name == hasNoOwner {
			return "", ErrNoSession
		}
		return "", err
	}
	return owner, nil
}

// watchEnd closes ended when the owner it was started with leaves the bus, or
// when the bus connection ends, and closes the connection either way. 1 signal
// channel means the changes arrive in order, so no lock is needed.
func watchEnd(ctx context.Context, conn *dbus.Conn, signals <-chan *dbus.Signal, owner string, ended chan<- struct{}) {
	defer func() { _ = conn.Close() }()
	for {
		select {
		case <-ctx.Done():
			// The caller stopped: it reads no close of ended.
			return
		case signal, ok := <-signals:
			if ctx.Err() != nil {
				// The caller stopped while the signal was in flight:
				// its own stop is not the session's end.
				return
			}
			if !ok {
				// The bus connection ended; the session went with it.
				close(ended)
				return
			}
			if endsSession(signal, owner) {
				close(ended)
				return
			}
		}
	}
}

// endsSession says whether signal is the NameOwnerChanged that says the
// session's owner left the bus. The match rule asks for Name alone, and a
// signal that another name changed is never this one.
func endsSession(signal *dbus.Signal, owner string) bool {
	if signal.Name != busName+"."+changeName || len(signal.Body) != 3 {
		return false
	}
	name, ok := signal.Body[0].(string)
	if !ok || name != Name {
		return false
	}
	old, ok := signal.Body[1].(string)
	return ok && old == owner
}
