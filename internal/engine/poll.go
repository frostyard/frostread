package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/extract"
	"github.com/frostyard/frostread/internal/feed"
	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/sanitize"
	"github.com/frostyard/frostread/internal/store"
)

// The poller's bounds.
const (
	// PollBatch is the most sources one pass takes.
	PollBatch = 4
	// MaxBackoff is the longest a failed source waits.
	MaxBackoff = 24 * time.Hour
)

// PollDue polls the sources whose next poll has come, at most PollBatch at
// a time, and returns how many it polled. An error on one source does not
// stop the others.
func (e *Engine) PollDue(ctx context.Context) (int, error) {
	sources, err := e.o.Store.DueSources(ctx, e.now(), PollBatch)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Go(func() {
			if err := e.pollSource(ctx, src); err != nil {
				e.log.Warn("a source did not poll", "source", src.ID, "host", host(src.URL), "err", err)
			}
		})
	}
	wg.Wait()
	return len(sources), nil
}

// Refresh polls one source now, or every source when id is nil.
func (e *Engine) Refresh(ctx context.Context, id *int64) error {
	if id != nil {
		src, err := e.o.Store.Source(ctx, *id)
		if err != nil {
			return err
		}
		return e.pollSource(ctx, src)
	}
	sources, err := e.o.Store.ListSources(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, PollBatch)
	var mu sync.Mutex
	var first error
	for _, src := range sources {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := e.pollSource(ctx, src); err != nil {
				e.log.Warn("a source did not poll", "source", src.ID, "host", host(src.URL), "err", err)
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if first != nil && len(sources) == 1 {
		return first
	}
	return nil
}

// pollSource runs one source's poll: a conditional GET, a parse, the new
// items, and the next poll's time. The error it returns is also stored on
// the source.
func (e *Engine) pollSource(ctx context.Context, src store.Source) error {
	res, err := e.o.Fetch.Get(ctx, src.URL, fetch.Options{
		ETag: src.ETag, LastModified: src.LastModified,
		Accept:   "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml, */*;q=0.5",
		MaxBytes: fetch.MaxFeed,
	})
	if err != nil {
		return e.pollFailed(ctx, src, err, 0)
	}
	if res.Status == 304 || res.NotModified {
		return e.polled(ctx, src, res, nil)
	}
	if res.Status >= 400 {
		return e.pollFailed(ctx, src, fmt.Errorf("%s answers %d", host(src.URL), res.Status), res.RetryAfter)
	}
	f, err := feed.Parse(res.Body, res.URL)
	if err != nil {
		return e.pollFailed(ctx, src, err, 0)
	}
	return e.polled(ctx, src, res, f.Items)
}

// polled stores a successful poll's new items and its next time.
func (e *Engine) polled(ctx context.Context, src store.Source, res *fetch.Response, items []feed.Item) error {
	newItems := make([]store.NewItem, 0, len(items))
	for _, it := range items {
		// The feed's HTML is untrusted: it is sanitized before the store
		// holds it, and its bytes are kept as sent, by hash.
		// A feed's links and image sources are often relative; resolving
		// them first keeps its images, which the extraction fetches later.
		summary := sanitize.HTML(extract.Absolutize(it.SummaryHTML, it.URL))
		content := sanitize.HTML(extract.Absolutize(it.ContentHTML, it.URL))
		// A feed may give an empty document as its content: store no HTML
		// when there are no words in it.
		if sanitize.PlainText(summary) == "" {
			summary = ""
		}
		if sanitize.PlainText(content) == "" {
			content = ""
		}
		newItems = append(newItems, store.NewItem{
			GUID: it.GUID, URL: it.URL, Title: it.Title, Author: it.Author,
			Published: it.Published, SummaryHTML: summary, ContentHTML: content,
			Text:         extract.StripTitle(sanitize.PlainText(summary+" "+content), it.Title),
			OriginalHash: blobs.ID([]byte(it.SummaryHTML + it.ContentHTML)),
		})
	}
	added, err := e.o.Store.AddItems(ctx, src.ID, newItems)
	if err != nil {
		return e.pollFailed(ctx, src, err, 0)
	}
	next := e.nextPoll(e.now(), 0, 0)
	if err := e.o.Store.Polled(ctx, src.ID, res.ETag, res.LastModified, next, 0, ""); err != nil {
		return err
	}
	for _, id := range added {
		e.Enqueue(id)
	}
	if len(added) > 0 {
		e.log.Info("new items", "source", src.ID, "host", host(src.URL), "count", len(added))
		e.change("unread", "all")
	}
	return nil
}

// pollFailed stores a poll's error and schedules the next attempt.
func (e *Engine) pollFailed(ctx context.Context, src store.Source, cause error, retryAfter time.Duration) error {
	count := src.ErrorCount + 1
	next := e.nextPoll(e.now(), count, retryAfter)
	// The error's text is a status or a parse message, never an article.
	msg := cause.Error()
	if err := e.o.Store.Polled(ctx, src.ID, src.ETag, src.LastModified, next, count, msg); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// nextPoll is when a source polls again: the interval with ±10% jitter
// after a success, interval × 2^failures (at most 24 h) after that many
// failures, and a Retry-After when it is longer.
func (e *Engine) nextPoll(now time.Time, failures int, retryAfter time.Duration) time.Time {
	interval := e.o.Interval
	if interval <= 0 {
		interval = config.DefaultInterval
	}
	d := interval
	if failures > 0 {
		for range failures {
			d *= 2
			if d >= MaxBackoff {
				break
			}
		}
	}
	if d > MaxBackoff {
		d = MaxBackoff
	}
	// ±10%, so a hundred sources do not poll in one minute, and never
	// past the longest wait.
	jitter := time.Duration(float64(d) * (e.o.Rand()*0.2 - 0.1))
	next := now.Add(min(d+jitter, MaxBackoff))
	if retryAfter > next.Sub(now) {
		next = now.Add(min(retryAfter, MaxBackoff))
	}
	return next
}
