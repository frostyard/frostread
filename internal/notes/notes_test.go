package notes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createIn struct {
	Path    string `json:"path" jsonschema:"the note's path"`
	Content string `json:"content" jsonschema:"the note's markdown"`
}

type created struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

// fakeNotes is notes' side: create_note, refusing a path that exists.
type fakeNotes struct {
	mu   sync.Mutex
	have map[string]string // path -> content
}

func (f *fakeNotes) server(t *testing.T) *httptest.Server {
	t.Helper()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		s := mcp.NewServer(&mcp.Implementation{Name: "fake-notes"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "create_note", Description: "Create a note."},
			func(_ context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, created, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				if _, ok := f.have[in.Path]; ok {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
						&mcp.TextContent{Text: "invalidParams: " + in.Path + " exists"}}}, created{}, nil
				}
				f.have[in.Path] = in.Content
				return nil, created{Path: in.Path, Created: true}, nil
			})
		return s
	}, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeNotes) content(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.have[path]
}

func article() Article {
	return Article{
		ID: 7, URL: "https://example.com/garden", Title: "Gardening in the spring",
		Author: "Ann Author", Site: "Example Journal",
		Content: `<p>Plant <strong>tomatoes</strong> after the frost.</p>
			<p><a href="https://example.com/tomato.png"><img src="blob:abc" alt="A ripe tomato"></a></p>
			<p><img src="blob:def" alt="An unlinked picture"></p>`,
	}
}

func TestSave(t *testing.T) {
	f := &fakeNotes{have: map[string]string{}}
	srv := f.server(t)
	c := &Client{HTTP: srv.Client(), Endpoint: srv.URL, Folder: "Reading/", VaultID: 1,
		Version: "test", Now: func() time.Time { return time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC) }}
	path, err := c.Save(t.Context(), article())
	if err != nil {
		t.Fatal(err)
	}
	want := "Reading/2026-10-21 Gardening in the spring.md"
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	body := f.content(path)
	for _, want := range []string{
		"---\nsource: https://example.com/garden\nsaved: 2026-10-21\nauthor: Ann Author\nsite: Example Journal\n---",
		"# Gardening in the spring",
		"[Open in Read](/read/item/7)",
		"[image: A ripe tomato](https://example.com/tomato.png)",
		"image: An unlinked picture",
		"**tomatoes**",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the note lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "blob:") {
		t.Fatalf("the note keeps a blob marker:\n%s", body)
	}
}

func TestSaveAgainGetsANumber(t *testing.T) {
	f := &fakeNotes{have: map[string]string{}}
	srv := f.server(t)
	c := &Client{HTTP: srv.Client(), Endpoint: srv.URL, Folder: "Reading/", VaultID: 1,
		Now: func() time.Time { return time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC) }}
	first, err := c.Save(t.Context(), article())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Save(t.Context(), article())
	if err != nil {
		t.Fatal(err)
	}
	if second != "Reading/2026-10-21 Gardening in the spring 2.md" {
		t.Fatalf("second path = %q", second)
	}
	if first == second {
		t.Fatal("the two notes share a path")
	}
}

func TestSaveWithALodgeURL(t *testing.T) {
	f := &fakeNotes{have: map[string]string{}}
	srv := f.server(t)
	c := &Client{HTTP: srv.Client(), Endpoint: srv.URL, Folder: "Reading/", VaultID: 1,
		LodgeURL: "https://lodge.example.ts.net", Now: time.Now}
	path, err := c.Save(t.Context(), article())
	if err != nil {
		t.Fatal(err)
	}
	if body := f.content(path); !strings.Contains(body, "[Open in Read](https://lodge.example.ts.net/read/item/7)") {
		t.Fatalf("the note's link is not lodge's:\n%s", body)
	}
}

func TestSaveWithoutAToken(t *testing.T) {
	var c *Client
	if _, err := c.Save(t.Context(), article()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no client = %v, want ErrUnavailable", err)
	}
	c = &Client{HTTP: http.DefaultClient, Endpoint: "http://lodge/mcp/notes", Folder: "Reading/"}
	if _, err := c.Save(t.Context(), article()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no vault = %v, want ErrUnavailable", err)
	}
}

func TestSafeTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Gardening in the spring", "Gardening in the spring"},
		{`A/B: C*D?E"F<G>H|I\J`, "A-B- C-D-E'F(G)H-I-J"},
		{"...dots", "dots"},
		{"  spaced   out  ", "spaced out"},
		{"", "Untitled"},
		{"日本語のタイトル", "日本語のタイトル"},
		{strings.Repeat("long ", 40), strings.TrimSpace(strings.Repeat("long ", 16))},
	} {
		if got := safeTitle(tc.in); got != tc.want {
			t.Fatalf("safeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := []rune(safeTitle(strings.Repeat("é", 100))); len(got) != 80 {
		t.Fatalf("a long title is %d runes, want 80", len(got))
	}
}

func TestRoute(t *testing.T) {
	got := Route(2, "Reading/2026-10-21 A tomato.md")
	want := "/notes/2/Reading/2026-10-21%20A%20tomato.md"
	if got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
}
