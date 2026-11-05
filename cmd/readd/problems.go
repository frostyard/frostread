package main

import (
	"net/http"
	"strings"

	"github.com/frostyard/std/problems"

	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/engine"
)

// AppID is frostread's app ID in its problem records (frostyard/core
// docs/specs/problem-report.md, S8 app_problem): the Flatpak's ID.
const AppID = config.AppID

// The codes of readd's problem records: internal faults only, never a
// network, an authentication or a data fault (docs/specs/problem-records.md).
// The engine records the last three.
const (
	// CodeMigrateFailed: the store's migration failed at the start.
	CodeMigrateFailed = "store.migrate_failed"
	// CodeLockHeld: another readd holds the state directory on lodge's host.
	CodeLockHeld = "lock.held"
	// CodeExtractFailed: an extraction failed in readd, not on the network.
	CodeExtractFailed = engine.CodeExtractFailed
	// CodeRenderTooLarge: an article's page alone is over items.render's
	// bound.
	CodeRenderTooLarge = engine.CodeRenderTooLarge
	// CodeSweepFailed: the scheduled blob sweep failed.
	CodeSweepFailed = engine.CodeSweepFailed
)

// releaseVersion is the version a problem record carries: the build's
// version without 1 leading "v". The recorder decides whether it is a
// release: v0.5.0 gives 0.5.0, which records; dev, flatpak, a git
// describe such as v0.4.0-3-gabc1234, and a snapshot such as
// 0.0.0-abc1234-snapshot give a value that is not a release semver, and
// the recorder is inert.
func releaseVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// recoverHandler records a panic of an HTTP handler, and then panics again
// with the same value, so net/http handles the panic as before.
type recoverHandler struct {
	next http.Handler
	rec  *problems.Recorder
}

func (h recoverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer h.rec.RecoverPanic()
	h.next.ServeHTTP(w, r)
}

// recovering wraps next in a recoverHandler; a nil next stays nil, so
// lodgeapp.Server still sees that the route is not served.
func recovering(next http.Handler, rec *problems.Recorder) http.Handler {
	if next == nil {
		return nil
	}
	return recoverHandler{next: next, rec: rec}
}
