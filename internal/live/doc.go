// Package live holds the checks that run against a running daemon: lodge's
// development build with Read and Notes in it (make dev-lodge), and real
// tokens. Its tests carry the "live" build tag, so no gate runs them, and
// they read every token from the environment: no token is ever printed or
// committed.
package live
