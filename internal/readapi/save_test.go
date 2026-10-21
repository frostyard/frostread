// later.save with a title (docs/specs/api.md, later.save): the API passes
// the page's title to the engine, which cleans and keeps it.
package readapi_test

import "testing"

func TestSaveURLKeepsThePageTitle(t *testing.T) {
	s := service(t)
	it, err := s.SaveURL(t.Context(), "https://example.org/from-my-phone", "  A page\nfrom my phone  ")
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "A page from my phone" {
		t.Fatalf("title = %q, want the page's own title", it.Title)
	}
	if !it.Saved {
		t.Fatalf("item = %+v, want a saved link", it)
	}
}

func TestSaveURLWithoutATitleNamesTheHost(t *testing.T) {
	s := service(t)
	it, err := s.SaveURL(t.Context(), "https://example.org/not-titled", "")
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "example.org" {
		t.Fatalf("title = %q, want the URL's host", it.Title)
	}
}
