package packagecheck

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/frostyard/lodge/kit/manifest"
)

const (
	user     = "frostread"
	appID    = "read"
	version  = "v1.2.3-4-gabc"
	unitDir  = "usr/lib/systemd/system"
	binaries = "readd readctl"
)

// stage runs packaging/lodge/stage.sh, as make lodge-package does.
func stage(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("lodge packages for Linux")
	}
	script, err := filepath.Abs("../../packaging/lodge/stage.sh")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", append([]string{script}, args...)...).CombinedOutput()
	return string(out), err
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeBuild is a build directory with stand-in binaries and web build.
func fakeBuild(t *testing.T) string {
	t.Helper()
	build := t.TempDir()
	for _, b := range strings.Fields(binaries) {
		write(t, filepath.Join(build, b), b)
	}
	write(t, filepath.Join(build, "web/index.html"), "<!doctype html>")
	write(t, filepath.Join(build, "web/icon.svg"), "<svg/>")
	return build
}

// staged stages into an empty root and returns it.
func staged(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if out, err := stage(t, root, version, fakeBuild(t)); err != nil {
		t.Fatalf("stage.sh: %v\n%s", err, out)
	}
	return root
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lines(s string) []string { return strings.Split(s, "\n") }

func TestStagesOnlyUnderUsr(t *testing.T) {
	root := staged(t)
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel != "." && rel != "usr" && !strings.HasPrefix(rel, "usr"+string(filepath.Separator)) {
			t.Errorf("%s is staged outside usr/", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStagesTheFiles(t *testing.T) {
	root := staged(t)
	for _, b := range strings.Fields(binaries) {
		fi, err := os.Stat(filepath.Join(root, "usr/bin", b))
		if err != nil {
			t.Errorf("usr/bin/%s not staged: %v", b, err)
		} else if fi.Mode().Perm() != 0o755 {
			t.Errorf("usr/bin/%s mode %v, want 0755", b, fi.Mode().Perm())
		}
	}
	for _, p := range []string{unitDir + "/frostread.socket", unitDir + "/frostread.service",
		"usr/lib/sysusers.d/frostread.conf", "usr/share/lodge/apps/read/web/index.html",
		"usr/share/lodge/apps/read/web/icon.svg"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s not staged: %v", p, err)
		}
	}
	m, err := manifest.Load(filepath.Join(root, "usr/share/lodge/apps", appID))
	if err != nil {
		t.Fatalf("staged manifest: %v", err)
	}
	if m.Version != version || m.ID != appID {
		t.Errorf("staged manifest %+v, want %s at %s", m, appID, version)
	}
}

func units(t *testing.T, root string) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, unitDir, "*"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no staged units: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		out[filepath.Base(p)] = read(t, p)
	}
	return out
}

func TestUnitRules(t *testing.T) {
	root := staged(t)
	for name, body := range units(t, root) {
		for _, l := range lines(body) {
			switch {
			case strings.HasPrefix(l, "RequiredBy="):
				t.Errorf("%s has %q (core ADR-0030)", name, l)
			case strings.HasPrefix(l, "Condition"):
				t.Errorf("%s has %q (snosi#1100)", name, l)
			case strings.HasSuffix(name, ".socket") && strings.HasPrefix(l, "DefaultDependencies="):
				t.Errorf("%s has %q; a socket keeps its default dependencies", name, l)
			}
		}
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		found := false
		for _, l := range lines(body) {
			exe, ok := strings.CutPrefix(l, "ExecStart=")
			if !ok {
				continue
			}
			found = true
			bin, _, _ := strings.Cut(exe, " ")
			if !strings.HasPrefix(bin, "/usr/bin/") {
				t.Errorf("%s: ExecStart=%s is not under /usr/bin", name, exe)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, bin)); err != nil {
				t.Errorf("%s: ExecStart binary %s is not staged: %v", name, bin, err)
			}
		}
		if !found {
			t.Errorf("%s has no ExecStart=", name)
		}
	}
}

func TestSysusers(t *testing.T) {
	root := staged(t)
	body := read(t, filepath.Join(root, "usr/lib/sysusers.d/frostread.conf"))
	var have []string
	for _, l := range lines(body) {
		have = append(have, strings.Join(strings.Fields(l), " "))
	}
	has := func(prefix string) bool {
		for _, l := range have {
			if l == prefix || strings.HasPrefix(l, prefix+" ") {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"g lodge -", "u " + user, "m " + user + " lodge-apps", "m " + user + " lodge-agents"} {
		if !has(want) {
			t.Errorf("sysusers.d entry lacks %q", want)
		}
	}
	for _, l := range have {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "m" && f[1] == user && f[2] == "lodge" {
			t.Errorf("sysusers.d entry has %q; the user is never in group lodge", l)
		}
	}
}

var execStart = regexp.MustCompile(`^ExecStart=.*$`)

func TestUnitsVerify(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("no systemd-analyze")
	}
	root := staged(t)
	dir := t.TempDir()
	var names []string
	for name, body := range units(t, root) {
		ls := lines(body)
		for i, l := range ls {
			// verify wants each ExecStart to exist.
			if execStart.MatchString(l) {
				ls[i] = "ExecStart=/bin/sh -c true"
			}
		}
		write(t, filepath.Join(dir, name), strings.Join(ls, "\n"))
		names = append(names, "./"+name)
	}
	cmd := exec.Command(analyze, append([]string{"verify"}, names...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("systemd-analyze verify: %v\n%s", err, out)
	}
}

// TestStagingKeepsASharedRoot stages into a lodge root that other apps and
// lodge itself use: only read's files change.
func TestStagingKeepsASharedRoot(t *testing.T) {
	root := t.TempDir()
	others := map[string]string{
		"usr/share/lodge/apps/notes/app.json":    `{"id": "notes"}`,
		"usr/share/lodge/apps/notes/web/a.js":    "notes",
		"var/lib/lodge/state.json":               "lodge's state",
		"usr/bin/lodge":                          "lodge",
		"usr/lib/systemd/system/lodge.service":   "[Unit]",
		"var/lib/frostread/frostread.db":         "read's data",
		"usr/share/lodge/apps/read/web/old.js":   "a stale bundle file",
		"usr/share/lodge/apps/read/app.json":     `{"version": "old"}`,
		"usr/share/lodge/apps/readx/app.json":    "another app",
		"usr/share/lodge/apps/read.bak/app.json": "a backup",
	}
	for p, c := range others {
		write(t, filepath.Join(root, p), c)
	}
	if out, err := stage(t, root, version, fakeBuild(t)); err != nil {
		t.Fatalf("stage.sh: %v\n%s", err, out)
	}
	for p, c := range others {
		if strings.HasPrefix(p, "usr/share/lodge/apps/read/") {
			continue
		}
		if got, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(got) != c {
			t.Errorf("%s = %q, %v after staging; want %q kept", p, got, err, c)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "usr/share/lodge/apps/read/web/old.js")); !os.IsNotExist(err) {
		t.Errorf("a stale file of read's web build survived: %v", err)
	}
}

func TestStagingRefusesAnEmptyRootOrSlash(t *testing.T) {
	build := fakeBuild(t)
	for _, dest := range []string{"", "/", "//", "/.", "/usr/.."} {
		if out, err := stage(t, dest, "v1", build); err == nil {
			t.Errorf("stage.sh %q succeeded:\n%s", dest, out)
		}
	}
	if out, err := stage(t, t.TempDir(), `v1"; rm -rf /tmp/x; "`, build); err == nil {
		t.Errorf("stage.sh with a version a manifest cannot carry succeeded:\n%s", out)
	}
}
