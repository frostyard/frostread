package sanitize

import (
	"strings"
	"testing"
)

// The hostile corpus: each case is HTML an article must not keep.
func TestHostileCorpus(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string // what must not appear, in lower case
	}{
		{"script", `<p>ok</p><script>alert(1)</script>`, []string{"<script", "alert"}},
		{"onerror", `<img src="https://example.com/a.png" onerror="alert(1)">`, []string{"onerror", "alert"}},
		{"javascript href", `<a href="javascript:alert(1)">go</a>`, []string{"javascript:", "alert"}},
		{"data href", `<a href="data:text/html;base64,PHNjcmlwdD4=">go</a>`, []string{"data:"}},
		{"vbscript href", `<a href="vbscript:msgbox(1)">go</a>`, []string{"vbscript:"}},
		{"svg", `<svg onload="alert(1)"><circle/></svg>`, []string{"<svg", "onload"}},
		{"math", `<math><mtext><script>alert(1)</script></mtext></math>`, []string{"<math", "<script"}},
		{"base", `<base href="https://evil.example/">`, []string{"<base"}},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`, []string{"<meta", "http-equiv"}},
		{"css expression", `<div style="width:expression(alert(1))">x</div>`, []string{"style=", "expression"}},
		{"iframe", `<iframe src="https://evil.example/"></iframe>`, []string{"<iframe"}},
		{"form", `<form action="https://evil.example/"><input name="a"></form>`, []string{"<form", "<input"}},
		{"object", `<object data="https://evil.example/x.swf"></object>`, []string{"<object"}},
		{"embed", `<embed src="https://evil.example/x.swf">`, []string{"<embed"}},
		{"data image", `<img src="data:image/svg+xml,<svg onload=alert(1)>">`, []string{"data:", "<svg"}},
		{"style element", `<style>p{background:url(javascript:alert(1))}</style>`, []string{"<style", "javascript:"}},
		{"class and id", `<p class="x" id="y">z</p>`, []string{"class=", "id="}},
		{"link rel", `<link rel="import" href="https://evil.example/x.html">`, []string{"<link"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.ToLower(HTML(tc.in))
			for _, bad := range tc.want {
				if strings.Contains(got, bad) {
					t.Fatalf("sanitized output keeps %q:\n%s", bad, got)
				}
			}
		})
	}
}

func TestKeepsTheArticle(t *testing.T) {
	in := `<article>
		<h1>Gardening</h1>
		<p>Plant <strong>tomatoes</strong> after the <a href="https://example.com/frost">frost</a>.</p>
		<ul><li>Water</li><li>Wait</li></ul>
		<blockquote cite="https://example.com/quote">A quote.</blockquote>
		<figure><img src="https://example.com/tomato.png" alt="A tomato" width="640"><figcaption>A tomato.</figcaption></figure>
		<table><tr><th>Week</th><td>3</td></tr></table>
		<pre><code>make check</code></pre>
	</article>`
	got := HTML(in)
	for _, want := range []string{"<h1>Gardening</h1>", "<strong>tomatoes</strong>", "<ul>", "<li>Water</li>",
		`<img src="https://example.com/tomato.png" alt="A tomato" width="640"`, "<figcaption>", "<table>",
		"<pre><code>make check</code></pre>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized output lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `target="_blank"`) || !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Fatalf("a link has no target and rel:\n%s", got)
	}
	if !strings.Contains(got, `href="https://example.com/frost"`) || !strings.Contains(got, `href="mailto:`) == false {
		t.Fatalf("a link lost its href:\n%s", got)
	}
	if got := HTML(`<a href="mailto:alice@example.com">mail</a>`); !strings.Contains(got, "mailto:alice@example.com") {
		t.Fatalf("a mailto link was dropped:\n%s", got)
	}
}

// The text of code, styles, embedded documents and titles is not the
// page's words: it never reaches the index or an agent.
func TestPlainTextSkipsSilentElements(t *testing.T) {
	for _, tag := range []string{"script", "style", "noscript", "template", "svg", "math", "iframe", "object", "title"} {
		html := "<p>before</p><" + tag + ">SENTINEL</" + tag + "><p>after</p>"
		got := PlainText(html)
		if strings.Contains(got, "SENTINEL") {
			t.Fatalf("%s's text reached the plain text: %q", tag, got)
		}
		if got != "before after" {
			t.Fatalf("%s: %q, want the words around it", tag, got)
		}
	}
	// The tags an article may keep still give their words.
	got := PlainText(`<p>Keep <strong>this</strong>.</p><pre><code>code words</code></pre>`)
	if got != "Keep this. code words" {
		t.Fatalf("kept elements = %q", got)
	}
}

func TestPlainText(t *testing.T) {
	got := PlainText(`<p>One <em>two</em>.</p><p>Three</p>`)
	if got != "One two. Three" {
		t.Fatalf("plain text = %q", got)
	}
	// A block's words never run into the next block's: a figure's caption
	// follows the paragraph with a space, not "mind.A".
	got = PlainText(`<p>The dog does not mind.</p><figure><img src="https://example.com/t.png" alt="x"><figcaption>A made-up tomato.</figcaption></figure>`)
	if got != "The dog does not mind. A made-up tomato." {
		t.Fatalf("blocks ran together: %q", got)
	}
	if got := PlainText(`<p>One<br>Two</p><div>Three</div><ul><li>Four</li><li>Five</li></ul>`); got != "One Two Three Four Five" {
		t.Fatalf("blocks = %q", got)
	}
	// html.Parse is forgiving: stray text stays text.
	if got := PlainText("<"); got != "<" {
		t.Fatalf("stray text = %q", got)
	}
}
