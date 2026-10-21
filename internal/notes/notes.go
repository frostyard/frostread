// Package notes writes an article into the owner's notes vault, through
// notes' own MCP endpoint on lodge's agent socket, with frostread's scoped
// token (ADR-0004). It never opens a vault file: the token's scope is what
// limits where it may write.
//
// It is one of the two packages that import the MCP SDK.
package notes

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
)

// ErrUnavailable means save-as-note has no client: no token, or no vault.
var ErrUnavailable = errors.New("notes: save as a note is not configured")

// ErrTooMany means nine notes of that name exist already.
var ErrTooMany = errors.New("notes: too many notes with that name")

// The states an item's note can be in (docs/specs/api.md, items.noteStatus).
const (
	// StatePresent means the note is at the path the item names.
	StatePresent = "present"
	// StateMoved means the note is elsewhere in the vault, and found.
	StateMoved = "moved"
	// StateMissing means no note holds the item's marker any more.
	StateMissing = "missing"
	// StateUnreachable means notes could not be asked.
	StateUnreachable = "unreachable"
)

// Marker is the frontmatter key every new note holds, whose value names
// the item it was written from. It is how a moved note is found again.
const Marker = "read_item"

// MarkerValue is the marker an item's note holds: the article's rooted
// path in the reader.
func MarkerValue(id int64) string { return "/read/item/" + strconv.FormatInt(id, 10) }

// Status is what notes says about an item's note.
type Status struct {
	State string
	// Path is the note's path when it is present or moved.
	Path string
	// Error says why notes could not answer, when State is unreachable.
	Error string
}

// Client writes notes through notes' MCP endpoint.
type Client struct {
	// HTTP carries frostread's token, lodgeapp's AgentClient in
	// production.
	HTTP *http.Client
	// Endpoint is notes' MCP URL, lodgeapp.AgentURL("notes") in production.
	Endpoint string
	// Folder is where notes go, ending in /, such as "Reading/".
	Folder string
	// VaultID is the notes vault, for the route a note has in lodge.
	VaultID int64
	// LodgeURL, when set, makes the note's link an absolute address.
	LodgeURL string
	// Version is frostread's, sent in the MCP client's initialize.
	Version string
	// Now is the clock, for tests.
	Now func() time.Time
}

// Article is what becomes a note.
type Article struct {
	// ID is the item's id, for the link back to the reader.
	ID int64
	// URL, Title, Author and Site are the article's.
	URL     string
	Title   string
	Author  string
	Site    string
	Content string // the sanitized HTML, with blob: image sources
}

// Route is a note's address in lodge: /notes/<vault>/<path segments>. A
// note's path is relative to the vault, and each segment is escaped.
func Route(vault int64, path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "/notes/" + strconv.FormatInt(vault, 10) + "/" + strings.Join(segments, "/")
}

// Save writes a as a new note and returns its vault path. The note is
// <Folder><YYYY-MM-DD> <title>.md; when that exists, " 2" to " 9" is
// tried.
func (c *Client) Save(ctx context.Context, a Article) (string, error) {
	if c == nil || c.HTTP == nil || c.Endpoint == "" || c.VaultID == 0 {
		return "", ErrUnavailable
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	day := now().UTC().Format("2006-01-02")
	base := c.Folder + day + " " + safeTitle(a.Title) + ".md"
	content, err := note(a, c.LodgeURL, now())
	if err != nil {
		return "", err
	}
	for n := 1; n <= 9; n++ {
		path := base
		if n > 1 {
			path = strings.TrimSuffix(base, ".md") + " " + strconv.Itoa(n) + ".md"
		}
		res, err := c.createNote(ctx, path, content)
		if err != nil {
			return "", err
		}
		if !res.IsError {
			return path, nil
		}
		if !strings.Contains(strings.ToLower(text(res)), "exists") {
			return "", fmt.Errorf("notes refused %s: %s", path, text(res))
		}
	}
	return "", fmt.Errorf("%s: %w", base, ErrTooMany)
}

// session opens one MCP session to notes, and the caller closes it.
func (c *Client) session(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "frostread", Version: c.Version}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: c.Endpoint, HTTPClient: c.HTTP, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to notes: %w", err)
	}
	return sess, nil
}

// Find looks for an item's note: at path first, then by the marker in every
// note's frontmatter, and at last in the notes under the folder, whose
// "Open in Read" link names the item. It reads at most 50 notes there.
// A failure to reach notes is unreachable, never missing.
func (c *Client) Find(ctx context.Context, path string, itemID int64) Status {
	if c == nil || c.HTTP == nil || c.Endpoint == "" || c.VaultID == 0 {
		return Status{State: StateUnreachable, Path: path, Error: ErrUnavailable.Error()}
	}
	sess, err := c.session(ctx)
	if err != nil {
		return Status{State: StateUnreachable, Path: path, Error: err.Error()}
	}
	defer func() { _ = sess.Close() }()
	marker := MarkerValue(itemID)

	if path != "" {
		present, err := c.readNote(ctx, sess, path)
		if err != nil {
			return Status{State: StateUnreachable, Path: path, Error: err.Error()}
		}
		if present {
			return Status{State: StatePresent, Path: path}
		}
	}
	found, err := c.findByMarker(ctx, sess, marker)
	if err != nil {
		return Status{State: StateUnreachable, Path: path, Error: err.Error()}
	}
	if found != "" {
		return Status{State: StateMoved, Path: found}
	}
	found, err = c.findByLink(ctx, sess, marker)
	if err != nil {
		return Status{State: StateUnreachable, Path: path, Error: err.Error()}
	}
	if found != "" {
		return Status{State: StateMoved, Path: found}
	}
	return Status{State: StateMissing}
}

// readNote reports whether notes has a note at path. A missing note is
// false with no error; notes being unreachable is an error.
func (c *Client) readNote(ctx context.Context, sess *mcp.ClientSession, path string) (bool, error) {
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "read_note",
		Arguments: map[string]any{"path": path}})
	if err != nil {
		return false, fmt.Errorf("notes' read_note: %w", err)
	}
	if !res.IsError {
		return true, nil
	}
	if missingNote(text(res)) {
		return false, nil
	}
	return false, fmt.Errorf("notes' read_note: %s", text(res))
}

// missingNote reports whether a tool error says the note does not exist,
// rather than notes failing to answer.
func missingNote(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "not found") || strings.Contains(t, "notfound") || strings.Contains(t, "does not exist")
}

// findByMarker lists the notes whose read_item property is marker: notes'
// list_notes filters on a frontmatter key's value, case folded.
func (c *Client) findByMarker(ctx context.Context, sess *mcp.ClientSession, marker string) (string, error) {
	var out struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_notes",
		Arguments: map[string]any{"property": Marker, "value": marker, "limit": 10}})
	if err != nil {
		return "", fmt.Errorf("notes' list_notes: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("notes' list_notes: %s", text(res))
	}
	if err := decodeStructured(res, &out); err != nil {
		return "", err
	}
	if len(out.Notes) > 0 {
		return out.Notes[0].Path, nil
	}
	return "", nil
}

// findByLink looks in the folder's notes for one that names the item, for
// notes written before the marker: it reads at most 50 of them.
func (c *Client) findByLink(ctx context.Context, sess *mcp.ClientSession, marker string) (string, error) {
	var out struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_notes",
		Arguments: map[string]any{"folder": c.Folder, "limit": maxFallback}})
	if err != nil {
		return "", fmt.Errorf("notes' list_notes: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("notes' list_notes: %s", text(res))
	}
	if err := decodeStructured(res, &out); err != nil {
		return "", err
	}
	for _, note := range out.Notes {
		if len(out.Notes) > maxFallback {
			break
		}
		read, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "read_note",
			Arguments: map[string]any{"path": note.Path}})
		if err != nil {
			return "", fmt.Errorf("notes' read_note: %w", err)
		}
		if read.IsError {
			continue // a note that vanished between the list and the read
		}
		var got struct {
			Content string `json:"content"`
		}
		if err := decodeStructured(read, &got); err != nil {
			return "", err
		}
		if strings.Contains(got.Content, marker) {
			return note.Path, nil
		}
	}
	return "", nil
}

// maxFallback is how many of the folder's notes findByLink reads.
const maxFallback = 50

// decodeStructured reads a tool result's structured content into out.
func decodeStructured(res *mcp.CallToolResult, out any) error {
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return fmt.Errorf("notes' answer: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("notes' answer: %w", err)
	}
	return nil
}

// createNote calls notes' create_note once, on a session of its own.
func (c *Client) createNote(ctx context.Context, path, content string) (*mcp.CallToolResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "frostread", Version: c.Version}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: c.Endpoint, HTTPClient: c.HTTP, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to notes: %w", err)
	}
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "create_note",
		Arguments: map[string]any{"path": path, "content": content}})
	if err != nil {
		return nil, fmt.Errorf("notes' create_note: %w", err)
	}
	return res, nil
}

// note is the Markdown a note holds: the frontmatter, the title, the link
// back to the reader, and the article's Markdown.
func note(a Article, lodgeURL string, now time.Time) (string, error) {
	body, err := md.ConvertString(imagesAsLinks(a.Content))
	if err != nil {
		return "", fmt.Errorf("markdown: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "%s: %s\n", Marker, MarkerValue(a.ID))
	fmt.Fprintf(&b, "source: %s\n", yaml(a.URL))
	fmt.Fprintf(&b, "saved: %s\n", now.UTC().Format("2006-01-02"))
	if a.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", yaml(a.Author))
	}
	if a.Site != "" {
		fmt.Fprintf(&b, "site: %s\n", yaml(a.Site))
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(a.Title))
	link := "/read/item/" + strconv.FormatInt(a.ID, 10)
	if lodgeURL != "" {
		link = strings.TrimSuffix(lodgeURL, "/") + link
	}
	fmt.Fprintf(&b, "[Open in Read](%s)\n\n", link)
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String(), nil
}

// imagesAsLinks turns each image into a link to its original address, as
// text: notes cannot fetch an image frostread holds, and the article's own
// link, which the extraction kept, is the best reference.
func imagesAsLinks(content string) string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return content
	}
	var images []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			images = append(images, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	for _, n := range images {
		alt := attr(n, "alt")
		label := "image"
		if alt != "" {
			label += ": " + alt
		}
		// The extraction wraps an image in a link to its original; that
		// link replaces the image whole, so no empty anchor is left.
		target := n
		for p := n.Parent; p != nil && p.Type == html.ElementNode && p.Data == "a"; p = p.Parent {
			target = p
		}
		var replacement *html.Node
		if href := original(n); href != "" {
			replacement = &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: href}}}
			replacement.AppendChild(&html.Node{Type: html.TextNode, Data: label})
		} else {
			replacement = &html.Node{Type: html.TextNode, Data: label}
		}
		replace(target, replacement)
	}
	var b strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return content
		}
	}
	return b.String()
}

// original is the http or https link an image is inside, or "".
func original(n *html.Node) string {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode || p.Data != "a" {
			continue
		}
		u, err := url.Parse(attr(p, "href"))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			return u.String()
		}
	}
	return ""
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func replace(old, next *html.Node) {
	if old.Parent == nil {
		return
	}
	old.Parent.InsertBefore(next, old)
	old.Parent.RemoveChild(old)
}

// yaml quotes a value a YAML reader would otherwise misread.
func yaml(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, "\"'{}[],&*!|>%@`#") || strings.Contains(s, ": ") ||
		strings.HasPrefix(s, "-") || strings.HasPrefix(s, "?") {
		return strconv.Quote(s)
	}
	return s
}

// safeTitle makes a title a file name: no reserved characters, no leading
// dot, NFC, at most 80 characters.
func safeTitle(title string) string {
	title = norm.NFC.String(strings.Join(strings.Fields(title), " "))
	replacer := strings.NewReplacer("/", "-", `\`, "-", ":", "-", "*", "-", "?", "-",
		"\"", "'", "<", "(", ">", ")", "|", "-")
	title = replacer.Replace(title)
	title = strings.TrimLeft(title, ".")
	title = strings.TrimSpace(title)
	if r := []rune(title); len(r) > 80 {
		title = strings.TrimSpace(string(r[:80]))
	}
	if title == "" {
		return "Untitled"
	}
	return title
}

// text is a tool result's text content, joined.
func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
