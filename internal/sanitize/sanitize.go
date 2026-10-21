// Package sanitize is the one place an article's untrusted HTML is made
// safe to show (ADR-0005). It is the only package that imports bluemonday;
// a policy here decides what an article may keep.
package sanitize

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"

	"golang.org/x/net/html"
)

// elements is what an article may keep: its structure and its words. A
// style, class, id, iframe, form, object or embed is not here, and so it
// goes.
var elements = []string{
	"h1", "h2", "h3", "h4", "h5", "h6",
	"p", "br", "hr", "div", "span",
	"ul", "ol", "li", "dl", "dt", "dd",
	"blockquote", "pre", "code", "kbd", "samp", "var",
	"table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td",
	"figure", "figcaption", "picture",
	"img", "a",
	"strong", "em", "b", "i", "u", "s", "del", "ins", "mark", "small", "sub", "sup",
	"abbr", "q", "cite", "time",
	"article", "section", "header", "footer", "aside", "main", "nav", "details", "summary",
}

// policy is the article policy. It is built once and read-only after.
var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(elements...)
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
	p.AllowAttrs("datetime").OnElements("time")
	p.AllowAttrs("colspan", "rowspan", "scope").OnElements("td", "th")
	p.AllowAttrs("start").OnElements("ol")
	p.AllowAttrs("value").OnElements("li")
	p.AllowAttrs("lang").OnElements("p", "span", "blockquote")
	p.AllowAttrs("title").OnElements("abbr")
	p.RequireParseableURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	// An image may be http or https only: never data:, and never a
	// scheme the browser would run.
	p.AllowRelativeURLs(false)
	p.RequireNoFollowOnLinks(false)
	return p
}

// HTML sanitizes an article's HTML with the article policy: headings,
// paragraphs, lists, quotes, code, tables, figures, images with an http or
// https src, and links with an http, https or mailto href. Every link
// gains target="_blank" and rel="noopener noreferrer".
func HTML(untrusted string) string {
	clean := policy.Sanitize(untrusted)
	return linkTargets(clean)
}

// blocks are the elements that start and end a line of text. A block's
// words never run into its neighbour's: "mind." and "A tomato." are two
// sentences, so a line break separates them (ADR-0005's text, for an
// excerpt and for the search index).
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "br": true,
	"dd": true, "div": true, "dl": true, "dt": true, "figcaption": true, "figure": true,
	"footer": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "tbody": true, "td": true, "tfoot": true,
	"th": true, "thead": true, "tr": true, "ul": true,
}

// PlainText returns the text of some HTML, a line break between blocks and
// a space between a block's words: what an excerpt or a search index
// needs. The result has no line breaks of its own: its words are joined by
// single spaces.
func PlainText(untrusted string) string {
	doc, err := html.Parse(strings.NewReader(untrusted))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		block := n.Type == html.ElementNode && blocks[n.Data]
		if block {
			b.WriteByte('\n')
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}

// linkTargets rewrites each anchor to open in a new window, without a
// handle back to the reader.
func linkTargets(clean string) string {
	doc, err := html.Parse(strings.NewReader(clean))
	if err != nil {
		return clean
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			setAttr(n, "target", "_blank")
			setAttr(n, "rel", "noopener noreferrer")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	var b strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return clean
		}
	}
	return b.String()
}

// setAttr sets key to value on n, replacing an existing value.
func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}
