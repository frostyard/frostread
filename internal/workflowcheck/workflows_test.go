package workflowcheck

import (
	"os"
	"path/filepath"
	"regexp"
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

// TestFlatpakWorkflow keeps the first Flatpak workflow build-only: it has
// source and build jobs, but no tag or publish job yet.
func TestFlatpakWorkflow(t *testing.T) {
	root, ok := workflows(t)["flatpak.yml"]
	if !ok {
		t.Fatal("no flatpak.yml")
	}
	read := map[string]string{"contents": "read"}
	if got := permMap(get(root, "permissions")); !samePermissions(got, read) {
		t.Errorf("top-level permissions = %v, want only contents: read", got)
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

	push := get(get(get(root, "on"), "push"), "tags")
	for _, tag := range values(push) {
		if strings.HasPrefix(tag, "v") {
			t.Errorf("flatpak workflow has tag trigger %q; v* tags are not part of Phase 4", tag)
		}
	}
	if publish := get(jobs, "publish"); publish != nil {
		t.Error("flatpak.yml has a publish job before the later Flatpak phase")
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
