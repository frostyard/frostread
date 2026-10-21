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
	"bytes"
	"flag"
	"fmt"
	"html"
	"html/template"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
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

// article is one development article: its title, and paragraphs of its
// own, so a search for one article's words finds only that article.
type article struct {
	title string
	paras []string
}

// articles are the six development articles. Only the first mentions
// tomatoes.
var articles = []article{
	{
		title: "Planting tomatoes after the last frost",
		paras: []string{
			"Tomatoes want sun, warmth and a steady drink. Plant them out only after the last frost, when the soil has warmed at a hand's depth, and give each one a stake or a cage before it starts to sprawl. A tomato planted deep roots along its buried stem, which makes a sturdier plant in a dry week.",
			"Water at the base, early in the day, so the leaves dry before evening. A mulch of straw keeps the moisture even and keeps the tomatoes off the bare earth, where they bruise and rot. Feed them once the first flowers set, and pinch out the side shoots of a cordon variety.",
			"A ripe tomato comes away with a small twist and smells of the vine. Pick them as they ripen, and pick the green ones before the first hard frost: they will finish indoors, on a windowsill, slowly and well enough for a sauce.",
			"Save the seed of the best fruit, and next year's tomatoes will suit this garden a little better. The seed keeps in a cool, dry envelope until spring.",
		},
	},
	{
		title: "An autumn walk along the river",
		paras: []string{
			"The river runs low in October, and the stones it has carried all summer stand out of the water like a path. A heron waits below the bridge with the patience of something that has nowhere else to be. The path beside the water is cold and bright, and the leaves of the alders turn over in the wind.",
			"Walk upriver and the town falls away. A few boats lie on the bank, covered against the winter, and a fisherman stands in the shallows with his line across the light. The water is so clear that the gravel of the bed seems close enough to touch.",
			"By the second bend the path narrows, and the river runs quick between the roots. A dipper works the stones, walking under the water with its wings open, and comes up on the far side as if nothing had happened. The air smells of wet leaves and cold stone.",
			"Walk back before the light goes. The town returns slowly, one window at a time, and the bridge hums under the cars. The heron is still there, and the river is still going.",
		},
	},
	{
		title: "A short note on bicycles",
		paras: []string{
			"A bicycle is a small machine that rewards attention. The chain wants a little oil and not too much; the cog and the chainring want to be clean. A chain that skips has usually stretched, and a new one is cheaper than a new cassette.",
			"Set the saddle height so the leg is almost straight at the bottom of the pedal's turn. Brakes should bite before the lever reaches the bar. A ticking sound with each turn of the wheel is often a spoke or the valve, and it is worth finding before it becomes a walk home.",
			"Hills are a matter of gears and patience. Change down before the hill, not on it, and keep a rhythm you can hold. A bicycle in the rain needs the same care afterwards: a wipe, a little oil, and a dry place.",
			"Ride where you are seen, and say what you are doing with a hand. The bicycle itself needs no more than that.",
		},
	},
	{
		title: "Making bread on a Sunday",
		paras: []string{
			"Bread is flour, water, salt and time. A Sunday loaf uses a little yeast and a long rise: mix the dough loosely, leave it cool and covered, and let the yeast do the work while you do something else. The dough is ready when it holds a shape and smells faintly sour.",
			"Shape it with a light hand, and let it rise again in the tin or the basket. Heat the oven well before the loaf goes in, and put a tray of water at the bottom for steam. The steam gives the crust its colour and its crackle.",
			"Bake it until it sounds hollow when you knock on the base, then let it cool on a rack. A hot loaf cut open too soon goes gummy at the crumb, so wait, however good it smells.",
			"The next loaf is better. The starter keeps on the counter, and the flour and the oven do the rest of the work.",
		},
	},
	{
		title: "Keeping a reading list that works",
		paras: []string{
			"A reading list works best when it is short and honest. Add a book when you mean to read it, take one off when you decide you will not, and do not let the list become a record of good intentions. Five or six books are enough to choose from.",
			"Write one line after each book: what it was about, what stayed with you, whether you would give it to a friend. The lines are more useful than the list. A page of them, read a year later, is a memory of your own reading.",
			"Read on paper where you can, and keep the notes with the book. Borrow from the library, and return the books on time; a shelf full of unread books is a reproach, but a library card is not.",
			"At the end of the year, read the notes and not the list. The list will have taken care of itself.",
		},
	},
	{
		title: "The garden in November",
		paras: []string{
			"November is the quiet month. The beds are turned and manured, the bean poles are down, and the last of the quince is ripening on the tree. Frost comes to the lawn early and stays in the shade all day. The garden is not finished; it is resting.",
			"Mulch what is still growing, and leave the seed heads for the birds. Drain the hoses and put the tools away dry. A greenhouse gets a clean and an airing, and its glass will let in more light for the winter salads.",
			"Order the seed for next year now, while the failures of this one are still fresh. Note where the beans were, and plan to move them. The catalogues come in the post, and the photographs are better than the summer was.",
			"Walk the garden in the frost, and see its bones: the paths, the walls, the shape of the beds. It is a good time to plan, and a better time to leave it alone.",
		},
	},
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
	mux.HandleFunc("/articles/", articlePage)
	mux.HandleFunc("/img/", illustration)
	return mux
}

// articlePage serves one article's page, with its illustration and text
// enough for a readability extraction.
func articlePage(w http.ResponseWriter, r *http.Request) {
	n := index(r.URL.Path, "/articles/")
	if n < 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, struct {
		Title string
		Body  template.HTML
	}{Title: articles[n].title, Body: template.HTML(body(articles[n], n))}); err != nil {
		log.Print(err)
	}
}

// illustration serves article n's picture: a 480 by 270 PNG, drawn rather
// than embedded, so the owner sees a real image in the reader.
func illustration(w http.ResponseWriter, r *http.Request) {
	n := index(r.URL.Path, "/img/")
	if n < 0 || !strings.HasSuffix(r.URL.Path, ".png") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(picture(n))
}

// index reads the 1-based article number after prefix, and returns its
// 0-based index; -1 for anything else.
func index(path, prefix string) int {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimSuffix(rest, "/")
	var n int
	if _, err := fmt.Sscan(rest, &n); err != nil || n < 1 || n > len(articles) {
		return -1
	}
	return n - 1
}

// body is an article's HTML: its title as the page's heading, its
// paragraphs, and its illustration after the second.
func body(a article, n int) string {
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(a.title) + "</h1>")
	for i, p := range a.paras {
		b.WriteString("<p>" + html.EscapeString(p) + "</p>")
		if i == 1 {
			fmt.Fprintf(&b, `<figure><img src="/img/%d.png" alt="A made-up illustration"><figcaption>A made-up illustration.</figcaption></figure>`, n+1)
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
<item><title>` + html.EscapeString(articles[0].title) + `</title>
<link>` + base + `/articles/1</link><guid>` + base + `/articles/1</guid>
<pubDate>Tue, 20 Oct 2026 09:00:00 GMT</pubDate>
<description>Tomatoes want sun and a steady drink, and a stake before they sprawl.</description></item>
<item><title>` + html.EscapeString(articles[1].title) + `</title>
<link>` + base + `/articles/2</link><guid>` + base + `/articles/2</guid>
<pubDate>Tue, 20 Oct 2026 10:00:00 GMT</pubDate>
<description>` + html.EscapeString(body(articles[1], 1)) + `</description></item>
</channel></rss>
`
}

// atom is the Atom feed.
func atom(base string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>Example Development Atoms</title><link href="` + base + `/"/>
<id>tag:example.org,2026:dev</id>
<entry><title>` + html.EscapeString(articles[2].title) + `</title>
<link href="` + base + `/articles/3"/><id>` + base + `/articles/3</id>
<author><name>Ann Author</name></author><updated>2026-10-20T11:00:00Z</updated>
<summary>A bicycle rewards attention, and a little oil.</summary></entry>
<entry><title>` + html.EscapeString(articles[3].title) + `</title>
<link href="` + base + `/articles/4"/><id>` + base + `/articles/4</id>
<author><name>Jo Writer</name></author><updated>2026-10-20T12:00:00Z</updated>
<content type="html">` + html.EscapeString(body(articles[3], 3)) + `</content></entry>
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
     "title": "` + articles[4].title + `", "summary": "A short reading list works better than a long one.",
     "date_published": "2026-10-20T13:00:00Z",
     "authors": [{"name": "Jo Writer"}]},
    {"id": "` + base + `/articles/6", "url": "` + base + `/articles/6",
     "title": "` + articles[5].title + `", "content_html": "` + jsonEscape(body(articles[5], 5)) + `",
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

// palette is one base colour for each article's illustration, from the
// suite's blues and deep navies.
var palette = []color.RGBA{
	{0x16, 0x8b, 0xd0, 0xff},
	{0x0d, 0x26, 0x3b, 0xff},
	{0x47, 0xb8, 0xef, 0xff},
	{0x08, 0x1b, 0x2d, 0xff},
	{0x2c, 0x63, 0x80, 0xff},
	{0x0d, 0x2d, 0x45, 0xff},
}

// pictures draws every article's illustration once: 480 by 270, a band of
// the article's colour, a sun and a horizon.
var pictures = sync.OnceValue(func() [][]byte {
	out := make([][]byte, len(articles))
	for i := range articles {
		out[i] = drawPicture(i)
	}
	return out
})

// picture is article n's illustration, as a PNG.
func picture(n int) []byte { return pictures()[n] }

func drawPicture(n int) []byte {
	const width, height = 480, 270
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	base := palette[n%len(palette)]
	sky := lighten(base, 0.45)
	ground := base
	for y := range height {
		c := sky
		if y > height*2/3 {
			c = ground
		}
		for x := range width {
			img.Set(x, y, c)
		}
	}
	// The sun: a filled circle in the sky, at a place that moves with n.
	cx, cy, r := 120+(n%3)*110, 84, 46
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.Set(x, y, color.RGBA{0xed, 0xf8, 0xff, 0xff})
			}
		}
	}
	// A darker band on the horizon, and a diagonal stripe.
	for y := height * 2 / 3; y < height*2/3+6; y++ {
		for x := range width {
			img.Set(x, y, darken(base, 0.35))
		}
	}
	for y := height * 2 / 3; y < height; y++ {
		for x := range width {
			if (x+y)%64 < 10 {
				img.Set(x, y, lighten(base, 0.2))
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Print(err)
		return nil
	}
	return buf.Bytes()
}

// lighten mixes c towards white by t (0 to 1).
func lighten(c color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c.R) + (255-float64(c.R))*t),
		G: uint8(float64(c.G) + (255-float64(c.G))*t),
		B: uint8(float64(c.B) + (255-float64(c.B))*t),
		A: 0xff,
	}
}

// darken mixes c towards the ink by t (0 to 1).
func darken(c color.RGBA, t float64) color.RGBA {
	ink := color.RGBA{0x06, 0x11, 0x1d, 0xff}
	return color.RGBA{
		R: uint8(float64(c.R) + (float64(ink.R)-float64(c.R))*t),
		G: uint8(float64(c.G) + (float64(ink.G)-float64(c.G))*t),
		B: uint8(float64(c.B) + (float64(ink.B)-float64(c.B))*t),
		A: 0xff,
	}
}
