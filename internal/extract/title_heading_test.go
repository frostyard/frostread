package extract

import (
	"strings"
	"testing"

	"github.com/frostyard/frostread/internal/blobs"
)

// words is real article text, long enough for readability (220 characters).
var words = strings.Repeat("<p>Useful words about a made-up article, enough of them for the extraction to keep.</p>", 6)

func TestDropLeadingTitle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		title   string
		keeps   bool // the heading stays
	}{
		{"the heading repeats the title", `<h1>Making bread on a Sunday</h1><p>Bread is flour.</p>`, "Making bread on a Sunday", false},
		{"case and spaces fold", `<h2>  making   BREAD on a Sunday </h2><p>Bread is flour.</p>`, "Making Bread on a Sunday", false},
		{"through a wrapper", `<div><h1>Making bread on a Sunday</h1></div><p>Bread is flour.</p>`, "Making bread on a Sunday", false},
		{"a paragraph comes first", `<p>An introduction.</p><h1>Making bread on a Sunday</h1>`, "Making bread on a Sunday", true},
		{"another heading", `<h1>A different heading</h1><p>Bread is flour.</p>`, "Making bread on a Sunday", true},
		{"a site name", `<h1>The Go Blog</h1><h2>Better reuse</h2>`, "Better reuse", true},
		{"no title", `<h1>Anything</h1>`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DropLeadingTitle(tc.content, tc.title)
			if has := strings.Contains(got, "<h1>") || strings.Contains(got, "<h2>"); has != tc.keeps {
				t.Fatalf("heading kept = %v, want %v:\n%s", has, tc.keeps, got)
			}
		})
	}
}

// Both paths drop a body heading that only repeats the title.
func TestTheInlinePathDropsTheTitleHeading(t *testing.T) {
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{}}
	content, _, err := x.Inline(t.Context(),
		`<h1>Making bread on a Sunday</h1><p>Bread is flour, water, salt and time.</p>`,
		"https://example.com/bread", "Making bread on a Sunday")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(content), "<h1>") {
		t.Fatalf("the inline content keeps its heading:\n%s", content)
	}
	if !strings.Contains(content, "Bread is flour") {
		t.Fatalf("the inline content lost its words:\n%s", content)
	}
}

func TestThePagePathDropsTheTitleHeading(t *testing.T) {
	page := `<!doctype html><html><head><title>Making bread on a Sunday</title></head><body>
<article><h1>Making bread on a Sunday</h1>` + words + `</article></body></html>`
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{}}
	got, err := x.Extract(t.Context(), []byte(page), "https://example.com/bread")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(got.ContentHTML), "<h1>") {
		t.Fatalf("the article keeps its heading:\n%s", got.ContentHTML)
	}
}
