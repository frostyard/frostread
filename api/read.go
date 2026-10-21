// What a generator writes from schema/rpc/read.yaml, written by hand until
// the app has one. Keep it in step with schema/rpc.

package api

import (
	"context"
	"encoding/json/jsontext"
	"time"

	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/store"
)

// ---- read ----

// Source is one subscription, as the app shows it.
type Source struct {
	ID         int64     `json:"id"`
	URL        string    `json:"url"`
	SiteURL    string    `json:"siteUrl,omitzero"`
	Title      string    `json:"title"`
	Unread     int       `json:"unread"`
	ErrorCount int       `json:"errorCount,omitzero"`
	LastError  string    `json:"lastError,omitzero"`
	PolledAt   time.Time `json:"polledAt,omitzero"`
	NextPollAt time.Time `json:"nextPollAt,omitzero"`
}

// SourceList is sources.list's result.
type SourceList struct {
	Sources []Source `json:"sources"`
}

// SourceAddParams holds the params of sources.add.
type SourceAddParams struct {
	URL string `json:"url"`
}

// SourceResult is sources.add's result.
type SourceResult struct {
	Source Source `json:"source"`
}

// SourceIDParams holds a source's id.
type SourceIDParams struct {
	ID int64 `json:"id"`
}

// SourceRenameParams holds the params of sources.rename.
type SourceRenameParams struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// SourceRefreshParams holds the params of sources.refresh; a 0 id polls
// every source.
type SourceRefreshParams struct {
	ID int64 `json:"id,omitzero"`
}

// OPMLImportParams holds the params of opml.import.
type OPMLImportParams struct {
	XML string `json:"xml"`
}

// OPMLImport is opml.import's result.
type OPMLImport struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
}

// OPMLDocument is opml.export's result.
type OPMLDocument struct {
	XML string `json:"xml"`
}

// ItemsListParams holds the params of items.list.
type ItemsListParams struct {
	View     string `json:"view"`
	SourceID int64  `json:"sourceId,omitzero"`
	Cursor   string `json:"cursor,omitzero"`
	Limit    int    `json:"limit,omitzero"`
}

// ItemsPage is items.list's and items.search's result.
type ItemsPage struct {
	Items []engine.Summary `json:"items"`
	Next  string           `json:"next,omitzero"`
}

// ItemParams holds an item's id.
type ItemParams struct {
	ID int64 `json:"id"`
}

// ItemsSearchParams holds the params of items.search.
type ItemsSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitzero"`
}

// ItemsSetReadParams holds the params of items.setRead.
type ItemsSetReadParams struct {
	IDs  []int64 `json:"ids"`
	Read bool    `json:"read"`
}

// ItemsMarkAllParams holds the params of items.markAllRead.
type ItemsMarkAllParams struct {
	View     string    `json:"view,omitzero"`
	SourceID int64     `json:"sourceId,omitzero"`
	Before   time.Time `json:"before,omitzero"`
}

// ItemStarParams holds the params of items.setStarred.
type ItemStarParams struct {
	ID      int64 `json:"id"`
	Starred bool  `json:"starred"`
}

// ItemArchiveParams holds the params of items.setArchived.
type ItemArchiveParams struct {
	ID       int64 `json:"id"`
	Archived bool  `json:"archived"`
}

// NoteResult is items.saveAsNote's result.
type NoteResult struct {
	Path  string `json:"path"`
	Route string `json:"route"`
}

// SaveParams holds the params of later.save. Title is the page's title,
// which the bookmarklet sends; it is other people's words, so the daemon
// cleans it and cuts it to 300 runes.
type SaveParams struct {
	URL   string `json:"url"`
	Title string `json:"title,omitzero"`
}

// ItemResult is later.save's result.
type ItemResult struct {
	Item engine.Summary `json:"item"`
}

// NoteStatusParams holds the params of items.noteStatus.
type NoteStatusParams struct {
	ID int64 `json:"id"`
}

// NoteState is where an item's note is.
type NoteState string

const (
	// NotePresent means the note is at the item's note_path.
	NotePresent NoteState = "present"
	// NoteMoved means the note is elsewhere in the vault, and found.
	NoteMoved NoteState = "moved"
	// NoteMissing means no note holds the item's marker any more.
	NoteMissing NoteState = "missing"
	// NoteUnreachable means notes could not be asked; the link stays.
	NoteUnreachable NoteState = "unreachable"
)

// NoteStatusResult is items.noteStatus's result.
type NoteStatusResult struct {
	State NoteState `json:"state"`
	Path  string    `json:"path,omitzero"`
	Route string    `json:"route,omitzero"`
	Error string    `json:"error,omitzero"`
}

// Counts is the counts method's result.
type Counts struct {
	Views   map[string]int      `json:"views"`
	Sources []store.SourceCount `json:"sources"`
}

// NotesTokenParams holds the params of notes.setToken.
type NotesTokenParams struct {
	Token string `json:"token"`
}

// ReadService is the read domain's methods. The API calls it as the
// browser session or the CLI; its writes carry the session's right.
type ReadService interface {
	Status(ctx context.Context) (engine.Status, error)
	Sources(ctx context.Context) ([]Source, error)
	AddSource(ctx context.Context, url string) (Source, error)
	RemoveSource(ctx context.Context, id int64) error
	RenameSource(ctx context.Context, id int64, title string) error
	Refresh(ctx context.Context, id *int64) error
	ImportOPML(ctx context.Context, document string) (added, skipped int, err error)
	ExportOPML(ctx context.Context) (string, error)
	ListItems(ctx context.Context, p ItemsListParams) (ItemsPage, error)
	GetItem(ctx context.Context, id int64) (engine.Detail, error)
	Search(ctx context.Context, query string, limit int) (ItemsPage, error)
	SetRead(ctx context.Context, ids []int64, read bool) error
	MarkAllRead(ctx context.Context, view string, sourceID *int64, before time.Time) error
	SetStarred(ctx context.Context, id int64, starred bool) error
	SetArchived(ctx context.Context, id int64, archived bool) error
	SaveAsNote(ctx context.Context, id int64) (path, route string, err error)
	NoteStatus(ctx context.Context, id int64) (NoteStatusResult, error)
	SaveURL(ctx context.Context, url, title string) (engine.Summary, error)
	Counts(ctx context.Context) (Counts, error)
	SetNotesToken(ctx context.Context, token string) error
}

func registerRead(r *Router, s ReadService) {
	session := []CallerKind{CallerLocal, CallerSession}
	local := []CallerKind{CallerLocal}

	r.handle("status", session, func(ctx context.Context, _ jsontext.Value) (any, error) {
		return s.Status(ctx)
	})
	r.handle("sources.list", session, func(ctx context.Context, _ jsontext.Value) (any, error) {
		sources, err := s.Sources(ctx)
		if err != nil {
			return nil, err
		}
		return SourceList{Sources: sources}, nil
	})
	r.handle("sources.add", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p SourceAddParams
		if err := decodeParams(raw, &p, []string{"url"}); err != nil {
			return nil, err
		}
		src, err := s.AddSource(ctx, p.URL)
		if err != nil {
			return nil, err
		}
		return SourceResult{Source: src}, nil
	})
	r.handle("sources.remove", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p SourceIDParams
		if err := decodeParams(raw, &p, []string{"id"}); err != nil {
			return nil, err
		}
		return nil, s.RemoveSource(ctx, p.ID)
	})
	r.handle("sources.rename", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p SourceRenameParams
		if err := decodeParams(raw, &p, []string{"id", "title"}); err != nil {
			return nil, err
		}
		return nil, s.RenameSource(ctx, p.ID, p.Title)
	})
	r.handle("sources.refresh", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p SourceRefreshParams
		if err := decodeParams(raw, &p, nil); err != nil {
			return nil, err
		}
		if p.ID == 0 {
			return nil, s.Refresh(ctx, nil)
		}
		return nil, s.Refresh(ctx, &p.ID)
	})
	r.handle("opml.import", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p OPMLImportParams
		if err := decodeParams(raw, &p, []string{"xml"}); err != nil {
			return nil, err
		}
		added, skipped, err := s.ImportOPML(ctx, p.XML)
		if err != nil {
			return nil, err
		}
		return OPMLImport{Added: added, Skipped: skipped}, nil
	})
	r.handle("opml.export", session, func(ctx context.Context, _ jsontext.Value) (any, error) {
		xml, err := s.ExportOPML(ctx)
		if err != nil {
			return nil, err
		}
		return OPMLDocument{XML: xml}, nil
	})
	r.handle("items.list", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemsListParams
		if err := decodeParams(raw, &p, []string{"view"}); err != nil {
			return nil, err
		}
		return s.ListItems(ctx, p)
	})
	r.handle("items.get", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemParams
		if err := decodeParams(raw, &p, []string{"id"}); err != nil {
			return nil, err
		}
		return s.GetItem(ctx, p.ID)
	})
	r.handle("items.search", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemsSearchParams
		if err := decodeParams(raw, &p, []string{"query"}); err != nil {
			return nil, err
		}
		return s.Search(ctx, p.Query, p.Limit)
	})
	r.handle("items.setRead", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemsSetReadParams
		if err := decodeParams(raw, &p, []string{"ids", "read"}); err != nil {
			return nil, err
		}
		return nil, s.SetRead(ctx, p.IDs, p.Read)
	})
	r.handle("items.markAllRead", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemsMarkAllParams
		if err := decodeParams(raw, &p, nil); err != nil {
			return nil, err
		}
		var sourceID *int64
		if p.SourceID != 0 {
			sourceID = &p.SourceID
		}
		return nil, s.MarkAllRead(ctx, p.View, sourceID, p.Before)
	})
	r.handle("items.setStarred", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemStarParams
		if err := decodeParams(raw, &p, []string{"id", "starred"}); err != nil {
			return nil, err
		}
		return nil, s.SetStarred(ctx, p.ID, p.Starred)
	})
	r.handle("items.setArchived", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemArchiveParams
		if err := decodeParams(raw, &p, []string{"id", "archived"}); err != nil {
			return nil, err
		}
		return nil, s.SetArchived(ctx, p.ID, p.Archived)
	})
	r.handle("items.saveAsNote", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p ItemParams
		if err := decodeParams(raw, &p, []string{"id"}); err != nil {
			return nil, err
		}
		path, route, err := s.SaveAsNote(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return NoteResult{Path: path, Route: route}, nil
	})
	r.handle("items.noteStatus", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p NoteStatusParams
		if err := decodeParams(raw, &p, []string{"id"}); err != nil {
			return nil, err
		}
		return s.NoteStatus(ctx, p.ID)
	})
	r.handle("later.save", session, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p SaveParams
		if err := decodeParams(raw, &p, []string{"url"}); err != nil {
			return nil, err
		}
		it, err := s.SaveURL(ctx, p.URL, p.Title)
		if err != nil {
			return nil, err
		}
		return ItemResult{Item: it}, nil
	})
	r.handle("counts", session, func(ctx context.Context, _ jsontext.Value) (any, error) {
		return s.Counts(ctx)
	})
	r.handle("notes.setToken", local, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p NotesTokenParams
		if err := decodeParams(raw, &p, []string{"token"}); err != nil {
			return nil, err
		}
		return nil, s.SetNotesToken(ctx, p.Token)
	})
}

// ReadClient calls the read methods; it implements ReadService.
type ReadClient struct{ c *Client }

// Read returns the read methods.
func (c *Client) Read() ReadClient { return ReadClient{c} }

var _ ReadService = ReadClient{}

// Status calls status.
func (x ReadClient) Status(ctx context.Context) (engine.Status, error) {
	var out engine.Status
	err := x.c.Call(ctx, "status", nil, &out)
	return out, err
}

// Sources calls sources.list.
func (x ReadClient) Sources(ctx context.Context) ([]Source, error) {
	var out SourceList
	if err := x.c.Call(ctx, "sources.list", nil, &out); err != nil {
		return nil, err
	}
	return out.Sources, nil
}

// AddSource calls sources.add.
func (x ReadClient) AddSource(ctx context.Context, url string) (Source, error) {
	var out SourceResult
	err := x.c.Call(ctx, "sources.add", SourceAddParams{URL: url}, &out)
	return out.Source, err
}

// RemoveSource calls sources.remove.
func (x ReadClient) RemoveSource(ctx context.Context, id int64) error {
	return x.c.Call(ctx, "sources.remove", SourceIDParams{ID: id}, nil)
}

// RenameSource calls sources.rename.
func (x ReadClient) RenameSource(ctx context.Context, id int64, title string) error {
	return x.c.Call(ctx, "sources.rename", SourceRenameParams{ID: id, Title: title}, nil)
}

// Refresh calls sources.refresh; a 0 id polls every source.
func (x ReadClient) Refresh(ctx context.Context, id *int64) error {
	var p SourceRefreshParams
	if id != nil {
		p.ID = *id
	}
	return x.c.Call(ctx, "sources.refresh", p, nil)
}

// ImportOPML calls opml.import.
func (x ReadClient) ImportOPML(ctx context.Context, document string) (added, skipped int, err error) {
	var out OPMLImport
	err = x.c.Call(ctx, "opml.import", OPMLImportParams{XML: document}, &out)
	return out.Added, out.Skipped, err
}

// ExportOPML calls opml.export.
func (x ReadClient) ExportOPML(ctx context.Context) (string, error) {
	var out OPMLDocument
	err := x.c.Call(ctx, "opml.export", nil, &out)
	return out.XML, err
}

// ListItems calls items.list.
func (x ReadClient) ListItems(ctx context.Context, p ItemsListParams) (ItemsPage, error) {
	var out ItemsPage
	err := x.c.Call(ctx, "items.list", p, &out)
	return out, err
}

// GetItem calls items.get.
func (x ReadClient) GetItem(ctx context.Context, id int64) (engine.Detail, error) {
	var out engine.Detail
	err := x.c.Call(ctx, "items.get", ItemParams{ID: id}, &out)
	return out, err
}

// Search calls items.search.
func (x ReadClient) Search(ctx context.Context, query string, limit int) (ItemsPage, error) {
	var out ItemsPage
	err := x.c.Call(ctx, "items.search", ItemsSearchParams{Query: query, Limit: limit}, &out)
	return out, err
}

// SetRead calls items.setRead.
func (x ReadClient) SetRead(ctx context.Context, ids []int64, read bool) error {
	return x.c.Call(ctx, "items.setRead", ItemsSetReadParams{IDs: ids, Read: read}, nil)
}

// MarkAllRead calls items.markAllRead.
func (x ReadClient) MarkAllRead(ctx context.Context, view string, sourceID *int64, before time.Time) error {
	var p ItemsMarkAllParams
	p.View = view
	if sourceID != nil {
		p.SourceID = *sourceID
	}
	p.Before = before
	return x.c.Call(ctx, "items.markAllRead", p, nil)
}

// SetStarred calls items.setStarred.
func (x ReadClient) SetStarred(ctx context.Context, id int64, starred bool) error {
	return x.c.Call(ctx, "items.setStarred", ItemStarParams{ID: id, Starred: starred}, nil)
}

// SetArchived calls items.setArchived.
func (x ReadClient) SetArchived(ctx context.Context, id int64, archived bool) error {
	return x.c.Call(ctx, "items.setArchived", ItemArchiveParams{ID: id, Archived: archived}, nil)
}

// SaveAsNote calls items.saveAsNote.
func (x ReadClient) SaveAsNote(ctx context.Context, id int64) (path, route string, err error) {
	var out NoteResult
	err = x.c.Call(ctx, "items.saveAsNote", ItemParams{ID: id}, &out)
	return out.Path, out.Route, err
}

// NoteStatus calls items.noteStatus.
func (x ReadClient) NoteStatus(ctx context.Context, id int64) (NoteStatusResult, error) {
	var out NoteStatusResult
	err := x.c.Call(ctx, "items.noteStatus", NoteStatusParams{ID: id}, &out)
	return out, err
}

// SaveURL calls later.save, with the title when there is one.
func (x ReadClient) SaveURL(ctx context.Context, url, title string) (engine.Summary, error) {
	var out ItemResult
	err := x.c.Call(ctx, "later.save", SaveParams{URL: url, Title: title}, &out)
	return out.Item, err
}

// Counts calls counts.
func (x ReadClient) Counts(ctx context.Context) (Counts, error) {
	var out Counts
	err := x.c.Call(ctx, "counts", nil, &out)
	return out, err
}

// SetNotesToken calls notes.setToken.
func (x ReadClient) SetNotesToken(ctx context.Context, token string) error {
	return x.c.Call(ctx, "notes.setToken", NotesTokenParams{Token: token}, nil)
}
