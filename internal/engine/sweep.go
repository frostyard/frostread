package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/frostyard/frostread/internal/config"
)

// The blob sweep's schedule (docs/adr/0007-*).
const (
	// BlobSweepDelay is how long after it starts readd waits for the first
	// sweep, so a start is not slowed by a walk of the blob store.
	BlobSweepDelay = 10 * time.Minute
	// BlobSweepEvery is the time between sweeps.
	BlobSweepEvery = 24 * time.Hour
)

// SweepResult is what a sweep found. A dry run reports what a real sweep
// would remove now.
type SweepResult struct {
	// DryRun is true when nothing was removed or written.
	DryRun bool `json:"dryRun"`
	// Scanned is the number of blob files found.
	Scanned int `json:"scanned"`
	// InUse is the number of blobs some row names.
	InUse int `json:"inUse"`
	// Waiting is the number of unreferenced blobs inside their grace period.
	Waiting int `json:"waiting"`
	// Removed is the number of blobs removed, or in a dry run, that a real
	// sweep would remove now.
	Removed int `json:"removed"`
	// RemovedBytes is the size of the removed blobs.
	RemovedBytes int64 `json:"removedBytes"`
}

// sweepFile is one blob file the scan found outside the mark.
type sweepFile struct {
	id   string
	size int64
	mod  time.Time
}

// grace is the blob grace period.
func (e *Engine) grace() time.Duration {
	days := e.o.Config.BlobGraceDays
	if days <= 0 {
		days = config.DefaultGraceDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// SweepBlobs removes the blobs that no row has named for the grace period:
// it marks the hashes the database names, scans the blob files, records
// when each unreferenced file was first found, and removes the files whose
// record and modification time are both past the grace period. Under the
// blob store's lock it checks both again. A dry run writes and removes
// nothing. One sweep runs at a time. A removal that fails is logged and the
// sweep goes on; the mark, the scan or the record failing is an error.
func (e *Engine) SweepBlobs(ctx context.Context, dryRun bool) (SweepResult, error) {
	e.sweepMu.Lock()
	defer e.sweepMu.Unlock()
	started := e.now()
	cutoff := started.Add(-e.grace())
	res := SweepResult{DryRun: dryRun}

	// Mark.
	referenced, err := e.o.Store.ReferencedBlobs(ctx)
	if err != nil {
		return res, fmt.Errorf("sweep blobs: %w", err)
	}
	// Scan.
	var orphans []sweepFile
	onDisk := map[string]struct{}{}
	err = e.o.Blobs.List(func(id string, size int64, mod time.Time) error {
		res.Scanned++
		onDisk[id] = struct{}{}
		if _, ok := referenced[id]; ok {
			res.InUse++
			return nil
		}
		orphans = append(orphans, sweepFile{id: id, size: size, mod: mod})
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("sweep blobs: %w", err)
	}
	// Record. A dry run reads the rows and writes none.
	since, err := e.o.Store.BlobOrphans(ctx)
	if err != nil {
		return res, fmt.Errorf("sweep blobs: %w", err)
	}
	var found, stale []string
	for _, f := range orphans {
		if _, ok := since[f.id]; !ok {
			found = append(found, f.id)
		}
	}
	for h := range since {
		_, present := onDisk[h]
		_, used := referenced[h]
		if !present || used {
			stale = append(stale, h)
			delete(since, h)
		}
	}
	if !dryRun {
		if err := e.o.Store.RecordBlobOrphans(ctx, found, started, stale); err != nil {
			return res, fmt.Errorf("sweep blobs: %w", err)
		}
		for _, h := range found {
			since[h] = started
		}
	}
	if e.sweepHook != nil {
		e.sweepHook()
	}
	// Remove.
	for _, f := range orphans {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("sweep blobs: %w", err)
		}
		first, seen := since[f.id]
		if !seen || first.After(cutoff) || f.mod.After(cutoff) {
			res.Waiting++
			continue
		}
		if dryRun {
			e.log.Info("would remove an orphaned blob", "hash", f.id, "bytes", f.size, "since", first)
			res.Removed++
			res.RemovedBytes += f.size
			continue
		}
		e.removeBlob(ctx, f, first, cutoff, &res)
	}
	e.log.Info("blob sweep done", "dryRun", dryRun, "scanned", res.Scanned, "inUse", res.InUse,
		"waiting", res.Waiting, "removed", res.Removed, "bytes", res.RemovedBytes,
		"duration", e.now().Sub(started))
	return res, nil
}

// removeBlob removes one orphan, when the checks under the blob lock still
// say so, and forgets its rows.
func (e *Engine) removeBlob(ctx context.Context, f sweepFile, since, cutoff time.Time, res *SweepResult) {
	var inUse bool
	removed, size, err := e.o.Blobs.RemoveIf(f.id, func(mod time.Time) (bool, error) {
		if mod.After(cutoff) {
			return false, nil // a Put touched it
		}
		used, err := e.o.Store.BlobReferenced(ctx, f.id)
		if err != nil {
			return false, err
		}
		inUse = used
		return !used, nil
	})
	switch {
	case err != nil:
		e.log.Warn("an orphaned blob was not removed", "hash", f.id, "err", err)
		return
	case inUse:
		res.InUse++
		return
	case !removed:
		res.Waiting++ // a Put touched it, or its file was already gone
		return
	}
	if err := e.o.Store.ForgetBlob(ctx, f.id); err != nil {
		e.log.Warn("a removed blob's rows were not deleted", "hash", f.id, "err", err)
	}
	e.log.Info("removed an orphaned blob", "hash", f.id, "bytes", size, "since", since)
	res.Removed++
	res.RemovedBytes += size
}

// RunBlobSweep sweeps BlobSweepDelay after it starts, then every
// BlobSweepEvery, until ctx ends. A dry-run mode only logs, and the mode
// off returns at once.
func (e *Engine) RunBlobSweep(ctx context.Context) {
	mode := e.o.Config.BlobSweep
	if mode == config.SweepOff {
		return
	}
	dryRun := mode == config.SweepDryRun
	wait := BlobSweepDelay
	if e.sweepFirst > 0 {
		wait = e.sweepFirst
	}
	every := BlobSweepEvery
	if e.sweepEvery > 0 {
		every = e.sweepEvery
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := e.SweepBlobs(ctx, dryRun); err != nil && ctx.Err() == nil {
			e.log.Error("the blob sweep failed", "err", err)
		}
		timer.Reset(every)
	}
}
