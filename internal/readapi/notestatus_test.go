package readapi_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/store"
)

// finder is a notes client whose Find answers what the test sets.
type finder struct{ status notes.Status }

func (f *finder) Save(context.Context, notes.Article) (string, error) {
	return "Reading/Note.md", nil
}

func (f *finder) Find(context.Context, string, int64) notes.Status { return f.status }

// statusEngine is an engine with notes configured, its status answering f.
func statusEngine(t *testing.T, f *finder) (*readapi.Service, *store.DB) {
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
		Config:   config.Config{PollInterval: "20m", NotesFolder: "Reading/", NotesVault: 1},
		Interval: 20 * time.Minute, Version: "test", Notes: f,
	})
	return readapi.New(eng, nil), db
}

// The API answers what notes said, with the route, and keeps note_path true.
func TestNoteStatusOverTheAPI(t *testing.T) {
	f := &finder{status: notes.Status{State: notes.StatePresent, Path: "Reading/Note.md"}}
	s, db := statusEngine(t, f)
	it, err := db.SaveLink(t.Context(), "https://example.org/a", "example.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetExtraction(t.Context(), it.ID, store.Extraction{ContentHTML: "<p>Words.</p>", State: store.ExtractDone}); err != nil {
		t.Fatal(err)
	}
	// Save names the note, so note_path is set.
	notesAPI := s
	if _, _, err := notesAPI.SaveAsNote(t.Context(), it.ID); err != nil {
		t.Fatal(err)
	}

	st, err := notesAPI.NoteStatus(t.Context(), it.ID)
	if err != nil || st.State != api.NotePresent || st.Path != "Reading/Note.md" {
		t.Fatalf("present = %+v, %v", st, err)
	}
	if st.Route != "/notes/1/Reading/Note.md" {
		t.Fatalf("route = %q", st.Route)
	}

	f.status = notes.Status{State: notes.StateMissing}
	if st, err = notesAPI.NoteStatus(t.Context(), it.ID); err != nil || st.State != api.NoteMissing || st.Path != "" {
		t.Fatalf("missing = %+v, %v", st, err)
	}
	if got, _ := db.Item(t.Context(), it.ID); got.NotePath != "" {
		t.Fatalf("the missing note's path stayed: %q", got.NotePath)
	}

	f.status = notes.Status{State: notes.StateMoved, Path: "Reading/New.md"}
	if st, err = notesAPI.NoteStatus(t.Context(), it.ID); err != nil || st.State != api.NoteMoved {
		t.Fatalf("moved = %+v, %v", st, err)
	}
	if st.Route != "/notes/1/Reading/New.md" {
		t.Fatalf("moved route = %q", st.Route)
	}

	f.status = notes.Status{State: notes.StateUnreachable, Error: "notes is down"}
	if st, err = notesAPI.NoteStatus(t.Context(), it.ID); err != nil || st.State != api.NoteUnreachable || st.Error != "notes is down" {
		t.Fatalf("unreachable = %+v, %v", st, err)
	}
	if got, _ := db.Item(t.Context(), it.ID); got.NotePath == "" {
		t.Fatal("an unreachable notes dropped the link")
	}
}

// Without a notes token, items.noteStatus answers unavailable.
func TestNoteStatusWithoutNotes(t *testing.T) {
	s := service(t)
	if _, err := s.NoteStatus(t.Context(), 1); err == nil {
		t.Fatal("without notes the status succeeded")
	} else if got, _ := apiError(t, err); got != api.CodeUnavailable {
		t.Fatalf("without notes = %v, want unavailable", err)
	}
}
