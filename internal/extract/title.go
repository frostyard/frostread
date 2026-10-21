package extract

import (
	"strings"
	"unicode"
)

// StripTitle removes a leading copy of title from text, and any separator
// after it, so an excerpt or an article's text does not repeat the title
// the reader already shows above it. A feed often puts its item's title at
// the start of the summary, and a readability extraction keeps the page's
// heading; the text then holds it once or twice.
//
// It strips only when the title is at the very start, and either a
// separator follows it, or the next word starts a sentence: an uppercase
// letter, a digit or an opening quote. "The garden in November is lovely"
// keeps its words, and "The garden in November The garden in November This
// is paragraph 1" becomes "This is paragraph 1".
func StripTitle(text, title string) string {
	text = strings.TrimSpace(text)
	title = strings.TrimSpace(title)
	if title == "" || text == "" {
		return text
	}
	for range 3 {
		rest, ok := cutTitle(text, title)
		if !ok {
			break
		}
		text = strings.TrimSpace(strings.TrimLeft(rest, " \t\n:–—-|.,;"))
		if text == "" {
			return ""
		}
	}
	return text
}

// cutTitle cuts title from text's start when it matches there, whitespace
// runs counting alike, and returns the rest. It matches with case folded.
func cutTitle(text, title string) (string, bool) {
	t, x := []rune(title), []rune(text)
	i, j := 0, 0
	for j < len(t) {
		switch {
		case unicode.IsSpace(t[j]):
			if i >= len(x) || !unicode.IsSpace(x[i]) {
				return "", false
			}
			for i < len(x) && unicode.IsSpace(x[i]) {
				i++
			}
			for j < len(t) && unicode.IsSpace(t[j]) {
				j++
			}
		case i < len(x) && unicode.ToLower(x[i]) == unicode.ToLower(t[j]):
			i++
			j++
		default:
			return "", false
		}
	}
	if i == len(x) {
		return "", true // the text is the title alone
	}
	if !unicode.IsSpace(x[i]) && !strings.ContainsRune(":–—-|.,;", x[i]) {
		return "", false // the title runs into a longer word
	}
	for i < len(x) && unicode.IsSpace(x[i]) {
		i++
	}
	if i >= len(x) {
		return "", true
	}
	// A separator after the title is always a title's own punctuation.
	if strings.ContainsRune(":–—-|.,;", x[i]) {
		return string(x[i:]), true
	}
	// A space is a title followed by a sentence only when a sentence
	// starts there; "The garden in November is lovely" is not one.
	if unicode.IsUpper(x[i]) || unicode.IsDigit(x[i]) || strings.ContainsRune(`"'“‘(`, x[i]) {
		return string(x[i:]), true
	}
	return "", false
}
