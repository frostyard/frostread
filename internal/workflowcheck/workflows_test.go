package workflowcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var (
	commitAction = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	dockerAction = regexp.MustCompile(`^docker://[^@]+@sha256:[0-9a-f]{64}$`)
)

// immutable reports whether an action reference names one version for
// good: a local action, a commit, or an image digest.
func immutable(ref string) bool {
	return strings.HasPrefix(ref, "./") || commitAction.MatchString(ref) || dockerAction.MatchString(ref)
}

func TestPinnedReferencesAreImmutable(t *testing.T) {
	sha, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	for ref, want := range map[string]bool{
		"actions/checkout@" + sha:                   true,
		"owner/repo/.github/actions/publish@" + sha: true,
		"docker://alpine@sha256:" + digest:          true,
		"./.github/actions/check":                   true,
		"actions/checkout@v7":                       false,
		"owner/repo/.github/actions/publish@main":   false,
		"actions/checkout@deadbeef":                 false,
		"actions/checkout@${{inputs.ref}}":          false,
		"docker://alpine:3.22":                      false,
		"actions/checkout@" + strings.ToUpper(sha):  false,
	} {
		if got := immutable(ref); got != want {
			t.Errorf("immutable(%q) = %v, want %v", ref, got, want)
		}
	}
}

// workflows parses every workflow in .github/workflows.
func workflows(t *testing.T) map[string]*yaml.Node {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no workflows found")
	}
	out := map[string]*yaml.Node{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
			t.Fatalf("parse %s: %v", p, err)
		}
		out[filepath.Base(p)] = doc.Content[0]
	}
	return out
}

// get returns the value of key in a mapping node, or nil.
func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// walk calls f for every mapping node under n.
func walk(n *yaml.Node, f func(*yaml.Node)) {
	if n.Kind == yaml.MappingNode {
		f(n)
	}
	for _, c := range n.Content {
		walk(c, f)
	}
}

// values returns the scalar values of a sequence node, or nil.
func values(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

// permMap returns the permissions a mapping node declares as key/value pairs.
func permMap(p *yaml.Node) map[string]string {
	out := map[string]string{}
	if p == nil || p.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(p.Content); i += 2 {
		out[p.Content[i].Value] = p.Content[i+1].Value
	}
	return out
}

// samePermissions reports whether got holds exactly the keys and values of want.
func samePermissions(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if gotV, ok := got[k]; !ok || gotV != v {
			return false
		}
	}
	return true
}

// Every external action is pinned to a full commit SHA (the version a
// trailing comment names is for people; the SHA is what runs), every
// workflow starts from declared permissions, and every checkout drops the
// token it would otherwise leave in .git/config.
func TestWorkflowsFollowADR0021(t *testing.T) {
	uses := 0
	for name, root := range workflows(t) {
		if get(root, "permissions") == nil {
			t.Errorf("%s: no top-level permissions (start from permissions: {})", name)
		}
		walk(root, func(m *yaml.Node) {
			ref := get(m, "uses")
			if ref == nil {
				return
			}
			uses++
			if ref.Kind != yaml.ScalarNode || !immutable(ref.Value) {
				t.Errorf("%s:%d: %q must name a full commit SHA or image digest", name, ref.Line, ref.Value)
			}
			if strings.HasPrefix(ref.Value, "actions/checkout@") {
				if pc := get(get(m, "with"), "persist-credentials"); pc == nil || pc.Value != "false" {
					t.Errorf("%s:%d: checkout must set persist-credentials: false", name, ref.Line)
				}
			}
		})
	}
	if uses == 0 {
		t.Fatal("no action references found")
	}
}

// TestFlatpakWorkflow holds the source, build and publish shape of flatpak.yml.
func TestFlatpakWorkflow(t *testing.T) {
	root, ok := workflows(t)["flatpak.yml"]
	if !ok {
		t.Fatal("no flatpak.yml")
	}
	read := map[string]string{"contents": "read"}
	if got := permMap(get(root, "permissions")); !samePermissions(got, read) {
		t.Errorf("top-level permissions = %v, want only contents: read", got)
	}

	push := get(get(get(root, "on"), "push"), "tags")
	if got := values(push); !slices.Equal(got, []string{"v*"}) {
		t.Errorf("flatpak push tags = %v, want [v*]", got)
	}

	jobs := get(root, "jobs")
	wantNames := map[string]string{
		"sources": "Flatpak sources (make flatpak-sources)",
		"flatpak": "Flatpak (build and test)",
	}
	for job, wantName := range wantNames {
		n := get(jobs, job)
		if n == nil {
			t.Fatalf("flatpak.yml has no %s job", job)
		}
		if name := get(n, "name"); name == nil || name.Value != wantName {
			t.Errorf("%s job name = %v, want %q", job, name, wantName)
		}
		if p := get(n, "permissions"); p != nil && !samePermissions(permMap(p), read) {
			t.Errorf("%s job permissions = %v, want none or only contents: read", job, permMap(p))
		}
	}

	flatpak := get(jobs, "flatpak")
	container := get(flatpak, "container")
	image := get(container, "image")
	if image == nil || !regexp.MustCompile(`^ghcr\.io/.+@sha256:[0-9a-f]{64}$`).MatchString(image.Value) {
		t.Errorf("flatpak container image = %v, want a ghcr.io image pinned by sha256", image)
	}

	publish := get(jobs, "publish")
	if publish == nil {
		t.Fatal("flatpak.yml has no publish job")
	}
	if name := get(publish, "name"); name == nil || name.Value != "Publish" {
		t.Errorf("publish job name = %v, want Publish", name)
	}
	wantPublishPerm := map[string]string{"contents": "read", "packages": "write", "id-token": "write", "attestations": "write"}
	if got := permMap(get(publish, "permissions")); !samePermissions(got, wantPublishPerm) {
		t.Errorf("publish permissions = %v, want %v", got, wantPublishPerm)
	}
	if needs := values(get(publish, "needs")); !slices.Equal(needs, []string{"sources", "flatpak"}) {
		t.Errorf("publish needs = %v, want [sources flatpak]", needs)
	}
	if got := get(publish, "runs-on"); got == nil || got.Value != "ubuntu-latest" {
		t.Errorf("publish runs-on = %v, want ubuntu-latest", got)
	}
	if got := get(publish, "if"); got == nil || got.Value != "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')" {
		t.Errorf("publish if = %v, want tag-only if", got)
	}

	steps := get(publish, "steps")
	if steps == nil {
		t.Fatal("publish has no steps")
	}
	walk(publish, func(m *yaml.Node) {
		ref := get(m, "uses")
		if ref != nil && strings.HasPrefix(ref.Value, "actions/checkout@") {
			t.Errorf("publish uses actions/checkout@%s; publishing checks out no repository", ref.Value)
		}
		run := get(m, "run")
		if run == nil {
			return
		}
		if strings.Contains(run.Value, "${{") {
			t.Errorf("publish run holds an expression; pass it through env: %s", strings.TrimSpace(run.Value))
		}
		for _, banned := range []string{"make ", "./", "sh ", "bash ", "go ", "scripts/"} {
			if strings.Contains(run.Value, banned) {
				t.Errorf("publish run calls %q: %s", banned, strings.TrimSpace(run.Value))
			}
		}
	})

	for _, jobName := range []string{"flatpak"} {
		job := get(jobs, jobName)
		walk(job, func(m *yaml.Node) {
			name := get(m, "name")
			if name == nil {
				return
			}
			if name.Value == "Export the OCI image" || name.Value == "Keep the OCI image for publishing" {
				ifStep := get(m, "if")
				if ifStep == nil || ifStep.Value != "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')" {
					t.Errorf("%s is not tag-only", name.Value)
				}
			}
		})
	}
}

// TestPromoteWorkflow holds promote.yml's dispatch-only shape.
func TestPromoteWorkflow(t *testing.T) {
	root, ok := workflows(t)["promote.yml"]
	if !ok {
		t.Fatal("no promote.yml")
	}
	if got := permMap(get(root, "permissions")); len(got) != 0 {
		t.Errorf("top-level promote permissions = %v, want {}", got)
	}
	on := get(root, "on")
	if on == nil || on.Kind != yaml.MappingNode {
		t.Fatal("promote.yml has no on mapping")
	}
	keys := make([]string, 0, len(on.Content)/2)
	for i := 0; i+1 < len(on.Content); i += 2 {
		keys = append(keys, on.Content[i].Value)
	}
	if !slices.Equal(keys, []string{"workflow_dispatch"}) {
		t.Errorf("promote triggers = %v, want only workflow_dispatch", keys)
	}
	dispatch := get(on, "workflow_dispatch")
	inputs := get(get(dispatch, "inputs"), "candidate")
	if inputs == nil {
		t.Fatal("promote.yml has no candidate input")
	}
	inputs = get(get(dispatch, "inputs"), "version")
	if inputs == nil {
		t.Fatal("promote.yml has no version input")
	}

	job := get(get(root, "jobs"), "promote")
	if job == nil {
		t.Fatal("promote.yml has no promote job")
	}
	want := map[string]string{"contents": "read", "packages": "write", "attestations": "read"}
	if got := permMap(get(job, "permissions")); !samePermissions(got, want) {
		t.Errorf("promote permissions = %v, want %v", got, want)
	}
	walk(job, func(m *yaml.Node) {
		if ref := get(m, "uses"); ref != nil {
			t.Errorf("promote step uses %q; the job has no actions", ref.Value)
		}
		if run := get(m, "run"); run != nil && strings.Contains(run.Value, "${{") {
			t.Errorf("promote run contains an expression: %s", strings.TrimSpace(run.Value))
		}
	})
}

// TestReleaseWorkflow keeps the exclusion of candidate tags.
func TestReleaseWorkflow(t *testing.T) {
	root, ok := workflows(t)["release.yml"]
	if !ok {
		t.Fatal("no release.yml")
	}
	push := get(get(get(root, "on"), "push"), "tags")
	if got := values(push); !slices.Equal(got, []string{"v*", "!v*-*"}) {
		t.Errorf("release push tags = %v, want [v* !v*-*]", got)
	}
	build := get(get(root, "jobs"), "build")
	if build == nil {
		t.Fatal("release.yml has no build job")
	}
	needs := get(build, "needs")
	var gotNeeds []string
	if needs != nil {
		if needs.Kind == yaml.ScalarNode {
			gotNeeds = []string{needs.Value}
		} else {
			gotNeeds = values(needs)
		}
	}
	if !slices.Equal(gotNeeds, []string{"candidate"}) {
		t.Errorf("release build needs = %v, want [candidate]", gotNeeds)
	}
}

// TestCIRequiredJobNames keeps the five required CI job names stable.
func TestCIRequiredJobNames(t *testing.T) {
	root, ok := workflows(t)["ci.yml"]
	if !ok {
		t.Fatal("no ci.yml")
	}
	jobs := get(root, "jobs")
	want := map[string]string{
		"verify": "Verify (make verify)",
		"race":   "Race detector and cross-builds (make race cross)",
		"web":    "web bundle (make web-check)",
		"app":    "Desktop app (make app-check app-build)",
		"deb":    "Snapshot .deb (make deb)",
	}
	for job, wantName := range want {
		n := get(jobs, job)
		if n == nil {
			t.Fatalf("ci.yml has no %s job", job)
		}
		if name := get(n, "name"); name == nil || name.Value != wantName {
			t.Errorf("ci %s job name = %v, want %q", job, name, wantName)
		}
	}
}
