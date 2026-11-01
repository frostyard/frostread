// This file is the desktop mode's paths, its mode file and its build
// (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md,
// readd's desktop mode; docs/specs/api.md, Desktop mode).

package config

import (
	"bufio"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AppID is the reverse-DNS application ID (frostyard/core ADR-0016).
const AppID = "org.frostyard.Frostread"

// ModeFileName is the mode file's name in the configuration's directory:
// {"lodge": "https://…"} is lodge mode, and no lodge member is local mode.
const ModeFileName = "app.json"

// DesktopStateDir is readd's state directory in desktop mode:
// $XDG_DATA_HOME/frostread, else $HOME/.local/share/frostread. Only an
// absolute XDG value counts.
func DesktopStateDir(getenv func(string) string) (string, error) {
	dir, err := xdg(getenv, "XDG_DATA_HOME", ".local/share")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "frostread"), nil
}

// DesktopConfigDir is the configuration's directory in desktop mode:
// $XDG_CONFIG_HOME/frostread, else $HOME/.config/frostread. The mode file
// is beside the configuration.
func DesktopConfigDir(getenv func(string) string) (string, error) {
	dir, err := xdg(getenv, "XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "frostread"), nil
}

// DesktopConfigPath is readd's configuration in desktop mode: READ_CONFIG
// when it is set, else frostread.json in DesktopConfigDir.
func DesktopConfigPath(getenv func(string) string) (string, error) {
	if v := getenv("READ_CONFIG"); v != "" {
		return v, nil
	}
	dir, err := DesktopConfigDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "frostread.json"), nil
}

// ModePath is the mode file: app.json in DesktopConfigDir.
func ModePath(getenv func(string) string) (string, error) {
	dir, err := DesktopConfigDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ModeFileName), nil
}

// Mode is what app.json says. Only the app writes it; readd reads it, under
// its lock, before it opens the database.
type Mode struct {
	// Lodge is lodge's address, such as https://lodge.example.ts.net; ""
	// is local mode.
	Lodge string `json:"lodge,omitzero"`
}

// ReadMode reads the mode file at path. A missing file, an absent lodge
// member and an empty one are local mode, and a member this does not know
// is ignored: the app may keep more in the file. A file that does not
// parse is an error, which stops readd before it opens the database.
func ReadMode(path string) (Mode, error) {
	var m Mode
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Mode{}, nil
	case err != nil:
		return Mode{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return Mode{}, fmt.Errorf("%s: %w", path, err)
	}
	m.Lodge = strings.TrimSpace(m.Lodge)
	return m, nil
}

// DesktopSocket is readd's private socket in desktop mode: READ_SOCKET when
// it is set, else $XDG_RUNTIME_DIR/frostread/readd.sock, or
// $XDG_RUNTIME_DIR/app/org.frostyard.Frostread/frostread/readd.sock when
// FLATPAK_ID is AppID. Flatpak shares $XDG_RUNTIME_DIR/app/<id> between
// every sandbox of the app, and the rest of $XDG_RUNTIME_DIR is private to
// each one, so the window and readd meet only there
// (ADR-0008, readd's desktop mode). web/src-tauri/src/paths.rs must stay in
// step with this function.
func DesktopSocket(getenv func(string) string) (string, error) {
	if s := getenv("READ_SOCKET"); s != "" {
		return s, nil
	}
	run := getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(run) {
		return "", errors.New("XDG_RUNTIME_DIR is not set; set READ_SOCKET")
	}
	if getenv("FLATPAK_ID") == AppID {
		run = filepath.Join(run, "app", AppID)
	}
	return filepath.Join(run, "frostread", "readd.sock"), nil
}

// Build reads a Flatpak instance's build: app-commit in the [Instance]
// group of the .flatpak-info file at path. "" is a machine that is not the
// Flatpak, and no error; the launcher compares the build of the readd that
// runs with the latest deployment's, to replace it forward only
// (ADR-0008, Which engine runs in each mode).
func Build(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("build: %w", err)
	}
	defer func() { _ = f.Close() }()
	group := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if name, ok := strings.CutPrefix(line, "["); ok {
			group, _, _ = strings.Cut(name, "]")
			continue
		}
		if group != "Instance" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "app-commit" {
			continue
		}
		return strings.TrimSpace(value), nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("build: %w", err)
	}
	return "", nil
}

// xdg is an XDG base directory: the variable when it is absolute, else
// fallback under HOME.
func xdg(getenv func(string) string, name, fallback string) (string, error) {
	if v := getenv(name); filepath.IsAbs(v) {
		return v, nil
	}
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return "", errors.New("HOME is not set")
	}
	return filepath.Join(home, fallback), nil
}
