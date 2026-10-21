// Package opml reads and writes OPML 2.0 subscription lists: the
// interchange format a reader's sources come in and go out as
// (docs/specs/api.md, readctl).
package opml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ErrInvalid is the sentinel every read failure wraps.
var ErrInvalid = errors.New("opml is invalid")

// Feed is one subscription in an OPML document.
type Feed struct {
	Title   string
	XMLURL  string
	HTMLURL string
}

// document is an OPML file as encoding/xml sees it.
type document struct {
	XMLName xml.Name `xml:"opml"`
	Body    body     `xml:"body"`
}

type body struct {
	Outlines []outline `xml:"outline"`
}

type outline struct {
	Type     string    `xml:"type,attr"`
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Outlines []outline `xml:"outline"`
}

// Import reads an OPML document and returns its feeds, in document order.
// Folders are flattened. An outline with no xmlUrl is a folder, and is
// walked; one with an xmlUrl is a feed. A document with no feeds is
// ErrInvalid.
func Import(r io.Reader) ([]Feed, error) {
	data, err := io.ReadAll(io.LimitReader(r, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read opml: %w", err)
	}
	var doc document
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.CharsetReader = charset
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse opml: %w: %v", ErrInvalid, err)
	}
	var out []Feed
	var walk func([]outline)
	walk = func(os []outline) {
		for _, o := range os {
			if url := strings.TrimSpace(o.XMLURL); url != "" {
				title := strings.TrimSpace(o.Title)
				if title == "" {
					title = strings.TrimSpace(o.Text)
				}
				out = append(out, Feed{Title: title, XMLURL: url, HTMLURL: strings.TrimSpace(o.HTMLURL)})
				continue
			}
			walk(o.Outlines)
		}
	}
	walk(doc.Body.Outlines)
	if len(out) == 0 {
		return nil, fmt.Errorf("the opml names no feed: %w", ErrInvalid)
	}
	return out, nil
}

// Export writes feeds as an OPML 2.0 document, by title.
func Export(feeds []Feed) ([]byte, error) {
	doc := document{Body: body{Outlines: make([]outline, 0, len(feeds))}}
	sorted := slices.Clone(feeds)
	slices.SortStableFunc(sorted, func(a, b Feed) int {
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	for _, f := range sorted {
		doc.Body.Outlines = append(doc.Body.Outlines, outline{
			Type: "rss", Text: f.Title, Title: f.Title, XMLURL: f.XMLURL, HTMLURL: f.HTMLURL,
		})
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("write opml: %w", err)
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}

// charset admits the encodings encoding/xml does not know, so an old
// export's Latin-1 title does not stop an import.
func charset(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii", "":
		return input, nil
	case "iso-8859-1", "latin1", "windows-1252":
		b, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		sb.Grow(len(b))
		for _, c := range b {
			sb.WriteRune(rune(c))
		}
		return strings.NewReader(sb.String()), nil
	}
	return nil, fmt.Errorf("unknown encoding %q", label)
}
