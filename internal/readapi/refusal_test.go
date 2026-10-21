package readapi_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/store"
)

// noNetwork fails every fetch: a refused URL never reaches it.
type noNetwork struct{}

func (noNetwork) Get(context.Context, string, fetch.Options) (*fetch.Response, error) {
	return nil, errors.New("the network must not be used")
}

// service is an engine over a temp store, and the API over it.
func service(t *testing.T) *readapi.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	eng := engine.New(engine.Options{
		Store: db, Blobs: b, Fetch: noNetwork{}, Extractor: &extract.Extractor{Fetch: noNetwork{}, Blobs: b},
		Config: config.Config{PollInterval: "20m", NotesFolder: "Reading/"}, Interval: 20 * time.Minute,
		Version: "test",
	})
	return readapi.New(eng, nil)
}

// apiError is an API error's code and message, as the wire carries them.
func apiError(t *testing.T, err error) (api.ErrorCode, string) {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("%v is not an API error", err)
	}
	return e.Code, e.Message
}

// A refusal is one sentence, not the guard's chain, and it is the same for
// a source and for a saved link.
func TestARefusalIsOneSentence(t *testing.T) {
	s := service(t)
	if _, err := s.AddSource(t.Context(), "http://192.0.2.1/feed.xml"); err == nil {
		t.Fatal("adding an inside feed succeeded")
	} else {
		got, message := apiError(t, err)
		want := "Read does not fetch addresses inside your network (192.0.2.1)."
		if got != api.CodeInvalidParams || message != want {
			t.Fatalf("code = %v, message = %q; want invalidParams and %q", got, message, want)
		}
	}
	if _, err := s.SaveURL(t.Context(), "http://192.0.2.1/page"); err == nil {
		t.Fatal("saving an inside link succeeded")
	} else {
		want := "Read does not fetch addresses inside your network (192.0.2.1)."
		if got, message := apiError(t, err); message != want || got != api.CodeInvalidParams {
			t.Fatalf("message = %q, code %v; want %q", message, got, want)
		}
	}
	if _, err := s.SaveURL(t.Context(), "file:///etc/passwd"); err == nil {
		t.Fatal("saving a file: link succeeded")
	} else if _, message := apiError(t, err); !strings.HasPrefix(message, "Read fetches only public http and https addresses") {
		t.Fatalf("message = %q", message)
	}
}
