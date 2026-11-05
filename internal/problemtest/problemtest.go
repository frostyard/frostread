// Package problemtest is a stand-in for the local journal in the tests of
// frostread's problem records (docs/specs/problem-records.md). Only tests
// import it.
//
// The std package github.com/frostyard/std/problems caps the records of a
// process at 10 in 60 seconds. A test binary is 1 process, so each package
// that uses this journal keeps its tests under 10 records in total.
package problemtest

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/frostyard/std/problems"
)

// AppID is frostread's app ID in its records, written out: a test that
// compares with it catches a change of config.AppID.
const AppID = "org.frostyard.Frostread"

// Version is the release version the test recorders carry.
const Version = "1.2.3"

// Journal is a datagram socket that takes the records of a test recorder.
type Journal struct {
	conn *net.UnixConn
	// Path is the socket's path, under the 108 bytes of a socket address.
	Path string
}

// Listen binds a journal in a short temporary folder, and closes it when
// the test ends.
func Listen(t *testing.T) *Journal {
	t.Helper()
	// t.TempDir's path can pass the 108 bytes of a socket address.
	dir, err := os.MkdirTemp("", "j")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Journal{conn: conn, Path: path}
}

// Recorder is an active recorder of component daemon at Version, that
// writes to this journal.
func (j *Journal) Recorder(t *testing.T) *problems.Recorder {
	t.Helper()
	rec := problems.New(AppID, Version, problems.ComponentDaemon, problems.WithSocket(j.Path))
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	return rec
}

// Next reads the next record, waiting at most 10 seconds for it, and
// checks it with problems.DecodeEntry and problems.Validate.
func (j *Journal) Next(t *testing.T) (map[string]string, problems.Detail) {
	t.Helper()
	if err := j.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	n, _, err := j.conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("no problem record: %v", err)
	}
	return decode(t, buf[:n])
}

// None fails when a record is waiting now. A recorder writes its datagram
// before its call returns, so a record of a call that has returned is
// already here: the check reads without waiting.
func (j *Journal) None(t *testing.T) {
	t.Helper()
	raw, err := j.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	var n int
	var rerr error
	if err := raw.Read(func(fd uintptr) bool {
		n, _, rerr = syscall.Recvfrom(int(fd), buf, syscall.MSG_DONTWAIT)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	switch {
	case errors.Is(rerr, syscall.EAGAIN):
		return
	case rerr != nil:
		t.Fatalf("read the journal: %v", rerr)
	}
	fields, _ := decode(t, buf[:n])
	t.Fatalf("an unexpected problem record: %s %s: %q", fields["FROSTYARD_CLASS"], fields["FROSTYARD_CODE"],
		fields["MESSAGE"])
}

// decode checks 1 datagram as every reader does.
func decode(t *testing.T, b []byte) (map[string]string, problems.Detail) {
	t.Helper()
	fields, err := problems.DecodeEntry(b)
	if err != nil {
		t.Fatalf("decode a problem record: %v", err)
	}
	d, err := problems.Validate(fields)
	if err != nil {
		t.Fatalf("validate a problem record: %v (%q)", err, fields)
	}
	return fields, d
}

// Expect reads the next record and checks that it is frostread's daemon
// record of class (panic or error) and code ("" for a panic). It returns
// the record's fields.
func (j *Journal) Expect(t *testing.T, class, code string) map[string]string {
	t.Helper()
	fields, d := j.Next(t)
	want := map[string]string{
		"MESSAGE_ID":            problems.MessageID,
		"FROSTYARD_APP_ID":      AppID,
		"FROSTYARD_APP_VERSION": Version,
		"FROSTYARD_RUNTIME":     "go",
		"FROSTYARD_COMPONENT":   string(problems.ComponentDaemon),
		"FROSTYARD_CLASS":       class,
	}
	if code != "" {
		want["FROSTYARD_CODE"] = code
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("record %s = %q, want %q", k, fields[k], v)
		}
	}
	if code == "" && d.Code != "" {
		t.Errorf("record code = %q, want none", d.Code)
	}
	if class == "panic" && len(d.Frames) == 0 {
		t.Error("a panic record has no frames")
	}
	return fields
}
