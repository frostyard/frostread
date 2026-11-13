// Package bustest starts a private D-Bus daemon for the tests of the
// packages that watch the session bus (internal/gnomesession, and readd's
// desktop mode). Only tests import it.
//
// The daemon is a session bus of its own on a Unix socket in a temporary
// directory: no test reaches the machine's real session bus, and no test
// needs one to be running. internal/bustest and internal/gnomesession are
// the only packages that import godbus.
package bustest

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Bus is a private dbus-daemon that serves this test alone.
type Bus struct {
	address string
	cmd     *exec.Cmd
	// stopped makes Stop and the test's cleanup end the process once.
	stopped sync.Once
	// done is closed when the daemon's process has ended.
	done chan struct{}
}

// Start runs a private dbus-daemon, and ends it when the test does. The
// test fails when dbus-daemon is not on PATH: CI's ubuntu-24.04 image and
// the host have it.
func Start(t *testing.T) *Bus {
	t.Helper()
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Fatalf("dbus-daemon is not on PATH, so a private session bus cannot run: %v", err)
	}
	// t.TempDir's path can pass the 108 bytes of a Unix socket address.
	dir, err := os.MkdirTemp("", "b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	config := filepath.Join(dir, "bus.conf")
	// The config holds the socket's path and no secret, in a directory this
	// test made 0700: the bus reads it, and no other process can.
	if err := os.WriteFile(config, []byte(configFile(socket)), 0o644); err != nil {
		t.Fatal(err)
	}
	// --print-address writes the bus's address to stdout as its first line,
	// so the test needs no sleep and no guess of the socket's name.
	cmd := exec.Command(path, "--nofork", "--config-file="+config, "--print-address")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	address, err := readAddress(stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("dbus-daemon printed no address: %v (stderr %q)", err, stderr.String())
	}
	b := &Bus{address: address, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(b.done) }()
	t.Cleanup(b.Stop)
	return b
}

// readAddress is the first line of the daemon's output: the bus's address.
func readAddress(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	address := strings.TrimSpace(line)
	if address == "" {
		return "", errors.New("an empty first line")
	}
	return address, nil
}

// configFile is the daemon's configuration: a session bus on socket, EXTERNAL
// authentication, and a policy that lets the test's connections talk and own
// names.
func configFile(socket string) string {
	return fmt.Sprintf(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=%s</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
    <allow user="*"/>
  </policy>
</busconfig>
`, socket)
}

// Address is the bus's address, for the connection's environment or for a
// package that takes an address.
func (b *Bus) Address() string { return b.address }

// Stop ends the bus: the daemon's process only, by its pid, and the test
// waits for it to be gone. A second call does nothing.
func (b *Bus) Stop() {
	b.stopped.Do(func() {
		_ = b.cmd.Process.Kill()
		<-b.done
	})
}

// Own connects to the bus at address and requests name, which it must become
// the primary owner of. The func it returns closes that connection, so the
// name has no owner again: the bus then sends NameOwnerChanged, as the
// session's own end does. The connection is closed when the test ends too.
func Own(t *testing.T, address, name string) func() {
	t.Helper()
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatalf("connect to the test's bus: %v", err)
	}
	// DBUS_NAME_FLAG_DO_NOT_QUEUE: a name already owned is a fault here.
	const doNotQueue = 0x4
	var reply uint32
	if err := conn.BusObject().Call("org.freedesktop.DBus.RequestName", 0, name, uint32(doNotQueue)).
		Store(&reply); err != nil {
		_ = conn.Close()
		t.Fatalf("RequestName %s: %v", name, err)
	}
	// 1 is DBUS_REQUEST_NAME_REPLY_PRIMARY_OWNER, 4 already owner.
	if reply != 1 && reply != 4 {
		_ = conn.Close()
		t.Fatalf("RequestName %s = %d, want the primary owner", name, reply)
	}
	var once sync.Once
	leave := func() { once.Do(func() { _ = conn.Close() }) }
	t.Cleanup(leave)
	return leave
}
