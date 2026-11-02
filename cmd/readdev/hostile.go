//go:build devfeeds

package main

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// hostileStart is the first hostile item's publication time: a day after the
// development articles, so the corpus sorts above them in the reader's list.
var hostileStart = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

// hostileText is served after every corpus page: made-up words, so a
// readability extraction has an article to find on a page of markup.
const hostileText = `<p>The workshop keeps its own hours in the cold months. A
lamp is lit at the bench before the kettle, and the tools come out of the
drawer in the same order they went in: the small square, the marking knife,
the chisels, and the mallet that someone turned from a piece of apple wood
years ago. There is a shelf of jars above the bench, each one labelled in a
careful hand, and a tin of screws that has outlived three projects. The
floor is swept at the end of every evening, and the broom leans in the
corner where the light does not reach. On a wet afternoon the workshop
smells of sawdust, oil and cold iron, and the work goes slowly because the
wood is cold and the hands are slower still. A drawer that sticks is a
small problem to solve, and a shelf that holds is a small thing done well.
The quiet of a workshop at dusk is a good quiet: the bench is clear, the
tools are away, and the next job waits under a cloth. Nothing here is
finished, and nothing here is urgent.</p>`

// hostilePage is one corpus file: its name without .html, and its bytes.
type hostilePage struct {
	name string
	data []byte
}

// hostileCorpus is a hostile corpus directory, read at start. A nil
// *hostileCorpus is no corpus at all, and readdev then serves nothing of it.
type hostileCorpus struct {
	pages []hostilePage
}

// loadHostile reads every .html file in dir, sorted by name. It never
// changes the directory.
func loadHostile(dir string) (*hostileCorpus, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	c := &hostileCorpus{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		c.pages = append(c.pages, hostilePage{name: strings.TrimSuffix(e.Name(), ".html"), data: data})
	}
	slices.SortFunc(c.pages, func(a, b hostilePage) int { return strings.Compare(a.name, b.name) })
	return c, nil
}

// title is a corpus file's item title: "Hostile 07: javascript href" for
// 07-javascript-href.html.
func (c *hostileCorpus) title(name string) string {
	num, rest, _ := strings.Cut(name, "-")
	return "Hostile " + num + ": " + strings.ReplaceAll(rest, "-", " ")
}

// feed is the corpus as an RSS feed, one item per page. The item's
// description is the page's own bytes, escaped, so an item has markup as
// text: readd extracts the article from the item's link instead, and the
// page it fetches is the hostile page itself.
func (c *hostileCorpus) feed(base string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Dev Hostile</title><link>` + base + `/hostile.xml</link>
<description>Hostile pages for frostread's desktop check.</description>
`)
	for i, p := range c.pages {
		link := base + "/hostile/" + p.name
		when := hostileStart.Add(time.Duration(i) * time.Minute).Format("Mon, 02 Jan 2006 15:04:05 GMT")
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><guid>%s</guid>
<pubDate>%s</pubDate><description>%s</description></item>
`, html.EscapeString(c.title(p.name)), link, link, when, html.EscapeString(string(p.data)))
	}
	b.WriteString(`</channel></rss>
`)
	return b.String()
}

// servePage answers one corpus file: a title element with the item's
// title, the file's bytes, then hostileText, so the page is an article with
// markup in it, and its extraction keeps the item's title, which the app's
// check looks for in the reader. It reports whether the corpus holds name.
func (c *hostileCorpus) servePage(w http.ResponseWriter, name string) bool {
	i, ok := c.find(name)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<title>%s</title>\n", html.EscapeString(c.title(name)))
	_, _ = w.Write(c.pages[i].data)
	_, _ = fmt.Fprint(w, hostileText)
	return true
}

// find is the index of the page called name.
func (c *hostileCorpus) find(name string) (int, bool) {
	for i, p := range c.pages {
		if p.name == name {
			return i, true
		}
	}
	return 0, false
}
