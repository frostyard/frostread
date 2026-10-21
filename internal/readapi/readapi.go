// Package readapi implements the Read app's RPC contract over the engine:
// the browser session and readctl arrive here, and this is where an
// error becomes the contract's code (docs/specs/api.md).
package readapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/notes"
	"github.com/frostyard/frostread/internal/opml"
	"github.com/frostyard/frostread/internal/store"

	"github.com/frostyard/lodge/kit/netguard"
)

// Service is the API's read domain, over the engine.
type Service struct {
	Engine *engine.Engine
	// Log receives a refusal's detail, which the client never sees; nil
	// discards it.
	Log *slog.Logger
}

var _ api.ReadService = (*Service)(nil)

// New returns a Service.
func New(e *engine.Engine, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{Engine: e, Log: log}
}

// write is the browser's and the CLI's scope: both may change anything.
var write = engine.Scope{Write: true}

// Status implements status.
func (s *Service) Status(ctx context.Context) (engine.Status, error) {
	return s.Engine.Status(ctx), nil
}

// Sources implements sources.list.
func (s *Service) Sources(ctx context.Context) ([]api.Source, error) {
	sources, err := s.Engine.Sources(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	out := make([]api.Source, 0, len(sources))
	for _, src := range sources {
		out = append(out, source(src))
	}
	return out, nil
}

// AddSource implements sources.add.
func (s *Service) AddSource(ctx context.Context, url string) (api.Source, error) {
	src, err := s.Engine.AddSource(ctx, url)
	if err != nil {
		return api.Source{}, s.fail(err)
	}
	return source(src), nil
}

// RemoveSource implements sources.remove.
func (s *Service) RemoveSource(ctx context.Context, id int64) error {
	return s.fail(s.Engine.RemoveSource(ctx, id))
}

// RenameSource implements sources.rename.
func (s *Service) RenameSource(ctx context.Context, id int64, title string) error {
	return s.fail(s.Engine.RenameSource(ctx, id, title))
}

// Refresh implements sources.refresh.
func (s *Service) Refresh(ctx context.Context, id *int64) error {
	return s.fail(s.Engine.Refresh(ctx, id))
}

// ImportOPML implements opml.import.
func (s *Service) ImportOPML(ctx context.Context, document string) (int, int, error) {
	added, skipped, err := s.Engine.ImportOPML(ctx, document)
	return added, skipped, s.fail(err)
}

// ExportOPML implements opml.export.
func (s *Service) ExportOPML(ctx context.Context) (string, error) {
	out, err := s.Engine.ExportOPML(ctx)
	return out, s.fail(err)
}

// ListItems implements items.list.
func (s *Service) ListItems(ctx context.Context, p api.ItemsListParams) (api.ItemsPage, error) {
	if !store.ValidView(p.View) {
		return api.ItemsPage{}, api.Errorf(api.CodeInvalidParams, "%q is not a view", p.View)
	}
	limit, err := limit(p.Limit)
	if err != nil {
		return api.ItemsPage{}, err
	}
	var sourceID *int64
	if p.SourceID != 0 {
		sourceID = &p.SourceID
	}
	items, next, err := s.Engine.ListItems(ctx, store.ListOptions{
		View: p.View, SourceID: sourceID, Cursor: p.Cursor, Limit: limit,
	})
	if err != nil {
		return api.ItemsPage{}, s.fail(err)
	}
	return api.ItemsPage{Items: items, Next: next}, nil
}

// GetItem implements items.get.
func (s *Service) GetItem(ctx context.Context, id int64) (engine.Detail, error) {
	d, err := s.Engine.Item(ctx, id)
	if err != nil {
		return engine.Detail{}, s.fail(err)
	}
	return d, nil
}

// Search implements items.search.
func (s *Service) Search(ctx context.Context, query string, n int) (api.ItemsPage, error) {
	limit, err := limit(n)
	if err != nil {
		return api.ItemsPage{}, err
	}
	items, err := s.Engine.Search(ctx, query, limit)
	if err != nil {
		return api.ItemsPage{}, s.fail(err)
	}
	return api.ItemsPage{Items: items}, nil
}

// SetRead implements items.setRead.
func (s *Service) SetRead(ctx context.Context, ids []int64, read bool) error {
	return s.fail(s.Engine.SetRead(ctx, write, ids, read))
}

// MarkAllRead implements items.markAllRead.
func (s *Service) MarkAllRead(ctx context.Context, view string, sourceID *int64, before time.Time) error {
	if view == "" {
		view = store.ViewAll
	}
	if !store.ValidView(view) {
		return api.Errorf(api.CodeInvalidParams, "%q is not a view", view)
	}
	return s.fail(s.Engine.MarkAllRead(ctx, write, view, sourceID, before))
}

// SetStarred implements items.setStarred.
func (s *Service) SetStarred(ctx context.Context, id int64, starred bool) error {
	return s.fail(s.Engine.SetStarred(ctx, write, id, starred))
}

// SetArchived implements items.setArchived.
func (s *Service) SetArchived(ctx context.Context, id int64, archived bool) error {
	return s.fail(s.Engine.SetArchived(ctx, write, id, archived))
}

// SaveAsNote implements items.saveAsNote.
func (s *Service) SaveAsNote(ctx context.Context, id int64) (string, string, error) {
	path, route, err := s.Engine.SaveAsNote(ctx, id)
	return path, route, s.fail(err)
}

// NoteStatus implements items.noteStatus.
func (s *Service) NoteStatus(ctx context.Context, id int64) (api.NoteStatusResult, error) {
	st, err := s.Engine.NoteStatus(ctx, id)
	if err != nil {
		return api.NoteStatusResult{}, s.fail(err)
	}
	out := api.NoteStatusResult{State: api.NoteState(st.State), Path: st.Path, Error: st.Error}
	if st.State == notes.StatePresent || st.State == notes.StateMoved {
		out.Route = s.Engine.NoteRoute(st.Path)
	}
	return out, nil
}

// SaveURL implements later.save.
func (s *Service) SaveURL(ctx context.Context, url, title string) (engine.Summary, error) {
	it, err := s.Engine.SaveURL(ctx, write, url, title)
	if err != nil {
		return engine.Summary{}, s.fail(err)
	}
	d, err := s.Engine.Item(ctx, it.ID)
	if err != nil {
		return engine.Summary{}, s.fail(err)
	}
	return d.Summary, nil
}

// Counts implements counts.
func (s *Service) Counts(ctx context.Context) (api.Counts, error) {
	c, err := s.Engine.Counts(ctx)
	if err != nil {
		return api.Counts{}, s.fail(err)
	}
	return api.Counts{Views: c.Views, Sources: c.Sources}, nil
}

// SetNotesToken implements notes.setToken.
func (s *Service) SetNotesToken(ctx context.Context, token string) error {
	return s.fail(s.Engine.SetNotesToken(ctx, token))
}

// source converts a stored source to the API's shape.
func source(src store.Source) api.Source {
	return api.Source{ID: src.ID, URL: src.URL, SiteURL: src.SiteURL, Title: src.Title,
		Unread: src.Unread, ErrorCount: src.ErrorCount, LastError: src.LastError,
		PolledAt: src.PolledAt.UTC(), NextPollAt: src.NextPollAt.UTC()}
}

// limit bounds a page: 50 when 0, at most 200.
func limit(n int) (int, error) {
	switch {
	case n == 0:
		return 50, nil
	case n < 1 || n > 200:
		return 0, api.Errorf(api.CodeInvalidParams, "limit must be 1 to 200")
	}
	return n, nil
}

// fail turns an engine error into the contract's code. An error this does
// not know reaches the client as internal. A refusal becomes one sentence,
// and its detail goes to the log.
func (s *Service) fail(err error) error {
	if err == nil {
		return nil
	}
	if sentence, ok := fetch.Refusal(err); ok {
		s.Log.Info("a fetch was refused", "err", err)
		return api.Errorf(api.CodeInvalidParams, "%s", sentence)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return api.Errorf(api.CodeNotFound, "%v", err)
	case errors.Is(err, store.ErrConflict):
		return api.Errorf(api.CodeConflict, "%v", err)
	case errors.Is(err, engine.ErrUnavailable), errors.Is(err, notes.ErrUnavailable):
		return api.Errorf(api.CodeUnavailable, "%v", err)
	case errors.Is(err, engine.ErrReadOnly):
		return api.Errorf(api.CodeInvalidParams, "%v", err)
	case errors.Is(err, fetch.ErrRefused), errors.Is(err, fetch.ErrTooLarge),
		errors.Is(err, netguard.ErrInside), errors.Is(err, feed.ErrInvalid),
		errors.Is(err, opml.ErrInvalid), errors.Is(err, notes.ErrTooMany):
		return api.Errorf(api.CodeInvalidParams, "%v", err)
	default:
		return err
	}
}
