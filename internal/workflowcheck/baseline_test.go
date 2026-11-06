package workflowcheck

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// skipped reports whether the repository walk leaves directory name out:
// version control, dependencies, build output, and each name in extra.
func skipped(name string, extra map[string]bool) bool {
	switch name {
	case ".git", "node_modules", "build", "dist", "target":
		return true
	}
	return extra[name]
}

// repoFiles returns every file of the repository as a path from its root with
// forward slashes. The directories skipped, and each of extra, are left out.
func repoFiles(t *testing.T, extra ...string) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	skip := make(map[string]bool, len(extra))
	for _, name := range extra {
		skip[name] = true
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped(d.Name(), skip) {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInstructionAliases guards row B1 of frostyard/core
// docs/specs/repository-baseline.md: AGENTS.md is the one instruction file,
// and every other agent entry point is a relative symlink to it.
func TestInstructionAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows checkout has no symlinks")
	}
	agents := filepath.Join("..", "..", "AGENTS.md")
	fi, err := os.Lstat(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("AGENTS.md is not a regular file (mode %s); it is the one instruction file", fi.Mode())
	}
	want, err := filepath.EvalSymlinks(agents)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"CLAUDE.md",
		"GEMINI.md",
		".cursorrules",
		filepath.Join(".github", "copilot-instructions.md"),
	} {
		if got, ok := relativeSymlink(t, name); ok && got != want {
			t.Errorf("%s resolves to %q, want %q", name, got, want)
		}
	}

	name := filepath.Join(".claude", "skills")
	got, ok := relativeSymlink(t, name)
	wantSkills, err := filepath.EvalSymlinks(filepath.Join("..", "..", ".agents", "skills"))
	if err != nil {
		t.Fatalf(".agents/skills: %v", err)
	}
	if ok && got != wantSkills {
		t.Errorf("%s resolves to %q, want %q", name, got, wantSkills)
	}
	if fi, err := os.Stat(filepath.Join("..", "..", name)); err != nil {
		t.Errorf("%s: %v", name, err)
	} else if !fi.IsDir() {
		t.Errorf("%s resolves to a file, want the directory .agents/skills", name)
	}
}

// relativeSymlink reports whether name is a symlink whose target is relative,
// and returns the path it resolves to.
func relativeSymlink(t *testing.T, name string) (string, bool) {
	t.Helper()
	link := filepath.Join("..", "..", name)
	fi, err := os.Lstat(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is not a symlink", name)
		return "", false
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	if filepath.IsAbs(target) {
		t.Errorf("%s links to the absolute path %q; it must be relative", name, target)
		return "", false
	}
	got, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	return got, true
}

// correctionsMember returns the 5 members a line of
// .memory/corrections.jsonl carries (row B2).
func correctionsMember() []string {
	return []string{"date", "scope", "correction", "evidence", "promoted_to"}
}

// datePattern is the form of a correction's date.
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// correctionProblems returns the problems of 1 line of
// .memory/corrections.jsonl, after frostyard/core scripts/check-corrections.mjs
// (row B2); no problem means a valid line. promoted_to may be empty, the way an
// unpromoted correction has it.
func correctionProblems(line string) []string {
	if strings.TrimSpace(line) == "" {
		return []string{"blank line"}
	}
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return []string{fmt.Sprintf("invalid JSON: %v", err)}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return []string{"not a plain object"}
	}

	var problems, missing, unknown []string
	for _, name := range correctionsMember() {
		if _, ok := obj[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range obj {
		if !slices.Contains(correctionsMember(), name) {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	if len(missing) > 0 {
		problems = append(problems, "missing key(s): "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "unknown key(s): "+strings.Join(unknown, ", "))
	}
	for _, name := range correctionsMember() {
		v, ok := obj[name]
		if !ok {
			continue
		}
		if _, ok := v.(string); !ok {
			problems = append(problems, "value for "+name+" is not a string")
		}
	}
	if s, ok := obj["date"].(string); ok {
		if !datePattern.MatchString(s) {
			problems = append(problems, "invalid date: "+s)
		} else if _, err := time.Parse("2006-01-02", s); err != nil {
			problems = append(problems, "invalid date: "+s)
		}
	}
	for _, name := range []string{"scope", "correction", "evidence"} {
		if s, ok := obj[name].(string); ok && strings.TrimSpace(s) == "" {
			problems = append(problems, name+" is empty")
		}
	}
	return problems
}

// correctionsLines returns the lines of .memory/corrections.jsonl that hold
// content: an empty file gives no line, and the last newline does not give
// one. A blank line inside the file stays, and it is a problem.
func correctionsLines(data string) []string {
	if data == "" {
		return nil
	}
	lines := strings.Split(data, "\n")
	if n := len(lines); lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// TestCorrectionsInbox guards row B2 of frostyard/core
// docs/specs/repository-baseline.md: .memory/corrections.jsonl holds one JSON
// object per line, with exactly the 5 fields of a correction. An empty file
// is valid too, as core's scripts/check-corrections.mjs accepts it.
func TestCorrectionsInbox(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".memory", "corrections.jsonl"))
	if err != nil {
		t.Fatalf(".memory/corrections.jsonl: %v", err)
	}
	lines := correctionsLines(string(data))
	for i, line := range lines {
		for _, problem := range correctionProblems(line) {
			t.Errorf("line %d: %s", i+1, problem)
		}
	}
	t.Logf("%d corrections lines", len(lines))
}

// TestCorrectionsLines checks that empty content gives no line, and so no
// problem, and that a blank line inside the content is a problem.
func TestCorrectionsLines(t *testing.T) {
	const valid = `{"date": "2026-11-05", "scope": "socket", "correction": "c", "evidence": "e", "promoted_to": ""}`
	if lines := correctionsLines(""); len(lines) != 0 {
		t.Errorf("correctionsLines of empty content = %q, want no line", lines)
	}
	for i, line := range correctionsLines("") {
		if problems := correctionProblems(line); len(problems) > 0 {
			t.Errorf("line %d of empty content: %v, want no problem", i+1, problems)
		}
	}
	if lines := correctionsLines(valid); !slices.Equal(lines, []string{valid}) {
		t.Errorf("correctionsLines of 1 line without a newline = %q, want 1 line", lines)
	}
	lines := correctionsLines(valid + "\n\n")
	if len(lines) != 2 {
		t.Fatalf("correctionsLines of 1 line and a blank line = %q, want 2 lines", lines)
	}
	if problems := correctionProblems(lines[1]); len(problems) == 0 {
		t.Errorf("the blank line inside the content gives no problem, want at least 1")
	}
}

// TestCorrectionProblems checks the line rules of row B2 on the cases
// frostyard/core scripts/check-corrections.mjs names.
func TestCorrectionProblems(t *testing.T) {
	const valid = `{"date": "2026-11-05", "scope": "socket", "correction": "c", "evidence": "e", "promoted_to": ""}`
	for _, tc := range []struct {
		name  string
		line  string
		valid bool
	}{
		{"unpromoted", valid, true},
		{"promoted", `{"date": "2026-11-05", "scope": "s", "correction": "c", "evidence": "e", "promoted_to": "a_test.go"}`, true},
		{"blank scope", `{"date": "2026-11-05", "scope": "  ", "correction": "c", "evidence": "e", "promoted_to": ""}`, false},
		{"month 13", `{"date": "2026-13-99", "scope": "s", "correction": "c", "evidence": "e", "promoted_to": ""}`, false},
		{"february 30", `{"date": "2026-02-30", "scope": "s", "correction": "c", "evidence": "e", "promoted_to": ""}`, false},
		{"number value", `{"date": "2026-11-05", "scope": 1, "correction": "c", "evidence": "e", "promoted_to": ""}`, false},
		{"missing member", `{"date": "2026-11-05", "scope": "s", "correction": "c", "evidence": "e"}`, false},
		{"unknown member", `{"date": "2026-11-05", "scope": "s", "correction": "c", "evidence": "e", "promoted_to": "", "extra": "x"}`, false},
		{"duplicate member", `{"date": "2026-11-05", "date": "2026-11-05", "scope": "s", "correction": "c", "evidence": "e", "promoted_to": ""}`, false},
		{"not an object", `["2026-11-05"]`, false},
		{"blank line", "  ", false},
	} {
		problems := correctionProblems(tc.line)
		switch {
		case tc.valid && len(problems) > 0:
			t.Errorf("%s: correctionProblems(%s) = %v, want none", tc.name, tc.line, problems)
		case !tc.valid && len(problems) == 0:
			t.Errorf("%s: correctionProblems(%s) = none, want at least 1", tc.name, tc.line)
		}
	}
}

// riskPath is 1 entry of .github/risk-paths.json.
type riskPath struct {
	Glob string `json:"glob"`
	Why  string `json:"why"`
}

// riskPathFile is the whole of .github/risk-paths.json.
type riskPathFile struct {
	SchemaVersion int        `json:"schema_version"`
	Paths         []riskPath `json:"paths"`
}

// riskCategories returns the categories of frostyard/core
// docs/specs/risk-paths.md (section B); each why starts with 1 of them and a
// colon.
func riskCategories() []string {
	return []string{
		"boot chain",
		"signing and keys",
		"update verification",
		"auth",
		"publication",
		"secrets and credentials",
		"rulesets and settings",
		"destructive operations",
		"governance",
	}
}

// whyCategory reports whether why starts with 1 of riskCategories and a colon.
func whyCategory(why string) bool {
	category, _, ok := strings.Cut(why, ":")
	return ok && slices.Contains(riskCategories(), category)
}

// governedPaths returns the release, secrets, auth and governance paths of
// this repository; .github/risk-paths.json must list each of them.
func governedPaths() []string {
	return []string{
		".github/risk-paths.json",
		".github/workflows/risk-paths.yml",
		".github/workflows/release.yml",
		".github/workflows/ci.yml",
		".github/workflows/flatpak.yml",
		"Makefile",
		"mise.toml",
		"mise.lock",
		".svu.yml",
		"internal/workflowcheck/baseline_test.go",
		"internal/workflowcheck/workflows_test.go",
		"internal/packagecheck/background_helper_test.go",
		"internal/packagecheck/debcheck_test.go",
		"internal/packagecheck/flatpak_manifest_test.go",
		"internal/packagecheck/flatpak_screenshots_test.go",
		"internal/packagecheck/flatpak_version_test.go",
		"internal/packagecheck/package_test.go",
		"packaging/nfpm.yaml",
		"packaging/deb-check.py",
		"packaging/lodge/app.json",
		"packaging/lodge/frostread.service",
		"packaging/lodge/frostread.socket",
		"packaging/lodge/stage.sh",
		"packaging/lodge/sysusers.d-frostread.conf",
		"packaging/flatpak/org.frostyard.Frostread.json",
		"packaging/flatpak/frostread.sh",
		"packaging/flatpak/request-background.py",
		"packaging/flatpak/tauri-build.sh",
		"scripts/release-version.sh",
		"scripts/flatpak-sources.sh",
		"packaging/flatpak/generator-requirements.txt",
		"packaging/flatpak/go-sources.json",
		"packaging/flatpak/cargo-sources.json",
		"packaging/flatpak/node-sources.json",
		"tools/flatpakgo/main.go",
		"cmd/readd/main.go",
		"cmd/readd/lock.go",
		"cmd/readd/logfile.go",
		"cmd/readd/main_test.go",
		"cmd/readd/desktop_test.go",
		"internal/config/config.go",
		"internal/config/desktop.go",
		"api/api.go",
		"api/daemon.go",
		"api/events.go",
		"api/rpc.go",
		"api/read.go",
		"internal/rpcserver/server.go",
		"internal/readapi/readapi.go",
		"internal/engine/scope.go",
		"internal/engine/engine.go",
		"internal/engine/poll.go",
		"internal/mcpserver/mcpserver.go",
		"internal/notes/notes.go",
		"internal/content/content.go",
		"internal/extract/extract.go",
		"internal/fetch/fetch.go",
		"internal/fetch/devfetch.go",
		"internal/sanitize/sanitize.go",
		"web/src-tauri/src/main.rs",
		"web/src-tauri/src/bridge.rs",
		"web/src-tauri/src/remote.rs",
		"web/src-tauri/src/paths.rs",
		"web/src-tauri/src/files.rs",
		"web/src-tauri/src/portal.rs",
		"web/src-tauri/tauri.conf.json",
		"web/src-tauri/capabilities/default.json",
		"web/src/components/Reader.tsx",
		"web/src/components/RenderedArticle.tsx",
		"internal/store/store.go",
		"internal/store/migrations/0001_init.sql",
		"internal/store/migrations/0002_blob_orphans.sql",
		"internal/store/migrations/0003_source_folders.sql",
		"internal/store/blobrefs.go",
		"internal/store/sources.go",
		"internal/blobs/blobs.go",
		"internal/engine/sweep.go",
	}
}

// readRiskPaths reads and parses .github/risk-paths.json strictly: an unknown
// member fails the test, so a new field of the file needs this test.
func readRiskPaths(t *testing.T) riskPathFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "risk-paths.json"))
	if err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	var cfg riskPathFile
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	return cfg
}

// riskMarkers returns the lines of Go source that mark a file as a risk path:
// a method's callers, a file's mode, a socket, the fetcher's address guard, a
// content URL's signature, a sanitizer call on untrusted HTML, the sanitizer
// policy, an MCP session, the agent token's scope and the scope check of a
// write method, and a delete in the store.
func riskMarkers() []string {
	return []string{
		"[]CallerKind{",
		".Chmod(",
		"0o600",
		"0o700",
		"jsonrpc.Listen",
		"netguard.",
		"hmac.",
		"sanitize.HTML(",
		"bluemonday.",
		"mcpsession.",
		"engine.Scope{",
		"sc.Write",
		"DELETE FROM",
	}
}

// riskMarker returns the first marker of riskMarkers() that src holds, in the
// order of the list; no marker means plain source.
func riskMarker(src string) (string, bool) {
	for _, marker := range riskMarkers() {
		if strings.Contains(src, marker) {
			return marker, true
		}
	}
	return "", false
}

// TestRiskPaths guards row B6 of frostyard/core
// docs/specs/repository-baseline.md: .github/risk-paths.json lists every
// governed path of the repository, with a reason, and no stale glob.
func TestRiskPaths(t *testing.T) {
	cfg := readRiskPaths(t)
	if cfg.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", cfg.SchemaVersion)
	}
	if len(cfg.Paths) == 0 {
		t.Fatal(".github/risk-paths.json lists no path")
	}
	globs := make([]string, 0, len(cfg.Paths))
	seen := make(map[string]bool, len(cfg.Paths))
	for i, p := range cfg.Paths {
		for _, problem := range validGlob(p.Glob) {
			t.Errorf("entry %d: glob %q: %s", i, p.Glob, problem)
		}
		if seen[p.Glob] {
			t.Errorf("entry %d: glob %q appears twice", i, p.Glob)
		}
		seen[p.Glob] = true
		globs = append(globs, p.Glob)
		if strings.TrimSpace(p.Why) == "" {
			t.Errorf("entry %d: glob %q has no why", i, p.Glob)
		}
		if !whyCategory(p.Why) {
			t.Errorf("entry %d: glob %q has the why %q; it must start with 1 of the categories %q and a colon", i, p.Glob, p.Why, riskCategories())
		}
		if n := utf8.RuneCountInString(p.Why); n > 200 {
			t.Errorf("entry %d: glob %q has a why of %d characters, want 200 at most", i, p.Glob, n)
		}
	}

	want := governedPaths()
	workflows, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range workflows {
		want = append(want, path.Join(".github/workflows", filepath.Base(p)))
	}
	files := repoFiles(t)
	for _, p := range missingPaths(want, files) {
		t.Errorf("%s is missing; the repository must have it, and .github/risk-paths.json must list it", p)
	}
	for _, p := range want {
		if !matchAny(globs, p) {
			t.Errorf("%s is a release, secrets, auth or governance path; .github/risk-paths.json must list it", p)
		}
	}

	for _, g := range globs {
		if !anyMatch(g, files) {
			t.Errorf("glob %q matches no file in the repository", g)
		}
	}
	t.Logf("%d globs; %d required paths listed; %d files searched", len(globs), len(want), len(files))
}

// TestRiskMarkers guards the risk list against the next omission: a Go file
// that admits a caller to a method, sets a file's mode, opens a socket, holds
// the fetcher's address guard, signs a content URL, holds the sanitizer
// policy, opens an MCP session or holds the agent token's scope is an auth or
// credential check, whatever package it is in. A glob that names files misses
// such a file when a function moves to a new one.
func TestRiskMarkers(t *testing.T) {
	cfg := readRiskPaths(t)
	globs := make([]string, 0, len(cfg.Paths))
	for _, p := range cfg.Paths {
		globs = append(globs, p.Glob)
	}
	marked := 0
	for _, f := range repoFiles(t) {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		marker, ok := riskMarker(string(data))
		if !ok {
			continue
		}
		marked++
		if !matchAny(globs, f) {
			t.Errorf("%s has %s (an auth, credential or schema check); .github/risk-paths.json must list it", f, marker)
		}
	}
	t.Logf("%d globs; %d Go files with a marker", len(globs), marked)
}

// TestRiskMarker checks the marker list on source that holds each marker, and
// on plain source.
func TestRiskMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"method admission", "callers := []CallerKind{CallerLocal, CallerSession, CallerAgent}", "[]CallerKind{"},
		{"file mode by Chmod", "if err := tmp.Chmod(0o644); err != nil {", ".Chmod("},
		{"token file mode", "os.WriteFile(path, data, 0o600)", "0o600"},
		{"state directory mode", "os.MkdirAll(state, 0o700)", "0o700"},
		{"private socket", "private, err := jsonrpc.Listen(socket, log)", "jsonrpc.Listen"},
		{"address guard", "guard, err := netguard.New(cfg)", "netguard."},
		{"content URL signature", "sum := hmac.New(sha256.New, secret)", "hmac."},
		{"sanitizer call on untrusted HTML", "body := sanitize.HTML(raw, base)", "sanitize.HTML("},
		{"sanitizer policy", "policy := bluemonday.NewPolicy()", "bluemonday."},
		{"MCP session", "session := mcpsession.New(conn)", "mcpsession."},
		{"token scope", "scope := engine.Scope{Read: true, Write: false}", "engine.Scope{"},
		{"write scope check", "if !sc.Write {\n\treturn ErrForbidden\n}", "sc.Write"},
		{"delete in the store", "_, err := tx.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)", "DELETE FROM"},
		{"first marker in the list order", "if err := tmp.Chmod(0o600); err != nil {", ".Chmod("},
		{"plain source", "package api\n\nfunc Serve() {}\n", ""},
	} {
		got, ok := riskMarker(tc.src)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: riskMarker(%q) = %q, %v, want %q", tc.name, tc.src, got, ok, tc.want)
		}
	}
}

// missingPaths returns each of required that is not in files, in order. A
// required path must exist: a glob such as .github/workflows/** still
// matches the name of a deleted caller.
func missingPaths(required, files []string) []string {
	var missing []string
	for _, p := range required {
		if !slices.Contains(files, p) {
			missing = append(missing, p)
		}
	}
	return missing
}

// TestMissingPaths checks that a required path that is not in the
// repository is missing, also when a wide glob still matches its name.
func TestMissingPaths(t *testing.T) {
	files := []string{".github/risk-paths.json", ".github/workflows/ci.yml"}
	required := []string{".github/risk-paths.json", ".github/workflows/risk-paths.yml"}
	if !matchAny([]string{".github/workflows/**"}, ".github/workflows/risk-paths.yml") {
		t.Fatal("the wide glob does not match the caller's name")
	}
	got := missingPaths(required, files)
	if want := []string{".github/workflows/risk-paths.yml"}; !slices.Equal(got, want) {
		t.Errorf("missingPaths(%q, %q) = %q, want %q", required, files, got, want)
	}
	if got := missingPaths(files, files); len(got) != 0 {
		t.Errorf("missingPaths of present paths = %q, want none", got)
	}
}

// matchAny reports whether p matches 1 of globs.
func matchAny(globs []string, p string) bool {
	for _, g := range globs {
		if matchGlob(g, p) {
			return true
		}
	}
	return false
}

// anyMatch reports whether glob matches 1 of paths.
func anyMatch(glob string, paths []string) bool {
	for _, p := range paths {
		if matchGlob(glob, p) {
			return true
		}
	}
	return false
}

// maxBraceExpansions is the most globs 1 brace group set may give
// (frostyard/core scripts/lib/risk-paths.mjs).
const maxBraceExpansions = 256

// matchGlob reports whether glob matches the whole slash-separated path
// (frostyard/core docs/specs/risk-paths.md): {a,b} is 1 of the alternatives,
// ** as a whole segment is 0 or more whole segments, * and ? never cross a
// slash, and every other character is literal. An empty path matches nothing,
// and a glob that cannot be expanded matches nothing.
func matchGlob(glob, p string) bool {
	if p == "" {
		return false
	}
	expansions, errs := expandBraces(glob)
	if len(errs) > 0 {
		return false
	}
	pathSegments := strings.Split(p, "/")
	for _, expansion := range expansions {
		if matchSegments(strings.Split(expansion, "/"), pathSegments) {
			return true
		}
	}
	return false
}

// expandBraces returns 1 expansion of glob for each combination of its brace
// groups, and the reasons glob cannot be expanded; a glob with a reason expands
// to nothing. Groups do not nest.
func expandBraces(glob string) (expansions []string, errs []string) {
	var parts [][]string
	literal := ""
	for i := 0; i < len(glob); {
		switch glob[i] {
		case '}':
			errs = append(errs, "unmatched '}'")
			i++
		case '{':
			rest := glob[i+1:]
			close, nextOpen := strings.IndexByte(rest, '}'), strings.IndexByte(rest, '{')
			if close < 0 {
				return nil, append(errs, "unmatched '{'")
			}
			if nextOpen >= 0 && nextOpen < close {
				return nil, append(errs, "nested braces are not supported")
			}
			alternatives := strings.Split(rest[:close], ",")
			if len(alternatives) < 2 {
				errs = append(errs, "a brace group needs at least 2 alternatives")
			}
			if slices.Contains(alternatives, "") {
				errs = append(errs, "a brace group has an empty alternative")
			}
			parts = append(parts, []string{literal}, alternatives)
			literal = ""
			i += close + 2
		default:
			literal += glob[i : i+1]
			i++
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	parts = append(parts, []string{literal})

	expansions = []string{""}
	for _, alternatives := range parts {
		next := make([]string, 0, len(expansions)*len(alternatives))
		for _, prefix := range expansions {
			for _, alternative := range alternatives {
				next = append(next, prefix+alternative)
			}
		}
		if len(next) > maxBraceExpansions {
			return nil, []string{fmt.Sprintf("brace expansion gives more than %d globs", maxBraceExpansions)}
		}
		expansions = next
	}
	return expansions, nil
}

// matchSegments reports whether the path segments match the glob segments,
// where a ** segment is 0 or more whole segments.
func matchSegments(globSegments, pathSegments []string) bool {
	patterns := make([]*regexp.Regexp, len(globSegments))
	for i, segment := range globSegments {
		if segment != "**" {
			patterns[i] = segmentPattern(segment)
		}
	}
	memo := make(map[[2]int]bool, len(globSegments)*len(pathSegments)+1)
	var visit func(g, p int) bool
	visit = func(g, p int) bool {
		if match, ok := memo[[2]int{g, p}]; ok {
			return match
		}
		var match bool
		switch {
		case g == len(patterns):
			match = p == len(pathSegments)
		case patterns[g] == nil:
			match = visit(g+1, p) || (p < len(pathSegments) && visit(g, p+1))
		default:
			match = p < len(pathSegments) && patterns[g].MatchString(pathSegments[p]) && visit(g+1, p+1)
		}
		memo[[2]int{g, p}] = match
		return match
	}
	return visit(0, 0)
}

// segmentPattern compiles 1 glob segment: * is any run of characters within the
// segment, ? is 1 such character, and every other character is literal.
func segmentPattern(segment string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, c := range segment {
		switch c {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// validGlob returns the reasons glob is not a path glob of frostyard/core
// docs/specs/risk-paths.md; no reason means a valid glob.
func validGlob(glob string) []string {
	if glob == "" {
		return []string{"glob is empty"}
	}
	var errs []string
	for _, c := range []string{"[", "]", "!", `\`} {
		if strings.Contains(glob, c) {
			errs = append(errs, fmt.Sprintf("%q is not supported", c))
		}
	}
	expansions, braceErrs := expandBraces(glob)
	errs = append(errs, braceErrs...)
	for _, expansion := range expansions {
		if strings.HasPrefix(expansion, "/") {
			errs = append(errs, "a glob must not start with '/'")
		}
		if strings.HasPrefix(expansion, "./") {
			errs = append(errs, "a glob must not start with './'")
		}
		for _, segment := range strings.Split(expansion, "/") {
			switch {
			case segment == "":
				errs = append(errs, "a glob must not have an empty segment")
			case segment == "..":
				errs = append(errs, "a glob must not have a '..' segment")
			case strings.Contains(segment, "**") && segment != "**":
				errs = append(errs, "'**' must be a whole segment")
			}
		}
	}
	return unique(errs)
}

// unique returns list without a repeated entry, in the order of 1st appearance.
func unique(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestValidGlob checks the glob validation of frostyard/core
// docs/specs/risk-paths.md on the cases a repository's list can hold.
func TestValidGlob(t *testing.T) {
	for _, glob := range []string{"a/**", "{a,b}/c", "**", "cmd/readd/{main,lock,logfile,main_test,desktop_test}.go"} {
		if problems := validGlob(glob); len(problems) > 0 {
			t.Errorf("validGlob(%q) = %v, want no problem", glob, problems)
		}
	}
	for _, glob := range []string{
		"cmd/readd/{main,}",
		"{a}",
		"{a,{b,c}}",
		"a}",
		"{a,b",
		"[ab]",
		"!a",
		`a\b`,
		"a**",
		"a/**b",
		"/a",
		"./a",
		"a//b",
		"a/../b",
		strings.Repeat("{a,b}", 9),
	} {
		if problems := validGlob(glob); len(problems) == 0 {
			t.Errorf("validGlob(%q) = no problem, want at least 1", glob)
		}
	}
}

// TestWhyCategory checks that a why names 1 category of frostyard/core
// docs/specs/risk-paths.md, before a colon.
func TestWhyCategory(t *testing.T) {
	for why, want := range map[string]bool{
		"auth: the content URLs' secret":             true,
		"destructive operations: a schema migration": true,
		"security: the state directory's mode":       false,
		"auth the content URLs' secret":              false,
		"Auth: the content URLs' secret":             false,
		"governance":                                 false,
		" governance: the list":                      false,
	} {
		if got := whyCategory(why); got != want {
			t.Errorf("whyCategory(%q) = %v, want %v", why, got, want)
		}
	}
}

// TestMatchGlob checks the glob rules of frostyard/core
// docs/specs/risk-paths.md on its own cases and the ones this repository's
// list holds.
func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		glob string
		path string
		want bool
	}{
		{"**/x", "x", true},
		{"**/x", "a/x", true},
		{"**/x", "a/b/x", true},
		{"**/x", "a/x/y", false},
		{"**", "x", true},
		{"**", "a/b", true},
		{"**", ".github/x.yml", true},
		{"x*", "x", true},
		{"x*", "xy", true},
		{"x*", "a/x", false},
		{"x*", "a/xy", false},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/.md", true},
		{"docs/*.md", "docs/a/b.md", false},
		{"docs/*.md", "src/a.md", false},
		{"shared/**", "shared/a", true},
		{"shared/**", "shared/a/b", true},
		{"shared/**", "other/a", false},
		{"a/**/x", "a/x", true},
		{"a/**/x", "a/b/x", true},
		{"a/*", "a/b", true},
		{"a/*", "a/b/c", false},
		{"**", "", false},
		{"*.md", "", false},
		{".github/{risk-paths.json,settings.yml}", ".github/risk-paths.json", true},
		{".github/{risk-paths.json,settings.yml}", ".github/settings.yml", true},
		{".github/{risk-paths.json,settings.yml}", ".github/workflows/ci.yml", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", ".github/workflows/risk-paths.yml", true},
		{".github/workflows/**", ".github/risk-paths.json", false},
		{"packaging/lodge/**", "packaging/lodge/stage.sh", true},
		{"packaging/lodge/**", "packaging/lodge/sysusers.d-frostread.conf", true},
		{"packaging/lodge/**", "packaging/nfpm.yaml", false},
		{"packaging/{nfpm.yaml,deb-check.py}", "packaging/deb-check.py", true},
		{"packaging/{nfpm.yaml,deb-check.py}", "packaging/lodge/deb-check.py", false},
		{"internal/{workflowcheck,packagecheck}/**", "internal/packagecheck/debcheck_test.go", true},
		{"internal/{workflowcheck,packagecheck}/**", "internal/workflowcheck/baseline_test.go", true},
		{"internal/{workflowcheck,packagecheck}/**", "internal/config/config.go", false},
		{"cmd/readd/{main,lock,logfile,main_test,desktop_test}.go", "cmd/readd/main.go", true},
		{"cmd/readd/{main,lock,logfile,main_test,desktop_test}.go", "cmd/readd/desktop_test.go", true},
		{"cmd/readd/{main,lock,logfile,main_test,desktop_test}.go", "cmd/readd/problems.go", false},
		{"{internal/engine/scope.go,internal/mcpserver/**}", "internal/engine/scope.go", true},
		{"{internal/engine/scope.go,internal/mcpserver/**}", "internal/mcpserver/mcpserver.go", true},
		{"{internal/engine/scope.go,internal/mcpserver/**}", "internal/engine/engine.go", false},
		{"packaging/flatpak/{org.frostyard.Frostread.json,frostread.sh,request-background.py,tauri-build.sh}", "packaging/flatpak/frostread.sh", true},
		{"packaging/flatpak/{org.frostyard.Frostread.json,frostread.sh,request-background.py,tauri-build.sh}", "packaging/flatpak/smoke-test.sh", false},
		{"internal/fetch/**", "internal/fetch/devfetch.go", true},
		{"internal/fetch/**", "internal/fetchdev/fetch.go", false},
		{"internal/notes/**", "internal/notes/notes.go", true},
		{"internal/notes/**", "internal/notesx/notes.go", false},
		{"web/src-tauri/src/{main,bridge,remote,files,portal}.rs", "web/src-tauri/src/bridge.rs", true},
		{"web/src-tauri/src/{main,bridge,remote,files,portal}.rs", "web/src-tauri/src/paths.rs", false},
		{"web/src-tauri/src/{main,bridge,remote,files,portal}.rs", "web/src-tauri/src/main.rs.bak", false},
		{"{web/src-tauri/tauri.conf.json,web/src-tauri/capabilities/**}", "web/src-tauri/tauri.conf.json", true},
		{"{web/src-tauri/tauri.conf.json,web/src-tauri/capabilities/**}", "web/src-tauri/capabilities/default.json", true},
		{"{web/src-tauri/tauri.conf.json,web/src-tauri/capabilities/**}", "web/src-tauri/Cargo.toml", false},
		{"{internal/store/store.go,internal/store/migrations/**}", "internal/store/store.go", true},
		{"{internal/store/store.go,internal/store/migrations/**}", "internal/store/migrations/0001_init.sql", true},
		{"{internal/store/store.go,internal/store/migrations/**}", "internal/store/items.go", false},
		{"{internal/blobs/blobs.go,internal/engine/sweep.go}", "internal/blobs/blobs.go", true},
		{"{internal/blobs/blobs.go,internal/engine/sweep.go}", "internal/engine/sweep.go", true},
		{"{internal/blobs/blobs.go,internal/engine/sweep.go}", "internal/engine/poll.go", false},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/api.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/daemon.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/events.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/rpc.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "internal/rpcserver/server.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "internal/readapi/readapi.go", true},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/api_test.go", false},
		{"{api/api.go,api/daemon.go,api/events.go,api/rpc.go,api/read.go,internal/rpcserver/server.go,internal/readapi/readapi.go}", "api/other.go", false},
		{"{mise.toml,mise.lock}", "mise.toml", true},
		{"{mise.toml,mise.lock}", "mise.lock", true},
		{"{mise.toml,mise.lock}", "mise.tom", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "app/Makefile", false},
		{".svu.yml", ".svu.yml", true},
		{".svu.yml", "docs/.svu.yml", false},
	} {
		if got := matchGlob(tc.glob, tc.path); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}
}

// dependabotUpdate is 1 entry of the updates list of
// .github/dependabot.yml.
type dependabotUpdate struct {
	Ecosystem string             `yaml:"package-ecosystem"`
	Directory string             `yaml:"directory"`
	Ignore    []dependabotIgnore `yaml:"ignore"`
}

// dependabotIgnore is 1 entry of an update's ignore list; an ignore with
// versions or update types leaves the other updates of the dependency on.
type dependabotIgnore struct {
	Name        string   `yaml:"dependency-name"`
	Versions    []string `yaml:"versions"`
	UpdateTypes []string `yaml:"update-types"`
}

// dependabotFile is the whole of .github/dependabot.yml.
type dependabotFile struct {
	Version int                `yaml:"version"`
	Updates []dependabotUpdate `yaml:"updates"`
}

// ecosystem names the Dependabot package-ecosystem of the manifest file
// base, if base is a manifest.
func ecosystem(base string) (string, bool) {
	switch base {
	case "go.mod":
		return "gomod", true
	case "package.json":
		return "npm", true
	case "Cargo.toml":
		return "cargo", true
	case "Dockerfile", "Containerfile":
		return "docker", true
	}
	return "", false
}

// normalizeDir drops the trailing slash of a Dependabot directory, and keeps
// the root as "/".
func normalizeDir(dir string) string {
	if dir == "/" {
		return dir
	}
	return strings.TrimSuffix(dir, "/")
}

// TestDependabotCoversManifests guards row B13 of frostyard/core
// docs/specs/repository-baseline.md: .github/dependabot.yml has 1 entry for
// each manifest of the repository, go.mod, web/package.json and
// web/src-tauri/Cargo.toml here, and the github-actions entry at "/".
func TestDependabotCoversManifests(t *testing.T) {
	cfg := readDependabot(t)
	if cfg.Version != 2 {
		t.Errorf("version = %d, want 2", cfg.Version)
	}
	covered := make(map[[2]string]bool, len(cfg.Updates))
	actions := false
	for _, u := range cfg.Updates {
		dir := normalizeDir(u.Directory)
		covered[[2]string{u.Ecosystem, dir}] = true
		if u.Ecosystem == "github-actions" && dir == "/" {
			actions = true
		}
	}
	if !actions {
		t.Error("no github-actions entry with directory /")
	}

	manifests := 0
	for _, f := range repoFiles(t, "docs") {
		eco, ok := ecosystem(path.Base(f))
		if !ok {
			continue
		}
		manifests++
		dir := path.Dir(f)
		want := "/"
		if dir != "." {
			want = "/" + dir
		}
		if !covered[[2]string{eco, want}] {
			t.Errorf("%s has no Dependabot entry (package-ecosystem %s, directory %s)", f, eco, want)
		}
	}
	if manifests == 0 {
		t.Fatal("no dependency manifest found")
	}
	t.Logf("%d manifests, %d Dependabot entries", manifests, len(cfg.Updates))
}

// readDependabot reads and parses .github/dependabot.yml.
func readDependabot(t *testing.T) dependabotFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf(".github/dependabot.yml: %v", err)
	}
	var cfg dependabotFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf(".github/dependabot.yml: %v", err)
	}
	return cfg
}

// cargoDependency is 1 dependency of a Cargo.toml: its key, the package
// it renames (the package field, or ""), and its git source.
type cargoDependency struct {
	key, pkg, git string
}

// name is the crate name Dependabot uses: the package of a renamed
// dependency, else the key.
func (d cargoDependency) name() string {
	if d.pkg != "" {
		return d.pkg
	}
	return d.key
}

// internalGitCrates returns the crate name of each dependency of the
// Cargo.toml text data whose git source is a frostyard repository, in the
// order of the file. It reads the TOML forms Cargo.toml dependencies take:
// the dependency sections ([dependencies], [dev-dependencies],
// [build-dependencies], [workspace.dependencies] and their
// [target.<cfg>.…] forms), an inline table, a dependency's own table and
// dotted keys, with quoted keys, both string quotes and comments. Other
// sections, such as [package.metadata.…], are not dependencies. A form it
// cannot read (a multi-line string or inline table) is an error, so a
// dependency is never missed in silence.
func internalGitCrates(data string) ([]string, error) {
	deps := map[string]*cargoDependency{}
	var order []string
	dep := func(key string) *cargoDependency {
		d, ok := deps[key]
		if !ok {
			d = &cargoDependency{key: key}
			deps[key] = d
			order = append(order, key)
		}
		return d
	}
	set := func(d *cargoDependency, field, value string) {
		switch field {
		case "git":
			d.git = value
		case "package":
			d.pkg = value
		}
	}

	inSection, table := false, ""
	n := 0
	for raw := range strings.Lines(data) {
		n++
		line, err := stripTOMLComment(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") {
			inSection, table = false, ""
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: a table header with no ]", n)
			}
			keys, err := splitTOMLKey(line[1 : len(line)-1])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			inSection, table = dependencySection(keys)
			continue
		}
		if !inSection && table == "" {
			continue
		}
		eq := indexOutsideQuotes(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: a line that is no key = value", n)
		}
		keys, err := splitTOMLKey(line[:eq])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		value := strings.TrimSpace(line[eq+1:])
		if table != "" {
			if len(keys) == 1 {
				if str, ok := tomlString(value); ok {
					set(dep(table), keys[0], str)
				}
			}
			continue
		}
		switch {
		case len(keys) == 1 && strings.HasPrefix(value, "{"):
			pairs, err := inlineTable(value)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			d := dep(keys[0])
			for field, str := range pairs {
				set(d, field, str)
			}
		case len(keys) == 2:
			if str, ok := tomlString(value); ok {
				set(dep(keys[0]), keys[1], str)
			}
		}
	}

	var crates []string
	for _, key := range order {
		if d := deps[key]; frostyardGit(d.git) {
			crates = append(crates, d.name())
		}
	}
	return crates, nil
}

// frostyardGit reports whether a git source URL names a repository of the
// frostyard organization on GitHub.
func frostyardGit(url string) bool {
	return strings.Contains(url, "github.com/frostyard/") || strings.Contains(url, "github.com:frostyard/")
}

// dependencySection reports whether the table keys name a dependency
// section, or, as table, the dependency whose own table they name.
func dependencySection(keys []string) (section bool, table string) {
	if len(keys) >= 3 && keys[0] == "target" {
		keys = keys[2:]
	} else if len(keys) >= 2 && keys[0] == "workspace" && keys[1] == "dependencies" {
		keys = keys[1:]
	}
	if len(keys) == 0 {
		return false, ""
	}
	switch keys[0] {
	case "dependencies", "dev-dependencies", "build-dependencies":
	default:
		return false, ""
	}
	switch len(keys) {
	case 1:
		return true, ""
	case 2:
		return false, keys[1]
	}
	return false, ""
}

// stripTOMLComment trims a line and drops its comment, outside strings. A
// multi-line string is an error.
func stripTOMLComment(line string) (string, error) {
	if strings.Contains(line, `"""`) || strings.Contains(line, "'''") {
		return "", errors.New("a multi-line string")
	}
	if i := indexOutsideQuotes(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line), nil
}

// indexOutsideQuotes returns the index of the first c of s that is outside
// a basic ("…", with backslash escapes) or literal ('…') string, or -1.
func indexOutsideQuotes(s string, c byte) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch {
		case quote == '"' && s[i] == '\\':
			i++
		case quote != 0:
			if s[i] == quote {
				quote = 0
			}
		case s[i] == '"' || s[i] == '\'':
			quote = s[i]
		case s[i] == c:
			return i
		}
	}
	return -1
}

// splitOutsideQuotes splits s at each sep outside strings and outside [ ]
// and { } nesting.
func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	var quote byte
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case quote == '"' && s[i] == '\\':
			i++
		case quote != 0:
			if s[i] == quote {
				quote = 0
			}
		case s[i] == '"' || s[i] == '\'':
			quote = s[i]
		case s[i] == '[' || s[i] == '{':
			depth++
		case s[i] == ']' || s[i] == '}':
			depth--
		case s[i] == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// splitTOMLKey splits a dotted key into its parts, each bare or quoted.
func splitTOMLKey(s string) ([]string, error) {
	var keys []string
	for _, part := range splitOutsideQuotes(s, '.') {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("an empty key part in %q", s)
		}
		if str, ok := tomlString(part); ok {
			keys = append(keys, str)
			continue
		}
		if strings.ContainsAny(part, "\"' =[]{}") {
			return nil, fmt.Errorf("a key part %q that is neither bare nor quoted", part)
		}
		keys = append(keys, part)
	}
	return keys, nil
}

// tomlString returns the text of a single-line basic or literal string.
func tomlString(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	switch {
	case s[0] == '\'' && s[len(s)-1] == '\'':
		return s[1 : len(s)-1], true
	case s[0] == '"' && s[len(s)-1] == '"':
		str, err := strconv.Unquote(s)
		return str, err == nil
	}
	return "", false
}

// inlineTable returns the string members of a one-line inline table; a
// table that does not end on its line is an error.
func inlineTable(value string) (map[string]string, error) {
	if !strings.HasSuffix(value, "}") {
		return nil, errors.New("an inline table that does not end on its line")
	}
	pairs := map[string]string{}
	for _, member := range splitOutsideQuotes(value[1:len(value)-1], ',') {
		if strings.TrimSpace(member) == "" {
			continue
		}
		eq := indexOutsideQuotes(member, '=')
		if eq < 0 {
			return nil, fmt.Errorf("an inline table member %q with no =", member)
		}
		keys, err := splitTOMLKey(member[:eq])
		if err != nil {
			return nil, err
		}
		if str, ok := tomlString(strings.TrimSpace(member[eq+1:])); ok && len(keys) == 1 {
			pairs[keys[0]] = str
		}
	}
	return pairs, nil
}

// cargoIgnores returns the (directory, crate) pairs that the cargo entries
// of cfg ignore completely: an ignore with versions or update types does
// not count.
func cargoIgnores(cfg dependabotFile) map[[2]string]bool {
	ignored := make(map[[2]string]bool)
	for _, u := range cfg.Updates {
		if u.Ecosystem != "cargo" {
			continue
		}
		for _, ig := range u.Ignore {
			if len(ig.Versions) == 0 && len(ig.UpdateTypes) == 0 {
				ignored[[2]string{normalizeDir(u.Directory), ig.Name}] = true
			}
		}
	}
	return ignored
}

// TestDependabotLeavesInternalGitCratesAlone: Dependabot cannot resolve a
// cargo git dependency on a frostyard repository whose crate is not at the
// root of every tag (frostyard-problems, from frostyard/std: its tag v0.3.0
// holds no Cargo.toml, so the update job fails). Such a crate is bumped by
// hand, so the cargo entry of its directory ignores it.
func TestDependabotLeavesInternalGitCratesAlone(t *testing.T) {
	ignored := cargoIgnores(readDependabot(t))
	crates := 0
	for _, f := range repoFiles(t, "docs") {
		if path.Base(f) != "Cargo.toml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		dir := "/" + path.Dir(f)
		if path.Dir(f) == "." {
			dir = "/"
		}
		names, err := internalGitCrates(string(data))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, name := range names {
			crates++
			if !ignored[[2]string{dir, name}] {
				t.Errorf("%s: the frostyard git crate %s has no ignore entry in the cargo entry for %s", f, name, dir)
			}
		}
	}
	if crates == 0 {
		t.Fatal("no frostyard git crate found; web/src-tauri/Cargo.toml has frostyard-problems")
	}
	t.Logf("%d frostyard git crates checked", crates)
}

// TestInternalGitCrates checks the Cargo.toml forms of a frostyard git
// crate, the forms that are not one, and the forms it refuses.
func TestInternalGitCrates(t *testing.T) {
	const std = "https://github.com/frostyard/std"
	for _, tc := range []struct {
		name string
		data string
		want []string
	}{
		{"inline", "[dependencies]\nfrostyard-problems = { git = \"" + std + "\", tag = \"v0.5.0\" }\n", []string{"frostyard-problems"}},
		{"inline, tag first, comment", "[dependencies] # deps\nfoo = { tag = \"v1.0.0\", git = \"https://github.com/frostyard/foo\" } # pinned\n", []string{"foo"}},
		{"inline, literal string", "[dependencies]\nextra = { git = 'https://github.com/frostyard/extra' }\n", []string{"extra"}},
		{"inline, quoted key", "[dependencies]\n\"another\" = { git = \"https://github.com/frostyard/another\" }\n", []string{"another"}},
		{"inline, features array", "[dependencies]\nfoo = { git = \"https://github.com/frostyard/foo\", features = [\"a\", \"b\"] }\n", []string{"foo"}},
		{"inline, renamed", "[dependencies]\nproblems = { package = \"frostyard-problems\", git = \"" + std + "\" }\n", []string{"frostyard-problems"}},
		{"dotted keys", "[dependencies]\nfoo.git = \"https://github.com/frostyard/foo\"\nfoo.package = \"frostyard-foo\"\n", []string{"frostyard-foo"}},
		{"table", "[dependencies.frostyard-problems]\ngit = \"" + std + "\"\ntag = \"v0.5.0\"\n", []string{"frostyard-problems"}},
		{"table, comment, renamed", "[dev-dependencies.extra] # helper\npackage = 'frostyard-extra'\ngit = \"https://github.com/frostyard/extra\"\n", []string{"frostyard-extra"}},
		{"build table", "[build-dependencies.bar]\ngit = \"https://github.com/frostyard/bar\"\n", []string{"bar"}},
		{"target section", "[target.'cfg(target_os = \"linux\")'.dependencies]\nfoo = { git = \"https://github.com/frostyard/foo\" }\n", []string{"foo"}},
		{"target table", "[target.\"cfg(unix)\".dev-dependencies.foo]\ngit = \"https://github.com/frostyard/foo\"\n", []string{"foo"}},
		{"workspace", "[workspace.dependencies]\nfoo = { git = \"ssh://git@github.com/frostyard/foo\" }\n", []string{"foo"}},
		{"another organization", "[dependencies]\nbaz = { git = \"https://github.com/example/baz\" }\n", nil},
		{"crates.io", "[dependencies]\nserde = { version = \"1\", features = [\"derive\"] }\ntauri = \"2\"\n", nil},
		{"package section", "[package]\ngit = \"" + std + "\"\n", nil},
		{"metadata", "[package.metadata.example]\nhelper = { git = \"https://github.com/frostyard/helper\" }\n", nil},
		{"a # in a string", "[dependencies]\nfoo = { git = \"https://github.com/frostyard/foo#x\" }\n", []string{"foo"}},
		{"array of tables", "[[bin]]\nname = \"x\"\n[dependencies]\nfoo = { git = \"https://github.com/frostyard/foo\" }\n", []string{"foo"}},
	} {
		got, err := internalGitCrates(tc.data)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: internalGitCrates = %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct{ name, data string }{
		{"multi-line inline table", "[dependencies]\nfoo = {\n  git = \"https://github.com/frostyard/foo\" }\n"},
		{"multi-line string", "[dependencies.foo]\ngit = \"\"\"\nhttps://github.com/frostyard/foo\"\"\"\n"},
		{"header with no ]", "[dependencies\n"},
	} {
		if _, err := internalGitCrates(tc.data); err == nil {
			t.Errorf("%s: internalGitCrates gave no error", tc.name)
		}
	}
}

// TestCargoIgnores checks what counts as a full ignore of a crate.
func TestCargoIgnores(t *testing.T) {
	const crate = "frostyard-problems"
	for _, tc := range []struct {
		name string
		u    dependabotUpdate
		want bool
	}{
		{"full ignore", dependabotUpdate{Ecosystem: "cargo", Directory: "/web/src-tauri", Ignore: []dependabotIgnore{{Name: crate}}}, true},
		{"full ignore, trailing slash", dependabotUpdate{Ecosystem: "cargo", Directory: "/web/src-tauri/", Ignore: []dependabotIgnore{{Name: crate}}}, true},
		{"versions only", dependabotUpdate{Ecosystem: "cargo", Directory: "/web/src-tauri", Ignore: []dependabotIgnore{{Name: crate, Versions: []string{">= 1"}}}}, false},
		{"update types only", dependabotUpdate{Ecosystem: "cargo", Directory: "/web/src-tauri", Ignore: []dependabotIgnore{{Name: crate, UpdateTypes: []string{"version-update:semver-major"}}}}, false},
		{"another ecosystem", dependabotUpdate{Ecosystem: "npm", Directory: "/web/src-tauri", Ignore: []dependabotIgnore{{Name: crate}}}, false},
		{"another directory", dependabotUpdate{Ecosystem: "cargo", Directory: "/web", Ignore: []dependabotIgnore{{Name: crate}}}, false},
	} {
		got := cargoIgnores(dependabotFile{Updates: []dependabotUpdate{tc.u}})[[2]string{"/web/src-tauri", crate}]
		if got != tc.want {
			t.Errorf("%s: ignored = %v, want %v", tc.name, got, tc.want)
		}
	}
}
