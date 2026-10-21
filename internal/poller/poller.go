// Package poller runs the poll loop: every minute it asks the engine to
// poll the sources whose time has come (docs/design/overview.md). The
// engine owns what a poll does; this package owns only when.
package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Every is how often the loop looks for due sources.
const Every = time.Minute

// Poller polls due sources on a timer.
type Poller struct {
	// Poll polls the due sources, and returns how many it polled.
	Poll func(ctx context.Context) (int, error)
	// Every is the loop's period; Every when 0.
	Every time.Duration
	// Log receives a poll's failure. Nil discards it.
	Log *slog.Logger

	mu     sync.Mutex
	polled int
}

// Run polls once at once, then every Every, until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	log := p.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	every := p.Every
	if every <= 0 {
		every = Every
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		p.poll(ctx, log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// poll runs one pass.
func (p *Poller) poll(ctx context.Context, log *slog.Logger) {
	n, err := p.Poll(ctx)
	if err != nil {
		log.Warn("a poll pass failed", "err", err)
		return
	}
	if n > 0 {
		log.Debug("polled", "sources", n)
	}
	p.mu.Lock()
	p.polled += n
	p.mu.Unlock()
}

// Polled is how many sources this poller has polled, for tests and logs.
func (p *Poller) Polled() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.polled
}
