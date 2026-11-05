package main

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/std/problems"

	"github.com/frostyard/frostread/internal/problemtest"
)

// readd's own problem records (docs/specs/problem-records.md). The std
// package caps a process at 10 records in 60 seconds, and this package's
// tests are 1 process: together they write 3 records.

func TestAppID(t *testing.T) {
	if AppID != problemtest.AppID {
		t.Fatalf("AppID = %q, want %q", AppID, problemtest.AppID)
	}
}

// A release tag records; any other build's version makes the recorder
// inert.
func TestReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		active   bool
	}{
		{"v0.5.0", "0.5.0", true},
		{"v1.2.3-rc.1", "1.2.3-rc.1", true},
		{"dev", "dev", false},
		{"flatpak", "flatpak", false},
		{"v0.4.0-3-gabc1234", "0.4.0-3-gabc1234", false},
		{"v0.4.0-3-gabc1234-dirty", "0.4.0-3-gabc1234-dirty", false},
		{"0.0.0-abc1234-snapshot", "0.0.0-abc1234-snapshot", false},
		{"vv1.2.3", "v1.2.3", false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := releaseVersion(tc.in)
			if got != tc.want {
				t.Fatalf("releaseVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
			err := problems.New(AppID, got, problems.ComponentDaemon).Err()
			if (err == nil) != tc.active {
				t.Fatalf("the recorder of %q: err = %v, want active %v", tc.in, err, tc.active)
			}
		})
	}
}

// -version prints the version, and needs no state.
func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-version"}, options{out: &out}); err != nil {
		t.Fatal(err)
	}
	if out.String() != version+"\n" {
		t.Fatalf("-version = %q, want %q", out.String(), version+"\n")
	}
}

// On lodge's host a second readd on 1 state directory is a fault:
// lock.held.
func TestLockHeldIsRecordedOnLodgesHost(t *testing.T) {
	j := problemtest.Listen(t)
	state := t.TempDir()
	t.Setenv("STATE_DIRECTORY", state)
	t.Setenv("READ_CONFIG", filepath.Join(state, "none.json"))
	lock, err := lockStateDir(state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	err = run(t.Context(), nil, options{problems: j.Recorder(t)})
	if !errors.Is(err, errLockHeld) || err.Error() != "another readd uses "+state {
		t.Fatalf("a second readd = %v, want \"another readd uses %s\"", err, state)
	}
	f := j.Expect(t, "error", CodeLockHeld)
	if f["MESSAGE"] != err.Error() {
		t.Fatalf("message = %q, want %q", f["MESSAGE"], err.Error())
	}
	j.None(t)
}

// The Flatpak's launcher starts a second readd on purpose: no record.
func TestLockHeldIsNotRecordedOnTheDesktop(t *testing.T) {
	j := problemtest.Listen(t)
	x := newXDG(t)
	state := x.state(t)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockStateDir(state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	err = run(t.Context(), []string{"-desktop"}, options{getenv: x.env(nil), problems: j.Recorder(t)})
	if err == nil || !strings.Contains(err.Error(), "another readd uses") {
		t.Fatalf("a second readd = %v, want \"another readd uses\"", err)
	}
	j.None(t)
}

// A database newer than this readd fails its migration:
// store.migrate_failed.
func TestMigrateFailedIsRecorded(t *testing.T) {
	j := problemtest.Listen(t)
	x := newXDG(t)
	state := x.state(t)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "frostread.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA user_version = 9999`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = run(t.Context(), []string{"-desktop"}, options{getenv: x.env(nil), problems: j.Recorder(t)})
	if err == nil || !strings.Contains(err.Error(), "migrate: database schema 9999 is newer than this readd") {
		t.Fatalf("readd on a newer database = %v", err)
	}
	f := j.Expect(t, "error", CodeMigrateFailed)
	if f["MESSAGE"] != err.Error() {
		t.Fatalf("message = %q, want %q", f["MESSAGE"], err.Error())
	}
	j.None(t)
}

// The content route's and MCP's wrapper records a panic and panics again,
// so net/http handles it as before; a route readd does not serve stays nil.
func TestRecoveringHandler(t *testing.T) {
	if recovering(nil, nil) != nil {
		t.Fatal("a nil handler is not nil once wrapped")
	}
	j := problemtest.Listen(t)
	h := recovering(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("a route with a bug")
	}), j.Recorder(t))
	var again any
	func() {
		defer func() { again = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/content/item/1/mac", nil))
	}()
	if again != "a route with a bug" {
		t.Fatalf("the wrapper's panic = %v, want the handler's", again)
	}
	f := j.Expect(t, "panic", "")
	if f["MESSAGE"] != "a route with a bug" {
		t.Fatalf("message = %q", f["MESSAGE"])
	}
	j.None(t)
}
