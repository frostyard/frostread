package config

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// env is a fake environment.
func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDesktopStateDir(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
		bad  bool
	}{
		{"an absolute XDG_DATA_HOME", map[string]string{"XDG_DATA_HOME": "/data", "HOME": "/home/u"}, "/data/frostread", false},
		{"HOME", map[string]string{"HOME": "/home/u"}, "/home/u/.local/share/frostread", false},
		{"a relative XDG_DATA_HOME", map[string]string{"XDG_DATA_HOME": "data", "HOME": "/home/u"},
			"/home/u/.local/share/frostread", false},
		{"no HOME", map[string]string{}, "", true},
	} {
		got, err := DesktopStateDir(env(c.env))
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s: DesktopStateDir = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestDesktopConfigDirAndPath(t *testing.T) {
	e := env(map[string]string{"XDG_CONFIG_HOME": "/config", "HOME": "/home/u"})
	if dir, err := DesktopConfigDir(e); err != nil || dir != "/config/frostread" {
		t.Fatalf("DesktopConfigDir = %q, %v", dir, err)
	}
	if path, err := DesktopConfigPath(e); err != nil || path != "/config/frostread/frostread.json" {
		t.Fatalf("DesktopConfigPath = %q, %v", path, err)
	}
	if mode, err := ModePath(e); err != nil || mode != "/config/frostread/app.json" {
		t.Fatalf("ModePath = %q, %v", mode, err)
	}
	// READ_CONFIG wins, wherever it points.
	e = env(map[string]string{"READ_CONFIG": "/etc/frostread/other.json", "HOME": "/home/u"})
	if path, err := DesktopConfigPath(e); err != nil || path != "/etc/frostread/other.json" {
		t.Fatalf("DesktopConfigPath with READ_CONFIG = %q, %v", path, err)
	}
	if mode, err := ModePath(e); err != nil || mode != "/home/u/.config/frostread/app.json" {
		t.Fatalf("ModePath with READ_CONFIG = %q, %v", mode, err)
	}
}

// The mode file: no file is local, a lodge member is lodge, and a file that
// does not parse is an error.
func TestReadMode(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct {
		name, path string
		want       string
		bad        bool
	}{
		{"no file", filepath.Join(dir, "missing.json"), "", false},
		{"no lodge", write("empty.json", `{}`), "", false},
		{"an empty lodge", write("blank.json", `{"lodge":""}`), "", false},
		{"lodge", write("lodge.json", `{"lodge":"https://lodge.example.ts.net"}`),
			"https://lodge.example.ts.net", false},
		{"another member", write("more.json", `{"lodge":"https://lodge.example.ts.net","theme":"dark"}`),
			"https://lodge.example.ts.net", false},
		{"not JSON", write("broken.json", `{"lodge":`), "", true},
	} {
		m, err := ReadMode(c.path)
		if (err != nil) != c.bad || m.Lodge != c.want {
			t.Errorf("%s: ReadMode = %+v, %v; want lodge %q", c.name, m, err, c.want)
		}
	}
}

// The socket: READ_SOCKET wins, the Flatpak's own directory is one all of
// its sandboxes share, and a missing runtime directory says what to do.
func TestDesktopSocket(t *testing.T) {
	run := "/run/user/1000"
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
		bad  bool
	}{
		{"READ_SOCKET", map[string]string{"READ_SOCKET": "/s/readd.sock", "XDG_RUNTIME_DIR": run}, "/s/readd.sock", false},
		{"the Flatpak", map[string]string{"XDG_RUNTIME_DIR": run, "FLATPAK_ID": AppID},
			"/run/user/1000/app/org.frostyard.Frostread/frostread/readd.sock", false},
		{"another Flatpak", map[string]string{"XDG_RUNTIME_DIR": run, "FLATPAK_ID": "org.example.Other"},
			"/run/user/1000/frostread/readd.sock", false},
		{"no Flatpak", map[string]string{"XDG_RUNTIME_DIR": run}, "/run/user/1000/frostread/readd.sock", false},
		{"no runtime directory", map[string]string{}, "", true},
		{"a relative runtime directory", map[string]string{"XDG_RUNTIME_DIR": "run"}, "", true},
	} {
		got, err := DesktopSocket(env(c.env))
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s: DesktopSocket = %q, %v; want %q", c.name, got, err, c.want)
		}
		if err != nil && !strings.Contains(err.Error(), "READ_SOCKET") {
			t.Errorf("%s: the error does not say to set READ_SOCKET: %v", c.name, err)
		}
	}
}

// The socket's table is shared with the shell: web/src-tauri/src/paths.rs
// reads this file with include_str!, and its own test runs it, so the two
// derivations of one path cannot drift.
func TestDesktopSocketTable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "desktop-socket.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string            `json:"name"`
		Env    map[string]string `json:"env"`
		Socket string            `json:"socket"`
		Error  bool              `json:"error"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatalf("testdata/desktop-socket.json: %v", err)
	}
	if len(cases) < 8 {
		t.Fatalf("testdata/desktop-socket.json holds %d cases; want at least 8", len(cases))
	}
	for _, c := range cases {
		got, err := DesktopSocket(env(c.Env))
		switch {
		case c.Error && err == nil:
			t.Errorf("%s: DesktopSocket = %q; want an error", c.Name, got)
		case c.Error:
			if !strings.Contains(err.Error(), "READ_SOCKET") {
				t.Errorf("%s: the error does not say to set READ_SOCKET: %v", c.Name, err)
			}
		case err != nil || got != c.Socket:
			t.Errorf("%s: DesktopSocket = %q, %v; want %q", c.Name, got, err, c.Socket)
		}
	}
}

// The build is the Flatpak instance's app-commit, and "" off the Flatpak.
func TestBuild(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	info := write("flatpak-info", `[Application]
name=org.frostyard.Frostread
runtime=org.gnome.Platform/x86_64/51

[Instance]
app-commit=0123456789abcdef0123456789abcdef01234567
app-path=/var/lib/flatpak/app/org.frostyard.Frostread
instance-path=/run/user/1000/.flatpak/1234
`)
	got, err := Build(info)
	if err != nil || got != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("Build = %q, %v", got, err)
	}
	if got, err := Build(filepath.Join(dir, "missing")); err != nil || got != "" {
		t.Fatalf("Build of no file = %q, %v; want none", got, err)
	}
	// The key is the [Instance] group's: another group's does not count.
	other := write("other-group", "[Application]\napp-commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	if got, err := Build(other); err != nil || got != "" {
		t.Fatalf("Build of another group = %q, %v; want none", got, err)
	}
}
