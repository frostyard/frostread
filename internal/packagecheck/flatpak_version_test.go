package packagecheck

// The Flatpak's version stamping (docs/specs/problem-records.md, Version
// stamping): a release tag at HEAD gives readd and the shell its version,
// and any other build stays unstamped, so its problem recorders are inert.

import (
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// flatpakModules reads the manifest's modules.
func flatpakModules(t *testing.T) flatpakManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "flatpak", "org.frostyard.Frostread.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m flatpakManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Modules) != 2 {
		t.Fatalf("modules = %d, want readd then frostread", len(m.Modules))
	}
	return m
}

// readd's version comes from release-version, and the shell's from readd.
func TestFlatpakStampsTheReleaseVersion(t *testing.T) {
	m := flatpakModules(t)
	var build string
	for _, c := range m.Modules[0].BuildCommands {
		if strings.Contains(c, "go build") {
			build = c
		}
	}
	if strings.Contains(build, "main.version=flatpak") || !strings.Contains(build, "packaging/flatpak/release-version") ||
		!strings.Contains(build, "main.version=${v:-dev}") {
		t.Errorf("readd's build = %q, want main.version from packaging/flatpak/release-version, else dev", build)
	}
	shell := m.Modules[1].BuildCommands
	if !slices.Contains(shell, `export PATH="$PWD/pnpm-bin:$PATH" && sh flatpak/tauri-build.sh`) {
		t.Errorf("frostread module build-commands = %v, want the shell built by flatpak/tauri-build.sh", shell)
	}
	for _, c := range shell {
		if strings.Contains(c, "pnpm tauri build") {
			t.Errorf("frostread module builds the shell without its version: %q", c)
		}
	}
	shipped := false
	for _, source := range m.Modules[1].Sources {
		if v, ok := source.(map[string]any); ok && v["type"] == "file" && v["path"] == "tauri-build.sh" && v["dest"] == "flatpak" {
			shipped = true
		}
	}
	if !shipped {
		t.Error("the frostread module does not copy tauri-build.sh into flatpak/")
	}
}

// writeScript writes an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// tauri-build.sh passes a release version to tauri build, and nothing else.
func TestTauriBuildPassesTheReleaseVersion(t *testing.T) {
	script, err := filepath.Abs("../../packaging/flatpak/tauri-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version string
		want    []string
	}{
		{"1.2.3", []string{"tauri", "build", "--config", `{"version":"1.2.3"}`}},
		{"1.2.3-rc.1", []string{"tauri", "build", "--config", `{"version":"1.2.3-rc.1"}`}},
		{"dev", []string{"tauri", "build"}},
		{"v0.4.0-3-gabc1234", []string{"tauri", "build"}},
		{"1.2", []string{"tauri", "build"}},
	} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(filepath.Join(dir, "web"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			args := filepath.Join(dir, "args")
			readd := filepath.Join(bin, "readd")
			writeScript(t, readd, `[ "$1" = -version ] || exit 2`+"\necho '"+tc.version+"'\n")
			// The fake pnpm writes its folder and each argument on a line.
			writeScript(t, filepath.Join(bin, "pnpm"), `pwd > "$ARGS"; for a in "$@"; do printf '%s\n' "$a" >> "$ARGS"; done`+"\n")
			cmd := exec.CommandContext(t.Context(), "sh", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "READD="+readd, "ARGS="+args, "PATH="+bin+":"+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tauri-build.sh: %v\n%s", err, out)
			}
			data, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if filepath.Base(got[0]) != "web" {
				t.Errorf("pnpm ran in %s, want web/", got[0])
			}
			if !slices.Equal(got[1:], tc.want) {
				t.Errorf("pnpm %q, want %q", got[1:], tc.want)
			}
		})
	}
}

// release-version.sh prints the version of an exact release tag at HEAD,
// and nothing for any other commit.
func TestReleaseVersionScript(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	script, err := filepath.Abs("../../scripts/release-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "HOME="+repo,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(repo),
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com")
	sh := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Dir, cmd.Env = repo, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		return string(out)
	}
	git := func(args ...string) { t.Helper(); sh("git", args...) }
	commit := func() { t.Helper(); git("commit", "-q", "--allow-empty", "-m", "a commit") }

	// No repository at all: nothing, and exit 0.
	if got := sh("sh", script); got != "" {
		t.Errorf("outside a repository = %q, want nothing", got)
	}
	git("init", "-q")
	commit()
	if got := sh("sh", script); got != "" {
		t.Errorf("an untagged commit = %q, want nothing", got)
	}
	for _, tc := range []struct{ tag, want string }{
		{"v1.2.3", "1.2.3\n"},
		{"v1.2.3-rc.2", "1.2.3\n"},
		{"v1.2", ""},
		{"1.2.4", ""},
		{"v1.2.5-beta.1", ""},
	} {
		commit()
		git("tag", tc.tag)
		if got := sh("sh", script); got != tc.want {
			t.Errorf("at tag %s = %q, want %q", tc.tag, got, tc.want)
		}
	}
	commit()
	if got := sh("sh", script); got != "" {
		t.Errorf("an untagged commit after the tags = %q, want nothing", got)
	}
}

func TestStampMetainfoScript(t *testing.T) {
	script, err := filepath.Abs("../../packaging/flatpak/stamp-metainfo.sh")
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(filepath.Join("..", "..", "packaging", "flatpak", "org.frostyard.Frostread.metainfo.xml"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("inserts first", func(t *testing.T) {
		metainfo := filepath.Join(t.TempDir(), "metainfo.xml")
		if err := os.WriteFile(metainfo, base, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "sh", script, "1.2.3", metainfo)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("stamp-metainfo.sh: %v\n%s", err, out)
		}
		got, err := os.ReadFile(metainfo)
		if err != nil {
			t.Fatal(err)
		}
		text := string(got)
		releases := strings.Index(text, "<releases>")
		added := strings.Index(text, `version="1.2.3"`)
		old := strings.Index(text, `version="0.0.0"`)
		if added < 0 || releases < 0 || added < releases || (old >= 0 && added > old) {
			t.Errorf("new release is not first under <releases>: %s", text)
		}
	})

	for _, tc := range []struct {
		name, version string
	}{
		{name: "refuses repeated version", version: "0.0.0"},
		{name: "refuses bad version", version: "1.2.3-rc.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metainfo := filepath.Join(t.TempDir(), "metainfo.xml")
			if err := os.WriteFile(metainfo, base, 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(metainfo)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "sh", script, tc.version, metainfo)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("stamp-metainfo.sh accepted %s: %s", tc.version, out)
			}
			after, err := os.ReadFile(metainfo)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(before, after) {
				t.Errorf("refused stamp changed the metainfo")
			}
		})
	}
}
