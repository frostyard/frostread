//go:build devfeeds

// Command readdev serves three generated feeds and their articles on
// 127.0.0.1, for the live check on lodge's development build
// (docs/specs/api.md, Development-only). It is built with -tags devfeeds,
// and is in no release.
//
//	readdev [-addr 127.0.0.1:8088] [-opml]
//
// With -opml it prints the OPML that names the three feeds and stops.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"html"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8088", "the address to serve on")
	opmlOnly := flag.Bool("opml", false, "print the OPML for the feeds and exit")
	flag.Parse()
	base := "http://" + *addr
	if *opmlOnly {
		fmt.Print(opmlDocument(base))
		return
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "readdev: three feeds at %s; set READ_DEV_ALLOW=%s for readd\n", base, *addr)
	srv := &http.Server{Handler: handler(base), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// articles are the development articles' titles and first paragraphs. The
// long one is its own article when the feed gives it in full.
var articles = []string{
	"Planting tomatoes after the last frost",
	"An autumn walk along the river",
	"A short note on bicycles",
	"Making bread on a Sunday",
	"Keeping a reading list that works",
	"The garden in November",
}

func handler(base string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><title>Development feeds</title>
<p>Three feeds for frostread's live check:</p><ul>
<li><a href="/rss.xml">RSS</a></li><li><a href="/atom.xml">Atom</a></li>
<li><a href="/feed.json">JSON Feed</a></li></ul>
<p><a href="/subscriptions.opml">The OPML</a></p>`)
	})
	mux.HandleFunc("/subscriptions.opml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
		fmt.Fprint(w, opmlDocument(base))
	})
	mux.HandleFunc("/rss.xml", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, rss(base)) })
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, atom(base)) })
	mux.HandleFunc("/feed.json", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, jsonFeed(base)) })
	mux.HandleFunc("/articles/", article)
	mux.HandleFunc("/img/tomato.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	return mux
}

// article serves one article's page, with an image and enough text for a
// readability extraction.
func article(w http.ResponseWriter, r *http.Request) {
	n := 0
	if _, err := fmt.Sscan(strings.TrimPrefix(r.URL.Path, "/articles/"), &n); err != nil || n < 1 || n > len(articles) {
		http.NotFound(w, r)
		return
	}
	title := articles[n-1]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Title string
		Body  template.HTML
	}{Title: title, Body: template.HTML(paragraphs(title))}
	if err := page.Execute(w, data); err != nil {
		log.Print(err)
	}
}

// paragraph is the article text, repeated with the title woven in: made
// up, and long enough that readability keeps all of it.
func paragraphs(title string) string {
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&b, `<p>This is paragraph %d of a made-up article for the development check. `+
			`It has no truth in it and no reader in mind, only enough words for an extraction to find. `+
			`The quick brown fox jumps over the lazy dog, and the dog does not mind.</p>`, i)
		if i == 2 {
			b.WriteString(`<figure><img src="/img/tomato.png" alt="A made-up tomato"><figcaption>A made-up tomato.</figcaption></figure>`)
		}
	}
	return b.String()
}

// page is an article's page.
var page = template.Must(template.New("article").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Title}}</title>
<meta property="og:site_name" content="Example Development Journal">
<meta property="article:published_time" content="2026-10-20T09:00:00Z"></head>
<body><article>{{.Body}}</article></body></html>
`))

// rss is the RSS feed: the first article as a short summary, the second in
// full, so both extraction paths are exercised.
func rss(base string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Example Development Journal</title>
<link>` + base + `/</link>
<description>Made-up articles for frostread's development check.</description>
<item><title>` + html.EscapeString(articles[0]) + `</title>
<link>` + base + `/articles/1</link><guid>` + base + `/articles/1</guid>
<pubDate>Tue, 20 Oct 2026 09:00:00 GMT</pubDate>
<description>A short summary of the first made-up article.</description></item>
<item><title>` + html.EscapeString(articles[1]) + `</title>
<link>` + base + `/articles/2</link><guid>` + base + `/articles/2</guid>
<pubDate>Tue, 20 Oct 2026 10:00:00 GMT</pubDate>
<description>` + strings.ReplaceAll(paragraphs(articles[1]), "\n", " ") + `</description></item>
</channel></rss>
`
}

// atom is the Atom feed.
func atom(base string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>Example Development Atoms</title><link href="` + base + `/"/>
<id>tag:example.org,2026:dev</id>
<entry><title>` + html.EscapeString(articles[2]) + `</title>
<link href="` + base + `/articles/3"/><id>` + base + `/articles/3</id>
<author><name>Ann Author</name></author><updated>2026-10-20T11:00:00Z</updated>
<summary>A short note's summary.</summary></entry>
<entry><title>` + html.EscapeString(articles[3]) + `</title>
<link href="` + base + `/articles/4"/><id>` + base + `/articles/4</id>
<author><name>Jo Writer</name></author><updated>2026-10-20T12:00:00Z</updated>
<content type="html">` + html.EscapeString(paragraphs(articles[3])) + `</content></entry>
</feed>
`
}

// jsonFeed is the JSON Feed.
func jsonFeed(base string) string {
	return `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "Example Development Feeds",
  "home_page_url": "` + base + `/",
  "items": [
    {"id": "` + base + `/articles/5", "url": "` + base + `/articles/5",
     "title": "` + articles[4] + `", "summary": "A short summary of a made-up article.",
     "date_published": "2026-10-20T13:00:00Z",
     "authors": [{"name": "Jo Writer"}]},
    {"id": "` + base + `/articles/6", "url": "` + base + `/articles/6",
     "title": "` + articles[5] + `", "content_html": "` + jsonEscape(paragraphs(articles[5])) + `",
     "date_published": "2026-10-20T14:00:00Z",
     "authors": [{"name": "Ann Author"}]}
  ]
}
`
}

// opmlDocument is the three feeds as an OPML document, for readctl import.
func opmlDocument(base string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0"><head><title>Development feeds</title></head><body>
<outline text="Development" title="Development">
<outline type="rss" text="Example Development Journal" xmlUrl="` + base + `/rss.xml" htmlUrl="` + base + `/"/>
<outline type="rss" text="Example Development Atoms" xmlUrl="` + base + `/atom.xml" htmlUrl="` + base + `/"/>
<outline type="rss" text="Example Development Feeds" xmlUrl="` + base + `/feed.json" htmlUrl="` + base + `/"/>
</outline></body></opml>
`
}

// jsonEscape escapes a string for a JSON string literal.
func jsonEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n', '\r':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// png is a 1x1 PNG, the development article's image.
var png = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
