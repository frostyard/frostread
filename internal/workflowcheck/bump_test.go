//go:build linux

package workflowcheck

// The bump target's candidate and plain release paths are exercised in
// temporary local Git repositories. These tests use only a bare origin, a
// stub make, and a stub svu: they push no network data and run no real gate.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type bumpRepo struct {
	root, origin, clone, stubDir, stubMake, stubSVU, makeLog string
	env                                                      []string
}

func bumpEnv(root string) []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+root,
		"GIT_AUTHOR_NAME=Alice",
		"GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=Alice",
		"GIT_COMMITTER_EMAIL=alice@example.com",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CEILING_DIRECTORIES="+root,
	)
}

func bumpCommand(t *testing.T, env []string, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newBumpRepo(t *testing.T) bumpRepo {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")
	stubDir := filepath.Join(root, "bin")
	makeLog := filepath.Join(root, "make.log")
	stubMake := filepath.Join(stubDir, "make")
	stubSVU := filepath.Join(stubDir, "svu")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := bumpEnv(root)

	bumpCommand(t, env, root, "git", "init", "-q", "--bare", origin)
	bumpCommand(t, env, root, "git", "clone", "-q", origin, clone)
	bumpCommand(t, env, clone, "git", "branch", "-M", "main")

	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "Makefile"), makefile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "mise.toml"), []byte("svu = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	makeStub := "#!/bin/sh\n{ printf '%s' \"$(basename \"$0\")\"; [ 0 -eq $# ] || printf ' %s' \"$@\"; printf '\n'; } >>\"$STUB_MAKE_LOG\"\nexit \"${STUB_MAKE_EXIT:-0}\"\n"
	if err := os.WriteFile(stubMake, []byte(makeStub), 0o755); err != nil {
		t.Fatal(err)
	}
	svuStub := "#!/bin/sh\nif [ \"$1\" = next ]; then printf '%s\n' \"${STUB_SVU_NEXT:-v1.0.0}\"; elif [ \"$1\" = --version ]; then echo 'GitVersion: 1.0.0'; else exit 2; fi\n"
	if err := os.WriteFile(stubSVU, []byte(svuStub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(makeLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	bumpCommand(t, env, clone, "git", "add", "Makefile", "mise.toml")
	bumpCommand(t, env, clone, "git", "commit", "-q", "-m", "add bump recipe")
	bumpCommand(t, env, clone, "git", "push", "-q", "-u", "origin", "main")

	return bumpRepo{root: root, origin: origin, clone: clone, stubDir: stubDir, stubMake: stubMake, stubSVU: stubSVU, makeLog: makeLog, env: env}
}

func (r bumpRepo) commitA(t *testing.T) string {
	t.Helper()
	return bumpCommand(t, r.env, r.clone, "git", "rev-parse", "HEAD")
}

func (r bumpRepo) commitB(t *testing.T) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.clone, "commit-b.txt"), []byte("B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpCommand(t, r.env, r.clone, "git", "add", "commit-b.txt")
	bumpCommand(t, r.env, r.clone, "git", "commit", "-q", "-m", "commit B")
	bumpCommand(t, r.env, r.clone, "git", "push", "-q", "origin", "main")
	return bumpCommand(t, r.env, r.clone, "git", "rev-parse", "HEAD")
}

func (r bumpRepo) addTag(t *testing.T, tag, commit string, candidate bool) {
	t.Helper()
	if candidate {
		bumpCommand(t, r.env, r.clone, "git", "tag", "-a", tag, commit, "-m", "candidate "+tag)
	} else {
		bumpCommand(t, r.env, r.clone, "git", "tag", "-a", tag, commit, "-m", "Version "+tag)
	}
	bumpCommand(t, r.env, r.clone, "git", "push", "-q", "origin", "refs/tags/"+tag)
}

func (r bumpRepo) runMake(t *testing.T, absMake, candidate, svuNext, stubExit string, dryRun bool) (string, error) {
	t.Helper()
	args := []string{"-f", "Makefile", "bump"}
	if dryRun {
		args = append([]string{"-n"}, args...)
	}
	if candidate != "" {
		args = append(args, "CANDIDATE="+candidate)
	}
	args = append(args, "SVU="+r.stubSVU)
	cmd := exec.CommandContext(t.Context(), absMake, args...)
	cmd.Dir = r.clone
	cmd.Env = append(r.env,
		"PATH="+r.stubDir+":"+os.Getenv("PATH"),
		"STUB_MAKE_LOG="+r.makeLog,
		"STUB_MAKE_EXIT="+stubExit,
		"STUB_SVU_NEXT="+svuNext,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r bumpRepo) originTags(t *testing.T) []string {
	t.Helper()
	out := bumpCommand(t, r.env, r.root, "git", "-C", r.origin, "tag", "--list")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (r bumpRepo) originTagCommit(t *testing.T, tag string) string {
	t.Helper()
	return bumpCommand(t, r.env, r.root, "git", "-C", r.origin, "rev-parse", "refs/tags/"+tag+"^{commit}")
}

func (r bumpRepo) originTagType(t *testing.T, tag string) string {
	t.Helper()
	return bumpCommand(t, r.env, r.root, "git", "-C", r.origin, "cat-file", "-t", "refs/tags/"+tag)
}

func (r bumpRepo) makeLogLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(r.makeLog)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (r bumpRepo) worktrees(t *testing.T) string {
	t.Helper()
	return bumpCommand(t, r.env, r.root, "git", "-C", r.clone, "worktree", "list", "--porcelain")
}

func TestBump(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is missing")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is missing")
	}

	t.Run("candidate success", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		b := r.commitB(t)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", false)
		if err != nil {
			t.Fatalf("bump failed: %v\n%s", err, out)
		}
		if got := r.originTagCommit(t, "v1.2.0"); got != a {
			t.Fatalf("v1.2.0 points at %s, want commit A %s (commit B is %s)", got, a, b)
		}
		if got := r.originTagType(t, "v1.2.0"); got != "tag" {
			t.Fatalf("v1.2.0 type = %q, want annotated tag", got)
		}
		logLines := r.makeLogLines(t)
		foundVerify := false
		for _, line := range logLines {
			if strings.Contains(line, "-C ") && strings.Contains(line, "--no-print-directory verify") {
				foundVerify = true
			}
		}
		if !foundVerify {
			t.Fatalf("make log %v has no temporary verify worktree", logLines)
		}
		if got := r.worktrees(t); strings.Count(got, "worktree ") != 1 {
			t.Fatalf("worktree list is not clean:\n%s", got)
		}
	})

	t.Run("failed verification", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		r.commitB(t)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "1", false)
		if err == nil {
			t.Fatalf("failed verification succeeded:\n%s", out)
		}
		if got := r.makeLogLines(t); len(got) != 1 || !strings.HasSuffix(got[0], "--no-print-directory verify") {
			t.Fatalf("make log = %v, want 1 failed verify", got)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("failed verification pushed v1.2.0")
		}
		local := bumpCommand(t, r.env, r.clone, "git", "tag", "--list", "v1.2.0")
		if local != "" {
			t.Fatalf("failed verification left local v1.2.0: %q", local)
		}
	})

	for _, tc := range []struct {
		name, candidate string
		releaseTag      string
		want            string
	}{
		{name: "candidate tag missing", candidate: "v1.2.0-rc.1", want: "the candidate tag v1.2.0-rc.1 does not exist"},
		{name: "candidate is not rc", candidate: "v1.2.0", want: "make bump needs CANDIDATE=vX.Y.Z-rc.N"},
		{name: "release tag exists", candidate: "v1.2.0-rc.1", releaseTag: "v1.2.0", want: "the tag v1.2.0 already exists"},
		{name: "release is not newer", candidate: "v1.2.0-rc.1", releaseTag: "v1.3.0", want: "v1.2.0 is not newer than the newest release tag v1.3.0"},
		{name: "injection is refused", candidate: "v1.2.0-rc.1'`touch PWNED`'", want: "make bump needs CANDIDATE=vX.Y.Z-rc.N"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBumpRepo(t)
			a := r.commitA(t)
			if tc.name != "candidate tag missing" && tc.name != "candidate is not rc" && tc.name != "injection is refused" {
				r.addTag(t, "v1.2.0-rc.1", a, true)
			}
			if tc.releaseTag != "" {
				r.addTag(t, tc.releaseTag, a, false)
			}
			out, err := r.runMake(t, makePath, tc.candidate, "", "", false)
			if err == nil {
				t.Fatalf("refusal succeeded:\n%s", out)
			}
			if tc.name == "injection is refused" {
				for _, path := range []string{filepath.Join(r.clone, "PWNED"), filepath.Join(r.root, "PWNED")} {
					if _, err := os.Stat(path); err == nil {
						t.Fatalf("injection created %s", path)
					}
				}
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("refusal output lacks %q:\n%s", tc.want, out)
			}
			if tc.releaseTag != "v1.2.0" && slices.Contains(r.originTags(t), "v1.2.0") {
				t.Fatal("refusal pushed v1.2.0")
			}
			if got := r.makeLogLines(t); len(got) != 0 {
				t.Fatalf("refusal ran make %v before its checks", got)
			}
		})
	}

	t.Run("plain candidate exists", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		out, err := r.runMake(t, makePath, "", "v1.2.0", "", false)
		if err == nil {
			t.Fatalf("plain bump succeeded:\n%s", out)
		}
		if !strings.Contains(out, "a candidate of v1.2.0 exists") {
			t.Fatalf("plain bump output lacks candidate refusal:\n%s", out)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("plain bump pushed v1.2.0")
		}
	})

	t.Run("plain success", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		out, err := r.runMake(t, makePath, "", "v1.4.0", "", false)
		if err != nil {
			t.Fatalf("plain bump failed: %v\n%s", err, out)
		}
		if got := r.originTagCommit(t, "v1.4.0"); got != a {
			t.Fatalf("v1.4.0 points at %s, want HEAD %s", got, a)
		}
		if got := r.originTagType(t, "v1.4.0"); got != "tag" {
			t.Fatalf("v1.4.0 type = %q, want annotated tag", got)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", true)
		if err != nil {
			t.Fatalf("make -n failed: %v\n%s", err, out)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("make -n pushed v1.2.0")
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("make -n ran stub make: %v", got)
		}
	})
}
