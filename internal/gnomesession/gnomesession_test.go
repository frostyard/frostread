package gnomesession_test

// The tests watch a private dbus-daemon (internal/bustest), never the
// machine's session bus, and no test sleeps to synchronize: an outcome the
// test waits for has a bound, and a wait that ends is a failure.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/bustest"
	"github.com/frostyard/frostread/internal/gnomesession"
)

// wait bounds every wait: the outcome, or the test fails.
const wait = 30 * time.Second

// closed waits for the watcher's channel to close, and fails when it does
// not within wait.
func closed(t *testing.T, ended <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ended:
	case <-time.After(wait):
		t.Fatalf("%s: the channel did not close", what)
	}
}

// open fails when the watcher's channel is already closed.
func open(t *testing.T, ended <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ended:
		t.Fatalf("%s: the channel closed", what)
	default:
	}
}

// The session's owner leaving the bus closes the channel.
func TestWatchEndsWhenTheSessionLeaves(t *testing.T) {
	bus := bustest.Start(t)
	leave := bustest.Own(t, bus.Address(), gnomesession.Name)

	ended, err := gnomesession.Watch(t.Context(), bus.Address())
	if err != nil {
		t.Fatal(err)
	}
	open(t, ended, "while the session was there")
	leave()
	closed(t, ended, "after the session left")
}

// A bus with no owner of the name is no GNOME session.
func TestWatchReportsNoSession(t *testing.T) {
	bus := bustest.Start(t)
	// Another name on the bus: the bus itself is up, and this name is not
	// the session's.
	bustest.Own(t, bus.Address(), "org.example.Other")

	if _, err := gnomesession.Watch(t.Context(), bus.Address()); !errors.Is(err, gnomesession.ErrNoSession) {
		t.Fatalf("Watch = %v, want ErrNoSession", err)
	}
}

// A bus that cannot be reached is an error, not a session that ended.
func TestWatchReportsAnUnreachableBus(t *testing.T) {
	_, err := gnomesession.Watch(t.Context(), "unix:path=/nonexistent/frostread-test-bus")
	if err == nil {
		t.Fatal("Watch reached no bus, and reported no error")
	}
	if errors.Is(err, gnomesession.ErrNoSession) {
		t.Fatalf("Watch = %v, want a bus that could not be reached", err)
	}
}

// The bus ending closes the channel: readd's connection to the session is
// gone, and nothing else will say so.
func TestWatchEndsWhenTheBusEnds(t *testing.T) {
	bus := bustest.Start(t)
	bustest.Own(t, bus.Address(), gnomesession.Name)

	ended, err := gnomesession.Watch(t.Context(), bus.Address())
	if err != nil {
		t.Fatal(err)
	}
	bus.Stop()
	closed(t, ended, "after the bus ended")
}

// A caller that stops its own work stops the watch, and reads no close of
// the channel: readd stopping for another reason is not the session ending.
// The goroutine ends with the connection, which this package's API does not
// hand out, so the test checks what a caller can see.
func TestWatchStopsWithItsContext(t *testing.T) {
	bus := bustest.Start(t)
	leave := bustest.Own(t, bus.Address(), gnomesession.Name)
	ctx, cancel := context.WithCancel(t.Context())

	ended, err := gnomesession.Watch(ctx, bus.Address())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	open(t, ended, "after the context ended")
	leave()
}
