// Package content serves an article's page to lodge-content: one HTML
// document, its images inlined as data: URIs, addressed by a MAC under a
// secret the daemon keeps (ADR-0005, and lodge-http.md's content routes).
package content

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
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

// Handler serves GET and HEAD /content/item/<id>/<mac>.
type Handler struct {
	Store  *store.DB
	Blobs  *blobs.Store
	Secret []byte
	// Location is the time zone an article's date is shown in: the
	// daemon's own, time.Local, when nil. The app formats the same date in
	// the reader's zone, so both show one day.
	Location *time.Location
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
	page, err := h.page(r, it)
	if err != nil {
		http.Error(w, "the article could not be read", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, _ = w.Write(page)
}

// page is the article's HTML: the style, the head of the article, and the
// content with its images inlined.
func (h *Handler) page(r *http.Request, it store.Item) ([]byte, error) {
	// The extraction's HTML is already sanitized by the extractor, and its
	// blob: markers are part of it; it is not sanitized again, which would
	// drop them. The feed's summary is still untrusted when an item is
	// read before its extraction, so it is sanitized here.
	body := template.HTML(it.ContentHTML)
	if strings.TrimSpace(string(body)) == "" || sanitize.PlainText(string(body)) == "" {
		body = template.HTML(sanitize.HTML(it.SummaryHTML))
	}
	images, err := h.Store.ItemBlobs(r.Context(), it.ID)
	if err != nil {
		return nil, err
	}
	cache := map[string]string{}
	html := replaceBlobs(inlineOnly(string(body)), func(hash string) string {
		if uri, ok := cache[hash]; ok {
			return uri
		}
		uri := ""
		if b, ok := images[hash]; ok {
			if data, err := h.Blobs.Get(b.Hash); err == nil {
				uri = "data:" + b.Mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			}
		}
		cache[hash] = uri
		return uri
	})
	data := struct {
		Scheme  string
		Title   string
		Byline  string
		Site    string
		Date    string
		Content template.HTML
	}{
		Scheme: Scheme(r), Title: it.Title, Byline: it.Author, Site: siteOf(it.URL), Date: h.published(it),
		Content: template.HTML(html),
	}
	var out strings.Builder
	if err := pageTemplate.Execute(&out, data); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
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
func (h *Handler) published(it store.Item) string {
	if it.PublishedAt.IsZero() {
		return ""
	}
	loc := h.Location
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
