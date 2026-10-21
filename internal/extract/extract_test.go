package extract

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/lodge/kit/netguard"
)

// pages is a fake network: a URL's body and content type.
type pages map[string]struct {
	body []byte
	typ  string
	err  error
}

func (p pages) Get(_ context.Context, url string, o fetch.Options) (*fetch.Response, error) {
	page, ok := p[url]
	if !ok {
		return nil, errors.New("no such page: " + url)
	}
	if page.err != nil {
		return nil, page.err
	}
	if int64(len(page.body)) > o.MaxBytes {
		return nil, fetch.ErrTooLarge
	}
	return &fetch.Response{Status: 200, URL: url, Body: page.body,
		Header: http.Header{"Content-Type": {page.typ}}}, nil
}

// png is a 1x1 PNG.
var png = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42,
	0x60, 0x82,
}

const article = `<!doctype html>
<html><head><title>Gardening</title><meta property="og:site_name" content="Example Journal"></head>
<body>
<article>
<h1>Gardening in the spring</h1>
<p>By Ann Author</p>
<p>Plant tomatoes after the last frost. A tomato plant needs sun, water and a little patience.
The soil should be warm and loose, and the plant should go in deep enough that its stem can root.
Water it well for the first week, then less often and more deeply, and the roots will follow the water down.</p>
<p><img src="/tomato.png" alt="A ripe tomato"></p>
<p><img src="http://127.0.0.1:9/inside.png" alt="An inside picture"></p>
</article>
</body></html>`

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	b := blobs.New(dir)
	x := &Extractor{Blobs: b, Fetch: pages{
		"https://example.com/tomato.png": {body: png, typ: "image/png"},
		"http://127.0.0.1:9/inside.png":  {err: netguard.ErrInside},
	}}
	got, err := x.Extract(t.Context(), []byte(article), "https://example.com/garden")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Gardening" {
		t.Fatalf("title = %q", got.Title)
	}
	if !strings.Contains(got.ContentHTML, "blob:") {
		t.Fatalf("the article keeps no blob marker:\n%s", got.ContentHTML)
	}
	if strings.Contains(got.ContentHTML, "127.0.0.1") {
		t.Fatalf("the inside image kept its address:\n%s", got.ContentHTML)
	}
	if !strings.Contains(got.ContentHTML, "An inside picture") {
		t.Fatalf("the refused image lost its alt text:\n%s", got.ContentHTML)
	}
	if !strings.Contains(got.ContentHTML, `href="https://example.com/tomato.png"`) {
		t.Fatalf("the image is not linked to its original:\n%s", got.ContentHTML)
	}
	if len(got.Images) != 1 {
		t.Fatalf("images = %+v, want the tomato alone", got.Images)
	}
	if got.Images[0].Mime != "image/png" || got.Images[0].Size != int64(len(png)) {
		t.Fatalf("image = %+v", got.Images[0])
	}
	if content, err := b.Get(got.Images[0].Hash); err != nil || len(content) != len(png) {
		t.Fatalf("the image is not in the store: %d, %v", len(content), err)
	}
	if got.Text == "" || got.Excerpt == "" {
		t.Fatal("the article has no text")
	}
	if got.Published.IsZero() {
		t.Log("the page names no published date, which is fine")
	}
}

func TestExtractRejectsAnEmptyPage(t *testing.T) {
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{}}
	if _, err := x.Extract(t.Context(), []byte("<html><body><p>tiny</p></body></html>"), "https://example.com/"); !errors.Is(err, ErrNoArticle) {
		t.Fatalf("a page with no article = %v, want ErrNoArticle", err)
	}
}

func TestInline(t *testing.T) {
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{
		"https://example.com/tomato.png": {body: png, typ: "image/png"},
	}}
	content, images, err := x.Inline(t.Context(), `<p>Full text.</p><img src="tomato.png" alt="A tomato">`,
		"https://example.com/feed-entry", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !strings.Contains(content, "blob:"+images[0].Hash) {
		t.Fatalf("inline = %q, %+v", content, images)
	}
}

func TestSniff(t *testing.T) {
	avif := make([]byte, 32)
	copy(avif[4:], "ftypavif")
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"png", png, "image/png"},
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0}, "image/jpeg"},
		{"gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), "image/gif"},
		{"avif", avif, "image/avif"},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), ""},
		{"html", []byte("<html><body>not an image</body></html>"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sniff(tc.body); got != tc.want {
				t.Fatalf("sniff = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := excerpt(long)
	if len([]rune(got)) > 301 {
		t.Fatalf("excerpt is %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a cut excerpt ends with an ellipsis: %q", got)
	}
	if excerpt("short") != "short" {
		t.Fatal("a short text changed")
	}
}

func TestPublishedTime(t *testing.T) {
	page := strings.Replace(article, `<meta property="og:site_name"`,
		`<meta property="article:published_time" content="2026-10-20T09:00:00Z"><meta property="og:site_name"`, 1)
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{
		"https://example.com/tomato.png": {body: png, typ: "image/png"},
	}}
	got, err := x.Extract(t.Context(), []byte(page), "https://example.com/garden")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if !got.Published.Equal(want) {
		t.Fatalf("published = %v, want %v", got.Published, want)
	}
}

// A page whose heading is its title does not repeat it in the extracted
// text: the reader shows the title above the text.
func TestExtractDropsTheHeading(t *testing.T) {
	page := `<!doctype html><html><head><title>Keeping a reading list</title></head><body>
<article><h1>Keeping a reading list</h1>` + strings.Repeat("A list is a small kindness to your later self, and it works best when it is short. ", 6) +
		`</article></body></html>`
	x := &Extractor{Blobs: blobs.New(t.TempDir()), Fetch: pages{}}
	got, err := x.Extract(t.Context(), []byte(page), "https://example.com/list")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Keeping a reading list" {
		t.Fatalf("title = %q", got.Title)
	}
	if strings.HasPrefix(got.Text, "Keeping a reading list") {
		t.Fatalf("the text starts with the title:\n%s", got.Text)
	}
	if !strings.HasPrefix(got.Text, "A list is a small kindness") {
		t.Fatalf("the text lost its first words:\n%s", got.Text)
	}
	if !strings.HasPrefix(got.Excerpt, "A list") {
		t.Fatalf("excerpt = %q", got.Excerpt)
	}
}
