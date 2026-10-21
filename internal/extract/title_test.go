package extract

import "testing"

func TestStripTitle(t *testing.T) {
	for _, tc := range []struct{ name, text, title, want string }{
		{"the feed's summary repeats it", "The garden in November The garden in November This is paragraph 1. More.",
			"The garden in November", "This is paragraph 1. More."},
		{"the extractor keeps the heading", "The garden in November This is paragraph 1.",
			"The garden in November", "This is paragraph 1."},
		{"a colon after it", "The garden in November: a note on frost.",
			"The garden in November", "a note on frost."},
		{"a dash after it", "The garden in November – a note.",
			"The garden in November", "a note."},
		{"line breaks and case", "the garden in\nNovember\nThis is the body.",
			"The Garden in November", "This is the body."},
		{"the title alone", "The garden in November", "The garden in November", ""},
		{"a sentence that starts with it", "The garden in November is lovely.",
			"The garden in November", "The garden in November is lovely."},
		{"a longer word", "The gardens in November are bare.",
			"The garden in November", "The gardens in November are bare."},
		{"nothing to strip", "Pears by the wall.", "The garden in November", "Pears by the wall."},
		{"no title", "Pears by the wall.", "", "Pears by the wall."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripTitle(tc.text, tc.title); got != tc.want {
				t.Fatalf("StripTitle(%q, %q) = %q, want %q", tc.text, tc.title, got, tc.want)
			}
		})
	}
}
