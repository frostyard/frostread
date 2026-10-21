package notes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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

type pathIn struct {
	Path string `json:"path" jsonschema:"the note's path"`
}

type listIn struct {
	Folder   string `json:"folder,omitzero"`
	Property string `json:"property,omitzero"`
	Value    string `json:"value,omitzero"`
	Limit    int    `json:"limit,omitzero"`
}

type listEntry struct {
	Path string `json:"path"`
}

type listNotesOut struct {
	Notes []listEntry `json:"notes"`
}

type readNoteOut struct {
	Path    string `json:"path"`
	Content string `json:"content"`
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
		mcp.AddTool(s, &mcp.Tool{Name: "read_note", Description: "Read a note."},
			func(_ context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, readNoteOut, error) {
				f.mu.Lock()
				content, ok := f.have[in.Path]
				f.mu.Unlock()
				if !ok {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
						&mcp.TextContent{Text: "notFound: note " + in.Path + " does not exist"}}}, readNoteOut{}, nil
				}
				return nil, readNoteOut{Path: in.Path, Content: content}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "list_notes", Description: "List notes."},
			func(_ context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listNotesOut, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				out := listNotesOut{Notes: []listEntry{}}
				for path, content := range f.have {
					if in.Property != "" && !strings.Contains(content, in.Property+": "+in.Value) {
						continue
					}
					out.Notes = append(out.Notes, listEntry{Path: path})
				}
				slices.SortFunc(out.Notes, func(a, b listEntry) int { return strings.Compare(a.Path, b.Path) })
				return nil, out, nil
			})
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
		"---\nread_item: /read/item/7\nsource: https://example.com/garden\nsaved: 2026-10-21\nauthor: Ann Author\nsite: Example Journal\n---",
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

// Find answers present, missing or moved from what notes holds, and never
// calls a failure to answer missing.
func TestFind(t *testing.T) {
	f := &fakeNotes{have: map[string]string{
		"Reading/kept.md":   "---\nread_item: /read/item/7\n---\n\n# Kept\n",
		"Reading/linked.md": "# Linked\n\n[Open in Read](/read/item/9)\n",
		"Other/moved.md":    "---\nread_item: /read/item/8\n---\n\n# Moved\n",
	}}
	srv := f.server(t)
	c := &Client{HTTP: srv.Client(), Endpoint: srv.URL, Folder: "Reading/", VaultID: 1, Version: "test"}

	for _, tc := range []struct {
		name string
		path string
		id   int64
		want string
		got  string
	}{
		{"present", "Reading/kept.md", 7, StatePresent, "Reading/kept.md"},
		{"missing", "Reading/gone.md", 42, StateMissing, ""},
		{"moved by its marker", "Reading/gone.md", 8, StateMoved, "Other/moved.md"},
		{"moved, an older note by its link", "Reading/gone.md", 9, StateMoved, "Reading/linked.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := c.Find(t.Context(), tc.path, tc.id)
			if st.State != tc.want || st.Path != tc.got {
				t.Fatalf("Find(%q, %d) = %+v, want %s %q", tc.path, tc.id, st, tc.want, tc.got)
			}
		})
	}

	// Notes out of reach is unreachable, with the path kept.
	dead := &Client{HTTP: &http.Client{Timeout: time.Second}, Endpoint: "http://127.0.0.1:1/mcp",
		Folder: "Reading/", VaultID: 1}
	st := dead.Find(t.Context(), "Reading/kept.md", 7)
	if st.State != StateUnreachable || st.Path != "Reading/kept.md" || st.Error == "" {
		t.Fatalf("unreachable = %+v", st)
	}
}
