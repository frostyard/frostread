package fetch

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var devBuild struct {
	once               sync.Once
	release, dev       string
	releaseSym, devSym string
	err                error
}

// TestReleaseHasNoDevFetch proves the development escape is in no release
// build: the release binary has no devFetchHosts symbol, and the dev build
// does (docs/specs/api.md, Development-only). It builds cmd/readd twice,
// as lodge's own development-build test does.
func TestReleaseHasNoDevFetch(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	devBuild.once.Do(func() {
		dir, err := filepath.Abs(t.TempDir())
		if err != nil {
			devBuild.err = err
			return
		}
		devBuild.release = filepath.Join(dir, "readd")
		devBuild.dev = filepath.Join(dir, "readd-dev")
		root := filepath.Join("..", "..")
		for _, b := range []struct {
			out  string
			tags string
		}{{devBuild.release, ""}, {devBuild.dev, "devfetch"}} {
			args := []string{"build", "-trimpath", "-o", b.out}
			if b.tags != "" {
				args = append(args, "-tags", b.tags)
			}
			cmd := exec.Command(gobin, append(args, "./cmd/readd")...)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				devBuild.err = err
				devBuild.releaseSym = string(out)
				return
			}
		}
		devBuild.releaseSym, devBuild.err = nm(gobin, devBuild.release)
		if devBuild.err != nil {
			return
		}
		devBuild.devSym, devBuild.err = nm(gobin, devBuild.dev)
	})
	if devBuild.err != nil {
		t.Fatalf("build: %v\n%s", devBuild.err, devBuild.releaseSym)
	}
	if strings.Contains(devBuild.releaseSym, "devFetchHosts") {
		t.Fatal("a release binary carries the development fetch escape")
	}
	if !strings.Contains(devBuild.devSym, "devFetchHosts") {
		t.Fatal("the devfetch build has no development fetch escape, so this test proves nothing")
	}
}

// nm returns a binary's symbol table, through go tool nm.
func nm(gobin, binary string) (string, error) {
	out, err := exec.Command(gobin, "tool", "nm", binary).CombinedOutput()
	return string(out), err
}
