//go:build linux

package workflowcheck

// The tests of flatpak.yml's publish job (plan 0002, Phase 9) are a dry run
// of it: they parse the job's steps, run them in order in a temporary work
// directory, and play skopeo and gh with a stub that logs each command. So
// they check which OCI tags a tag pushes, that a candidate's digest is
// attested, and that a release tag publishes nothing and moves no latest,
// without a registry, a token or a Flatpak build. promote_test.go does the
// same for promote.yml.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	publishImage  = "ghcr.io/frostyard/frostread"
	publishSHA    = "0123456789abcdef0123456789abcdef01234567"
	publishDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

const publishStubs = `#!/bin/bash
{ printf '%s' "$(basename "$0")"; [ $# -eq 0 ] || printf ' %s' "$@"; printf '\n'; } >>"$PUBLISH_LOG"
if [ "$(basename "$0")" = skopeo ]; then
	prev=
	for arg in "$@"; do
		if [ "$prev" = --digestfile ]; then printf '%s' "$PUBLISH_DIGEST" >"$arg"; fi
		prev=$arg
	done
fi
`

type publishStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Env  map[string]string `yaml:"env"`
	With map[string]string `yaml:"with"`
	Run  string            `yaml:"run"`
}

func (s publishStep) label() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Uses
}

func publishSteps(t *testing.T) []publishStep {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "flatpak.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []publishStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("flatpak.yml: %v", err)
	}
	steps := wf.Jobs["publish"].Steps
	if len(steps) == 0 {
		t.Fatal("flatpak.yml has no publish job")
	}
	return steps
}

var publishExpr = regexp.MustCompile(`\$\{\{[^}]*\}\}`)

type publishRun struct {
	t        *testing.T
	tag      string
	root     string
	work     string
	log      string
	outputs  map[string]map[string]string
	order    []string
	attested string
	steps    int
}

func newPublishRun(t *testing.T, tag string) *publishRun {
	t.Helper()
	root := t.TempDir()
	r := &publishRun{t: t, tag: tag, root: root, work: filepath.Join(root, "work"), log: filepath.Join(root, "log"), outputs: map[string]map[string]string{}}
	if err := os.MkdirAll(r.work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wfWriteFile(t, r.log, "")
	stub := filepath.Join(root, "bin", "stub")
	wfWriteFile(t, stub, publishStubs)
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skopeo", "gh"} {
		if err := os.Symlink("stub", filepath.Join(root, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *publishRun) subst(v string) string {
	r.t.Helper()
	var unknown string
	out := publishExpr.ReplaceAllStringFunc(v, func(e string) string {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(e, "${{"), "}}"))
		switch name {
		case "github.ref_name":
			return r.tag
		case "env.IMAGE":
			return publishImage
		case "steps.push.outputs.digest":
			return r.outputs["push"]["digest"]
		case "secrets.GITHUB_TOKEN":
			return "token"
		case "github.actor":
			return "actor"
		default:
			unknown = name
			return e
		}
	})
	if unknown != "" {
		r.t.Fatalf("unknown publish expression %q", unknown)
	}
	return out
}

func (r *publishRun) slot(s publishStep) string {
	if s.ID != "" {
		return s.ID
	}
	r.steps++
	return "step" + strconv.Itoa(r.steps)
}

func (r *publishRun) env(s publishStep, output string) []string {
	env := []string{
		"PATH=" + filepath.Join(r.root, "bin") + ":" + os.Getenv("PATH"),
		"PUBLISH_LOG=" + r.log,
		"PUBLISH_DIGEST=" + publishDigest,
		"GITHUB_SHA=" + publishSHA,
		"IMAGE=" + publishImage,
		"GITHUB_OUTPUT=" + output,
	}
	for k, v := range s.Env {
		env = append(env, k+"="+r.subst(v))
	}
	return env
}

func (r *publishRun) condition(cond string) bool {
	switch strings.TrimSpace(cond) {
	case "":
		return true
	case "steps.tags.outputs.publish == 'true'":
		return r.outputs["tags"]["publish"] == "true"
	default:
		r.t.Fatalf("new publish condition %q", cond)
		return false
	}
}

func (r *publishRun) readOutput(id, output string) {
	if id == "" {
		return
	}
	got := map[string]string{}
	data, err := os.ReadFile(output)
	if err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				got[k] = v
			}
		}
	}
	r.outputs[id] = got
}

func (r *publishRun) run(steps []publishStep) (string, string) {
	for _, s := range steps {
		r.order = append(r.order, s.label())
		if !r.condition(s.If) {
			continue
		}
		if s.Uses != "" {
			if strings.Contains(s.Uses, "attest-build-provenance") {
				r.attested = r.subst(s.With["subject-digest"])
			}
			continue
		}
		output := filepath.Join(r.work, "github-output-"+r.slot(s))
		_ = os.Remove(output)
		cmd := exec.CommandContext(r.t.Context(), "bash", "-e", "-c", s.Run)
		cmd.Dir = r.work
		cmd.Env = r.env(s, output)
		out, err := cmd.CombinedOutput()
		r.readOutput(s.ID, output)
		if err != nil {
			return s.label(), string(out)
		}
	}
	return "", ""
}

func wfWriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (r *publishRun) commands() []string {
	data, err := os.ReadFile(r.log)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func publishCommands(tag string) []string {
	return []string{
		"skopeo login ghcr.io --username actor --password-stdin",
		"skopeo copy --digestfile digest oci:frostread.oci docker://" + publishImage + ":" + tag,
		"skopeo copy --preserve-digests docker://" + publishImage + "@" + publishDigest + " docker://" + publishImage + ":sha-" + publishSHA[:12],
	}
}

func TestFlatpakPublishPublishesCandidatesOnly(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is missing")
	}
	steps := publishSteps(t)
	for _, tc := range []struct {
		name, tag string
		want      []string
	}{
		{name: "release", tag: "v1.2.3"},
		{name: "candidate", tag: "v1.2.3-rc.1", want: publishCommands("v1.2.3-rc.1")},
		{name: "later candidate", tag: "v1.2.3-rc.12", want: publishCommands("v1.2.3-rc.12")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newPublishRun(t, tc.tag)
			if fail, out := r.run(steps); fail != "" {
				t.Fatalf("step %q failed:\n%s", fail, out)
			}
			if got := r.commands(); !slices.Equal(got, tc.want) {
				t.Fatalf("tag %s ran %v, want %v", tc.tag, got, tc.want)
			}
			wantAttest := ""
			if len(tc.want) > 0 {
				wantAttest = publishDigest
			}
			if r.attested != wantAttest {
				t.Fatalf("attested %q, want %q", r.attested, wantAttest)
			}
		})
	}
}

func TestFlatpakPublishRefusesBadTags(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is missing")
	}
	steps := publishSteps(t)
	for _, tag := range []string{"v1.2", "v1.2.3-beta.1", "v1.2.3-rc", "v1.2.3-rc.1-x", "vnext", "v1.2.3.1"} {
		t.Run(tag, func(t *testing.T) {
			r := newPublishRun(t, tag)
			fail, _ := r.run(steps)
			if fail != "Name the OCI tags" {
				t.Fatalf("tag %s failed at %q, want Name the OCI tags", tag, fail)
			}
			if len(r.commands()) != 0 || r.attested != "" {
				t.Fatalf("tag %s published or attested", tag)
			}
		})
	}
}

func TestFlatpakPublishMovesNoLatest(t *testing.T) {
	steps := publishSteps(t)
	for _, s := range steps {
		if (strings.Contains(s.Run, "skopeo copy") && strings.Contains(s.Run, "latest")) || strings.Contains(s.If, "latest") || strings.Contains(s.Run, "workflow run") {
			t.Fatalf("publish step %q moves latest or dispatches promotion", s.label())
		}
	}
}
