package extract

import (
	"strings"

	"golang.org/x/net/html"
)

// DropLeadingTitle removes the body's first heading when it only repeats
// the title: a reader that shows the title above the article would print
// it twice. It looks before any paragraph, and through a wrapper that
// holds one thing, so a heading that opens the article's own sections
// stays.
func DropLeadingTitle(content, title string) string {
	want := fold(title)
	if want == "" || content == "" {
		return content
	}
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return content
	}
	body := findBody(doc)
	if body == nil {
		return content
	}
	first := firstFlow(body)
	if first == nil || !isHeading(first.Data) {
		return content
	}
	if fold(textOf(first)) != want {
		return content
	}
	first.Parent.RemoveChild(first)
	var out strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return content
		}
	}
	return out.String()
}

// fold collapses whitespace and folds case, for a title comparison.
func fold(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// findBody is the document's body element, or nil.
func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "body" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// isHeading reports whether tag is h1 to h6.
func isHeading(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

// wrappers are the elements firstFlow looks through while they hold one
// thing, so <div><h1>…</h1></div> counts as the heading first.
var wrappers = map[string]bool{
	"article": true, "div": true, "header": true, "main": true, "section": true,
}

// firstFlow is the first element or word of a body, looking through a
// wrapper that holds one thing.
func firstFlow(n *html.Node) *html.Node {
	var element *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimSpace(c.Data) != "" {
				return nil // a word comes first: no leading heading
			}
		case html.ElementNode:
			if element == nil {
				element = c
			}
		}
	}
	if element == nil {
		return nil
	}
	if wrappers[element.Data] {
		if inner := firstFlow(element); inner != nil {
			return inner
		}
	}
	return element
}

// textOf is an element's text, whitespace collapsed.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
