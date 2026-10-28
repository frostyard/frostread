package main

// The desktop mode's tests: a temporary XDG tree, an in-process readd, and
// a feed server on a Unix socket, so no test reaches the network
// (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md).

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/store"
)

// xdgTree is a temporary XDG tree: the desktop's data, configuration and
// runtime directories.
type xdgTree struct{ root string }

func newXDG(t *testing.T) xdgTree {
	t.Helper()
	return xdgTree{root: t.TempDir()}
}

func (x xdgTree) data() string   { return filepath.Join(x.root, "data") }
func (x xdgTree) config() string { return filepath.Join(x.root, "config") }
func (x xdgTree) run() string    { return filepath.Join(x.root, "run") }

// state is readd's state directory in the tree, as the daemon derives it.
func (x xdgTree) state(t *testing.T) string {
	t.Helper()
	dir, err := config.DesktopStateDir(x.env(nil))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// env is the environment readd and readctl see, with extra's names added.
func (x xdgTree) env(extra map[string]string) func(string) string {
	m := map[string]string{
		"HOME": filepath.Join(x.root, "home"), "XDG_DATA_HOME": x.data(),
		"XDG_CONFIG_HOME": x.config(), "XDG_RUNTIME_DIR": x.run(),
	}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

// feed is the one page the test's server answers.
const feed = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Example Journal</title><link>https://example.com/</link>
<item><title>First post</title><link>https://example.com/first</link><guid>1</guid>
<description>An article.</description></item></channel></rss>`

// feedServer serves the feed on a Unix socket, which no network reaches.
func feedServer(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "feed.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feed.xml" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, feed)
	}))
	_ = srv.Listener.Close() // httptest's own TCP listener: the feed is local
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return socket
}

// unixFetch fetches over a Unix socket: the daemon's requests reach the
// test's feed server, and nothing else.
type unixFetch struct{ socket string }

func (u unixFetch) Get(ctx context.Context, rawurl string, o fetch.Options) (*fetch.Response, error) {
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", u.socket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	max := o.MaxBytes
	if max <= 0 {
		max = fetch.MaxPage
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fetch.ErrTooLarge
	}
	return &fetch.Response{Status: res.StatusCode, URL: rawurl, Header: res.Header, Body: body}, nil
}

// startRead runs readd, and returns its private socket, the channel its
// error comes on, and how to stop it.
func startRead(t *testing.T, o options, args ...string) (string, <-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan string, 1)
	o.listening = func(socket string) { ready <- socket }
	errs := make(chan error, 1)
	go func() { errs <- run(ctx, args, o) }()
	select {
	case socket := <-ready:
		t.Cleanup(cancel)
		return socket, errs, cancel
	case err := <-errs:
		cancel()
		t.Fatalf("readd did not start: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("readd did not start")
	}
	return "", nil, cancel
}

// stopRead stops readd, and fails when it does not stop cleanly.
func stopRead(t *testing.T, cancel context.CancelFunc, errs <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("readd stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("readd did not stop")
	}
}

// A desktop readd serves rpc.hello and status on the socket the Flatpak's
// tree derives, the one readctl finds, and polls a feed.
func TestDesktopServesAndPolls(t *testing.T) {
	x := newXDG(t)
	feeds := feedServer(t)
	o := options{getenv: x.env(map[string]string{"FLATPAK_ID": config.AppID}),
		fetcher: unixFetch{socket: feeds}}
	socket, errs, cancel := startRead(t, o, "-desktop")
	defer func() { stopRead(t, cancel, errs) }()

	want, err := config.DesktopSocket(o.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if socket != want {
		t.Fatalf("readd serves %q, readctl finds %q", socket, want)
	}
	if !strings.HasPrefix(socket, filepath.Join(x.run(), "app", config.AppID)) {
		t.Fatalf("the socket is not the Flatpak's own: %q", socket)
	}

	c, hello, err := api.Dial(t.Context(), socket, "readctl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if hello.Caller != api.CallerLocal || hello.Protocol != api.Protocol {
		t.Fatalf("rpc.hello = %+v, want a local protocol %d", hello, api.Protocol)
	}
	st, err := c.Read().Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != version || st.Build != "" || st.Notes.Configured {
		t.Fatalf("status = %+v, want this daemon, no build and no notes", st)
	}

	if _, err := c.Read().AddSource(t.Context(), "http://example.com/feed.xml"); err != nil {
		t.Fatal(err)
	}
	page, err := c.Read().ListItems(t.Context(), api.ItemsListParams{View: store.ViewAll})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "First post" {
		t.Fatalf("items.list = %+v, %v", page, err)
	}
}

// One readd owns a state directory: a second one fails at once, whatever its
// socket.
func TestOneReaddPerStateDirectory(t *testing.T) {
	x := newXDG(t)
	o := options{getenv: x.env(nil)}
	_, errs, cancel := startRead(t, o, "-desktop")
	defer func() { stopRead(t, cancel, errs) }()

	// The running readd holds the lock on its own state directory, and its
	// database is there.
	state := x.state(t)
	if _, err := os.Stat(filepath.Join(state, "frostread.db")); err != nil {
		t.Fatalf("the running readd's database: %v", err)
	}
	if lock, err := lockStateDir(state); err == nil {
		_ = lock.Close()
		t.Fatalf("the lock on %s is free while readd runs", state)
	}

	// A second readd on another socket fails at once. -log would rotate
	// the log, which only the readd that holds the lock may do: the second
	// one must fail before that, so it writes no log.
	logPath := filepath.Join(t.TempDir(), "readd.log")
	other := filepath.Join(x.root, "other.sock")
	err := run(t.Context(), []string{"-desktop", "-log", logPath, "--socket", other},
		options{getenv: x.env(nil)})
	if err == nil || !strings.Contains(err.Error(), "another readd uses") {
		t.Fatalf("a second readd = %v, want \"another readd uses\"", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the second readd wrote its log: %v", err)
	}
}

// In lodge mode readd stops before it touches the database.
func TestLodgeModeOpensNoDatabase(t *testing.T) {
	x := newXDG(t)
	if err := os.MkdirAll(x.config(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"lodge":"https://lodge.example.ts.net","theme":"dark"}`
	modePath, err := config.ModePath(x.env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(modePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"-desktop"}, options{getenv: x.env(nil)}); err != nil {
		t.Fatalf("run = %v, want a clean stop", err)
	}
	if _, err := os.Stat(filepath.Join(x.state(t), "frostread.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the database exists: %v", err)
	}
	// The lock is free again: nothing holds the directory.
	lock, err := lockStateDir(x.state(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
}

// daemon.shutdown answers, and then readd stops and releases its lock.
func TestShutdownStopsReadd(t *testing.T) {
	x := newXDG(t)
	o := options{getenv: x.env(nil)}
	socket, errs, _ := startRead(t, o, "-desktop")

	c, _, err := api.Dial(t.Context(), socket, "readctl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Daemon().Shutdown(t.Context()); err != nil {
		t.Fatalf("daemon.shutdown = %v", err)
	}
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("readd stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("readd did not stop after daemon.shutdown")
	}
	lock, err := lockStateDir(x.state(t))
	if err != nil {
		t.Fatalf("the lock is still held: %v", err)
	}
	_ = lock.Close()
}

// A desktop readd opens no TCP or UDP socket, and its one Unix listener is
// its private socket beside the test's own feed server.
func TestDesktopListensOnNoNetworkPort(t *testing.T) {
	x := newXDG(t)
	feeds := feedServer(t)
	o := options{getenv: x.env(nil), fetcher: unixFetch{socket: feeds}}
	socket, errs, cancel := startRead(t, o, "-desktop")
	defer func() { stopRead(t, cancel, errs) }()

	open := socketInodes(t)
	for _, name := range []string{"tcp", "tcp6"} {
		for inode, state := range netSockets(t, name) {
			if open[inode] && state == "0A" {
				t.Errorf("%s has a listening socket of this process: inode %s", name, inode)
			}
		}
	}
	for _, name := range []string{"udp", "udp6"} {
		for inode := range netSockets(t, name) {
			if open[inode] {
				t.Errorf("%s has a socket of this process: inode %s", name, inode)
			}
		}
	}
	allowed := map[string]bool{socket: true, feeds: true}
	seen := map[string]bool{}
	for inode, path := range unixListeners(t) {
		if !open[inode] {
			continue // another process's socket
		}
		if !allowed[path] {
			t.Errorf("a listening Unix socket is neither the private socket nor the feed: %q", path)
		}
		seen[path] = true
	}
	for _, path := range []string{socket, feeds} {
		if !seen[path] {
			t.Errorf("no listening socket of this process is %q", path)
		}
	}
}

// socketInodes is the sockets this process has open, by inode.
func socketInodes(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc: %v", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err != nil {
			continue // a descriptor the read raced with
		}
		if inode, ok := strings.CutPrefix(link, "socket:["); ok {
			out[strings.TrimSuffix(inode, "]")] = true
		}
	}
	return out
}

// netSockets is /proc/self/net/<name>'s sockets' states, by inode.
func netSockets(t *testing.T, name string) map[string]string {
	t.Helper()
	rows := procNet(t, name)
	out := map[string]string{}
	for _, f := range rows {
		if len(f) < 10 {
			continue
		}
		out[f[9]] = f[3]
	}
	return out
}

// unixListeners is this process's listening Unix sockets' paths, by inode.
// A socket with no path, such as an unnamed pair, is not in the map.
func unixListeners(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range procNet(t, "unix") {
		if len(f) < 7 || f[5] != "01" {
			continue // 01 is a listening socket
		}
		if len(f) < 8 {
			continue
		}
		out[f[6]] = f[7]
	}
	return out
}

// procNet is one /proc/self/net file's rows, split into fields, without its
// header.
func procNet(t *testing.T, name string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc/self/net", name))
	if err != nil {
		t.Skipf("no /proc/self/net/%s: %v", name, err)
	}
	var rows [][]string
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if i == 0 {
			continue // the header
		}
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

// The log moves to its one old copy past 1 MiB, and a smaller one stays.
func TestTheLogFileRotatesAtOneMebibyte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readd.log")
	old := path + ".1"
	open := func() {
		t.Helper()
		f, err := openLogFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	noOld := func() {
		t.Helper()
		if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("an old log = %v; want none", err)
		}
	}

	// A missing file is made, empty and private.
	open()
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the new log = %v, %v; want an empty file of mode 0600", fi, err)
	}
	noOld()

	// A small file stays where it is.
	if err := os.WriteFile(path, []byte("a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	open()
	if got, err := os.ReadFile(path); err != nil || string(got) != "a line\n" {
		t.Fatalf("the small log = %q, %v; want it kept", got, err)
	}
	noOld()

	// A file over 1 MiB moves, replacing the old copy.
	big := strings.Repeat("x", logLimit+1)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("an older log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	open()
	if got, err := os.ReadFile(old); err != nil || string(got) != big {
		t.Fatalf("the old log = %d bytes, %v; want the %d bytes it replaced", len(got), err, len(big))
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
		t.Fatalf("the new log = %v, %v; want an empty file", fi, err)
	}
}

// -build prints the Flatpak instance's app-commit, and an empty line off
// the Flatpak.
func TestBuildFlag(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "flatpak-info")
	body := "[Application]\nname=org.frostyard.Frostread\n\n[Instance]\napp-commit=abc1234\n"
	if err := os.WriteFile(info, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := buildOutput(t, options{flatpakInfo: info}); got != "abc1234\n" {
		t.Fatalf("-build = %q, want the instance's commit", got)
	}
	if got := buildOutput(t, options{flatpakInfo: filepath.Join(dir, "missing")}); got != "\n" {
		t.Fatalf("-build off the Flatpak = %q, want an empty line", got)
	}
}

// buildOutput is what readd -build prints.
func buildOutput(t *testing.T, o options) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, old := w, os.Stdout
	os.Stdout = out
	runErr := run(t.Context(), []string{"-build"}, o)
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	printed, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("-build: %v", runErr)
	}
	return string(printed)
}
