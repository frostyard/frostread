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
	root, origin, clone, stubDir, stubMake, stubSVU, stubGH, makeLog, ghLog string
	env                                                                     []string
}

// bumpEnv is the fixtures' environment: the caller's, without the make
// variables that a real make bump CANDIDATE=... passes down to its make
// verify (and so to these tests), plus an isolated Git.
func bumpEnv(root string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "CANDIDATE", "MAKEFLAGS", "MAKEOVERRIDES", "MFLAGS", "MAKELEVEL":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
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
	ghLog := filepath.Join(root, "gh.log")
	stubMake := filepath.Join(stubDir, "make")
	stubSVU := filepath.Join(stubDir, "svu")
	stubGH := filepath.Join(stubDir, "gh")
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
	if err := os.WriteFile(ghLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ghStub := "#!/bin/sh\n{ printf '%s' gh; [ 0 -eq $# ] || printf ' %s' \"$@\"; printf '\\n'; } >>\"$STUB_GH_LOG\"\nif [ \"${1:-}\" != run ] || [ \"${2:-}\" != list ]; then exit 0; fi\nrepo=0; workflow=0; event=0; status=0; branch=0\nprev=\nfor arg in \"$@\"; do\n  if [ \"$prev\" = --repo ] && [ \"$arg\" = frostyard/frostread ]; then repo=1; fi\n  if [ \"$prev\" = --workflow ] && [ \"$arg\" = flatpak.yml ]; then workflow=1; fi\n  if [ \"$prev\" = --event ] && [ \"$arg\" = push ]; then event=1; fi\n  if [ \"$prev\" = --status ] && [ \"$arg\" = success ]; then status=1; fi\n  if [ \"$prev\" = --branch ] && [ \"$arg\" = \"$CANDIDATE\" ]; then branch=1; fi\n  prev=$arg\ndone\nif [ $repo -eq 1 ] && [ $workflow -eq 1 ] && [ $event -eq 1 ] && [ $status -eq 1 ] && [ $branch -eq 1 ]; then\n  for sha in ${STUB_GH_RUNS:-}; do printf '%s\\n' \"$sha\"; done\nfi\n"
	if err := os.WriteFile(stubGH, []byte(ghStub), 0o755); err != nil {
		t.Fatal(err)
	}

	bumpCommand(t, env, clone, "git", "add", "Makefile", "mise.toml")
	bumpCommand(t, env, clone, "git", "commit", "-q", "-m", "add bump recipe")
	bumpCommand(t, env, clone, "git", "push", "-q", "-u", "origin", "main")

	return bumpRepo{root: root, origin: origin, clone: clone, stubDir: stubDir, stubMake: stubMake, stubSVU: stubSVU, stubGH: stubGH, makeLog: makeLog, ghLog: ghLog, env: env}
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

func (r bumpRepo) runMake(t *testing.T, absMake, candidate, svuNext, stubExit, stubRuns string, dryRun bool) (string, error) {
	t.Helper()
	args := []string{"-f", "Makefile", "bump"}
	if dryRun {
		args = append([]string{"-n"}, args...)
	}
	// Always set, also when empty: the plain path must stay plain.
	args = append(args, "CANDIDATE="+candidate)
	args = append(args, "SVU="+r.stubSVU)
	cmd := exec.CommandContext(t.Context(), absMake, args...)
	cmd.Dir = r.clone
	cmd.Env = append(r.env,
		"PATH="+r.stubDir+":"+os.Getenv("PATH"),
		"STUB_MAKE_LOG="+r.makeLog,
		"STUB_MAKE_EXIT="+stubExit,
		"STUB_SVU_NEXT="+svuNext,
		"CANDIDATE="+candidate,
		"STUB_GH_LOG="+r.ghLog,
		"STUB_GH_RUNS="+stubRuns,
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

func (r bumpRepo) ghLogLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(r.ghLog)
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
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", a, false)
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
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "1", a, false)
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
			out, err := r.runMake(t, makePath, tc.candidate, "", "", a, false)
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

	t.Run("no candidate is refused", func(t *testing.T) {
		r := newBumpRepo(t)
		out, err := r.runMake(t, makePath, "", "v1.4.0", "", "", false)
		if err == nil {
			t.Fatalf("bump without CANDIDATE succeeded:\n%s", out)
		}
		if !strings.Contains(out, "make bump needs CANDIDATE=vX.Y.Z-rc.N, the candidate that passed the gate") {
			t.Fatalf("bump without CANDIDATE lacks its refusal:\n%s", out)
		}
		if slices.Contains(r.originTags(t), "v1.4.0") {
			t.Fatal("bump without CANDIDATE pushed v1.4.0")
		}
		if local := bumpCommand(t, r.env, r.clone, "git", "tag", "--list", "v1.4.0"); local != "" {
			t.Fatalf("bump without CANDIDATE left v1.4.0 local: %q", local)
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("bump without CANDIDATE ran stub make: %v", got)
		}
		if got := r.ghLogLines(t); len(got) != 0 {
			t.Fatalf("bump without CANDIDATE ran gh: %v", got)
		}
	})

	// A real make bump CANDIDATE=... runs make verify, and so this test
	// isolates CANDIDATE and MAKEFLAGS: the fixture must not inherit the caller.
	t.Run("no candidate under a candidate release fixture", func(t *testing.T) {
		t.Setenv("CANDIDATE", "v9.9.9-rc.1")
		t.Setenv("MAKEFLAGS", "CANDIDATE=v9.9.9-rc.1")
		t.Setenv("MAKEOVERRIDES", "CANDIDATE=v9.9.9-rc.1")
		r := newBumpRepo(t)
		out, err := r.runMake(t, makePath, "", "v1.4.0", "", "", false)
		if err == nil {
			t.Fatalf("bump under inherited candidate fixtures succeeded:\n%s", out)
		}
		if !strings.Contains(out, "make bump needs CANDIDATE=vX.Y.Z-rc.N, the candidate that passed the gate") {
			t.Fatalf("isolated fixture lacks the no-candidate refusal:\n%s", out)
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("fixture isolation ran stub make: %v", got)
		}
		if got := r.ghLogLines(t); len(got) != 0 {
			t.Fatalf("fixture isolation ran gh: %v", got)
		}
	})

	t.Run("candidate not on origin", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		bumpCommand(t, r.env, r.clone, "git", "tag", "-a", "v1.2.0-rc.1", a, "-m", "candidate v1.2.0-rc.1")
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", a, false)
		if err == nil {
			t.Fatalf("local-only candidate succeeded:\n%s", out)
		}
		if !strings.Contains(out, "the candidate tag v1.2.0-rc.1 is not on origin") {
			t.Fatalf("candidate origin refusal lacks its message:\n%s", out)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("candidate not on origin pushed v1.2.0")
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("candidate not on origin ran make: %v", got)
		}
		if got := r.ghLogLines(t); len(got) != 0 {
			t.Fatalf("candidate not on origin ran gh: %v", got)
		}
	})

	t.Run("candidate not published", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", "", false)
		if err == nil {
			t.Fatalf("candidate with no successful workflow succeeded:\n%s", out)
		}
		want := "the candidate v1.2.0-rc.1 has no successful Flatpak workflow run on " + a + ": it was not published"
		if !strings.Contains(out, want) {
			t.Fatalf("unpublished candidate lacks %q:\n%s", want, out)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("unpublished candidate pushed v1.2.0")
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("unpublished candidate ran make: %v", got)
		}
	})

	t.Run("candidate published on another commit", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		other := strings.Repeat("3", 40)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", other, false)
		if err == nil {
			t.Fatalf("candidate published on another commit succeeded:\n%s", out)
		}
		want := "the candidate v1.2.0-rc.1 has no successful Flatpak workflow run on " + a + ": it was not published"
		if !strings.Contains(out, want) {
			t.Fatalf("another-commit publication lacks %q:\n%s", want, out)
		}
		if slices.Contains(r.originTags(t), "v1.2.0") {
			t.Fatal("candidate published on another commit pushed v1.2.0")
		}
		if got := r.makeLogLines(t); len(got) != 0 {
			t.Fatalf("candidate published on another commit ran make: %v", got)
		}
	})

	t.Run("candidate from the environment", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		cmd := exec.CommandContext(t.Context(), makePath, "-f", "Makefile", "bump", "SVU="+r.stubSVU)
		cmd.Dir = r.clone
		cmd.Env = append(r.env, "PATH="+r.stubDir+":"+os.Getenv("PATH"), "STUB_MAKE_LOG="+r.makeLog, "STUB_GH_LOG="+r.ghLog, "STUB_GH_RUNS="+a, "CANDIDATE=v1.2.0-rc.1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bump with CANDIDATE in the environment failed: %v\n%s", err, out)
		}
		if got := r.originTagCommit(t, "v1.2.0"); got != a {
			t.Fatalf("v1.2.0 points at %s, want the candidate's commit %s", got, a)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		r := newBumpRepo(t)
		a := r.commitA(t)
		r.addTag(t, "v1.2.0-rc.1", a, true)
		out, err := r.runMake(t, makePath, "v1.2.0-rc.1", "", "", a, true)
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
