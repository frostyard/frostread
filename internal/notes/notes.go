// Package notes writes an article into the owner's notes vault, through
// notes' own MCP endpoint on lodge's agent socket, with frostread's scoped
// token (ADR-0004). It never opens a vault file: the token's scope is what
// limits where it may write.
//
// It is one of the two packages that import the MCP SDK.
package notes

import (
	"context"
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
