// Package feed parses RSS, Atom and JSON Feed into one shape, and finds a
// feed's address in a page (docs/specs/api.md). It is the only package
// that imports gofeed; the parser's types do not leave it.
package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"golang.org/x/net/html"
)

// Feed is a parsed feed.
type Feed struct {
	Title   string
	SiteURL string
	Items   []Item
}

// Item is one entry of a feed.
type Item struct {
	GUID        string
	URL         string
	Title       string
	Author      string
	Published   time.Time
	SummaryHTML string
	ContentHTML string
}

var (
	// ErrInvalid is the sentinel every parse failure wraps.
	ErrInvalid = errors.New("feed is invalid")
	// ErrNotAFeed means the body is not RSS, Atom or JSON Feed.
	ErrNotAFeed = fmt.Errorf("not a feed: %w", ErrInvalid)
)

// Parse reads body as RSS, Atom or JSON Feed. baseURL resolves a relative
// link, such as an item's when a feed does not give an absolute one.
func Parse(body []byte, baseURL string) (Feed, error) {
	p := gofeed.NewParser()
	f, err := p.ParseString(string(body))
	if err != nil {
		return Feed{}, fmt.Errorf("parse %s: %w: %v", baseURL, ErrInvalid, err)
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return Feed{}, fmt.Errorf("parse base %q: %w", baseURL, err)
	}
	out := Feed{Title: strings.TrimSpace(f.Title), SiteURL: resolve(base, f.Link)}
	for _, it := range f.Items {
		if it == nil {
			continue
		}
		published := time.Time{}
		switch {
		case it.PublishedParsed != nil:
			published = *it.PublishedParsed
		case it.UpdatedParsed != nil:
			published = *it.UpdatedParsed
		}
		link := resolve(base, it.Link)
		guid := strings.TrimSpace(it.GUID)
		if guid == "" {
			guid = link
		}
		title := strings.TrimSpace(it.Title)
		if guid == "" {
			guid = fallbackGUID(title, published)
		}
		out.Items = append(out.Items, Item{
			GUID:        guid,
			URL:         link,
			Title:       title,
			Author:      author(it),
			Published:   published,
			SummaryHTML: it.Description,
			ContentHTML: it.Content,
		})
	}
	if out.Title == "" && len(out.Items) == 0 {
		return Feed{}, ErrNotAFeed
	}
	return out, nil
}

// author is an item's first author, from its author or its authors.
func author(it *gofeed.Item) string {
	if it.Author != nil && strings.TrimSpace(it.Author.Name) != "" {
		return strings.TrimSpace(it.Author.Name)
	}
	for _, a := range it.Authors {
		if a != nil && strings.TrimSpace(a.Name) != "" {
			return strings.TrimSpace(a.Name)
		}
	}
	return ""
}

// fallbackGUID names an item by its title and date when it has no other
// id, so a re-poll of an unchanged feed does not add it twice.
func fallbackGUID(title string, published time.Time) string {
	sum := sha256.Sum256([]byte(title + "|" + published.UTC().Format(time.RFC3339)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// resolve makes ref absolute against base; an unparseable ref is dropped.
func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u.String()
}

// feedTypes are the link types Discover accepts, as a page writes them.
var feedTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/feed+json": true,
	"application/json":      true, // a page may name JSON Feed so
	"application/xml":       true, // loosely; the parser decides
	"text/xml":              true,
}

// Discover returns the feed addresses an HTML page names in its
// <link rel="alternate"> elements, absolute against baseURL, without
// duplicates.
func Discover(page []byte, baseURL string) []string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			rel, typ, href := "", "", ""
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(strings.TrimSpace(a.Val))
				case "type":
					typ = strings.ToLower(strings.TrimSpace(a.Val))
				case "href":
					href = a.Val
				}
			}
			if rel == "alternate" && feedTypes[typ] && href != "" {
				if u := resolve(base, href); u != "" && !seen[u] {
					seen[u] = true
					out = append(out, u)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// String is a feed's title and item count, for a log.
func (f Feed) String() string { return fmt.Sprintf("%q (%d items)", f.Title, len(f.Items)) }
