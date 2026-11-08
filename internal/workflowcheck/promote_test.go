//go:build linux

package workflowcheck

// The tests of promote.yml (plan 0002, Phases 9 and 11) are a dry run of its
// promote job, as publish_test.go does for flatpak.yml's publish job: stubs
// play skopeo, gh and docker from files in a state directory. So they check
// that a promotion copies the candidate's digest to the version tag and to
// latest, builds and attests nothing, and refuses a bad input, a missing
// candidate, a release tag on another commit, an attestation from another
// workflow, and a version tag at another digest.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	promoteRepository = "frostyard/frostread"
	promoteImage      = "ghcr.io/frostyard/frostread"
	promoteCandidate  = "v1.2.3-rc.1"
	promoteVersion    = "v1.2.3"
	promoteDigest     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	promoteOther      = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

const promoteStubs = `#!/bin/bash
set -u
name=$(basename "$0")
{ printf '%s' "$name"; [ 0 -eq $# ] || printf ' %s' "$@"; printf '\n'; } >>"$PROMOTE_LOG"
state=$PROMOTE_STATE
case "$name" in
skopeo)
	case "${1:-}" in
	login) cat >/dev/null ;;
	inspect)
		image=${!#}; tag=${image##*:}
		[ -f "$state/tags/$tag" ] || { echo "manifest unknown: $image" >&2; exit 1; }
		cat "$state/tags/$tag"; printf '\n'
		;;
	list-tags)
		list=
		for f in "$state"/tags/*; do
			[ -f "$f" ] || continue
			[ -z "$list" ] || list="$list,"
			list="$list\"$(basename "$f")\""
		done
		printf '{"Repository":"%s","Tags":[%s]}\n' "$IMAGE" "$list"
		;;
	copy)
		src=${@: -2:1}; dst=${!#}
		printf '%s' "${src##*@}" >"$state/tags/${dst##*:}"
		;;
	esac
	;;
gh)
	if [ "${1:-}" = api ]; then
		status=$(cat "$state/compare" 2>/dev/null) || { echo "gh: Not Found" >&2; exit 1; }
		printf '%s\n' "$status"
	elif [ "${1:-}" = attestation ]; then
		[ -f "$state/attestation.json" ] || { echo "gh: no attestation" >&2; exit 1; }
		cat "$state/attestation.json"
	elif [ "${1:-}" = workflow ]; then
		:
	fi
	;;
docker) cat >/dev/null ;;
esac
`

type promoteWorkflow struct {
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Steps       []publishStep     `yaml:"steps"`
}

func promoteJob(t *testing.T) promoteWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "promote.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Required bool   `yaml:"required"`
					Type     string `yaml:"type"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Env         map[string]string `yaml:"env"`
			Steps       []publishStep     `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("promote.yml: %v", err)
	}
	if len(wf.On.WorkflowDispatch.Inputs) != 2 {
		t.Fatalf("promote inputs = %v, want candidate and version", wf.On.WorkflowDispatch.Inputs)
	}
	job, ok := wf.Jobs["promote"]
	if !ok || len(job.Steps) == 0 {
		t.Fatal("promote.yml has no promote job")
	}
	return promoteWorkflow{Permissions: job.Permissions, Env: job.Env, Steps: job.Steps}
}

type promoteRun struct {
	t                         *testing.T
	root, work, state, log    string
	candidate, version, token string
	steps                     int
	env                       map[string]string
	outputs                   map[string]map[string]string
	order                     []string
}

func newPromoteRun(t *testing.T, wf promoteWorkflow, candidate, version, token string) *promoteRun {
	t.Helper()
	root := t.TempDir()
	r := &promoteRun{t: t, root: root, work: filepath.Join(root, "work"), state: filepath.Join(root, "state"), log: filepath.Join(root, "log"), candidate: candidate, version: version, token: token, env: map[string]string{}, outputs: map[string]map[string]string{}}
	for k, v := range wf.Env {
		r.env[k] = r.subst(v)
	}
	if err := os.MkdirAll(r.work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.state, "tags"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wfWriteFile(t, r.log, "")
	stub := filepath.Join(root, "bin", "stub")
	wfWriteFile(t, stub, promoteStubs)
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skopeo", "gh", "docker"} {
		if err := os.Symlink("stub", filepath.Join(root, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *promoteRun) subst(v string) string {
	var unknown string
	out := publishExpr.ReplaceAllStringFunc(v, func(e string) string {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(e, "${{"), "}}"))
		switch name {
		case "github.repository":
			return promoteRepository
		case "inputs.candidate":
			return r.candidate
		case "inputs.version":
			return r.version
		case "secrets.FLATPAK_INDEX_DISPATCH_TOKEN", "env.DISPATCH_TOKEN":
			return r.token
		case "secrets.GITHUB_TOKEN", "github.token":
			return "token"
		case "github.actor":
			return "actor"
		case "env.IMAGE":
			return promoteImage
		case "steps.candidate.outputs.digest":
			return r.outputs["candidate"]["digest"]
		default:
			unknown = name
			return e
		}
	})
	if unknown != "" {
		r.t.Fatalf("unknown promote expression %q", unknown)
	}
	return out
}

func (r *promoteRun) setTag(tag, digest string) {
	r.t.Helper()
	wfWriteFile(r.t, filepath.Join(r.state, "tags", tag), digest)
}

func (r *promoteRun) setCompare(status string) {
	r.t.Helper()
	wfWriteFile(r.t, filepath.Join(r.state, "compare"), status)
}

func (r *promoteRun) setAttestation(ref, signer string) {
	r.t.Helper()
	data := `[{"verificationResult":{"signature":{"certificate":{"sourceRepositoryRef":"` + ref + `","buildSignerURI":"` + signer + `"}}}}]`
	wfWriteFile(r.t, filepath.Join(r.state, "attestation.json"), data)
}

func goodPromoteAttestation(candidate string) (string, string) {
	return "refs/tags/" + candidate,
		"https://github.com/" + promoteRepository + "/.github/workflows/flatpak.yml@refs/tags/" + candidate
}

func (r *promoteRun) slot(s publishStep) string {
	if s.ID != "" {
		return s.ID
	}
	r.steps++
	return "step" + strconv.Itoa(r.steps)
}

func (r *promoteRun) condition(cond string) bool {
	switch strings.TrimSpace(cond) {
	case "":
		return true
	case "env.DISPATCH_TOKEN != ''":
		return r.token != ""
	default:
		r.t.Fatalf("new promote condition %q", cond)
		return false
	}
}

func (r *promoteRun) readOutput(id, output string) {
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

func (r *promoteRun) run(steps []publishStep) (string, string) {
	for _, s := range steps {
		r.order = append(r.order, s.label())
		if s.Uses != "" {
			r.t.Fatalf("promote step %q uses an action", s.label())
		}
		if !r.condition(s.If) {
			continue
		}
		output := filepath.Join(r.work, "github-output-"+r.slot(s))
		_ = os.Remove(output)
		cmd := exec.CommandContext(r.t.Context(), "bash", "-e", "-c", s.Run)
		cmd.Dir = r.work
		env := []string{"PATH=" + filepath.Join(r.root, "bin") + ":" + os.Getenv("PATH"), "PROMOTE_LOG=" + r.log, "PROMOTE_STATE=" + r.state, "GITHUB_OUTPUT=" + output}
		for k, v := range r.env {
			env = append(env, k+"="+v)
		}
		for k, v := range s.Env {
			env = append(env, k+"="+r.subst(v))
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		r.readOutput(s.ID, output)
		if err != nil {
			return s.label(), string(out)
		}
	}
	return "", ""
}

func (r *promoteRun) commands() []string {
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

func readyPromote(t *testing.T, wf promoteWorkflow, token string) *promoteRun {
	t.Helper()
	r := newPromoteRun(t, wf, promoteCandidate, promoteVersion, token)
	r.setTag(promoteCandidate, promoteDigest)
	r.setCompare("identical")
	ref, signer := goodPromoteAttestation(promoteCandidate)
	r.setAttestation(ref, signer)
	return r
}

func copyPromoteCommand(tag string) string {
	return "skopeo copy --preserve-digests docker://" + promoteImage + "@" + promoteDigest + " docker://" + promoteImage + ":" + tag
}

func TestFlatpakPromoteCopiesCandidate(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is missing")
	}
	wf := promoteJob(t)
	for _, token := range []string{"", "x"} {
		r := readyPromote(t, wf, token)
		if fail, out := r.run(wf.Steps); fail != "" {
			t.Fatalf("step %q failed:\n%s", fail, out)
		}
		for _, tag := range []string{promoteVersion, "latest"} {
			got, err := os.ReadFile(filepath.Join(r.state, "tags", tag))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != promoteDigest {
				t.Errorf("tag %s holds %q, want %q", tag, got, promoteDigest)
			}
		}
		cmds := r.commands()
		if !slices.Contains(cmds, copyPromoteCommand(promoteVersion)) || !slices.Contains(cmds, copyPromoteCommand("latest")) {
			t.Fatalf("promotion commands = %v", cmds)
		}
	}
}

func TestFlatpakPromoteRefusesBadStates(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is missing")
	}
	wf := promoteJob(t)
	for _, tc := range []struct {
		name, candidate, version, step               string
		missing, mismatch, badAttestation, badDigest bool
	}{
		{name: "bad candidate", candidate: promoteVersion, version: promoteVersion, step: "Check the inputs"},
		{name: "bad beta", candidate: "v1.2.3-beta.1", version: promoteVersion, step: "Check the inputs"},
		{name: "short version", candidate: promoteCandidate, version: "v1.2", step: "Check the inputs"},
		{name: "other release", candidate: promoteCandidate, version: "v1.3.0", step: "Check the inputs"},
		{name: "missing candidate", candidate: promoteCandidate, version: promoteVersion, step: "Resolve the candidate's digest", missing: true},
		{name: "mismatch release tag", candidate: promoteCandidate, version: promoteVersion, step: "Check the release tag is the candidate's commit", mismatch: true},
		{name: "bad attestation", candidate: promoteCandidate, version: promoteVersion, step: "Verify the candidate's attestation", badAttestation: true},
		{name: "other version digest", candidate: promoteCandidate, version: promoteVersion, step: "Check the version tag is free", badDigest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newPromoteRun(t, wf, tc.candidate, tc.version, "")
			if !tc.missing {
				r.setTag(promoteCandidate, promoteDigest)
			}
			if tc.mismatch {
				r.setCompare("ahead")
			} else {
				r.setCompare("identical")
			}
			if tc.badAttestation {
				r.setAttestation("refs/tags/"+promoteCandidate, "https://github.com/"+promoteRepository+"/.github/workflows/release.yml@refs/tags/"+promoteCandidate)
			} else if !tc.missing {
				ref, signer := goodPromoteAttestation(promoteCandidate)
				r.setAttestation(ref, signer)
			}
			if tc.badDigest {
				r.setTag(promoteVersion, promoteOther)
			}
			fail, _ := r.run(wf.Steps)
			if fail != tc.step {
				t.Fatalf("failed at %q, want %q", fail, tc.step)
			}
			for _, cmd := range r.commands() {
				if strings.Contains(cmd, "skopeo copy") {
					t.Fatalf("refused state copied an image: %s", cmd)
				}
			}
		})
	}
}

func TestFlatpakPromoteAllowsRerunAndBuildsNothing(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is missing")
	}
	wf := promoteJob(t)
	r := readyPromote(t, wf, "")
	r.setTag(promoteVersion, promoteDigest)
	if fail, out := r.run(wf.Steps); fail != "" {
		t.Fatalf("step %q failed:\n%s", fail, out)
	}
	for _, s := range wf.Steps {
		if s.Uses != "" {
			t.Fatalf("promote step %q uses an action", s.label())
		}
		for _, banned := range []string{"flatpak-builder", "build-bundle", "attest-build-provenance", "attestation create"} {
			if strings.Contains(s.Run, banned) {
				t.Fatalf("promote step %q builds or attests", s.label())
			}
		}
	}
}
