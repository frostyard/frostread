package readapi_test

import (
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/frostyard/frostread/api"
)

// items.exportMarkdown is a local caller's: the article as the Markdown
// file a desktop user saves, without the note's marker and link.
func TestExportMarkdown(t *testing.T) {
	s := seed(t)
	id := itoa(s.items["Planting tomatoes after the last frost"])
	local := api.WithCaller(t.Context(), api.CallerLocal)
	out, err := s.router.Dispatch(local, "items.exportMarkdown", jsontext.Value(`{"id":`+id+`}`))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(api.ItemMarkdown)
	if !ok {
		t.Fatalf("items.exportMarkdown = %#v", out)
	}
	if res.Filename != "2026-10-20 Planting tomatoes after the last frost.md" {
		t.Fatalf("filename = %q", res.Filename)
	}
	for _, want := range []string{
		"---\nsource: https://example.com/tomatoes\nsaved: 2026-10-20\nauthor: Ann Author\n" +
			"site: Example Journal\n---\n\n",
		"# Planting tomatoes after the last frost\n\n",
		"Plant tomatoes after the last frost",
	} {
		if !strings.Contains(res.Markdown, want) {
			t.Fatalf("the markdown lacks %q:\n%s", want, res.Markdown)
		}
	}
	for _, unwanted := range []string{"read_item", "[Open in Read]", "Reading/"} {
		if strings.Contains(res.Markdown, unwanted) {
			t.Fatalf("the markdown keeps %q:\n%s", unwanted, res.Markdown)
		}
	}

	// A browser session may not export: it saves a note, in lodge mode.
	session := api.WithCaller(t.Context(), api.CallerSession)
	if _, err := s.router.Dispatch(session, "items.exportMarkdown", jsontext.Value(`{"id":`+id+`}`)); !errors.Is(err, api.ErrMethodNotFound) {
		t.Fatalf("items.exportMarkdown as a session = %v, want methodNotFound", err)
	}
}

// items.exportMarkdown refuses an item with no words, and an unknown id.
func TestExportMarkdownRefusals(t *testing.T) {
	s := seed(t)
	local := api.WithCaller(t.Context(), api.CallerLocal)
	// "An item with no words" has neither an extraction nor a summary. An
	// article whose extraction failed keeps its summary, so it exports that,
	// as the content page shows it.
	empty := s.items["An item with no words"]
	_, err := s.router.Dispatch(local, "items.exportMarkdown", jsontext.Value(`{"id":`+itoa(empty)+`}`))
	if code := codeOf(t, err); code != api.CodeUnavailable {
		t.Fatalf("an item with no words = %v, want unavailable", err)
	}
	if _, err := s.router.Dispatch(local, "items.exportMarkdown",
		jsontext.Value(`{"id":`+itoa(s.items["A page that fails"])+`}`)); err != nil {
		t.Fatalf("an item whose extraction failed = %v, want its summary's export", err)
	}
	if _, err := s.router.Dispatch(local, "items.exportMarkdown", jsontext.Value(`{"id":9999}`)); codeOf(t, err) != api.CodeNotFound {
		t.Fatalf("an unknown item = %v, want notFound", err)
	}
}

// items.render answers a page for a session, refuses a scheme the page does
// not follow, and refuses an unknown item.
func TestRenderRefusals(t *testing.T) {
	s := seed(t)
	session := api.WithCaller(t.Context(), api.CallerSession)
	id := itoa(s.items["Planting tomatoes after the last frost"])
	out, err := s.router.Dispatch(session, "items.render", jsontext.Value(`{"id":`+id+`,"scheme":"dark"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(api.ItemRender)
	if !ok {
		t.Fatalf("items.render = %#v", out)
	}
	if !strings.Contains(res.HTML, `<meta http-equiv="Content-Security-Policy"`) {
		t.Fatalf("the page has no CSP meta:\n%s", res.HTML)
	}
	if _, err := s.router.Dispatch(session, "items.render", jsontext.Value(`{"id":`+id+`,"scheme":"sepia"}`)); codeOf(t, err) != api.CodeInvalidParams {
		t.Fatalf("scheme sepia = %v, want invalidParams", err)
	}
	if _, err := s.router.Dispatch(session, "items.render", jsontext.Value(`{"id":9999}`)); codeOf(t, err) != api.CodeNotFound {
		t.Fatalf("an unknown item = %v, want notFound", err)
	}
}

// codeOf is err's contract code, or 0.
func codeOf(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("%v is not a contract error", err)
	}
	return e.Code
}
