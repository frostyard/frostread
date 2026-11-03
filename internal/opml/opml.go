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

// Feed is one subscription in an OPML document, and the folder it is in.
type Feed struct {
	Title   string
	XMLURL  string
	HTMLURL string
	// Folder is the feed's folder, one level; "" for none.
	Folder string
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
	// folder is true for an outline that holds feeds (Export): it writes
	// its name and its feeds alone, and no type or URL.
	folder bool
}

// MarshalXML writes an outline: a feed's with its type and its URLs, a
// folder's with its name and the feeds it holds.
func (o outline) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "outline"}
	start.Attr = nil
	if o.folder {
		start.Attr = []xml.Attr{
			{Name: xml.Name{Local: "text"}, Value: o.Text},
			{Name: xml.Name{Local: "title"}, Value: o.Title},
		}
	} else {
		start.Attr = []xml.Attr{
			{Name: xml.Name{Local: "type"}, Value: o.Type},
			{Name: xml.Name{Local: "text"}, Value: o.Text},
			{Name: xml.Name{Local: "title"}, Value: o.Title},
			{Name: xml.Name{Local: "xmlUrl"}, Value: o.XMLURL},
			{Name: xml.Name{Local: "htmlUrl"}, Value: o.HTMLURL},
		}
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, child := range o.Outlines {
		if err := e.Encode(child); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

// Import reads an OPML document and returns its feeds, in document order.
// An outline with no xmlUrl is a folder, and is walked; one with an xmlUrl
// is a feed. A feed's folder is its outermost enclosing folder's title, or
// its text when it has no title: folders are flattened into that one, so a
// deeper folder names nothing. A feed at the top level is in no folder. A
// document with no feeds is ErrInvalid.
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
	var walk func([]outline, string)
	walk = func(os []outline, folder string) {
		for _, o := range os {
			if url := strings.TrimSpace(o.XMLURL); url != "" {
				title := strings.TrimSpace(o.Title)
				if title == "" {
					title = strings.TrimSpace(o.Text)
				}
				out = append(out, Feed{Title: title, XMLURL: url, HTMLURL: strings.TrimSpace(o.HTMLURL),
					Folder: folder})
				continue
			}
			in := folder
			if in == "" {
				in = strings.TrimSpace(o.Title)
				if in == "" {
					in = strings.TrimSpace(o.Text)
				}
			}
			walk(o.Outlines, in)
		}
	}
	walk(doc.Body.Outlines, "")
	if len(out) == 0 {
		return nil, fmt.Errorf("the opml names no feed: %w", ErrInvalid)
	}
	return out, nil
}

// Export writes feeds as an OPML 2.0 document: the folders first, by name,
// case-insensitively, each one an outline with its feeds by title, and then
// the feeds with no folder, by title.
func Export(feeds []Feed) ([]byte, error) {
	sorted := slices.Clone(feeds)
	slices.SortStableFunc(sorted, func(a, b Feed) int {
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	// The folders, by name as the store keeps it: two spellings are two
	// folders, as items.list's folder filter sees them. They sort by the
	// name folded to lower case, then by the name itself.
	folders := map[string][]outline{}
	var names []string
	var loose []outline
	for _, f := range sorted {
		o := outline{Type: "rss", Text: f.Title, Title: f.Title, XMLURL: f.XMLURL, HTMLURL: f.HTMLURL}
		if f.Folder == "" {
			loose = append(loose, o)
			continue
		}
		if _, ok := folders[f.Folder]; !ok {
			names = append(names, f.Folder)
		}
		folders[f.Folder] = append(folders[f.Folder], o)
	}
	slices.SortFunc(names, func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	doc := document{Body: body{Outlines: make([]outline, 0, len(names)+len(loose))}}
	for _, name := range names {
		doc.Body.Outlines = append(doc.Body.Outlines, outline{
			Text: name, Title: name, Outlines: folders[name], folder: true,
		})
	}
	doc.Body.Outlines = append(doc.Body.Outlines, loose...)
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
