package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frostyard/frostread/internal/fetch"
	"github.com/frostyard/frostread/internal/store"
)

// The extraction queue's bounds.
const (
	// ExtractWorkers is how many extractions run at once.
	ExtractWorkers = 2
	// SweepEvery is how often a worker looks for work a lost enqueue left.
	SweepEvery = 30 * time.Second
	// FullArticle is the feed text length past which a feed's own content
	// is the article, so no page is fetched.
	FullArticle = 600
)

// Enqueue asks for an item's extraction. A full queue drops the request;
// a worker's sweep finds the item anyway.
func (e *Engine) Enqueue(id int64) {
	select {
	case e.queue <- id:
	default:
		e.log.Debug("the extraction queue is full", "item", id)
	}
}

// RunExtraction extracts queued items until ctx ends, on n workers (2 when
// n is 0). Each worker also sweeps the store, so an item whose enqueue was
// dropped is not lost.
func (e *Engine) RunExtraction(ctx context.Context, n int) {
	if n <= 0 {
		n = ExtractWorkers
	}
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { e.worker(ctx) })
	}
	wg.Wait()
}

// worker takes items from the queue, and the store, until ctx ends.
func (e *Engine) worker(ctx context.Context) {
	ticker := time.NewTicker(SweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-e.queue:
			e.claim(ctx, id)
		case <-ticker.C:
			ids, err := e.o.Store.PendingExtraction(ctx, 16)
			if err != nil {
				e.log.Warn("look for pending extractions", "err", err)
				continue
			}
			for _, id := range ids {
				e.claim(ctx, id)
			}
		}
	}
}

// claim extracts an item once, whichever worker takes it.
func (e *Engine) claim(ctx context.Context, id int64) {
	if _, busy := e.busy.LoadOrStore(id, struct{}{}); busy {
		return
	}
	defer e.busy.Delete(id)
	if err := e.ExtractItem(ctx, id); err != nil {
		e.log.Warn("an item did not extract", "item", id, "err", err)
	}
}

// ExtractItem extracts one item, stores its images and records the
// result. It is idempotent: an item that is done is left alone.
func (e *Engine) ExtractItem(ctx context.Context, id int64) error {
	it, err := e.o.Store.Item(ctx, id)
	if err != nil {
		return err
	}
	if it.ExtractState != store.ExtractPending {
		return nil
	}
	// A feed that already gave the whole article needs no page: its
	// content is the article, and only its images need fetching.
	feedContent := it.ContentHTML
	if feedContent == "" {
		feedContent = it.SummaryHTML
	}
	if feedContent != "" && len([]rune(it.Text)) >= FullArticle {
		content, images, err := e.o.Extractor.Inline(ctx, feedContent, it.URL, it.Title)
		if err != nil {
			return e.extractFailed(ctx, id, err)
		}
		if err := e.o.Store.SetExtraction(ctx, id, store.Extraction{
			ContentHTML: content, State: store.ExtractDone, Images: images,
		}); err != nil {
			return err
		}
		e.change("unread", "later", "all")
		return nil
	}
	if it.URL == "" {
		return e.extractFailed(ctx, id, errors.New("the item has no address"))
	}
	res, err := e.o.Fetch.Get(ctx, it.URL, fetch.Options{
		Accept: "text/html, application/xhtml+xml, */*;q=0.5", MaxBytes: fetch.MaxPage,
	})
	if err != nil {
		return e.extractFailed(ctx, id, err)
	}
	if res.Status >= 400 {
		return e.extractFailed(ctx, id, fmt.Errorf("the page answers %d", res.Status))
	}
	// The original is kept as sent, before anything changes it.
	hash, err := e.o.Blobs.Put(ctx, bytes.NewReader(res.Body), "text/html")
	if err != nil {
		return e.extractFailed(ctx, id, err)
	}
	if err := e.o.Store.SetOriginal(ctx, id, hash); err != nil {
		return err
	}
	art, err := e.o.Extractor.Extract(ctx, res.Body, res.URL)
	if err != nil {
		return e.extractFailed(ctx, id, err)
	}
	if err := e.o.Store.SetExtraction(ctx, id, store.Extraction{
		Title: art.Title, Author: art.Byline, Text: art.Text, ContentHTML: art.ContentHTML,
		Published: art.Published, State: store.ExtractDone, Images: art.Images,
	}); err != nil {
		return err
	}
	e.log.Info("extracted", "item", id, "host", host(it.URL), "images", len(art.Images))
	e.change("unread", "later", "all")
	return nil
}

// extractFailed records why an extraction failed.
func (e *Engine) extractFailed(ctx context.Context, id int64, cause error) error {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if err := e.o.Store.SetExtraction(ctx, id, store.Extraction{State: store.ExtractFailed, Error: msg}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
