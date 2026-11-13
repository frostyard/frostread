// Package gnomesession watches the GNOME session on the session bus, so that
// the desktop's readd stops when the session ends
// (docs/adr/0009-readd-stops-when-the-gnome-session-ends.md).
//
// gnome-session owns org.gnome.SessionManager on the session bus for the life
// of the graphical session, so that name's owner leaving the bus is the
// session ending. godbus is imported only here and in the test helper
// internal/bustest. Watch always takes the bus address it is given: a default
// or autolaunched bus is never connected to.
package gnomesession

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// Name is the session bus name gnome-session owns for the life of the
// graphical session.
const Name = "org.gnome.SessionManager"

// ErrNoSession says that the session bus has no owner of Name: this is not a
// GNOME session, and readd cannot stop at logout.
var ErrNoSession = errors.New("gnomesession: no owner of " + Name)

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
// Watch returns ErrNoSession when the bus has no owner of Name, and a
// wrapped error when the bus could not be watched. The connection is shut
// down before either.
func Watch(ctx context.Context, address string) (<-chan struct{}, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("gnomesession: connect: %w", err)
	}
	// The match rule comes before the owner lookup, so that a change between
	// the 2 is delivered and never lost.
	if err := conn.AddMatchSignalContext(ctx, matchOptions()...); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("gnomesession: watch %s: %w", Name, err)
	}
	// The signals channel is registered after the rule, and before the
	// lookup: the bus delivers in order, and the buffer holds a burst of
	// changes.
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	owner, err := nameOwner(ctx, conn)
	if err != nil {
		_ = conn.Close()
		if errors.Is(err, ErrNoSession) {
			return nil, err
		}
		return nil, fmt.Errorf("gnomesession: the owner of %s: %w", Name, err)
	}
	ended := make(chan struct{})
	go watch(ctx, conn, signals, owner, ended)
	return ended, nil
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

// watch closes ended when the owner it was started with leaves the bus, or
// when the bus connection ends, and closes the connection either way. 1
// signal channel means the changes arrive in order, so no lock is needed.
func watch(ctx context.Context, conn *dbus.Conn, signals <-chan *dbus.Signal, owner string, ended chan<- struct{}) {
	defer func() { _ = conn.Close() }()
	for {
		select {
		case <-ctx.Done():
			// The caller stopped: it reads no close of ended.
			return
		case signal, ok := <-signals:
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
