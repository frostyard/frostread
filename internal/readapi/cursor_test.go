// items.list answers a cursor the daemon did not issue with invalidParams,
// as docs/specs/api.md says (Errors), and not with notFound.
package readapi_test

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"testing"

	"github.com/frostyard/frostread/api"
)

// TestListItemsRefusesAForeignCursor checks a cursor that does not decode,
// and one that decodes to something makeCursor never wrote.
func TestListItemsRefusesAForeignCursor(t *testing.T) {
	s := seed(t)
	ctx := api.WithCaller(t.Context(), api.CallerSession)
	encoded := func(raw string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(raw))
	}
	for _, c := range []struct{ name, cursor string }{
		{"does not decode", "not-a-cursor"},
		{"decodes but has no separator", encoded("2026-10-20T12:00:00.000Z")},
		{"decodes but has no time", encoded("|1")},
		{"decodes but its id is not a number", encoded("2026-10-20T12:00:00.000Z|seven")},
		{"decodes but its id is not positive", encoded("2026-10-20T12:00:00.000Z|0")},
	} {
		t.Run(c.name, func(t *testing.T) {
			params := `{"view":"all","cursor":"` + c.cursor + `"}`
			_, err := s.router.Dispatch(ctx, "items.list", jsontext.Value(params))
			if code := codeOf(t, err); code != api.CodeInvalidParams {
				t.Fatalf("items.list with the cursor %q = %v, code %v; want invalidParams", c.cursor, err, code)
			}
		})
	}
}

// TestListItemsAcceptsTheCursorItIssued keeps the refusal above honest: a
// cursor makeCursor wrote reaches the store and answers a page.
func TestListItemsAcceptsTheCursorItIssued(t *testing.T) {
	s := seed(t)
	ctx := api.WithCaller(t.Context(), api.CallerSession)
	out, err := s.router.Dispatch(ctx, "items.list", jsontext.Value(`{"view":"all","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	page, ok := out.(api.ItemsPage)
	if !ok {
		t.Fatalf("items.list = %#v", out)
	}
	if len(page.Items) != 1 || page.Next == "" {
		t.Fatalf("first page = %+v, want one item and a next cursor", page)
	}
	more, err := s.router.Dispatch(ctx, "items.list", jsontext.Value(
		`{"view":"all","limit":1,"cursor":"`+page.Next+`"}`))
	if err != nil {
		t.Fatalf("items.list with its own cursor: %v", err)
	}
	if second, ok := more.(api.ItemsPage); !ok || len(second.Items) != 1 ||
		second.Items[0].ID == page.Items[0].ID {
		t.Fatalf("second page = %#v", more)
	}
}
