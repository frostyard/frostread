// Package extract turns a page into an article: readability finds its main
// content, the sanitizer makes it safe, and each image becomes a stored
// blob whose source the page names (ADR-0005). It is the only package that
// imports a readability port.
package extract

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"

	"golang.org/x/net/html"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/sanitize"
	"github.com/frostyard/frostread/internal/store"
)

// The image bounds of one item.
const (
	// MaxImages is the most images one article keeps.
	MaxImages = 60
	// MinText is the extracted text of an article; less is not an article.
	MinText = 220
)

// Article is what extraction made of a page.
type Article struct {
	Title       string
	Byline      string
	SiteName    string
	Published   time.Time
	ContentHTML string
	Text        string
	Excerpt     string
	// Images are the blobs ContentHTML inlines with blob: markers.
	Images []store.Blob
}

// Extractor extracts articles, fetching their images through Fetch and
// storing them in Blobs.
type Extractor struct {
	Fetch fetch.Fetcher
	Blobs *blobs.Store
}

// ErrNoArticle means the page has no readable article.
var ErrNoArticle = fmt.Errorf("extract: no article on the page")

// Extract reads body, a page at pageURL, as an article. It runs
// readability, sanitizes the result, fetches each image and rewrites its
// source to blob:<hash>.
func (e *Extractor) Extract(ctx context.Context, body []byte, pageURL string) (Article, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return Article{}, fmt.Errorf("extract %s: %w", pageURL, err)
	}
	art, err := readability.FromReader(bytes.NewReader(body), u)
	if err != nil {
		return Article{}, fmt.Errorf("extract %s: %w: %v", pageURL, ErrNoArticle, err)
	}
	var buf bytes.Buffer
	if err := art.RenderHTML(&buf); err != nil {
		return Article{}, fmt.Errorf("extract %s: render: %w", pageURL, err)
	}
	clean := sanitize.HTML(absolutize(buf.String(), pageURL))
	text := sanitize.PlainText(clean)
	if len([]rune(text)) < MinText {
		return Article{}, fmt.Errorf("extract %s: %d characters: %w", pageURL, len([]rune(text)), ErrNoArticle)
	}
	title := strings.TrimSpace(art.Title())
	// The heading readability keeps in the text is the title the reader
	// shows above it: drop the copy.
	text = StripTitle(text, title)
	published, _ := art.PublishedTime()
	out := Article{
		Title:     title,
		Byline:    strings.TrimSpace(art.Byline()),
		SiteName:  strings.TrimSpace(art.SiteName()),
		Published: published,
		Text:      text,
		Excerpt:   excerpt(text),
	}
	out.ContentHTML, out.Images, err = e.Images(ctx, clean, pageURL)
	if err != nil {
		return Article{}, err
	}
	return out, nil
}

// Inline cleans an article the feed already gave in full: it sanitizes the
// HTML, and its images are stored and rewritten as Extract's are.
func (e *Extractor) Inline(ctx context.Context, untrusted, pageURL string) (content string, images []store.Blob, err error) {
	clean := sanitize.HTML(absolutize(untrusted, pageURL))
	return e.Images(ctx, clean, pageURL)
}

// absolutize resolves every relative URL an article keeps against its
// page, before the sanitizer sees them: the policy admits absolute http
// and https URLs only, so a relative source would be dropped.
func absolutize(untrusted, pageURL string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return untrusted
	}
	doc, err := html.Parse(strings.NewReader(untrusted))
	if err != nil {
		return untrusted
	}
	targets := map[string][]string{
		"a": {"href"}, "img": {"src"}, "blockquote": {"cite"}, "q": {"cite"},
		"del": {"cite"}, "ins": {"cite"},
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, key := range targets[n.Data] {
				if v := attr(n, key); v != "" {
					if ref, err := url.Parse(v); err == nil {
						setAttr(n, key, base.ResolveReference(ref).String())
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	var out strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return untrusted
		}
	}
	return out.String()
}

// Images stores each image an article names and rewrites its source to
// blob:<hash>. A failed image keeps its alt text and loses its source. An
// image that is not inside a link is wrapped in one to its original URL,
// so a note can keep the reference (ADR-0004).
func (e *Extractor) Images(ctx context.Context, cleanHTML, pageURL string) (string, []store.Blob, error) {
	doc, err := html.Parse(strings.NewReader(cleanHTML))
	if err != nil {
		return cleanHTML, nil, fmt.Errorf("extract images: %w", err)
	}
	base, _ := url.Parse(pageURL)
	var images []store.Blob
	seen := map[string]string{} // url -> hash
	var total int64
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			src, alt := attr(n, "src"), attr(n, "alt")
			if src == "" || ctx.Err() != nil {
				return
			}
			// Resolve the source, and admit only http and https.
			ref, err := url.Parse(src)
			if err != nil {
				replaceWithAlt(n, alt)
				return
			}
			if base != nil {
				ref = base.ResolveReference(ref)
			}
			if ref.Scheme != "http" && ref.Scheme != "https" {
				replaceWithAlt(n, alt)
				return
			}
			hash, ok := seen[ref.String()]
			var blob store.Blob
			if ok {
				blob.Hash = hash
			} else {
				if len(images) >= MaxImages {
					replaceWithAlt(n, alt)
					return
				}
				b, err := e.fetchImage(ctx, ref.String())
				if err != nil || b.Hash == "" {
					replaceWithAlt(n, alt)
					return
				}
				if total+b.Size > fetch.MaxItemImages {
					replaceWithAlt(n, alt)
					return
				}
				total += b.Size
				seen[ref.String()] = b.Hash
				blob = b
				images = append(images, b)
			}
			setAttr(n, "src", "blob:"+blob.Hash)
			// Keep the original address reachable for a note.
			if !insideAnchor(n) {
				wrapInAnchor(n, ref.String())
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	var out strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return cleanHTML, nil, fmt.Errorf("extract images: %w", err)
		}
	}
	return out.String(), images, nil
}

// fetchImage fetches one image, keeps it when it is an image the browser
// can show, and stores it.
func (e *Extractor) fetchImage(ctx context.Context, src string) (store.Blob, error) {
	res, err := e.Fetch.Get(ctx, src, fetch.Options{MaxBytes: fetch.MaxImage, Accept: "image/*"})
	if err != nil {
		return store.Blob{}, err
	}
	if res.Status != http.StatusOK {
		return store.Blob{}, fmt.Errorf("image %s answers %d", src, res.Status)
	}
	mime := Sniff(res.Body)
	if mime == "" {
		return store.Blob{}, fmt.Errorf("image %s is not an image the reader shows", src)
	}
	hash, err := e.Blobs.Put(ctx, bytes.NewReader(res.Body), mime)
	if err != nil {
		return store.Blob{}, err
	}
	return store.Blob{Hash: hash, Mime: mime, Size: int64(len(res.Body))}, nil
}

// supported are the image types an article keeps. SVG is not one: it can
// carry script.
var supported = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
}

// Sniff returns the MIME type of an image body, or "" when it is not one
// the reader shows.
func Sniff(body []byte) string {
	mime, _, _ := strings.Cut(http.DetectContentType(body), ";")
	if mime == "application/octet-stream" && isAVIF(body) {
		return "image/avif"
	}
	if !supported[mime] {
		return ""
	}
	return mime
}

// isAVIF reports whether body begins with an ISO-BMFF ftyp box naming avif.
func isAVIF(body []byte) bool {
	if len(body) < 12 || string(body[4:8]) != "ftyp" {
		return false
	}
	return bytes.Contains(body[8:min(32, len(body))], []byte("avif"))
}

// excerpt is a text's first 300 characters, cut at a word when it can be.
func excerpt(text string) string {
	r := []rune(text)
	if len(r) <= 300 {
		return text
	}
	cut := string(r[:300])
	if i := strings.LastIndexByte(cut, ' '); i > 200 {
		cut = cut[:i]
	}
	return cut + "…"
}

// attr returns n's attribute key, or "".
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

// replaceWithAlt turns a failed image into its alt text.
func replaceWithAlt(n *html.Node, alt string) {
	span := &html.Node{Type: html.ElementNode, Data: "span"}
	if alt != "" {
		span.AppendChild(&html.Node{Type: html.TextNode, Data: alt})
	}
	replaceNode(n, span)
}

// insideAnchor reports whether n's parent is an a element.
func insideAnchor(n *html.Node) bool {
	return n.Parent != nil && n.Parent.Type == html.ElementNode && n.Parent.Data == "a"
}

// wrapInAnchor puts n inside a link to href, in n's parent.
func wrapInAnchor(n *html.Node, href string) {
	if n.Parent == nil {
		return
	}
	a := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{
		{Key: "href", Val: href}, {Key: "target", Val: "_blank"},
		{Key: "rel", Val: "noopener noreferrer"},
	}}
	n.Parent.InsertBefore(a, n)
	n.Parent.RemoveChild(n)
	a.AppendChild(n)
}

// replaceNode puts next where old was.
func replaceNode(old, next *html.Node) {
	if old.Parent == nil {
		return
	}
	old.Parent.InsertBefore(next, old)
	old.Parent.RemoveChild(old)
}
