//go:build devfeeds

// Command screenshots-server serves a small set of made-up feeds for the
// Flatpak metainfo screenshots. It listens only on loopback and carries no
// real people, addresses or sites.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var articles = []article{
	{feed: 0, title: "Tomatoes after the last frost", paras: []string{
		"Tomatoes want sun, warmth and a steady drink. Plant them out only after the last frost, when the soil has warmed at a hand's depth, and give each plant a stake before it starts to sprawl.",
		"Water at the base in the morning. A mulch of straw keeps the soil cool and keeps the fruit off the bare ground.",
		"Pick the fruit as it ripens. The green fruit will finish indoors on a windowsill before the first hard frost.",
	}, link: "https://example.org/garden/tomatoes"},
	{feed: 0, title: "A quiet week in the seed garden", paras: []string{
		"The beans are tied to their poles and the herb bed has filled in. The first carrots come from the cooler soil beside the wall.",
		"We leave one bed empty for next spring. The compost goes on top and the winter rain will carry it down.",
		"A garden journal helps keep the calendar honest. It records the weather, the sowing and the first harvest.",
	}, link: "https://example.com/garden/journal"},
	{feed: 1, title: "Winter squash on the stove", paras: []string{
		"Cut one squash into small pieces and cook it with onion, stock and a bay leaf. The kitchen smells warm before the soup begins to thicken.",
		"Blend the soup until it is smooth. Add lemon juice, pepper and a spoon of cream if the evening calls for it.",
		"Serve it with toasted bread and a crisp salad.",
	}, link: "https://example.com/kitchen/squash"},
	{feed: 1, title: "A simple loaf for Saturday", paras: []string{
		"Mix flour, water, salt and a little yeast. Leave the dough in a cool place for most of the day.",
		"Shape the loaf with a light hand and let it rise again. Heat the oven fully before the bread goes in.",
		"The loaf is ready when the base sounds hollow. Wait for it to cool before the first slice.",
	}, link: "https://example.org/kitchen/bread"},
	{feed: 2, title: "Along the river in October", paras: []string{
		"The river is low and the stones stand clear of the water. A heron waits below the bridge in the pale morning light.",
		"The path climbs beside the alders, then turns where the bank is too narrow. A small footbridge crosses at the bend.",
		"Carry a light and leave the ridge before the weather closes in.",
	}, link: "https://example.org/trails/riverside"},
	{feed: 2, title: "The north ridge route", paras: []string{
		"The north route starts through pine and rises over a narrow shoulder. Its highest point gives a clear view over the valley.",
		"The descent follows a dry stream. The stones can be loose, so slow steps are useful here.",
		"The trail ends at a small station where the map can be checked before the return walk.",
	}, link: "https://example.com/trails/north-ridge"},
	{feed: 3, title: "Repairing the porch bench", paras: []string{
		"The bench has worn through at the front rail. The repair starts with a level mark and one sound piece of timber.",
		"Cut the replacement slightly long, fit it dry and then fasten it with coated screws.",
		"Round the edges and finish the wood before the next wet week.",
	}, link: "https://example.com/workshop/bench"},
	{feed: 3, title: "A small cabinet for the hallway", paras: []string{
		"The cabinet uses shallow shelves and a full-size back panel. It fits under the coat hooks without blocking the door.",
		"Mark the opening first, then cut the parts in a safe sequence. A trial fit keeps the small gaps even.",
		"A clear coat keeps the timber easy to clean and shows its grain.",
	}, link: "https://example.org/workshop/cabinet"},
}

type article struct {
	feed  int
	title string
	paras []string
	link  string
}

var feedTitles = []string{
	"The Orchard Journal",
	"Weeknight Kitchen",
	"Trail Notes",
	"The Home Workshop",
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "loopback listen address")
	baseFlag := flag.String("base", "http://127.0.0.1:8099", "host name used in made-up feed URLs")
	opmlOnly := flag.Bool("opml", false, "print the screenshot OPML and exit")
	flag.Parse()
	base := *baseFlag
	if *opmlOnly {
		fmt.Print(opmlDocument(base))
		return
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("screenshots server: %v", err)
	}
	fmt.Fprintf(os.Stderr, "screenshots server: four feeds at %s\n", base)
	srv := &http.Server{Handler: handler(base), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func handler(base string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			notArticle(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>Frostread screenshot feeds</title><p>Four made-up feeds.</p>")
		for i, title := range feedTitles {
			fmt.Fprintf(w, `<p><a href="/feed/%d.xml">%s</a></p>`, i+1, html.EscapeString(title))
		}
	})
	for i := range feedTitles {
		feedIndex := i
		mux.HandleFunc("/feed/"+strconv.Itoa(i+1)+".xml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			fmt.Fprint(w, rss(base, feedIndex))
		})
	}
	mux.HandleFunc("/subscriptions.opml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
		fmt.Fprint(w, opmlDocument(base))
	})
	mux.HandleFunc("/articles/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/articles/")
		name = strings.TrimSuffix(name, "/")
		n, err := strconv.Atoi(name)
		if err != nil || n < 1 || n > len(articles) {
			http.NotFound(w, r)
			return
		}
		a := articles[n-1]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title></head><body><article>", html.EscapeString(a.title))
		fmt.Fprintf(w, "<h1>%s</h1>", html.EscapeString(a.title))
		for i, p := range a.paras {
			fmt.Fprintf(w, "<p>%s</p>", html.EscapeString(p))
			if i == 1 {
				fmt.Fprintf(w, `<figure><img src="/img/%d.png" alt="A made-up field or workshop scene"><figcaption>A made-up scene.</figcaption></figure>`, n)
			}
		}
		fmt.Fprintf(w, `<p>More in <a href="%s">the article archive</a>.</p></article></body></html>`, html.EscapeString(a.link))
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/img/")
		name = strings.TrimSuffix(name, ".png")
		n, err := strconv.Atoi(name)
		if err != nil || n < 1 || n > len(articles) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, picture(n-1))
	})
	mux.HandleFunc("/later/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/later/")
		name = strings.TrimSuffix(name, "/")
		var title, text string
		switch name {
		case "Garden-notes":
			title = "Garden notes for October"
			text = "The compost is ready beside the shed, and the first raised bed is clear. Mark the garlic rows, cover the carrots and leave the herb stems for the birds. This short plan keeps the garden work useful through the next cool week."
		case "Weekend-baking":
			title = "A weekend loaf and a squash supper"
			text = "Start the loaf before breakfast and let the dough rise while the kitchen warms. At noon, roast the squash with onion and stock. The two simple dishes make a quiet supper for a wet weekend."
		case "Trail-map":
			title = "The north ridge trail map"
			text = "The north ridge begins at the stone bridge and follows the old path through pine. Carry water, check the weather and return before dusk. The map marks the second crossing and the clear view over the valley."
		case "Bench-project":
			title = "Repairing the porch bench"
			text = "Measure the porch rail before buying timber. A sound replacement piece should fit dry, then receive coated screws and two coats of clear finish. The finished bench will stay level through the autumn rain."
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title></head><body><article><h1>%s</h1><p>%s</p><p>Keep this note with the plan for the next free afternoon. The first page has the useful steps, and the second one records what to check again. It is made-up material for a made-up project.</p></article></body></html>", html.EscapeString(title), html.EscapeString(title), html.EscapeString(text))
	})
	return mux
}

func notArticle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/subscriptions.opml" {
		return
	}
	http.NotFound(w, r)
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Guid        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func rss(base string, feed int) string {
	var items []rssItem
	for i, a := range articles {
		if a.feed != feed {
			continue
		}
		n := i + 1
		items = append(items, rssItem{
			Title:       a.title,
			Link:        fmt.Sprintf("%s/articles/%d", base, n),
			Guid:        fmt.Sprintf("%s/articles/%d", base, n),
			PubDate:     time.Date(2026, time.October, 20-i, 9, 0, 0, 0, time.UTC).Format(time.RFC1123Z),
			Description: strings.Join(a.paras, " "),
		})
	}
	doc := rssDocument{Version: "2.0", Channel: rssChannel{
		Title: feedTitles[feed], Link: base, Description: "Made-up articles for the Frostread desktop.", Items: items,
	}}
	b, err := xml.Marshal(doc)
	if err != nil {
		return ""
	}
	return xml.Header + string(b)
}

func opmlDocument(base string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><opml version="2.0"><head><title>Frostread screenshot feeds</title></head><body>`)
	for i, title := range feedTitles {
		fmt.Fprintf(&b, `<outline text="%s" title="%s" type="rss" xmlUrl="%s/feed/%d.xml" />`, html.EscapeString(title), html.EscapeString(title), base, i+1)
	}
	b.WriteString(`</body></opml>`)
	return b.String()
}

func picture(n int) image.Image {
	palette := []color.RGBA{
		{R: 48, G: 126, B: 78, A: 255},
		{R: 210, G: 166, B: 96, A: 255},
		{R: 72, G: 132, B: 166, A: 255},
		{R: 180, G: 126, B: 72, A: 255},
	}
	base := palette[n%len(palette)]
	img := image.NewRGBA(image.Rect(0, 0, 480, 270))
	for y := 0; y < 270; y++ {
		for x := 0; x < 480; x++ {
			a := uint8((x*3 + y) % 24)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(min(255, int(base.R)+int(a))),
				G: uint8(min(255, int(base.G)+int(a))),
				B: uint8(min(255, int(base.B)+int(a))),
				A: 255,
			})
		}
	}
	for y := 150; y < 270; y++ {
		for x := 0; x < 480; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 70, G: 82, B: 67, A: 255})
		}
	}
	return img
}
