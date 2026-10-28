// What a generator writes from schema/rpc/daemon.yaml, written by hand
// until the app has one.

package api

import (
	"context"
	"encoding/json/jsontext"
)

// DaemonShutdownParams holds the params of daemon.shutdown: none.
type DaemonShutdownParams struct{}

// DaemonShutdownResult is daemon.shutdown's result: an empty object.
type DaemonShutdownResult struct{}

// DaemonService is the daemon's own life: a desktop readd stops when its
// window leaves local mode (ADR-0008).
type DaemonService interface {
	Shutdown(ctx context.Context) error
}

func registerDaemon(r *Router, s DaemonService) {
	r.handle("daemon.shutdown", []CallerKind{CallerLocal},
		func(ctx context.Context, raw jsontext.Value) (any, error) {
			var p DaemonShutdownParams
			if err := decodeParams(raw, &p, nil); err != nil {
				return nil, err
			}
			if err := s.Shutdown(ctx); err != nil {
				return nil, err
			}
			return DaemonShutdownResult{}, nil
		})
}

// DaemonClient calls the daemon methods; it implements DaemonService.
type DaemonClient struct{ c *Client }

// Daemon returns the daemon methods.
func (c *Client) Daemon() DaemonClient { return DaemonClient{c} }

var _ DaemonService = DaemonClient{}

// Shutdown calls daemon.shutdown.
func (x DaemonClient) Shutdown(ctx context.Context) error {
	return x.c.Call(ctx, "daemon.shutdown", nil, nil)
}
