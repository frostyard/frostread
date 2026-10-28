// Package content renders an article's page: one HTML document, its images
// inlined as data: URIs. Lodge serves it on /content/ to lodge-content,
// addressed by a MAC under a secret the daemon keeps (ADR-0005, and
// lodge-http.md's content routes); the desktop's window asks for it with
// items.render, which adds a CSP that admits no network source
// (ADR-0008). One renderer makes both pages.
package content

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/sanitize"
	"github.com/frostyard/frostread/internal/store"
)

// ErrTooLarge means an article's page alone is at or over the bound on the
// answer, so items.render answers tooLarge and the reader shows the
// excerpt and a link instead (ADR-0008, The answer stays under the
// transport's limit).
var ErrTooLarge = errors.New("the article is too large to show in the app")

// DefaultLimit is the most bytes an items.render answer may take on the
// wire, its JSON-RPC envelope included. The kit refuses a line of
// jsonrpc.MaxMessageSize (16 MiB), so an answer under 12 MiB keeps 4 MiB
// clear of that limit, whatever the id and the escaping cost.
const DefaultLimit = 12 << 20 // 12 MiB

// EnvelopeAllowance is what the renderer reserves for an items.render
// answer's envelope: {"jsonrpc":"2.0","id":<id>,"result":<result>}, as
// jsonrpc.reply writes it. The fixed part and the line's newline take 34
// bytes, so the allowance holds an encoded id of up to 990 bytes. The kit hands a
// handler no request id, so a longer id is not counted: the shell's
// bridge and readctl send integer ids, and a client that sends a longer
// one may get an answer over 12 MiB. The kit still sends no line of
// jsonrpc.MaxMessageSize or more: it answers internal in its place.
const EnvelopeAllowance = 1 << 10 // 1 KiB

// TagAllowance is what inlining one image costs beyond its data: URI: the
// <img src="…"> around it, and the alt span it replaces.
const TagAllowance = 64

// CSP is the desktop page's content policy, first in its head: no source
// of any kind but data: images and the page's own inline style
// (ADR-0008, Untrusted article content without lodge-content).
const CSP = "default-src 'none'; img-src data:; style-src 'unsafe-inline'"

// Renderer renders an article's page. Lodge's content route and
// items.render both use it.
type Renderer struct {
	Store *store.DB
	Blobs *blobs.Store
	// Location is the time zone an article's date is shown in: the
	// daemon's own, time.Local, when nil. The app formats the same date in
	// the reader's zone, so both show one day.
	Location *time.Location
	// Limit is the most bytes an items.render answer may take on the wire;
	// DefaultLimit when 0. Tests render with small limits.
	Limit int
}

// Handler serves GET and HEAD /content/item/<id>/<mac>: lodge's content
// route.
type Handler struct {
	Renderer
	Secret []byte
}

// Rendered is items.render's result: the page, and how many of its images
// it left out to stay under the bound. It is the wire result itself, so
// the size this package measures is the answer's size.
type Rendered struct {
	HTML          string `json:"html"`
	OmittedImages int    `json:"omittedImages"`
}

// limit is the bound on an answer, DefaultLimit when the renderer names
// none.
func (r *Renderer) limit() int {
	if r.Limit > 0 {
		return r.Limit
	}
	return DefaultLimit
}

// Path is an item's content path relative to the content root:
// item/<id>/<mac>. It has no leading slash and no "content/" segment,
// because lodge-client's contentURL puts them there:
// CONTENT/<app>/<path>. This handler answers /content/<path>. The MAC
// covers the id, so no other item's page can be asked for.
func (h *Handler) Path(id int64) string {
	return "item/" + strconv.FormatInt(id, 10) + "/" + h.mac(id)
}

// mac is base64url(HMAC-SHA256(secret, "item:<id>")) cut to 22 characters:
// 132 bits, which no one can guess.
func (h *Handler) mac(id int64) string {
	sum := hmac.New(sha256.New, h.Secret)
	sum.Write([]byte("item:" + strconv.FormatInt(id, 10)))
	return base64.RawURLEncoding.EncodeToString(sum.Sum(nil))[:22]
}

// Scheme returns the scheme the page follows: "light" or "dark" from the
// scheme query (the shell's theme, which the web app passes), or "" to
// follow the reader's own preference.
func Scheme(r *http.Request) string {
	switch r.URL.Query().Get("scheme") {
	case "light":
		return "light"
	case "dark":
		return "dark"
	}
	return ""
}

// ValidScheme reports whether scheme is one a page follows: "", "light"
// or "dark".
func ValidScheme(scheme string) bool {
	switch scheme {
	case "", "light", "dark":
		return true
	}
	return false
}

// ServeHTTP answers an article's page. A wrong MAC, an unknown item and a
// malformed path all answer 404 alike, so a wrong MAC says nothing.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/content/item/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if !hmac.Equal([]byte(parts[1]), []byte(h.mac(id))) {
		http.NotFound(w, r)
		return
	}
	it, err := h.Store.Item(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "the item could not be read", http.StatusInternalServerError)
		return
	}
	page, err := h.page(r.Context(), it, Scheme(r))
	if err != nil {
		http.Error(w, "the article could not be read", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, _ = w.Write(page)
}

// page is lodge-content's page for an item: the article with every image
// it stored inlined. It has no CSP of its own: lodge adds the sandbox
// policy (ADR-0005).
func (h *Handler) page(ctx context.Context, it store.Item, scheme string) ([]byte, error) {
	images, err := h.Store.ItemBlobs(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	content := replaceBlobs(inlineOnly(h.body(it)), imageResolver(h.Blobs, images))
	return []byte(h.document(it, scheme, false, content)), nil
}

// Render answers items.render for an item: the desktop page, whose CSP is
// first in its head, under the bound on the encoded answer (ADR-0008, The
// answer stays under the transport's limit).
func (r *Renderer) Render(ctx context.Context, it store.Item, scheme string) (Rendered, error) {
	images, err := r.Store.ItemBlobs(ctx, it.ID)
	if err != nil {
		return Rendered{}, fmt.Errorf("render item %d: %w", it.ID, err)
	}
	limit := r.limit()
	body := inlineOnly(r.body(it))

	// Step 1: no image embedded, which is the smallest page the article
	// can take. An image that is not inlined keeps its alt text. Only an
	// image the store holds counts as left out: one it never fetched shows
	// as its alt text in lodge's page too.
	resolve := imageResolver(r.Blobs, images)
	bare, all := imageRewrite(body, func(hash string) (string, bool) { return "", resolve(hash) != "" })
	plain := Rendered{HTML: r.document(it, scheme, true, bare)}
	if answerSize(plain) >= limit {
		return Rendered{}, ErrTooLarge
	}

	// Step 2: inline the images in document order while each one's data:
	// URI still fits what the answer may take.
	budget := limit - answerSize(plain)
	content, omitted := imageRewrite(body, func(hash string) (string, bool) {
		uri := resolve(hash)
		if uri == "" {
			return "", false
		}
		if len(uri)+TagAllowance > budget {
			return "", true
		}
		budget -= len(uri) + TagAllowance
		return uri, false
	})

	// Step 3: measure the answer itself, and fall back to step 1's page
	// when the escaping grew it past the bound.
	out := Rendered{HTML: r.document(it, scheme, true, content), OmittedImages: omitted}
	if answerSize(out) >= limit {
		return Rendered{HTML: plain.HTML, OmittedImages: all}, nil
	}
	return out, nil
}

// answerSize is the bytes an items.render answer takes on the wire: the
// kit's json.Marshal of the result, and the envelope's allowance.
func answerSize(p Rendered) int {
	raw, err := json.Marshal(p)
	if err != nil {
		return math.MaxInt // a result the kit cannot encode is not sent
	}
	return len(raw) + EnvelopeAllowance
}

// body is the article's HTML: the extraction's, or the feed's summary,
// sanitized, when the extraction has no words yet. The extraction's HTML is
// already sanitized by the extractor, and its blob: markers are part of
// it; it is not sanitized again, which would drop them. The feed's summary
// is still untrusted when an item is read before its extraction.
func (r *Renderer) body(it store.Item) string {
	if strings.TrimSpace(it.ContentHTML) == "" || sanitize.PlainText(it.ContentHTML) == "" {
		return sanitize.HTML(it.SummaryHTML)
	}
	return it.ContentHTML
}

// document is the page around an article's HTML. The desktop's page, which
// items.render answers, has its CSP first in its head, so an article's
// frame loads no remote resource of any kind; lodge's page has none, and
// lodge's sandbox policy decides. The two differ by that one line.
func (r *Renderer) document(it store.Item, scheme string, csp bool, content string) string {
	data := struct {
		Scheme  string
		Title   string
		Byline  string
		Site    string
		Date    string
		Content template.HTML
	}{
		Scheme: scheme, Title: it.Title, Byline: it.Author, Site: siteOf(it.URL),
		Date: r.published(it), Content: template.HTML(content),
	}
	var out strings.Builder
	if err := pageTemplate.Execute(&out, data); err != nil {
		return content // a template this fixed cannot fail; keep the article
	}
	page := out.String()
	if !csp {
		return page
	}
	meta := `<meta http-equiv="Content-Security-Policy" content="` + CSP + `">` + "\n"
	return strings.Replace(page, "<head>\n", "<head>\n"+meta, 1)
}

// imageResolver answers an image's data: URI from the item's blobs, reading
// each stored image once, and "" for one the item does not have.
func imageResolver(b *blobs.Store, images map[string]store.Blob) func(hash string) string {
	cache := map[string]string{}
	return func(hash string) string {
		if uri, ok := cache[hash]; ok {
			return uri
		}
		uri := ""
		if img, ok := images[hash]; ok {
			if data, err := b.Get(img.Hash); err == nil {
				uri = "data:" + img.Mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			}
		}
		cache[hash] = uri
		return uri
	}
}

// imageRewrite walks s's blob images in document order, and asks uri what
// each one is: the data: URI to inline it, or "" to leave it out, which
// keeps its alt text as text, as inlineOnly does with an image that is not
// a blob. It returns the HTML and how many images uri said were left out
// to stay under the bound.
func imageRewrite(s string, uri func(hash string) (string, bool)) (string, int) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s, 0
	}
	var images []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			if strings.HasPrefix(attr(n, "src"), "blob:") {
				images = append(images, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	omitted := 0
	for _, n := range images {
		u, left := uri(strings.TrimPrefix(attr(n, "src"), "blob:"))
		if u != "" {
			setAttr(n, "src", u)
			continue
		}
		if left {
			omitted++
		}
		replaceWithAlt(n)
	}
	var out strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return s, omitted
		}
	}
	return out.String(), omitted
}

// replaceWithAlt puts n's alt text where n is, as text, which a page that
// loads no image shows in its place.
func replaceWithAlt(n *html.Node) {
	alt := attr(n, "alt")
	var replacement *html.Node
	if alt != "" {
		replacement = &html.Node{Type: html.ElementNode, Data: "span"}
		replacement.AppendChild(&html.Node{Type: html.TextNode, Data: alt})
	} else {
		replacement = &html.Node{Type: html.TextNode, Data: ""}
	}
	if n.Parent != nil {
		n.Parent.InsertBefore(replacement, n)
		n.Parent.RemoveChild(n)
	}
}

// inlineOnly keeps an image only when its source is a stored blob, which
// replaceBlobs turns into a data: URI: the content policy loads no remote
// resource, so anything else becomes its alt text.
func inlineOnly(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	var images []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			if src := attr(n, "src"); !strings.HasPrefix(src, "blob:") {
				images = append(images, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	for _, n := range images {
		replaceWithAlt(n)
	}
	var out strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return s
		}
	}
	return out.String()
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

// setAttr sets n's attribute key to value, adding it when it has none.
func setAttr(n *html.Node, key, value string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

// replaceBlobs rewrites every blob:<hash> source with what uri answers for
// its hash. A source with no blob is left as it was, which the content
// policy then refuses to load.
func replaceBlobs(html string, uri func(hash string) string) string {
	var b strings.Builder
	for {
		i := strings.Index(html, "blob:")
		if i < 0 {
			b.WriteString(html)
			return b.String()
		}
		b.WriteString(html[:i])
		rest := html[i+len("blob:"):]
		hash, tail := rest[:min(64, len(rest))], rest[min(64, len(rest)):]
		if u := uri(hash); u != "" && len(hash) == 64 {
			b.WriteString(u)
		} else {
			b.WriteString("blob:" + hash)
		}
		html = tail
	}
}

// siteOf is a URL's host, for the article's header.
func siteOf(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// published is the article's date in the daemon's time zone, or "" when it
// has none.
func (r *Renderer) published(it store.Item) string {
	if it.PublishedAt.IsZero() {
		return ""
	}
	loc := r.Location
	if loc == nil {
		loc = time.Local
	}
	return it.PublishedAt.In(loc).Format("2 January 2006")
}

// pageTemplate is the article's page. Its style is inline because the
// content policy admits no stylesheet, and it reads well on a phone.
var pageTemplate = template.Must(template.New("content").Parse(`<!doctype html>
<html lang="en"{{with .Scheme}} data-scheme="{{.}}"{{end}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
/* The page follows the shell's theme when the app names it in ?scheme=,
   and the reader's own preference otherwise. */
:root { color-scheme: light; --fg: #1a1a1a; --bg: #fdfdfc; --muted: #666; --link: #1a56db;
  --code: rgba(127,127,127,.12); --line: rgba(127,127,127,.35); }
@media (prefers-color-scheme: dark) {
  :root { color-scheme: dark; --fg: #e6e6e6; --bg: #17181a; --muted: #a0a4a8; --link: #8ab4f8;
    --code: rgba(127,127,127,.18); --line: rgba(127,127,127,.45); }
}
:root[data-scheme="light"] { color-scheme: light; --fg: #1a1a1a; --bg: #fdfdfc; --muted: #666;
  --link: #1a56db; --code: rgba(127,127,127,.12); --line: rgba(127,127,127,.35); }
:root[data-scheme="dark"] { color-scheme: dark; --fg: #e6e6e6; --bg: #17181a; --muted: #a0a4a8;
  --link: #8ab4f8; --code: rgba(127,127,127,.18); --line: rgba(127,127,127,.45); }
body { margin: 0 auto; padding: 1.25rem; max-width: 42rem; line-height: 1.6;
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
  color: var(--fg); background: var(--bg); }
header { border-bottom: 1px solid var(--line); margin-bottom: 1.5rem; padding-bottom: .75rem; }
h1 { font-size: 1.6rem; line-height: 1.25; margin: .25rem 0 .5rem; }
.byline { color: var(--muted); font-size: .9rem; margin: 0; }
.byline span + span::before { content: " · "; }
img { max-width: 100%; height: auto; }
figure { margin: 1.5rem 0; }
figcaption { font-size: .85rem; color: var(--muted); }
pre { overflow-x: auto; padding: .75rem; background: var(--code); border-radius: .4rem; }
blockquote { margin: 1.5rem 0; padding-left: 1rem; border-left: 3px solid var(--line); }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid var(--line); padding: .35rem .5rem; text-align: left; }
a { color: var(--link); }
</style>
</head>
<body>
<header>
<h1>{{.Title}}</h1>
<p class="byline">{{with .Byline}}<span>{{.}}</span>{{end}}{{with .Site}}<span>{{.}}</span>{{end}}{{with .Date}}<span>{{.}}</span>{{end}}</p>
</header>
<article>
{{.Content}}
</article>
</body>
</html>
`))
