package content

import (
	"bytes"
	"encoding/json/v2"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/store"
)

// update rewrites the golden pages: go test ./internal/content -update.
var update = flag.Bool("update", false, "rewrite testdata's pages")

// item seeds one saved link whose extraction is content, with "blob:<1>",
// "blob:<2>" and so on standing for the images' hashes: images[i] is the
// (i+1)-th image's bytes. It returns the handler, the renderer and the item.
func item(t *testing.T, content string, images ...[]byte) (*Handler, *Renderer, store.Item) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	it, err := db.SaveLink(t.Context(), "https://example.com/an-article", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	var stored []store.Blob
	for i, data := range images {
		hash, err := b.Put(t.Context(), bytes.NewReader(data), "image/png")
		if err != nil {
			t.Fatal(err)
		}
		content = strings.ReplaceAll(content, fmt.Sprintf("blob:<%d>", i+1), "blob:"+hash)
		stored = append(stored, store.Blob{Hash: hash, Mime: "image/png", Size: int64(len(data))})
	}
	if err := db.SetExtraction(t.Context(), it.ID, store.Extraction{Title: "An article", Author: "Ann",
		Text: "words", ContentHTML: content, State: store.ExtractDone, Images: stored}); err != nil {
		t.Fatal(err)
	}
	if it, err = db.Item(t.Context(), it.ID); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Renderer: Renderer{Store: db, Blobs: b}, Secret: []byte(strings.Repeat("s", 32))}
	return h, &h.Renderer, it
}

// Both pages come from one renderer, and the desktop's differs from
// lodge's by the CSP meta alone: the golden files in testdata hold them.
func TestTheTwoPagesDifferByTheCSPMeta(t *testing.T) {
	h, r, it := item(t, `<p>A paragraph.</p>
<p><img src="blob:<1>" alt="A tomato"></p>
<p><img src="https://example.com/remote.png" alt="A remote picture"></p>`,
		[]byte("png bytes"))
	// SaveLink stamps the current time; pin the date and the zone, or the
	// golden pages change each day and with the machine's zone.
	it.PublishedAt = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	r.Location = time.UTC
	lodge, err := h.page(t.Context(), it, "dark")
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := r.Render(t.Context(), it, "dark")
	if err != nil {
		t.Fatal(err)
	}
	if desktop.OmittedImages != 0 {
		t.Fatalf("omittedImages = %d, want 0", desktop.OmittedImages)
	}
	meta := `<meta http-equiv="Content-Security-Policy" content="` + CSP + `">` + "\n"
	want := strings.Replace(string(lodge), "<head>\n", "<head>\n"+meta, 1)
	if desktop.HTML != want {
		t.Fatalf("the desktop page is not lodge's page with the CSP meta:\n%s", desktop.HTML)
	}
	for _, page := range []string{string(lodge), desktop.HTML} {
		if !strings.Contains(page, `<img src="data:image/png;base64,`) {
			t.Fatalf("the page does not inline the stored image:\n%s", page)
		}
		if !strings.Contains(page, "<span>A remote picture</span>") || strings.Contains(page, "remote.png") {
			t.Fatalf("the page keeps an image it may not load:\n%s", page)
		}
	}
	check(t, "lodge.html", lodge)
	check(t, "render.html", []byte(desktop.HTML))
}

// check holds a golden page: it writes it with -update, and compares it
// otherwise.
func check(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run go test ./internal/content -update)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run go test ./internal/content -update\n got:\n%s", path, got)
	}
}

// article builds an article's HTML with n distinct images, "blob:<n>" for
// the n-th.
func article(n int, alt string) string {
	var b strings.Builder
	b.WriteString("<p>Words.</p>")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "\n<p><img src=\"blob:<%d>\" alt=\"%s %d\"></p>", i, alt, i)
	}
	return b.String()
}

// An item with 20 MiB of images renders under the real 12 MiB bound, and
// says how many images it left out.
func TestTwentyMebibytesOfImages(t *testing.T) {
	five := func(b byte) []byte { return bytes.Repeat([]byte{b}, 5<<20) }
	_, r, it := item(t, article(4, "A picture"), five(1), five(2), five(3), five(4))
	out, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	if size := answerSize(out); size >= DefaultLimit {
		t.Fatalf("the answer is %d bytes, over the %d bound", size, DefaultLimit)
	}
	// One 5 MiB image is 6.7 MB of base64, so one fits and three do not.
	if out.OmittedImages != 3 {
		t.Fatalf("omittedImages = %d, want 3", out.OmittedImages)
	}
	if n := strings.Count(out.HTML, `src="data:image/png;base64,`); n != 1 {
		t.Fatalf("the page inlines %d images, want 1", n)
	}
	for i := 2; i <= 4; i++ {
		if !strings.Contains(out.HTML, fmt.Sprintf("<span>A picture %d</span>", i)) {
			t.Fatalf("image %d is not its alt text", i)
		}
	}
}

// Text that JSON escaping grows: a quote and a backslash take 2 bytes on
// the wire, and a control character 6.
func TestTextThatEscapingGrows(t *testing.T) {
	text := strings.Repeat(`"quoted" \slash\ `, 40) + "a\x01b"
	_, r, it := item(t, "<p>"+text+"</p>")
	out, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	size := answerSize(out)
	if !strings.Contains(out.HTML, "\x01") {
		t.Fatal("the control character did not reach the page")
	}
	// At the bound the page alone is too large; one byte more fits.
	r.Limit = size
	if _, err := r.Render(t.Context(), it, ""); err == nil {
		t.Fatalf("a page of exactly the %d bound rendered", size)
	}
	r.Limit = size + 1
	if _, err := r.Render(t.Context(), it, ""); err != nil {
		t.Fatalf("one byte under the bound: %v", err)
	}
}

// one is an article that shows one image n times, which counts n times.
func one(n int, alt string) string {
	var b strings.Builder
	b.WriteString("<p>Words.</p>")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "\n<p><img src=\"blob:<1>\" alt=\"%s %d\"></p>", alt, i)
	}
	return b.String()
}

// One image many times counts every time it appears.
func TestOneImageRepeated(t *testing.T) {
	const n = 20
	_, r, it := item(t, one(n, "A picture"), bytes.Repeat([]byte("i"), 1<<20))
	r.Limit = 5 << 20
	out, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	inline := strings.Count(out.HTML, `src="data:image/png;base64,`)
	// A 1 MiB image is 1,398,104 bytes of base64, and 3 of them fit 5 MiB.
	if inline != 3 || out.OmittedImages != n-3 {
		t.Fatalf("inlined %d, omitted %d; want 3 and %d", inline, out.OmittedImages, n-3)
	}
	if size := answerSize(out); size >= r.Limit {
		t.Fatalf("the answer is %d bytes, over the %d bound", size, r.Limit)
	}
}

// An article whose text alone reaches the bound answers tooLarge, and one
// byte under it does not.
func TestTextAloneAtTheBound(t *testing.T) {
	_, r, it := item(t, "<p>"+strings.Repeat("word ", 400)+"</p>")
	out, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	size := answerSize(out)
	if out.OmittedImages != 0 {
		t.Fatalf("omittedImages = %d, want 0", out.OmittedImages)
	}
	r.Limit = size
	if _, err := r.Render(t.Context(), it, ""); !isTooLarge(err) {
		t.Fatalf("a page of exactly the %d bound = %v, want tooLarge", size, err)
	}
	r.Limit = size + 1
	if _, err := r.Render(t.Context(), it, ""); err != nil {
		t.Fatalf("one byte under the bound: %v", err)
	}
}

// isTooLarge reports whether err is ErrTooLarge.
func isTooLarge(err error) bool { return err != nil && err == ErrTooLarge }

// The page follows the scheme the reader names, and its CSP is first in its
// head.
func TestRenderFollowsTheScheme(t *testing.T) {
	_, r, it := item(t, "<p>Words.</p>")
	out, err := r.Render(t.Context(), it, "light")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.HTML, `<html lang="en" data-scheme="light">`) {
		t.Fatalf("the page does not follow the scheme:\n%s", out.HTML)
	}
	if !strings.Contains(out.HTML, "<head>\n"+`<meta http-equiv="Content-Security-Policy"`) {
		t.Fatalf("the CSP meta is not first in the head:\n%s", out.HTML)
	}
}

// An item that is not extracted yet shows its feed's summary, sanitized, as
// the content page does.
func TestRenderOfAPendingItem(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	src, err := db.AddSource(t.Context(), store.Source{URL: "https://example.org/feed.xml", Title: "Example"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := db.AddItems(t.Context(), src.ID, []store.NewItem{{GUID: "1", Title: "A pending article",
		URL: "https://example.org/pending", SummaryHTML: `<script>alert(1)</script><p>the summary</p>`}})
	if err != nil || len(ids) != 1 {
		t.Fatalf("items = %v, %v", ids, err)
	}
	it, err := db.Item(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	r := &Renderer{Store: db, Blobs: b}
	out, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.HTML, "<script") || !strings.Contains(out.HTML, "the summary") {
		t.Fatalf("the page does not hold the sanitized summary:\n%s", out.HTML)
	}
}

// An image the store never held is not one the bound left out: it shows as
// its alt text, as on lodge's page, and omittedImages does not count it.
func TestAMissingImageIsNotOmitted(t *testing.T) {
	missing := "blob:" + strings.Repeat("0", 64)
	_, r, it := item(t, `<p>Words.</p><p><img src="`+missing+`" alt="Never fetched"></p><p><img src="blob:<1>" alt="Kept"></p>`,
		[]byte("png bytes"))
	got, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.OmittedImages != 0 {
		t.Fatalf("omittedImages = %d, want 0", got.OmittedImages)
	}
	if !strings.Contains(got.HTML, "<span>Never fetched</span>") || strings.Contains(got.HTML, missing) {
		t.Fatalf("the missing image is not its alt text:\n%s", got.HTML)
	}
	if !strings.Contains(got.HTML, `<img src="data:image/png;base64,`) {
		t.Fatalf("the stored image is not inlined:\n%s", got.HTML)
	}
}

// The page step 1 measures is the page the fallback answers, with every
// stored image counted: an omittedImages of several digits is counted too.
// At the exact bound, the line the kit writes for the longest id the guard
// admits stays under it.
func TestTheFallbackCountsItsOmissions(t *testing.T) {
	const images = 100
	var html strings.Builder
	html.WriteString("<p>Many pictures.</p>")
	data := make([][]byte, images)
	for i := range images {
		data[i] = []byte(fmt.Sprintf("png bytes %03d", i))
		fmt.Fprintf(&html, `<p><img src="blob:<%d>" alt="Picture"></p>`, i+1)
	}
	_, r, it := item(t, html.String(), data...)

	// The answer step 1 measures: no image, all 100 counted.
	bare, all := imageRewrite(inlineOnly(r.body(it)), func(string) (string, bool) { return "", true })
	if all != images {
		t.Fatalf("the article has %d stored images, want %d", all, images)
	}
	size := answerSize(Rendered{HTML: r.document(it, "", true, bare), OmittedImages: images})

	// At the bound: tooLarge, though the page with a count of 0 would fit.
	r.Limit = size
	if _, err := r.Render(t.Context(), it, ""); !isTooLarge(err) {
		t.Fatalf("at the bound, Render = %v, want tooLarge", err)
	}

	// One byte over: the page, with no image (none fits a budget of 1) and
	// all 100 counted, and its line under the bound with a 990-byte id.
	r.Limit = size + 1
	got, err := r.Render(t.Context(), it, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.OmittedImages != images || strings.Contains(got.HTML, "data:image/") {
		t.Fatalf("omittedImages = %d, want %d and no image", got.OmittedImages, images)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	id := `"` + strings.Repeat("i", MaxRequestID-2) + `"`
	line := `{"jsonrpc":"2.0","id":` + id + `,"result":` + string(raw) + "}\n"
	if len(line) >= r.Limit {
		t.Fatalf("the line takes %d bytes, want under %d", len(line), r.Limit)
	}
}
